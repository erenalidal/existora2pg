package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
)

// RowIDChunk represents a ROWID-based range for parallel table reads.
type RowIDChunk struct {
	ID            int
	StartRID      string
	EndRID        string
	PartitionName string // populated for partitioned table chunks (mapped via DBMS_ROWID)
}

// Chunker splits tables into ROWID-based or ORA_HASH-based chunks for parallel reads.
type Chunker struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewChunker creates a ROWID chunker.
func NewChunker(db *sql.DB, logger *slog.Logger) *Chunker {
	return &Chunker{db: db, logger: logger}
}

// ComputeChunks splits a table into ROWID chunks.
// It first tries DBMS_PARALLEL_EXECUTE (efficient, uses data dictionary extents).
// Falls back to ORA_HASH-based virtual chunking if privileges are insufficient.
func (c *Chunker) ComputeChunks(ctx context.Context, owner, tableName string, numChunks int) ([]RowIDChunk, error) {
	if numChunks <= 1 {
		return nil, nil
	}

	chunks, err := c.execDBMSChunks(ctx, owner, tableName, numChunks, false)
	if err != nil {
		c.logger.Warn("DBMS_PARALLEL_EXECUTE unavailable, using ORA_HASH fallback",
			"table", tableName, "error", err)
		return chunksViaOraHash(numChunks), nil
	}

	c.logger.Info("ROWID chunks computed via DBMS_PARALLEL_EXECUTE",
		"table", tableName, "requested", numChunks, "actual", len(chunks))
	return chunks, nil
}

// ComputePartitionedChunks splits a partitioned table into ROWID chunks via DBMS_PARALLEL_EXECUTE
// and maps each chunk to its partition using DBMS_ROWID.
// Returns chunks grouped by partition name. Falls back to nil if DBMS_PE is unavailable.
func (c *Chunker) ComputePartitionedChunks(ctx context.Context, owner, tableName string, numChunks int) (map[string][]RowIDChunk, error) {
	if numChunks <= 1 {
		return nil, nil
	}

	chunks, err := c.execDBMSChunks(ctx, owner, tableName, numChunks, true)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]RowIDChunk)
	for _, ch := range chunks {
		result[ch.PartitionName] = append(result[ch.PartitionName], ch)
	}

	c.logger.Info("partitioned ROWID chunks computed",
		"table", tableName, "partitions", len(result), "total_chunks", len(chunks))
	return result, nil
}

// execDBMSChunks creates a DBMS_PARALLEL_EXECUTE task, splits a table by ROWID,
// reads chunk boundaries, and cleans up. If withPartitions is true, each chunk
// is mapped to its partition via DBMS_ROWID.ROWID_OBJECT.
func (c *Chunker) execDBMSChunks(ctx context.Context, owner, tableName string, numChunks int, withPartitions bool) ([]RowIDChunk, error) {
	taskName := fmt.Sprintf("E2PG_%s_%s", strings.ToUpper(owner), strings.ToUpper(tableName))
	if len(taskName) > 128 {
		taskName = taskName[:128]
	}

	// Drop task if it already exists (from a previous failed run)
	_, _ = c.db.ExecContext(ctx, fmt.Sprintf(
		"BEGIN DBMS_PARALLEL_EXECUTE.DROP_TASK('%s'); EXCEPTION WHEN OTHERS THEN NULL; END;", taskName))

	// Create task + ROWID chunks
	createSQL := fmt.Sprintf(`BEGIN
  DBMS_PARALLEL_EXECUTE.CREATE_TASK('%s');
  DBMS_PARALLEL_EXECUTE.CREATE_CHUNKS_BY_ROWID('%s', '%s', '%s', TRUE, %d);
END;`,
		taskName, taskName, strings.ToUpper(owner), strings.ToUpper(tableName), numChunks)

	if _, err := c.db.ExecContext(ctx, createSQL); err != nil {
		return nil, fmt.Errorf("create ROWID chunks: %w", err)
	}

	// Ensure cleanup
	defer func() {
		_, _ = c.db.ExecContext(ctx, fmt.Sprintf(
			"BEGIN DBMS_PARALLEL_EXECUTE.DROP_TASK('%s'); END;", taskName))
	}()

	// Build query — optionally join with all_objects to map chunks to partitions
	var query string
	if withPartitions {
		query = fmt.Sprintf(`
			SELECT c.chunk_id, c.start_rowid, c.end_rowid,
			       NVL(o.subobject_name, '') as partition_name
			FROM user_parallel_execute_chunks c
			LEFT JOIN all_objects o
			  ON o.data_object_id = DBMS_ROWID.ROWID_OBJECT(c.start_rowid)
			  AND o.owner = '%s'
			  AND o.object_name = '%s'
			WHERE c.task_name = '%s'
			ORDER BY c.chunk_id`,
			strings.ToUpper(owner), strings.ToUpper(tableName), taskName)
	} else {
		query = fmt.Sprintf(
			`SELECT chunk_id, start_rowid, end_rowid
			 FROM user_parallel_execute_chunks
			 WHERE task_name = '%s'
			 ORDER BY chunk_id`, taskName)
	}

	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read chunks: %w", err)
	}
	defer rows.Close()

	var chunks []RowIDChunk
	for rows.Next() {
		var ch RowIDChunk
		if withPartitions {
			if err := rows.Scan(&ch.ID, &ch.StartRID, &ch.EndRID, &ch.PartitionName); err != nil {
				return nil, fmt.Errorf("scan partitioned chunk: %w", err)
			}
		} else {
			if err := rows.Scan(&ch.ID, &ch.StartRID, &ch.EndRID); err != nil {
				return nil, fmt.Errorf("scan chunk: %w", err)
			}
		}
		chunks = append(chunks, ch)
	}

	return chunks, nil
}

// chunksViaOraHash returns virtual chunks using ORA_HASH.
// Each chunk gets a hash bucket ID; the WHERE clause filters rows with ORA_HASH(ROWID, N) = chunk_id.
// This doesn't need special privileges but causes N full table scans (each filtering different rows).
func chunksViaOraHash(numChunks int) []RowIDChunk {
	chunks := make([]RowIDChunk, numChunks)
	for i := 0; i < numChunks; i++ {
		chunks[i] = RowIDChunk{
			ID:       i,
			StartRID: "", // empty = use ORA_HASH mode
			EndRID:   "",
		}
	}
	return chunks
}

// BuildChunkWhereClause returns the WHERE clause for a chunk.
// For ROWID-based chunks: "ROWID BETWEEN 'start' AND 'end'"
// For ORA_HASH chunks: "ORA_HASH(ROWID, N) = chunk_id"
func BuildChunkWhereClause(chunk RowIDChunk, totalChunks int, existingWhere string) string {
	var chunkFilter string

	if chunk.StartRID != "" && chunk.EndRID != "" {
		// DBMS_PARALLEL_EXECUTE mode — true ROWID range (most efficient)
		chunkFilter = fmt.Sprintf("ROWID BETWEEN '%s' AND '%s'", chunk.StartRID, chunk.EndRID)
	} else {
		// ORA_HASH fallback
		chunkFilter = fmt.Sprintf("ORA_HASH(ROWID, %d) = %d", totalChunks-1, chunk.ID)
	}

	if existingWhere != "" {
		return chunkFilter + " AND (" + existingWhere + ")"
	}
	return chunkFilter
}
