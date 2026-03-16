# existora2pg

Production-grade Oracle to PostgreSQL migration tool. Designed for multi-terabyte enterprise databases with partitioned tables, billions of rows, and migrations that may run for days.

## Features

- **High throughput** — Oracle streaming reads + PostgreSQL binary COPY protocol
- **Partition-aware** — Oracle range partitions → PostgreSQL native partitioning, per-partition parallel migration
- **Auto sub-chunking** — Large partitions automatically split via ORA_HASH for parallel reads
- **ROWID chunking** — Non-partitioned large tables split via DBMS_PARALLEL_EXECUTE or ORA_HASH fallback
- **Resumable** — SQLite-backed job state, resume from last failure without re-migrating completed tables
- **Multi-phase** — DDL → Data → Constraints/Indexes → Sequences → Validation (indexes after data for 5-10x speed)
- **UNLOGGED mode** — Skip WAL during bulk load, convert to LOGGED after (configurable)
- **Real-time progress** — Web UI with SSE live progress, row counts, speed metrics
- **Desktop app** — Native window via Wails v2 (macOS, Windows, Linux)
- **CLI + Web UI** — Full CLI for automation, embedded React SPA for visual management
- **PL/SQL converter** — Oracle PL/SQL → PostgreSQL PL/pgSQL (triggers, procedures, functions, views)
- **Smart naming** — Per-project naming convention (lowercase/uppercase/keep_original) + type/name overrides
- **Validation** — Post-migration row count comparison with exact and estimated modes

## Quick Start

### Prerequisites

- Go 1.22+
- Oracle Instant Client (for godror driver)
- Node.js 20+ (for frontend build)
- PostgreSQL 14+ (target database)

### Build

```bash
# CLI only
make build

# CLI + Web UI (embedded)
make build-all

# Desktop app (native window)
make desktop
```

### Run

```bash
# CLI migration
./bin/existora migrate --config config.yaml

# Dry run (DDL preview only)
./bin/existora migrate --config config.yaml --dry-run

# Web UI server
./bin/existora serve --port 9740

# Check migration status
./bin/existora status --config config.yaml

# Resume failed migration
./bin/existora resume --config config.yaml

# Validate row counts
./bin/existora validate --config config.yaml
```

## Configuration

```yaml
oracle:
  dsn: "oracle://user:pass@host:1521/SERVICE"
  schema: "MY_SCHEMA"
  max_conns: 8

postgres:
  dsn: "postgres://user:pass@host:5432/mydb"
  schema: "public"
  max_conns: 8
  drop_target: true        # DROP + CREATE target tables (clean migration)
  unlogged: true           # UNLOGGED tables during bulk load (faster, no WAL)

migration:
  workers: 8               # parallel worker count
  batch_size: 50000        # rows per COPY batch (also controls partition sub-chunking)
  fetch_size: 5000         # Oracle fetch array size
  state_db_path: "./migration_state.db"
  chunk_strategy: auto     # auto | rowid | ora_hash (see Chunk Strategies below)
  validate_after: true     # run row count validation after migration

  include_tables:          # glob patterns (empty = all tables)
    - "CUSTOMERS*"
    - "ORDERS*"

  exclude_tables:
    - "*_BAK"
    - "*_TMP"

  table_overrides:
    LARGE_TABLE:
      where: "STATUS = 'ACTIVE'"        # filter rows
      batch_size: 100000                 # custom batch size
      fetch_size: 10000                  # custom Oracle fetch size
      max_parallel: 4                    # max concurrent partition workers
      skip_indexes: true                 # skip index creation
      skip_validation: true             # skip row count validation
      target_mode: "truncate"           # recreate | truncate | append | skip | truncate_partition
      chunk_size: 500000                # ROWID chunking for non-partitioned tables
      chunk_strategy: rowid             # per-table strategy override

    PARTITIONED_TABLE:
      partition_filter:                 # explicit partition names
        - "P202401"
        - "P202402"
      # OR value-based range (auto-detects overlapping partitions):
      partition_range:
        from: "2024-01-01"
        to: "2024-07-01"

logging:
  level: "info"            # debug | info | warn | error
  format: "text"           # text | json
  file: "./migration.log"  # optional log file
```

### Chunk Strategies

Large tables and partitions are automatically split into sub-chunks for parallel reads. Two strategies are available:

