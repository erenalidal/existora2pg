package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/erenalidal/existora2pg/internal/oracle"
	"github.com/erenalidal/existora2pg/internal/schema"
	"github.com/erenalidal/existora2pg/internal/transform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Writer handles PostgreSQL DDL execution and data loading via COPY.
type Writer struct {
	pool     *pgxpool.Pool
	pgSchema string
	logger   *slog.Logger
	unlogged bool         // create tables as UNLOGGED for faster bulk load
	naming   NamingConfig // naming convention for table/column names
}

// NewWriter creates a new PostgreSQL writer.
func NewWriter(pool *pgxpool.Pool, pgSchema string, logger *slog.Logger) *Writer {
	return &Writer{pool: pool, pgSchema: pgSchema, logger: logger, naming: DefaultNaming()}
}

// SetUnlogged enables UNLOGGED table creation mode.
func (w *Writer) SetUnlogged(enabled bool) {
	w.unlogged = enabled
}

// SetNaming sets the naming convention for table/column name conversion.
func (w *Writer) SetNaming(nc NamingConfig) {
	if nc.Convention == "" {
		nc.Convention = "lowercase"
	}
	w.naming = nc
}

// ExecDDL executes a DDL statement.
func (w *Writer) ExecDDL(ctx context.Context, ddl string) error {
	w.logger.Debug("executing DDL", "ddl", truncate(ddl, 200))
	_, err := w.pool.Exec(ctx, ddl)
	return err
}

