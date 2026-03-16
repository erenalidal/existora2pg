package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/erenalidal/existora2pg/internal/schema"
	"github.com/godror/godror"
)

// Reader streams rows from Oracle tables.
type Reader struct {
	db        *sql.DB
	fetchSize int
	logger    *slog.Logger
}

// NewReader creates a streaming row reader.
func NewReader(db *sql.DB, fetchSize int, logger *slog.Logger) *Reader {
	if fetchSize <= 0 {
		fetchSize = 10000
	}
	return &Reader{db: db, fetchSize: fetchSize, logger: logger}
}

// DB returns the underlying database connection (used by Chunker).
func (r *Reader) DB() *sql.DB {
	return r.db
}

// ReadResult is sent through the row channel.
type ReadResult struct {
	Row []any
	Err error
}

// Read streams all rows from a table (or partition) into a channel.
// The caller should read from the returned channel until it's closed.
// The optional where clause adds a filter condition.
// fetchSizeOverride overrides the default fetch size if > 0.
func (r *Reader) Read(ctx context.Context, table schema.Table, partition *schema.Partition, where string, fetchSizeOverride ...int) <-chan ReadResult {
	bufSize := r.fetchSize
	if len(fetchSizeOverride) > 0 && fetchSizeOverride[0] > 0 {
		bufSize = fetchSizeOverride[0]
	}
	ch := make(chan ReadResult, bufSize)

	go func() {
		defer close(ch)

		query := r.buildQuery(table, partition, where)
		pName := partitionName(partition)
		r.logger.Debug("starting read",
			"table", table.Name,
			"partition", pName,
			"query", query)

		queryStart := time.Now()
		// godror FetchArraySize: bulk-fetch rows from Oracle in arrays (huge perf win for large tables)
		rows, err := r.db.QueryContext(ctx, query,
			godror.FetchArraySize(bufSize),
			godror.PrefetchCount(bufSize),
		)
		if err != nil {
			ch <- ReadResult{Err: fmt.Errorf("query %s: %w", table.Name, err)}
			return
		}
		defer rows.Close()

		r.logger.Info("oracle query opened",
			"table", table.Name,
			"partition", pName,
			"query_time", time.Since(queryStart).Round(time.Millisecond),
			"fetch_array_size", bufSize)

		colTypes, err := rows.ColumnTypes()
		if err != nil {
			ch <- ReadResult{Err: fmt.Errorf("column types %s: %w", table.Name, err)}
			return
		}
		numCols := len(colTypes)
		ptrs := make([]any, numCols)

		for rows.Next() {
			vals := make([]any, numCols)
			for i := range vals {
				ptrs[i] = &vals[i]
			}

			if err := rows.Scan(ptrs...); err != nil {
				ch <- ReadResult{Err: fmt.Errorf("scan %s: %w", table.Name, err)}
				return
			}

			select {
			case ch <- ReadResult{Row: vals}:
			case <-ctx.Done():
				ch <- ReadResult{Err: ctx.Err()}
				return
			}
		}

		if err := rows.Err(); err != nil {
			ch <- ReadResult{Err: fmt.Errorf("rows iteration %s: %w", table.Name, err)}
		}
	}()

	return ch
}

// Count returns the row count for a table, optionally filtered.
func (r *Reader) Count(ctx context.Context, owner, tableName string, partition *schema.Partition, where string) (int64, error) {
	var query string
	if partition != nil {
		query = fmt.Sprintf("SELECT COUNT(*) FROM %s.%s PARTITION (%s)",
			owner, tableName, partition.Name)
	} else {
		query = fmt.Sprintf("SELECT COUNT(*) FROM %s.%s", owner, tableName)
	}

	if where != "" {
		query += " WHERE " + where
	}

	var count int64
	err := r.db.QueryRowContext(ctx, query).Scan(&count)
	return count, err
}

