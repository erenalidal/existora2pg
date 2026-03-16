# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**existora2pg** is a production-grade Oracle → PostgreSQL migration tool built in Go. Designed for multi-terabyte enterprise databases with partitioned tables, billions of rows, and migrations that may run for days. Prioritizes control, reliability, resumability, and observability over automation magic.

## Technology Stack

- **Language:** Go 1.22+
- **Oracle driver:** godror (via Oracle OCI)
- **PostgreSQL driver:** jackc/pgx v5 (native binary COPY protocol)
- **Job state:** SQLite via modernc.org/sqlite (pure Go, no CGO dependency for SQLite)
- **CLI:** cobra
- **Logging:** slog (stdlib, structured JSON/text)
- **Config:** YAML (gopkg.in/yaml.v3)
- **Web UI:** React 19 + TypeScript + Vite (embedded in Go binary via go:embed)
- **HTTP API:** chi router, SSE for real-time progress
- **Desktop:** Wails v2 (native window: macOS/Windows/Linux)

## Build & Development Commands

```bash
# Build
make build
# or: go build -o bin/existora ./cmd/existora/

# Run tests
go test ./...

# Run single package tests
go test ./internal/schema/...

# Run with verbose output
go test -v -run TestTypemap ./internal/schema/...

# Lint
golangci-lint run

# Docker test databases (Oracle Free 23ai + PostgreSQL 16)
make docker-up      # start and wait for Oracle readiness
make docker-down    # stop
make docker-reset   # destroy volumes and restart

# Web UI (embedded server mode)
make ui             # build React frontend into pkg/api/static/
make build-all      # build frontend + Go binary together
./bin/existora serve --port 9740   # start web UI server

# Frontend development (hot reload)
cd web/frontend && npm run dev     # Vite dev server on :5173, proxies /api to :9740
./bin/existora serve --cors http://localhost:5173   # API server with CORS

# Desktop app (Wails v2 — native window)
make desktop        # build frontend + desktop binary
make desktop-dev    # dev mode with hot reload (requires wails CLI)
# Cross-platform:
make desktop-darwin-arm64
make desktop-darwin-amd64
make desktop-linux-amd64
make desktop-windows-amd64

# Windows distribution (clean build + zip)
make dist-windows   # cleans old artifacts, builds ui+desktop+cli, creates zip
# Output: dist/existora2pg-windows-amd64.zip

# Run the tool
./bin/existora migrate --config config.yaml
./bin/existora migrate --config config.yaml --dry-run   # DDL preview only
./bin/existora extract --config config.yaml -o schema.json
./bin/existora status --config config.yaml
./bin/existora status --config config.yaml --table CUSTOMERS
./bin/existora resume --config config.yaml
./bin/existora validate --config config.yaml
```

## Testing Strategy

- **Unit tests:** `go test ./...` — 8 test files covering config, oracle, schema, postgres, jobstore, migration, transform
- **Integration tests:** Require Docker containers (`make docker-up`). Test config: `testdata/config_valid.yaml`
- **Test data:** Oracle init scripts in `testdata/oracle-init/` create 7 tables (simple, FK, partitioned 12-month, exotic types, constraints, big tables) with seed data up to 5.6M rows
- **Oracle connection:** `oracle://migtest:migtest123@localhost:1521/FREEPDB1`
- **PostgreSQL connection:** `postgres://migtest:migtest123@localhost:5432/migtest`

## Architecture

### Module Dependency Flow

```
cmd/existora → internal/cli → internal/migration/engine
                                  ├── internal/oracle     (metadata extraction + streaming row reader)
                                  ├── internal/postgres   (DDL generation + binary COPY writer)
                                  ├── internal/schema     (neutral schema model + type mapping)
                                  ├── internal/transform  (row-level data transformation)
                                  ├── internal/jobstore   (SQLite job state persistence)
                                  ├── internal/validate   (row count + checksum comparison)
                                  └── internal/logging    (slog setup)
```

### Key Design Principles

