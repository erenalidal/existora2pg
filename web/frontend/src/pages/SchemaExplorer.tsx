import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { ChevronDown, ChevronRight, Table2, Key, Hash, Layers, RefreshCw, Eye, Database, Zap, Code } from 'lucide-react';
import { schema as schemaApi, SchemaInfo, TableInfo, ViewInfo, MaterializedViewInfo, TriggerInfo, ProcedureInfo } from '../api';

export function SchemaExplorer() {
  const { id } = useParams<{ id: string }>();
  const [schemaData, setSchemaData] = useState<SchemaInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [expandedTables, setExpandedTables] = useState<Set<string>>(new Set());
  const [expandedSections, setExpandedSections] = useState<Set<string>>(new Set(['tables']));
  const [selectedTable, setSelectedTable] = useState<TableInfo | null>(null);
  const [selectedObject, setSelectedObject] = useState<{ type: string; data: ViewInfo | MaterializedViewInfo | TriggerInfo | ProcedureInfo } | null>(null);

  const load = () => {
    if (!id) return;
    setLoading(true);
    setError('');
    schemaApi.get(id)
      .then(setSchemaData)
      .catch(e => setError(e.message))
      .finally(() => setLoading(false));
  };

  useEffect(load, [id]);

  const toggleTable = (name: string) => {
    setExpandedTables(prev => {
      const next = new Set(prev);
      next.has(name) ? next.delete(name) : next.add(name);
      return next;
    });
  };

  const toggleSection = (name: string) => {
    setExpandedSections(prev => {
      const next = new Set(prev);
      next.has(name) ? next.delete(name) : next.add(name);
      return next;
    });
  };

  const viewCount = schemaData?.views?.length || 0;
  const mviewCount = schemaData?.materialized_views?.length || 0;
  const triggerCount = schemaData?.triggers?.length || 0;
  const procCount = schemaData?.procedures?.length || 0;
  const nonTableCount = viewCount + mviewCount + triggerCount + procCount;

  if (loading) return <div className="loading-page"><span className="spinner" /> Extracting Oracle metadata...</div>;
  if (error) return <div className="alert alert-error">{error}</div>;
  if (!schemaData) return null;

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="breadcrumb"><Link to={`/projects/${id}`}>Dashboard</Link> / Schema</div>
          <h2>Schema: {schemaData.owner}</h2>
          <span className="text-muted text-sm">
            {schemaData.tables.length} tables, {schemaData.sequences.length} sequences
            {nonTableCount > 0 && `, ${nonTableCount} other objects`}
          </span>
        </div>
        <button className="btn btn-secondary" onClick={load}><RefreshCw size={14} /> Refresh</button>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '320px 1fr', gap: 16 }}>
        {/* Object tree */}
        <div className="card" style={{ maxHeight: 'calc(100vh - 140px)', overflowY: 'auto' }}>
          {/* Tables section */}
          <div className="tree-toggle" onClick={() => toggleSection('tables')} style={{ fontWeight: 600, marginBottom: 4 }}>
            {expandedSections.has('tables') ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
            <Table2 size={14} />
            <span>Tables ({schemaData.tables.length})</span>
          </div>
          {expandedSections.has('tables') && schemaData.tables.map(t => (
            <div key={t.name} style={{ paddingLeft: 12 }}>
              <div className="tree-toggle" onClick={() => { toggleTable(t.name); setSelectedTable(t); setSelectedObject(null); }}>
                {expandedTables.has(t.name) ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                <Table2 size={14} />
                <span>{t.name}</span>
                {t.partitioning && <Layers size={12} style={{ color: 'var(--accent)' }} />}
              </div>
              {expandedTables.has(t.name) && (
                <div className="tree-item">
                  {t.columns.map(c => (
                    <div key={c.name} className="tree-leaf">
                      {c.name} <span className="text-muted">{c.oracle_type} → {c.pg_type}</span>
                      {!c.nullable && <span style={{ color: 'var(--warning)', marginLeft: 4 }}>NOT NULL</span>}
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}

          {/* Views */}
          {viewCount > 0 && (
            <>
              <div className="tree-toggle" onClick={() => toggleSection('views')} style={{ fontWeight: 600, marginTop: 8, marginBottom: 4 }}>
                {expandedSections.has('views') ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                <Eye size={14} />
                <span>Views ({viewCount})</span>
                <span className="badge badge-warning" style={{ fontSize: 10, marginLeft: 'auto' }}>Manual</span>
              </div>
              {expandedSections.has('views') && schemaData.views!.map(v => (
                <div key={v.name} style={{ paddingLeft: 12 }}>
                  <div className="tree-toggle" onClick={() => { setSelectedObject({ type: 'view', data: v }); setSelectedTable(null); }}>
                    <Eye size={14} />
                    <span>{v.name}</span>
                  </div>
                </div>
              ))}
            </>
          )}

          {/* Materialized Views */}
          {mviewCount > 0 && (
            <>
              <div className="tree-toggle" onClick={() => toggleSection('mviews')} style={{ fontWeight: 600, marginTop: 8, marginBottom: 4 }}>
                {expandedSections.has('mviews') ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                <Database size={14} />
                <span>Materialized Views ({mviewCount})</span>
                <span className="badge badge-warning" style={{ fontSize: 10, marginLeft: 'auto' }}>Manual</span>
              </div>
              {expandedSections.has('mviews') && schemaData.materialized_views!.map(mv => (
                <div key={mv.name} style={{ paddingLeft: 12 }}>
                  <div className="tree-toggle" onClick={() => { setSelectedObject({ type: 'mview', data: mv }); setSelectedTable(null); }}>
                    <Database size={14} />
                    <span>{mv.name}</span>
                  </div>
                </div>
              ))}
            </>
          )}

          {/* Triggers */}
          {triggerCount > 0 && (
            <>
              <div className="tree-toggle" onClick={() => toggleSection('triggers')} style={{ fontWeight: 600, marginTop: 8, marginBottom: 4 }}>
                {expandedSections.has('triggers') ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                <Zap size={14} />
                <span>Triggers ({triggerCount})</span>
                <span className="badge badge-warning" style={{ fontSize: 10, marginLeft: 'auto' }}>Manual</span>
              </div>
              {expandedSections.has('triggers') && schemaData.triggers!.map(tr => (
                <div key={tr.name} style={{ paddingLeft: 12 }}>
                  <div className="tree-toggle" onClick={() => { setSelectedObject({ type: 'trigger', data: tr }); setSelectedTable(null); }}>
                    <Zap size={14} />
                    <span>{tr.name}</span>
                    <span className="text-muted text-sm" style={{ marginLeft: 4 }}>on {(tr as TriggerInfo).table_name}</span>
                  </div>
                </div>
              ))}
            </>
          )}

          {/* Procedures / Functions / Packages */}
          {procCount > 0 && (
            <>
              <div className="tree-toggle" onClick={() => toggleSection('procedures')} style={{ fontWeight: 600, marginTop: 8, marginBottom: 4 }}>
                {expandedSections.has('procedures') ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                <Code size={14} />
                <span>Procedures ({procCount})</span>
                <span className="badge badge-warning" style={{ fontSize: 10, marginLeft: 'auto' }}>Manual</span>
              </div>
              {expandedSections.has('procedures') && schemaData.procedures!.map(p => (
                <div key={p.name} style={{ paddingLeft: 12 }}>
                  <div className="tree-toggle" onClick={() => { setSelectedObject({ type: 'procedure', data: p }); setSelectedTable(null); }}>
                    <Code size={14} />
                    <span>{p.name}</span>
                    <span className="text-muted text-sm" style={{ marginLeft: 4 }}>{(p as ProcedureInfo).object_type}</span>
                  </div>
                </div>
              ))}
            </>
          )}
        </div>

        {/* Detail panel */}
        <div>
          {selectedTable ? (
            <TableDetail table={selectedTable} />
          ) : selectedObject ? (
            <ObjectDetail type={selectedObject.type} data={selectedObject.data} />
          ) : (
            <div className="empty-state"><p>Select a table or object to view details</p></div>
          )}
        </div>
      </div>
    </div>
  );
}

function TableDetail({ table }: { table: TableInfo }) {
  const [tab, setTab] = useState<'columns' | 'constraints' | 'indexes' | 'partitions'>('columns');

  return (
    <div className="card">
      <h3 style={{ fontSize: 16, marginBottom: 4 }}>{table.name}</h3>
      <div className="flex gap-8 mb-16">
        <span className="text-muted text-sm">{table.columns.length} columns</span>
        {table.partitioning && (
          <span className="badge badge-info">
            {table.partitioning.strategy} ({table.partitioning.partitions.length} partitions)
          </span>
        )}
      </div>

      <div className="tabs">
        <button className={`tab ${tab === 'columns' ? 'active' : ''}`} onClick={() => setTab('columns')}>Columns</button>
        <button className={`tab ${tab === 'constraints' ? 'active' : ''}`} onClick={() => setTab('constraints')}>
          Constraints {table.constraints.length > 0 && `(${table.constraints.length})`}
        </button>
        <button className={`tab ${tab === 'indexes' ? 'active' : ''}`} onClick={() => setTab('indexes')}>
          Indexes {table.indexes.length > 0 && `(${table.indexes.length})`}
        </button>
        {table.partitioning && (
          <button className={`tab ${tab === 'partitions' ? 'active' : ''}`} onClick={() => setTab('partitions')}>
            Partitions ({table.partitioning.partitions.length})
          </button>
        )}
      </div>

      {tab === 'columns' && (
        <table className="data-table">
          <thead><tr><th>#</th><th>Column</th><th>Oracle Type</th><th>PG Type</th><th>Nullable</th></tr></thead>
          <tbody>
            {table.columns.map(c => (
              <tr key={c.name}>
                <td className="text-muted">{c.position}</td>
                <td className="mono">
                  {table.primary_key?.columns.includes(c.name) && <Key size={11} style={{ color: 'var(--warning)', marginRight: 4 }} />}
                  {c.name}
                </td>
                <td className="mono">{c.oracle_type}</td>
                <td className="mono" style={{ color: 'var(--accent)' }}>{c.pg_type}</td>
                <td>{c.nullable ? <span className="text-muted">YES</span> : <span style={{ color: 'var(--warning)' }}>NO</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {tab === 'constraints' && (
        <table className="data-table">
          <thead><tr><th>Name</th><th>Type</th><th>Columns</th><th>References</th></tr></thead>
          <tbody>
            {table.primary_key && (
              <tr>
                <td className="mono">{table.primary_key.name}</td>
                <td><span className="badge badge-warning">PK</span></td>
                <td className="mono">{table.primary_key.columns.join(', ')}</td>
                <td />
              </tr>
            )}
            {table.constraints.map(c => (
              <tr key={c.name}>
                <td className="mono">{c.name}</td>
                <td>
                  <span className={`badge ${c.type === 'R' ? 'badge-info' : c.type === 'U' ? 'badge-success' : 'badge-pending'}`}>
                    {c.type === 'R' ? 'FK' : c.type === 'U' ? 'UNIQUE' : 'CHECK'}
                  </span>
                </td>
                <td className="mono">{c.columns.join(', ')}</td>
                <td className="mono text-muted">{c.ref_table || ''}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {tab === 'indexes' && (
        <table className="data-table">
          <thead><tr><th>Name</th><th>Columns</th><th>Unique</th></tr></thead>
          <tbody>
            {table.indexes.map(idx => (
              <tr key={idx.name}>
                <td className="mono">{idx.name}</td>
                <td className="mono">{idx.columns.join(', ')}</td>
                <td>{idx.unique ? <Hash size={14} style={{ color: 'var(--success)' }} /> : ''}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {tab === 'partitions' && table.partitioning && (
        <table className="data-table">
          <thead><tr><th>#</th><th>Partition</th><th>High Value</th></tr></thead>
          <tbody>
            {table.partitioning.partitions.map(p => (
              <tr key={p.name}>
                <td className="text-muted">{p.position}</td>
                <td className="mono">{p.name}</td>
                <td className="mono text-muted" style={{ fontSize: 11 }}>{p.high_value}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

function ObjectDetail({ type, data }: { type: string; data: ViewInfo | MaterializedViewInfo | TriggerInfo | ProcedureInfo }) {
  const [codeTab, setCodeTab] = useState<'oracle' | 'pg'>('pg');

  // Reset tab to PG when object changes
  useEffect(() => { setCodeTab('pg'); }, [data.name, type]);

  const labels: Record<string, string> = {
    view: 'View',
    mview: 'Materialized View',
    trigger: 'Trigger',
    procedure: 'Procedure / Function',
  };

  const pgDef = (data as any).pg_definition as string | undefined;
  const warnings = (data as any).warnings as string[] | undefined;
  const hasWarnings = warnings && warnings.length > 0;

  return (
    <div className="card">
      <div className="flex gap-8 mb-16" style={{ alignItems: 'center' }}>
        <h3 style={{ fontSize: 16, margin: 0 }}>{data.name}</h3>
        <span className="badge badge-warning">{labels[type] || type}</span>
        {hasWarnings ? (
          <span className="badge badge-pending">Review Required</span>
        ) : (
          <span className="badge badge-success">Auto-converted</span>
        )}
      </div>

      {type === 'mview' && (
        <div className="mb-16">
          <span className="text-muted text-sm">Refresh Mode: </span>
          <span className="mono">{(data as MaterializedViewInfo).refresh_mode}</span>
        </div>
      )}

      {type === 'trigger' && (
        <div className="mb-16">
          <span className="text-muted text-sm">Table: </span>
          <span className="mono">{(data as TriggerInfo).table_name}</span>
          <span className="text-muted text-sm" style={{ marginLeft: 12 }}>Type: </span>
          <span className="mono">{(data as TriggerInfo).trigger_type} {(data as TriggerInfo).event}</span>
        </div>
      )}

      {type === 'procedure' && (
        <div className="mb-16">
          <span className="text-muted text-sm">Type: </span>
          <span className="mono">{(data as ProcedureInfo).object_type}</span>
        </div>
      )}

      {/* Code tabs: Oracle / PostgreSQL */}
      <div className="tabs" style={{ marginBottom: 0 }}>
        <button className={`tab ${codeTab === 'pg' ? 'active' : ''}`} onClick={() => setCodeTab('pg')}>
          PostgreSQL (Suggestion)
        </button>
        <button className={`tab ${codeTab === 'oracle' ? 'active' : ''}`} onClick={() => setCodeTab('oracle')}>
          Oracle (Original)
        </button>
      </div>

      <pre style={{
        background: 'var(--bg-primary)',
        border: '1px solid var(--border)',
        borderRadius: '0 0 6px 6px',
        padding: 12,
        fontSize: 12,
        maxHeight: 500,
        overflow: 'auto',
        whiteSpace: 'pre-wrap',
        wordBreak: 'break-word',
        margin: 0,
      }}>
        {codeTab === 'oracle'
          ? (data.definition || '(definition not available)')
          : (pgDef || data.definition || '(definition not available)')}
      </pre>

      {/* Warnings */}
      {hasWarnings && (
        <div className="alert alert-warning" style={{ marginTop: 12 }}>
          <strong style={{ display: 'block', marginBottom: 4 }}>Manual review needed:</strong>
          <ul style={{ margin: 0, paddingLeft: 20 }}>
            {warnings!.map((w, i) => (
              <li key={i} style={{ fontSize: 13 }}>{w}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
