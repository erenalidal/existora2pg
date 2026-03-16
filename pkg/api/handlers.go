package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/erenalidal/existora2pg/internal/config"
	"github.com/erenalidal/existora2pg/internal/jobstore"
	"github.com/erenalidal/existora2pg/internal/updater"
	"github.com/erenalidal/existora2pg/internal/migration"
	"github.com/erenalidal/existora2pg/internal/oracle"
	"github.com/erenalidal/existora2pg/internal/plsql"
	"github.com/erenalidal/existora2pg/internal/postgres"
	"github.com/erenalidal/existora2pg/internal/schema"
	"github.com/erenalidal/existora2pg/internal/validate"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Handlers holds all API handler dependencies.
type Handlers struct {
	projects    *ProjectStore
	jobStore    jobstore.Store
	broadcaster *Broadcaster
	logger      *slog.Logger
	stateDBPath string
	appVersion  string

	// Active migrations: projectID -> cancel function
	activeMu   sync.RWMutex
	activeMigs map[string]context.CancelFunc

	// Active migration phases: projectID -> current phase info
	activePhaseMu sync.RWMutex
	activePhases  map[string]MigrationPhase

	// Pipeline stats: persisted in memory for page refreshes
	// key = "table" or "table:partition"
	pipelineStatsMu sync.RWMutex
	pipelineStats   map[string]ProgressEvent
}

// NewHandlers creates a new set of API handlers.
func NewHandlers(projects *ProjectStore, jobStore jobstore.Store, broadcaster *Broadcaster, stateDBPath string, logger *slog.Logger, version string) *Handlers {
	return &Handlers{
		projects:      projects,
		jobStore:      jobStore,
		broadcaster:   broadcaster,
		logger:        logger,
		stateDBPath:   stateDBPath,
		appVersion:    version,
		activeMigs:    make(map[string]context.CancelFunc),
		activePhases:  make(map[string]MigrationPhase),
		pipelineStats: make(map[string]ProgressEvent),
	}
}

// --- Project CRUD ---

func (h *Handlers) CreateProject(w http.ResponseWriter, r *http.Request) {
	var req ProjectCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required", "")
		return
	}
	if req.OracleDSN == "" {
		writeError(w, http.StatusBadRequest, "oracle_dsn is required", "")
		return
	}
	if req.OracleSchema == "" {
		writeError(w, http.StatusBadRequest, "oracle_schema is required", "")
		return
	}
	if req.PgDSN == "" {
		writeError(w, http.StatusBadRequest, "pg_dsn is required", "")
		return
	}

	// Apply defaults
	if req.Config.Workers <= 0 {
		w := runtime.NumCPU()
		if w > config.DefaultMaxWorkers {
			w = config.DefaultMaxWorkers
		}
		req.Config.Workers = w
	}
	if req.Config.BatchSize <= 0 {
		req.Config.BatchSize = config.DefaultBatchSize
	}
	if req.Config.FetchSize <= 0 {
		req.Config.FetchSize = config.DefaultFetchSize
	}
	// Enable validation by default
	if !req.Config.ValidateAfter {
		req.Config.ValidateAfter = true
	}

	project, err := h.projects.Create(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create project", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, maskedProject(*project))
}

func (h *Handlers) ListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := h.projects.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list projects", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, maskedProjects(projects))
}

func (h *Handlers) GetProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := h.projects.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get project", err.Error())
		return
	}
	if project == nil {
		writeError(w, http.StatusNotFound, "project not found", "")
		return
	}
	writeJSON(w, http.StatusOK, maskedProject(*project))
}

