package migration

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/erenalidal/existora2pg/internal/config"
	"github.com/erenalidal/existora2pg/internal/oracle"
	"github.com/erenalidal/existora2pg/internal/schema"
)

// TableTask represents a single unit of work in the migration plan.
type TableTask struct {
	Table       schema.Table
	Partition   *schema.Partition   // nil for non-partitioned or whole table
	Chunk       *oracle.RowIDChunk  // nil for non-chunked reads
	TotalChunks int                 // total number of chunks (for ORA_HASH WHERE clause)
	Where       string              // optional filter
	FetchSize   int                 // per-table fetch size override (0 = use global)
	BatchSize   int                 // per-table COPY chunk size override (0 = use global)
	MaxParallel int                 // per-table max concurrent workers (0 = use global)
	TargetMode  config.TargetMode   // how to handle the target table
}

// MigrationPlan holds the ordered list of tasks to execute.
type MigrationPlan struct {
	DDLOrder             []schema.Table    // tables in FK dependency order
	DataTasks            []TableTask       // data migration tasks (may be per-partition or per-chunk)
	ConstraintOrder      []schema.Table    // tables in reverse FK order for constraint creation
	Sequences            []schema.Sequence
	ChunkableTasks       []chunkableTask   // non-partitioned tables needing ROWID chunking
	PartitionChunkTables []partitionChunkTask // partitioned tables needing ROWID chunking (resolved later)
}

// partitionChunkTask marks a partitioned table that needs ROWID chunking resolution.
type partitionChunkTask struct {
	Table          schema.Table
	Override       config.TableOverride
	IncludedParts  []int // indices into Table.Partitioning.Partitions
	TargetMode     config.TargetMode
	PlaceholderIdx int   // first placeholder index in DataTasks
	PlaceholderCnt int   // number of placeholder tasks
}

// chunkableTask marks a non-partitioned table that should be split into ROWID chunks.
type chunkableTask struct {
	Table       schema.Table
	Override    config.TableOverride
	ChunkSize   int
	PlaceholderIdx int // index in DataTasks where placeholder was inserted
}

