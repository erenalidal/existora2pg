package config

import (
	"fmt"
	"os"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Default values for migration configuration.
const (
	DefaultMaxWorkers = 8
	DefaultBatchSize  = 50000
	DefaultFetchSize  = 5000
)

// Config is the root configuration for existora2pg.
type Config struct {
	Oracle    OracleConfig    `yaml:"oracle"`
	Postgres  PostgresConfig  `yaml:"postgres"`
	Migration MigrationConfig `yaml:"migration"`
	Logging   LoggingConfig   `yaml:"logging"`
}

// OracleConfig holds Oracle connection settings.
type OracleConfig struct {
	DSN      string `yaml:"dsn"`
	Schema   string `yaml:"schema"`
	MaxConns int    `yaml:"max_conns"`
}

// PostgresConfig holds PostgreSQL connection settings.
type PostgresConfig struct {
	DSN        string `yaml:"dsn"`
	Schema     string `yaml:"schema"`
	MaxConns   int    `yaml:"max_conns"`
	DropTarget bool   `yaml:"drop_target"`
	Unlogged   bool   `yaml:"unlogged"` // create tables as UNLOGGED for faster bulk load (no WAL), then convert to LOGGED after
}

// MigrationConfig holds migration behavior settings.
// ChunkStrategy determines how large tables/partitions are split for parallel reads.
type ChunkStrategy string

const (
	ChunkStrategyOraHash  ChunkStrategy = "ora_hash"    // ORA_HASH(ROWID, N) — no privileges needed, but N× I/O
	ChunkStrategyRowID    ChunkStrategy = "rowid"        // ROWID range via DBMS_PARALLEL_EXECUTE — 1× I/O, needs EXECUTE on DBMS_PARALLEL_EXECUTE
	ChunkStrategyOffset   ChunkStrategy = "offset"       // OFFSET/FETCH pagination — no privileges, universally compatible, but O(n²) for large tables
	ChunkStrategyAuto     ChunkStrategy = "auto"         // try ROWID first, fall back to ORA_HASH
)

type MigrationConfig struct {
	Workers        int                       `yaml:"workers"`
	BatchSize      int                       `yaml:"batch_size"`
	MaxBatchBytes  int64                     `yaml:"max_batch_bytes"` // max bytes per COPY batch (default: 64MB)
	FetchSize      int                       `yaml:"fetch_size"`
	ChunkStrategy  ChunkStrategy             `yaml:"chunk_strategy"` // auto | ora_hash | rowid (default: auto)
	IncludeTables    []string                  `yaml:"include_tables"`
	ExcludeTables    []string                  `yaml:"exclude_tables"`
	IncludeSequences []string                  `yaml:"include_sequences"` // empty = all sequences; explicit list to select specific ones
	TableOverrides map[string]TableOverride  `yaml:"table_overrides"`
	StateDBPath    string                    `yaml:"state_db_path"`
	Resume         bool                      `yaml:"resume"`
	ValidateAfter  bool                      `yaml:"validate_after"`
	ChecksumCols   bool                      `yaml:"checksum_cols"`
	NamingConvention string                   `yaml:"naming_convention"` // lowercase | uppercase | keep_original
	TypeOverrides    map[string]string         `yaml:"type_overrides"`   // TABLE.COLUMN -> pg_type
	NameOverrides    map[string]string         `yaml:"name_overrides"`   // TABLE -> pg_name, TABLE.COLUMN -> pg_col
}

// TargetMode controls what happens to the target table before migration.
type TargetMode string

const (
	TargetModeRecreate          TargetMode = "recreate"           // DROP CASCADE + CREATE (clean slate)
	TargetModeTruncate          TargetMode = "truncate"           // TRUNCATE (keep structure, delete data)
	TargetModeAppend            TargetMode = "append"             // INSERT into existing table (incremental)
	TargetModeSkip              TargetMode = "skip"               // Don't touch this table
	TargetModeTruncatePartition TargetMode = "truncate_partition" // TRUNCATE only migrated partitions
)

// TableOverride provides per-table migration overrides.
type TableOverride struct {
	Where           string     `yaml:"where"`
	PartitionFilter []string   `yaml:"partition_filter"`  // explicit partition names: ["P202401", "P202402"]
	PartitionRange  *RangeFilter `yaml:"partition_range"` // value-based: auto-detect overlapping partitions
	TargetName      string     `yaml:"target_name"`
	TargetMode      TargetMode `yaml:"target_mode"`       // recreate, truncate, append, skip, truncate_partition

	// Feature toggles — disable slow operations for large tables
	SkipIndexes     bool   `yaml:"skip_indexes"`      // deprecated: use index_mode instead
	IndexMode       string `yaml:"index_mode"`         // create (default), skip, concurrent
	SkipConstraints bool   `yaml:"skip_constraints"`
	SkipValidation  bool   `yaml:"skip_validation"`

	// Per-table performance tuning
	BatchSize       int `yaml:"batch_size"`
	FetchSize       int `yaml:"fetch_size"`
	MaxParallel     int `yaml:"max_parallel"` // max concurrent partition workers for this table (0 = use global workers)
	ChunkSize       int           `yaml:"chunk_size"`       // rows per ROWID chunk for non-partitioned tables (0 = no chunking, single read)
	ChunkStrategy   ChunkStrategy `yaml:"chunk_strategy"`   // per-table override: auto | ora_hash | rowid
}

// ResolveTargetMode returns the effective target mode for a table.
// Falls back to global DropTarget when not explicitly set.
func (o TableOverride) ResolveTargetMode(globalDropTarget bool) TargetMode {
	if o.TargetMode != "" {
		return o.TargetMode
	}
	if globalDropTarget {
		return TargetModeRecreate
	}
	return TargetModeRecreate // default: recreate for safety
}

const (
	IndexModeCreate     = "create"     // default: CREATE INDEX
	IndexModeSkip       = "skip"       // don't create indexes
	IndexModeConcurrent = "concurrent" // CREATE INDEX CONCURRENTLY (no lock, slower)
)

// ResolveIndexMode returns the effective index mode, with backward compat for skip_indexes.
func (o TableOverride) ResolveIndexMode() string {
	if o.IndexMode != "" {
		return o.IndexMode
	}
	if o.SkipIndexes {
		return IndexModeSkip
	}
	return IndexModeCreate
}

// RangeFilter specifies a value-based partition range.
// The system auto-detects which partitions overlap with [From, To).
// Works with date partitions ("2024-01-01") and numeric partitions ("1000").
type RangeFilter struct {
	From string `yaml:"from"` // inclusive lower bound (e.g., "2024-01-01" or "1000")
	To   string `yaml:"to"`   // exclusive upper bound (e.g., "2024-04-01" or "5000")
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	File   string `yaml:"file"`
}

// Load reads and parses a YAML config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

func applyDefaults(cfg *Config) {
	// Workers first — pool defaults depend on worker count
	if cfg.Migration.Workers <= 0 {
		w := runtime.NumCPU()
		if w > DefaultMaxWorkers {
			w = DefaultMaxWorkers
		}
		cfg.Migration.Workers = w
	}
	if cfg.Oracle.MaxConns <= 0 {
		cfg.Oracle.MaxConns = cfg.Migration.Workers + 4
	}
	if cfg.Postgres.MaxConns <= 0 {
		cfg.Postgres.MaxConns = cfg.Migration.Workers*2 + 4
	}
	if cfg.Migration.BatchSize <= 0 {
		cfg.Migration.BatchSize = DefaultBatchSize
	}
	if cfg.Migration.FetchSize <= 0 {
		cfg.Migration.FetchSize = DefaultFetchSize
	}
	if cfg.Migration.MaxBatchBytes <= 0 {
		cfg.Migration.MaxBatchBytes = 64 << 20 // 64MB default
	}
	if cfg.Migration.StateDBPath == "" {
		cfg.Migration.StateDBPath = "./existora2pg_state.db"
	}
	if cfg.Migration.ChunkStrategy == "" {
		cfg.Migration.ChunkStrategy = ChunkStrategyAuto
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "text"
	}
}

func validate(cfg *Config) error {
	if cfg.Oracle.DSN == "" {
		return fmt.Errorf("oracle.dsn is required")
	}
	if cfg.Oracle.Schema == "" {
		return fmt.Errorf("oracle.schema is required")
	}
	if cfg.Postgres.DSN == "" {
		return fmt.Errorf("postgres.dsn is required")
	}
	if cfg.Migration.Workers < 1 || cfg.Migration.Workers > 64 {
		return fmt.Errorf("migration.workers must be between 1 and 64, got %d", cfg.Migration.Workers)
	}
	return nil
}