func (h *Handlers) ExportProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := h.projects.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get project", err.Error())
		return
	}
	if project == nil {
		writeError(w, http.StatusNotFound, "project not found", "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-settings.json"`, project.Name))
	writeJSON(w, http.StatusOK, *project)
}

func (h *Handlers) UpdateProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req ProjectUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	// If DSN contains masked password (****), preserve the original DSN from DB
	if strings.Contains(req.OracleDSN, "****") || strings.Contains(req.PgDSN, "****") {
		existing, err := h.projects.Get(r.Context(), id)
		if err == nil && existing != nil {
			if strings.Contains(req.OracleDSN, "****") {
				req.OracleDSN = existing.OracleDSN
			}
			if strings.Contains(req.PgDSN, "****") {
				req.PgDSN = existing.PgDSN
			}
		}
	}

	project, err := h.projects.Update(r.Context(), id, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update project", err.Error())
		return
	}
	if project == nil {
		writeError(w, http.StatusNotFound, "project not found", "")
		return
	}
	writeJSON(w, http.StatusOK, maskedProject(*project))
}

func (h *Handlers) DeleteProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	deleted, err := h.projects.Delete(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete project", err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "project not found", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Connection Tests ---

// connectionTestRequest allows testing with an inline DSN (before project is saved).
type connectionTestRequest struct {
	DSN string `json:"dsn"`
}

func (h *Handlers) TestOracle(w http.ResponseWriter, r *http.Request) {
	dsn, err := h.resolveTestDSN(r, "oracle")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	h.logger.Info("testing oracle connection", "dsn", maskDSN(dsn))
	start := time.Now()
	db, err := oracle.OpenPool(ctx, dsn, 1)
	latency := time.Since(start)

	if err != nil {
		h.logger.Warn("oracle connection failed", "error", err, "latency", latency)
		writeJSON(w, http.StatusOK, ConnectionTestResponse{
			Success: false,
			Message: fmt.Sprintf("connection failed: %v", err),
			Latency: latency.Round(time.Millisecond).String(),
		})
		return
	}
	defer db.Close()

	// Fetch available schemas
	schemas := listOracleSchemas(ctx, db)

	h.logger.Info("oracle connection ok", "latency", latency, "schemas", len(schemas))
	writeJSON(w, http.StatusOK, ConnectionTestResponse{
		Success: true,
		Message: "connected successfully",
		Latency: latency.Round(time.Millisecond).String(),
		Schemas: schemas,
	})
}

func (h *Handlers) TestPostgres(w http.ResponseWriter, r *http.Request) {
	dsn, err := h.resolveTestDSN(r, "postgres")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	h.logger.Info("testing postgres connection", "dsn", maskDSN(dsn))
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	start := time.Now()
	pool, err := postgres.OpenPool(ctx, dsn, 1)
	latency := time.Since(start)

	if err != nil {
		h.logger.Warn("postgres connection failed", "error", err, "latency", latency)
		writeJSON(w, http.StatusOK, ConnectionTestResponse{
			Success: false,
			Message: fmt.Sprintf("connection failed: %v", err),
			Latency: latency.Round(time.Millisecond).String(),
		})
		return
	}
	defer pool.Close()

	// Fetch available schemas
	schemas := listPGSchemas(ctx, pool)

	h.logger.Info("postgres connection ok", "latency", latency, "schemas", len(schemas))
	writeJSON(w, http.StatusOK, ConnectionTestResponse{
		Success: true,
		Message: "connected successfully",
		Latency: latency.Round(time.Millisecond).String(),
		Schemas: schemas,
	})
}

// resolveTestDSN returns the DSN to test. It prefers an inline DSN from the
// request body; if absent, falls back to the saved project's DSN.
func (h *Handlers) resolveTestDSN(r *http.Request, kind string) (string, error) {
	// Try reading inline DSN from request body
	var req connectionTestRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.DSN != "" {
		return req.DSN, nil
	}

	// Fallback: load from saved project
	id := chi.URLParam(r, "id")
	project, err := h.projects.Get(r.Context(), id)
	if err != nil || project == nil {
		return "", fmt.Errorf("no DSN provided and project not found")
	}
	switch kind {
	case "oracle":
		return project.OracleDSN, nil
	case "postgres":
		return project.PgDSN, nil
	default:
		return "", fmt.Errorf("unknown connection type: %s", kind)
	}
}

// listOracleSchemas returns available schema names from an Oracle connection.
func listOracleSchemas(ctx context.Context, db *sql.DB) []string {
	rows, err := db.QueryContext(ctx,
		`SELECT username FROM all_users
		 WHERE oracle_maintained = 'N'
		 ORDER BY username`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var schemas []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			schemas = append(schemas, s)
		}
	}
	return schemas
}

// listPGSchemas returns available schema names from a PostgreSQL connection.
func listPGSchemas(ctx context.Context, pool *pgxpool.Pool) []string {
	rows, err := pool.Query(ctx,
		`SELECT schema_name FROM information_schema.schemata
		 WHERE schema_name NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
		   AND schema_name NOT LIKE 'pg_temp_%'
		 ORDER BY schema_name`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var schemas []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			schemas = append(schemas, s)
		}
	}
	return schemas
}

// --- PostgreSQL Settings Check ---

type PGSetting struct {
	Name        string `json:"name"`
	Current     string `json:"current"`
	Recommended string `json:"recommended"`
	Unit        string `json:"unit,omitempty"`
	Status      string `json:"status"` // "ok", "warn", "info"
	Description string `json:"description"`
}

func (h *Handlers) GetPGSettings(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := h.projects.Get(r.Context(), id)
	if err != nil || project == nil {
		writeError(w, http.StatusNotFound, "project not found", "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	pool, err := postgres.OpenPool(ctx, project.PgDSN, 1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot connect to PostgreSQL", err.Error())
		return
	}
	defer pool.Close()

	// Settings to check
	settingsToCheck := []string{
		"wal_level", "max_wal_size", "min_wal_size",
		"synchronous_commit", "checkpoint_timeout",
		"shared_buffers", "work_mem", "maintenance_work_mem",
		"effective_cache_size", "max_connections",
		"autovacuum", "max_wal_senders",
		"checkpoint_completion_target",
		"wal_buffers", "wal_compression",
	}

	results := make([]PGSetting, 0, len(settingsToCheck))

	for _, name := range settingsToCheck {
		var value, unit string
		err := pool.QueryRow(ctx,
			`SELECT current_setting($1), COALESCE((SELECT unit FROM pg_settings WHERE name = $1), '')`,
			name).Scan(&value, &unit)
		if err != nil {
			continue
		}

		setting := PGSetting{
			Name:    name,
			Current: value,
			Unit:    unit,
		}

		// Evaluate recommendations
		switch name {
		case "wal_level":
			setting.Recommended = "minimal"
			setting.Description = "minimal eliminates WAL for COPY into new/truncated tables (2-5x faster)"
			if value == "minimal" {
				setting.Status = "ok"
			} else {
				setting.Status = "warn"
			}
		case "max_wal_size":
			setting.Recommended = "16GB+"
			setting.Description = "Large value prevents checkpoint storms during bulk load"
			setting.Status = "info"
		case "min_wal_size":
			setting.Recommended = "4GB+"
			setting.Description = "Pre-allocated WAL files reduce allocation overhead"
			setting.Status = "info"
		case "synchronous_commit":
			setting.Recommended = "off (auto per-session)"
			setting.Description = "existora2pg sets this per-session automatically. Server-level OFF avoids overhead for DDL/constraint phases too"
			if value == "off" {
				setting.Status = "ok"
			} else {
				setting.Status = "info"
			}
		case "checkpoint_timeout":
			setting.Recommended = "30min"
			setting.Description = "Longer timeout reduces checkpoint frequency during bulk load"
			setting.Status = "info"
		case "shared_buffers":
			setting.Recommended = "25% of RAM"
			setting.Description = "Main memory area for caching table and index data"
			setting.Status = "info"
		case "work_mem":
			setting.Recommended = "256MB"
			setting.Description = "Used for sorting during index creation"
			setting.Status = "info"
		case "maintenance_work_mem":
			setting.Recommended = "2GB+"
			setting.Description = "Used by CREATE INDEX, VACUUM, ALTER TABLE"
			setting.Status = "info"
		case "effective_cache_size":
			setting.Recommended = "75% of RAM"
			setting.Description = "Hint for query planner about available OS cache"
			setting.Status = "info"
		case "max_connections":
			workers := 4
			if project.Config.Workers > 0 {
				workers = project.Config.Workers
			}
			setting.Recommended = fmt.Sprintf("%d+ (workers=%d + overhead)", workers+20, workers)
			setting.Description = "Must be greater than worker count + monitoring connections"
			setting.Status = "info"
		case "autovacuum":
			setting.Recommended = "off (during load)"
			setting.Description = "Autovacuum wastes I/O during bulk COPY, disable temporarily"
			if value == "off" {
				setting.Status = "ok"
			} else {
				setting.Status = "warn"
			}
		case "max_wal_senders":
			setting.Recommended = "0 (for wal_level=minimal)"
			setting.Description = "Required to be 0 when wal_level=minimal"
			setting.Status = "info"
		case "checkpoint_completion_target":
			setting.Recommended = "0.9"
			setting.Description = "Spreads checkpoint I/O over more time"
			if value == "0.9" {
				setting.Status = "ok"
			} else {
				setting.Status = "info"
			}
		case "wal_buffers":
			setting.Recommended = "64MB+"
			setting.Description = "Larger WAL buffer reduces write contention during parallel COPY (default 16MB is often too small)"
			setting.Status = "info"
		case "wal_compression":
			setting.Recommended = "on"
			setting.Description = "Compresses WAL records — reduces I/O at minimal CPU cost (PG 15+: lz4/zstd supported)"
			if value != "off" && value != "" {
				setting.Status = "ok"
			} else {
				setting.Status = "warn"
			}
		}

		results = append(results, setting)
	}

	writeJSON(w, http.StatusOK, map[string]any{"settings": results})
}

func (h *Handlers) GetOracleSettings(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := h.projects.Get(r.Context(), id)
	if err != nil || project == nil {
		writeError(w, http.StatusNotFound, "project not found", "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	db, err := oracle.OpenPool(ctx, project.OracleDSN, 1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot connect to Oracle", err.Error())
		return
	}
	defer db.Close()

	workers := 4
	if project.Config.Workers > 0 {
		workers = project.Config.Workers
	}

	results := make([]PGSetting, 0)

	// Helper: query a single parameter from v$parameter
	getParam := func(name string) string {
		var val string
		if err := db.QueryRowContext(ctx, `SELECT value FROM v$parameter WHERE name = :1`, name).Scan(&val); err != nil {
			return ""
		}
		return val
	}

	// Helper: format bytes to human readable
	fmtBytes := func(s string) string {
		var n int64
		if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n == 0 {
			return s
		}
		switch {
		case n >= 1<<30:
			return fmt.Sprintf("%.1f GB", float64(n)/float64(1<<30))
		case n >= 1<<20:
			return fmt.Sprintf("%.0f MB", float64(n)/float64(1<<20))
		case n >= 1<<10:
			return fmt.Sprintf("%.0f KB", float64(n)/float64(1<<10))
		default:
			return s
		}
	}

	// --- Performance Parameters ---

	// SGA
	if v := getParam("sga_target"); v != "" {
		status := "info"
		if v == "0" {
			status = "warn"
		}
		results = append(results, PGSetting{
			Name: "sga_target", Current: fmtBytes(v), Recommended: "auto (>= 2 GB)",
			Status: status, Description: "Automatic SGA memory management. 0 = disabled (using manual SGA components)",
		})
	}

	// PGA
	if v := getParam("pga_aggregate_target"); v != "" {
		results = append(results, PGSetting{
			Name: "pga_aggregate_target", Current: fmtBytes(v), Recommended: ">= 1 GB",
			Status: "info", Description: "Aggregate PGA memory for sorting, hashing in parallel queries",
		})
	}

	// Parallel settings
	if v := getParam("parallel_max_servers"); v != "" {
		status := "info"
		var n int
		fmt.Sscanf(v, "%d", &n)
		if n < workers*2 && n > 0 {
			status = "warn"
		} else if n >= workers*2 {
			status = "ok"
		}
		results = append(results, PGSetting{
			Name: "parallel_max_servers", Current: v,
			Recommended: fmt.Sprintf(">= %d (2x workers)", workers*2),
			Status: status, Description: "Max parallel execution slaves. Should be >= 2x migration workers for PARALLEL hints",
		})
	}

	if v := getParam("parallel_degree_policy"); v != "" {
		results = append(results, PGSetting{
			Name: "parallel_degree_policy", Current: v, Recommended: "MANUAL or AUTO",
			Status: "info", Description: "AUTO enables statement-level auto DOP. MANUAL requires /*+ PARALLEL */ hints",
		})
	}

	// Sessions & Processes
	if v := getParam("sessions"); v != "" {
		status := "info"
		var n int
		fmt.Sscanf(v, "%d", &n)
		needed := workers*3 + 50
		if n >= needed {
			status = "ok"
		} else {
			status = "warn"
		}
		results = append(results, PGSetting{
			Name: "sessions", Current: v,
			Recommended: fmt.Sprintf(">= %d (workers=%d x3 + overhead)", needed, workers),
			Status: status, Description: "Max concurrent sessions. Each worker uses 1+ sessions for parallel reads",
		})
	}

	if v := getParam("processes"); v != "" {
		status := "info"
		var n int
		fmt.Sscanf(v, "%d", &n)
		needed := workers*2 + 30
		if n >= needed {
			status = "ok"
		} else {
			status = "warn"
		}
		results = append(results, PGSetting{
			Name: "processes", Current: v,
			Recommended: fmt.Sprintf(">= %d (workers=%d x2 + overhead)", needed, workers),
			Status: status, Description: "Max OS processes. Must accommodate parallel slaves + background processes",
		})
	}

	// Open cursors
	if v := getParam("open_cursors"); v != "" {
		status := "info"
		var n int
		fmt.Sscanf(v, "%d", &n)
		if n >= 300 {
			status = "ok"
		} else {
			status = "warn"
		}
		results = append(results, PGSetting{
			Name: "open_cursors", Current: v, Recommended: ">= 300",
			Status: status, Description: "Max open cursors per session. Each streaming read uses a cursor",
		})
	}

	// Full scan / I/O parameters
	if v := getParam("db_file_multiblock_read_count"); v != "" {
		status := "info"
		var n int
		fmt.Sscanf(v, "%d", &n)
		if n >= 128 {
			status = "ok"
		}
		results = append(results, PGSetting{
			Name: "db_file_multiblock_read_count", Current: v, Recommended: ">= 128",
			Status: status, Description: "Blocks per I/O during full table scans. Higher = fewer I/O calls for bulk reads",
		})
	}

	// Optimizer
	if v := getParam("optimizer_mode"); v != "" {
		status := "ok"
		if v != "ALL_ROWS" {
			status = "warn"
		}
		results = append(results, PGSetting{
			Name: "optimizer_mode", Current: v, Recommended: "ALL_ROWS",
			Status: status, Description: "ALL_ROWS optimizes for throughput (full scans). FIRST_ROWS optimizes for latency",
		})
	}

	// NLS / Character Set
	if v := getParam("nls_characterset"); v == "" {
		// Query from database properties instead
		var charset string
		_ = db.QueryRowContext(ctx,
			`SELECT value FROM nls_database_parameters WHERE parameter = 'NLS_CHARACTERSET'`).Scan(&charset)
		if charset != "" {
			status := "ok"
			if charset != "AL32UTF8" && charset != "UTF8" {
				status = "warn"
			}
			results = append(results, PGSetting{
				Name: "NLS_CHARACTERSET", Current: charset, Recommended: "AL32UTF8",
				Status: status, Description: "Database character set. Non-UTF8 requires implicit conversion during migration",
			})
		}
	}

	var nlsLang string
	_ = db.QueryRowContext(ctx,
		`SELECT value FROM nls_database_parameters WHERE parameter = 'NLS_NCHAR_CHARACTERSET'`).Scan(&nlsLang)
	if nlsLang != "" {
		results = append(results, PGSetting{
			Name: "NLS_NCHAR_CHARACTERSET", Current: nlsLang, Recommended: "AL16UTF16",
			Status: "info", Description: "National character set for NCHAR/NVARCHAR2/NCLOB columns",
		})
	}

	// Undo / Redo
	if v := getParam("undo_retention"); v != "" {
		status := "info"
		var n int
		fmt.Sscanf(v, "%d", &n)
		if n >= 900 {
			status = "ok"
		} else {
			status = "warn"
		}
		results = append(results, PGSetting{
			Name: "undo_retention", Current: fmt.Sprintf("%s sec (%dm)", v, n/60),
			Recommended: ">= 900 sec (15 min)",
			Status: status, Description: "Undo retention for read consistency. Low value can cause ORA-01555 on long reads",
		})
	}

	// Redo log size
	var redoSizeMB float64
	_ = db.QueryRowContext(ctx,
		`SELECT ROUND(AVG(bytes)/1024/1024) FROM v$log`).Scan(&redoSizeMB)
	if redoSizeMB > 0 {
		status := "info"
		if redoSizeMB >= 500 {
			status = "ok"
		} else if redoSizeMB < 200 {
			status = "warn"
		}
		results = append(results, PGSetting{
			Name: "redo_log_size", Current: fmt.Sprintf("%.0f MB (avg per group)", redoSizeMB),
			Recommended: ">= 500 MB per group",
			Status: status, Description: "Larger redo logs reduce log switch frequency during bulk reads with DML",
		})
	}

	// Redo log groups count
	var logGroups int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM v$log`).Scan(&logGroups)
	if logGroups > 0 {
		results = append(results, PGSetting{
			Name: "redo_log_groups", Current: fmt.Sprintf("%d groups", logGroups),
			Recommended: ">= 3 groups",
			Status: func() string {
				if logGroups >= 3 {
					return "ok"
				}
				return "warn"
			}(),
			Description: "Number of redo log groups. More groups = smoother log switching",
		})
	}

	// Exadata-specific
	if v := getParam("cell_offload_processing"); v != "" {
		status := "ok"
		if v != "TRUE" {
			status = "warn"
		}
		results = append(results, PGSetting{
			Name: "cell_offload_processing", Current: v, Recommended: "TRUE",
			Status: status, Description: "Exadata Smart Scan: offloads filtering/decompression to storage cells",
		})
	}

	// --- Schema Info ---
	schemaName := strings.ToUpper(project.OracleSchema)
	schemaInfo := make(map[string]any)

	// Table count
	var tableCount int
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM all_tables WHERE owner = :1`, schemaName).Scan(&tableCount)
	schemaInfo["table_count"] = tableCount

	// Total rows estimate
	var totalRows int64
	_ = db.QueryRowContext(ctx,
		`SELECT NVL(SUM(num_rows), 0) FROM all_tables WHERE owner = :1`, schemaName).Scan(&totalRows)
	schemaInfo["total_rows"] = totalRows

	// Total size (estimated from avg_row_len * num_rows, fallback if all_segments unavailable)
	var totalSizeMB float64
	err = db.QueryRowContext(ctx,
		`SELECT NVL(SUM(bytes)/1024/1024, 0) FROM all_segments WHERE owner = :1`, schemaName).Scan(&totalSizeMB)
	if err != nil || totalSizeMB == 0 {
		// Fallback: estimate from table stats
		_ = db.QueryRowContext(ctx,
			`SELECT NVL(SUM(NVL(num_rows,0) * NVL(avg_row_len,100))/1024/1024, 0) FROM all_tables WHERE owner = :1`, schemaName).Scan(&totalSizeMB)
	}
	schemaInfo["total_size_mb"] = totalSizeMB

	// Top 5 largest tables (try all_segments first, fallback to num_rows estimate)
	type tableSize struct {
		Name    string  `json:"name"`
		Rows    int64   `json:"rows"`
		SizeMB  float64 `json:"size_mb"`
	}
	var largestTables []tableSize
	rows, err := db.QueryContext(ctx,
		`SELECT table_name, NVL(num_rows, 0),
		        ROUND(NVL(num_rows,0) * NVL(avg_row_len,100) / 1024 / 1024, 1)
		 FROM all_tables WHERE owner = :1
		 ORDER BY NVL(num_rows,0) DESC
		 FETCH FIRST 5 ROWS ONLY`, schemaName)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ts tableSize
			if err := rows.Scan(&ts.Name, &ts.Rows, &ts.SizeMB); err == nil {
				largestTables = append(largestTables, ts)
			}
		}
	}
	schemaInfo["largest_tables"] = largestTables

	// Partitioned table count
	var partTableCount int
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT table_name) FROM all_tab_partitions WHERE table_owner = :1`, schemaName).Scan(&partTableCount)
	schemaInfo["partitioned_tables"] = partTableCount

	// LOB column count
	var lobCount int
	_ = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM all_tab_columns WHERE owner = :1 AND data_type IN ('CLOB','NCLOB','BLOB','BFILE')`, schemaName).Scan(&lobCount)
	schemaInfo["lob_columns"] = lobCount

	// Check if Exadata
	var cellCount int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM v$cell_state`).Scan(&cellCount)
	isExadata := cellCount > 0

	// Version
	var version string
	_ = db.QueryRowContext(ctx, `SELECT banner FROM v$version WHERE ROWNUM = 1`).Scan(&version)

	// Last ANALYZE time
	var lastAnalyze sql.NullString
	_ = db.QueryRowContext(ctx,
		`SELECT TO_CHAR(MAX(last_analyzed), 'YYYY-MM-DD HH24:MI') FROM all_tables WHERE owner = :1`, schemaName).Scan(&lastAnalyze)
	if lastAnalyze.Valid {
		schemaInfo["last_analyzed"] = lastAnalyze.String
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"settings":    results,
		"schema_info": schemaInfo,
		"is_exadata":  isExadata,
		"version":     version,
	})
}