// BuildPlan creates a migration plan from the extracted schema and config.
// dropTarget is the global fallback for per-table TargetMode.
func BuildPlan(s *schema.Schema, cfg *config.MigrationConfig, dropTarget ...bool) *MigrationPlan {
	globalDrop := false
	if len(dropTarget) > 0 {
		globalDrop = dropTarget[0]
	}
	// Topological sort by FK dependencies
	sorted := topoSortTables(s.Tables)

	// Filter out skip tables from DDL order
	var ddlOrder []schema.Table
	for _, t := range sorted {
		override := cfg.TableOverrides[t.Name]
		if override.ResolveTargetMode(globalDrop) != config.TargetModeSkip {
			ddlOrder = append(ddlOrder, t)
		}
	}

	plan := &MigrationPlan{
		DDLOrder:  ddlOrder,
		Sequences: s.Sequences,
	}

	// Build data tasks
	for _, t := range sorted {
		override := cfg.TableOverrides[t.Name]
		targetMode := override.ResolveTargetMode(globalDrop)

		// Skip tables marked as skip
		if targetMode == config.TargetModeSkip {
			continue
		}

		if t.IsPartitioned() {
			included := resolvePartitions(t, override)

			// Determine chunk strategy: per-table > global
			strategy := override.ChunkStrategy
			if strategy == "" {
				strategy = cfg.ChunkStrategy
			}

			batchSize := override.BatchSize
			if batchSize <= 0 {
				batchSize = cfg.BatchSize
			}

			// Offset strategy handles pagination at read time — no upfront chunking needed
			// Check if any partition is large enough to warrant chunking (for rowid/ora_hash)
			needsChunking := false
			if strategy != config.ChunkStrategyOffset {
				for _, idx := range included {
					p := &t.Partitioning.Partitions[idx]
					if partitionChunkCount(p.NumRows, int64(batchSize), cfg.Workers) > 1 {
						needsChunking = true
						break
					}
				}
			}

			if needsChunking && (strategy == config.ChunkStrategyRowID || strategy == config.ChunkStrategyAuto) {
				// ROWID strategy: create placeholders, resolve via DBMS_PARALLEL_EXECUTE later
				placeholderIdx := len(plan.DataTasks)
				for _, idx := range included {
					p := &t.Partitioning.Partitions[idx]
					plan.DataTasks = append(plan.DataTasks, TableTask{
						Table:       t,
						Partition:   p,
						Where:       override.Where,
						FetchSize:   override.FetchSize,
						BatchSize:   override.BatchSize,
						MaxParallel: override.MaxParallel,
						TargetMode:  targetMode,
					})
				}
				plan.PartitionChunkTables = append(plan.PartitionChunkTables, partitionChunkTask{
					Table:          t,
					Override:       override,
					IncludedParts:  included,
					TargetMode:     targetMode,
					PlaceholderIdx: placeholderIdx,
					PlaceholderCnt: len(included),
				})
			} else if needsChunking && strategy == config.ChunkStrategyOraHash {
				// ORA_HASH strategy: create sub-chunks inline
				for _, idx := range included {
					p := &t.Partitioning.Partitions[idx]
					numChunks := partitionChunkCount(p.NumRows, int64(batchSize), cfg.Workers)
					if numChunks > 1 {
						for chunkID := 0; chunkID < numChunks; chunkID++ {
							where := oraHashWhere(numChunks, chunkID, override.Where)
							plan.DataTasks = append(plan.DataTasks, TableTask{
								Table:       t,
								Partition:   p,
								Chunk:       &oracle.RowIDChunk{ID: chunkID},
								TotalChunks: numChunks,
								Where:       where,
								FetchSize:   override.FetchSize,
								BatchSize:   override.BatchSize,
								MaxParallel: override.MaxParallel,
								TargetMode:  targetMode,
							})
						}
					} else {
						plan.DataTasks = append(plan.DataTasks, TableTask{
							Table:       t,
							Partition:   p,
							Where:       override.Where,
							FetchSize:   override.FetchSize,
							BatchSize:   override.BatchSize,
							MaxParallel: override.MaxParallel,
							TargetMode:  targetMode,
						})
					}
				}
			} else {
				// No chunking needed — one task per partition
				for _, idx := range included {
					p := &t.Partitioning.Partitions[idx]
					plan.DataTasks = append(plan.DataTasks, TableTask{
						Table:       t,
						Partition:   p,
						Where:       override.Where,
						FetchSize:   override.FetchSize,
						BatchSize:   override.BatchSize,
						MaxParallel: override.MaxParallel,
						TargetMode:  targetMode,
					})
				}
			}
		} else {
			// Non-partitioned table
			strategy := override.ChunkStrategy
			if strategy == "" {
				strategy = cfg.ChunkStrategy
			}

			// Determine if this table needs chunking
			bs := override.BatchSize
			if bs <= 0 {
				bs = cfg.BatchSize
			}
			needsChunking := override.ChunkSize > 0 ||
				(strategy != config.ChunkStrategyOffset && partitionChunkCount(t.NumRows, int64(bs), cfg.Workers) > 1)

			if strategy == config.ChunkStrategyOffset || !needsChunking {
				// Offset strategy: reader paginates, no upfront chunking
				// Small table: single read
				plan.DataTasks = append(plan.DataTasks, TableTask{
					Table:       t,
					Where:       override.Where,
					FetchSize:   override.FetchSize,
					BatchSize:   override.BatchSize,
					MaxParallel: override.MaxParallel,
					TargetMode:  targetMode,
				})
			} else if strategy == config.ChunkStrategyOraHash {
				// ORA_HASH: create inline sub-chunks
				chunkSize := override.ChunkSize
				if chunkSize <= 0 {
					chunkSize = bs // use batch_size as chunk size
				}
				numChunks := partitionChunkCount(t.NumRows, int64(chunkSize), cfg.Workers)
				if numChunks > 1 {
					for chunkID := 0; chunkID < numChunks; chunkID++ {
						where := oraHashWhere(numChunks, chunkID, override.Where)
						plan.DataTasks = append(plan.DataTasks, TableTask{
							Table:       t,
							Chunk:       &oracle.RowIDChunk{ID: chunkID},
							TotalChunks: numChunks,
							Where:       where,
							FetchSize:   override.FetchSize,
							BatchSize:   override.BatchSize,
							MaxParallel: override.MaxParallel,
							TargetMode:  targetMode,
						})
					}
				} else {
					plan.DataTasks = append(plan.DataTasks, TableTask{
						Table:       t,
						Where:       override.Where,
						FetchSize:   override.FetchSize,
						BatchSize:   override.BatchSize,
						MaxParallel: override.MaxParallel,
						TargetMode:  targetMode,
					})
				}
			} else {
				// ROWID or Auto: placeholder, resolved by ResolveChunks (tries DBMS_PE, falls back to ORA_HASH)
				chunkSize := override.ChunkSize
				if chunkSize <= 0 {
					chunkSize = bs
				}
				placeholderIdx := len(plan.DataTasks)
				plan.DataTasks = append(plan.DataTasks, TableTask{
					Table:       t,
					Where:       override.Where,
					FetchSize:   override.FetchSize,
					BatchSize:   override.BatchSize,
					MaxParallel: override.MaxParallel,
					TargetMode:  targetMode,
				})
				plan.ChunkableTasks = append(plan.ChunkableTasks, chunkableTask{
					Table:          t,
					Override:       override,
					ChunkSize:      chunkSize,
					PlaceholderIdx: placeholderIdx,
				})
			}
		}
	}

	// Constraint order is reverse of DDL order (FKs point backward)
	plan.ConstraintOrder = make([]schema.Table, len(sorted))
	for i, t := range sorted {
		plan.ConstraintOrder[len(sorted)-1-i] = t
	}

	return plan
}

