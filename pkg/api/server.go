package api

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/erenalidal/existora2pg/internal/jobstore"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	_ "modernc.org/sqlite"
)

//go:embed static/*
var staticFS embed.FS

// ServerConfig holds configuration for the HTTP server.
type ServerConfig struct {
	// Port to listen on (default 9740).
	Port int
	// StateDBPath is the path to the SQLite state database.
	StateDBPath string
	// CORSOrigins is a comma-separated list of allowed origins for development (e.g., "http://localhost:3000").
	CORSOrigins string
	// Logger is the structured logger to use.
	Logger *slog.Logger
	// Version is the application version (set via ldflags).
	Version string
}

// Server is the REST API + SPA HTTP server.
type Server struct {
	router      chi.Router
	config      ServerConfig
	projectDB   *sql.DB
	projects    *ProjectStore
	jobStore    jobstore.Store
	broadcaster *Broadcaster
	handlers    *Handlers
	logger      *slog.Logger
}

// NewServer creates and configures the HTTP server.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Port <= 0 {
		cfg.Port = 9740
	}
	if cfg.StateDBPath == "" {
		cfg.StateDBPath = "./existora2pg_state.db"
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	// Open the shared SQLite database (same one used by jobstore)
	db, err := sql.Open("sqlite", cfg.StateDBPath)
	if err != nil {
		return nil, fmt.Errorf("open state db: %w", err)
	}
	if err := jobstore.ApplySQLitePragmas(db); err != nil {
		return nil, err
	}

	// Initialize project store
	projectStore := NewProjectStore(db)
	if err := projectStore.InitSchema(context.Background()); err != nil {
		return nil, fmt.Errorf("init project schema: %w", err)
	}

	// Initialize job store (reuse the same DB path)
	store, err := jobstore.New(cfg.StateDBPath)
	if err != nil {
		return nil, fmt.Errorf("open job store: %w", err)
	}
	if err := store.InitSchema(context.Background()); err != nil {
		return nil, fmt.Errorf("init job store schema: %w", err)
	}

	broadcaster := NewBroadcaster()
	handlers := NewHandlers(projectStore, store, broadcaster, cfg.StateDBPath, cfg.Logger, cfg.Version)

	s := &Server{
		config:      cfg,
		projectDB:   db,
		projects:    projectStore,
		jobStore:    store,
		broadcaster: broadcaster,
		handlers:    handlers,
		logger:      cfg.Logger,
	}

	s.router = s.buildRouter(cfg.CORSOrigins)
	return s, nil
}

// buildRouter constructs the chi router with all routes.
func (s *Server) buildRouter(corsOrigins string) chi.Router {
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(slogMiddleware(s.logger))
	r.Use(middleware.Recoverer)
	// No global timeout — SSE streams can run for hours/days.
	// Individual handlers set their own timeouts (connection test: 30s, reset: 5min, etc.).

	// CORS for development
	if corsOrigins != "" {
		r.Use(corsMiddleware(corsOrigins))
	}

	// API routes
	r.Route("/api", func(r chi.Router) {
		// Projects
		r.Post("/projects", s.handlers.CreateProject)
		r.Get("/projects", s.handlers.ListProjects)
		r.Route("/projects/{id}", func(r chi.Router) {
			r.Get("/", s.handlers.GetProject)
			r.Put("/", s.handlers.UpdateProject)
			r.Delete("/", s.handlers.DeleteProject)
			r.Get("/export", s.handlers.ExportProject)

			// Connection tests
			r.Post("/test-oracle", s.handlers.TestOracle)
			r.Post("/test-postgres", s.handlers.TestPostgres)

			// Schema & DDL
			r.Get("/schema", s.handlers.GetSchema)
			r.Get("/ddl-preview", s.handlers.GetDDLPreview)

			// Migration control
			r.Post("/migrate", s.handlers.StartMigration)
			r.Post("/resume", s.handlers.ResumeMigration)
			r.Post("/cancel", s.handlers.CancelMigration)
			r.Post("/reset", s.handlers.ResetMigration)

			// Status & jobs
			r.Get("/status", s.handlers.GetStatus)
			r.Get("/jobs", s.handlers.GetJobs)

			// SSE progress stream (long-lived, no timeout)
			r.Get("/progress", s.handlers.StreamProgress)

			// Target status (PG-side table status)
			r.Get("/target-status", s.handlers.GetTargetStatus)

			// Validation
			r.Post("/validate", s.handlers.RunValidation)

			// Mappings
			r.Put("/mappings", s.handlers.SaveMappings)

			// Recommendations (DB settings check)
			r.Get("/pg-settings", s.handlers.GetPGSettings)
			r.Get("/oracle-settings", s.handlers.GetOracleSettings)
		})

		// Update
		r.Get("/update/check", s.handlers.CheckUpdate)
		r.Post("/update/apply", s.handlers.ApplyUpdate)
	})

	// Health check
	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.config.Version})
	})

	// Serve embedded React SPA for production
	r.Group(func(r chi.Router) {
		r.Handle("/*", spaHandler())
	})

	return r
}

