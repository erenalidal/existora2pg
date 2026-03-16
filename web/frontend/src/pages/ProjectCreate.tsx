import { useEffect, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { Save, Zap, CheckCircle, XCircle, Download, Upload } from 'lucide-react';
import { projects, connections, Project, ConnectionTestResult, MigrationConfig } from '../api';

interface ConnFields {
  host: string;
  port: string;
  user: string;
  password: string;
  database: string; // Oracle: service name, PG: database name
  sslmode?: string;
}

interface FormData {
  name: string;
  oracle: ConnFields;
  oracle_schema: string;
  pg: ConnFields;
  pg_schema: string;
  workers: number;
  batch_size: number;
  fetch_size: number;
  chunk_strategy: string;
  validate_after: boolean;
  drop_target: boolean;
  unlogged: boolean;
  include_tables: string;
  exclude_tables: string;
  naming_convention: string;
}

const defaultOracle: ConnFields = { host: 'localhost', port: '1521', user: '', password: '', database: '' };
const defaultPG: ConnFields = { host: 'localhost', port: '5432', user: '', password: '', database: '', sslmode: 'disable' };

const defaultForm: FormData = {
  name: '',
  oracle: { ...defaultOracle },
  oracle_schema: '',
  pg: { ...defaultPG },
  pg_schema: 'public',
  workers: navigator.hardwareConcurrency > 8 ? 8 : navigator.hardwareConcurrency || 4,
  batch_size: 50000,
  fetch_size: 5000,
  chunk_strategy: 'auto',
  validate_after: true,
  drop_target: false,
  unlogged: false,
  include_tables: '',
  exclude_tables: '',
  naming_convention: 'lowercase',
};

// Build DSN strings from fields
function buildOracleDSN(c: ConnFields): string {
  const user = c.user.trim(), host = c.host.trim(), port = c.port.trim() || '1521', svc = c.database.trim(), pass = c.password;
  if (!user || !host || !svc) return '';
  if (pass.includes('/') || pass.includes('@')) {
    return `oracle://${encodeURIComponent(user)}:${encodeURIComponent(pass)}@${host}:${port}/${svc}`;
  }
  return `${user}/${pass}@//${host}:${port}/${svc}`;
}

function buildPgDSN(c: ConnFields): string {
  const user = c.user.trim(), host = c.host.trim(), port = c.port.trim() || '5432', db = c.database.trim(), pass = c.password;
  if (!user || !host || !db) return '';
  let dsn = `postgres://${encodeURIComponent(user)}:${encodeURIComponent(pass)}@${host}:${port}/${encodeURIComponent(db)}`;
  if (c.sslmode) dsn += `?sslmode=${c.sslmode}`;
  return dsn;
}

// Parse DSN strings into fields
function parseOracleDSN(dsn: string): { fields: ConnFields; parsed: boolean } {
  const d = { ...defaultOracle };
  if (!dsn) return { fields: d, parsed: false };
  // oracle://user:pass@host:port/service
  const uriMatch = dsn.match(/^oracle:\/\/([^:]+):([^@]*)@([^:]+):(\d+)\/(.+)$/);
  if (uriMatch) {
    [, d.user, d.password, d.host, d.port, d.database] = uriMatch;
    return { fields: d, parsed: true };
  }
  // user/pass@//host:port/service  or  user/pass@host:port/service
  const classicMatch = dsn.match(/^([^/]+)\/([^@]*)@\/?\/?([\w.-]+):(\d+)\/(.+)$/);
  if (classicMatch) {
    [, d.user, d.password, d.host, d.port, d.database] = classicMatch;
    return { fields: d, parsed: true };
  }
  return { fields: d, parsed: false };
}

function parsePgDSN(dsn: string): { fields: ConnFields; parsed: boolean } {
  const d = { ...defaultPG };
  if (!dsn) return { fields: d, parsed: false };
  try {
    const url = new URL(dsn);
    d.user = decodeURIComponent(url.username);
    d.password = decodeURIComponent(url.password);
    d.host = url.hostname;
    d.port = url.port || '5432';
    d.database = url.pathname.replace(/^\//, '');
    d.sslmode = url.searchParams.get('sslmode') || 'disable';
    return { fields: d, parsed: true };
  } catch {
    return { fields: d, parsed: false };
  }
}

export function ProjectCreate() {
  const { id } = useParams();
  const navigate = useNavigate();
  const isEdit = !!id;
  const [form, setForm] = useState<FormData>(defaultForm);
  const [saving, setSaving] = useState(false);
  const [oraTest, setOraTest] = useState<ConnectionTestResult | null>(null);
  const [pgTest, setPgTest] = useState<ConnectionTestResult | null>(null);
  const [testingOra, setTestingOra] = useState(false);
  const [testingPg, setTestingPg] = useState(false);
  const [error, setError] = useState('');
  const [savedId, setSavedId] = useState(id || '');
  const [existingConfig, setExistingConfig] = useState<MigrationConfig | null>(null);
  const [oraSchemas, setOraSchemas] = useState<string[]>([]);
  const [pgSchemas, setPgSchemas] = useState<string[]>([]);

  useEffect(() => {
    if (isEdit && id) {
      // Use export endpoint to get unmasked DSN for parsing
      projects.export(id).then((p: any) => {
        setExistingConfig(p.migration_config);
        setForm({
          name: p.name,
          oracle: parseOracleDSN(p.oracle_dsn).fields,
          oracle_schema: p.oracle_schema,
          pg: parsePgDSN(p.pg_dsn).fields,
          pg_schema: p.pg_schema || 'public',
          workers: p.migration_config?.workers || (navigator.hardwareConcurrency > 8 ? 8 : navigator.hardwareConcurrency || 4),
          batch_size: p.migration_config?.batch_size || 50000,
          fetch_size: p.migration_config?.fetch_size || 5000,
          chunk_strategy: p.migration_config?.chunk_strategy || 'ora_hash',
          validate_after: p.migration_config?.validate_after ?? true,
          drop_target: p.migration_config?.drop_target ?? false,
          unlogged: p.migration_config?.unlogged ?? false,
          include_tables: (p.migration_config?.include_tables || []).join(', '),
          exclude_tables: (p.migration_config?.exclude_tables || []).join(', '),
          naming_convention: p.migration_config?.naming_convention || 'lowercase',
        });
        // Auto-load schemas using saved DSN (no inline DSN needed)
        connections.testOracle(id).then(r => {
          if (r.schemas?.length) setOraSchemas(r.schemas);
        }).catch(() => {});
        connections.testPostgres(id).then(r => {
          if (r.schemas?.length) setPgSchemas(r.schemas);
        }).catch(() => {});
      });
    }
  }, [id, isEdit]);

  const set = (field: keyof FormData, value: any) => {
    setForm(prev => ({ ...prev, [field]: value }));
  };

  const setOracle = (field: keyof ConnFields, value: string) => {
    setForm(prev => ({ ...prev, oracle: { ...prev.oracle, [field]: value } }));
    setOraSchemas([]); setOraTest(null);
  };

  const setPg = (field: keyof ConnFields, value: string) => {
    setForm(prev => ({ ...prev, pg: { ...prev.pg, [field]: value } }));
    setPgSchemas([]); setPgTest(null);
  };

  // Build DSN from current fields
  const oracleDSN = buildOracleDSN(form.oracle);
  const pgDSN = buildPgDSN(form.pg);

  // Auto-fetch schemas when any connection field loses focus
  const onOracleFieldBlur = () => {
    const dsn = buildOracleDSN(form.oracle);
    if (!dsn) return;
    setTestingOra(true);
    const pid = savedId || '_';
    connections.testOracle(pid, dsn).then(r => {
      if (r.schemas?.length) setOraSchemas(r.schemas);
      setOraTest(r);
    }).catch(() => {}).finally(() => setTestingOra(false));
  };

  const onPgFieldBlur = () => {
    const dsn = buildPgDSN(form.pg);
    if (!dsn) return;
    setTestingPg(true);
    const pid = savedId || '_';
    connections.testPostgres(pid, dsn).then(r => {
      if (r.schemas?.length) setPgSchemas(r.schemas);
      setPgTest(r);
    }).catch(() => {}).finally(() => setTestingPg(false));
  };

  const exportSettings = async () => {
    try {
      let data: Record<string, unknown>;
      if (savedId) {
        const raw = await projects.export(savedId);
        data = raw as unknown as Record<string, unknown>;
      } else {
        // Build DSN strings from form fields for export
        data = {
          name: form.name,
          oracle_dsn: buildOracleDSN(form.oracle),
          oracle_schema: form.oracle_schema,
          pg_dsn: buildPgDSN(form.pg),
          pg_schema: form.pg_schema,
          migration_config: {
            ...existingConfig,
            workers: form.workers,
            batch_size: form.batch_size,
            fetch_size: form.fetch_size,
            chunk_strategy: form.chunk_strategy,
            validate_after: form.validate_after,
            drop_target: form.drop_target,
            unlogged: form.unlogged,
            naming_convention: form.naming_convention,
          },
        };
      }
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${form.name || 'project'}-settings.json`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e: unknown) {
      setError((e as Error).message);
    }
  };

  const importSettings = () => {
    const input = document.createElement('input');
    input.type = 'file';
    input.accept = '.json';
    input.onchange = (e) => {
      const file = (e.target as HTMLInputElement).files?.[0];
      if (!file) return;
      const reader = new FileReader();
      reader.onload = (ev) => {
        try {
          const data = JSON.parse(ev.target?.result as string);
          // Parse DSN strings into ConnFields for the split-field form
          const mc = data.migration_config || {};
          const oraResult = data.oracle_dsn ? parseOracleDSN(data.oracle_dsn) : null;
          const pgResult = data.pg_dsn ? parsePgDSN(data.pg_dsn) : null;
          const warnings: string[] = [];
          if (data.oracle_dsn && oraResult && !oraResult.parsed) {
            warnings.push('Oracle DSN parse edilemedi — bağlantı bilgilerini manuel kontrol edin');
          }
          if (data.pg_dsn && pgResult && !pgResult.parsed) {
            warnings.push('PostgreSQL DSN parse edilemedi — bağlantı bilgilerini manuel kontrol edin');
          }
          if (warnings.length > 0) {
            alert(warnings.join('\n'));
          }
          setForm(prev => ({
            ...prev,
            name: data.name || prev.name,
            oracle: oraResult ? oraResult.fields : prev.oracle,
            oracle_schema: data.oracle_schema || prev.oracle_schema,
            pg: pgResult ? pgResult.fields : prev.pg,
            pg_schema: data.pg_schema || prev.pg_schema,
            workers: mc.workers || prev.workers,
            batch_size: mc.batch_size || prev.batch_size,
            fetch_size: mc.fetch_size || prev.fetch_size,
            chunk_strategy: mc.chunk_strategy || prev.chunk_strategy,
            validate_after: mc.validate_after ?? prev.validate_after,
            drop_target: mc.drop_target ?? prev.drop_target,
            unlogged: mc.unlogged ?? prev.unlogged,
            naming_convention: mc.naming_convention || prev.naming_convention,
            include_tables: (mc.include_tables || []).join(', '),
            exclude_tables: (mc.exclude_tables || []).join(', '),
          }));
          setExistingConfig(mc);
        } catch {
          setError('Invalid JSON file');
        }
      };
      reader.readAsText(file);
    };
    input.click();
  };

  const handleSave = async () => {
    setError('');
    setSaving(true);
    try {
      const builtOra = buildOracleDSN(form.oracle);
      const builtPg = buildPgDSN(form.pg);
      const payload: Partial<Project> = {
        name: form.name,
        oracle_dsn: builtOra,
        oracle_schema: form.oracle_schema.toUpperCase(),
        pg_dsn: builtPg,
        pg_schema: form.pg_schema,
        migration_config: {
          ...existingConfig,
          workers: form.workers,
          batch_size: form.batch_size,
          fetch_size: form.fetch_size,
          validate_after: form.validate_after,
          drop_target: form.drop_target,
          unlogged: form.unlogged,
          include_tables: form.include_tables ? form.include_tables.split(',').map(s => s.trim()).filter(Boolean) : [],
          exclude_tables: form.exclude_tables ? form.exclude_tables.split(',').map(s => s.trim()).filter(Boolean) : [],
          naming_convention: form.naming_convention,
          chunk_strategy: form.chunk_strategy,
          table_overrides: existingConfig?.table_overrides || {},
        },
      };

      let result: Project;
      if (isEdit && id) {
        result = await projects.update(id, payload);
      } else {
        result = await projects.create(payload);
      }
      setSavedId(result.id);
      navigate(`/projects/${result.id}`);
    } catch (e: unknown) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  const testOracle = async () => {
    const dsn = buildOracleDSN(form.oracle);
    if (!dsn) {
      setError('Please fill in Oracle connection fields');
      return;
    }
    setTestingOra(true);
    setOraTest(null);
    try {
      const pid = savedId || '_';
      const result = await connections.testOracle(pid, dsn);
      setOraTest(result);
      if (result.schemas?.length) setOraSchemas(result.schemas);
    } catch (e: unknown) {
      setOraTest({ success: false, message: (e as Error).message, latency_ms: 0 });
    } finally {
      setTestingOra(false);
    }
  };

  const testPostgres = async () => {
    const dsn = buildPgDSN(form.pg);
    if (!dsn) {
      setError('Please fill in PostgreSQL connection fields');
      return;
    }
    setTestingPg(true);
    setPgTest(null);
    try {
      const pid = savedId || '_';
      const result = await connections.testPostgres(pid, dsn);
      setPgTest(result);
      if (result.schemas?.length) setPgSchemas(result.schemas);
    } catch (e: unknown) {
      setPgTest({ success: false, message: (e as Error).message, latency_ms: 0 });
    } finally {
      setTestingPg(false);
    }
  };

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="breadcrumb"><a href="/projects">Projects</a> / {isEdit ? 'Edit' : 'New'}</div>
          <h2>{isEdit ? 'Edit Project' : 'New Migration Project'}</h2>
        </div>
        <div className="flex gap-8">
          <button className="btn btn-sm btn-secondary" onClick={importSettings}>
            <Upload size={12} /> Import
          </button>
          <button className="btn btn-sm btn-secondary" onClick={exportSettings}>
            <Download size={12} /> Export
          </button>
        </div>
      </div>

      {error && <div className="alert alert-error">{error}</div>}

      <div className="card mb-16">
        <div className="card-header"><h3>Project</h3></div>
        <div className="form-group">
          <label>Project Name</label>
          <input className="form-input" placeholder="e.g. Production DB Migration" value={form.name} onChange={e => set('name', e.target.value)} />
        </div>
      </div>

      <div className="card-grid card-grid-2 mb-16">
        <div className="card">
          <div className="card-header">
            <h3>Oracle Source</h3>
            <button className="btn btn-sm btn-secondary" onClick={testOracle} disabled={testingOra}>
              {testingOra ? <span className="spinner" /> : <Zap size={12} />} Test
            </button>
          </div>
          {oraTest && <ConnectionResult result={oraTest} />}
          <div className="form-row">
            <div className="form-group">
              <label>Host</label>
              <input className="form-input text-mono" placeholder="localhost" value={form.oracle.host} onChange={e => setOracle('host', e.target.value)} />
            </div>
            <div className="form-group">
              <label>Port</label>
              <input className="form-input text-mono" placeholder="1521" value={form.oracle.port} onChange={e => setOracle('port', e.target.value)} />
            </div>
          </div>
          <div className="form-row">
            <div className="form-group">
              <label>User</label>
              <input className="form-input text-mono" placeholder="username" value={form.oracle.user} onChange={e => setOracle('user', e.target.value)} />
            </div>
            <div className="form-group">
              <label>Password</label>
              <input className="form-input text-mono" type="password" placeholder="••••••" value={form.oracle.password} onChange={e => setOracle('password', e.target.value)} onBlur={onOracleFieldBlur} />
            </div>
          </div>
          <div className="form-row">
            <div className="form-group">
              <label>Service Name</label>
              <input className="form-input text-mono" placeholder="ORCL" value={form.oracle.database} onChange={e => setOracle('database', e.target.value)} onBlur={onOracleFieldBlur} />
            </div>
            <div className="form-group">
              <label>Schema</label>
              {oraSchemas.length > 0 ? (
                <select className="form-input" value={form.oracle_schema} onChange={e => set('oracle_schema', e.target.value)}>
                  <option value="">-- Select Schema --</option>
                  {oraSchemas.map(s => <option key={s} value={s}>{s}</option>)}
                </select>
              ) : (
                <input className="form-input" placeholder="Fill connection to load" value={form.oracle_schema} onChange={e => set('oracle_schema', e.target.value)} />
              )}
            </div>
          </div>
        </div>

        <div className="card">
          <div className="card-header">
            <h3>PostgreSQL Target</h3>
            <button className="btn btn-sm btn-secondary" onClick={testPostgres} disabled={testingPg}>
              {testingPg ? <span className="spinner" /> : <Zap size={12} />} Test
            </button>
          </div>
          {pgTest && <ConnectionResult result={pgTest} />}
          <div className="form-row">
            <div className="form-group">
              <label>Host</label>
              <input className="form-input text-mono" placeholder="localhost" value={form.pg.host} onChange={e => setPg('host', e.target.value)} />
            </div>
            <div className="form-group">
              <label>Port</label>
              <input className="form-input text-mono" placeholder="5432" value={form.pg.port} onChange={e => setPg('port', e.target.value)} />
            </div>
          </div>
          <div className="form-row">
            <div className="form-group">
              <label>User</label>
              <input className="form-input text-mono" placeholder="username" value={form.pg.user} onChange={e => setPg('user', e.target.value)} />
            </div>
            <div className="form-group">
              <label>Password</label>
              <input className="form-input text-mono" type="password" placeholder="••••••" value={form.pg.password} onChange={e => setPg('password', e.target.value)} onBlur={onPgFieldBlur} />
            </div>
          </div>
          <div className="form-row">
            <div className="form-group">
              <label>Database</label>
              <input className="form-input text-mono" placeholder="mydb" value={form.pg.database} onChange={e => setPg('database', e.target.value)} onBlur={onPgFieldBlur} />
            </div>
            <div className="form-group">
              <label>Target Schema</label>
              {pgSchemas.length > 0 ? (
                <select className="form-input" value={form.pg_schema} onChange={e => set('pg_schema', e.target.value)}>
                  {pgSchemas.map(s => <option key={s} value={s}>{s}</option>)}
                </select>
              ) : (
                <input className="form-input" placeholder="public" value={form.pg_schema} onChange={e => set('pg_schema', e.target.value)} />
              )}
            </div>
          </div>
          <div className="form-group">
            <label>SSL Mode</label>
            <select className="form-input" value={form.pg.sslmode || 'disable'} onChange={e => setPg('sslmode', e.target.value)} style={{ maxWidth: 200 }}>
              <option value="disable">disable</option>
              <option value="require">require</option>
              <option value="verify-ca">verify-ca</option>
              <option value="verify-full">verify-full</option>
              <option value="prefer">prefer</option>
            </select>
          </div>
        </div>
      </div>

      <div className="card mb-16">
        <div className="card-header"><h3>Migration Settings</h3></div>
        <div className="form-row-3">
          <div className="form-group">
            <label>Workers (Parallel)</label>
            <input className="form-input" type="number" min={1} max={64} value={form.workers} onChange={e => set('workers', parseInt(e.target.value) || 4)} />
          </div>
          <div className="form-group">
            <label>Batch Size</label>
            <input className="form-input" type="number" min={100} max={100000} value={form.batch_size} onChange={e => set('batch_size', parseInt(e.target.value) || 5000)} />
          </div>
          <div className="form-group">
            <label>Fetch Size (Oracle)</label>
            <input className="form-input" type="number" min={100} max={100000} value={form.fetch_size} onChange={e => set('fetch_size', parseInt(e.target.value) || 5000)} />
          </div>
        </div>
        <div className="form-group mt-12">
          <label>Naming Convention</label>
          <select className="form-input" value={form.naming_convention} onChange={e => set('naming_convention', e.target.value)} style={{ maxWidth: 300 }}>
            <option value="lowercase">lowercase — CUSTOMERS → customers</option>
            <option value="uppercase">UPPERCASE — CUSTOMERS → CUSTOMERS</option>
            <option value="keep_original">Keep Original — CUSTOMERS → CUSTOMERS</option>
          </select>
          <span className="mode-hint">Controls how Oracle table/column names are converted in PostgreSQL</span>
        </div>
        <div className="form-group mt-12">
          <label>Chunk Strategy</label>
          <select className="form-input" value={form.chunk_strategy} onChange={e => set('chunk_strategy', e.target.value)} style={{ maxWidth: 300 }}>
            <option value="ora_hash">ORA_HASH — Instant planning, no privileges needed (recommended)</option>
            <option value="rowid">ROWID Range — Faster reads but slow planning (DBMS_PARALLEL_EXECUTE)</option>
            <option value="auto">Auto — Try ROWID first, fallback to ORA_HASH</option>
            <option value="offset">Offset — Slowest, universal compatibility</option>
          </select>
          <span className="mode-hint">ORA_HASH: instant start, reads N× per chunk. ROWID: planning can take hours for large schemas but reads 1×.</span>
        </div>
        <div className="flex gap-16 mt-12" style={{ flexWrap: 'wrap' }}>
          <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, cursor: 'pointer' }}>
            <input type="checkbox" checked={form.validate_after} onChange={e => set('validate_after', e.target.checked)} />
            Validate after migration
          </label>
          <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, cursor: 'pointer' }}>
            <input type="checkbox" checked={form.unlogged} onChange={e => {
              const checked = e.target.checked;
              setForm(prev => {
                if (checked) {
                  return {
                    ...prev,
                    unlogged: true,
                    batch_size: (prev.batch_size || 50000) * 4,
                    fetch_size: (prev.fetch_size || 5000) * 2,
                  };
                } else {
                  return {
                    ...prev,
                    unlogged: false,
                    batch_size: Math.round((prev.batch_size || 200000) / 4),
                    fetch_size: Math.round((prev.fetch_size || 10000) / 2),
                  };
                }
              });
            }} />
            UNLOGGED tables
            {form.unlogged && <span className="text-muted" style={{ fontSize: 11 }}>(Batch ×4, Fetch ×2 applied)</span>}
          </label>
        </div>
      </div>

      <div className="flex gap-12">
        <button className="btn btn-primary btn-lg" onClick={handleSave} disabled={saving}>
          {saving ? <span className="spinner" /> : <Save size={16} />}
          {isEdit ? 'Update Project' : 'Create Project'}
        </button>
        <button className="btn btn-secondary btn-lg" onClick={() => navigate('/projects')}>Cancel</button>
      </div>
    </div>
  );
}

function ConnectionResult({ result }: { result: ConnectionTestResult }) {
  return (
    <div className={`alert ${result.success ? 'alert-success' : 'alert-error'}`} style={{ marginBottom: 12 }}>
      <div className="flex gap-8" style={{ alignItems: 'center' }}>
        {result.success ? <CheckCircle size={14} /> : <XCircle size={14} />}
        <span>{result.message}</span>
        {result.latency_ms > 0 && <span className="text-muted text-sm">({result.latency_ms}ms)</span>}
      </div>
    </div>
  );
}
