package migration

import (
	"context"
	"testing"

	"github.com/erenalidal/existora2pg/internal/config"
	"github.com/erenalidal/existora2pg/internal/oracle"
	"github.com/erenalidal/existora2pg/internal/schema"
)

func TestTopoSort_FKDependency(t *testing.T) {
	tables := []schema.Table{
		{
			Name: "ORDERS",
			Constraints: []schema.Constraint{
				{Name: "FK_ORDERS_CUST", Type: schema.ConstraintFK, RefTable: "CUSTOMERS"},
			},
		},
		{Name: "CUSTOMERS"},
		{Name: "PRODUCTS"},
	}

	sorted := topoSortTables(tables)

	// CUSTOMERS must come before ORDERS
	custIdx, orderIdx := -1, -1
	for i, t := range sorted {
		if t.Name == "CUSTOMERS" {
			custIdx = i
		}
		if t.Name == "ORDERS" {
			orderIdx = i
		}
	}

	if custIdx >= orderIdx {
		t.Errorf("CUSTOMERS (idx=%d) should come before ORDERS (idx=%d)", custIdx, orderIdx)
	}
}

func TestBuildPlan_PartitionedTable(t *testing.T) {
	s := &schema.Schema{
		Tables: []schema.Table{
			{
				Name: "METER_READINGS",
				Partitioning: &schema.PartitionInfo{
					Strategy:   schema.PartitionRange,
					KeyColumns: []string{"READING_DATE"},
					Partitions: []schema.Partition{
						{Name: "P202401", Position: 1},
						{Name: "P202402", Position: 2},
						{Name: "P202403", Position: 3},
					},
				},
			},
		},
	}

	cfg := &config.MigrationConfig{}
	plan := BuildPlan(s, cfg)

	if len(plan.DataTasks) != 3 {
		t.Errorf("expected 3 data tasks (per partition), got %d", len(plan.DataTasks))
	}

	for _, task := range plan.DataTasks {
		if task.Partition == nil {
			t.Error("expected non-nil partition for partitioned table task")
		}
	}
}

func TestBuildPlan_PartitionFilter(t *testing.T) {
	s := &schema.Schema{
		Tables: []schema.Table{
			{
				Name: "METER_READINGS",
				Partitioning: &schema.PartitionInfo{
					Strategy:   schema.PartitionRange,
					KeyColumns: []string{"READING_DATE"},
					Partitions: []schema.Partition{
						{Name: "P202401", Position: 1},
						{Name: "P202402", Position: 2},
						{Name: "P202403", Position: 3},
					},
				},
			},
		},
	}

	cfg := &config.MigrationConfig{
		TableOverrides: map[string]config.TableOverride{
			"METER_READINGS": {
				PartitionFilter: []string{"P202401", "P202403"},
			},
		},
	}

	plan := BuildPlan(s, cfg)

	if len(plan.DataTasks) != 2 {
		t.Fatalf("expected 2 filtered data tasks, got %d", len(plan.DataTasks))
	}
	if plan.DataTasks[0].Partition.Name != "P202401" {
		t.Errorf("first task partition = %s, want P202401", plan.DataTasks[0].Partition.Name)
	}
	if plan.DataTasks[1].Partition.Name != "P202403" {
		t.Errorf("second task partition = %s, want P202403", plan.DataTasks[1].Partition.Name)
	}
}