| Strategy | I/O | Speed | Privileges | How it works |
|----------|-----|-------|-----------|--------------|
| **rowid** | 1× (optimal) | Fastest | EXECUTE on DBMS_PARALLEL_EXECUTE | Splits table into ROWID ranges using Oracle's extent metadata. Each chunk reads only its range. |
| **ora_hash** | N× (each chunk scans full table) | Medium | None | Adds `WHERE ORA_HASH(ROWID, N) = chunk_id` filter. Simple but multiplies I/O. |
| **offset** | O(n²) | Slowest | None | Uses `OFFSET/FETCH` pagination (like ora2pg). Universally compatible but each page re-scans from start. |
| **auto** (default) | Best available | Adaptive | Tries ROWID first | Attempts ROWID strategy, falls back to ORA_HASH if privileges are insufficient. |

**ROWID strategy** is recommended for large tables (100M+ rows). It uses `DBMS_PARALLEL_EXECUTE.CREATE_CHUNKS_BY_ROWID` to split based on data dictionary extents — no table scan required. For partitioned tables, chunks automatically respect partition boundaries and are mapped via `DBMS_ROWID`.

**ORA_HASH strategy** requires no special privileges but each sub-chunk scans the entire table/partition, filtering only its hash bucket. Best for medium tables or when DBMS_PARALLEL_EXECUTE is unavailable.

**Offset strategy** uses `ORDER BY ROWID OFFSET X ROWS FETCH NEXT Y ROWS ONLY` (Oracle 12c+). No special privileges needed, works everywhere, but performance degrades for large tables because each page must skip over all previous rows. Similar to ora2pg's approach. Use this as a last resort when other strategies fail.

**Auto-chunking threshold:** Partitions with fewer than `batch_size × 10` rows are not chunked. Larger partitions get `min(workers × 2, numRows / batchSize)` chunks.

## Architecture

```
Oracle (godror, streaming cursor)
  → channel buffer (FetchArraySize rows)
    → transform (type conversion, Oracle quirks)
      → chunked pgx.CopyFrom (binary COPY, batch_size rows/tx)
        → PostgreSQL
```

### Migration Phases

```
1. EXTRACT      → Oracle metadata via ALL_TABLES, ALL_TAB_COLUMNS, etc.
2. PLAN         → Topological sort by FK dependencies, expand partitions, resolve chunks
3. INIT JOBS    → SQLite: one job per table/partition/chunk per phase
4. DDL          → Serial, FK dependency order. CREATE TABLE + partitions (no indexes)
5. DATA         → Parallel worker pool. Streaming Oracle → transform → binary COPY
5.5 UNLOGGED    → If enabled, ALTER TABLE SET LOGGED for all tables
6. CONSTRAINTS  → Two-pass: PK+indexes first, then FK/UNIQUE/CHECK
7. SEQUENCES    → CREATE SEQUENCE with Oracle MAXVALUE clamping
8. VALIDATE     → Parallel row count comparison
```

### Type Mapping

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

## Web UI

The web UI is a React SPA embedded in the Go binary. Start with `./bin/existora serve`.

**Pages:**
- Project management (create, list, delete)
- Connection testing (Oracle + PostgreSQL)
- Schema explorer (tables, columns, partitions with type mapping)
- DDL preview (generated PostgreSQL DDL)
- Object selector (table/partition selection)
- Migration runner (start, cancel, resume, reset with live SSE progress)
- Validation (row count comparison)
- Recommendations (PG/Oracle settings analysis)

**Development:**
```bash
# Terminal 1: API server
./bin/existora serve --cors http://localhost:5173

# Terminal 2: Frontend hot reload
cd web/frontend && npm run dev
```

## Desktop App

Native desktop application using Wails v2. Bundles the web UI in a native window with full API access.

### Build from Source

```bash
# Prerequisites
go install github.com/wailsapp/wails/v2/cmd/wails@v2.9.2
# Oracle Instant Client must be installed

# macOS (Apple Silicon)
make desktop-darwin-arm64

# macOS (Intel)
make desktop-darwin-amd64

# Linux
make desktop-linux-amd64

# Windows (cross-compile from macOS/Linux, requires mingw-w64)
brew install mingw-w64   # macOS
make desktop-windows-amd64

# Development mode (hot reload)
make desktop-dev
```

### Windows Distribution

The Windows build produces a self-contained distribution package:

```bash
make dist-windows
```

This creates `dist/existora2pg-windows-amd64/` containing:

```
existora-desktop.exe     — Desktop app (native window, no console)
existora.exe             — CLI tool
lib/oracle/
  oci.dll                — Oracle Instant Client (required)
  oraociicus.dll         — Oracle Basic Light (~75MB)
  oraocci21.dll          — Oracle OCCI
  orannzsbb.dll          — Oracle network security
  orasql.dll             — Oracle SQL
```

