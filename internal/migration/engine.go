package migration

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/erenalidal/existora2pg/internal/config"
	"github.com/erenalidal/existora2pg/internal/jobstore"
	"github.com/erenalidal/existora2pg/internal/oracle"
	"github.com/erenalidal/existora2pg/internal/postgres"
	"github.com/erenalidal/existora2pg/internal/schema"
	"github.com/google/uuid"
)

// Migration phase constants.
const (
	PhaseExtracting  = "extracting"
	PhasePlanning    = "planning"
	PhaseDDL         = "ddl"
	PhaseCopying     = "copying"
	PhaseLogging     = "logging"
	PhaseConstraints = "constraints"
	PhaseSequences   = "sequences"
	PhaseValidating  = "validating"
	PhaseCompleted   = "completed"
	PhaseFailed      = "failed"
)

// PhaseCallback is called when the engine transitions to a new phase.
type PhaseCallback func(phase string, detail string)

// Engine orchestrates the full migration pipeline.
type Engine struct {
	extractor *oracle.Extractor
	reader    *oracle.Reader
	writer    *postgres.Writer
	store     jobstore.Store
	cfg       *config.Config
	logger    *slog.Logger
	onPhase   PhaseCallback
}

// SetPhaseCallback registers a callback for phase transitions.
func (e *Engine) SetPhaseCallback(cb PhaseCallback) {
	e.onPhase = cb
}

func (e *Engine) setPhase(phase, detail string) {
	e.logger.Info(phase, "detail", detail)
	if e.onPhase != nil {
		e.onPhase(phase, detail)
	}
}

// NewEngine creates a migration engine with all dependencies.
func NewEngine(
	extractor *oracle.Extractor,
	reader *oracle.Reader,
	writer *postgres.Writer,
	store jobstore.Store,
	cfg *config.Config,
	logger *slog.Logger,
) *Engine {
	return &Engine{
		extractor: extractor,
		reader:    reader,
		writer:    writer,
		store:     store,
		cfg:       cfg,
		logger:    logger,
	}
}

