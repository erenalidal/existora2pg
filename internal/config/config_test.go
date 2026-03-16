package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoad_ValidConfig(t *testing.T) {
	content := `
oracle:
  dsn: "oracle://user:pass@localhost:1521/XEPDB1"
  schema: "MIGTEST"
  max_conns: 8
postgres:
  dsn: "postgres://user:pass@localhost:5432/migtest"
  schema: "public"
  max_conns: 8
migration:
  workers: 4
  batch_size: 10000
  include_tables:
    - "CUSTOMERS"
    - "ORDERS"
  table_overrides:
    ORDERS:
      where: "order_date >= DATE '2024-01-01'"
logging:
  level: "debug"
  format: "json"
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Oracle.Schema != "MIGTEST" {
		t.Errorf("Oracle.Schema = %q, want MIGTEST", cfg.Oracle.Schema)
	}
	if cfg.Migration.Workers != 4 {
		t.Errorf("Migration.Workers = %d, want 4", cfg.Migration.Workers)
	}
	if cfg.Migration.BatchSize != 10000 {
		t.Errorf("Migration.BatchSize = %d, want 10000", cfg.Migration.BatchSize)
	}
	if len(cfg.Migration.IncludeTables) != 2 {
		t.Errorf("IncludeTables len = %d, want 2", len(cfg.Migration.IncludeTables))
	}
	if cfg.Migration.TableOverrides["ORDERS"].Where != "order_date >= DATE '2024-01-01'" {
		t.Errorf("ORDERS override where mismatch")
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %q, want debug", cfg.Logging.Level)
	}
}

func TestLoad_Defaults(t *testing.T) {
	content := `
oracle:
  dsn: "oracle://user:pass@localhost:1521/XEPDB1"
  schema: "TEST"
postgres:
  dsn: "postgres://user:pass@localhost:5432/test"
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	expectedWorkers := runtime.NumCPU()
	if expectedWorkers > 8 {
		expectedWorkers = 8
	}
	if cfg.Migration.Workers != expectedWorkers {
		t.Errorf("default Workers = %d, want %d", cfg.Migration.Workers, expectedWorkers)
	}
	// Pool defaults: Oracle = workers+4, Postgres = workers*2+4
	expectedOraConns := expectedWorkers + 4
	expectedPgConns := expectedWorkers*2 + 4
	if cfg.Oracle.MaxConns != expectedOraConns {
		t.Errorf("default Oracle.MaxConns = %d, want %d (= workers+4)", cfg.Oracle.MaxConns, expectedOraConns)
	}
	if cfg.Postgres.MaxConns != expectedPgConns {
		t.Errorf("default Postgres.MaxConns = %d, want %d (= workers*2+4)", cfg.Postgres.MaxConns, expectedPgConns)
	}
	if cfg.Migration.BatchSize != 50000 {
		t.Errorf("default BatchSize = %d, want 50000", cfg.Migration.BatchSize)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("default Level = %q, want info", cfg.Logging.Level)
	}
}

func TestLoad_MissingOracleDSN(t *testing.T) {
	content := `
oracle:
  schema: "TEST"
postgres:
  dsn: "postgres://localhost/test"
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing oracle.dsn")
	}
}

func TestLoad_MissingPostgresDSN(t *testing.T) {
	content := `
oracle:
  dsn: "oracle://localhost:1521/XEPDB1"
  schema: "TEST"
postgres:
  schema: "public"
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing postgres.dsn")
	}
}

func TestLoad_InvalidWorkers(t *testing.T) {
	content := `
oracle:
  dsn: "oracle://localhost:1521/XEPDB1"
  schema: "TEST"
postgres:
  dsn: "postgres://localhost/test"
migration:
  workers: 100
`
	path := writeTemp(t, content)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for workers=100")
	}
}

