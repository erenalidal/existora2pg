package jobstore

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"
)

//go:embed sql/*.sql
var migrationSQL embed.FS

// ApplySQLitePragmas sets recommended pragmas for SQLite performance.
func ApplySQLitePragmas(db *sql.DB) error {
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=10000",
	} {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("exec %s: %w", p, err)
		}
	}
	return nil
}

// JobState represents the state of a migration job.
type JobState string

const (
	JobPending   JobState = "PENDING"
	JobRunning   JobState = "RUNNING"
	JobFailed    JobState = "FAILED"
	JobCompleted JobState = "COMPLETED"
	JobRetrying  JobState = "RETRYING"
	JobSkipped   JobState = "SKIPPED"
)

// Job represents a single migration task (one table+partition+phase).
type Job struct {
	ID           int64
	RunID        string
	TableSchema  string
	TableName    string
	Partition    string
	Phase        string // "ddl", "data", "constraints", "validate"
	State        JobState
	RowsExpected int64
	RowsCopied   int64
	BytesCopied  int64
	Error        string
	Attempt      int
	StartedAt    *time.Time
	FinishedAt   *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// RunSummary aggregates job counts by state for a run.
type RunSummary struct {
	RunID      string
	Total      int
	Pending    int
	Running    int
	Completed  int
	Failed     int
	Retrying   int
	Skipped    int
	StartedAt  *time.Time
	FinishedAt *time.Time
	TotalRows  int64
}

// Store provides persistence for migration job state.
type Store interface {
	// InitSchema creates the SQLite tables if they don't exist.
	InitSchema(ctx context.Context) error

	// CreateRun creates a new migration run.
	CreateRun(ctx context.Context, runID, configHash string) error

	// CreateJob inserts a PENDING job for a given run.
	CreateJob(ctx context.Context, job *Job) error

	// GetPendingJobs returns PENDING or RETRYING jobs for a given run and phase.
	GetPendingJobs(ctx context.Context, runID, phase string) ([]Job, error)

	// UpdateState transitions a job to a new state.
	UpdateState(ctx context.Context, jobID int64, state JobState) error

	// SetRunning marks a job as RUNNING and records start time.
	SetRunning(ctx context.Context, jobID int64) error

	// SetCompleted marks a job as COMPLETED with row count.
	SetCompleted(ctx context.Context, jobID int64, rowsCopied, bytesCopied int64) error

	// SetFailed marks a job as FAILED with error message.
	SetFailed(ctx context.Context, jobID int64, errMsg string) error

	// MarkFailedAsRetrying resets all FAILED jobs in a run to RETRYING. Returns count.
	MarkFailedAsRetrying(ctx context.Context, runID string) (int64, error)

	// SetSkipped marks a job as skipped (previously completed in an earlier run).
	SetSkipped(ctx context.Context, jobID int64, rowsCopied int64) error

	// GetRunSummary returns aggregate job counts for a run.
	GetRunSummary(ctx context.Context, runID string) (*RunSummary, error)

	// GetLatestRunID returns the most recent run ID.
	GetLatestRunID(ctx context.Context) (string, error)

	// GetJobs returns all jobs for a run, optionally filtered by table name.
	GetJobs(ctx context.Context, runID string, tableName string) ([]Job, error)

	// GetJobsByPhase returns all jobs for a given phase (any state).
	GetJobsByPhase(ctx context.Context, runID, phase string) ([]Job, error)

	// GetCompletedPartitions returns a map of "table:partition" → rows_copied for completed data jobs in the given run.
	GetCompletedPartitions(ctx context.Context, runID string) (map[string]int64, error)

	// DeleteRun removes a run and all its jobs.
	DeleteRun(ctx context.Context, runID string) error

	// Close closes the underlying database connection.
	Close() error
}

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// New creates a new SQLiteStore at the given path.
func New(dbPath string) (*SQLiteStore, error) {
	// Add busy_timeout and WAL mode to DSN for concurrent access
	dsn := dbPath + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}
	if err := ApplySQLitePragmas(db); err != nil {
		return nil, err
	}
	// WAL mode: concurrent readers + single writer. 8 covers API polling + engine writes.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) InitSchema(ctx context.Context) error {
	ddl, err := migrationSQL.ReadFile("sql/001_init.sql")
	if err != nil {
		return fmt.Errorf("read schema sql: %w", err)
	}
	_, err = s.db.ExecContext(ctx, string(ddl))
	if err != nil {
		return fmt.Errorf("exec schema sql: %w", err)
	}
	return nil
}

func (s *SQLiteStore) CreateRun(ctx context.Context, runID, configHash string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO migration_runs (id, config_hash, status) VALUES (?, ?, 'RUNNING')`,
		runID, configHash)
	return err
}

func (s *SQLiteStore) CreateJob(ctx context.Context, job *Job) error {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (run_id, table_schema, table_name, partition, phase, state, rows_expected)
		 VALUES (?, ?, ?, ?, ?, 'PENDING', ?)`,
		job.RunID, job.TableSchema, job.TableName, job.Partition, job.Phase, job.RowsExpected)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	job.ID = id
	return nil
}