// ExecDDLReturn executes a DDL statement and returns the command tag.
func (w *Writer) ExecDDLReturn(ctx context.Context, ddl string) (int64, error) {
	w.logger.Debug("executing DDL", "ddl", truncate(ddl, 200))
	ct, err := w.pool.Exec(ctx, ddl)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// QualifiedName returns the schema-qualified table name with naming convention applied.
func (w *Writer) QualifiedName(name string) string {
	return qualifiedName(w.pgSchema, w.naming.convertTableName(name))
}

// CreateTable creates a table and its partitions.
func (w *Writer) CreateTable(ctx context.Context, t schema.Table, dropIfExists bool) error {
	tableName := qualifiedName(w.pgSchema, w.naming.convertTableName(t.Name))

	if dropIfExists {
		if err := w.ExecDDL(ctx, GenerateDropTableWithNaming(t.Name, w.pgSchema, w.naming)); err != nil {
			return fmt.Errorf("drop table %s: %w", tableName, err)
		}
	}

	var ddl string
	if w.unlogged {
		ddl = GenerateCreateTableUnlogged(t, w.pgSchema, w.naming)
	} else {
		ddl = GenerateCreateTableWithNaming(t, w.pgSchema, w.naming)
	}
	if err := w.ExecDDL(ctx, ddl); err != nil {
		return fmt.Errorf("create table %s: %w", tableName, err)
	}

	w.logger.Info("table created", "table", tableName, "partitioned", t.IsPartitioned(), "unlogged", w.unlogged)
	return nil
}

// CreatePartitions creates partition child tables.
func (w *Writer) CreatePartitions(ctx context.Context, t schema.Table, bounds []PartitionBound) error {
	ddls := GeneratePartitionDDLWithNaming(t, w.pgSchema, bounds, w.naming)
	for _, ddl := range ddls {
		if err := w.ExecDDL(ctx, ddl); err != nil {
			return fmt.Errorf("create partition: %w", err)
		}
	}
	w.logger.Info("partitions created", "table", t.Name, "count", len(ddls))
	return nil
}

// CreateConstraintsAndIndexes creates PK, indexes, FK, and other constraints post-data.
func (w *Writer) CreateConstraintsAndIndexes(ctx context.Context, t schema.Table) error {
	// Primary key
	if pk := GeneratePrimaryKeyWithNaming(t, w.pgSchema, w.naming); pk != "" {
		if err := w.ExecDDL(ctx, pk); err != nil {
			return fmt.Errorf("create PK for %s: %w", t.Name, err)
		}
	}

	// Indexes
	for _, ddl := range GenerateIndexesWithNaming(t, w.pgSchema, w.naming) {
		if isCommentOnly(ddl) {
			w.logger.Info("index skipped", "table", t.Name, "reason", ddl)
			continue
		}
		if err := w.ExecDDL(ctx, ddl); err != nil {
			return fmt.Errorf("create index for %s: %w", t.Name, err)
		}
	}

	// Constraints (FK, UNIQUE, CHECK)
	for _, ddl := range GenerateConstraintsWithNaming(t, w.pgSchema, w.naming) {
		if err := w.ExecDDL(ctx, ddl); err != nil {
			return fmt.Errorf("create constraint for %s: %w", t.Name, err)
		}
	}

	return nil
}

// CreatePrimaryKey creates only the primary key constraint.
func (w *Writer) CreatePrimaryKey(ctx context.Context, t schema.Table) error {
	if pk := GeneratePrimaryKeyWithNaming(t, w.pgSchema, w.naming); pk != "" {
		if err := w.ExecDDL(ctx, pk); err != nil {
			if strings.Contains(err.Error(), "already exists") {
				w.logger.Info("PK already exists, skipping", "table", t.Name)
				return nil
			}
			return fmt.Errorf("create PK for %s: %w", t.Name, err)
		}
	}
	return nil
}

// CreateIndexesRaw creates only indexes (no PK, no constraints).
// indexMode: "create" (default), "concurrent" (CREATE INDEX CONCURRENTLY), "skip".
func (w *Writer) CreateIndexesRaw(ctx context.Context, t schema.Table, indexMode string) error {
	if indexMode == "skip" {
		w.logger.Info("indexes skipped by config", "table", t.Name)
		return nil
	}
	concurrent := indexMode == "concurrent"
	for _, ddl := range GenerateIndexesWithNaming(t, w.pgSchema, w.naming) {
		if isCommentOnly(ddl) {
			w.logger.Info("index skipped", "table", t.Name, "reason", ddl)
			continue
		}
		if concurrent {
			ddl = strings.Replace(ddl, "CREATE INDEX ", "CREATE INDEX CONCURRENTLY ", 1)
			ddl = strings.Replace(ddl, "CREATE UNIQUE INDEX ", "CREATE UNIQUE INDEX CONCURRENTLY ", 1)
		}
		if err := w.ExecDDL(ctx, ddl); err != nil {
			// Index already exists — safe to ignore for non-recreate modes
			if strings.Contains(err.Error(), "already exists") {
				w.logger.Info("index already exists, skipping", "table", t.Name)
				continue
			}
			return fmt.Errorf("create index for %s: %w", t.Name, err)
		}
	}
	return nil
}

// CreateFKAndOtherConstraints creates FK, UNIQUE, and CHECK constraints (not PK).
func (w *Writer) CreateFKAndOtherConstraints(ctx context.Context, t schema.Table) error {
	for _, ddl := range GenerateConstraintsWithNaming(t, w.pgSchema, w.naming) {
		if err := w.ExecDDL(ctx, ddl); err != nil {
			if strings.Contains(err.Error(), "already exists") {
				w.logger.Info("constraint already exists, skipping", "table", t.Name)
				continue
			}
			return fmt.Errorf("create constraint for %s: %w", t.Name, err)
		}
	}
	return nil
}

// CreateSequence creates a sequence and sets its current value.
func (w *Writer) CreateSequence(ctx context.Context, seq schema.Sequence) error {
	ddl := GenerateSequenceWithNaming(seq, w.pgSchema, w.naming)
	// Split into CREATE + setval
	for _, stmt := range strings.Split(ddl, "\n") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if err := w.ExecDDL(ctx, stmt); err != nil {
			return fmt.Errorf("create sequence %s: %w", seq.Name, err)
		}
	}
	return nil
}

// ConvertToLogged converts UNLOGGED tables back to LOGGED after data loading.
func (w *Writer) ConvertToLogged(ctx context.Context, tables []string) error {
	for _, t := range tables {
		tableName := qualifiedName(w.pgSchema, w.naming.convertTableName(t))
		ddl := fmt.Sprintf("ALTER TABLE %s SET LOGGED", tableName)
		if err := w.ExecDDL(ctx, ddl); err != nil {
			return fmt.Errorf("set logged %s: %w", tableName, err)
		}
		w.logger.Info("table converted to LOGGED", "table", tableName)
	}
	return nil
}

