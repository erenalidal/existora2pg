// In Wails desktop mode, bypass the AssetServer proxy (which strips query params)
// and call the HTTP server directly on port 9740.
const isWails = typeof (window as any).runtime !== 'undefined';
const BASE = isWails ? 'http://localhost:9740/api' : '/api';

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  });
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(err.error || res.statusText);
  }
  return res.json();
}

// Projects
export interface Project {
  id: string;
  name: string;
  oracle_dsn: string;
  oracle_schema: string;
  pg_dsn: string;
  pg_schema: string;
  migration_config: MigrationConfig;
  created_at: string;
  updated_at: string;
}

export interface MigrationConfig {
  workers: number;
  batch_size: number;
  fetch_size: number;
  chunk_strategy?: string; // auto | rowid | ora_hash | offset
  include_tables: string[];
  exclude_tables: string[];
  include_sequences?: string[];
  table_overrides: Record<string, TableOverride>;
  validate_after: boolean;
  drop_target: boolean;
  naming_convention?: string;
  type_overrides?: Record<string, string>;
  name_overrides?: Record<string, string>;
  unlogged?: boolean;
}

export interface TableOverride {
  where?: string;
  partition_filter?: string[];
  partition_range?: RangeFilter;
  target_name?: string;
  target_mode?: string; // recreate, truncate, append, skip, truncate_partition
  skip_indexes?: boolean;
  index_mode?: string; // create, skip, concurrent
  skip_constraints?: boolean;
  skip_validation?: boolean;
  batch_size?: number;
  fetch_size?: number;
  max_parallel?: number;
  chunk_size?: number;
}

export interface RangeFilter {
  from: string;
  to: string;
}

