package schema

import "fmt"

// Schema represents the complete Oracle schema metadata extracted for migration.
type Schema struct {
	Owner              string
	Tables             []Table
	Sequences          []Sequence
	Views              []View
	MaterializedViews  []MaterializedView
	Triggers           []Trigger
	Procedures         []Procedure
}

// View represents an Oracle view (not migrated automatically, shown for manual review).
type View struct {
	Owner      string
	Name       string
	Definition string
}

// MaterializedView represents an Oracle materialized view (manual review required).
type MaterializedView struct {
	Owner       string
	Name        string
	Definition  string
	RefreshMode string // FORCE, FAST, COMPLETE, NEVER
}

// Trigger represents an Oracle trigger (manual review required for PL/SQL → PL/pgSQL).
type Trigger struct {
	Owner       string
	Name        string
	TableName   string // table it's attached to
	TriggerType string // BEFORE/AFTER/INSTEAD OF
	Event       string // INSERT/UPDATE/DELETE
	Definition  string // trigger body
}

// Procedure represents an Oracle stored procedure, function, or package (manual review required).
type Procedure struct {
	Owner      string
	Name       string
	ObjectType string // PROCEDURE, FUNCTION, PACKAGE, PACKAGE BODY
	Definition string
}

// Table represents an Oracle table with all its metadata.
type Table struct {
	Owner        string
	Name         string
	Columns      []Column
	PrimaryKey   *Constraint
	Constraints  []Constraint
	Indexes      []Index
	Partitioning *PartitionInfo
	Comment      string
	NumRows      int64   // estimated row count from Oracle stats
	SizeMB       float64 // table size in MB from Oracle segments
}

// QualifiedName returns OWNER.TABLE_NAME.
func (t Table) QualifiedName() string {
	if t.Owner == "" {
		return t.Name
	}
	return fmt.Sprintf("%s.%s", t.Owner, t.Name)
}

// IsPartitioned returns true if the table has partition metadata.
func (t Table) IsPartitioned() bool {
	return t.Partitioning != nil && len(t.Partitioning.Partitions) > 0
}

// Column represents a table column.
type Column struct {
	Name        string
	OracleType  string // raw Oracle type (e.g., "NUMBER", "VARCHAR2")
	PGType      string // mapped PostgreSQL type (filled by typemap)
	Precision   *int
	Scale       *int
	Length      *int
	Nullable    bool
	DefaultExpr *string
	Position    int
}

// ConstraintType identifies the kind of constraint.
type ConstraintType string

const (
	ConstraintPK    ConstraintType = "P"
	ConstraintFK    ConstraintType = "R"
	ConstraintUniq  ConstraintType = "U"
	ConstraintCheck ConstraintType = "C"
)

// Constraint represents a table constraint.
type Constraint struct {
	Name       string
	Type       ConstraintType
	Columns    []string
	RefTable   string // FK only
	RefColumns []string
	OnDelete   string // CASCADE, SET NULL, etc.
	Condition  string // CHECK only: the check expression
}

// Index represents a table index.
type Index struct {
	Name    string
	Columns []IndexColumn
	Unique  bool
	Type    string // NORMAL, BITMAP, etc.
	Local   bool   // LOCAL partitioned index
}

// IndexColumn represents a column within an index.
type IndexColumn struct {
	Name       string
	Descending bool
	Position   int
}

// PartitionStrategy represents the partitioning type.
type PartitionStrategy string

const (
	PartitionRange PartitionStrategy = "RANGE"
	PartitionList  PartitionStrategy = "LIST"
	PartitionHash  PartitionStrategy = "HASH"
)

// PartitionInfo describes partitioning metadata for a table.
type PartitionInfo struct {
	Strategy   PartitionStrategy
	KeyColumns []string
	Partitions []Partition
}

// Partition represents a single partition of a partitioned table.
type Partition struct {
	Name      string
	HighValue string // Oracle HIGH_VALUE expression (e.g., "TO_DATE(' 2024-02-01'...)")
	Position  int
	NumRows   int64   // estimated row count from Oracle partition stats
	SizeMB    float64 // partition size in MB
}

// Sequence represents an Oracle sequence.
type Sequence struct {
	Name      string
	MinValue  int64
	MaxValue  int64
	Increment int64
	LastValue int64
	CacheSize int64
	Cycle     bool
}
