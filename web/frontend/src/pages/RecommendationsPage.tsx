import { useState, useEffect } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Lightbulb, Server, Database, Cpu, HardDrive, ChevronDown, ChevronRight, Gauge, Zap, Shield, RefreshCw, CheckCircle, AlertTriangle, Info } from 'lucide-react';
import { projects, recommendations, Project, DBSetting, OracleSettingsResponse } from '../api';

export function RecommendationsPage() {
  const { id } = useParams<{ id: string }>();
  const [project, setProject] = useState<Project | null>(null);
  const [pgSettings, setPgSettings] = useState<DBSetting[]>([]);
  const [oracleSettings, setOracleSettings] = useState<DBSetting[]>([]);
  const [oracleSchemaInfo, setOracleSchemaInfo] = useState<OracleSettingsResponse['schema_info'] | null>(null);
  const [oracleVersion, setOracleVersion] = useState('');
  const [isExadata, setIsExadata] = useState(false);
  const [pgLoading, setPgLoading] = useState(false);
  const [oraLoading, setOraLoading] = useState(false);
  const [pgError, setPgError] = useState('');
  const [oraError, setOraError] = useState('');
  const [expanded, setExpanded] = useState<Set<string>>(new Set(['pg-live', 'ora-live', 'resources', 'existora']));

  useEffect(() => {
    if (id) projects.get(id).then(setProject);
  }, [id]);

  const loadPGSettings = () => {
    if (!id) return;
    setPgLoading(true);
    setPgError('');
    recommendations.pgSettings(id)
      .then(r => setPgSettings(r.settings))
      .catch(e => setPgError(e.message))
      .finally(() => setPgLoading(false));
  };

  const loadOracleSettings = () => {
    if (!id) return;
    setOraLoading(true);
    setOraError('');
    recommendations.oracleSettings(id)
      .then(r => {
        setOracleSettings(r.settings);
        setOracleSchemaInfo(r.schema_info);
        setOracleVersion(r.version);
        setIsExadata(r.is_exadata);
      })
      .catch(e => setOraError(e.message))
      .finally(() => setOraLoading(false));
  };

  const workers = project?.migration_config?.workers || 4;
  const batchSize = project?.migration_config?.batch_size || 50000;

  const toggle = (sectionId: string) => {
    setExpanded(prev => {
      const next = new Set(prev);
      next.has(sectionId) ? next.delete(sectionId) : next.add(sectionId);
      return next;
    });
  };

  const statusIcon = (status: string) => {
    switch (status) {
      case 'ok': return <CheckCircle size={14} style={{ color: 'var(--success)' }} />;
      case 'warn': return <AlertTriangle size={14} style={{ color: 'var(--warning, #eab308)' }} />;
      default: return <Info size={14} style={{ color: 'var(--accent)' }} />;
    }
  };

  const statusColor = (status: string) => {
    switch (status) {
      case 'ok': return { bg: 'rgba(34,197,94,0.08)', border: 'rgba(34,197,94,0.2)' };
      case 'warn': return { bg: 'rgba(234,179,8,0.08)', border: 'rgba(234,179,8,0.2)' };
      default: return { bg: 'transparent', border: 'var(--border)' };
    }
  };

  const SettingsTable = ({ settings, loading, error, onLoad, label }: {
    settings: DBSetting[], loading: boolean, error: string, onLoad: () => void, label: string
  }) => (
    <div>
      {settings.length === 0 && !loading && !error && (
        <div style={{ padding: 16, textAlign: 'center' }}>
          <button className="btn btn-primary btn-sm" onClick={onLoad}>
            <Zap size={12} /> Check {label} Settings
          </button>
          <p className="text-muted text-sm" style={{ marginTop: 8 }}>Connect to {label} and analyze current configuration</p>
        </div>
      )}
      {loading && (
        <div style={{ padding: 16, textAlign: 'center' }}>
          <span className="spinner" /> Connecting to {label}...
        </div>
      )}
      {error && (
        <div className="alert alert-error" style={{ margin: 12 }}>{error}</div>
      )}
      {settings.length > 0 && (
        <div>
          <div style={{ padding: '8px 16px', display: 'flex', justifyContent: 'flex-end' }}>
            <button className="btn btn-sm btn-secondary" onClick={onLoad} disabled={loading}>
              <RefreshCw size={11} /> Refresh
            </button>
          </div>
          <table className="data-table" style={{ fontSize: 12 }}>
            <thead>
              <tr>
                <th style={{ width: 30 }}></th>
                <th>Parameter</th>
                <th>Current Value</th>
                <th>Recommended</th>
                <th>Description</th>
              </tr>
            </thead>
            <tbody>
              {settings.map(s => {
                const sc = statusColor(s.status);
                return (
                  <tr key={s.name} style={{ background: sc.bg }}>
                    <td style={{ textAlign: 'center' }}>{statusIcon(s.status)}</td>
                    <td className="mono" style={{ fontWeight: 600 }}>{s.name}</td>
                    <td className="mono">
                      {s.current}
                      {s.unit && <span className="text-muted"> {s.unit}</span>}
                    </td>
                    <td style={{ color: 'var(--accent)', fontSize: 11 }}>{s.recommended}</td>
                    <td className="text-muted" style={{ fontSize: 11 }}>{s.description}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          {settings.some(s => s.status === 'ok') && (
            <div style={{ padding: '8px 16px', fontSize: 11 }} className="flex gap-8 text-muted">
              <CheckCircle size={12} style={{ color: 'var(--success)' }} /> = optimal for migration
              <AlertTriangle size={12} style={{ color: 'var(--warning, #eab308)', marginLeft: 8 }} /> = consider changing
              <Info size={12} style={{ color: 'var(--accent)', marginLeft: 8 }} /> = informational
            </div>
          )}
        </div>
      )}
    </div>
  );

  interface TipItem {
    title: string;
    description: string;
    code?: string;
    impact: 'high' | 'medium' | 'low';
    when?: string;
  }

  interface TipSection {
    id: string;
    icon: React.ReactNode;
    title: string;
    items: TipItem[];
  }

  const tipSections: TipSection[] = [
    {
      id: 'resources',
      icon: <Cpu size={16} />,
      title: 'Resource Sizing Guide',
      items: [
        {
          title: 'CPU',
          description: `Each worker uses ~1 CPU core. Current: ${workers} workers.`,
          code: `Minimum: ${workers} cores\nRecommended: ${workers + 4} cores (workers + OS/network)`,
          impact: 'medium',
        },
        {
          title: 'Memory (RAM)',
          description: 'Consumed by Oracle fetch buffers + PG COPY batch buffers per worker.',
          code: `Formula: workers x batch_size x avg_row_bytes x 3 + 512MB base\n\nCurrent: ${workers} workers, ${batchSize.toLocaleString()} batch_size\n  Small rows (~200B):  ~${Math.round(workers * batchSize * 200 * 3 / 1024 / 1024 / 1024 + 0.5)} GB\n  Medium rows (~1KB):  ~${Math.round(workers * batchSize * 1024 * 3 / 1024 / 1024 / 1024 + 0.5)} GB\n  Large rows (~5KB):   ~${Math.round(workers * batchSize * 5120 * 3 / 1024 / 1024 / 1024 + 0.5)} GB`,
          impact: 'high',
        },
        {
          title: 'Network',
          description: 'At 100K rows/sec with 1KB rows: ~200 MB/s bidirectional.',
          code: `Same datacenter: 1 Gbps min, 10 Gbps recommended\nCross-datacenter: 10 Gbps min`,
          impact: 'high',
        },
        {
          title: 'Disk I/O (PG Target)',
          description: 'COPY generates sequential writes. NVMe SSD strongly recommended.',
          code: `At 100K rows/sec, 1KB rows:\n  Data: ~100 MB/s, WAL: ~130 MB/s\n  Tip: Separate disks for data + WAL`,
          impact: 'medium',
        },
      ],
    },
    {
      id: 'existora',
      icon: <Gauge size={16} />,
      title: 'existora2pg Tips',
      items: [
        {
          title: 'Async Commit (Auto)',
          description: 'existora2pg automatically sets synchronous_commit=off per COPY session. Each COMMIT skips WAL fsync wait — 3-5x throughput improvement. Crash-safe: retry + resume handles any data loss.',
          code: `# Automatic — no config needed\n# Set per-session on each COPY connection\nSET synchronous_commit = off`,
          impact: 'high',
          when: 'Always active',
        },
        {
          title: 'Chunked COPY',
          description: `Current batch_size: ${batchSize.toLocaleString()}. Each batch is a separate transaction to bound WAL pressure. 50K is optimal for most workloads.`,
          code: `migration:\n  batch_size: 50000\n  # Per-table override:\n  table_overrides:\n    WIDE_TABLE:\n      batch_size: 10000`,
          impact: 'high',
        },
        {
          title: 'UNLOGGED Mode',
          description: 'Skip WAL entirely during bulk load, convert to LOGGED after. Maximum throughput but no crash recovery until conversion.',
          code: `postgres:\n  unlogged: true\n\n# Combined with async commit:\n# UNLOGGED = no WAL generation at all\n# async commit = no WAL fsync wait (for LOGGED tables)`,
          impact: 'high',
          when: 'Fresh migration, no replication',
        },
        {
          title: 'Fault Tolerance',
          description: 'Individual table failures do NOT stop other tables. Failed tables can be resumed later while the rest complete normally.',
          code: `Worker pool: continue-on-error (only user cancel stops all)\nConnection: 3 retries with backoff on acquire timeout\nBatch: 3 retries with exp backoff (1s, 2s, 4s)\nPipeline: 3 retries, truncate+re-read (non-chunked)\nResume: manual, FAILED → RETRYING → re-run`,
          impact: 'high',
        },
        {
          title: 'Index Strategy',
          description: 'Indexes and constraints created AFTER data load (5-10x faster than loading with indexes).',
          code: `Phase order: DDL → DATA → PK/INDEX → FK/UNIQUE → VALIDATE\nPer-table: skip_indexes, skip_constraints, skip_validation\nIndex mode: create (default), concurrent, skip`,
          impact: 'medium',
        },
      ],
    },
  ];

  const impactBadge = (impact: string) => {
    const colors = {
      high: { bg: 'rgba(239,68,68,0.1)', color: '#ef4444' },
      medium: { bg: 'rgba(234,179,8,0.1)', color: '#eab308' },
      low: { bg: 'rgba(59,130,246,0.1)', color: '#3b82f6' },
    };
    const c = colors[impact as keyof typeof colors] || colors.low;
    return (
      <span className="badge" style={{ background: c.bg, color: c.color, fontSize: 10 }}>
        {impact === 'high' ? <Zap size={10} /> : <Gauge size={10} />}
        &nbsp;{impact}
      </span>
    );
  };

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="breadcrumb">
            <Link to={`/projects/${id}`}>Dashboard</Link> / Recommendations
          </div>
          <h2>Performance Recommendations</h2>
          <span className="text-muted text-sm">
            Live settings analysis + tuning tips
          </span>
        </div>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        {/* PostgreSQL Live Settings */}
        <div className="card" style={{ padding: 0 }}>
          <div
            className="flex-between"
            style={{ padding: '12px 16px', cursor: 'pointer', borderBottom: expanded.has('pg-live') ? '1px solid var(--border)' : 'none' }}
            onClick={() => toggle('pg-live')}
          >
            <div className="flex gap-8" style={{ alignItems: 'center' }}>
              {expanded.has('pg-live') ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
              <Server size={16} />
              <span style={{ fontWeight: 600, fontSize: 14 }}>PostgreSQL Settings</span>
              {pgSettings.length > 0 && (
                <>
                  <span className="badge badge-success" style={{ fontSize: 10 }}>
                    {pgSettings.filter(s => s.status === 'ok').length} optimal
                  </span>
                  {pgSettings.filter(s => s.status === 'warn').length > 0 && (
                    <span className="badge" style={{ background: 'rgba(234,179,8,0.1)', color: '#eab308', fontSize: 10 }}>
                      {pgSettings.filter(s => s.status === 'warn').length} to review
                    </span>
                  )}
                </>
              )}
            </div>
          </div>
          {expanded.has('pg-live') && (
            <SettingsTable
              settings={pgSettings}
              loading={pgLoading}
              error={pgError}
              onLoad={loadPGSettings}
              label="PostgreSQL"
            />
          )}
        </div>

        {/* Oracle Live Settings */}
        <div className="card" style={{ padding: 0 }}>
          <div
            className="flex-between"
            style={{ padding: '12px 16px', cursor: 'pointer', borderBottom: expanded.has('ora-live') ? '1px solid var(--border)' : 'none' }}
            onClick={() => toggle('ora-live')}
          >
            <div className="flex gap-8" style={{ alignItems: 'center' }}>
              {expanded.has('ora-live') ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
              <Database size={16} />
              <span style={{ fontWeight: 600, fontSize: 14 }}>Oracle Settings</span>
              {isExadata && (
                <span className="badge" style={{ background: 'rgba(168,85,247,0.1)', color: '#a855f7', fontSize: 10 }}>
                  <Zap size={10} /> Exadata
                </span>
              )}
              {oracleVersion && (
                <span className="text-muted text-sm" style={{ fontSize: 10 }}>{oracleVersion.slice(0, 40)}</span>
              )}
              {oracleSettings.length > 0 && (
                <>
                  <span className="badge badge-success" style={{ fontSize: 10 }}>
                    {oracleSettings.filter(s => s.status === 'ok').length} optimal
                  </span>
                  {oracleSettings.filter(s => s.status === 'warn').length > 0 && (
                    <span className="badge" style={{ background: 'rgba(234,179,8,0.1)', color: '#eab308', fontSize: 10 }}>
                      {oracleSettings.filter(s => s.status === 'warn').length} to review
                    </span>
                  )}
                </>
              )}
            </div>
          </div>
          {expanded.has('ora-live') && (
            <>
              <SettingsTable
                settings={oracleSettings}
                loading={oraLoading}
                error={oraError}
                onLoad={loadOracleSettings}
                label="Oracle"
              />
              {oracleSchemaInfo && (
                <div style={{ borderTop: '1px solid var(--border)' }}>
                  <div style={{ padding: '10px 16px', fontSize: 12, fontWeight: 600, color: 'var(--text-secondary)' }}>
                    Schema Overview — {project?.oracle_schema}
                  </div>
                  <div className="card-grid card-grid-4" style={{ padding: '0 16px 12px', gap: 12 }}>
                    <div className="stat">
                      <div className="stat-value">{oracleSchemaInfo.table_count}</div>
                      <div className="stat-label">Tables</div>
                    </div>
                    <div className="stat">
                      <div className="stat-value">{oracleSchemaInfo.total_rows?.toLocaleString()}</div>
                      <div className="stat-label">Total Rows (est)</div>
                    </div>
                    <div className="stat">
                      <div className="stat-value">
                        {oracleSchemaInfo.total_size_mb >= 1024
                          ? `${(oracleSchemaInfo.total_size_mb / 1024).toFixed(1)} GB`
                          : `${Math.round(oracleSchemaInfo.total_size_mb)} MB`}
                      </div>
                      <div className="stat-label">Total Size</div>
                    </div>
                    <div className="stat">
                      <div className="stat-value">{oracleSchemaInfo.partitioned_tables}</div>
                      <div className="stat-label">Partitioned</div>
                    </div>
                  </div>
                  <div style={{ padding: '0 16px 4px' }}>
                    <div className="flex gap-16" style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                      {oracleSchemaInfo.lob_columns > 0 && (
                        <span>LOB columns: {oracleSchemaInfo.lob_columns}</span>
                      )}
                      {oracleSchemaInfo.last_analyzed && (
                        <span>Last ANALYZE: {oracleSchemaInfo.last_analyzed}</span>
                      )}
                    </div>
                  </div>
                  {oracleSchemaInfo.largest_tables && oracleSchemaInfo.largest_tables.length > 0 && (
                    <div style={{ padding: '8px 16px 12px' }}>
                      <div style={{ fontSize: 11, fontWeight: 600, color: 'var(--text-secondary)', marginBottom: 6 }}>Largest Tables</div>
                      <table className="data-table" style={{ fontSize: 11 }}>
                        <thead>
                          <tr><th>Table</th><th style={{ textAlign: 'right' }}>Rows</th><th style={{ textAlign: 'right' }}>Size</th></tr>
                        </thead>
                        <tbody>
                          {oracleSchemaInfo.largest_tables.map(t => (
                            <tr key={t.name}>
                              <td className="mono">{t.name}</td>
                              <td style={{ textAlign: 'right' }}>{t.rows.toLocaleString()}</td>
                              <td style={{ textAlign: 'right' }}>
                                {t.size_mb >= 1024 ? `${(t.size_mb / 1024).toFixed(1)} GB` : `${Math.round(t.size_mb)} MB`}
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  )}
                </div>
              )}
            </>
          )}
        </div>

        {/* Static tip sections */}
        {tipSections.map(section => (
          <div key={section.id} className="card" style={{ padding: 0 }}>
            <div
              className="flex-between"
              style={{ padding: '12px 16px', cursor: 'pointer', borderBottom: expanded.has(section.id) ? '1px solid var(--border)' : 'none' }}
              onClick={() => toggle(section.id)}
            >
              <div className="flex gap-8" style={{ alignItems: 'center' }}>
                {expanded.has(section.id) ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                {section.icon}
                <span style={{ fontWeight: 600, fontSize: 14 }}>{section.title}</span>
                <span className="text-muted text-sm">{section.items.length} tips</span>
              </div>
            </div>
            {expanded.has(section.id) && (
              <div>
                {section.items.map((item, i) => (
                  <div key={i} style={{ padding: '12px 16px', borderBottom: i < section.items.length - 1 ? '1px solid var(--border)' : 'none' }}>
                    <div className="flex gap-8" style={{ alignItems: 'center', marginBottom: 6 }}>
                      <span style={{ fontWeight: 600, fontSize: 13 }}>{item.title}</span>
                      {impactBadge(item.impact)}
                      {item.when && <span className="text-muted text-sm" style={{ fontStyle: 'italic' }}>{item.when}</span>}
                    </div>
                    <p style={{ fontSize: 12, color: 'var(--text-secondary)', margin: '0 0 6px', lineHeight: 1.5 }}>
                      {item.description}
                    </p>
                    {item.code && (
                      <pre className="code-block" style={{ fontSize: 11, margin: 0, borderRadius: 6 }}>{item.code}</pre>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