// TruncateTable truncates a table (keeps structure, deletes all data).
func (w *Writer) TruncateTable(ctx context.Context, tableName string) error {
	name := qualifiedName(w.pgSchema, w.naming.convertTableName(tableName))
	ddl := fmt.Sprintf("TRUNCATE TABLE %s", name)
	w.logger.Info("truncating table", "table", name)
	return w.ExecDDL(ctx, ddl)
}

// TruncatePartition truncates a specific partition (child table in PG).
func (w *Writer) TruncatePartition(ctx context.Context, parentTable, partitionName string) error {
	pgPartName := w.naming.convertTableName(parentTable) + "_" + w.naming.convertName(partitionName)
	// Check existence first — partition may not exist yet on first run
	schema := w.pgSchema
	if schema == "" || schema == "public" {
		schema = "public"
	}
	var exists bool
	if err := w.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2)`,
		schema, pgPartName).Scan(&exists); err != nil {
		return fmt.Errorf("check partition existence: %w", err)
	}
	if !exists {
		w.logger.Debug("partition does not exist, skipping truncate", "partition", pgPartName)
		return nil
	}
	name := qualifiedName(w.pgSchema, pgPartName)
	ddl := fmt.Sprintf("TRUNCATE TABLE %s", name)
	w.logger.Info("truncating partition", "partition", name)
	return w.ExecDDL(ctx, ddl)
}

// TableExists checks if a table exists in the target schema.
func (w *Writer) TableExists(ctx context.Context, tableName string) (bool, error) {
	schema := w.pgSchema
	if schema == "" || schema == "public" {
		schema = "public"
	}
	var exists bool
	err := w.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2)`,
		schema, w.naming.convertTableName(tableName)).Scan(&exists)
	return exists, err
}

// DeleteByFilter deletes rows matching a WHERE clause (for chunk retry cleanup).
func (w *Writer) DeleteByFilter(ctx context.Context, tableName, where string) error {
	name := qualifiedName(w.pgSchema, w.naming.convertTableName(tableName))
	ddl := fmt.Sprintf("DELETE FROM %s WHERE %s", name, where)
	w.logger.Debug("deleting rows by filter", "table", name)
	_, err := w.pool.Exec(ctx, ddl)
	return err
}

// CopyStats holds statistics for a COPY operation.
type CopyStats struct {
	RowsCopied    int64
	BytesCopied   int64
	Duration      time.Duration
	ReadTime      time.Duration
	WriteTime     time.Duration
	BatchCount    int
	AvgBatchWrite time.Duration
	AcquireWait   time.Duration // pool connection acquire wait time
	CommitWait    time.Duration // cumulative COMMIT wait time
}

// writeResult holds the final statistics from the writer goroutine.
type writeResult struct {
	totalRows  int64
	writeTime  time.Duration
	commitWait time.Duration
	batchCount int
	err        error
}

// CopyData streams rows from an Oracle reader channel into PostgreSQL using chunked COPY.
// Uses double-buffering: reader fills batches while writer flushes the previous batch to PG.
// Each chunk of batchSize rows is committed in its own transaction to bound WAL pressure.
//
// Concurrency contract:
//   - batchCh is closed exactly once, by the reader (main goroutine).
//   - Writer on error: signals via writerErrCh, then drains batchCh (so reader sends don't block).
//   - Reader on error/cancel: breaks loop, closes batchCh, waits writerDoneCh.
// CopyDataOpts holds optional parameters for CopyData.
type CopyDataOpts struct {
	MaxBatchBytes int64 // flush batch when cumulative byte estimate exceeds this (0 = no limit)
}