1. **Interface-driven:** `MetadataExtractor`, `RowReader`, `DataWriter`, `JobStore` are interfaces. The `Engine` depends only on interfaces for testability.
2. **Schema model is the bridge:** `internal/schema/model.go` defines database-agnostic types (`Table`, `Column`, `Partition`, etc.) that all modules share. Oracle extractor fills this model, PostgreSQL generator reads from it.
3. **Phases are separated:** DDL creation → data copy → constraint/index creation → validation. Indexes and FKs are created AFTER data loading for performance (5-10x faster).
4. **Per-partition jobs:** Partitioned tables are split into per-partition migration tasks for granular resume and parallel execution.
5. **Chunked COPY:** Data is written in batch-size transactions (default 50K rows) to bound WAL pressure and prevent progressive slowdown on large tables.

### Migration Execution Order

```
1. EXTRACT    → Oracle metadata via ALL_TABLES, ALL_TAB_COLUMNS, etc.
2. PLAN       → Topological sort by FK dependencies, expand partitions, resolve ROWID chunks
3. INIT JOBS  → SQLite: one job per table per phase (ddl/data/constraints/validate)
4. DDL        → Serial, FK dependency order. CREATE TABLE + partitions (no indexes yet)
5. DATA       → Parallel worker pool. Oracle streaming read → transform → chunked pgx binary COPY
5.5 UNLOGGED  → If unlogged=true, ALTER TABLE SET LOGGED for all tables
6. CONSTRAINTS → Two-pass: PK+indexes first (all tables), then FK/UNIQUE/CHECK (reverse FK order)
7. SEQUENCES  → CREATE SEQUENCE with MAXVALUE clamping
8. VALIDATE   → Parallel row count comparison (exact for small tables, estimated for large)
```

### Critical Performance Path

The data migration pipeline (Phase 5) is the throughput bottleneck:

```
Oracle (godror, FetchArraySize=5000)
  → channel ([]any rows)
    → transform (type conversion, NULL handling)
      → chunked pgx.CopyFrom (binary COPY, 50K rows per transaction)
        → PostgreSQL
```

- **Always use binary COPY**, never INSERT — 5-10x throughput difference
- **Chunked COPY** (50K rows/tx) prevents unbounded WAL growth and checkpoint storms
- **FetchArraySize** on Oracle side: 1000-5000 optimal range
- **Worker pool** for parallel table/partition migration, configurable via `migration.workers`
- **ROWID chunking** for large tables (DBMS_PARALLEL_EXECUTE or ORA_HASH fallback)
- **Chunk strategies:** `auto` (ROWID→ORA_HASH fallback), `rowid`, `ora_hash`, `offset` (OFFSET/FETCH pagination like ora2pg)

### Oracle → PostgreSQL Type Mapping (`internal/schema/typemap.go`)

Key mappings:
| Oracle | PostgreSQL |
|--------|------------|
| NUMBER(p,0) p≤9 | INTEGER |
| NUMBER(p,0) p≤18 | BIGINT |
| NUMBER(p,s) | NUMERIC(p,s) |
| NUMBER (no precision) | NUMERIC |
| VARCHAR2/NVARCHAR2 | VARCHAR |
| CLOB/NCLOB | TEXT |
| BLOB/RAW | BYTEA |
| DATE | TIMESTAMP(0) |
| TIMESTAMP(p) | TIMESTAMP(p) |
| TIMESTAMP WITH TIME ZONE | TIMESTAMPTZ |
| XMLTYPE | XML |
| FLOAT | DOUBLE PRECISION |

### Job State Machine

```
PENDING → RUNNING → COMPLETED
                  → FAILED → RETRYING → RUNNING → ...
```

State is persisted in SQLite (`migration.state_db_path`). Resume picks up from last COMPLETED job.

### Retry Strategy (3 levels)

1. **Batch retry** (writer.go): 3 retries with exponential backoff (1s, 2s, 4s). Re-acquires PG connection, retries single COPY batch.
2. **Pipeline retry** (engine.go): 3 retries for full Oracle→PG pipeline. Truncates target before retry. Disabled for chunked tables (batch retry only).
3. **Resume** (engine.go): Manual via CLI/UI. FAILED+RUNNING jobs → RETRYING. For chunked tables: truncates entire table, re-runs ALL chunks.