// --- Schema Extraction ---

func (h *Handlers) GetSchema(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := h.projects.Get(r.Context(), id)
	if err != nil || project == nil {
		writeError(w, http.StatusNotFound, "project not found", "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	db, err := oracle.OpenPool(ctx, project.OracleDSN, 2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to connect to Oracle", err.Error())
		return
	}
	defer db.Close()

	extractor := oracle.NewExtractor(db, h.logger)

	// If ?all=true, skip include filters so ObjectSelector can show all objects
	showAll := r.URL.Query().Get("all") == "true"
	var filter oracle.TableFilter
	if !showAll {
		filter = oracle.TableFilter{
			Include:          project.Config.IncludeTables,
			Exclude:          project.Config.ExcludeTables,
			IncludeSequences: project.Config.IncludeSequences,
		}
	}

	s, err := extractor.Extract(ctx, project.OracleSchema, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "schema extraction failed", err.Error())
		return
	}

	nc := postgres.NamingConfig{
		Convention:    project.Config.NamingConvention,
		TypeOverrides: project.Config.TypeOverrides,
		NameOverrides: project.Config.NameOverrides,
	}
	if nc.Convention == "" {
		nc.Convention = "lowercase"
	}
	writeJSON(w, http.StatusOK, schemaToResponse(s, nc))
}

// --- DDL Preview ---

func (h *Handlers) GetDDLPreview(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := h.projects.Get(r.Context(), id)
	if err != nil || project == nil {
		writeError(w, http.StatusNotFound, "project not found", "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	db, err := oracle.OpenPool(ctx, project.OracleDSN, 2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to connect to Oracle", err.Error())
		return
	}
	defer db.Close()

	extractor := oracle.NewExtractor(db, h.logger)
	filter := oracle.TableFilter{
		Include:          project.Config.IncludeTables,
		Exclude:          project.Config.ExcludeTables,
		IncludeSequences: project.Config.IncludeSequences,
	}

	s, err := extractor.Extract(ctx, project.OracleSchema, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "schema extraction failed", err.Error())
		return
	}

	pgSchema := defaultPGSchema(project.PgSchema)

	// Build naming config from project
	nc := postgres.NamingConfig{
		Convention:    project.Config.NamingConvention,
		TypeOverrides: project.Config.TypeOverrides,
		NameOverrides: project.Config.NameOverrides,
	}
	if nc.Convention == "" {
		nc.Convention = "lowercase"
	}

	// Resolve per-table target mode
	resolveMode := func(tableName string) config.TargetMode {
		if override, ok := project.Config.TableOverrides[tableName]; ok {
			tm := config.TargetMode(override.TargetMode)
			if tm != "" {
				return tm
			}
		}
		if project.Config.DropTarget {
			return config.TargetModeRecreate
		}
		return config.TargetModeRecreate
	}

	var stmts []DDLStatement
	for _, t := range s.Tables {
		mode := resolveMode(t.Name)
		modeStr := string(mode)

		// skip: no DDL at all
		if mode == config.TargetModeSkip {
			stmts = append(stmts, DDLStatement{
				Type:       "skip",
				Table:      t.Name,
				Statement:  "-- SKIPPED: " + t.Name,
				TargetMode: modeStr,
				Skipped:    true,
			})
			continue
		}

		// append: only CREATE IF NOT EXISTS, no DROP
		// truncate / truncate_partition: only CREATE IF NOT EXISTS, no DROP
		// recreate: DROP + CREATE
		skipDrop := mode != config.TargetModeRecreate
		skipCreate := false

		if !skipDrop {
			stmts = append(stmts, DDLStatement{
				Type:       "drop_table",
				Table:      t.Name,
				Statement:  postgres.GenerateDropTableWithNaming(t.Name, pgSchema, nc),
				TargetMode: modeStr,
			})
		}

		if !skipCreate {
			stmt := postgres.GenerateCreateTableWithNaming(t, pgSchema, nc)
			if mode == config.TargetModeAppend || mode == config.TargetModeTruncate || mode == config.TargetModeTruncatePartition {
				// These modes create only if table doesn't exist
				stmt = "-- CREATE IF NOT EXISTS (mode: " + modeStr + ")\n" + stmt
			}
			stmts = append(stmts, DDLStatement{
				Type:       "create_table",
				Table:      t.Name,
				Statement:  stmt,
				TargetMode: modeStr,
			})
		}

		if mode == config.TargetModeTruncate {
			stmts = append(stmts, DDLStatement{
				Type:       "truncate",
				Table:      t.Name,
				Statement:  fmt.Sprintf("TRUNCATE TABLE %s.%s;", pgSchema, strings.ToLower(t.Name)),
				TargetMode: modeStr,
			})
		}

		if t.IsPartitioned() {
			bounds := migration.BuildPartitionBounds(t)
			for _, ddl := range postgres.GeneratePartitionDDLWithNaming(t, pgSchema, bounds, nc) {
				stmts = append(stmts, DDLStatement{
					Type:       "partition",
					Table:      t.Name,
					Statement:  ddl,
					TargetMode: modeStr,
				})
			}
		}

		if pk := postgres.GeneratePrimaryKeyWithNaming(t, pgSchema, nc); pk != "" {
			stmts = append(stmts, DDLStatement{
				Type:       "primary_key",
				Table:      t.Name,
				Statement:  pk,
				TargetMode: modeStr,
			})
		}

		for _, ddl := range postgres.GenerateIndexesWithNaming(t, pgSchema, nc) {
			stmts = append(stmts, DDLStatement{
				Type:       "index",
				Table:      t.Name,
				Statement:  ddl,
				TargetMode: modeStr,
			})
		}

		for _, ddl := range postgres.GenerateConstraintsWithNaming(t, pgSchema, nc) {
			stmts = append(stmts, DDLStatement{
				Type:       "constraint",
				Table:      t.Name,
				Statement:  ddl,
				TargetMode: modeStr,
			})
		}
	}

	// Only show sequences that are explicitly selected (if include list exists)
	seqIncludeSet := make(map[string]bool)
	for _, name := range project.Config.IncludeSequences {
		seqIncludeSet[strings.ToUpper(name)] = true
	}
	for _, seq := range s.Sequences {
		// If include list exists but this sequence is not in it, skip
		if len(seqIncludeSet) > 0 && !seqIncludeSet[strings.ToUpper(seq.Name)] {
			continue
		}
		// If no sequences selected at all, skip all
		if len(project.Config.IncludeSequences) == 0 && len(project.Config.IncludeTables) > 0 {
			continue
		}
		stmts = append(stmts, DDLStatement{
			Type:      "sequence",
			Table:     seq.Name,
			Statement: postgres.GenerateSequenceWithNaming(seq, pgSchema, nc),
		})
	}

	writeJSON(w, http.StatusOK, DDLPreviewResponse{Statements: stmts})
}