// Run executes the full migration pipeline.
func (e *Engine) Run(ctx context.Context) error {
	start := time.Now()
	runID := uuid.New().String()
	e.logger.Info("migration started", "run_id", runID,
		"unlogged", e.cfg.Postgres.Unlogged)

	// Create run record immediately so status endpoint can track it
	configHash := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%v", e.cfg))))
	if err := e.store.CreateRun(ctx, runID, configHash); err != nil {
		return fmt.Errorf("create run: %w", err)
	}

	// Phase 1: Extract metadata
	phaseStart := time.Now()
	e.setPhase(PhaseExtracting, "Extracting Oracle metadata")
	filter := oracle.TableFilter{
		Include:          e.cfg.Migration.IncludeTables,
		Exclude:          e.cfg.Migration.ExcludeTables,
		IncludeSequences: e.cfg.Migration.IncludeSequences,
	}

	s, err := e.extractor.Extract(ctx, e.cfg.Oracle.Schema, filter)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	e.logger.Info("phase 1 completed", "duration", time.Since(phaseStart).Round(time.Millisecond))

	// Phase 2: Build plan
	phaseStart = time.Now()
	e.setPhase(PhasePlanning, "Building migration plan")
	plan := BuildPlan(s, &e.cfg.Migration, e.cfg.Postgres.DropTarget)

	// Resolve chunks — only ROWID strategy requires Oracle-side pre-computation (slow).
	// ORA_HASH adds WHERE clauses at query time, no pre-computation needed.
	strategy := e.cfg.Migration.ChunkStrategy
	needsROWID := strategy == config.ChunkStrategyRowID || strategy == config.ChunkStrategyAuto

	if needsROWID {
		chunker := oracle.NewChunker(e.reader.DB(), e.logger)

		if len(plan.PartitionChunkTables) > 0 {
			e.setPhase(PhasePlanning, fmt.Sprintf("Resolving partition chunks for %d tables (ROWID)", len(plan.PartitionChunkTables)))
			if err := plan.ResolvePartitionChunks(ctx, chunker, e.cfg.Migration.Workers, e.cfg.Migration.BatchSize); err != nil {
				e.logger.Warn("partition chunk resolution had errors", "error", err)
			}
		}

		if len(plan.ChunkableTasks) > 0 {
			e.setPhase(PhasePlanning, fmt.Sprintf("Resolving chunks for %d large tables (ROWID)", len(plan.ChunkableTasks)))
			rowEstimator := func(ctx context.Context, owner, table string) (int64, error) {
				return e.reader.EstimateCount(ctx, owner, table, nil)
			}
			if err := plan.ResolveChunks(ctx, chunker, rowEstimator); err != nil {
				e.logger.Warn("chunk resolution had errors, continuing with non-chunked tasks", "error", err)
			}
		}
	} else {
		// ORA_HASH / offset: resolve chunks inline (no Oracle round-trip)
		plan.ResolveOraHashChunks(e.cfg.Migration.Workers, e.cfg.Migration.BatchSize)
		e.logger.Info("chunks resolved via ORA_HASH (no Oracle pre-computation)")
	}

	// Interleave tasks across tables/partitions so workers spread across
	// different tables instead of all workers piling on the same partition's chunks.
	plan.DataTasks = interleaveTasksByTable(plan.DataTasks)

	e.logger.Info("migration plan ready",
		"tables", len(plan.DDLOrder),
		"data_tasks", len(plan.DataTasks),
		"sequences", len(plan.Sequences),
		"duration", time.Since(phaseStart).Round(time.Millisecond))

	// Check previous run for completed partitions (used for incremental/truncate_partition tables)
	var prevCompleted map[string]int64
	if prevRunID, err := e.store.GetLatestRunID(ctx); err == nil {
		if completed, err := e.store.GetCompletedPartitions(ctx, prevRunID); err == nil && len(completed) > 0 {
			prevCompleted = completed
			e.logger.Info("found previous run", "run_id", prevRunID, "completed_partitions", len(completed))
		}
	}

	// Phase 3: Init job store
	if err := e.initJobs(ctx, runID, plan, prevCompleted); err != nil {
		return fmt.Errorf("init jobs: %w", err)
	}

	// Enable UNLOGGED mode if configured (skip WAL for faster bulk load)
	if e.cfg.Postgres.Unlogged {
		e.writer.SetUnlogged(true)
	}

	// Apply naming convention from config
	e.writer.SetNaming(postgres.NamingConfig{
		Convention:    e.cfg.Migration.NamingConvention,
		TypeOverrides: e.cfg.Migration.TypeOverrides,
		NameOverrides: e.cfg.Migration.NameOverrides,
	})

	// Phase 4: DDL execution
	phaseStart = time.Now()
	e.setPhase(PhaseDDL, fmt.Sprintf("Creating %d tables", len(plan.DDLOrder)))
	if err := e.executeDDL(ctx, runID, plan); err != nil {
		return fmt.Errorf("ddl phase: %w", err)
	}
	e.logger.Info("phase 4 completed", "duration", time.Since(phaseStart).Round(time.Millisecond))

	// Phase 5: Data migration
	phaseStart = time.Now()
	e.setPhase(PhaseCopying, fmt.Sprintf("Copying data: %d tasks, %d workers", len(plan.DataTasks), e.cfg.Migration.Workers))
	if err := e.executeDataMigration(ctx, runID, plan); err != nil {
		return fmt.Errorf("data phase: %w", err)
	}
	e.logger.Info("phase 5 completed", "duration", time.Since(phaseStart).Round(time.Millisecond))

	// Phase 5.5: Convert UNLOGGED tables back to LOGGED
	if e.cfg.Postgres.Unlogged {
		phaseStart = time.Now()
		e.setPhase(PhaseLogging, "Converting UNLOGGED tables to LOGGED")
		tableNames := make([]string, len(plan.DDLOrder))
		for i, t := range plan.DDLOrder {
			tableNames[i] = t.Name
		}
		if err := e.writer.ConvertToLogged(ctx, tableNames); err != nil {
			e.logger.Warn("failed to convert some tables to LOGGED", "error", err)
		}
		e.logger.Info("UNLOGGED→LOGGED conversion completed", "duration", time.Since(phaseStart).Round(time.Millisecond))
	}

	// Phase 6: Constraints and indexes
	phaseStart = time.Now()
	e.setPhase(PhaseConstraints, "Creating constraints and indexes")
	if err := e.executeConstraints(ctx, runID, plan); err != nil {
		return fmt.Errorf("constraints phase: %w", err)
	}
	e.logger.Info("phase 6 completed", "duration", time.Since(phaseStart).Round(time.Millisecond))

	// Phase 7: Sequences
	phaseStart = time.Now()
	e.setPhase(PhaseSequences, fmt.Sprintf("Creating %d sequences", len(plan.Sequences)))
	for _, seq := range plan.Sequences {
		if err := e.writer.CreateSequence(ctx, seq); err != nil {
			e.logger.Warn("sequence creation failed", "sequence", seq.Name, "error", err)
		}
	}
	e.logger.Info("phase 7 completed", "duration", time.Since(phaseStart).Round(time.Millisecond))

	// Phase 8: Validation
	if e.cfg.Migration.ValidateAfter {
		phaseStart = time.Now()
		e.setPhase(PhaseValidating, "Validating migration results")
		if err := e.executeValidation(ctx, runID, plan); err != nil {
			return fmt.Errorf("validation phase: %w", err)
		}
		e.logger.Info("phase 8 completed", "duration", time.Since(phaseStart).Round(time.Millisecond))
	}

	e.setPhase(PhaseCompleted, "Migration finished")
	summary, _ := e.store.GetRunSummary(ctx, runID)
	e.logger.Info("migration completed",
		"run_id", runID,
		"total_duration", time.Since(start).Round(time.Millisecond),
		"completed", summary.Completed,
		"failed", summary.Failed)

	if summary.Failed > 0 {
		return fmt.Errorf("%d jobs failed, use 'resume' command to retry", summary.Failed)
	}

	return nil
}