### Partition Strategy

Oracle range partitions → PostgreSQL native partitioning:
- `CREATE TABLE t (...) PARTITION BY RANGE(col)`
- `CREATE TABLE t_p202501 PARTITION OF t FOR VALUES FROM (...) TO (...)`
- Partition names follow deterministic pattern: `{table}_p{YYYYMM}` or from Oracle metadata
- Partitions are created before data loading, data is loaded per-partition
- Smart partition pruning: value-based range filter auto-detects overlapping partitions
- **Auto sub-chunking:** Large partitions auto-split via `ORA_HASH(ROWID, N) = chunk_id` for parallel reads. Chunk count = `min(workers*2, numRows/batchSize)`. Each sub-chunk scans partition with hash filter.

## Project Structure

```
cmd/existora/main.go          → CLI entrypoint
cmd/desktop/main.go           → Wails v2 desktop entrypoint (native window)
internal/cli/                  → cobra commands (root, extract, migrate, status, resume, validate, serve)
internal/config/               → YAML config parsing and validation
internal/oracle/               → Oracle connection, metadata extractor, streaming reader, ROWID chunker
internal/schema/               → Neutral schema model (Table, Column, Index, Partition, Sequence) + typemap
internal/postgres/             → PG connection, DDL generator, partition DDL, binary COPY writer (chunked)
internal/migration/            → Engine orchestration (8-phase), migration plan (FK topo sort), worker pool
internal/transform/            → Row-level transforms (Oracle quirks: empty string vs NULL, LOB, etc.)
internal/jobstore/             → SQLite job store (state persistence, resume support)
internal/validate/             → Post-migration validation (row count, checksum)
internal/logging/              → slog structured logger setup
pkg/api/                       → REST API server (chi), SSE progress, project CRUD, embedded React SPA
web/frontend/                  → React + TypeScript + Vite frontend (builds to pkg/api/static/)
```

## Configuration

Config is YAML. Key sections:
- `oracle.dsn`, `oracle.schema`, `oracle.max_conns`
- `postgres.dsn`, `postgres.schema`, `postgres.max_conns`, `postgres.unlogged`
- `migration.workers` — parallel worker count (default: min(CPU, 8))
- `migration.batch_size` — rows per COPY batch (default: 50000)
- `migration.fetch_size` — Oracle fetch array size (default: 5000)
- `migration.include_tables` / `exclude_tables` — glob patterns
- `migration.table_overrides` — per-table: WHERE clause, partition filters, target mode, skip flags, custom batch/fetch/parallel
- `migration.state_db_path` — SQLite file for job tracking
- `migration.resume` — boolean, resume from last failed run
- `migration.naming_convention` — lowercase/uppercase/keep_original

## Conventions

- All Oracle queries use `ALL_*` views (not `DBA_*`) unless schema requires DBA access
- Table and column names are stored uppercase (Oracle convention) in schema model, lowercased when generating PG DDL
- Error handling: wrap with `fmt.Errorf("context: %w", err)`, never swallow errors
- Context propagation: all long-running operations accept `context.Context` for cancellation
- Logging: use `slog.With("table", name, "partition", p)` for structured context

## Web UI Architecture

The UI is a React SPA embedded in the Go binary via `go:embed`.

### API Endpoints (`pkg/api/`)
```
POST/GET    /api/projects              → project CRUD
GET/PUT/DEL /api/projects/:id          → single project operations
POST        /api/projects/:id/test-*   → connection testing (oracle, postgres)
GET         /api/projects/:id/schema   → Oracle metadata extraction
PUT         /api/projects/:id/mappings → naming convention + type/name overrides
GET         /api/projects/:id/ddl-preview → DDL generation preview
POST        /api/projects/:id/migrate  → start migration
POST        /api/projects/:id/resume   → resume failed
POST        /api/projects/:id/cancel   → cancel running
POST        /api/projects/:id/reset    → drop PG tables + clear all runs
GET         /api/projects/:id/status   → run summary with timing
GET         /api/projects/:id/jobs     → job list with started_at/finished_at
GET         /api/projects/:id/progress → SSE real-time stream
GET         /api/projects/:id/target-status → PG-side table existence + row counts
POST        /api/projects/:id/validate → row count validation
GET         /api/projects/:id/pg-settings → live PG configuration check
GET         /api/projects/:id/oracle-settings → live Oracle configuration + schema overview
```

