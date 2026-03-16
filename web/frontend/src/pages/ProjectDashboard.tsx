import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Database, Code, Play, CheckCircle, Edit, RefreshCw, ListChecks, ArrowRight, Eye } from 'lucide-react';
import { projects, migration, Project, RunStatus } from '../api';

/** Format a masked DSN for display — strips password tokens and Oracle // prefix */
function formatDSN(dsn: string): string {
  if (!dsn) return '';
  // Oracle classic: user/****@//host → user@host
  if (!dsn.includes('://')) {
    return dsn.replace(/\/\*{3,}/, '').replace('@//', '@');
  }
  // URI: postgres://user:****@host → postgres://user@host
  return dsn.replace(/:(\*{3,})/, '');
}

export function ProjectDashboard() {
  const { id } = useParams<{ id: string }>();
  const [project, setProject] = useState<Project | null>(null);
  const [status, setStatus] = useState<RunStatus | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!id) return;
    Promise.all([
      projects.get(id).then(setProject),
      migration.status(id).then(setStatus).catch(() => null),
    ]).finally(() => setLoading(false));
  }, [id]);

  const refreshStatus = () => {
    if (!id) return;
    migration.status(id).then(setStatus).catch(() => null);
  };

  if (loading || !project) return <div className="loading-page"><span className="spinner" /> Loading...</div>;

  const ss = status?.summary;
  const hasRun = ss && ss.total > 0;
  const progressPercent = hasRun ? Math.round(((ss.completed + ss.skipped) / ss.total) * 100) : 0;
  const hasSelection = project.migration_config?.include_tables?.length > 0;

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="breadcrumb"><Link to="/projects">Projects</Link> / {project.name}</div>
          <h2>{project.name}</h2>
        </div>
        <Link to={`/projects/${id}/edit`} className="btn btn-secondary">
          <Edit size={14} /> Edit
        </Link>
      </div>

      {/* Connection info */}
      <div className="card-grid card-grid-2 mb-16">
        <div className="card">
          <h3 style={{ fontSize: 13, color: 'var(--text-muted)', marginBottom: 10 }}>ORACLE SOURCE</h3>
          <div className="text-mono" style={{ fontSize: 13 }}>{formatDSN(project.oracle_dsn)}</div>
          <div className="mt-12">
            <span className="badge badge-info">{project.oracle_schema}</span>
          </div>
        </div>
        <div className="card">
          <h3 style={{ fontSize: 13, color: 'var(--text-muted)', marginBottom: 10 }}>POSTGRESQL TARGET</h3>
          <div className="text-mono" style={{ fontSize: 13 }}>{formatDSN(project.pg_dsn)}</div>
          <div className="mt-12">
            <span className="badge badge-success">{project.pg_schema || 'public'}</span>
          </div>
        </div>
      </div>

      {/* Next step CTA */}
      {!hasRun && (
        <div className="card mb-16" style={{ background: 'rgba(99, 102, 241, 0.06)', borderColor: 'rgba(99, 102, 241, 0.2)' }}>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
            <div>
              <h3 style={{ fontSize: 16, marginBottom: 4 }}>
                {hasSelection ? 'Ready to migrate' : 'Get started'}
              </h3>
              <p className="text-muted text-sm">
                {hasSelection
                  ? `${project.migration_config.include_tables.length} tables selected. Review DDL or start migration.`
                  : 'Connect to Oracle, explore the schema, and select which objects to migrate.'}
              </p>
            </div>
            <Link
              to={`/projects/${id}/${hasSelection ? 'ddl' : 'objects'}`}
              className="btn btn-primary btn-lg"
            >
              {hasSelection ? <Code size={16} /> : <ListChecks size={16} />}
              {hasSelection ? 'Review DDL' : 'Select Objects'}
              <ArrowRight size={14} />
            </Link>
          </div>
        </div>
      )}

      {/* Migration status */}
      {hasRun && (
        <div className="card mb-16">
          <div className="card-header">
            <h3>Migration Status</h3>
            <button className="btn btn-sm btn-secondary" onClick={refreshStatus}>
              <RefreshCw size={12} /> Refresh
            </button>
          </div>
          <div className="progress-bar mb-16">
            <div
              className={`progress-bar-fill ${ss.failed > 0 ? 'error' : progressPercent === 100 ? 'success' : ''}`}
              style={{ width: `${progressPercent}%` }}
            />
          </div>
          <div className="card-grid card-grid-4">
            <div className="stat">
              <div className="stat-value">{ss.total}</div>
              <div className="stat-label">Total Jobs</div>
            </div>
            <div className="stat">
              <div className="stat-value" style={{ color: 'var(--success)' }}>{ss.completed + ss.skipped}</div>
              <div className="stat-label">Completed</div>
            </div>
            <div className="stat">
              <div className="stat-value" style={{ color: ss.running > 0 ? 'var(--accent)' : undefined }}>{ss.running}</div>
              <div className="stat-label">Running</div>
            </div>
            <div className="stat">
              <div className="stat-value" style={{ color: ss.failed > 0 ? 'var(--error)' : undefined }}>{ss.failed}</div>
              <div className="stat-label">Failed</div>
            </div>
          </div>
        </div>
      )}

      {/* Quick actions */}
      <div className="card mb-16">
        <div className="card-header"><h3>Actions</h3></div>
        <div className="flex gap-12" style={{ flexWrap: 'wrap' }}>
          <Link to={`/projects/${id}/objects`} className="btn btn-secondary">
            <ListChecks size={14} /> Select Objects
          </Link>
          <Link to={`/projects/${id}/schema`} className="btn btn-secondary">
            <Eye size={14} /> Schema Explorer
          </Link>
          <Link to={`/projects/${id}/ddl`} className="btn btn-secondary">
            <Code size={14} /> Preview DDL
          </Link>
          <Link to={`/projects/${id}/migrate`} className="btn btn-primary">
            <Play size={14} /> Start Migration
          </Link>
          <Link to={`/projects/${id}/validate`} className="btn btn-success">
            <CheckCircle size={14} /> Validate
          </Link>
        </div>
      </div>

      {/* Selected tables */}
      {hasSelection && (
        <div className="card mb-16">
          <div className="card-header">
            <h3>Selected Tables ({project.migration_config.include_tables.length})</h3>
            <Link to={`/projects/${id}/objects`} className="btn btn-sm btn-secondary">
              <Edit size={12} /> Edit Selection
            </Link>
          </div>
          <div className="flex gap-8" style={{ flexWrap: 'wrap' }}>
            {project.migration_config.include_tables.map(t => (
              <span key={t} className="badge badge-info">{t}</span>
            ))}
          </div>
        </div>
      )}

      {/* Config summary */}
      <div className="card">
        <div className="card-header"><h3>Configuration</h3></div>
        <div className="card-grid card-grid-3">
          <div>
            <div className="text-muted text-sm">Workers</div>
            <div style={{ fontSize: 18, fontWeight: 600 }}>{project.migration_config?.workers || (navigator.hardwareConcurrency > 8 ? 8 : navigator.hardwareConcurrency || 4)}</div>
          </div>
          <div>
            <div className="text-muted text-sm">Batch Size</div>
            <div style={{ fontSize: 18, fontWeight: 600 }}>{(project.migration_config?.batch_size || 50000).toLocaleString()}</div>
          </div>
          <div>
            <div className="text-muted text-sm">Fetch Size</div>
            <div style={{ fontSize: 18, fontWeight: 600 }}>{(project.migration_config?.fetch_size || 5000).toLocaleString()}</div>
          </div>
        </div>
      </div>
    </div>
  );
}