// interleaveTasksByTable reorders tasks in round-robin fashion across tables.
// Input:  [T1_P1, T1_P2, T1_P3, T2_P1, T2_P2, T3_P1]
// Output: [T1_P1, T2_P1, T3_P1, T1_P2, T2_P2, T1_P3]
// This ensures N workers naturally spread across N different tables/partitions
// instead of all workers piling on the same table's chunks.
func interleaveTasksByTable(tasks []TableTask) []TableTask {
	if len(tasks) <= 1 {
		return tasks
	}

	// Group tasks by table+partition (each partition is a separate "bucket")
	type bucket struct {
		key   string
		tasks []TableTask
	}
	bucketMap := make(map[string]*bucket)
	var order []string // preserve insertion order

	for _, t := range tasks {
		key := t.Table.Name
		if t.Partition != nil {
			key += ":" + t.Partition.Name
		}
		b, exists := bucketMap[key]
		if !exists {
			b = &bucket{key: key}
			bucketMap[key] = b
			order = append(order, key)
		}
		b.tasks = append(b.tasks, t)
	}

	// Round-robin across buckets
	result := make([]TableTask, 0, len(tasks))
	for len(result) < len(tasks) {
		for _, key := range order {
			b := bucketMap[key]
			if len(b.tasks) > 0 {
				result = append(result, b.tasks[0])
				b.tasks = b.tasks[1:]
			}
		}
	}
	return result
}

// topoSortTables performs a topological sort based on FK dependencies.
// Tables with no FKs come first, tables that depend on others come later.
func topoSortTables(tables []schema.Table) []schema.Table {
	// Build adjacency: table -> tables it depends on (FK references)
	deps := make(map[string][]string)
	tableMap := make(map[string]schema.Table)

	for _, t := range tables {
		tableMap[t.Name] = t
		for _, c := range t.Constraints {
			if c.Type == schema.ConstraintFK {
				deps[t.Name] = append(deps[t.Name], c.RefTable)
			}
		}
	}

	// Kahn's algorithm
	visited := make(map[string]bool)
	visiting := make(map[string]bool)
	var sorted []schema.Table

	var visit func(name string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		if visiting[name] {
			// Circular dependency — just add it (Oracle allows deferred constraints)
			return
		}
		visiting[name] = true

		for _, dep := range deps[name] {
			if _, exists := tableMap[dep]; exists {
				visit(dep)
			}
		}

		visiting[name] = false
		visited[name] = true
		if t, ok := tableMap[name]; ok {
			sorted = append(sorted, t)
		}
	}

	for _, t := range tables {
		visit(t.Name)
	}

	return sorted
}

// resolvePartitions determines which partition indices to include based on:
// 1. partition_filter (explicit names) — highest priority
// 2. partition_range (value-based auto-pruning) — smart mode
// 3. all partitions (no filter)
func resolvePartitions(t schema.Table, override config.TableOverride) []int {
	parts := t.Partitioning.Partitions

	// Priority 1: explicit partition name filter
	if len(override.PartitionFilter) > 0 {
		var indices []int
		for i, p := range parts {
			if inFilter(p.Name, override.PartitionFilter) {
				indices = append(indices, i)
			}
		}
		return indices
	}

	// Priority 2: value-based range filter (auto-detect overlapping partitions)
	if override.PartitionRange != nil && override.PartitionRange.From != "" {
		return prunePartitionsByRange(parts, override.PartitionRange.From, override.PartitionRange.To)
	}

	// Default: all partitions
	indices := make([]int, len(parts))
	for i := range parts {
		indices[i] = i
	}
	return indices
}