### Frontend Pages (`web/frontend/src/pages/`)
- **ProjectList** — project cards, create/delete
- **ProjectCreate** — connection config, migration settings (workers, batch, fetch, naming), connection test
- **ProjectDashboard** — overview, status summary, quick actions
- **ObjectSelector** — table/partition selection with include/exclude patterns
- **SchemaExplorer** — tree view of tables/columns/partitions with type mapping
- **DDLPreview** — generated DDL with copy/filter, mode badges, "Start Migration" navigation
- **MigrationRun** — start/cancel/resume/reset, live SSE progress, job table with progress bars
- **ValidationPage** — row count comparison table
- **RecommendationsPage** — live PG/Oracle settings check, schema overview, resource sizing tips

## Windows Paketleme Rehberi

### KRITIK: Frontend Embed Yapısı

Desktop binary (`cmd/desktop/`) **iki ayrı** embed kaynağı kullanır:

1. **Wails frontend** → `cmd/desktop/frontend/dist/` (Wails asset server)
2. **pkg/api static** → `pkg/api/static/` (`go:embed static/*` — HTTP serve modu)

`cmd/desktop/` Go paketi `pkg/api` paketini import ettiği için, **her iki frontend build'i de mevcut olmalı**, yoksa Go compiler `no matching files found` hatası verir.

### Doğru Paketleme Komutu

```bash
# TEK KOMUT — her şeyi temizler, build eder, zip oluşturur:
make dist-windows
```

Bu komut sırasıyla:
1. `clean-dist` — eski dist, frontend output, windows exe'leri siler
2. `ui` — `pkg/api/static/` build (serve embed için)
3. `desktop-ui` — `cmd/desktop/frontend/dist/` build (Wails embed için)
4. `desktop-windows-amd64` — Windows desktop exe (GUI, -H windowsgui)
5. `build-windows` — Windows CLI exe
6. zip oluşturur: `dist/existora2pg-windows-amd64.zip`

### Oracle Instant Client DLL'leri

Windows'ta Oracle bağlantısı için DLL'ler gerekir. Bunları `dist/oracle-dlls/` klasörüne koy — `make dist-windows` otomatik olarak paketin `lib/oracle/` klasörüne kopyalar.

Gerekli DLL'ler (Oracle Instant Client Basic Light):
- `oci.dll`
- `oraociicus.dll`
- `orannz.dll`
- `legacy.dll`
- `fips.dll`
- `pkcs11.dll`
- `extks.dll`

`dist/oracle-dlls/` yoksa veya boşsa, paket DLL'siz oluşur.

### Doğrulama

Build sonrası frontend'in güncel olduğunu doğrulamak için:
```bash
# Wails embed'de split-field kodu var mı?
grep -c "Service Name" cmd/desktop/frontend/dist/assets/index-*.js
# 1 dönmeli

# pkg/api static'te de var mı?
grep -c "Service Name" pkg/api/static/assets/index-*.js
# 1 dönmeli
```

### Sık Yapılan Hatalar

| Hata | Sonuç | Çözüm |
|------|-------|-------|
| `make desktop-windows-amd64` tek başına çalıştırmak | `pkg/api/static/` eksik → derleme hatası | `make dist-windows` kullan |
| Frontend değişikliği sonrası sadece Go build | Exe içinde eski JS embed kalır | Önce `make ui desktop-ui` sonra Go build |
| `dist/` elle düzenleyip zip'lemek | Eski exe veya eksik dosya | Her zaman `make dist-windows` kullan |
| DLL'leri `dist/existora2pg-windows-amd64/lib/oracle/` içine koymak | `clean-dist` ile silinir | `dist/oracle-dlls/` içine koy (kalıcı) |