func TestBuildPlan_PartitionRange(t *testing.T) {
	// Simulate monthly partitions: P202401=[MINVAL, 2024-02-01), P202402=[2024-02-01, 2024-03-01), etc.
	s := &schema.Schema{
		Tables: []schema.Table{
			{
				Name: "METER_READINGS",
				Partitioning: &schema.PartitionInfo{
					Strategy:   schema.PartitionRange,
					KeyColumns: []string{"READING_DATE"},
					Partitions: []schema.Partition{
						{Name: "P202401", HighValue: "TO_DATE(' 2024-02-01 00:00:00', 'SYYYY-MM-DD HH24:MI:SS')", Position: 1},
						{Name: "P202402", HighValue: "TO_DATE(' 2024-03-01 00:00:00', 'SYYYY-MM-DD HH24:MI:SS')", Position: 2},
						{Name: "P202403", HighValue: "TO_DATE(' 2024-04-01 00:00:00', 'SYYYY-MM-DD HH24:MI:SS')", Position: 3},
						{Name: "P202404", HighValue: "TO_DATE(' 2024-05-01 00:00:00', 'SYYYY-MM-DD HH24:MI:SS')", Position: 4},
						{Name: "P202405", HighValue: "TO_DATE(' 2024-06-01 00:00:00', 'SYYYY-MM-DD HH24:MI:SS')", Position: 5},
						{Name: "P202406", HighValue: "TO_DATE(' 2024-07-01 00:00:00', 'SYYYY-MM-DD HH24:MI:SS')", Position: 6},
					},
				},
			},
		},
	}

	tests := []struct {
		name     string
		from     string
		to       string
		wantParts []string
	}{
		{
			name:      "march to june → P202403, P202404, P202405",
			from:      "2024-03-01",
			to:        "2024-06-01",
			wantParts: []string{"P202403", "P202404", "P202405"},
		},
		{
			name:      "january only → P202401",
			from:      "2024-01-01",
			to:        "2024-02-01",
			wantParts: []string{"P202401"},
		},
		{
			name:      "mid-feb to mid-apr → P202402, P202403",
			from:      "2024-02-15",
			to:        "2024-04-15",
			wantParts: []string{"P202402", "P202403", "P202404"},
		},
		{
			name:      "open-ended from april → P202404, P202405, P202406",
			from:      "2024-04-01",
			to:        "",
			wantParts: []string{"P202404", "P202405", "P202406"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.MigrationConfig{
				TableOverrides: map[string]config.TableOverride{
					"METER_READINGS": {
						PartitionRange: &config.RangeFilter{From: tt.from, To: tt.to},
					},
				},
			}

			plan := BuildPlan(s, cfg)

			if len(plan.DataTasks) != len(tt.wantParts) {
				names := make([]string, len(plan.DataTasks))
				for i, task := range plan.DataTasks {
					names[i] = task.Partition.Name
				}
				t.Fatalf("expected %d partitions %v, got %d: %v", len(tt.wantParts), tt.wantParts, len(plan.DataTasks), names)
			}

			for i, task := range plan.DataTasks {
				if task.Partition.Name != tt.wantParts[i] {
					t.Errorf("task[%d] partition = %s, want %s", i, task.Partition.Name, tt.wantParts[i])
				}
			}
		})
	}
}

func TestPartitionOverlaps(t *testing.T) {
	tests := []struct {
		name               string
		pLow, pHigh        string
		fLow, fHigh        string
		want               bool
	}{
		{"exact match", "2024-01-01", "2024-02-01", "2024-01-01", "2024-02-01", true},
		{"no overlap before", "2024-01-01", "2024-02-01", "2024-03-01", "2024-04-01", false},
		{"no overlap after", "2024-03-01", "2024-04-01", "2024-01-01", "2024-02-01", false},
		{"partial overlap start", "2024-01-15", "2024-02-15", "2024-02-01", "2024-03-01", true},
		{"MINVALUE partition", "", "2024-02-01", "2024-01-01", "2024-03-01", true},
		{"MAXVALUE partition", "2024-11-01", "", "2024-10-01", "2024-12-01", true},
		{"open filter end", "2024-03-01", "2024-04-01", "2024-01-01", "", true},
		{"numeric partitions", "100", "200", "150", "250", true},
		{"numeric no overlap", "100", "200", "300", "400", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := partitionOverlaps(tt.pLow, tt.pHigh, tt.fLow, tt.fHigh)
			if got != tt.want {
				t.Errorf("partitionOverlaps(%q,%q,%q,%q) = %v, want %v",
					tt.pLow, tt.pHigh, tt.fLow, tt.fHigh, got, tt.want)
			}
		})
	}
}