// --- Migration Control ---

func (h *Handlers) StartMigration(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := h.projects.Get(r.Context(), id)
	if err != nil || project == nil {
		writeError(w, http.StatusNotFound, "project not found", "")
		return
	}

	// Atomic check-and-set to prevent double-start race
	cfg := h.buildConfig(project)
	runID := uuid.New().String()
	ctx, cancel := context.WithCancel(context.Background())

	h.activeMu.Lock()
	if _, running := h.activeMigs[id]; running {
		h.activeMu.Unlock()
		cancel()
		writeError(w, http.StatusConflict, "migration already running for this project", "")
		return
	}
	h.activeMigs[id] = cancel
	h.activeMu.Unlock()

	h.logger.Info("migration starting", "project", id, "run_id", runID,
		"workers", cfg.Migration.Workers, "batch_size", cfg.Migration.BatchSize,
		"chunk_strategy", cfg.Migration.ChunkStrategy,
		"tables", len(cfg.Migration.IncludeTables))

	h.runInBackground(id, "migration", func() error {
		return h.runMigration(ctx, id, runID, cfg)
	})

	writeJSON(w, http.StatusAccepted, MigrationStartResponse{
		RunID:   runID,
		Message: "migration started",
	})
}

func (h *Handlers) ResumeMigration(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := h.projects.Get(r.Context(), id)
	if err != nil || project == nil {
		writeError(w, http.StatusNotFound, "project not found", "")
		return
	}

	cfg := h.buildConfig(project)
	ctx, cancel := context.WithCancel(context.Background())

	h.activeMu.Lock()
	if _, running := h.activeMigs[id]; running {
		h.activeMu.Unlock()
		cancel()
		writeError(w, http.StatusConflict, "migration already running for this project", "")
		return
	}
	h.activeMigs[id] = cancel
	h.activeMu.Unlock()

	h.logger.Info("migration resuming", "project", id)

	h.runInBackground(id, "resume", func() error {
		return h.resumeMigration(ctx, id, cfg)
	})

	writeJSON(w, http.StatusAccepted, MigrationStartResponse{
		RunID:   "",
		Message: "resume started",
	})
}

func (h *Handlers) CancelMigration(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	h.activeMu.RLock()
	cancelFn, ok := h.activeMigs[id]
	h.activeMu.RUnlock()

	if !ok {
		writeError(w, http.StatusNotFound, "no active migration for this project", "")
		return
	}

	h.logger.Info("migration cancelling", "project", id)
	cancelFn()
	writeJSON(w, http.StatusOK, map[string]string{"message": "cancel signal sent"})
}

