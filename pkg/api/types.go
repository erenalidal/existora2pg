package api

import (
	"strings"
	"time"
)

// --- Project ---

// ProjectConfig represents a saved migration project.
type ProjectConfig struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	OracleDSN    string          `json:"oracle_dsn"`
	OracleSchema string          `json:"oracle_schema"`
	PgDSN        string          `json:"pg_dsn"`
	PgSchema     string          `json:"pg_schema"`
	Config       MigrationConfig `json:"migration_config"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// ProjectCreateRequest is the payload for creating a new project.
type ProjectCreateRequest struct {
	Name         string          `json:"name"`
	OracleDSN    string          `json:"oracle_dsn"`
	OracleSchema string          `json:"oracle_schema"`
	PgDSN        string          `json:"pg_dsn"`
	PgSchema     string          `json:"pg_schema"`
	Config       MigrationConfig `json:"migration_config"`
}

// ProjectUpdateRequest is the payload for updating a project.
type ProjectUpdateRequest struct {
	Name         string          `json:"name"`
	OracleDSN    string          `json:"oracle_dsn"`
	OracleSchema string          `json:"oracle_schema"`
	PgDSN        string          `json:"pg_dsn"`
	PgSchema     string          `json:"pg_schema"`
	Config       MigrationConfig `json:"migration_config"`
}

// MigrationConfig holds the migration-specific configuration stored as JSON.
type MigrationConfig struct {
	Workers        int                        `json:"workers"`
	BatchSize      int                        `json:"batch_size"`
	FetchSize      int                        `json:"fetch_size"`
	IncludeTables    []string                   `json:"include_tables"`
	ExcludeTables    []string                   `json:"exclude_tables"`
	IncludeSequences []string                   `json:"include_sequences"`
	TableOverrides map[string]TableOverride   `json:"table_overrides"`
	DropTarget     bool                       `json:"drop_target"`
	Unlogged       bool                       `json:"unlogged"`
	ValidateAfter  bool                       `json:"validate_after"`

	// Chunking: "ora_hash" (default, instant), "rowid" (DBMS_PE, slow), "auto" (rowid→ora_hash fallback)
	ChunkStrategy    string            `json:"chunk_strategy"`
	// Naming: "lowercase" (default, ora2pg style), "uppercase", "keep_original"
	NamingConvention string            `json:"naming_convention"`
	// Type overrides: "TABLE.COLUMN" -> "custom_pg_type" (e.g., "CUSTOMERS.ID" -> "UUID")
	TypeOverrides    map[string]string `json:"type_overrides"`
	// Name overrides: "ORACLE_NAME" -> "pg_name" for tables, "TABLE.COLUMN" -> "pg_col" for columns
	NameOverrides    map[string]string `json:"name_overrides"`
}

// TableOverride provides per-table migration overrides.
type TableOverride struct {
	Where           string       `json:"where"`
	PartitionFilter []string     `json:"partition_filter"`
	PartitionRange  *RangeFilter `json:"partition_range,omitempty"`
	TargetName      string       `json:"target_name"`
	TargetMode      string       `json:"target_mode"` // recreate, truncate, append, skip, truncate_partition

	// Feature toggles — disable slow operations for large tables
	SkipIndexes     bool   `json:"skip_indexes"`
	IndexMode       string `json:"index_mode"`        // create, skip, concurrent
	SkipConstraints bool   `json:"skip_constraints"`
	SkipValidation  bool   `json:"skip_validation"`

	// Per-table performance tuning
	BatchSize       int    `json:"batch_size"`
	FetchSize       int    `json:"fetch_size"`
	MaxParallel     int    `json:"max_parallel"`
	ChunkSize       int    `json:"chunk_size"`
	ChunkStrategy   string `json:"chunk_strategy"`    // per-table override: auto, ora_hash, rowid, offset
}

// RangeFilter specifies a value-based partition range.
type RangeFilter struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// --- Connection Test ---

// ConnectionTestResponse is returned from the test-oracle/test-postgres endpoints.
type ConnectionTestResponse struct {
	Success bool     `json:"success"`
	Message string   `json:"message"`
	Latency string   `json:"latency,omitempty"`
	Schemas []string `json:"schemas,omitempty"`
}

// --- Schema ---

// SchemaResponse is the response from the schema extraction endpoint.
type SchemaResponse struct {
	Owner             string                      `json:"owner"`
	Tables            []TableResponse             `json:"tables"`
	Sequences         []SequenceResponse          `json:"sequences"`
	Views             []ViewResponse              `json:"views,omitempty"`
	MaterializedViews []MaterializedViewResponse  `json:"materialized_views,omitempty"`
	Triggers          []TriggerResponse           `json:"triggers,omitempty"`
	Procedures        []ProcedureResponse         `json:"procedures,omitempty"`
}

// TableResponse represents a table in the schema response.
type TableResponse struct {
	Name         string              `json:"name"`
	Columns      []ColumnResponse    `json:"columns"`
	PrimaryKey   *ConstraintResponse `json:"primary_key,omitempty"`
	Constraints  []ConstraintResponse `json:"constraints"`
	Indexes      []IndexResponse     `json:"indexes"`
	Partitioning *PartitionInfoResponse `json:"partitioning,omitempty"`
	Comment      string              `json:"comment,omitempty"`
	NumRows      int64               `json:"num_rows"`
	SizeMB       float64             `json:"size_mb"`
}

// ColumnResponse represents a column.
type ColumnResponse struct {
	Name       string `json:"name"`
	OracleType string `json:"oracle_type"`
	PGType     string `json:"pg_type"`
	Nullable   bool   `json:"nullable"`
	Position   int    `json:"position"`
	Precision  *int   `json:"precision,omitempty"`
	Scale      *int   `json:"scale,omitempty"`
	Length     *int   `json:"length,omitempty"`
}

// ConstraintResponse represents a constraint.
type ConstraintResponse struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Columns    []string `json:"columns"`
	RefTable   string   `json:"ref_table,omitempty"`
	RefColumns []string `json:"ref_columns,omitempty"`
	OnDelete   string   `json:"on_delete,omitempty"`
	Condition  string   `json:"condition,omitempty"`
}

// IndexResponse represents an index.
type IndexResponse struct {
	Name    string               `json:"name"`
	Columns []IndexColumnResponse `json:"columns"`
	Unique  bool                 `json:"unique"`
	Type    string               `json:"type"`
	Local   bool                 `json:"local"`
}

// IndexColumnResponse represents a column within an index.
type IndexColumnResponse struct {
	Name       string `json:"name"`
	Descending bool   `json:"descending"`
	Position   int    `json:"position"`
}

// PartitionInfoResponse describes partitioning.
type PartitionInfoResponse struct {
	Strategy   string              `json:"strategy"`
	KeyColumns []string            `json:"key_columns"`
	Partitions []PartitionResponse `json:"partitions"`
}

// PartitionResponse represents a single partition.
type PartitionResponse struct {
	Name      string  `json:"name"`
	HighValue string  `json:"high_value"`
	Position  int     `json:"position"`
	NumRows   int64   `json:"num_rows"`
	SizeMB    float64 `json:"size_mb"`
}

// SequenceResponse represents a sequence.
type SequenceResponse struct {
	Name      string `json:"name"`
	MinValue  int64  `json:"min_value"`
	MaxValue  int64  `json:"max_value"`
	Increment int64  `json:"increment"`
	LastValue int64  `json:"last_value"`
	CacheSize int64  `json:"cache_size"`
	Cycle     bool   `json:"cycle"`
}

// ViewResponse represents a view (manual review required).
type ViewResponse struct {
	Name         string   `json:"name"`
	Definition   string   `json:"definition"`
	PGDefinition string   `json:"pg_definition,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