// Resume continues a previously failed migration run.
func (e *Engine) Resume(ctx context.Context) error {
	runID, err := e.store.GetLatestRunID(ctx)
	if err != nil {
		return fmt.Errorf("get latest run: %w", err)
	}

	e.logger.Info("resuming migration", "run_id", runID)

	count, err := e.store.MarkFailedAsRetrying(ctx, runID)
	if err != nil {
		return fmt.Errorf("mark retrying: %w", err)
	}
	e.logger.Info("marked failed jobs for retry", "count", count)

	// For chunked tables: if any chunk failed, we must re-run ALL chunks for that table
	// because we can't selectively delete a chunk's data from PG (ROWID is Oracle-only).
	// Truncate the table and reset all its chunks to RETRYING.
	if err := e.resetFailedChunkedTables(ctx, runID); err != nil {
		e.logger.Warn("failed to reset chunked tables", "error", err)
	}

	// Re-extract metadata to get table definitions
	filter := oracle.TableFilter{
		Include:          e.cfg.Migration.IncludeTables,
		Exclude:          e.cfg.Migration.ExcludeTables,
		IncludeSequences: e.cfg.Migration.IncludeSequences,
	}
	s, err := e.extractor.Extract(ctx, e.cfg.Oracle.Schema, filter)
	if err != nil {
		return fmt.Errorf("extract for resume: %w", err)
	}
	plan := BuildPlan(s, &e.cfg.Migration, e.cfg.Postgres.DropTarget)

	// Resolve chunks (same as initial run) so task keys match job partition keys
	strategy := e.cfg.Migration.ChunkStrategy
	if strategy == config.ChunkStrategyRowID || strategy == config.ChunkStrategyAuto {
		chunker := oracle.NewChunker(e.reader.DB(), e.logger)
		if len(plan.PartitionChunkTables) > 0 {
			if err := plan.ResolvePartitionChunks(ctx, chunker, e.cfg.Migration.Workers, e.cfg.Migration.BatchSize); err != nil {
				e.logger.Warn("resume: partition chunk resolution had errors", "error", err)
			}
		}
		if len(plan.ChunkableTasks) > 0 {
			rowEstimator := func(ctx context.Context, owner, table string) (int64, error) {
				return e.reader.EstimateCount(ctx, owner, table, nil)
			}
			if err := plan.ResolveChunks(ctx, chunker, rowEstimator); err != nil {
				e.logger.Warn("resume: chunk resolution had errors", "error", err)
			}
		}
	} else {
		plan.ResolveOraHashChunks(e.cfg.Migration.Workers, e.cfg.Migration.BatchSize)
	}

	plan.DataTasks = interleaveTasksByTable(plan.DataTasks)
	e.logger.Info("resume plan ready", "data_tasks", len(plan.DataTasks))

	// Re-run data phase for pending/retrying jobs
	if err := e.executeDataMigration(ctx, runID, plan); err != nil {
		return fmt.Errorf("data phase resume: %w", err)
	}

	// Re-run constraints for pending/retrying
	if err := e.executeConstraints(ctx, runID, plan); err != nil {
		return fmt.Errorf("constraints phase resume: %w", err)
	}

	summary, _ := e.store.GetRunSummary(ctx, runID)
	e.logger.Info("resume completed",
		"run_id", runID,
		"completed", summary.Completed,
		"failed", summary.Failed)

	return nil
}

func (e *Engine) initJobs(ctx context.Context, runID string, plan *MigrationPlan, prevCompleted map[string]int64) error {
	// Build set of tables that use incremental mode (truncate_partition)
	// Only these tables should skip previously completed partitions
	incrementalTables := make(map[string]bool)
	for _, t := range plan.DDLOrder {
		override := e.cfg.Migration.TableOverrides[t.Name]
		mode := override.ResolveTargetMode(e.cfg.Postgres.DropTarget)
		if mode == config.TargetModeTruncatePartition {
			incrementalTables[t.Name] = true
		}
	}

	// Determine which incremental tables have ALL their data partitions already completed
	// so we can skip DDL/constraint/validate for them too
	tableFullyCompleted := make(map[string]bool)
	if len(prevCompleted) > 0 {
		tablePartitions := make(map[string][]string)
		for _, task := range plan.DataTasks {
			if !incrementalTables[task.Table.Name] {
				continue
			}
			partition := jobPartitionKey(task)
			tablePartitions[task.Table.Name] = append(tablePartitions[task.Table.Name], partition)
		}
		for tbl, parts := range tablePartitions {
			allDone := true
			for _, p := range parts {
				if _, ok := prevCompleted[tbl+":"+p]; !ok {
					allDone = false
					break
				}
			}
			if allDone {
				tableFullyCompleted[tbl] = true
			}
		}
	}

	skippedCount := 0

	// DDL jobs
	for _, t := range plan.DDLOrder {
		job := &jobstore.Job{
			RunID:       runID,
			TableSchema: t.Owner,
			TableName:   t.Name,
			Phase:       "ddl",
		}
		if err := e.store.CreateJob(ctx, job); err != nil {
			return err
		}
		if tableFullyCompleted[t.Name] {
			e.store.SetSkipped(ctx, job.ID, 0)
			skippedCount++
		}
	}

	// Data jobs
	for _, task := range plan.DataTasks {
		partition := jobPartitionKey(task)
		// Determine expected row count from Oracle metadata
		var rowsExpected int64
		if task.Partition != nil && task.Chunk != nil {
			// Partition sub-chunk: estimate = partition rows / num chunks
			if task.TotalChunks > 0 {
				rowsExpected = task.Partition.NumRows / int64(task.TotalChunks)
			}
		} else if task.Partition != nil {
			rowsExpected = task.Partition.NumRows
		} else if task.Chunk != nil {
			// Non-partitioned chunk: estimate = table rows / num chunks
			if task.TotalChunks > 0 {
				rowsExpected = task.Table.NumRows / int64(task.TotalChunks)
			}
		} else {
			rowsExpected = task.Table.NumRows
		}
		job := &jobstore.Job{
			RunID:        runID,
			TableSchema:  task.Table.Owner,
			TableName:    task.Table.Name,
			Partition:    partition,
			Phase:        "data",
			RowsExpected: rowsExpected,
		}
		if err := e.store.CreateJob(ctx, job); err != nil {
			return err
		}
		// Only skip if this table is in incremental mode AND partition was completed before
		if incrementalTables[task.Table.Name] {
			key := task.Table.Name + ":" + partition
			if prevRows, ok := prevCompleted[key]; ok {
				e.store.SetSkipped(ctx, job.ID, prevRows)
				skippedCount++
			}
		}
	}

	// Constraint jobs
	for _, t := range plan.ConstraintOrder {
		job := &jobstore.Job{
			RunID:       runID,
			TableSchema: t.Owner,
			TableName:   t.Name,
			Phase:       "constraints",
		}
		if err := e.store.CreateJob(ctx, job); err != nil {
			return err
		}
		if tableFullyCompleted[t.Name] {
			e.store.SetSkipped(ctx, job.ID, 0)
			skippedCount++
		}
	}

	// Validate jobs
	for _, t := range plan.DDLOrder {
		job := &jobstore.Job{
			RunID:       runID,
			TableSchema: t.Owner,
			TableName:   t.Name,
			Phase:       "validate",
		}
		if err := e.store.CreateJob(ctx, job); err != nil {
			return err
		}
		if tableFullyCompleted[t.Name] {
			e.store.SetSkipped(ctx, job.ID, 0)
			skippedCount++
		}
	}

	if skippedCount > 0 {
		e.logger.Info("skipped previously completed jobs", "count", skippedCount)
	}

	return nil
}