// prunePartitionsByRange returns indices of partitions whose range [lower, upper)
// overlaps with the filter range [from, to).
//
// Partition boundaries come from Oracle HIGH_VALUE metadata.
// A partition at index i covers:
//   - lower = HIGH_VALUE of partition i-1 (or MINVALUE for first)
//   - upper = HIGH_VALUE of partition i
//
// Two ranges [A,B) and [C,D) overlap when A < D AND C < B.
func prunePartitionsByRange(parts []schema.Partition, from, to string) []int {
	filterFrom := normalizeValue(from)
	filterTo := normalizeValue(to)

	var indices []int
	for i, p := range parts {
		// Compute this partition's [lower, upper) bounds
		var lower string
		if i == 0 {
			lower = "" // MINVALUE — always before any filter
		} else {
			lower = extractValue(parts[i-1].HighValue)
		}
		upper := extractValue(p.HighValue)

		// Check overlap: partition [lower, upper) vs filter [filterFrom, filterTo)
		if partitionOverlaps(lower, upper, filterFrom, filterTo) {
			indices = append(indices, i)
		}
	}

	return indices
}

// partitionOverlaps checks if two ranges [pLow, pHigh) and [fLow, fHigh) overlap.
// Empty string = unbounded (MINVALUE/MAXVALUE).
func partitionOverlaps(pLow, pHigh, fLow, fHigh string) bool {
	// Two ranges [A,B) and [C,D) overlap iff A < D AND C < B
	// With unbounded edges:
	//   - pLow="" (MINVALUE): always < fHigh  → condition 1 always true
	//   - pHigh="" (MAXVALUE): always > fLow  → condition 2 always true
	//   - fHigh="": no upper bound filter → condition 1 always true

	// Condition 1: pLow < fHigh (partition starts before filter ends)
	if pLow != "" && fHigh != "" && compareValues(pLow, fHigh) >= 0 {
		return false
	}

	// Condition 2: fLow < pHigh (filter starts before partition ends)
	if fLow != "" && pHigh != "" && compareValues(fLow, pHigh) >= 0 {
		return false
	}

	return true
}

// extractValue pulls the comparable value from Oracle's HIGH_VALUE string.
// "TO_DATE(' 2024-02-01 00:00:00', ...)" → "2024-02-01"
// "MAXVALUE" → ""
// "1000" → "1000"
func extractValue(highValue string) string {
	hv := strings.TrimSpace(highValue)
	if hv == "" || strings.ToUpper(hv) == "MAXVALUE" {
		return "" // unbounded
	}

	// Try to find a date pattern YYYY-MM-DD
	for i := 0; i < len(hv)-10; i++ {
		if hv[i] >= '1' && hv[i] <= '2' && i+4 < len(hv) && hv[i+4] == '-' && i+7 < len(hv) && hv[i+7] == '-' {
			return hv[i : i+10]
		}
	}

	// Numeric: strip quotes and whitespace
	return strings.Trim(hv, " '\"")
}

// normalizeValue cleans user input for comparison.
// "2024-01-01" stays as is, "'2024-01-01'" → "2024-01-01"
func normalizeValue(v string) string {
	return strings.Trim(strings.TrimSpace(v), "'\"")
}