func (w *Writer) CopyData(ctx context.Context, t schema.Table, partition *schema.Partition,
	rowCh <-chan oracle.ReadResult, batchSize int, progressFn func(rows int64, interim *CopyStats), opts ...CopyDataOpts) (*CopyStats, error) {

	var maxBatchBytes int64
	if len(opts) > 0 {
		maxBatchBytes = opts[0].MaxBatchBytes
	}

	tableName := w.targetTable(t, partition)
	tableIdent := pgx.Identifier(w.targetTableIdent(t, partition))
	colNames := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		// pgx CopyFrom expects unquoted column names — it quotes internally
		colNames[i] = w.naming.convertName(c.Name)
		if w.naming.NameOverrides != nil {
			if override, ok := w.naming.NameOverrides[t.Name+"."+c.Name]; ok {
				colNames[i] = override
			}
		}
	}

	if batchSize <= 0 {
		batchSize = 50000
	}

	start := time.Now()
	var totalReadTime time.Duration

	const maxRetries = 3

	// Acquire initial PG connection with retry — transient network errors
	// (e.g. TCP timeout) should not immediately fail the entire pipeline.
	// Use a dedicated timeout to prevent indefinite blocking when pool is exhausted.
	var conn *pgxpool.Conn
	var acquireWait time.Duration
	acquireStart := time.Now()
	for attempt := 0; attempt <= maxRetries; attempt++ {
		var err error
		acquireCtx, acquireCancel := context.WithTimeout(ctx, 60*time.Second)
		conn, err = w.pool.Acquire(acquireCtx)
		acquireCancel()
		if err == nil {
			break
		}
		if attempt == maxRetries || ctx.Err() != nil {
			return nil, fmt.Errorf("acquire connection: %w", err)
		}
		delay := time.Duration(1<<attempt) * time.Second
		w.logger.Warn("acquire connection failed, retrying",
			"attempt", attempt+1,
			"delay", delay,
			"error", err)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, fmt.Errorf("acquire connection: %w", ctx.Err())
		}
	}
	acquireWait = time.Since(acquireStart)

	// Disable synchronous_commit for this session — bulk COPY doesn't need
	// per-TX fsync since we have retry + resume for crash recovery.
	// Skip for UNLOGGED tables since WAL is not written anyway.
	if !w.unlogged {
		if _, err := conn.Exec(ctx, "SET synchronous_commit = off"); err != nil {
			conn.Release()
			return nil, fmt.Errorf("set synchronous_commit: %w", err)
		}
	}

	// Double-buffer: UNLOGGED tables get larger buffer since writer is faster.
	bufCap := 2
	if w.unlogged {
		bufCap = 4
	}
	batchCh := make(chan [][]any, bufCap)
	writerErrCh := make(chan error, 1)   // writer signals failure (buffered: non-blocking send)
	writerDoneCh := make(chan writeResult, 1) // writer sends final stats when done

	// ── Writer goroutine ──
	// Owns conn exclusively. Reads from batchCh until closed.
	// On error: sends to writerErrCh, then drains batchCh to unblock reader.
	go func() {
		defer conn.Release()
		var wr writeResult

		for rows := range batchCh {
			var lastErr error
			flushed := false
			for attempt := 0; attempt <= maxRetries; attempt++ {
				if attempt > 0 {
					delay := time.Duration(1<<(attempt-1)) * time.Second
					w.logger.Warn("retrying COPY batch",
						"table", tableName,
						"attempt", attempt+1,
						"delay", delay,
						"error", lastErr)
					select {
					case <-time.After(delay):
					case <-ctx.Done():
						wr.err = ctx.Err()
						writerErrCh <- wr.err
						for range batchCh {
						}
						writerDoneCh <- wr
						return
					}
					conn.Release()
					acquireCtx, acquireCancel := context.WithTimeout(ctx, 60*time.Second)
					newConn, acqErr := w.pool.Acquire(acquireCtx)
					acquireCancel()
					if acqErr != nil {
						lastErr = fmt.Errorf("re-acquire connection: %w", acqErr)
						continue
					}
					if !w.unlogged {
						if _, setErr := newConn.Exec(ctx, "SET synchronous_commit = off"); setErr != nil {
							newConn.Release()
							lastErr = fmt.Errorf("set synchronous_commit on new conn: %w", setErr)
							continue
						}
					}
					conn = newConn
				}

				writeStart := time.Now()
				tx, txErr := conn.Begin(ctx)
				if txErr != nil {
					lastErr = fmt.Errorf("begin tx: %w", txErr)
					continue
				}
				n, copyErr := tx.CopyFrom(ctx, tableIdent, colNames, pgx.CopyFromRows(rows))
				if copyErr != nil {
					tx.Rollback(ctx)
					lastErr = fmt.Errorf("COPY to %s: %w", tableName, copyErr)
					continue
				}
				commitStart := time.Now()
				if commitErr := tx.Commit(ctx); commitErr != nil {
					lastErr = fmt.Errorf("commit: %w", commitErr)
					continue
				}
				wr.commitWait += time.Since(commitStart)
				wr.writeTime += time.Since(writeStart)
				wr.batchCount++
				wr.totalRows += n
				if progressFn != nil {
					interim := &CopyStats{
						RowsCopied:    wr.totalRows,
						WriteTime:     wr.writeTime,
						CommitWait:    wr.commitWait,
						BatchCount:    wr.batchCount,
						AcquireWait:   acquireWait,
						AvgBatchWrite: wr.writeTime / time.Duration(wr.batchCount),
					}
					progressFn(wr.totalRows, interim)
				}
				flushed = true
				break
			}
			if !flushed {
				wr.err = fmt.Errorf("COPY failed after %d retries: %w", maxRetries, lastErr)
				writerErrCh <- wr.err
				// Drain remaining batches so reader's sends don't block.
				// Reader will close batchCh shortly after seeing writerErrCh,
				// which terminates this range.
				for range batchCh {
				}
				writerDoneCh <- wr
				return
			}
		}
		writerDoneCh <- wr
	}()

	// ── Reader loop ──
	// Reads from Oracle rowCh, transforms inline, accumulates batches, sends to writer.
	// On any exit, closes batchCh (single close point) and waits for writer.
	batch := make([][]any, 0, batchSize)
	var batchBytes int64
	var readErr error
	var readDone bool // true if rowCh closed normally
	readStart := time.Now()