func (s *SQLiteStore) GetPendingJobs(ctx context.Context, runID, phase string) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, table_schema, table_name, partition, phase, state,
		        rows_expected, rows_copied, bytes_copied, error_message, attempt,
		        started_at, finished_at
		 FROM jobs WHERE run_id = ? AND phase = ? AND state IN ('PENDING', 'RETRYING')
		 ORDER BY id`, runID, phase)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

// GetJobsByPhase returns all jobs for a given phase (any state).
func (s *SQLiteStore) GetJobsByPhase(ctx context.Context, runID, phase string) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, run_id, table_schema, table_name, partition, phase, state,
		        rows_expected, rows_copied, bytes_copied, error_message, attempt,
		        started_at, finished_at
		 FROM jobs WHERE run_id = ? AND phase = ?
		 ORDER BY id`, runID, phase)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (s *SQLiteStore) UpdateState(ctx context.Context, jobID int64, state JobState) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state = ?, updated_at = datetime('now') WHERE id = ?`,
		string(state), jobID)
	return err
}

func (s *SQLiteStore) SetRunning(ctx context.Context, jobID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state = 'RUNNING', started_at = datetime('now'),
		 attempt = attempt + 1, updated_at = datetime('now') WHERE id = ?`, jobID)
	return err
}

func (s *SQLiteStore) SetCompleted(ctx context.Context, jobID int64, rowsCopied, bytesCopied int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state = 'COMPLETED', rows_copied = ?, bytes_copied = ?,
		 finished_at = datetime('now'), updated_at = datetime('now') WHERE id = ?`,
		rowsCopied, bytesCopied, jobID)
	return err
}

func (s *SQLiteStore) SetSkipped(ctx context.Context, jobID int64, rowsCopied int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state = 'SKIPPED', rows_copied = ?,
		 finished_at = datetime('now'), updated_at = datetime('now') WHERE id = ?`,
		rowsCopied, jobID)
	return err
}

func (s *SQLiteStore) SetFailed(ctx context.Context, jobID int64, errMsg string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state = 'FAILED', error_message = ?,
		 finished_at = datetime('now'), updated_at = datetime('now') WHERE id = ?`,
		errMsg, jobID)
	return err
}

func (s *SQLiteStore) MarkFailedAsRetrying(ctx context.Context, runID string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state = 'RETRYING', error_message = NULL, updated_at = datetime('now')
		 WHERE run_id = ? AND state IN ('FAILED', 'RUNNING')`, runID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *SQLiteStore) GetRunSummary(ctx context.Context, runID string) (*RunSummary, error) {
	summary := &RunSummary{RunID: runID}
	rows, err := s.db.QueryContext(ctx,
		`SELECT state, COUNT(*) FROM jobs WHERE run_id = ? GROUP BY state`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		summary.Total += count
		switch JobState(state) {
		case JobPending:
			summary.Pending = count
		case JobRunning:
			summary.Running = count
		case JobCompleted:
			summary.Completed = count
		case JobFailed:
			summary.Failed = count
		case JobRetrying:
			summary.Retrying = count
		case JobSkipped:
			summary.Skipped = count
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Timing + total_rows via SQL (avoids fetching all jobs into Go)
	var startedAt, finishedAt sql.NullString
	var totalRows sql.NullInt64
	_ = s.db.QueryRowContext(ctx,
		`SELECT MIN(started_at), MAX(finished_at), COALESCE(SUM(CASE WHEN phase='data' THEN rows_copied ELSE 0 END), 0)
		 FROM jobs WHERE run_id = ?`, runID).Scan(&startedAt, &finishedAt, &totalRows)
	if startedAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, startedAt.String); err == nil {
			summary.StartedAt = &t
		}
	}
	if finishedAt.Valid && summary.Running == 0 && summary.Pending == 0 && summary.Retrying == 0 {
		if t, err := time.Parse(time.RFC3339Nano, finishedAt.String); err == nil {
			summary.FinishedAt = &t
		}
	}
	summary.TotalRows = totalRows.Int64

	return summary, nil
}

func (s *SQLiteStore) GetLatestRunID(ctx context.Context) (string, error) {
	var runID string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM migration_runs ORDER BY created_at DESC, rowid DESC LIMIT 1`).Scan(&runID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("no migration runs found")
	}
	return runID, err
}

func (s *SQLiteStore) GetJobs(ctx context.Context, runID string, tableName string) ([]Job, error) {
	query := `SELECT id, run_id, table_schema, table_name, partition, phase, state,
	                 rows_expected, rows_copied, bytes_copied, error_message, attempt,
	                 started_at, finished_at
	          FROM jobs WHERE run_id = ?`
	args := []any{runID}

	if tableName != "" {
		query += " AND table_name = ?"
		args = append(args, tableName)
	}
	query += " ORDER BY id"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (s *SQLiteStore) GetCompletedPartitions(ctx context.Context, runID string) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT table_name, partition, rows_copied FROM jobs
		 WHERE run_id = ? AND phase = 'data' AND (state = 'COMPLETED' OR state = 'SKIPPED')`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int64)
	for rows.Next() {
		var table, partition string
		var rowsCopied int64
		if err := rows.Scan(&table, &partition, &rowsCopied); err != nil {
			return nil, err
		}
		result[table+":"+partition] = rowsCopied
	}
	return result, rows.Err()
}

func (s *SQLiteStore) DeleteRun(ctx context.Context, runID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM jobs WHERE run_id = ?`, runID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM migration_runs WHERE id = ?`, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func scanJobs(rows *sql.Rows) ([]Job, error) {
	var jobs []Job
	for rows.Next() {
		var j Job
		var errMsg sql.NullString
		var startedAt, finishedAt sql.NullTime
		if err := rows.Scan(
			&j.ID, &j.RunID, &j.TableSchema, &j.TableName, &j.Partition,
			&j.Phase, &j.State, &j.RowsExpected, &j.RowsCopied, &j.BytesCopied,
			&errMsg, &j.Attempt, &startedAt, &finishedAt,
		); err != nil {
			return nil, err
		}
		if errMsg.Valid {
			j.Error = errMsg.String
		}
		if startedAt.Valid {
			j.StartedAt = &startedAt.Time
		}
		if finishedAt.Valid {
			j.FinishedAt = &finishedAt.Time
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