**Windows requirements:**
- **WebView2 Runtime** — Pre-installed on Windows 10 (1803+) and Windows 11. For Windows Server, install from [Microsoft](https://developer.microsoft.com/en-us/microsoft-edge/webview2/).
- **Oracle Instant Client** — Bundled in `lib/oracle/`. No separate installation needed.

**Cross-compiling for Windows from macOS:**

```bash
# Install cross-compiler
brew install mingw-w64

# Build desktop binary (no console window)
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
  go build -tags production -ldflags "-H windowsgui" \
  -o bin/existora-desktop.exe ./cmd/desktop/

# Build CLI binary
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
  go build -o bin/existora.exe ./cmd/existora/

# Build debug desktop binary (console visible for troubleshooting)
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 CC=x86_64-w64-mingw32-gcc \
  go build -tags production \
  -o bin/existora-desktop-debug.exe ./cmd/desktop/
```

**Oracle Instant Client DLLs:**

Download [Oracle Instant Client Basic Light](https://www.oracle.com/database/technologies/instant-client/winx64-64-downloads.html) for Windows x64 and place the DLLs in `lib/oracle/` next to the executable. Required files:
- `oci.dll` (required)
- `oraociicus.dll` (Basic Light, ~75MB) or `oraociei.dll` (Basic, ~200MB)

The application automatically detects `lib/oracle/` next to the executable and sets the PATH.

### macOS Distribution

```bash
make desktop-darwin-arm64
# Output: bin/existora-desktop-darwin-arm64
```

Requires Oracle Instant Client installed via Homebrew or in `lib/oracle/` next to the binary.

### GitHub Actions CI/CD

The repository includes a GitHub Actions workflow (`.github/workflows/build-desktop.yml`) that:

1. Builds Windows desktop + CLI binaries on `windows-latest`
2. Builds macOS desktop + CLI binaries on `macos-14` (Apple Silicon)
3. Downloads and bundles Oracle Instant Client DLLs
4. Creates distribution packages (zip/tar.gz)
5. Uploads artifacts and creates releases on tag push (`v*`)

## PL/SQL Converter

Built-in Oracle PL/SQL → PostgreSQL PL/pgSQL converter with ~70 conversion patterns:

- Trigger syntax (`CREATE TRIGGER` → `CREATE FUNCTION` + `CREATE TRIGGER`)
- Procedure/function headers (parameter modes, return types)
- Data types (VARCHAR2→VARCHAR, NUMBER→NUMERIC, etc.)
- Built-in functions (NVL→COALESCE, SYSDATE→CURRENT_DATE, DECODE→CASE, etc.)
- Exception handling (Oracle exceptions → PostgreSQL SQLSTATE)
- Cursor declarations, ROWNUM→ROW_NUMBER(), DUAL removal
- REGEXP functions (REGEXP_LIKE→~, REGEXP_SUBSTR→substring, REGEXP_COUNT)
- Date format conversion (Oracle→PostgreSQL format elements)
- PRAGMA removal, DETERMINISTIC→IMMUTABLE, AUTHID→SECURITY
- Warnings for unsupported patterns (CONNECT BY, PIPELINED, UTL_FILE, DBMS_SQL, etc.)

## Testing

```bash
# Unit tests (no external dependencies)
go test ./...

# Verbose with specific test
go test -v -run TestTypemap ./internal/schema/...

# Integration tests (require Docker)
make docker-up
go test -v -tags=integration ./...
make docker-down

# Docker test databases
make docker-up      # Oracle Free 23ai + PostgreSQL 16
make docker-down    # stop containers
make docker-reset   # destroy volumes + restart
```

## Project Structure

```
cmd/
  existora/main.go            CLI entrypoint
  desktop/
    main.go                   Wails v2 desktop entrypoint
    app.go                    Wails bindings
    oracle.go                 Oracle lib path detection + re-exec
    exec_windows.go           Windows process re-exec
    exec_unix.go              Unix execve
    webview2_windows.go       WebView2 runtime installer (Windows)
    webview2_other.go         No-op stub (non-Windows)

internal/
  cli/                        Cobra commands
  config/                     YAML config parsing
  oracle/                     Oracle connection, metadata extractor, streaming reader, ROWID chunker
  schema/                     Neutral schema model + type mapping
  postgres/                   DDL generator, partition DDL, binary COPY writer
  migration/                  Engine (8-phase), migration plan (FK topo sort), worker pool
  transform/                  Row-level data transforms
  jobstore/                   SQLite job state persistence
  validate/                   Post-migration validation
  logging/                    slog setup
  plsql/                      PL/SQL → PL/pgSQL converter

pkg/api/                      REST API + SSE + embedded React SPA

web/frontend/                 React + TypeScript + Vite
  src/pages/                  7 pages (projects, schema, DDL, migration, validation, etc.)
  src/api.ts                  API client + SSE streaming
```

## License

Proprietary. All rights reserved.