readLoop:
	for {
		select {
		case <-ctx.Done():
			readErr = ctx.Err()
			break readLoop

		case werr := <-writerErrCh:
			// Writer died while we were waiting for rowCh
			readErr = werr
			break readLoop

		case result, ok := <-rowCh:
			totalReadTime += time.Since(readStart)
			if !ok {
				readDone = true
				break readLoop
			}
			if result.Err != nil {
				readErr = result.Err
				break readLoop
			}

			// Inline transform — no separate goroutine needed
			transform.TransformRowInline(result.Row, t.Columns)
			batch = append(batch, result.Row)
			if maxBatchBytes > 0 {
				batchBytes += estimateRowBytes(result.Row)
			}

			if len(batch) >= batchSize || (maxBatchBytes > 0 && batchBytes >= maxBatchBytes) {
				select {
				case batchCh <- batch:
					batch = make([][]any, 0, batchSize)
					batchBytes = 0
				case werr := <-writerErrCh:
					readErr = werr
					break readLoop
				case <-ctx.Done():
					readErr = ctx.Err()
					break readLoop
				}
			}
			readStart = time.Now()
		}
	}

	// Send final partial batch if read completed normally
	if readDone && len(batch) > 0 {
		select {
		case batchCh <- batch:
		case werr := <-writerErrCh:
			if readErr == nil {
				readErr = werr
			}
		case <-ctx.Done():
			if readErr == nil {
				readErr = ctx.Err()
			}
		}
	}

	// Signal writer: no more batches. This is the ONLY close point.
	// Safe even if writer is draining — range over closed channel exits cleanly.
	close(batchCh)

	// Always wait for writer to finish and collect stats.
	wr := <-writerDoneCh

	if readErr != nil {
		return nil, fmt.Errorf("oracle read error during COPY: %w", readErr)
	}
	if wr.err != nil {
		return nil, wr.err
	}

	var avgBatchWrite time.Duration
	if wr.batchCount > 0 {
		avgBatchWrite = wr.writeTime / time.Duration(wr.batchCount)
	}

	stats := &CopyStats{
		RowsCopied:    wr.totalRows,
		Duration:      time.Since(start),
		ReadTime:      totalReadTime,
		WriteTime:     wr.writeTime,
		BatchCount:    wr.batchCount,
		AvgBatchWrite: avgBatchWrite,
		AcquireWait:   acquireWait,
		CommitWait:    wr.commitWait,
	}

	w.logger.Info("COPY completed",
		"table", tableName,
		"rows", wr.totalRows,
		"batches", wr.batchCount,
		"batch_size", batchSize,
		"duration", stats.Duration.Round(time.Millisecond),
		"rows_per_sec", rowsPerSec(wr.totalRows, stats.Duration),
		"read_time", stats.ReadTime.Round(time.Millisecond),
		"write_time", stats.WriteTime.Round(time.Millisecond),
		"read_pct", int(float64(stats.ReadTime)/float64(stats.Duration)*100),
		"write_pct", int(float64(stats.WriteTime)/float64(stats.Duration)*100),
		"note", "read+write overlap via double-buffering, pct may sum >100%",
		"avg_batch_write", avgBatchWrite.Round(time.Millisecond))

	return stats, nil
}