func (e *Engine) executeDDL(ctx context.Context, runID string, plan *MigrationPlan) error {
	jobs, err := e.store.GetPendingJobs(ctx, runID, "ddl")
	if err != nil {
		return err
	}

	tableMap := make(map[string]schema.Table)
	for _, t := range plan.DDLOrder {
		tableMap[t.Name] = t
	}

	// Resolve target mode per table
	targetModes := make(map[string]config.TargetMode)
	for _, t := range plan.DDLOrder {
		override := e.cfg.Migration.TableOverrides[t.Name]
		targetModes[t.Name] = override.ResolveTargetMode(e.cfg.Postgres.DropTarget)
	}

	for _, job := range jobs {
		t, ok := tableMap[job.TableName]
		if !ok {
			continue
		}

		mode := targetModes[t.Name]
		if mode == config.TargetModeSkip {
			e.store.SetCompleted(ctx, job.ID, 0, 0)
			continue
		}

		if err := e.store.SetRunning(ctx, job.ID); err != nil {
			return err
		}

		switch mode {
		case config.TargetModeAppend:
			// Append: no DDL changes, table must already exist
			exists, err := e.writer.TableExists(ctx, t.Name)
			if err != nil {
				e.store.SetFailed(ctx, job.ID, err.Error())
				return fmt.Errorf("check table %s: %w", t.Name, err)
			}
			if !exists {
				// Table doesn't exist yet, create it
				if err := e.writer.CreateTable(ctx, t, false); err != nil {
					e.store.SetFailed(ctx, job.ID, err.Error())
					return fmt.Errorf("create table %s: %w", t.Name, err)
				}
				if t.IsPartitioned() {
					bounds := BuildPartitionBounds(t)
					if err := e.writer.CreatePartitions(ctx, t, bounds); err != nil {
						e.store.SetFailed(ctx, job.ID, err.Error())
						return fmt.Errorf("create partitions for %s: %w", t.Name, err)
					}
				}
			}

		case config.TargetModeTruncate:
			// Truncate: keep structure, delete all data
			exists, _ := e.writer.TableExists(ctx, t.Name)
			if exists {
				if err := e.writer.TruncateTable(ctx, t.Name); err != nil {
					e.store.SetFailed(ctx, job.ID, err.Error())
					return fmt.Errorf("truncate table %s: %w", t.Name, err)
				}
			} else {
				// Table doesn't exist, create it
				if err := e.writer.CreateTable(ctx, t, false); err != nil {
					e.store.SetFailed(ctx, job.ID, err.Error())
					return fmt.Errorf("create table %s: %w", t.Name, err)
				}
				if t.IsPartitioned() {
					bounds := BuildPartitionBounds(t)
					if err := e.writer.CreatePartitions(ctx, t, bounds); err != nil {
						e.store.SetFailed(ctx, job.ID, err.Error())
						return fmt.Errorf("create partitions for %s: %w", t.Name, err)
					}
				}
			}

		case config.TargetModeTruncatePartition:
			// Truncate partition: create table if needed, truncate only migrated partitions later in data phase
			exists, _ := e.writer.TableExists(ctx, t.Name)
			if !exists {
				if err := e.writer.CreateTable(ctx, t, false); err != nil {
					e.store.SetFailed(ctx, job.ID, err.Error())
					return fmt.Errorf("create table %s: %w", t.Name, err)
				}
				if t.IsPartitioned() {
					bounds := BuildPartitionBounds(t)
					if err := e.writer.CreatePartitions(ctx, t, bounds); err != nil {
						e.store.SetFailed(ctx, job.ID, err.Error())
						return fmt.Errorf("create partitions for %s: %w", t.Name, err)
					}
				}
			}

		default: // recreate
			if err := e.writer.CreateTable(ctx, t, true); err != nil {
				e.store.SetFailed(ctx, job.ID, err.Error())
				return fmt.Errorf("create table %s: %w", t.Name, err)
			}
			if t.IsPartitioned() {
				bounds := BuildPartitionBounds(t)
				if err := e.writer.CreatePartitions(ctx, t, bounds); err != nil {
					e.store.SetFailed(ctx, job.ID, err.Error())
					return fmt.Errorf("create partitions for %s: %w", t.Name, err)
				}
			}
		}

		if err := e.store.SetCompleted(ctx, job.ID, 0, 0); err != nil {
			return err
		}
	}

	return nil
}