func TestLoad_PoolSizing_WithWorkers(t *testing.T) {
	tests := []struct {
		name       string
		workers    int
		wantOra    int
		wantPg     int
	}{
		{"1 worker", 1, 5, 6},       // 1+4=5, 1*2+4=6
		{"4 workers", 4, 8, 12},     // 4+4=8, 4*2+4=12
		{"8 workers", 8, 12, 20},    // 8+4=12, 8*2+4=20
		{"16 workers", 16, 20, 36},  // 16+4=20, 16*2+4=36
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := `
oracle:
  dsn: "oracle://user:pass@localhost:1521/XEPDB1"
  schema: "TEST"
postgres:
  dsn: "postgres://user:pass@localhost:5432/test"
migration:
  workers: ` + fmt.Sprintf("%d", tt.workers) + `
`
			path := writeTemp(t, content)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			if cfg.Oracle.MaxConns != tt.wantOra {
				t.Errorf("Oracle.MaxConns = %d, want %d", cfg.Oracle.MaxConns, tt.wantOra)
			}
			if cfg.Postgres.MaxConns != tt.wantPg {
				t.Errorf("Postgres.MaxConns = %d, want %d", cfg.Postgres.MaxConns, tt.wantPg)
			}
		})
	}
}

func TestLoad_PoolSizing_ExplicitOverride(t *testing.T) {
	// Explicit max_conns should NOT be overridden by default formula
	content := `
oracle:
  dsn: "oracle://user:pass@localhost:1521/XEPDB1"
  schema: "TEST"
  max_conns: 30
postgres:
  dsn: "postgres://user:pass@localhost:5432/test"
  max_conns: 50
migration:
  workers: 4
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Oracle.MaxConns != 30 {
		t.Errorf("Oracle.MaxConns = %d, want 30 (explicit)", cfg.Oracle.MaxConns)
	}
	if cfg.Postgres.MaxConns != 50 {
		t.Errorf("Postgres.MaxConns = %d, want 50 (explicit)", cfg.Postgres.MaxConns)
	}
}

func TestLoad_PoolSizing_OracleExplicitPostgresDefault(t *testing.T) {
	// Oracle max_conns is set explicitly, postgres is not → postgres gets default formula
	content := `
oracle:
  dsn: "oracle://user:pass@localhost:1521/XEPDB1"
  schema: "TEST"
  max_conns: 20
postgres:
  dsn: "postgres://user:pass@localhost:5432/test"
migration:
  workers: 4
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Oracle.MaxConns != 20 {
		t.Errorf("Oracle.MaxConns = %d, want 20 (explicit)", cfg.Oracle.MaxConns)
	}
	// Postgres should get default: workers*2+4 = 4*2+4 = 12
	if cfg.Postgres.MaxConns != 12 {
		t.Errorf("Postgres.MaxConns = %d, want 12 (default = workers*2+4)", cfg.Postgres.MaxConns)
	}
}

func TestLoad_PoolSizing_WorkersZeroDefault(t *testing.T) {
	// Workers = 0 → defaults to min(CPU, 8), then pool sizing applied
	content := `
oracle:
  dsn: "oracle://user:pass@localhost:1521/XEPDB1"
  schema: "TEST"
postgres:
  dsn: "postgres://user:pass@localhost:5432/test"
migration:
  workers: 0
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	expectedWorkers := runtime.NumCPU()
	if expectedWorkers > 8 {
		expectedWorkers = 8
	}
	if cfg.Migration.Workers != expectedWorkers {
		t.Errorf("Workers = %d, want %d (min(CPU, 8))", cfg.Migration.Workers, expectedWorkers)
	}
	expectedOra := expectedWorkers + 4
	expectedPg := expectedWorkers*2 + 4
	if cfg.Oracle.MaxConns != expectedOra {
		t.Errorf("Oracle.MaxConns = %d, want %d (workers+4)", cfg.Oracle.MaxConns, expectedOra)
	}
	if cfg.Postgres.MaxConns != expectedPg {
		t.Errorf("Postgres.MaxConns = %d, want %d (workers*2+4)", cfg.Postgres.MaxConns, expectedPg)
	}
}

func TestLoad_PoolSizing_PostgresExplicitOracleDefault(t *testing.T) {
	// Postgres max_conns is set explicitly, oracle is not → oracle gets default formula
	content := `
oracle:
  dsn: "oracle://user:pass@localhost:1521/XEPDB1"
  schema: "TEST"
postgres:
  dsn: "postgres://user:pass@localhost:5432/test"
  max_conns: 40
migration:
  workers: 4
`
	path := writeTemp(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	// Oracle should get default: workers+4 = 4+4 = 8
	if cfg.Oracle.MaxConns != 8 {
		t.Errorf("Oracle.MaxConns = %d, want 8 (default = workers+4)", cfg.Oracle.MaxConns)
	}
	if cfg.Postgres.MaxConns != 40 {
		t.Errorf("Postgres.MaxConns = %d, want 40 (explicit)", cfg.Postgres.MaxConns)
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}
