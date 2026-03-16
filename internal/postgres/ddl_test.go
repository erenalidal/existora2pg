package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/erenalidal/existora2pg/internal/schema"
)

func intPtr(v int) *int { return &v }

func TestGenerateCreateTable_Simple(t *testing.T) {
	tbl := schema.Table{
		Name: "CUSTOMERS",
		Columns: []schema.Column{
			{Name: "CUSTOMER_ID", PGType: "BIGINT", Nullable: false, Position: 1},
			{Name: "NAME", PGType: "VARCHAR(200)", Nullable: false, Position: 2},
			{Name: "EMAIL", PGType: "VARCHAR(300)", Nullable: true, Position: 3},
			{Name: "BALANCE", PGType: "NUMERIC(15,2)", Nullable: true, Position: 4},
		},
	}

	ddl := GenerateCreateTable(tbl, "public")

	assertContains(t, ddl, `CREATE TABLE "customers"`)
	assertContains(t, ddl, `"customer_id" BIGINT NOT NULL`)
	assertContains(t, ddl, `"name" VARCHAR(200) NOT NULL`)
	assertContains(t, ddl, `"email" VARCHAR(300)`)
	assertNotContains(t, ddl, "PARTITION BY")
}

func TestGenerateCreateTable_Partitioned(t *testing.T) {
	tbl := schema.Table{
		Name: "METER_READINGS",
		Columns: []schema.Column{
			{Name: "READING_ID", PGType: "BIGINT", Nullable: false},
			{Name: "READING_DATE", PGType: "TIMESTAMP(0)", Nullable: false},
			{Name: "VALUE_KWH", PGType: "NUMERIC(12,4)", Nullable: false},
		},
		Partitioning: &schema.PartitionInfo{
			Strategy:   schema.PartitionRange,
			KeyColumns: []string{"READING_DATE"},
			Partitions: []schema.Partition{
				{Name: "P202401", HighValue: "DATE '2024-02-01'", Position: 1},
			},
		},
	}

	ddl := GenerateCreateTable(tbl, "migtest")

	assertContains(t, ddl, `CREATE TABLE "migtest"."meter_readings"`)
	assertContains(t, ddl, `PARTITION BY RANGE ("reading_date")`)
}

func TestGeneratePartitionDDL(t *testing.T) {
	tbl := schema.Table{
		Name: "METER_READINGS",
		Partitioning: &schema.PartitionInfo{
			Strategy:   schema.PartitionRange,
			KeyColumns: []string{"READING_DATE"},
			Partitions: []schema.Partition{
				{Name: "P202401"},
				{Name: "P202402"},
			},
		},
	}

	bounds := []PartitionBound{
		{Name: "meter_readings_p202401", LowerBound: "'2024-01-01'", UpperBound: "'2024-02-01'"},
		{Name: "meter_readings_p202402", LowerBound: "'2024-02-01'", UpperBound: "'2024-03-01'"},
	}

	ddls := GeneratePartitionDDL(tbl, "public", bounds)
	if len(ddls) != 2 {
		t.Fatalf("expected 2 partition DDLs, got %d", len(ddls))
	}

	assertContains(t, ddls[0], `CREATE TABLE "meter_readings_p202401" PARTITION OF "meter_readings"`)
	assertContains(t, ddls[0], "FOR VALUES FROM ('2024-01-01') TO ('2024-02-01')")
}

func TestGeneratePrimaryKey(t *testing.T) {
	tbl := schema.Table{
		Name: "CUSTOMERS",
		PrimaryKey: &schema.Constraint{
			Name:    "PK_CUSTOMERS",
			Type:    schema.ConstraintPK,
			Columns: []string{"CUSTOMER_ID"},
		},
	}

	ddl := GeneratePrimaryKey(tbl, "public")
	assertContains(t, ddl, `ALTER TABLE "customers" ADD CONSTRAINT "pk_customers" PRIMARY KEY ("customer_id")`)
}

func TestGenerateIndexes(t *testing.T) {
	tbl := schema.Table{
		Name: "CUSTOMERS",
		Indexes: []schema.Index{
			{
				Name:    "IDX_CUSTOMERS_EMAIL",
				Columns: []schema.IndexColumn{{Name: "EMAIL", Position: 1}},
				Unique:  false,
			},
			{
				Name:    "UQ_CUSTOMERS_SKU",
				Columns: []schema.IndexColumn{{Name: "SKU", Position: 1}},
				Unique:  true,
			},
		},
	}

	ddls := GenerateIndexes(tbl, "public")
	if len(ddls) != 2 {
		t.Fatalf("expected 2 index DDLs, got %d", len(ddls))
	}

	assertContains(t, ddls[0], `CREATE INDEX "idx_customers_email" ON "customers" ("email")`)
	assertContains(t, ddls[1], `CREATE UNIQUE INDEX "uq_customers_sku" ON "customers" ("sku")`)
}