func (e *Engine) executeDataMigration(ctx context.Context, runID string, plan *MigrationPlan) error {
	jobs, err := e.store.GetPendingJobs(ctx, runID, "data")
	if err != nil {
		return err
	}

	if len(jobs) == 0 {
		e.logger.Info("no pending data jobs")
		return nil
	}

	// Build task lookup
	taskMap := make(map[string]TableTask)
	for _, task := range plan.DataTasks {
		key := taskKey(task)
		taskMap[key] = task
	}

	// Map jobs to tasks
	var tasks []TableTask
	jobLookup := make(map[string]int64) // task key -> job ID
	for _, job := range jobs {
		key := job.TableName
		if job.Partition != "" {
			key += ":" + job.Partition
		}
		if task, ok := taskMap[key]; ok {
			tasks = append(tasks, task)
			jobLookup[key] = job.ID
		}
	}

	// Build per-table semaphores for max_parallel limits
	tableSemaphores := make(map[string]chan struct{})
	for _, task := range tasks {
		if task.MaxParallel > 0 {
			if _, exists := tableSemaphores[task.Table.Name]; !exists {
				tableSemaphores[task.Table.Name] = make(chan struct{}, task.MaxParallel)
				e.logger.Info("per-table parallelism limit",
					"table", task.Table.Name,
					"max_parallel", task.MaxParallel)
			}
		}
	}

	pool := NewWorkerPool(e.cfg.Migration.Workers, e.logger)
	return pool.Run(ctx, tasks, func(ctx context.Context, task TableTask) error {
		// Apply per-table parallelism limit via semaphore
		if sem, ok := tableSemaphores[task.Table.Name]; ok {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		jobID := jobLookup[taskKey(task)]

		// Check if this is a retry (job was RETRYING from previous crash/failure).
		// Find the job to check its current state before we set it to RUNNING.
		isRetry := false
		for _, j := range jobs {
			if j.ID == jobID && j.State == jobstore.JobRetrying {
				isRetry = true
				break
			}
		}

		if err := e.store.SetRunning(ctx, jobID); err != nil {
			return err
		}

		// Pre-task cleanup: remove partial data to avoid duplicates.
		// For retries, clean up partial data from crashed/failed attempt.
		// For chunked tables, use DELETE with the chunk's WHERE clause (safe for other chunks).
		// For partitions, truncate only the partition. For whole tables, truncate table.
		if isRetry {
			if task.Chunk != nil && task.Where != "" {
				// Chunk retry: delete only this chunk's rows using the Oracle-side WHERE clause
				// mapped to PG. For ROWID-based chunks this won't work directly, so we rely on
				// the fact that resume should re-run ALL chunks for this table after truncate.
				// Skip per-chunk cleanup — handled at table level by resume logic.
				e.logger.Info("chunk retry detected, skipping per-chunk cleanup",
					"table", task.Table.Name,
					"chunk", task.Chunk.ID)
			} else if task.Partition != nil {
				e.writer.TruncatePartition(ctx, task.Table.Name, task.Partition.Name)
			} else {
				e.writer.TruncateTable(ctx, task.Table.Name)
			}
			e.logger.Info("pre-task cleanup for retry",
				"table", task.Table.Name,
				"partition", partName(task))
		} else if task.TargetMode == config.TargetModeTruncatePartition && task.Partition != nil {
			if err := e.writer.TruncatePartition(ctx, task.Table.Name, task.Partition.Name); err != nil {
				e.logger.Warn("partition truncate failed (may not exist yet)", "table", task.Table.Name,
					"partition", task.Partition.Name, "error", err)
			}
		}

		// Determine batch size (per-table override or global default)
		batchSize := e.cfg.Migration.BatchSize
		if task.BatchSize > 0 {
			batchSize = task.BatchSize
		}

		var rowsExpected int64
		if task.Partition != nil && task.Chunk != nil {
			// Partition sub-chunk
			if task.TotalChunks > 0 {
				rowsExpected = task.Partition.NumRows / int64(task.TotalChunks)
			}
		} else if task.Partition != nil {
			rowsExpected = task.Partition.NumRows
		} else {
			rowsExpected = task.Table.NumRows
		}

		// Retry wrapper for the entire read→transform→write pipeline.
		// Oracle connection drops require re-reading from the start, so we retry
		// the full pipeline. Previously committed PG batches are already durable;
		// we clean up partial data before retry to avoid duplicates.
		//
		// For chunked tables, pipeline retry is disabled — batch-level retry in
		// writer.CopyData handles transient PG errors. If Oracle fails mid-chunk,
		// the chunk is marked FAILED and resume re-runs all chunks after table truncate.
		maxPipelineRetries := 3
		if task.Chunk != nil {
			maxPipelineRetries = 0 // no pipeline retry for chunks — batch retry only
		}
		var stats *postgres.CopyStats
		var lastErr error

		for attempt := 0; attempt <= maxPipelineRetries; attempt++ {
			if attempt > 0 {
				delay := time.Duration(1<<(attempt-1)) * time.Second
				e.logger.Warn("retrying data pipeline",
					"table", task.Table.Name,
					"partition", partName(task),
					"attempt", attempt+1,
					"delay", delay,
					"error", lastErr)
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					e.store.SetFailed(ctx, jobID, ctx.Err().Error())
					return ctx.Err()
				}
				// Clean up partial data before retry
				if task.Chunk != nil {
					// Sub-chunk: can't truncate partition (other chunks' data is there)
					e.logger.Info("sub-chunk pipeline retry, skipping truncate",
						"table", task.Table.Name, "partition", partName(task))
				} else if task.Partition != nil {
					e.writer.TruncatePartition(ctx, task.Table.Name, task.Partition.Name)
				} else {
					e.writer.TruncateTable(ctx, task.Table.Name)
				}
			}

			// Progress watchdog: cancel pipeline if no progress for 30 minutes.
			// Stuck jobs (e.g. Oracle connection hung) won't be detected otherwise.
			const progressTimeout = 30 * time.Minute
			var lastProgress atomic.Value
			lastProgress.Store(time.Now())

			pipelineCtx, pipelineCancel := context.WithCancel(ctx)
			defer pipelineCancel()

			go func() {
				ticker := time.NewTicker(5 * time.Minute)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						if lp, ok := lastProgress.Load().(time.Time); ok {
							if time.Since(lp) > progressTimeout {
								e.logger.Error("progress timeout — no progress for 30min, cancelling pipeline",
									"table", task.Table.Name,
									"partition", partName(task))
								pipelineCancel()
								return
							}
						}
					case <-pipelineCtx.Done():
						return
					}
				}
			}()

			copyStart := time.Now()
			var lastProgressLog time.Time
			progressFn := func(rows int64, interim *postgres.CopyStats) {
				lastProgress.Store(time.Now())
				// Throttle: log at most once per second to prevent SSE event flood.
				// A 40M row table at 50K batch = 800 batches. Without throttle,
				// 800 SSE events cause browser UI to freeze.
				now := time.Now()
				if now.Sub(lastProgressLog) < time.Second {
					return
				}
				lastProgressLog = now
				pn := partName(task)
				var pct float64
				if rowsExpected > 0 {
					pct = float64(rows) / float64(rowsExpected) * 100
					if pct > 100 {
						pct = 99
					}
				}
				elapsed := time.Since(copyStart).Seconds()
				var speed int64
				if elapsed > 0 {
					speed = int64(float64(rows) / elapsed)
				}
				args := []any{
					"table", task.Table.Name,
					"partition", pn,
					"rows", rows,
					"rows_per_sec", speed,
					"percent", pct,
				}
				if interim != nil {
					args = append(args,
						"batches", interim.BatchCount,
						"avg_batch_write", interim.AvgBatchWrite.Round(time.Millisecond),
						"acquire_wait", interim.AcquireWait.Round(time.Millisecond),
						"commit_wait", interim.CommitWait.Round(time.Millisecond),
					)
				}
				e.logger.Info("progress", args...)
			}

			// Read from Oracle — choose strategy
			// Use pipelineCtx so the progress watchdog can cancel stuck pipelines.
			var rowCh <-chan oracle.ReadResult
			chunkStrategy := e.cfg.Migration.ChunkStrategy
			if chunkStrategy == config.ChunkStrategyOffset {
				pageSize := batchSize
				if task.FetchSize > 0 {
					pageSize = task.FetchSize
				}
				rowCh = e.reader.ReadPaginated(pipelineCtx, task.Table, task.Partition, task.Where, pageSize)
			} else {
				rowCh = e.reader.Read(pipelineCtx, task.Table, task.Partition, task.Where, task.FetchSize)
			}

			// Write to PostgreSQL via chunked COPY (transform is done inline in writer)
			stats, lastErr = e.writer.CopyData(pipelineCtx, task.Table, task.Partition, rowCh, batchSize, progressFn,
				postgres.CopyDataOpts{MaxBatchBytes: e.cfg.Migration.MaxBatchBytes})
			if lastErr == nil {
				pn := partName(task)
				totalDur := stats.Duration
				readPct := int(float64(stats.ReadTime) / float64(totalDur) * 100)
				writePct := int(float64(stats.WriteTime) / float64(totalDur) * 100)
				e.logger.Info("pipeline stats",
					"table", task.Table.Name,
					"partition", pn,
					"rows", stats.RowsCopied,
					"duration", totalDur.Round(time.Millisecond),
					"read_time", stats.ReadTime.Round(time.Millisecond),
					"write_time", stats.WriteTime.Round(time.Millisecond),
					"read_pct", readPct,
					"write_pct", writePct,
					"batches", stats.BatchCount,
					"avg_batch_write", stats.AvgBatchWrite.Round(time.Millisecond),
					"acquire_wait", stats.AcquireWait.Round(time.Millisecond),
					"commit_wait", stats.CommitWait.Round(time.Millisecond),
					"rows_per_sec", int64(float64(stats.RowsCopied)/totalDur.Seconds()))
				break
			}

			// Don't retry on context cancellation
			if ctx.Err() != nil {
				e.store.SetFailed(ctx, jobID, lastErr.Error())
				return lastErr
			}
			e.logger.Error("data pipeline failed",
				"table", task.Table.Name,
				"partition", partName(task),
				"attempt", attempt+1,
				"error", lastErr)
		}

		if lastErr != nil {
			e.store.SetFailed(ctx, jobID, fmt.Sprintf("failed after %d retries: %s", maxPipelineRetries, lastErr))
			return lastErr
		}

		return e.store.SetCompleted(ctx, jobID, stats.RowsCopied, stats.BytesCopied)
	})
}

