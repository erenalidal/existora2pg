import { useEffect, useState, useMemo } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Copy, RefreshCw, Play, SkipForward, ChevronDown, ChevronRight, AlertTriangle, Info, ArrowRight } from 'lucide-react';
import { ddl as ddlApi, DDLPreview as DDLPreviewType } from '../api';

const MODE_LABELS: Record<string, { label: string; color: string }> = {
  recreate: { label: 'Recreate', color: 'var(--error)' },
  truncate: { label: 'Truncate', color: 'var(--warning)' },
  append: { label: 'Append', color: 'var(--success)' },
  skip: { label: 'Skip', color: 'var(--text-muted)' },
  truncate_partition: { label: 'Truncate Partition', color: 'var(--orange)' },
};

export function DDLPreview() {
  const { id } = useParams<{ id: string }>();
  const [data, setData] = useState<DDLPreviewType | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [filter, setFilter] = useState('all');
  const [showSkipped, setShowSkipped] = useState(false);
  const [copied, setCopied] = useState(false);
  const [collapsedTables, setCollapsedTables] = useState<Set<string>>(new Set());

  const load = () => {
    if (!id) return;
    setLoading(true);
    setError('');
    ddlApi.preview(id)
      .then(setData)
      .catch(e => setError(e.message))
      .finally(() => setLoading(false));
  };

  useEffect(load, [id]);

  const activeStatements = useMemo(() =>
    data?.statements.filter(s => !s.skipped) || [], [data]);

  const skippedStatements = useMemo(() =>
    data?.statements.filter(s => s.skipped) || [], [data]);

  const filteredActive = useMemo(() =>
    activeStatements.filter(s => filter === 'all' || s.type === filter), [activeStatements, filter]);

  // Group by table
  const groupedByTable = useMemo(() => {
    const map = new Map<string, typeof filteredActive>();
    for (const s of filteredActive) {
      const list = map.get(s.table) || [];
      list.push(s);
      map.set(s.table, list);
    }
    return map;
  }, [filteredActive]);

  const allDDL = filteredActive.map(s => s.statement).join('\n\n');

  const copyAll = () => {
    navigator.clipboard.writeText(allDDL);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const toggleCollapse = (table: string) => {
    setCollapsedTables(prev => {
      const next = new Set(prev);
      next.has(table) ? next.delete(table) : next.add(table);
      return next;
    });
  };

  const types = data ? [...new Set(activeStatements.map(s => s.type))] : [];

  if (loading) return <div className="loading-page"><span className="spinner" /> Generating DDL...</div>;
  if (error) return <div className="alert alert-error">{error}</div>;
  if (!data) return null;

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="breadcrumb"><Link to={`/projects/${id}`}>Dashboard</Link> / DDL Preview</div>
          <h2>DDL Preview</h2>
          <span className="text-muted text-sm">
            <Play size={12} style={{ display: 'inline', verticalAlign: 'middle' }} /> {activeStatements.length} active
            {skippedStatements.length > 0 && (
              <> &middot; <SkipForward size={12} style={{ display: 'inline', verticalAlign: 'middle' }} /> {skippedStatements.length} skipped</>
            )}
          </span>
        </div>
        <div className="flex gap-8">
          <button className="btn btn-secondary" onClick={copyAll}>
            <Copy size={14} /> {copied ? 'Copied!' : 'Copy All'}
          </button>
          <button className="btn btn-secondary" onClick={load}><RefreshCw size={14} /></button>
          <Link to={`/projects/${id}/migrate`} className="btn btn-primary">
            <Play size={14} /> Start Migration <ArrowRight size={14} />
          </Link>
        </div>
      </div>

      <div className="tabs">
        <button className={`tab ${filter === 'all' ? 'active' : ''}`} onClick={() => setFilter('all')}>
          All ({activeStatements.length})
        </button>
        {types.map(t => (
          <button key={t} className={`tab ${filter === t ? 'active' : ''}`} onClick={() => setFilter(t)}>
            {t} ({activeStatements.filter(s => s.type === t).length})
          </button>
        ))}
      </div>

      {/* Active statements grouped by table */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        {[...groupedByTable.entries()].map(([table, stmts]) => {
          const mode = stmts[0]?.target_mode;
          const modeInfo = mode ? MODE_LABELS[mode] : null;
          const isCollapsed = collapsedTables.has(table);

          return (
            <div key={table} className="card" style={{ padding: 0 }}>
              <div
                className="flex-between"
                style={{ padding: '10px 16px', borderBottom: isCollapsed ? 'none' : '1px solid var(--border)', cursor: 'pointer' }}
                onClick={() => toggleCollapse(table)}
              >
                <div className="flex gap-8" style={{ alignItems: 'center' }}>
                  {isCollapsed ? <ChevronRight size={14} /> : <ChevronDown size={14} />}
                  <span className="text-mono text-sm" style={{ fontWeight: 600 }}>{table}</span>
                  {modeInfo && (
                    <span className="badge" style={{ background: `${modeInfo.color}20`, color: modeInfo.color, fontSize: 10 }}>
                      {modeInfo.label}
                    </span>
                  )}
                  <span className="text-muted text-sm">{stmts.length} statement{stmts.length > 1 ? 's' : ''}</span>
                </div>
                <button className="btn btn-sm btn-secondary" onClick={e => { e.stopPropagation(); navigator.clipboard.writeText(stmts.map(s => s.statement).join('\n\n')); }}>
                  <Copy size={11} />
                </button>
              </div>

              {!isCollapsed && stmts.map((s, i) => {
                const isSkippedIndex = s.statement.startsWith('-- SKIPPED:');
                const hasNote = s.statement.startsWith('-- NOTE:');
                const noteLines = s.statement.split('\n').filter(l => l.startsWith('-- NOTE:') || l.startsWith('-- SKIPPED:'));
                const codeLines = s.statement.split('\n').filter(l => !l.startsWith('-- NOTE:') && !l.startsWith('-- SKIPPED:'));

                return (
                  <div key={i} style={{ borderBottom: i < stmts.length - 1 ? '1px solid var(--border)' : 'none' }}>
                    <div className="flex-between" style={{ padding: '6px 16px 0', alignItems: 'center' }}>
                      <div className="flex gap-8" style={{ alignItems: 'center' }}>
                        <span className="badge badge-info" style={{ fontSize: 10 }}>{s.type}</span>
                        {isSkippedIndex && (
                          <span className="badge" style={{ background: 'var(--warning-bg, rgba(234,179,8,0.1))', color: 'var(--warning, #eab308)', fontSize: 10 }}>
                            <AlertTriangle size={10} style={{ marginRight: 3 }} />Incompatible
                          </span>
                        )}
                        {hasNote && !isSkippedIndex && (
                          <span className="badge" style={{ background: 'rgba(59,130,246,0.1)', color: '#3b82f6', fontSize: 10 }}>
                            <Info size={10} style={{ marginRight: 3 }} />Converted
                          </span>
                        )}
                      </div>
                    </div>
                    {noteLines.length > 0 && (
                      <div style={{ padding: '6px 16px', fontSize: 12 }}>
                        {noteLines.map((line, li) => (
                          <div key={li} className="flex gap-8" style={{
                            alignItems: 'center',
                            padding: '4px 8px',
                            borderRadius: 4,
                            background: line.startsWith('-- SKIPPED:') ? 'rgba(234,179,8,0.08)' : 'rgba(59,130,246,0.08)',
                            color: line.startsWith('-- SKIPPED:') ? 'var(--warning, #eab308)' : '#3b82f6',
                            marginBottom: li < noteLines.length - 1 ? 4 : 0,
                          }}>
                            {line.startsWith('-- SKIPPED:') ? <AlertTriangle size={12} /> : <Info size={12} />}
                            <span style={{ fontSize: 11 }}>{line.replace(/^-- (SKIPPED|NOTE): /, '')}</span>
                          </div>
                        ))}
                      </div>
                    )}
                    {codeLines.join('\n').trim() && (
                      <pre className="code-block" style={{ borderRadius: 0, border: 'none', margin: 0, fontSize: 12 }}>{codeLines.join('\n').trim()}</pre>
                    )}
                  </div>
                );
              })}
            </div>
          );
        })}
      </div>

      {/* Skipped tables */}
      {skippedStatements.length > 0 && (
        <div style={{ marginTop: 24 }}>
          <button
            className="btn btn-sm btn-secondary"
            onClick={() => setShowSkipped(!showSkipped)}
            style={{ marginBottom: 12 }}
          >
            <SkipForward size={14} />
            {showSkipped ? 'Hide' : 'Show'} Skipped Tables ({skippedStatements.length})
          </button>

          {showSkipped && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {skippedStatements.map((s, i) => (
                <div key={i} className="card" style={{ padding: '10px 16px', opacity: 0.5 }}>
                  <div className="flex gap-8" style={{ alignItems: 'center' }}>
                    <SkipForward size={14} style={{ color: 'var(--text-muted)' }} />
                    <span className="text-mono text-sm">{s.table}</span>
                    <span className="badge badge-pending" style={{ fontSize: 10 }}>skip</span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