// compareValues compares two values as dates (YYYY-MM-DD) or numbers.
// Returns -1, 0, or 1.
func compareValues(a, b string) int {
	// Try numeric comparison first
	na, errA := strconv.ParseFloat(a, 64)
	nb, errB := strconv.ParseFloat(b, 64)
	if errA == nil && errB == nil {
		if na < nb {
			return -1
		}
		if na > nb {
			return 1
		}
		return 0
	}

	// String/date comparison (works for YYYY-MM-DD format)
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func inFilter(name string, filter []string) bool {
	for _, f := range filter {
		if name == f {
			return true
		}
	}
	return false
}

// partitionChunkCount calculates how many ORA_HASH sub-chunks a partition should
// be split into, based on the partition's row count and batch size.
// Returns 1 (no chunking) for small partitions.
// Caps at workers*4 to allow sufficient parallelism even with single partitions.
func partitionChunkCount(numRows int64, batchSize int64, workers int) int {
	if batchSize <= 0 {
		batchSize = 50000
	}
	// Don't chunk partitions smaller than 10 batches
	threshold := batchSize * 10
	if numRows <= threshold {
		return 1
	}
	// numChunks = numRows / batchSize, but cap at workers*4
	numChunks := int(numRows / batchSize)
	maxChunks := workers * 4
	if maxChunks < 8 {
		maxChunks = 8
	}
	if numChunks > maxChunks {
		numChunks = maxChunks
	}
	if numChunks < 2 {
		numChunks = 2
	}
	return numChunks
}

// oraHashWhere builds an ORA_HASH WHERE clause, optionally merged with an existing WHERE.
func oraHashWhere(totalChunks, chunkID int, existingWhere string) string {
	filter := fmt.Sprintf("ORA_HASH(ROWID, %d) = %d", totalChunks-1, chunkID)
	if existingWhere != "" {
		return filter + " AND (" + existingWhere + ")"
	}
	return filter
}

// mergeChunks reduces a ROWID chunk list to targetN by merging adjacent chunks.
// Each merged chunk spans from the first chunk's StartRID to the last chunk's EndRID.
func mergeChunks(chunks []oracle.RowIDChunk, targetN int) []oracle.RowIDChunk {
	if len(chunks) <= targetN || targetN <= 0 {
		return chunks
	}
	merged := make([]oracle.RowIDChunk, 0, targetN)
	perGroup := len(chunks) / targetN
	extra := len(chunks) % targetN
	pos := 0
	for i := 0; i < targetN; i++ {
		size := perGroup
		if i < extra {
			size++
		}
		group := chunks[pos : pos+size]
		merged = append(merged, oracle.RowIDChunk{
			ID:            i,
			StartRID:      group[0].StartRID,
			EndRID:        group[len(group)-1].EndRID,
			PartitionName: group[0].PartitionName,
		})
		pos += size
	}
	return merged
}

// ResolveChunks computes ROWID chunks for chunkable tasks by querying Oracle.
// This replaces placeholder tasks in DataTasks with actual chunk tasks.
func (plan *MigrationPlan) ResolveChunks(ctx context.Context, chunker *oracle.Chunker, rowEstimator func(ctx context.Context, owner, table string) (int64, error)) error {
	if len(plan.ChunkableTasks) == 0 {
		return nil
	}

	// Process in reverse order so placeholder indices remain valid during replacement
	for i := len(plan.ChunkableTasks) - 1; i >= 0; i-- {
		ct := plan.ChunkableTasks[i]

		// Estimate row count to determine number of chunks (uses Oracle stats, no full scan)
		rowCount, err := rowEstimator(ctx, ct.Table.Owner, ct.Table.Name)
		if err != nil {
			// On error, keep the placeholder as a single non-chunked task
			continue
		}

		numChunks := int(rowCount) / ct.ChunkSize
		if numChunks < 2 {
			// Table too small for chunking, keep as-is
			continue
		}
		if numChunks > 256 {
			numChunks = 256 // cap to avoid excessive chunks
		}

		chunks, err := chunker.ComputeChunks(ctx, ct.Table.Owner, ct.Table.Name, numChunks)
		if err != nil || len(chunks) == 0 {
			continue
		}
		// DBMS_PE may return more chunks than requested; merge excess
		if len(chunks) > numChunks {
			chunks = mergeChunks(chunks, numChunks)
		}

		// Replace placeholder with chunk tasks
		chunkTasks := make([]TableTask, len(chunks))
		for j, chunk := range chunks {
			ch := chunk // capture
			where := oracle.BuildChunkWhereClause(ch, len(chunks), ct.Override.Where)
			chunkTasks[j] = TableTask{
				Table:       ct.Table,
				Chunk:       &ch,
				TotalChunks: len(chunks),
				Where:       where,
				FetchSize:   ct.Override.FetchSize,
				MaxParallel: ct.Override.MaxParallel,
			}
		}

		// Splice: replace placeholder at PlaceholderIdx with chunkTasks
		idx := ct.PlaceholderIdx
		newTasks := make([]TableTask, 0, len(plan.DataTasks)-1+len(chunkTasks))
		newTasks = append(newTasks, plan.DataTasks[:idx]...)
		newTasks = append(newTasks, chunkTasks...)
		newTasks = append(newTasks, plan.DataTasks[idx+1:]...)
		plan.DataTasks = newTasks
	}

	plan.ChunkableTasks = nil
	return nil
}

// ResolvePartitionChunks computes ROWID chunks for partitioned tables via DBMS_PARALLEL_EXECUTE.
// Chunks are mapped to partitions using DBMS_ROWID. Falls back to ORA_HASH if DBMS_PE fails.
func (plan *MigrationPlan) ResolvePartitionChunks(ctx context.Context, chunker *oracle.Chunker, workers, batchSize int) error {
	if len(plan.PartitionChunkTables) == 0 {
		return nil
	}

	// Process in reverse order so splice indices remain valid
	for i := len(plan.PartitionChunkTables) - 1; i >= 0; i-- {
		pct := plan.PartitionChunkTables[i]

		// Try ROWID chunking via DBMS_PARALLEL_EXECUTE
		partChunks, err := chunker.ComputePartitionedChunks(ctx, pct.Table.Owner, pct.Table.Name, workers*4)

		var replacementTasks []TableTask

		maxChunksPerPart := workers * 4
		if maxChunksPerPart < 8 {
			maxChunksPerPart = 8
		}

		if err == nil && len(partChunks) > 0 {
			// ROWID strategy succeeded — create chunk tasks per partition
			for _, idx := range pct.IncludedParts {
				p := &pct.Table.Partitioning.Partitions[idx]
				chunks, ok := partChunks[p.Name]
				if !ok || len(chunks) == 0 {
					// No chunks for this partition (empty?), keep as single task
					replacementTasks = append(replacementTasks, TableTask{
						Table:       pct.Table,
						Partition:   p,
						Where:       pct.Override.Where,
						FetchSize:   pct.Override.FetchSize,
						BatchSize:   pct.Override.BatchSize,
						MaxParallel: pct.Override.MaxParallel,
						TargetMode:  pct.TargetMode,
					})
					continue
				}
				// DBMS_PE can return far more chunks than requested (extent-based).
				// Merge excess chunks to stay within workers*2 per partition.
				if len(chunks) > maxChunksPerPart {
					chunks = mergeChunks(chunks, maxChunksPerPart)
				}
				for _, ch := range chunks {
					chunk := ch // capture
					where := oracle.BuildChunkWhereClause(chunk, len(chunks), pct.Override.Where)
					replacementTasks = append(replacementTasks, TableTask{
						Table:       pct.Table,
						Partition:   p,
						Chunk:       &chunk,
						TotalChunks: len(chunks),
						Where:       where,
						FetchSize:   pct.Override.FetchSize,
						BatchSize:   pct.Override.BatchSize,
						MaxParallel: pct.Override.MaxParallel,
						TargetMode:  pct.TargetMode,
					})
				}
			}
		} else {
			// Fallback to ORA_HASH
			bs := pct.Override.BatchSize
			if bs <= 0 {
				bs = batchSize
			}
			for _, idx := range pct.IncludedParts {
				p := &pct.Table.Partitioning.Partitions[idx]
				numChunks := partitionChunkCount(p.NumRows, int64(bs), workers)
				if numChunks > 1 {
					for chunkID := 0; chunkID < numChunks; chunkID++ {
						where := oraHashWhere(numChunks, chunkID, pct.Override.Where)
						replacementTasks = append(replacementTasks, TableTask{
							Table:       pct.Table,
							Partition:   p,
							Chunk:       &oracle.RowIDChunk{ID: chunkID},
							TotalChunks: numChunks,
							Where:       where,
							FetchSize:   pct.Override.FetchSize,
							BatchSize:   pct.Override.BatchSize,
							MaxParallel: pct.Override.MaxParallel,
							TargetMode:  pct.TargetMode,
						})
					}
				} else {
					replacementTasks = append(replacementTasks, TableTask{
						Table:       pct.Table,
						Partition:   p,
						Where:       pct.Override.Where,
						FetchSize:   pct.Override.FetchSize,
						BatchSize:   pct.Override.BatchSize,
						MaxParallel: pct.Override.MaxParallel,
						TargetMode:  pct.TargetMode,
					})
				}
			}
		}

		// Splice: replace placeholders with resolved tasks
		idx := pct.PlaceholderIdx
		cnt := pct.PlaceholderCnt
		newTasks := make([]TableTask, 0, len(plan.DataTasks)-cnt+len(replacementTasks))
		newTasks = append(newTasks, plan.DataTasks[:idx]...)
		newTasks = append(newTasks, replacementTasks...)
		newTasks = append(newTasks, plan.DataTasks[idx+cnt:]...)
		plan.DataTasks = newTasks
	}

	plan.PartitionChunkTables = nil
	return nil
}

// ResolveOraHashChunks resolves all chunkable and partitioned-chunkable tasks
// using ORA_HASH. This requires NO Oracle round-trip — chunks are computed
// purely from row estimates already available in the schema metadata.
func (plan *MigrationPlan) ResolveOraHashChunks(workers, batchSize int) {
	// Resolve partitioned chunk tables (reverse order for valid splice indices)
	for i := len(plan.PartitionChunkTables) - 1; i >= 0; i-- {
		pct := plan.PartitionChunkTables[i]
		bs := pct.Override.BatchSize
		if bs <= 0 {
			bs = batchSize
		}

		var replacementTasks []TableTask
		for _, idx := range pct.IncludedParts {
			p := &pct.Table.Partitioning.Partitions[idx]
			numChunks := partitionChunkCount(p.NumRows, int64(bs), workers)
			if numChunks > 1 {
				for chunkID := 0; chunkID < numChunks; chunkID++ {
					where := oraHashWhere(numChunks, chunkID, pct.Override.Where)
					replacementTasks = append(replacementTasks, TableTask{
						Table:       pct.Table,
						Partition:   p,
						Chunk:       &oracle.RowIDChunk{ID: chunkID},
						TotalChunks: numChunks,
						Where:       where,
						FetchSize:   pct.Override.FetchSize,
						BatchSize:   pct.Override.BatchSize,
						MaxParallel: pct.Override.MaxParallel,
						TargetMode:  pct.TargetMode,
					})
				}
			} else {
				replacementTasks = append(replacementTasks, TableTask{
					Table:       pct.Table,
					Partition:   p,
					Where:       pct.Override.Where,
					FetchSize:   pct.Override.FetchSize,
					BatchSize:   pct.Override.BatchSize,
					MaxParallel: pct.Override.MaxParallel,
					TargetMode:  pct.TargetMode,
				})
			}
		}

		idx := pct.PlaceholderIdx
		cnt := pct.PlaceholderCnt
		newTasks := make([]TableTask, 0, len(plan.DataTasks)-cnt+len(replacementTasks))
		newTasks = append(newTasks, plan.DataTasks[:idx]...)
		newTasks = append(newTasks, replacementTasks...)
		newTasks = append(newTasks, plan.DataTasks[idx+cnt:]...)
		plan.DataTasks = newTasks
	}
	plan.PartitionChunkTables = nil

	// Resolve non-partitioned chunkable tables
	for i := len(plan.ChunkableTasks) - 1; i >= 0; i-- {
		ct := plan.ChunkableTasks[i]
		if ct.ChunkSize <= 0 {
			ct.ChunkSize = batchSize
		}
		numChunks := ct.Table.NumRows / int64(ct.ChunkSize)
		if numChunks < 2 {
			continue
		}
		if numChunks > 256 {
			numChunks = 256
		}

		chunkTasks := make([]TableTask, numChunks)
		for j := 0; j < int(numChunks); j++ {
			where := oraHashWhere(int(numChunks), j, ct.Override.Where)
			chunkTasks[j] = TableTask{
				Table:       ct.Table,
				Chunk:       &oracle.RowIDChunk{ID: j},
				TotalChunks: int(numChunks),
				Where:       where,
				FetchSize:   ct.Override.FetchSize,
				MaxParallel: ct.Override.MaxParallel,
			}
		}

		idx := ct.PlaceholderIdx
		newTasks := make([]TableTask, 0, len(plan.DataTasks)-1+len(chunkTasks))
		newTasks = append(newTasks, plan.DataTasks[:idx]...)
		newTasks = append(newTasks, chunkTasks...)
		newTasks = append(newTasks, plan.DataTasks[idx+1:]...)
		plan.DataTasks = newTasks
	}
	plan.ChunkableTasks = nil
}