// ListenAndServe starts the HTTP server on localhost only.
// Binds to 127.0.0.1 to prevent external network access.
func (s *Server) ListenAndServe() error {
	addr := fmt.Sprintf("127.0.0.1:%d", s.config.Port)
	s.logger.Info("starting API server", "addr", addr)
	return http.ListenAndServe(addr, s.router)
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.projectDB.Close()
	s.jobStore.Close()
	return nil
}

// Router returns the underlying chi.Router for testing.
func (s *Server) Router() chi.Router {
	return s.router
}

// APIHandler returns an http.Handler that serves only /api routes.
// Useful for Wails integration where the frontend is served by AssetServer.
func (s *Server) APIHandler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(slogMiddleware(s.logger))
	r.Use(middleware.Recoverer)

	r.Route("/api", func(r chi.Router) {
		r.Post("/projects", s.handlers.CreateProject)
		r.Get("/projects", s.handlers.ListProjects)
		r.Route("/projects/{id}", func(r chi.Router) {
			r.Get("/", s.handlers.GetProject)
			r.Put("/", s.handlers.UpdateProject)
			r.Delete("/", s.handlers.DeleteProject)
			r.Post("/test-oracle", s.handlers.TestOracle)
			r.Post("/test-postgres", s.handlers.TestPostgres)
			r.Get("/schema", s.handlers.GetSchema)
			r.Get("/ddl-preview", s.handlers.GetDDLPreview)
			r.Post("/migrate", s.handlers.StartMigration)
			r.Post("/resume", s.handlers.ResumeMigration)
			r.Post("/cancel", s.handlers.CancelMigration)
			r.Get("/status", s.handlers.GetStatus)
			r.Get("/jobs", s.handlers.GetJobs)
			r.Get("/progress", s.handlers.StreamProgress)
			r.Get("/target-status", s.handlers.GetTargetStatus)
			r.Post("/validate", s.handlers.RunValidation)
			r.Put("/mappings", s.handlers.SaveMappings)
		})

		// Update
		r.Get("/update/check", s.handlers.CheckUpdate)
		r.Post("/update/apply", s.handlers.ApplyUpdate)
	})
	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.config.Version})
	})

	return r
}

// --- SPA handler ---

// spaHandler serves the embedded React SPA. All non-API, non-file routes
// fall through to index.html so client-side routing works.
func spaHandler() http.Handler {
	subFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		// If static dir doesn't exist in the embed (dev mode), serve a placeholder
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `<!DOCTYPE html><html><body><h1>existora2pg</h1><p>UI not built. Run from /ui for development.</p></body></html>`)
		})
	}

	fileServer := http.FileServer(http.FS(subFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Try to serve the file directly
		if path != "/" && !strings.HasPrefix(path, "/api") {
			// Check if file exists in embedded FS
			f, err := subFS.Open(strings.TrimPrefix(path, "/"))
			if err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// Fallback to index.html for SPA routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

// --- Middleware ---

// corsMiddleware adds CORS headers for development.
func corsMiddleware(allowedOrigins string) func(http.Handler) http.Handler {
	origins := strings.Split(allowedOrigins, ",")
	originSet := make(map[string]bool, len(origins))
	for _, o := range origins {
		originSet[strings.TrimSpace(o)] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			if originSet[origin] || originSet["*"] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// slogMiddleware logs HTTP requests using slog.
func slogMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			next.ServeHTTP(ww, r)

			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"duration", time.Since(start).Round(time.Millisecond),
				"bytes", ww.BytesWritten(),
			)
		})
	}
}
