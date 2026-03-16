package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ProjectStore manages project CRUD operations in SQLite.
type ProjectStore struct {
	db *sql.DB
}

// NewProjectStore creates a new ProjectStore using the given SQLite DB connection.
func NewProjectStore(db *sql.DB) *ProjectStore {
	return &ProjectStore{db: db}
}

// InitSchema creates the projects table if it does not exist.
func (ps *ProjectStore) InitSchema(ctx context.Context) error {
	ddl := `CREATE TABLE IF NOT EXISTS projects (
		id            TEXT PRIMARY KEY,
		name          TEXT NOT NULL,
		oracle_dsn    TEXT NOT NULL,
		oracle_schema TEXT NOT NULL,
		pg_dsn        TEXT NOT NULL,
		pg_schema     TEXT NOT NULL DEFAULT 'public',
		config_json   TEXT NOT NULL DEFAULT '{}',
		created_at    DATETIME NOT NULL DEFAULT (datetime('now')),
		updated_at    DATETIME NOT NULL DEFAULT (datetime('now'))
	);
	CREATE INDEX IF NOT EXISTS idx_projects_name ON projects(name);`

	_, err := ps.db.ExecContext(ctx, ddl)
	if err != nil {
		return fmt.Errorf("create projects table: %w", err)
	}
	return nil
}

// Create inserts a new project and returns it with generated ID and timestamps.
func (ps *ProjectStore) Create(ctx context.Context, req ProjectCreateRequest) (*ProjectConfig, error) {
	id := uuid.New().String()
	now := time.Now().UTC()

	configJSON, err := json.Marshal(req.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	pgSchema := req.PgSchema
	if pgSchema == "" {
		pgSchema = "public"
	}

	_, err = ps.db.ExecContext(ctx,
		`INSERT INTO projects (id, name, oracle_dsn, oracle_schema, pg_dsn, pg_schema, config_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, req.Name, req.OracleDSN, req.OracleSchema, req.PgDSN, pgSchema, string(configJSON), now, now)
	if err != nil {
		return nil, fmt.Errorf("insert project: %w", err)
	}

	return &ProjectConfig{
		ID:           id,
		Name:         req.Name,
		OracleDSN:    req.OracleDSN,
		OracleSchema: req.OracleSchema,
		PgDSN:        req.PgDSN,
		PgSchema:     pgSchema,
		Config:       req.Config,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// Get retrieves a project by ID.
func (ps *ProjectStore) Get(ctx context.Context, id string) (*ProjectConfig, error) {
	var p ProjectConfig
	var configJSON string

	err := ps.db.QueryRowContext(ctx,
		`SELECT id, name, oracle_dsn, oracle_schema, pg_dsn, pg_schema, config_json, created_at, updated_at
		 FROM projects WHERE id = ?`, id).Scan(
		&p.ID, &p.Name, &p.OracleDSN, &p.OracleSchema, &p.PgDSN, &p.PgSchema,
		&configJSON, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}

	if err := json.Unmarshal([]byte(configJSON), &p.Config); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return &p, nil
}

// List returns all saved projects ordered by creation time descending.
func (ps *ProjectStore) List(ctx context.Context) ([]ProjectConfig, error) {
	rows, err := ps.db.QueryContext(ctx,
		`SELECT id, name, oracle_dsn, oracle_schema, pg_dsn, pg_schema, config_json, created_at, updated_at
		 FROM projects ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var projects []ProjectConfig
	for rows.Next() {
		var p ProjectConfig
		var configJSON string
		if err := rows.Scan(
			&p.ID, &p.Name, &p.OracleDSN, &p.OracleSchema, &p.PgDSN, &p.PgSchema,
			&configJSON, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		if err := json.Unmarshal([]byte(configJSON), &p.Config); err != nil {
			return nil, fmt.Errorf("unmarshal config: %w", err)
		}
		projects = append(projects, p)
	}
	if projects == nil {
		projects = []ProjectConfig{}
	}
	return projects, rows.Err()
}

// Update modifies an existing project.
func (ps *ProjectStore) Update(ctx context.Context, id string, req ProjectUpdateRequest) (*ProjectConfig, error) {
	configJSON, err := json.Marshal(req.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	pgSchema := req.PgSchema
	if pgSchema == "" {
		pgSchema = "public"
	}

	now := time.Now().UTC()
	res, err := ps.db.ExecContext(ctx,
		`UPDATE projects SET name = ?, oracle_dsn = ?, oracle_schema = ?, pg_dsn = ?,
		 pg_schema = ?, config_json = ?, updated_at = ?
		 WHERE id = ?`,
		req.Name, req.OracleDSN, req.OracleSchema, req.PgDSN, pgSchema,
		string(configJSON), now, id)
	if err != nil {
		return nil, fmt.Errorf("update project: %w", err)
	}

	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, nil
	}

	return ps.Get(ctx, id)
}

// Delete removes a project by ID. Returns true if a row was deleted.
func (ps *ProjectStore) Delete(ctx context.Context, id string) (bool, error) {
	res, err := ps.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete project: %w", err)
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

// GetRawDSN returns the raw Oracle and PG DSNs for a project (for internal use, not API responses).
func (ps *ProjectStore) GetRawDSN(ctx context.Context, id string) (oracleDSN, pgDSN string, err error) {
	err = ps.db.QueryRowContext(ctx,
		`SELECT oracle_dsn, pg_dsn FROM projects WHERE id = ?`, id).Scan(&oracleDSN, &pgDSN)
	if err == sql.ErrNoRows {
		return "", "", fmt.Errorf("project not found: %s", id)
	}
	return
}

// maskedProject returns a copy of the project with masked DSNs for API responses.
func maskedProject(p ProjectConfig) ProjectConfig {
	p.OracleDSN = maskDSN(p.OracleDSN)
	p.PgDSN = maskDSN(p.PgDSN)
	return p
}

// maskedProjects returns copies of projects with masked DSNs.
func maskedProjects(projects []ProjectConfig) []ProjectConfig {
	result := make([]ProjectConfig, len(projects))
	for i, p := range projects {
		result[i] = maskedProject(p)
	}
	return result
}