// Count returns the row count of a table.
func (w *Writer) Count(ctx context.Context, tableName string) (int64, error) {
	name := qualifiedName(w.pgSchema, w.naming.convertTableName(tableName))
	var count int64
	err := w.pool.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", name)).Scan(&count)
	return count, err
}

// EstimateCount returns an estimated row count from pg_class (instant, no table scan).
func (w *Writer) EstimateCount(ctx context.Context, tableName string) (int64, error) {
	schema := w.pgSchema
	if schema == "" || schema == "public" {
		schema = "public"
	}
	converted := w.naming.convertTableName(tableName)
	var estimate float64
	err := w.pool.QueryRow(ctx,
		`SELECT COALESCE(c.reltuples, 0)
		 FROM pg_class c
		 JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND c.relname = $2`,
		schema, converted).Scan(&estimate)
	if err != nil {
		return 0, err
	}
	// reltuples is 0 or -1 when ANALYZE hasn't run yet (e.g. after recreate + COPY).
	// Fall back to real COUNT(*) so the UI shows accurate numbers.
	if estimate <= 0 {
		var exact int64
		q := fmt.Sprintf(`SELECT COUNT(*) FROM %s.%s`,
			pgx.Identifier{schema}.Sanitize(),
			pgx.Identifier{converted}.Sanitize())
		if err := w.pool.QueryRow(ctx, q).Scan(&exact); err != nil {
			return 0, nil
		}
		return exact, nil
	}
	return int64(estimate), nil
}

// targetTableIdent returns unquoted identifier parts for pgx.Identifier.
// pgx handles quoting internally.
func (w *Writer) targetTableIdent(t schema.Table, partition *schema.Partition) []string {
	tblName := w.naming.convertTableName(t.Name)
	if partition != nil {
		tblName = tblName + "_" + w.naming.convertName(partition.Name)
	}
	if w.pgSchema != "" && w.pgSchema != "public" {
		return []string{w.pgSchema, tblName}
	}
	return []string{tblName}
}

// targetTable returns a quoted qualified table name for use in raw SQL (DDL, TRUNCATE, etc.).
func (w *Writer) targetTable(t schema.Table, partition *schema.Partition) string {
	if partition != nil {
		name := w.naming.convertTableName(t.Name) + "_" + w.naming.convertName(partition.Name)
		return qualifiedName(w.pgSchema, name)
	}
	return qualifiedName(w.pgSchema, w.naming.convertTableName(t.Name))
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// isCommentOnly returns true if the DDL string contains only SQL comments (no executable statements).
func isCommentOnly(ddl string) bool {
	for _, line := range strings.Split(ddl, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "--") {
			return false
		}
	}
	return true
}

// estimateRowBytes gives a rough byte estimate for a row (for batch byte limiting).
// Exact size is not needed — we just want to prevent 100MB+ batches.
func estimateRowBytes(row []any) int64 {
	var n int64
	for _, v := range row {
		switch val := v.(type) {
		case nil:
			n += 4
		case string:
			n += int64(len(val))
		case []byte:
			n += int64(len(val))
		default:
			n += 16 // numbers, timestamps, etc.
		}
	}
	return n
}

func rowsPerSec(rows int64, d time.Duration) int64 {
	if d == 0 {
		return 0
	}
	return int64(float64(rows) / d.Seconds())
}