func (e *Engine) executeConstraints(ctx context.Context, runID string, plan *MigrationPlan) error {
	jobs, err := e.store.GetPendingJobs(ctx, runID, "constraints")
	if err != nil {
		return err
	}

	tableMap := make(map[string]schema.Table)
	for _, t := range plan.ConstraintOrder {
		tableMap[t.Name] = t
	}

	// Two-pass approach: PK + indexes first (all tables), then FK/UNIQUE/CHECK.
	// This ensures referenced PKs exist before FKs are created.

	// Pass 1: PK + indexes
	for _, job := range jobs {
		t, ok := tableMap[job.TableName]
		if !ok {
			continue
		}

		override := e.cfg.Migration.TableOverrides[t.Name]
		indexMode := override.ResolveIndexMode()
		if override.SkipConstraints && indexMode == config.IndexModeSkip {
			e.logger.Info("skipping constraints and indexes (per-table override)",
				"table", t.Name)
			e.store.SetCompleted(ctx, job.ID, 0, 0)
			continue
		}

		if err := e.store.SetRunning(ctx, job.ID); err != nil {
			return err
		}

		// Create PK (always, unless both are skipped)
		if err := e.writer.CreatePrimaryKey(ctx, t); err != nil {
			e.logger.Warn("PK creation failed, continuing", "table", t.Name, "error", err)
		}

		// Create indexes
		if err := e.writer.CreateIndexesRaw(ctx, t, indexMode); err != nil {
			e.logger.Warn("index creation failed, continuing", "table", t.Name, "error", err)
		}
	}

	// Pass 2: FK, UNIQUE, CHECK constraints
	for _, job := range jobs {
		t, ok := tableMap[job.TableName]
		if !ok {
			continue
		}

		override := e.cfg.Migration.TableOverrides[t.Name]
		if override.SkipConstraints && override.ResolveIndexMode() == config.IndexModeSkip {
			continue // already completed in pass 1
		}

		if !override.SkipConstraints {
			if err := e.writer.CreateFKAndOtherConstraints(ctx, t); err != nil {
				e.store.SetFailed(ctx, job.ID, err.Error())
				e.logger.Warn("constraint creation failed, continuing",
					"table", t.Name, "error", err)
				continue
			}
		}

		if err := e.store.SetCompleted(ctx, job.ID, 0, 0); err != nil {
			return err
		}
	}

	return nil
}