func (h *Handlers) ResetMigration(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := h.projects.Get(r.Context(), id)
	if err != nil || project == nil {
		writeError(w, http.StatusNotFound, "project not found", "")
		return
	}

	// Don't allow reset while migration is running
	h.activeMu.RLock()
	_, running := h.activeMigs[id]
	h.activeMu.RUnlock()
	if running {
		writeError(w, http.StatusConflict, "cannot reset while migration is running", "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	// Drop target tables in PostgreSQL
	pgPool, err := postgres.OpenPool(ctx, project.PgDSN, 2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to connect to PostgreSQL", err.Error())
		return
	}
	defer pgPool.Close()

	pgSchema := defaultPGSchema(project.PgSchema)

	// Drop selected tables (CASCADE to handle partitions and FK deps)
	dropped := 0
	for _, tableName := range project.Config.IncludeTables {
		pgName := strings.ToLower(tableName)
		_, err := pgPool.Exec(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS "%s"."%s" CASCADE`, pgSchema, pgName))
		if err != nil {
			h.logger.Warn("failed to drop table", "table", pgName, "error", err)
		} else {
			dropped++
		}
	}

	// Drop selected sequences
	for _, seqName := range project.Config.IncludeSequences {
		pgName := strings.ToLower(seqName)
		_, err := pgPool.Exec(ctx, fmt.Sprintf(`DROP SEQUENCE IF EXISTS "%s"."%s"`, pgSchema, pgName))
		if err != nil {
			h.logger.Warn("failed to drop sequence", "seq", pgName, "error", err)
		}
	}

	// Delete all migration runs for this project
	for {
		runID, err := h.jobStore.GetLatestRunID(ctx)
		if err != nil {
			break
		}
		if err := h.jobStore.DeleteRun(ctx, runID); err != nil {
			break
		}
	}

	// Clear stale phase info so status endpoint doesn't return old state
	h.activePhaseMu.Lock()
	delete(h.activePhases, id)
	h.activePhaseMu.Unlock()

	h.logger.Info("migration reset", "project", id, "tables_dropped", dropped)
	writeJSON(w, http.StatusOK, map[string]string{
		"message": fmt.Sprintf("Reset complete: %d tables dropped, job history cleared", dropped),
	})
}

// --- Status & Jobs ---

func (h *Handlers) GetStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Check active phase first (available even before run is created)
	var phase *MigrationPhase
	h.activePhaseMu.RLock()
	if p, ok := h.activePhases[id]; ok {
		phase = &p
	}
	h.activePhaseMu.RUnlock()

	runID, err := h.jobStore.GetLatestRunID(r.Context())
	if err != nil {
		// No run yet — but if migration is active (extracting/planning), return phase info
		if phase != nil {
			writeJSON(w, http.StatusOK, JobStatusResponse{
				Phase: phase,
			})
			return
		}
		writeError(w, http.StatusNotFound, "no migration runs found", err.Error())
		return
	}

	summary, err := h.jobStore.GetRunSummary(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get run summary", err.Error())
		return
	}

	// Timing is now computed in SQL by GetRunSummary — no need to fetch all jobs
	rs := RunSummary{
		Total:     summary.Total,
		Pending:   summary.Pending,
		Running:   summary.Running,
		Completed: summary.Completed,
		Failed:    summary.Failed,
		Retrying:  summary.Retrying,
		Skipped:   summary.Skipped,
		TotalRows: summary.TotalRows,
	}
	if summary.StartedAt != nil {
		rs.StartedAt = summary.StartedAt
	}
	if summary.FinishedAt != nil {
		rs.FinishedAt = summary.FinishedAt
	}

	writeJSON(w, http.StatusOK, JobStatusResponse{
		RunID:   runID,
		Phase:   phase,
		Summary: rs,
	})
}

func (h *Handlers) GetJobs(w http.ResponseWriter, r *http.Request) {
	_ = chi.URLParam(r, "id")
	tableFilter := r.URL.Query().Get("table")

	runID, err := h.jobStore.GetLatestRunID(r.Context())
	if err != nil {
		writeError(w, http.StatusNotFound, "no migration runs found", err.Error())
		return
	}

	jobs, err := h.jobStore.GetJobs(r.Context(), runID, tableFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get jobs", err.Error())
		return
	}

	summary, err := h.jobStore.GetRunSummary(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get run summary", err.Error())
		return
	}

	// Use all jobs for timing (not filtered subset)
	allJobs := jobs
	if tableFilter != "" {
		allJobs, _ = h.jobStore.GetJobs(r.Context(), runID, "")
	}
	rs := buildRunSummaryWithTiming(summary, allJobs)

	// Aggregate chunk jobs into their parent table/partition
	type chunkAgg struct {
		job JobResponse
		cnt int
	}
	aggregated := make(map[string]*chunkAgg) // key = "table:partition:phase"
	var result []JobResponse

	for _, j := range jobs {
		if isChunkPartition(j.Partition) {
			realPartition := stripChunkSuffix(j.Partition)
			key := j.TableName + ":" + realPartition + ":" + j.Phase
			agg, ok := aggregated[key]
			if !ok {
				agg = &chunkAgg{
					job: JobResponse{
						ID:        j.ID,
						TableName: j.TableName,
						Partition: realPartition,
						Phase:     j.Phase,
						State:     string(j.State),
						StartedAt: j.StartedAt,
					},
				}
				aggregated[key] = agg
			}
			agg.cnt++
			agg.job.RowsExpected += j.RowsExpected
			agg.job.RowsCopied += j.RowsCopied
			agg.job.BytesCopied += j.BytesCopied
			// Worst state wins: FAILED > RUNNING > RETRYING > PENDING > COMPLETED
			agg.job.State = mergeJobState(agg.job.State, string(j.State))
			if j.Error != "" && agg.job.Error == "" {
				agg.job.Error = j.Error
			}
			if j.Attempt > agg.job.Attempt {
				agg.job.Attempt = j.Attempt
			}
			if j.StartedAt != nil && (agg.job.StartedAt == nil || j.StartedAt.Before(*agg.job.StartedAt)) {
				agg.job.StartedAt = j.StartedAt
			}
			if j.FinishedAt != nil && (agg.job.FinishedAt == nil || j.FinishedAt.After(*agg.job.FinishedAt)) {
				agg.job.FinishedAt = j.FinishedAt
			}
		} else {
			result = append(result, JobResponse{
				ID:           j.ID,
				TableName:    j.TableName,
				Partition:    j.Partition,
				Phase:        j.Phase,
				State:        string(j.State),
				RowsExpected: j.RowsExpected,
				RowsCopied:   j.RowsCopied,
				BytesCopied:  j.BytesCopied,
				Error:        j.Error,
				Attempt:      j.Attempt,
				StartedAt:    j.StartedAt,
				FinishedAt:   j.FinishedAt,
			})
		}
	}

	// Append aggregated chunk jobs in deterministic order
	// Fix floor-division remainder: when rows_copied > rows_expected due to integer division,
	// use rows_copied as the true count (actual Oracle rows are authoritative).
	for _, agg := range aggregated {
		if agg.job.RowsCopied > agg.job.RowsExpected {
			agg.job.RowsExpected = agg.job.RowsCopied
		}
		result = append(result, agg.job)
	}

	// Include persisted pipeline stats
	h.pipelineStatsMu.RLock()
	var stats []ProgressEvent
	for _, st := range h.pipelineStats {
		stats = append(stats, st)
	}
	h.pipelineStatsMu.RUnlock()

	resp := JobStatusResponse{
		RunID:         runID,
		Summary:       rs,
		Jobs:          result,
		PipelineStats: stats,
	}

	writeJSON(w, http.StatusOK, resp)
}

// mergeJobState returns the "worst" state: FAILED > RUNNING > RETRYING > PENDING > COMPLETED > SKIPPED.
func mergeJobState(a, b string) string {
	priority := map[string]int{
		"SKIPPED":   0,
		"COMPLETED": 1,
		"PENDING":   2,
		"RETRYING":  3,
		"RUNNING":   4,
		"FAILED":    5,
	}
	pa, pb := priority[a], priority[b]
	if pb > pa {
		return b
	}
	return a
}

func buildRunSummaryWithTiming(summary *jobstore.RunSummary, jobs []jobstore.Job) RunSummary {
	// Count chunk jobs as single aggregated jobs in summary
	// A chunk group (same table+phase, partition contains "chunk:") counts as 1 job
	type chunkKey struct{ table, phase string }
	chunkGroups := make(map[chunkKey]struct {
		count     int
		completed int
		failed    int
		running   int
		pending   int
		retrying  int
		skipped   int
	})
	var nonChunkTotal, nonChunkCompleted, nonChunkFailed, nonChunkRunning, nonChunkPending, nonChunkRetrying, nonChunkSkipped int

	for _, j := range jobs {
		if isChunkPartition(j.Partition) {
			key := chunkKey{j.TableName, j.Phase}
			g := chunkGroups[key]
			g.count++
			switch j.State {
			case "COMPLETED":
				g.completed++
			case "FAILED":
				g.failed++
			case "RUNNING":
				g.running++
			case "PENDING":
				g.pending++
			case "RETRYING":
				g.retrying++
			case "SKIPPED":
				g.skipped++
			}
			chunkGroups[key] = g
		} else {
			nonChunkTotal++
			switch j.State {
			case "COMPLETED":
				nonChunkCompleted++
			case "FAILED":
				nonChunkFailed++
			case "RUNNING":
				nonChunkRunning++
			case "PENDING":
				nonChunkPending++
			case "RETRYING":
				nonChunkRetrying++
			case "SKIPPED":
				nonChunkSkipped++
			}
		}
	}

	// Each chunk group = 1 aggregated job
	aggTotal := nonChunkTotal + len(chunkGroups)
	aggCompleted := nonChunkCompleted
	aggFailed := nonChunkFailed
	aggRunning := nonChunkRunning
	aggPending := nonChunkPending
	aggRetrying := nonChunkRetrying
	aggSkipped := nonChunkSkipped

	for _, g := range chunkGroups {
		if g.failed > 0 {
			aggFailed++
		} else if g.running > 0 {
			aggRunning++
		} else if g.retrying > 0 {
			aggRetrying++
		} else if g.pending > 0 {
			aggPending++
		} else if g.completed == g.count {
			aggCompleted++
		} else {
			aggSkipped++
		}
	}

	rs := RunSummary{
		Total:     aggTotal,
		Pending:   aggPending,
		Running:   aggRunning,
		Completed: aggCompleted,
		Failed:    aggFailed,
		Retrying:  aggRetrying,
		Skipped:   aggSkipped,
	}

	var earliest *time.Time
	var latest *time.Time
	var totalRows int64

	for _, j := range jobs {
		if j.StartedAt != nil {
			if earliest == nil || j.StartedAt.Before(*earliest) {
				t := *j.StartedAt
				earliest = &t
			}
		}
		if j.FinishedAt != nil {
			if latest == nil || j.FinishedAt.After(*latest) {
				t := *j.FinishedAt
				latest = &t
			}
		}
		if j.Phase == "data" {
			totalRows += j.RowsCopied
		}
	}

	rs.StartedAt = earliest
	rs.FinishedAt = latest
	rs.TotalRows = totalRows

	return rs
}

// --- SSE Progress ---

func (h *Handlers) StreamProgress(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported", "")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsubscribe := h.broadcaster.Subscribe(id)
	defer unsubscribe()

	// Send initial keepalive
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				// Channel closed, migration ended
				fmt.Fprintf(w, "event: done\ndata: {}\n\n")
				flusher.Flush()
				return
			}
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

		case <-ctx.Done():
			return
		}
	}
}

// --- Target Status ---

func (h *Handlers) GetTargetStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := h.projects.Get(r.Context(), id)
	if err != nil || project == nil {
		writeError(w, http.StatusNotFound, "project not found", "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	// Connect to PostgreSQL
	pgPool, err := postgres.OpenPool(ctx, project.PgDSN, 2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to connect to PostgreSQL", err.Error())
		return
	}
	defer pgPool.Close()

	pgSchema := defaultPGSchema(project.PgSchema)
	pgWriter := postgres.NewWriter(pgPool, pgSchema, h.logger)

	// Also need Oracle schema to know partitions
	oraDB, err := oracle.OpenPool(ctx, project.OracleDSN, 2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to connect to Oracle", err.Error())
		return
	}
	defer oraDB.Close()

	extractor := oracle.NewExtractor(oraDB, h.logger)
	showAllTS := r.URL.Query().Get("all") == "true"
	var tsFilter oracle.TableFilter
	if !showAllTS {
		tsFilter = oracle.TableFilter{
			Include:          project.Config.IncludeTables,
			Exclude:          project.Config.ExcludeTables,
			IncludeSequences: project.Config.IncludeSequences,
		}
	}
	s, err := extractor.Extract(ctx, project.OracleSchema, tsFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "schema extraction failed", err.Error())
		return
	}

	resp := TargetStatusResponse{
		Tables:    make([]TableTargetStatus, 0, len(s.Tables)),
		Sequences: make([]SequenceTargetStatus, 0, len(s.Sequences)),
	}

	for _, t := range s.Tables {
		ts := TableTargetStatus{Name: t.Name}
		exists, err := pgWriter.TableExists(ctx, t.Name)
		if err == nil && exists {
			ts.Exists = true
			// Use estimated count (pg_class.reltuples) — instant, no table scan
			count, err := pgWriter.EstimateCount(ctx, t.Name)
			if err == nil {
				ts.RowCount = count
			}
		}

		// Check partitions
		if t.IsPartitioned() {
			for _, p := range t.Partitioning.Partitions {
				ps := PartitionTargetStatus{Name: p.Name}
				childName := strings.ToLower(t.Name) + "_" + strings.ToLower(p.Name)
				var childExists bool
				err := pgPool.QueryRow(ctx,
					`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2)`,
					pgSchema, childName).Scan(&childExists)
				if err == nil && childExists {
					ps.Exists = true
					var cnt float64
					err := pgPool.QueryRow(ctx,
						`SELECT COALESCE(c.reltuples, 0) FROM pg_class c
						 JOIN pg_namespace n ON n.oid = c.relnamespace
						 WHERE n.nspname = $1 AND c.relname = $2`,
						pgSchema, childName).Scan(&cnt)
					if err == nil && cnt > 0 {
						ps.RowCount = int64(cnt)
					} else if err == nil {
						// reltuples 0/-1: ANALYZE hasn't run yet, use real COUNT(*)
						var exact int64
						_ = pgPool.QueryRow(ctx,
							fmt.Sprintf(`SELECT COUNT(*) FROM "%s"."%s"`, pgSchema, childName)).Scan(&exact)
						ps.RowCount = exact
					}
				}
				ts.Partitions = append(ts.Partitions, ps)
			}
		}

		resp.Tables = append(resp.Tables, ts)
	}

	for _, seq := range s.Sequences {
		ss := SequenceTargetStatus{Name: seq.Name}
		seqName := pgSchema + "." + strings.ToLower(seq.Name)
		var seqExists bool
		err := pgPool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.sequences WHERE sequence_schema = $1 AND sequence_name = $2)`,
			pgSchema, strings.ToLower(seq.Name)).Scan(&seqExists)
		if err == nil {
			ss.Exists = seqExists
		}
		_ = seqName
		resp.Sequences = append(resp.Sequences, ss)
	}

	writeJSON(w, http.StatusOK, resp)
}

// --- Validation ---

func (h *Handlers) RunValidation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := h.projects.Get(r.Context(), id)
	if err != nil || project == nil {
		writeError(w, http.StatusNotFound, "project not found", "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	// Connect to Oracle
	oraDB, err := oracle.OpenPool(ctx, project.OracleDSN, 2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to connect to Oracle", err.Error())
		return
	}
	defer oraDB.Close()

	// Connect to PostgreSQL
	pgPool, err := postgres.OpenPool(ctx, project.PgDSN, 2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to connect to PostgreSQL", err.Error())
		return
	}
	defer pgPool.Close()

	// Extract schema to get table list
	extractor := oracle.NewExtractor(oraDB, h.logger)
	filter := oracle.TableFilter{
		Include:          project.Config.IncludeTables,
		Exclude:          project.Config.ExcludeTables,
		IncludeSequences: project.Config.IncludeSequences,
	}
	s, err := extractor.Extract(ctx, project.OracleSchema, filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "schema extraction failed", err.Error())
		return
	}

	pgSchema := defaultPGSchema(project.PgSchema)

	oraReader := oracle.NewReader(oraDB, 1000, h.logger)
	pgWriter := postgres.NewWriter(pgPool, pgSchema, h.logger)

	validator := validate.NewValidator(oraReader, pgWriter, h.logger)

	var results []ValidationResult
	for _, t := range s.Tables {
		// Use WHERE clause from table_overrides so validation counts only what was migrated
		whereClause := ""
		if override, ok := project.Config.TableOverrides[t.Name]; ok {
			whereClause = override.Where
		}
		result, _ := validator.ValidateRowCount(ctx, project.OracleSchema, t.Name, whereClause)
		vr := ValidationResult{
			TableName: t.Name,
		}
		if result != nil {
			vr.OracleCount = result.OracleCount
			vr.PGCount = result.PGCount
			vr.Match = result.Match
			vr.Error = result.Error
		} else {
			vr.Error = "validation returned nil result"
		}
		results = append(results, vr)
	}

	writeJSON(w, http.StatusOK, ValidationResponse{Results: results})
}

// --- Mappings ---

type SaveMappingsRequest struct {
	NamingConvention string            `json:"naming_convention"`
	TypeOverrides    map[string]string `json:"type_overrides"`
	NameOverrides    map[string]string `json:"name_overrides"`
}

func (h *Handlers) SaveMappings(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	project, err := h.projects.Get(r.Context(), id)
	if err != nil || project == nil {
		writeError(w, http.StatusNotFound, "project not found", "")
		return
	}

	var req SaveMappingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	// Update config with new mappings
	project.Config.NamingConvention = req.NamingConvention
	project.Config.TypeOverrides = req.TypeOverrides
	project.Config.NameOverrides = req.NameOverrides

	updated, err := h.projects.Update(r.Context(), id, ProjectUpdateRequest{
		Name:         project.Name,
		OracleDSN:    project.OracleDSN,
		OracleSchema: project.OracleSchema,
		PgDSN:        project.PgDSN,
		PgSchema:     project.PgSchema,
		Config:       project.Config,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save mappings", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, maskedProject(*updated))
}

// --- Internal helpers ---

func (h *Handlers) buildConfig(project *ProjectConfig) *config.Config {
	overrides := make(map[string]config.TableOverride)
	for k, v := range project.Config.TableOverrides {
		o := config.TableOverride{
			Where:           v.Where,
			PartitionFilter: v.PartitionFilter,
			TargetName:      v.TargetName,
			TargetMode:      config.TargetMode(v.TargetMode),
			SkipIndexes:     v.SkipIndexes,
			IndexMode:       v.IndexMode,
			SkipConstraints: v.SkipConstraints,
			SkipValidation:  v.SkipValidation,
			BatchSize:       v.BatchSize,
			FetchSize:       v.FetchSize,
			MaxParallel:     v.MaxParallel,
			ChunkSize:       v.ChunkSize,
			ChunkStrategy:   config.ChunkStrategy(v.ChunkStrategy),
		}
		if v.PartitionRange != nil {
			o.PartitionRange = &config.RangeFilter{
				From: v.PartitionRange.From,
				To:   v.PartitionRange.To,
			}
		}
		overrides[k] = o
	}

	workers := project.Config.Workers
	if workers <= 0 {
		workers = 4
	}
	batchSize := project.Config.BatchSize
	if batchSize <= 0 {
		batchSize = 5000
	}
	fetchSize := project.Config.FetchSize
	if fetchSize <= 0 {
		fetchSize = 5000
	}
	pgSchema := defaultPGSchema(project.PgSchema)

	return &config.Config{
		Oracle: config.OracleConfig{
			DSN:      project.OracleDSN,
			Schema:   project.OracleSchema,
			MaxConns: workers*2 + 4,
		},
		Postgres: config.PostgresConfig{
			DSN:        project.PgDSN,
			Schema:     pgSchema,
			MaxConns:   workers*2 + 4,
			DropTarget: project.Config.DropTarget,
			Unlogged:   project.Config.Unlogged,
		},
		Migration: config.MigrationConfig{
			Workers:          workers,
			BatchSize:        batchSize,
			FetchSize:        fetchSize,
			IncludeTables:    project.Config.IncludeTables,
			ExcludeTables:    project.Config.ExcludeTables,
			IncludeSequences: project.Config.IncludeSequences,
			TableOverrides:   overrides,
			StateDBPath:      h.stateDBPath,
			ValidateAfter:    project.Config.ValidateAfter,
			NamingConvention: project.Config.NamingConvention,
			TypeOverrides:    project.Config.TypeOverrides,
			NameOverrides:    project.Config.NameOverrides,
			ChunkStrategy:    config.ChunkStrategy(project.Config.ChunkStrategy),
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// buildEngine creates a migration engine with all dependencies (Oracle, PG, progress logger).
// Returns the engine and a cleanup function that must be deferred.
func (h *Handlers) buildEngine(ctx context.Context, projectID string, cfg *config.Config) (*migration.Engine, func(), error) {
	oraDB, err := oracle.OpenPool(ctx, cfg.Oracle.DSN, cfg.Oracle.MaxConns)
	if err != nil {
		return nil, nil, fmt.Errorf("connect oracle: %w", err)
	}

	pgPool, err := postgres.OpenPool(ctx, cfg.Postgres.DSN, cfg.Postgres.MaxConns)
	if err != nil {
		oraDB.Close()
		return nil, nil, fmt.Errorf("connect postgres: %w", err)
	}

	extractor := oracle.NewExtractor(oraDB, h.logger)
	reader := oracle.NewReader(oraDB, cfg.Migration.FetchSize, h.logger)
	writer := postgres.NewWriter(pgPool, cfg.Postgres.Schema, h.logger)
	progressLogger := h.newProgressLogger(h.logger, projectID)

	engine := migration.NewEngine(extractor, reader, writer, h.jobStore, cfg, progressLogger)
	engine.SetPhaseCallback(func(phase, detail string) {
		h.activePhaseMu.Lock()
		h.activePhases[projectID] = MigrationPhase{Phase: phase, Detail: detail}
		h.activePhaseMu.Unlock()
	})

	cleanup := func() {
		pgPool.Close()
		oraDB.Close()
	}
	return engine, cleanup, nil
}

// runInBackground runs fn in a goroutine with panic recovery, phase tracking, and cleanup.
func (h *Handlers) runInBackground(projectID, label string, fn func() error) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				h.logger.Error(label+" panicked", "project", projectID, "panic", r)
				h.activePhaseMu.Lock()
				h.activePhases[projectID] = MigrationPhase{Phase: migration.PhaseFailed, Detail: fmt.Sprintf("panic: %v", r)}
				h.activePhaseMu.Unlock()
			}
			h.activeMu.Lock()
			delete(h.activeMigs, projectID)
			h.activeMu.Unlock()
			h.broadcaster.Close(projectID)
		}()

		if err := fn(); err != nil {
			h.logger.Error(label+" failed", "project", projectID, "error", err)
			h.activePhaseMu.Lock()
			h.activePhases[projectID] = MigrationPhase{Phase: migration.PhaseFailed, Detail: err.Error()}
			h.activePhaseMu.Unlock()
		} else {
			h.logger.Info(label+" completed", "project", projectID)
			h.activePhaseMu.Lock()
			delete(h.activePhases, projectID)
			h.activePhaseMu.Unlock()
		}
	}()
}

func (h *Handlers) runMigration(ctx context.Context, projectID, runID string, cfg *config.Config) error {
	engine, cleanup, err := h.buildEngine(ctx, projectID, cfg)
	if err != nil {
		return err
	}
	defer cleanup()
	return engine.Run(ctx)
}

func (h *Handlers) resumeMigration(ctx context.Context, projectID string, cfg *config.Config) error {
	engine, cleanup, err := h.buildEngine(ctx, projectID, cfg)
	if err != nil {
		return err
	}
	defer cleanup()
	return engine.Resume(ctx)
}

// isChunkPartition returns true if the partition string represents a chunk (not a real partition).
func isChunkPartition(partition string) bool {
	return strings.Contains(partition, "chunk:")
}

// stripChunkSuffix removes the ":chunk:N" suffix from partition keys.
// "P202501:chunk:3" → "P202501", "chunk:5" → "".
func stripChunkSuffix(partition string) string {
	if idx := strings.Index(partition, "chunk:"); idx > 0 {
		// "PART:chunk:N" → "PART"
		return strings.TrimSuffix(partition[:idx], ":")
	}
	if strings.HasPrefix(partition, "chunk:") {
		return ""
	}
	return partition
}

// chunkAggregator tracks per-table aggregate progress for chunked tables.
// Throttles output to at most 1 event per second per table to prevent SSE flood.
type chunkAggregator struct {
	mu    sync.Mutex
	state map[string]*chunkTableState // key = "TABLE" or "TABLE:PARTITION"
}

type chunkTableState struct {
	table         string
	partition     string // real partition name (stripped of chunk suffix), or ""
	totalRows     int64
	latestSpeed   int64
	chunkRows     map[string]int64 // per-chunk latest rows (to avoid cumulative overcounting)
	chunksRunning int
	chunksTotal   int
	chunksDone    int
	chunksFailed  int
	lastEmit      time.Time // throttle: last time we emitted an event
	dirty         bool      // has pending changes since lastEmit
}

func newChunkAggregator() *chunkAggregator {
	return &chunkAggregator{state: make(map[string]*chunkTableState)}
}

func (ca *chunkAggregator) buildEvent(st *chunkTableState) ProgressEvent {
	agg := ProgressEvent{
		Table:     st.table,
		Partition: st.partition,
		Rows:      st.totalRows,
		Speed:     st.latestSpeed,
	}
	allDone := st.chunksTotal > 0 && (st.chunksDone+st.chunksFailed) == st.chunksTotal
	if st.chunksFailed > 0 && allDone {
		agg.State = "FAILED"
	} else if allDone {
		agg.State = "COMPLETED"
	} else {
		agg.State = "RUNNING"
	}
	if st.chunksTotal > 0 {
		agg.Percent = float64(st.chunksDone) / float64(st.chunksTotal) * 100
	}
	return agg
}

// update processes a chunk-level event and returns an aggregated table-level event.
// Returns nil if throttled (not enough time since last emit). Always emits on terminal states.
func (ca *chunkAggregator) update(event ProgressEvent) *ProgressEvent {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	realPartition := stripChunkSuffix(event.Partition)
	key := event.Table
	if realPartition != "" {
		key = event.Table + ":" + realPartition
	}

	st, ok := ca.state[key]
	if !ok {
		st = &chunkTableState{table: event.Table, partition: realPartition}
		ca.state[key] = st
	}

	switch event.State {
	case "RUNNING":
		if event.Rows > 0 {
			// Progress update: track per-chunk latest rows (not cumulative sum)
			chunkKey := event.Partition // original partition with chunk suffix
			if st.chunkRows == nil {
				st.chunkRows = make(map[string]int64)
			}
			st.chunkRows[chunkKey] = event.Rows
			// Recalculate total from all chunk snapshots
			var total int64
			for _, r := range st.chunkRows {
				total += r
			}
			st.totalRows = total
			st.latestSpeed = event.Speed
		} else {
			// Worker starting task
			st.chunksRunning++
			st.chunksTotal++
		}
	case "COMPLETED":
		st.chunksDone++
		if st.chunksRunning > 0 {
			st.chunksRunning--
		}
		// Update final rows for this chunk
		if event.Rows > 0 {
			chunkKey := event.Partition
			if st.chunkRows == nil {
				st.chunkRows = make(map[string]int64)
			}
			st.chunkRows[chunkKey] = event.Rows
			var total int64
			for _, r := range st.chunkRows {
				total += r
			}
			st.totalRows = total
		}
	case "FAILED":
		st.chunksFailed++
		if st.chunksRunning > 0 {
			st.chunksRunning--
		}
	}

	st.dirty = true

	// Always emit on terminal (all done) or failure
	isTerminal := (st.chunksDone+st.chunksFailed) == st.chunksTotal && st.chunksTotal > 0
	isFailure := event.State == "FAILED"

	now := time.Now()
	if !isTerminal && !isFailure && now.Sub(st.lastEmit) < 1*time.Second {
		return nil // throttled
	}

	st.lastEmit = now
	st.dirty = false
	agg := ca.buildEvent(st)
	return &agg
}

// chunkStatsAggregator accumulates pipeline stats for chunked tables.
type chunkStatsAggregator struct {
	mu    sync.Mutex
	state map[string]*chunkStatsState // key = "TABLE" or "TABLE:PARTITION"
}

type chunkStatsState struct {
	table        string
	partition    string
	totalRows    int64
	totalDurMs   int64
	totalReadMs  int64
	totalWriteMs int64
	totalBatches int
	count        int // number of chunk stats received
	lastEmit     time.Time
}

func newChunkStatsAggregator() *chunkStatsAggregator {
	return &chunkStatsAggregator{state: make(map[string]*chunkStatsState)}
}

// update accumulates a chunk STATS event. Returns aggregated stats event or nil if throttled.
func (csa *chunkStatsAggregator) update(event ProgressEvent) *ProgressEvent {
	csa.mu.Lock()
	defer csa.mu.Unlock()

	realPartition := stripChunkSuffix(event.Partition)
	key := event.Table
	if realPartition != "" {
		key = event.Table + ":" + realPartition
	}

	st, ok := csa.state[key]
	if !ok {
		st = &chunkStatsState{table: event.Table, partition: realPartition}
		csa.state[key] = st
	}

	st.totalRows += event.Rows
	st.totalDurMs += event.DurationMs
	st.totalReadMs += event.ReadTimeMs
	st.totalWriteMs += event.WriteTimeMs
	st.totalBatches += event.Batches
	st.count++

	// Throttle: max 1 event/sec unless this looks like the last batch
	now := time.Now()
	if now.Sub(st.lastEmit) < time.Second {
		return nil
	}
	st.lastEmit = now

	// Weighted averages for pct
	readPct := 0
	writePct := 0
	if st.totalDurMs > 0 {
		readPct = int(st.totalReadMs * 100 / st.totalDurMs)
		writePct = int(st.totalWriteMs * 100 / st.totalDurMs)
	}

	speed := int64(0)
	if st.totalDurMs > 0 {
		speed = st.totalRows * 1000 / st.totalDurMs
	}

	avgBatchMs := int64(0)
	if st.totalBatches > 0 {
		avgBatchMs = st.totalWriteMs / int64(st.totalBatches)
	}

	agg := ProgressEvent{
		Table:       st.table,
		Partition:   st.partition,
		State:       "STATS",
		Rows:        st.totalRows,
		Speed:       speed,
		DurationMs:  st.totalDurMs,
		ReadTimeMs:  st.totalReadMs,
		WriteTimeMs: st.totalWriteMs,
		ReadPct:     readPct,
		WritePct:    writePct,
		Batches:     st.totalBatches,
		AvgBatchMs:  avgBatchMs,
	}
	return &agg
}

// throttleState holds shared throttle state across WithAttrs/WithGroup clones.
type throttleState struct {
	mu      sync.Mutex
	lastMap map[string]time.Time // key = table name
}

// progressLogger wraps slog.Logger and also broadcasts SSE events
// when it detects progress log messages from the migration engine.
type progressLogHandler struct {
	inner       slog.Handler
	broadcaster *Broadcaster
	projectID   string
	handlers    *Handlers // for persisting pipeline stats
	chunks      *chunkAggregator
	chunkStats  *chunkStatsAggregator
	throttle    *throttleState
}

func (h *Handlers) newProgressLogger(base *slog.Logger, projectID string) *slog.Logger {
	// Clear previous stats on new migration
	h.pipelineStatsMu.Lock()
	h.pipelineStats = make(map[string]ProgressEvent)
	h.pipelineStatsMu.Unlock()

	return slog.New(&progressLogHandler{
		inner:       base.Handler(),
		broadcaster: h.broadcaster,
		projectID:   projectID,
		handlers:    h,
		chunks:      newChunkAggregator(),
		chunkStats:  newChunkStatsAggregator(),
		throttle:    &throttleState{lastMap: make(map[string]time.Time)},
	})
}

// throttledSend sends an SSE event.
// Chunk events are already throttled by chunkAggregator.
// Non-chunk RUNNING events are throttled to 1 per second per table.
// FAILED, STATS, and COMPLETED events always bypass throttle.
func (plh *progressLogHandler) throttledSend(event ProgressEvent) {
	if event.State != "FAILED" && event.State != "STATS" && event.State != "COMPLETED" {
		plh.throttle.mu.Lock()
		now := time.Now()
		last := plh.throttle.lastMap[event.Table]
		if now.Sub(last) < 1*time.Second {
			plh.throttle.mu.Unlock()
			return
		}
		plh.throttle.lastMap[event.Table] = now
		plh.throttle.mu.Unlock()
	}
	// Persist STATS in memory for page refreshes
	if event.State == "STATS" && plh.handlers != nil {
		key := event.Table
		if event.Partition != "" {
			key += ":" + event.Partition
		}
		plh.handlers.pipelineStatsMu.Lock()
		plh.handlers.pipelineStats[key] = event
		plh.handlers.pipelineStatsMu.Unlock()
	}
	plh.broadcaster.Send(plh.projectID, event)
}

func (plh *progressLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return plh.inner.Enabled(ctx, level)
}

func (plh *progressLogHandler) Handle(ctx context.Context, record slog.Record) error {
	// Intercept progress messages from the migration engine and broadcast them
	if record.Message == "progress" ||
		record.Message == "worker starting task" || record.Message == "worker completed task" ||
		record.Message == "pipeline stats" {
		event := ProgressEvent{}
		record.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "table":
				event.Table = a.Value.String()
			case "partition":
				event.Partition = a.Value.String()
			case "rows":
				event.Rows = a.Value.Int64()
			case "rows_per_sec":
				event.Speed = a.Value.Int64()
			case "percent":
				event.Percent = a.Value.Float64()
			case "read_pct":
				event.ReadPct = int(a.Value.Int64())
			case "write_pct":
				event.WritePct = int(a.Value.Int64())
			case "batches":
				event.Batches = int(a.Value.Int64())
			}
			return true
		})
		// Extract duration values (logged as time.Duration, resolve to ms)
		record.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "read_time":
				event.ReadTimeMs = parseDurationMs(a.Value.Any())
			case "write_time":
				event.WriteTimeMs = parseDurationMs(a.Value.Any())
			case "duration":
				event.DurationMs = parseDurationMs(a.Value.Any())
			case "avg_batch_write":
				event.AvgBatchMs = parseDurationMs(a.Value.Any())
			case "acquire_wait":
				event.AcquireWaitMs = parseDurationMs(a.Value.Any())
			case "commit_wait":
				event.CommitWaitMs = parseDurationMs(a.Value.Any())
			}
			return true
		})

		switch record.Message {
		case "worker starting task":
			event.State = "RUNNING"
		case "worker completed task":
			event.State = "COMPLETED"
		case "pipeline stats":
			event.State = "STATS"
		case "progress":
			event.State = "RUNNING"
		}

		// Aggregate chunk events at table level — don't expose chunks to UI
		if isChunkPartition(event.Partition) {
			if event.State == "STATS" {
				if agg := plh.chunkStats.update(event); agg != nil {
					plh.throttledSend(*agg)
				}
			} else if agg := plh.chunks.update(event); agg != nil {
				plh.throttledSend(*agg)
			}
		} else {
			plh.throttledSend(event)
		}
	}

	if record.Message == "task failed" {
		event := ProgressEvent{State: "FAILED"}
		record.Attrs(func(a slog.Attr) bool {
			switch a.Key {
			case "table":
				event.Table = a.Value.String()
			case "partition":
				event.Partition = a.Value.String()
			}
			return true
		})
		if isChunkPartition(event.Partition) {
			if agg := plh.chunks.update(event); agg != nil {
				plh.throttledSend(*agg)
			}
		} else {
			plh.throttledSend(event)
		}
	}

	return plh.inner.Handle(ctx, record)
}

