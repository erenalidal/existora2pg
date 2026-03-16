CREATE TABLE IF NOT EXISTS migration_runs (
    id          TEXT PRIMARY KEY,
    config_hash TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'RUNNING',
    started_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    finished_at DATETIME,
    total_tables INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS jobs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id        TEXT NOT NULL REFERENCES migration_runs(id),
    table_schema  TEXT NOT NULL,
    table_name    TEXT NOT NULL,
    partition     TEXT NOT NULL DEFAULT '',
    phase         TEXT NOT NULL,
    state         TEXT NOT NULL DEFAULT 'PENDING',
    rows_expected INTEGER NOT NULL DEFAULT 0,
    rows_copied   INTEGER NOT NULL DEFAULT 0,
    bytes_copied  INTEGER NOT NULL DEFAULT 0,
    error_message TEXT,
    attempt       INTEGER NOT NULL DEFAULT 0,
    started_at    DATETIME,
    finished_at   DATETIME,
    created_at    DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at    DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_jobs_run_state ON jobs(run_id, state);
CREATE INDEX IF NOT EXISTS idx_jobs_run_table ON jobs(run_id, table_name, partition, phase);
CREATE UNIQUE INDEX IF NOT EXISTS uq_jobs_run_table_phase ON jobs(run_id, table_name, partition, phase);

CREATE TABLE IF NOT EXISTS job_logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id     INTEGER NOT NULL REFERENCES jobs(id),
    level      TEXT NOT NULL,
    message    TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_job_logs_job ON job_logs(job_id);