// estimateThreshold is the row count above which we use estimated counts
// instead of exact COUNT(*) to avoid long-running table scans.
const estimateThreshold = 500_000

func (e *Engine) executeValidation(ctx context.Context, runID string, plan *MigrationPlan) error {
	jobs, err := e.store.GetPendingJobs(ctx, runID, "validate")
	if err != nil {
		return err
	}

	// First, run ANALYZE on PG tables so estimated counts are fresh
	e.logger.Info("running ANALYZE on migrated tables for accurate estimates")
	for _, job := range jobs {
		qualName := e.writer.QualifiedName(job.TableName)
		if _, err := e.writer.ExecDDLReturn(ctx, fmt.Sprintf("ANALYZE %s", qualName)); err != nil {
			e.logger.Warn("ANALYZE failed, will use exact count", "table", job.TableName, "error", err)
		}
	}

	// Collect total rows from data phase (COPY results) per table
	copiedRows := make(map[string]int64)
	dataJobs, _ := e.store.GetJobsByPhase(ctx, runID, "data")
	for _, dj := range dataJobs {
		if dj.State == "COMPLETED" {
			copiedRows[dj.TableName] += dj.RowsCopied
		}
	}

	for _, job := range jobs {
		// Skip validation if configured for this table
		override := e.cfg.Migration.TableOverrides[job.TableName]
		if override.SkipValidation {
			e.logger.Info("skipping validation (per-table override)",
				"table", job.TableName)
			e.store.SetCompleted(ctx, job.ID, 0, 0)
			continue
		}

		// Detect if a partial filter is active (partition_filter, partition_range, or where)
		hasPartitionFilter := len(override.PartitionFilter) > 0 || override.PartitionRange != nil

		if err := e.store.SetRunning(ctx, job.ID); err != nil {
			return err
		}

		// Quick validation: compare COPY result rows with PG estimated count
		// This catches gross mismatches without expensive Oracle COUNT(*)
		copied := copiedRows[job.TableName]

		// Get Oracle count — use stats for large tables
		var oraCount int64
		useEstimate := copied > estimateThreshold && !hasPartitionFilter && override.Where == ""

		if useEstimate {
			// Use Oracle stats (instant)
			est, err := e.reader.EstimateCount(ctx, job.TableSchema, job.TableName, nil)
			if err == nil && est > 0 {
				oraCount = est
				e.logger.Debug("using estimated oracle count", "table", job.TableName, "estimate", est)
			} else {
				// Fallback to exact count
				useEstimate = false
			}
		}

		if !useEstimate {
			var countFailed bool
			if hasPartitionFilter {
				for _, task := range plan.DataTasks {
					if task.Table.Name != job.TableName {
						continue
					}
					partCount, err := e.reader.Count(ctx, job.TableSchema, job.TableName, task.Partition, override.Where)
					if err != nil {
						e.store.SetFailed(ctx, job.ID, fmt.Sprintf("oracle partition count: %v", err))
						countFailed = true
						break
					}
					oraCount += partCount
				}
			} else {
				var err error
				oraCount, err = e.reader.Count(ctx, job.TableSchema, job.TableName, nil, override.Where)
				if err != nil {
					e.store.SetFailed(ctx, job.ID, fmt.Sprintf("oracle count: %v", err))
					countFailed = true
				}
			}
			if countFailed {
				continue
			}
		}

		// Get PG count — use estimate for large tables (ANALYZE already ran)
		var pgCount int64
		if copied > estimateThreshold {
			est, err := e.writer.EstimateCount(ctx, job.TableName)
			if err == nil && est > 0 {
				pgCount = est
			} else {
				pgCount, err = e.writer.Count(ctx, job.TableName)
				if err != nil {
					e.store.SetFailed(ctx, job.ID, fmt.Sprintf("pg count: %v", err))
					continue
				}
			}
		} else {
			var err error
			pgCount, err = e.writer.Count(ctx, job.TableName)
			if err != nil {
				e.store.SetFailed(ctx, job.ID, fmt.Sprintf("pg count: %v", err))
				continue
			}
		}

		// For estimated counts, allow 1% tolerance
		tolerance := int64(0)
		if useEstimate || copied > estimateThreshold {
			tolerance = max(oraCount, pgCount) / 100 // 1%
		}

		diff := oraCount - pgCount
		if diff < 0 {
			diff = -diff
		}

		if diff > tolerance {
			method := "exact"
			if useEstimate || copied > estimateThreshold {
				method = "estimated (~1% tolerance)"
			}
			msg := fmt.Sprintf("row count mismatch (%s): oracle=%d pg=%d diff=%d", method, oraCount, pgCount, diff)
			e.logger.Warn("validation failed", "table", job.TableName, "detail", msg)
			e.store.SetFailed(ctx, job.ID, msg)
			continue
		}

		method := "exact"
		if useEstimate || copied > estimateThreshold {
			method = "estimated"
		}
		e.logger.Info("validation passed",
			"table", job.TableName,
			"rows", oraCount,
			"method", method)

		if err := e.store.SetCompleted(ctx, job.ID, oraCount, 0); err != nil {
			return err
		}
	}

	return nil
}