func TestGenerateConstraints_FK(t *testing.T) {
	tbl := schema.Table{
		Name: "ORDERS",
		Constraints: []schema.Constraint{
			{
				Name:       "FK_ORDERS_CUSTOMER",
				Type:       schema.ConstraintFK,
				Columns:    []string{"CUSTOMER_ID"},
				RefTable:   "CUSTOMERS",
				RefColumns: []string{"CUSTOMER_ID"},
				OnDelete:   "CASCADE",
			},
		},
	}

	ddls := GenerateConstraints(tbl, "public")
	if len(ddls) != 1 {
		t.Fatalf("expected 1 constraint DDL, got %d", len(ddls))
	}
	assertContains(t, ddls[0], `FOREIGN KEY ("customer_id") REFERENCES "customers" ("customer_id") ON DELETE CASCADE`)
}

func TestGenerateSequence(t *testing.T) {
	seq := schema.Sequence{
		Name:      "SEQ_CUSTOMERS",
		MinValue:  1,
		MaxValue:  9223372036854775806,
		Increment: 1,
		LastValue: 150,
		CacheSize: 20,
		Cycle:     false,
	}

	ddl := GenerateSequence(seq, "public")
	assertContains(t, ddl, `CREATE SEQUENCE IF NOT EXISTS "seq_customers"`)
	assertContains(t, ddl, "INCREMENT BY 1")
	assertContains(t, ddl, "NO MAXVALUE")
	assertContains(t, ddl, "CACHE 20")
	assertContains(t, ddl, "SELECT setval('seq_customers', 150)")
}

func TestGenerateCreateTable_WithSchema(t *testing.T) {
	tbl := schema.Table{
		Name: "TEST",
		Columns: []schema.Column{
			{Name: "ID", PGType: "INTEGER", Nullable: false},
		},
	}

	ddl := GenerateCreateTable(tbl, "myschema")
	assertContains(t, ddl, `CREATE TABLE "myschema"."test"`)
}

func TestGenerateCreateTable_ReservedWords(t *testing.T) {
	tbl := schema.Table{
		Name: "ORDER",
		Columns: []schema.Column{
			{Name: "USER", PGType: "VARCHAR(100)", Nullable: false},
			{Name: "GROUP", PGType: "INTEGER", Nullable: true},
		},
	}

	ddl := GenerateCreateTable(tbl, "public")
	assertContains(t, ddl, `CREATE TABLE "order"`)
	assertContains(t, ddl, `"user" VARCHAR(100) NOT NULL`)
	assertContains(t, ddl, `"group" INTEGER`)
}

func TestCopyStats_NewFields(t *testing.T) {
	stats := CopyStats{
		RowsCopied:    100000,
		BytesCopied:   5242880,
		Duration:      10 * time.Second,
		ReadTime:      4 * time.Second,
		WriteTime:     6 * time.Second,
		BatchCount:    5,
		AvgBatchWrite: 1200 * time.Millisecond,
		AcquireWait:   150 * time.Millisecond,
		CommitWait:    300 * time.Millisecond,
	}

	if stats.RowsCopied != 100000 {
		t.Errorf("RowsCopied = %d, want 100000", stats.RowsCopied)
	}
	if stats.BytesCopied != 5242880 {
		t.Errorf("BytesCopied = %d, want 5242880", stats.BytesCopied)
	}
	if stats.Duration != 10*time.Second {
		t.Errorf("Duration = %v, want 10s", stats.Duration)
	}
	if stats.ReadTime != 4*time.Second {
		t.Errorf("ReadTime = %v, want 4s", stats.ReadTime)
	}
	if stats.WriteTime != 6*time.Second {
		t.Errorf("WriteTime = %v, want 6s", stats.WriteTime)
	}
	if stats.BatchCount != 5 {
		t.Errorf("BatchCount = %d, want 5", stats.BatchCount)
	}
	if stats.AvgBatchWrite != 1200*time.Millisecond {
		t.Errorf("AvgBatchWrite = %v, want 1200ms", stats.AvgBatchWrite)
	}
	if stats.AcquireWait != 150*time.Millisecond {
		t.Errorf("AcquireWait = %v, want 150ms", stats.AcquireWait)
	}
	if stats.CommitWait != 300*time.Millisecond {
		t.Errorf("CommitWait = %v, want 300ms", stats.CommitWait)
	}

	// Zero-value struct should have all fields at zero
	var zero CopyStats
	if zero.AcquireWait != 0 {
		t.Errorf("zero CopyStats.AcquireWait = %v, want 0", zero.AcquireWait)
	}
	if zero.CommitWait != 0 {
		t.Errorf("zero CopyStats.CommitWait = %v, want 0", zero.CommitWait)
	}
	if zero.ReadTime != 0 {
		t.Errorf("zero CopyStats.ReadTime = %v, want 0", zero.ReadTime)
	}
	if zero.WriteTime != 0 {
		t.Errorf("zero CopyStats.WriteTime = %v, want 0", zero.WriteTime)
	}
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q to contain %q", s, substr)
	}
}

func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("expected %q NOT to contain %q", s, substr)
	}
}