func (plh *progressLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &progressLogHandler{
		inner:       plh.inner.WithAttrs(attrs),
		broadcaster: plh.broadcaster,
		projectID:   plh.projectID,
		handlers:    plh.handlers,
		chunks:      plh.chunks,
		chunkStats:  plh.chunkStats,
		throttle:    plh.throttle,
	}
}

func (plh *progressLogHandler) WithGroup(name string) slog.Handler {
	return &progressLogHandler{
		inner:       plh.inner.WithGroup(name),
		broadcaster: plh.broadcaster,
		projectID:   plh.projectID,
		handlers:    plh.handlers,
		chunks:      plh.chunks,
		chunkStats:  plh.chunkStats,
		throttle:    plh.throttle,
	}
}

// --- Schema conversion helpers ---

func schemaToResponse(s *schema.Schema, nc postgres.NamingConfig) SchemaResponse {
	resp := SchemaResponse{
		Owner:     s.Owner,
		Tables:    make([]TableResponse, 0, len(s.Tables)),
		Sequences: make([]SequenceResponse, 0, len(s.Sequences)),
	}

	for _, t := range s.Tables {
		tr := TableResponse{
			Name:        t.Name,
			Columns:     make([]ColumnResponse, 0, len(t.Columns)),
			Constraints: make([]ConstraintResponse, 0, len(t.Constraints)),
			Indexes:     make([]IndexResponse, 0, len(t.Indexes)),
			Comment:     t.Comment,
			NumRows:     t.NumRows,
			SizeMB:      t.SizeMB,
		}

		for _, c := range t.Columns {
			tr.Columns = append(tr.Columns, ColumnResponse{
				Name:       c.Name,
				OracleType: c.OracleType,
				PGType:     c.PGType,
				Nullable:   c.Nullable,
				Position:   c.Position,
				Precision:  c.Precision,
				Scale:      c.Scale,
				Length:     c.Length,
			})
		}

		if t.PrimaryKey != nil {
			pk := constraintToResponse(*t.PrimaryKey)
			tr.PrimaryKey = &pk
		}

		for _, c := range t.Constraints {
			tr.Constraints = append(tr.Constraints, constraintToResponse(c))
		}

		for _, idx := range t.Indexes {
			ir := IndexResponse{
				Name:    idx.Name,
				Columns: make([]IndexColumnResponse, 0, len(idx.Columns)),
				Unique:  idx.Unique,
				Type:    idx.Type,
				Local:   idx.Local,
			}
			for _, ic := range idx.Columns {
				ir.Columns = append(ir.Columns, IndexColumnResponse{
					Name:       ic.Name,
					Descending: ic.Descending,
					Position:   ic.Position,
				})
			}
			tr.Indexes = append(tr.Indexes, ir)
		}

		if t.Partitioning != nil {
			pi := &PartitionInfoResponse{
				Strategy:   string(t.Partitioning.Strategy),
				KeyColumns: t.Partitioning.KeyColumns,
				Partitions: make([]PartitionResponse, 0, len(t.Partitioning.Partitions)),
			}
			for _, p := range t.Partitioning.Partitions {
				pi.Partitions = append(pi.Partitions, PartitionResponse{
					Name:      p.Name,
					HighValue: p.HighValue,
					Position:  p.Position,
					NumRows:   p.NumRows,
					SizeMB:    p.SizeMB,
				})
			}
			tr.Partitioning = pi
		}

		resp.Tables = append(resp.Tables, tr)
	}

	for _, seq := range s.Sequences {
		resp.Sequences = append(resp.Sequences, SequenceResponse{
			Name:      seq.Name,
			MinValue:  seq.MinValue,
			MaxValue:  seq.MaxValue,
			Increment: seq.Increment,
			LastValue: seq.LastValue,
			CacheSize: seq.CacheSize,
			Cycle:     seq.Cycle,
		})
	}

	naming := plsql.NamingConvention(nc.Convention)

	for _, v := range s.Views {
		cr := plsql.ConvertView(v.Definition, naming)
		resp.Views = append(resp.Views, ViewResponse{
			Name:         v.Name,
			Definition:   v.Definition,
			PGDefinition: cr.Definition,
			Warnings:     cr.Warnings,
		})
	}
	for _, mv := range s.MaterializedViews {
		cr := plsql.ConvertMaterializedView(mv.Definition, naming)
		resp.MaterializedViews = append(resp.MaterializedViews, MaterializedViewResponse{
			Name:         mv.Name,
			Definition:   mv.Definition,
			RefreshMode:  mv.RefreshMode,
			PGDefinition: cr.Definition,
			Warnings:     cr.Warnings,
		})
	}
	for _, tr := range s.Triggers {
		cr := plsql.ConvertTrigger(tr.Name, tr.TableName, tr.TriggerType, tr.Event, tr.Definition, naming)
		resp.Triggers = append(resp.Triggers, TriggerResponse{
			Name:         tr.Name,
			TableName:    tr.TableName,
			TriggerType:  tr.TriggerType,
			Event:        tr.Event,
			Definition:   tr.Definition,
			PGDefinition: cr.Definition,
			Warnings:     cr.Warnings,
		})
	}
	for _, p := range s.Procedures {
		cr := plsql.ConvertProcedure(p.Name, p.ObjectType, p.Definition, naming)
		resp.Procedures = append(resp.Procedures, ProcedureResponse{
			Name:         p.Name,
			ObjectType:   p.ObjectType,
			Definition:   p.Definition,
			PGDefinition: cr.Definition,
			Warnings:     cr.Warnings,
		})
	}

	return resp
}