// MaterializedViewResponse represents a materialized view (manual review required).
type MaterializedViewResponse struct {
	Name         string   `json:"name"`
	Definition   string   `json:"definition"`
	RefreshMode  string   `json:"refresh_mode"`
	PGDefinition string   `json:"pg_definition,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

// TriggerResponse represents a trigger (manual review required).
type TriggerResponse struct {
	Name         string   `json:"name"`
	TableName    string   `json:"table_name"`
	TriggerType  string   `json:"trigger_type"`
	Event        string   `json:"event"`
	Definition   string   `json:"definition"`
	PGDefinition string   `json:"pg_definition,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

// ProcedureResponse represents a stored procedure/function/package (manual review required).
type ProcedureResponse struct {
	Name         string   `json:"name"`
	ObjectType   string   `json:"object_type"`
	Definition   string   `json:"definition"`
	PGDefinition string   `json:"pg_definition,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

// --- DDL Preview ---

// DDLPreviewResponse contains generated DDL statements.
type DDLPreviewResponse struct {
	Statements []DDLStatement `json:"statements"`
}

// DDLStatement is a single DDL statement with metadata.
type DDLStatement struct {
	Type       string `json:"type"` // "create_table", "partition", "primary_key", "index", "constraint", "sequence"
	Table      string `json:"table"`
	Statement  string `json:"statement"`
	TargetMode string `json:"target_mode,omitempty"` // recreate, truncate, append, skip, truncate_partition
	Skipped    bool   `json:"skipped"`               // true if this DDL won't run based on target_mode
}

// --- Migration ---

// MigrationStartResponse is returned when a migration is started.
type MigrationStartResponse struct {
	RunID   string `json:"run_id"`
	Message string `json:"message"`
}

// --- Job Status ---

// MigrationPhase tracks the current engine phase.
type MigrationPhase struct {
	Phase  string `json:"phase"`  // extracting, planning, ddl, copying, constraints, sequences, validating, completed
	Detail string `json:"detail"` // human-readable detail
}

// JobStatusResponse is the response for the status endpoint.
type JobStatusResponse struct {
	RunID         string           `json:"run_id"`
	Phase         *MigrationPhase  `json:"phase,omitempty"`
	Summary       RunSummary       `json:"summary"`
	Jobs          []JobResponse    `json:"jobs,omitempty"`
	PipelineStats []ProgressEvent  `json:"pipeline_stats,omitempty"`
}

// RunSummary aggregates job counts.
type RunSummary struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Retrying  int `json:"retrying"`
	Skipped   int `json:"skipped"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	TotalRows  int64      `json:"total_rows"`
}

// JobResponse represents a single migration job.
type JobResponse struct {
	ID           int64      `json:"id"`
	TableName    string     `json:"table_name"`
	Partition    string     `json:"partition,omitempty"`
	Phase        string     `json:"phase"`
	State        string     `json:"state"`
	RowsExpected int64      `json:"rows_expected"`
	RowsCopied   int64      `json:"rows_copied"`
	BytesCopied  int64      `json:"bytes_copied"`
	Error        string     `json:"error,omitempty"`
	Attempt      int        `json:"attempt"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

// --- SSE Progress ---

// ProgressEvent is a Server-Sent Event for real-time progress tracking.
type ProgressEvent struct {
	Table         string  `json:"table"`
	Partition     string  `json:"partition"`
	Rows          int64   `json:"rows"`
	Speed         int64   `json:"speed"`
	Percent       float64 `json:"percent"`
	State         string  `json:"state"`
	ReadTimeMs    int64   `json:"read_time_ms,omitempty"`
	WriteTimeMs   int64   `json:"write_time_ms,omitempty"`
	ReadPct       int     `json:"read_pct,omitempty"`
	WritePct      int     `json:"write_pct,omitempty"`
	Batches       int     `json:"batches,omitempty"`
	AvgBatchMs    int64   `json:"avg_batch_ms,omitempty"`
	DurationMs    int64   `json:"duration_ms,omitempty"`
	AcquireWaitMs int64   `json:"acquire_wait_ms,omitempty"`
	CommitWaitMs  int64   `json:"commit_wait_ms,omitempty"`
}

// --- Validation ---

// ValidationResponse contains row-count validation results.
type ValidationResponse struct {
	Results []ValidationResult `json:"results"`
}

// ValidationResult is the validation result for a single table.
type ValidationResult struct {
	TableName   string `json:"table_name"`
	OracleCount int64  `json:"oracle_count"`
	PGCount     int64  `json:"pg_count"`
	Match       bool   `json:"match"`
	Error       string `json:"error,omitempty"`
}

// --- Target Status ---

// TargetStatusResponse contains the PostgreSQL-side status for each table/sequence.
type TargetStatusResponse struct {
	Tables    []TableTargetStatus    `json:"tables"`
	Sequences []SequenceTargetStatus `json:"sequences"`
}

// TableTargetStatus describes a table's state on the PostgreSQL side.
type TableTargetStatus struct {
	Name       string                   `json:"name"`
	Exists     bool                     `json:"exists"`
	RowCount   int64                    `json:"row_count"`
	Partitions []PartitionTargetStatus  `json:"partitions,omitempty"`
}

// PartitionTargetStatus describes a partition child table's state.
type PartitionTargetStatus struct {
	Name     string `json:"name"`
	Exists   bool   `json:"exists"`
	RowCount int64  `json:"row_count"`
}

// SequenceTargetStatus describes a sequence's state on the PostgreSQL side.
type SequenceTargetStatus struct {
	Name   string `json:"name"`
	Exists bool   `json:"exists"`
}

// --- Generic ---

// ErrorResponse is a standard error response.
type ErrorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
}

// maskDSN masks the password portion of a DSN for safe display.
// Handles formats like: user/pass@host:port/service and postgresql://user:pass@host/db
func maskDSN(dsn string) string {
	if dsn == "" {
		return ""
	}

	// URI format: postgresql://user:pass@... or postgres://user:pass@... or oracle://user:pass@...
	if strings.Contains(dsn, "://") {
		return maskURIDSN(dsn)
	}

	// Oracle classic format: user/password@host:port/service
	return maskOracleDSN(dsn)
}

func maskURIDSN(dsn string) string {
	// Find :// then user:pass@
	schemeEnd := 0
	for i := 0; i < len(dsn)-2; i++ {
		if dsn[i] == ':' && dsn[i+1] == '/' && dsn[i+2] == '/' {
			schemeEnd = i + 3
			break
		}
	}
	rest := dsn[schemeEnd:]

	// Find @ separator
	atIdx := -1
	for i, c := range rest {
		if c == '@' {
			atIdx = i
			break
		}
	}
	if atIdx < 0 {
		return dsn // no credentials
	}

	userInfo := rest[:atIdx]
	hostPart := rest[atIdx:]

	// Find : in userInfo to separate user from password
	colonIdx := -1
	for i, c := range userInfo {
		if c == ':' {
			colonIdx = i
			break
		}
	}
	if colonIdx < 0 {
		return dsn // no password
	}

	return dsn[:schemeEnd] + userInfo[:colonIdx] + ":****" + hostPart
}

func maskOracleDSN(dsn string) string {
	// Oracle classic: user/password@host:port/service
	// Find @ first to locate credential boundary
	atIdx := strings.IndexByte(dsn, '@')
	if atIdx < 0 {
		// No @, try masking user/password portion
		slashIdx := strings.IndexByte(dsn, '/')
		if slashIdx < 0 {
			return dsn
		}
		return dsn[:slashIdx] + "/****"
	}

	// Find / in user/password portion (before @)
	userPart := dsn[:atIdx]
	slashIdx := strings.IndexByte(userPart, '/')
	if slashIdx < 0 {
		return dsn // no password separator
	}

	return userPart[:slashIdx] + "/****" + dsn[atIdx:]
}
