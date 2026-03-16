package jobstore

import (
	"context"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_state.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	ctx := context.Background()
	if err := store.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema() error: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestCreateRunAndJobs(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.CreateRun(ctx, "run-001", "abc123"); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	job := &Job{
		RunID:       "run-001",
		TableSchema: "MIGTEST",
		TableName:   "CUSTOMERS",
		Phase:       "data",
	}
	if err := store.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if job.ID == 0 {
		t.Error("expected job.ID to be set")
	}

	jobs, err := store.GetPendingJobs(ctx, "run-001", "data")
	if err != nil {
		t.Fatalf("GetPendingJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 pending job, got %d", len(jobs))
	}
	if jobs[0].TableName != "CUSTOMERS" {
		t.Errorf("table = %q, want CUSTOMERS", jobs[0].TableName)
	}
}

func TestJobStateTransitions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateRun(ctx, "run-002", "hash")
	job := &Job{RunID: "run-002", TableSchema: "S", TableName: "T1", Phase: "data"}
	store.CreateJob(ctx, job)

	// PENDING -> RUNNING
	if err := store.SetRunning(ctx, job.ID); err != nil {
		t.Fatalf("SetRunning: %v", err)
	}

	// RUNNING -> COMPLETED
	if err := store.SetCompleted(ctx, job.ID, 1000, 50000); err != nil {
		t.Fatalf("SetCompleted: %v", err)
	}

	jobs, _ := store.GetJobs(ctx, "run-002", "T1")
	if len(jobs) != 1 || jobs[0].State != JobCompleted {
		t.Fatalf("expected COMPLETED state, got %v", jobs)
	}
	if jobs[0].RowsCopied != 1000 {
		t.Errorf("rows_copied = %d, want 1000", jobs[0].RowsCopied)
	}
}

func TestJobFailAndRetry(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateRun(ctx, "run-003", "hash")
	j1 := &Job{RunID: "run-003", TableSchema: "S", TableName: "T1", Phase: "data"}
	j2 := &Job{RunID: "run-003", TableSchema: "S", TableName: "T2", Phase: "data"}
	store.CreateJob(ctx, j1)
	store.CreateJob(ctx, j2)

	// T1 succeeds, T2 fails
	store.SetRunning(ctx, j1.ID)
	store.SetCompleted(ctx, j1.ID, 500, 25000)
	store.SetRunning(ctx, j2.ID)
	store.SetFailed(ctx, j2.ID, "connection lost")

	// Mark failed as retrying
	count, err := store.MarkFailedAsRetrying(ctx, "run-003")
	if err != nil {
		t.Fatalf("MarkFailedAsRetrying: %v", err)
	}
	if count != 1 {
		t.Errorf("retrying count = %d, want 1", count)
	}

	// T2 should now be RETRYING and appear in pending
	pending, _ := store.GetPendingJobs(ctx, "run-003", "data")
	if len(pending) != 1 || pending[0].TableName != "T2" {
		t.Errorf("expected T2 in pending, got %v", pending)
	}
}

func TestRunSummary(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateRun(ctx, "run-004", "hash")
	for _, name := range []string{"A", "B", "C"} {
		store.CreateJob(ctx, &Job{RunID: "run-004", TableSchema: "S", TableName: name, Phase: "data"})
	}

	summary, err := store.GetRunSummary(ctx, "run-004")
	if err != nil {
		t.Fatalf("GetRunSummary: %v", err)
	}
	if summary.Total != 3 || summary.Pending != 3 {
		t.Errorf("summary = %+v, want 3 total / 3 pending", summary)
	}
}

func TestGetLatestRunID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateRun(ctx, "run-first", "h1")
	store.CreateRun(ctx, "run-second", "h2")

	latest, err := store.GetLatestRunID(ctx)
	if err != nil {
		t.Fatalf("GetLatestRunID: %v", err)
	}
	if latest != "run-second" {
		t.Errorf("latest = %q, want run-second", latest)
	}
}