func TestExtractValue(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"TO_DATE(' 2024-02-01 00:00:00', 'SYYYY-MM-DD HH24:MI:SS')", "2024-02-01"},
		{"MAXVALUE", ""},
		{"1000", "1000"},
		{"", ""},
	}

	for _, tt := range tests {
		got := extractValue(tt.input)
		if got != tt.want {
			t.Errorf("extractValue(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildPlan_SimpleTable(t *testing.T) {
	s := &schema.Schema{
		Tables: []schema.Table{
			{Name: "CUSTOMERS"},
		},
	}

	cfg := &config.MigrationConfig{}
	plan := BuildPlan(s, cfg)

	if len(plan.DataTasks) != 1 {
		t.Fatalf("expected 1 data task, got %d", len(plan.DataTasks))
	}
	if plan.DataTasks[0].Partition != nil {
		t.Error("expected nil partition for non-partitioned table")
	}
}

func TestBuildPlan_ChunkSize(t *testing.T) {
	s := &schema.Schema{
		Tables: []schema.Table{
			{Owner: "TEST", Name: "BIG_TABLE"},
		},
	}

	cfg := &config.MigrationConfig{
		TableOverrides: map[string]config.TableOverride{
			"BIG_TABLE": {ChunkSize: 100000, MaxParallel: 4},
		},
	}
	plan := BuildPlan(s, cfg)

	// Should have 1 placeholder task and 1 chunkable task
	if len(plan.DataTasks) != 1 {
		t.Fatalf("expected 1 placeholder task, got %d", len(plan.DataTasks))
	}
	if len(plan.ChunkableTasks) != 1 {
		t.Fatalf("expected 1 chunkable task, got %d", len(plan.ChunkableTasks))
	}
	if plan.ChunkableTasks[0].ChunkSize != 100000 {
		t.Errorf("expected chunk_size=100000, got %d", plan.ChunkableTasks[0].ChunkSize)
	}
}

func TestResolveChunks(t *testing.T) {
	s := &schema.Schema{
		Tables: []schema.Table{
			{Owner: "TEST", Name: "BIG_TABLE"},
			{Owner: "TEST", Name: "SMALL_TABLE"},
		},
	}

	cfg := &config.MigrationConfig{
		TableOverrides: map[string]config.TableOverride{
			"BIG_TABLE": {ChunkSize: 100000, MaxParallel: 4, Where: "status = 'A'"},
		},
	}
	plan := BuildPlan(s, cfg)

	// Mock chunker: return ORA_HASH-style chunks
	mockChunker := &oracle.Chunker{} // not used directly
	_ = mockChunker

	// Simulate ResolveChunks by directly manipulating
	// Since we can't create a real Oracle connection in tests,
	// test the plan structure before and after manual chunk injection
	if len(plan.ChunkableTasks) != 1 {
		t.Fatalf("expected 1 chunkable, got %d", len(plan.ChunkableTasks))
	}

	// Simulate what ResolveChunks does: replace placeholder with chunk tasks
	ct := plan.ChunkableTasks[0]
	chunks := []oracle.RowIDChunk{
		{ID: 0}, {ID: 1}, {ID: 2}, {ID: 3},
	}

	chunkTasks := make([]TableTask, len(chunks))
	for j, chunk := range chunks {
		ch := chunk
		where := oracle.BuildChunkWhereClause(ch, len(chunks), ct.Override.Where)
		chunkTasks[j] = TableTask{
			Table:       ct.Table,
			Chunk:       &ch,
			TotalChunks: len(chunks),
			Where:       where,
			MaxParallel: ct.Override.MaxParallel,
		}
	}

	// Replace placeholder
	idx := ct.PlaceholderIdx
	newTasks := make([]TableTask, 0, len(plan.DataTasks)-1+len(chunkTasks))
	newTasks = append(newTasks, plan.DataTasks[:idx]...)
	newTasks = append(newTasks, chunkTasks...)
	newTasks = append(newTasks, plan.DataTasks[idx+1:]...)
	plan.DataTasks = newTasks

	// Should now have 4 chunk tasks + 1 small table = 5
	if len(plan.DataTasks) != 5 {
		t.Fatalf("expected 5 tasks after chunk resolve, got %d", len(plan.DataTasks))
	}

	// First 4 should be chunk tasks for BIG_TABLE
	for i := 0; i < 4; i++ {
		task := plan.DataTasks[i]
		if task.Table.Name != "BIG_TABLE" {
			t.Errorf("task %d: expected BIG_TABLE, got %s", i, task.Table.Name)
		}
		if task.Chunk == nil {
			t.Errorf("task %d: expected chunk, got nil", i)
		}
		if task.MaxParallel != 4 {
			t.Errorf("task %d: expected max_parallel=4, got %d", i, task.MaxParallel)
		}
		// WHERE should include both ORA_HASH and original where
		if task.Where == "" {
			t.Errorf("task %d: expected non-empty WHERE", i)
		}
	}

	// Last task should be SMALL_TABLE
	if plan.DataTasks[4].Table.Name != "SMALL_TABLE" {
		t.Errorf("expected SMALL_TABLE as last task, got %s", plan.DataTasks[4].Table.Name)
	}
}

func TestPartitionChunkCount(t *testing.T) {
	tests := []struct {
		name       string
		numRows    int64
		batchSize  int64
		workers    int
		wantChunks int
	}{
		// Small partition — no chunking
		{"small partition", 100000, 50000, 4, 1},
		// Exactly at threshold (10 batches) — no chunking
		{"at threshold", 500000, 50000, 4, 1},
		// Just above threshold
		{"above threshold 4 workers", 600000, 50000, 4, 12},
		// Large partition — should cap at workers*4
		{"large partition 4 workers", 100_000_000, 50000, 4, 16},  // workers*4=16
		{"large partition 8 workers", 100_000_000, 50000, 8, 32},  // workers*4=32
		// 1 worker — min cap is 8
		{"1 worker min cap", 100_000_000, 50000, 1, 8},
		// 2 workers — workers*4=8 (equals min)
		{"2 workers", 100_000_000, 50000, 2, 8},
		// Minimum chunk count is 2 when above threshold
		{"min 2 chunks", 600000, 50000, 1, 8},  // numRows/batchSize=12, but cap at max(workers*4, 8)=8
		// Zero batchSize defaults to 50000
		{"zero batchSize default", 100_000_000, 0, 4, 16},
		// Boundary: exactly threshold+1 row should chunk
		{"threshold+1 triggers chunking", 500001, 50000, 4, 10},
		// 3 workers: maxChunks = max(3*4, 8) = 12
		{"3 workers max 12", 100_000_000, 50000, 3, 12},
		// Very large numRows with small batchSize — capped at maxChunks
		{"huge rows small batch 4 workers", 1_000_000_000, 1000, 4, 16},
		// Workers = 0: maxChunks = max(0*4, 8) = 8
		{"0 workers max 8", 100_000_000, 50000, 0, 8},
		// Negative batchSize defaults to 50000
		{"negative batchSize default", 100_000_000, -100, 4, 16},
		// Small numRows with large batchSize — below threshold, no chunking
		{"below threshold large batch", 100000, 100000, 8, 1},
		// Exactly at threshold with custom batchSize
		{"at threshold custom batch", 100000, 10000, 4, 1},
		// Just above threshold with custom batchSize
		{"above threshold custom batch", 100001, 10000, 4, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := partitionChunkCount(tt.numRows, tt.batchSize, tt.workers)
			if got != tt.wantChunks {
				t.Errorf("partitionChunkCount(%d, %d, %d) = %d, want %d",
					tt.numRows, tt.batchSize, tt.workers, got, tt.wantChunks)
			}
		})
	}
}

func TestBuildChunkWhereClause(t *testing.T) {
	// ROWID-based chunk
	rowidChunk := oracle.RowIDChunk{ID: 0, StartRID: "AAAR6gAAFAAAACHAAA", EndRID: "AAAR6gAAFAAAACHAAZ"}
	where := oracle.BuildChunkWhereClause(rowidChunk, 4, "")
	if where != "ROWID BETWEEN 'AAAR6gAAFAAAACHAAA' AND 'AAAR6gAAFAAAACHAAZ'" {
		t.Errorf("unexpected ROWID WHERE: %s", where)
	}

	// ROWID with existing WHERE
	where = oracle.BuildChunkWhereClause(rowidChunk, 4, "status = 'A'")
	expected := "ROWID BETWEEN 'AAAR6gAAFAAAACHAAA' AND 'AAAR6gAAFAAAACHAAZ' AND (status = 'A')"
	if where != expected {
		t.Errorf("unexpected ROWID+WHERE: %s", where)
	}

	// ORA_HASH fallback
	hashChunk := oracle.RowIDChunk{ID: 2}
	where = oracle.BuildChunkWhereClause(hashChunk, 4, "")
	if where != "ORA_HASH(ROWID, 3) = 2" {
		t.Errorf("unexpected ORA_HASH WHERE: %s", where)
	}

	// ORA_HASH with existing WHERE
	where = oracle.BuildChunkWhereClause(hashChunk, 4, "status = 'A'")
	expected = "ORA_HASH(ROWID, 3) = 2 AND (status = 'A')"
	if where != expected {
		t.Errorf("unexpected ORA_HASH+WHERE: %s", where)
	}
}

func TestResolveChunks_SmallTable(t *testing.T) {
	// Table smaller than chunk_size should not be chunked
	s := &schema.Schema{
		Tables: []schema.Table{
			{Owner: "TEST", Name: "TINY_TABLE"},
		},
	}

	cfg := &config.MigrationConfig{
		TableOverrides: map[string]config.TableOverride{
			"TINY_TABLE": {ChunkSize: 100000},
		},
	}
	plan := BuildPlan(s, cfg)

	// Mock row counter that returns < chunk_size
	mockCounter := func(_ context.Context, _, _ string) (int64, error) {
		return 50000, nil // less than 100K → no chunking
	}

	err := plan.ResolveChunks(context.Background(), nil, mockCounter)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still have 1 task (not chunked because table is too small)
	if len(plan.DataTasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(plan.DataTasks))
	}
	if plan.DataTasks[0].Chunk != nil {
		t.Error("expected no chunk for small table")
	}
}