export const projects = {
  list: () => request<Project[]>('/projects'),
  get: (id: string) => request<Project>(`/projects/${id}`),
  create: (data: Partial<Project>) => request<Project>('/projects', { method: 'POST', body: JSON.stringify(data) }),
  update: (id: string, data: Partial<Project>) => request<Project>(`/projects/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  delete: (id: string) => request<void>(`/projects/${id}`, { method: 'DELETE' }),
  export: (id: string) => request<Project>(`/projects/${id}/export`),
};

// Connection test
export interface ConnectionTestResult {
  success: boolean;
  message: string;
  latency_ms: number;
  server_version?: string;
  schemas?: string[];
}

export const connections = {
  testOracle: (id: string, dsn?: string) => request<ConnectionTestResult>(`/projects/${id}/test-oracle`, {
    method: 'POST',
    body: dsn ? JSON.stringify({ dsn }) : undefined,
  }),
  testPostgres: (id: string, dsn?: string) => request<ConnectionTestResult>(`/projects/${id}/test-postgres`, {
    method: 'POST',
    body: dsn ? JSON.stringify({ dsn }) : undefined,
  }),
};

// Schema
export interface SchemaInfo {
  owner: string;
  tables: TableInfo[];
  sequences: SequenceInfo[];
  views?: ViewInfo[];
  materialized_views?: MaterializedViewInfo[];
  triggers?: TriggerInfo[];
  procedures?: ProcedureInfo[];
}

export interface ViewInfo {
  name: string;
  definition: string;
  pg_definition?: string;
  warnings?: string[];
}

export interface MaterializedViewInfo {
  name: string;
  definition: string;
  refresh_mode: string;
  pg_definition?: string;
  warnings?: string[];
}

export interface TriggerInfo {
  name: string;
  table_name: string;
  trigger_type: string;
  event: string;
  definition: string;
  pg_definition?: string;
  warnings?: string[];
}

export interface ProcedureInfo {
  name: string;
  object_type: string;
  definition: string;
  pg_definition?: string;
  warnings?: string[];
}

export interface TableInfo {
  name: string;
  columns: ColumnInfo[];
  primary_key: { name: string; columns: string[] } | null;
  constraints: ConstraintInfo[];
  indexes: IndexInfo[];
  partitioning: PartitioningInfo | null;
  num_rows: number;
  size_mb: number;
}

export interface ColumnInfo {
  name: string;
  oracle_type: string;
  pg_type: string;
  nullable: boolean;
  position: number;
}

export interface ConstraintInfo {
  name: string;
  type: string;
  columns: string[];
  ref_table?: string;
}

export interface IndexInfo {
  name: string;
  columns: string[];
  unique: boolean;
}

export interface PartitioningInfo {
  strategy: string;
  key_columns: string[];
  partitions: { name: string; high_value: string; position: number; num_rows: number; size_mb: number }[];
}

export interface SequenceInfo {
  name: string;
  last_value: number;
  increment: number;
}

export const schema = {
  get: (id: string, all?: boolean) => request<SchemaInfo>(`/projects/${id}/schema${all ? '?all=true' : ''}`),
};

// Mappings
export interface SaveMappingsRequest {
  naming_convention: string;
  type_overrides: Record<string, string>;
  name_overrides: Record<string, string>;
}

export const mappings = {
  save: (id: string, data: SaveMappingsRequest) =>
    request<Project>(`/projects/${id}/mappings`, { method: 'PUT', body: JSON.stringify(data) }),
};

// DDL Preview
export interface DDLPreview {
  statements: { type: string; table: string; statement: string; target_mode?: string; skipped?: boolean }[];
}

export const ddl = {
  preview: (id: string) => request<DDLPreview>(`/projects/${id}/ddl-preview`),
};

// Migration
export interface MigrationStart {
  run_id: string;
  message: string;
}

export interface RunSummary {
  total: number;
  pending: number;
  running: number;
  completed: number;
  failed: number;
  retrying: number;
  skipped: number;
  started_at?: string;
  finished_at?: string;
  total_rows: number;
}

export interface MigrationPhase {
  phase: string;  // extracting, planning, ddl, copying, constraints, sequences, validating, completed
  detail: string;
}

export interface RunStatus {
  run_id: string;
  phase?: MigrationPhase;
  summary: RunSummary;
}

export interface Job {
  id: number;
  table_name: string;
  partition: string;
  phase: string;
  state: string;
  rows_expected: number;
  rows_copied: number;
  error: string;
  attempt: number;
  started_at?: string;
  finished_at?: string;
}

export interface JobsResponse {
  run_id: string;
  jobs: Job[];
  pipeline_stats?: ProgressEvent[];
}

export const migration = {
  start: (id: string) => request<MigrationStart>(`/projects/${id}/migrate`, { method: 'POST' }),
  resume: (id: string) => request<MigrationStart>(`/projects/${id}/resume`, { method: 'POST' }),
  cancel: (id: string) => request<{ message: string }>(`/projects/${id}/cancel`, { method: 'POST' }),
  reset: (id: string) => request<{ message: string }>(`/projects/${id}/reset`, { method: 'POST' }),
  status: (id: string) => request<RunStatus>(`/projects/${id}/status`),
  jobs: (id: string, table?: string) => {
    const q = table ? `?table=${encodeURIComponent(table)}` : '';
    return request<JobsResponse>(`/projects/${id}/jobs${q}`);
  },
};

// Target Status
export interface TargetStatus {
  tables: TableTargetStatus[];
  sequences: SequenceTargetStatus[];
}

export interface TableTargetStatus {
  name: string;
  exists: boolean;
  row_count: number;
  partitions?: PartitionTargetStatus[];
}

export interface PartitionTargetStatus {
  name: string;
  exists: boolean;
  row_count: number;
}

export interface SequenceTargetStatus {
  name: string;
  exists: boolean;
}

export const targetStatus = {
  get: (id: string, all?: boolean) => request<TargetStatus>(`/projects/${id}/target-status${all ? '?all=true' : ''}`),
};

// Validation
export interface ValidationResult {
  results: { table: string; oracle_count: number; pg_count: number; match: boolean; error?: string }[];
}

export const validation = {
  run: (id: string) => request<ValidationResult>(`/projects/${id}/validate`, { method: 'POST' }),
};

// DB Settings (Recommendations)
export interface DBSetting {
  name: string;
  current: string;
  recommended: string;
  unit?: string;
  status: 'ok' | 'warn' | 'info';
  description: string;
}

export interface PGSettingsResponse {
  settings: DBSetting[];
}

export interface OracleSettingsResponse {
  settings: DBSetting[];
  schema_info: {
    table_count: number;
    total_rows: number;
    total_size_mb: number;
    largest_tables: { name: string; rows: number; size_mb: number }[];
    partitioned_tables: number;
    lob_columns: number;
    last_analyzed?: string;
  };
  is_exadata: boolean;
  version: string;
}

export const recommendations = {
  pgSettings: (id: string) => request<PGSettingsResponse>(`/projects/${id}/pg-settings`),
  oracleSettings: (id: string) => request<OracleSettingsResponse>(`/projects/${id}/oracle-settings`),
};

// SSE Progress stream
export interface ProgressEvent {
  table: string;
  partition: string;
  rows: number;
  speed: number;
  percent: number;
  state: string;
  read_time_ms?: number;
  write_time_ms?: number;
  read_pct?: number;
  write_pct?: number;
  batches?: number;
  avg_batch_ms?: number;
  duration_ms?: number;
  acquire_wait_ms?: number;
  commit_wait_ms?: number;
}

export function streamProgress(projectId: string, onEvent: (e: ProgressEvent) => void, onDone?: () => void, onError?: (msg: string) => void): () => void {
  const es = new EventSource(`${BASE}/projects/${projectId}/progress`);
  es.onmessage = (e) => {
    if (e.data === 'done') {
      onDone?.();
      es.close();
      return;
    }
    try {
      const parsed = JSON.parse(e.data);
      if (parsed.type === 'error') {
        onError?.(parsed.message || 'Unknown error');
        return;
      }
      onEvent(parsed);
    } catch { /* ignore parse errors */ }
  };
  es.onerror = () => {
    es.close();
    onDone?.();
  };
  return () => es.close();
}

// --- Update API ---

export interface UpdateInfo {
  available: boolean;
  current_version: string;
  latest_version: string;
  release_notes?: string;
  release_url?: string;
  download_url?: string;
  asset_size?: number;
}

export const updates = {
  check: (): Promise<UpdateInfo> => request('/update/check'),
  apply: (downloadUrl: string): Promise<{ status: string; message: string }> =>
    request('/update/apply', { method: 'POST', body: JSON.stringify({ download_url: downloadUrl }) }),
  applyLocal: async (file: File): Promise<{ status: string; message: string }> => {
    const form = new FormData();
    form.append('file', file);
    const res = await fetch(`${BASE}/update/apply-local`, { method: 'POST', body: form });
    if (!res.ok) {
      const err = await res.json().catch(() => ({ error: res.statusText }));
      throw new Error(err.error || res.statusText);
    }
    return res.json();
  },
};