func constraintToResponse(c schema.Constraint) ConstraintResponse {
	return ConstraintResponse{
		Name:       c.Name,
		Type:       string(c.Type),
		Columns:    c.Columns,
		RefTable:   c.RefTable,
		RefColumns: c.RefColumns,
		OnDelete:   c.OnDelete,
		Condition:  c.Condition,
	}
}

// --- JSON response helpers ---

func defaultPGSchema(s string) string {
	if s == "" {
		return "public"
	}
	return s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message, details string) {
	writeJSON(w, status, ErrorResponse{Error: message, Details: details})
}

// configHash produces a SHA256 hash of the config for dedup.
func configHash(cfg *config.Config) string {
	data := fmt.Sprintf("%v", cfg)
	h := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", h)
}

// parseDurationMs extracts milliseconds from a slog attribute value.
// slog.Any(time.Duration) may resolve to time.Duration, int64 (nanoseconds), or string.
func parseDurationMs(v any) int64 {
	switch d := v.(type) {
	case time.Duration:
		return d.Milliseconds()
	case int64:
		return d / int64(time.Millisecond)
	case string:
		if parsed, err := time.ParseDuration(d); err == nil {
			return parsed.Milliseconds()
		}
	}
	return 0
}

// --- Update handlers ---

// CheckUpdate checks GitHub for a newer release.
func (h *Handlers) CheckUpdate(w http.ResponseWriter, r *http.Request) {
	info, err := updater.CheckUpdate(r.Context(), h.appVersion)
	if err != nil {
		h.logger.Warn("update check failed", "error", err)
		writeJSON(w, http.StatusOK, &updater.UpdateInfo{
			CurrentVersion: h.appVersion,
			LatestVersion:  "unknown",
		})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// ApplyUpdate downloads and applies the latest update.
func (h *Handlers) ApplyUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DownloadURL string `json:"download_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.DownloadURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "download_url required"})
		return
	}

	h.logger.Info("applying update", "url", req.DownloadURL)

	if err := updater.ApplyUpdate(r.Context(), req.DownloadURL, nil); err != nil {
		h.logger.Error("update failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "updated",
		"message": "Update applied. Please restart the application.",
	})
}

// ApplyLocalUpdate handles offline update from a zip file upload.
func (h *Handlers) ApplyLocalUpdate(w http.ResponseWriter, r *http.Request) {
	// Limit upload to 200MB
	r.Body = http.MaxBytesReader(w, r.Body, 200<<20)

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file upload required: " + err.Error()})
		return
	}
	defer file.Close()

	if !strings.HasSuffix(strings.ToLower(header.Filename), ".zip") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "only .zip files are supported"})
		return
	}

	h.logger.Info("applying offline update", "filename", header.Filename, "size", header.Size)

	if err := updater.ApplyFromReader(file); err != nil {
		h.logger.Error("offline update failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "updated",
		"message": "Update applied. Please restart the application.",
	})
}

// Ensure unused imports are accounted for at compile time.
var (
	_ = sql.ErrNoRows
	_ = strings.ToLower
	_ = uuid.New
	_ = configHash
)