// EstimateCount returns estimated row count from Oracle stats (instant, no table scan).
// For partitions, uses ALL_TAB_PARTITIONS.NUM_ROWS.
func (r *Reader) EstimateCount(ctx context.Context, owner, tableName string, partition *schema.Partition) (int64, error) {
	var count int64
	if partition != nil {
		err := r.db.QueryRowContext(ctx,
			`SELECT NVL(num_rows, 0) FROM all_tab_partitions
			 WHERE table_owner = :1 AND table_name = :2 AND partition_name = :3`,
			owner, tableName, partition.Name).Scan(&count)
		return count, err
	}
	err := r.db.QueryRowContext(ctx,
		`SELECT NVL(num_rows, 0) FROM all_tables WHERE owner = :1 AND table_name = :2`,
		owner, tableName).Scan(&count)
	return count, err
}

func (r *Reader) buildQuery(table schema.Table, partition *schema.Partition, where string) string {
	cols := make([]string, len(table.Columns))
	for i, c := range table.Columns {
		cols[i] = c.Name
	}

	var source string
	if partition != nil {
		source = fmt.Sprintf("%s.%s PARTITION (%s)", table.Owner, table.Name, partition.Name)
	} else {
		source = fmt.Sprintf("%s.%s", table.Owner, table.Name)
	}

	query := fmt.Sprintf("SELECT %s FROM %s", strings.Join(cols, ", "), source)
	if where != "" {
		query += " WHERE " + where
	}

	return query
}

// ReadPaginated reads rows using OFFSET/FETCH pagination.
// Each batch is a separate query: SELECT ... ORDER BY ROWID OFFSET X ROWS FETCH NEXT Y ROWS ONLY.
// Compatible with Oracle 12c+. Universally works without any privileges.
// Slower than streaming cursor for large tables (O(n²) total I/O) but guarantees bounded memory.
func (r *Reader) ReadPaginated(ctx context.Context, table schema.Table, partition *schema.Partition, where string, pageSize int) <-chan ReadResult {
	if pageSize <= 0 {
		pageSize = r.fetchSize
	}
	ch := make(chan ReadResult, pageSize)

	go func() {
		defer close(ch)

		baseQuery := r.buildQuery(table, partition, where)
		pName := partitionName(partition)
		offset := 0
		batchNum := 0
		totalRows := 0

		for {
			batchNum++
			query := fmt.Sprintf("%s ORDER BY ROWID OFFSET %d ROWS FETCH NEXT %d ROWS ONLY",
				baseQuery, offset, pageSize)

			batchStart := time.Now()
			rows, err := r.db.QueryContext(ctx, query,
				godror.FetchArraySize(pageSize),
				godror.PrefetchCount(pageSize),
			)
			if err != nil {
				ch <- ReadResult{Err: fmt.Errorf("paginated query %s batch %d: %w", table.Name, batchNum, err)}
				return
			}

			colTypes, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				ch <- ReadResult{Err: fmt.Errorf("column types %s: %w", table.Name, err)}
				return
			}
			numCols := len(colTypes)
			ptrs := make([]any, numCols)

			rowsInBatch := 0
			for rows.Next() {
				vals := make([]any, numCols)
				for i := range vals {
					ptrs[i] = &vals[i]
				}

				if err := rows.Scan(ptrs...); err != nil {
					rows.Close()
					ch <- ReadResult{Err: fmt.Errorf("scan %s batch %d: %w", table.Name, batchNum, err)}
					return
				}

				select {
				case ch <- ReadResult{Row: vals}:
					rowsInBatch++
				case <-ctx.Done():
					rows.Close()
					ch <- ReadResult{Err: ctx.Err()}
					return
				}
			}

			if err := rows.Err(); err != nil {
				rows.Close()
				ch <- ReadResult{Err: fmt.Errorf("rows iteration %s batch %d: %w", table.Name, batchNum, err)}
				return
			}
			rows.Close()

			totalRows += rowsInBatch
			r.logger.Debug("paginated batch completed",
				"table", table.Name,
				"partition", pName,
				"batch", batchNum,
				"rows_in_batch", rowsInBatch,
				"total_rows", totalRows,
				"batch_time", time.Since(batchStart).Round(time.Millisecond))

			if rowsInBatch < pageSize {
				// Last batch — fewer rows than page size means no more data
				break
			}
			offset += pageSize
		}

		r.logger.Info("paginated read completed",
			"table", table.Name,
			"partition", pName,
			"total_rows", totalRows,
			"batches", batchNum)
	}()

	return ch
}

func partitionName(p *schema.Partition) string {
	if p == nil {
		return ""
	}
	return p.Name
}