// BuildPartitionBounds converts Oracle partition HIGH_VALUE metadata to PostgreSQL bounds.
// Supports RANGE (FROM/TO) and LIST (IN) partition strategies.
func BuildPartitionBounds(t schema.Table) []postgres.PartitionBound {
	if !t.IsPartitioned() {
		return nil
	}

	parts := t.Partitioning.Partitions
	bounds := make([]postgres.PartitionBound, 0, len(parts))
	strategy := string(t.Partitioning.Strategy)

	for i, p := range parts {
		bound := postgres.PartitionBound{
			Name:     strings.ToLower(t.Name) + "_" + strings.ToLower(p.Name),
			Strategy: strategy,
		}

		if strategy == "LIST" {
			bound.Values = parseListValues(p.HighValue)
		} else {
			// RANGE
			if i == 0 {
				bound.LowerBound = "MINVALUE"
			} else {
				bound.LowerBound = parseHighValue(parts[i-1].HighValue)
			}
			bound.UpperBound = parseHighValue(p.HighValue)
		}

		bounds = append(bounds, bound)
	}

	return bounds
}

// parseListValues extracts individual values from Oracle LIST partition HIGH_VALUE.
// Oracle format: "'US', 'CA'" or "1, 2, 3" or "'ACTIVE'"
func parseListValues(highValue string) []string {
	hv := strings.TrimSpace(highValue)
	if hv == "" || hv == "DEFAULT" {
		return []string{"DEFAULT"}
	}
	// Split by comma and trim each value
	parts := strings.Split(hv, ",")
	values := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			values = append(values, v)
		}
	}
	return values
}

// parseHighValue extracts a PostgreSQL-compatible value from Oracle's HIGH_VALUE string.
// Oracle format: TO_DATE(' 2024-02-01 00:00:00', 'SYYYY-MM-DD HH24:MI:SS', ...)
// or: TIMESTAMP' 2024-02-01 00:00:00'
// We extract the date and return as a PG literal.
func parseHighValue(highValue string) string {
	hv := strings.TrimSpace(highValue)
	if hv == "" || hv == "MAXVALUE" {
		return "MAXVALUE"
	}

	// Try to find a date pattern YYYY-MM-DD in the HIGH_VALUE
	// Oracle TO_DATE format: TO_DATE(' 2024-02-01 00:00:00', ...)
	for i := 0; i < len(hv)-10; i++ {
		if hv[i] >= '1' && hv[i] <= '2' && hv[i+4] == '-' && hv[i+7] == '-' {
			date := hv[i : i+10]
			return "'" + date + "'"
		}
	}

	// Fallback: try to use as-is (for numeric partitions)
	return hv
}

func partName(task TableTask) string {
	name := ""
	if task.Partition != nil {
		name = task.Partition.Name
	}
	if task.Chunk != nil {
		if name != "" {
			name += fmt.Sprintf(":chunk:%d", task.Chunk.ID)
		} else {
			name = fmt.Sprintf("chunk:%d", task.Chunk.ID)
		}
	}
	return name
}

// resetFailedChunkedTables finds chunked tables with any FAILED/RETRYING chunks,
// truncates the PG table, and resets ALL chunks for that table to RETRYING.
// This is necessary because ROWID-based chunk boundaries are Oracle-only —
// we can't selectively delete a chunk's rows from PG.
func (e *Engine) resetFailedChunkedTables(ctx context.Context, runID string) error {
	jobs, err := e.store.GetJobsByPhase(ctx, runID, "data")
	if err != nil {
		return err
	}

	// Find tables that have chunk jobs (partition name starts with "chunk:")
	// and at least one RETRYING chunk
	type tableInfo struct {
		hasRetrying bool
		chunkIDs    []int64 // all chunk job IDs for this table
	}
	tables := make(map[string]*tableInfo)

	for _, j := range jobs {
		if !strings.HasPrefix(j.Partition, "chunk:") {
			continue
		}
		info, ok := tables[j.TableName]
		if !ok {
			info = &tableInfo{}
			tables[j.TableName] = info
		}
		info.chunkIDs = append(info.chunkIDs, j.ID)
		if j.State == jobstore.JobRetrying {
			info.hasRetrying = true
		}
	}

	for tableName, info := range tables {
		if !info.hasRetrying {
			continue
		}
		// Truncate the PG table to remove partial chunk data
		e.logger.Info("truncating chunked table for clean retry",
			"table", tableName,
			"chunks", len(info.chunkIDs))
		if err := e.writer.TruncateTable(ctx, tableName); err != nil {
			e.logger.Warn("truncate failed for chunked table retry",
				"table", tableName, "error", err)
			continue
		}
		// Reset ALL chunks (including COMPLETED ones) to RETRYING
		for _, jobID := range info.chunkIDs {
			if err := e.store.UpdateState(ctx, jobID, jobstore.JobRetrying); err != nil {
				e.logger.Warn("failed to reset chunk job", "job_id", jobID, "error", err)
			}
		}
	}
	return nil
}

// taskKey generates a unique key for a task in the job store.
// jobPartitionKey returns the partition string stored in the job store.
func jobPartitionKey(task TableTask) string {
	partition := ""
	if task.Partition != nil {
		partition = task.Partition.Name
	}
	if task.Chunk != nil {
		if partition != "" {
			partition += fmt.Sprintf(":chunk:%d", task.Chunk.ID)
		} else {
			partition = fmt.Sprintf("chunk:%d", task.Chunk.ID)
		}
	}
	return partition
}

func taskKey(task TableTask) string {
	key := task.Table.Name
	if task.Partition != nil {
		key += ":" + task.Partition.Name
	}
	if task.Chunk != nil {
		key += ":" + fmt.Sprintf("chunk:%d", task.Chunk.ID)
	}
	return key
}
