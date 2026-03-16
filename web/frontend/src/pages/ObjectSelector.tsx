import { useEffect, useState, useMemo, useCallback } from 'react';
import { Link, useParams, useNavigate } from 'react-router-dom';
import {
  Search, Table2, Layers, Key, ChevronDown, ChevronRight,
  CheckSquare, Square, Filter, ArrowRight, RefreshCw, Hash,
  ToggleLeft, ToggleRight, Database, CheckCircle2,
  MinusCircle, SkipForward, Plus, ArrowUpDown
} from 'lucide-react';
import {
  schema as schemaApi, projects, targetStatus as targetStatusApi,
  SchemaInfo, TableInfo, SequenceInfo, Project,
  TargetStatus, TableTargetStatus, SequenceTargetStatus
} from '../api';
import { formatRowCount, formatSize } from '../utils';

type ObjectType = 'table' | 'sequence';
type TargetMode = 'recreate' | 'truncate' | 'append' | 'skip' | 'truncate_partition';

const TARGET_MODES: { value: TargetMode; label: string; desc: string; icon: React.ReactNode }[] = [
  { value: 'recreate', label: 'Drop & Create', desc: 'Drop + recreate table, indexes, constraints', icon: <RefreshCw size={12} /> },
  { value: 'truncate', label: 'Truncate & Reload', desc: 'Keep DDL, truncate data, reload', icon: <MinusCircle size={12} /> },
  { value: 'append', label: 'Append', desc: 'Keep DDL + data, append new rows (may duplicate)', icon: <Plus size={12} /> },
  { value: 'skip', label: 'Skip', desc: 'Do not migrate this table', icon: <SkipForward size={12} /> },
];

const PARTITIONED_MODES: { value: TargetMode; label: string; desc: string; icon: React.ReactNode }[] = [
  { value: 'truncate_partition', label: 'Incremental', desc: 'Keep existing, migrate new partitions only', icon: <Layers size={12} /> },
  ...TARGET_MODES,
];

interface SelectableObject {
  type: ObjectType;
  name: string;
  table?: TableInfo;
  sequence?: SequenceInfo;
}

export function ObjectSelector() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [project, setProject] = useState<Project | null>(null);
  const [schemaData, setSchemaData] = useState<SchemaInfo | null>(null);
  const [pgStatus, setPgStatus] = useState<TargetStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const [statusLoading, setStatusLoading] = useState(false);
  const [error, setError] = useState('');
  const [search, setSearch] = useState('');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [expandedTable, setExpandedTable] = useState<string | null>(null);
  const [typeFilter, setTypeFilter] = useState<'all' | 'table' | 'partitioned' | 'sequence'>('all');
  const [saving, setSaving] = useState(false);
  const [tableModes, setTableModes] = useState<Record<string, TargetMode>>({});
  const [sortBy, setSortBy] = useState<'name' | 'rows' | 'size'>('name');
  const [partitionFilters, setPartitionFilters] = useState<Record<string, Set<string>>>({});
  const [whereFilters, setWhereFilters] = useState<Record<string, string>>({});
  const [indexModes, setIndexModes] = useState<Record<string, string>>({});

  // Load project
  useEffect(() => {
    if (!id) return;
    projects.get(id).then(p => {
      setProject(p);
      // Restore previously selected tables and sequences
      const restored = new Set<string>();
      if (p.migration_config?.include_tables?.length > 0) {
        p.migration_config.include_tables.forEach(t => restored.add(t));
      }
      if ((p.migration_config?.include_sequences?.length ?? 0) > 0) {
        p.migration_config.include_sequences!.forEach(s => restored.add(`SEQ:${s}`));
      }
      if (restored.size > 0) {
        setSelected(restored);
      }
      // Restore saved target modes, partition filters, where filters
      if (p.migration_config?.table_overrides) {
        const modes: Record<string, TargetMode> = {};
        const partFilters: Record<string, Set<string>> = {};
        const whereF: Record<string, string> = {};
        const idxModes: Record<string, string> = {};
        for (const [k, v] of Object.entries(p.migration_config.table_overrides)) {
          if (v.target_mode) modes[k] = v.target_mode as TargetMode;
          if (v.partition_filter && v.partition_filter.length > 0) {
            partFilters[k] = new Set(v.partition_filter);
          }
          if (v.where) whereF[k] = v.where;
          if (v.index_mode) idxModes[k] = v.index_mode;
        }
        setTableModes(modes);
        setPartitionFilters(partFilters);
        setWhereFilters(whereF);
        setIndexModes(idxModes);
      }
    }).catch(e => setError(e.message));
  }, [id]);

  const fetchSchema = useCallback(() => {
    if (!id) return;
    setLoading(true);
    setError('');
    schemaApi.get(id, true)
      .then(data => {
        setSchemaData(data);
        if (!project?.migration_config?.include_tables?.length) {
          const all = new Set<string>();
          data.tables.forEach(t => all.add(t.name));
          data.sequences.forEach(s => all.add(`SEQ:${s.name}`));
          setSelected(all);
        }
      })
      .catch(e => setError(e.message))
      .finally(() => setLoading(false));
  }, [id, project]);

  const fetchTargetStatus = useCallback(() => {
    if (!id) return;
    setStatusLoading(true);
    targetStatusApi.get(id, true)
      .then(data => setPgStatus(data))
      .catch(() => {}) // Silently fail — PG might not be connected
      .finally(() => setStatusLoading(false));
  }, [id]);

  useEffect(() => {
    if (project && id) {
      fetchSchema();
      fetchTargetStatus();
    }
  }, [project, id]);

  // Build lookup maps for PG status
  const pgTableMap = useMemo(() => {
    if (!pgStatus) return new Map<string, TableTargetStatus>();
    const m = new Map<string, TableTargetStatus>();
    pgStatus.tables.forEach(t => m.set(t.name, t));
    return m;
  }, [pgStatus]);

  const pgSeqMap = useMemo(() => {
    if (!pgStatus) return new Map<string, SequenceTargetStatus>();
    const m = new Map<string, SequenceTargetStatus>();
    pgStatus.sequences.forEach(s => m.set(s.name, s));
    return m;
  }, [pgStatus]);

  // Build selectable list
  const objects: SelectableObject[] = useMemo(() => {
    if (!schemaData) return [];
    const list: SelectableObject[] = [];
    schemaData.tables.forEach(t => list.push({ type: 'table', name: t.name, table: t }));
    schemaData.sequences.forEach(s => list.push({ type: 'sequence', name: s.name, sequence: s }));
    return list;
  }, [schemaData]);

  // Filter and sort
  const filtered = useMemo(() => {
    const list = objects.filter(obj => {
      const matchSearch = !search || obj.name.toLowerCase().includes(search.toLowerCase());
      if (!matchSearch) return false;
      if (typeFilter === 'all') return true;
      if (typeFilter === 'table') return obj.type === 'table' && !obj.table?.partitioning;
      if (typeFilter === 'partitioned') return obj.type === 'table' && !!obj.table?.partitioning;
      if (typeFilter === 'sequence') return obj.type === 'sequence';
      return true;
    });

    if (sortBy === 'rows') {
      list.sort((a, b) => (b.table?.num_rows ?? 0) - (a.table?.num_rows ?? 0));
    } else if (sortBy === 'size') {
      list.sort((a, b) => (b.table?.size_mb ?? 0) - (a.table?.size_mb ?? 0));
    } else {
      list.sort((a, b) => a.name.localeCompare(b.name));
    }

    return list;
  }, [objects, search, typeFilter, sortBy]);

  // Build table -> related sequences map (convention: SEQ_TABLENAME, TABLENAME_SEQ, etc.)
  const tableSeqMap = useMemo(() => {
    if (!schemaData) return new Map<string, string[]>();
    const m = new Map<string, string[]>();
    for (const t of schemaData.tables) {
      const related: string[] = [];
      const tUpper = t.name.toUpperCase();
      for (const s of schemaData.sequences) {
        const sUpper = s.name.toUpperCase();
        // Match: SEQ_TABLE, TABLE_SEQ, SEQ_TABLE_ID, TABLE_ID_SEQ etc.
        if (sUpper.includes(tUpper) || tUpper.includes(sUpper.replace(/^SEQ_/, '').replace(/_SEQ$/, ''))) {
          related.push(s.name);
        }
      }
      if (related.length > 0) m.set(t.name, related);
    }
    return m;
  }, [schemaData]);

  const toggleSelect = (key: string) => {
    setSelected(prev => {
      const next = new Set(prev);
      const isAdding = !next.has(key);
      isAdding ? next.add(key) : next.delete(key);

      // Auto-link: when selecting/deselecting a table, toggle related sequences
      if (!key.startsWith('SEQ:')) {
        const relatedSeqs = tableSeqMap.get(key) || [];
        for (const seq of relatedSeqs) {
          const seqKey = `SEQ:${seq}`;
          if (isAdding) {
            next.add(seqKey);
          } else {
            // Only remove sequence if no other selected table uses it
            const otherTableUsesIt = [...next].some(k =>
              !k.startsWith('SEQ:') && k !== key && (tableSeqMap.get(k) || []).includes(seq)
            );
            if (!otherTableUsesIt) {
              next.delete(seqKey);
            }
          }
        }
      }

      return next;
    });
  };

  const selectAll = () => {
    const next = new Set(selected);
    filtered.forEach(obj => {
      const key = obj.type === 'sequence' ? `SEQ:${obj.name}` : obj.name;
      next.add(key);
    });
    setSelected(next);
  };

  const deselectAll = () => {
    const next = new Set(selected);
    filtered.forEach(obj => {
      const key = obj.type === 'sequence' ? `SEQ:${obj.name}` : obj.name;
      next.delete(key);
    });
    setSelected(next);
  };

  const isSelected = (obj: SelectableObject) => {
    const key = obj.type === 'sequence' ? `SEQ:${obj.name}` : obj.name;
    return selected.has(key);
  };

  const setTargetMode = (tableName: string, mode: TargetMode) => {
    setTableModes(prev => ({ ...prev, [tableName]: mode }));
  };

  const getTargetMode = (tableName: string): TargetMode => {
    if (tableModes[tableName]) return tableModes[tableName];
    // Default: partitioned tables use truncate_partition (incremental-safe), others use recreate
    const table = schemaData?.tables.find(t => t.name === tableName);
    if (table?.partitioning) return 'truncate_partition';
    return 'recreate';
  };

  const selectedTables = [...selected].filter(s => !s.startsWith('SEQ:'));
  const selectedSequences = [...selected].filter(s => s.startsWith('SEQ:')).map(s => s.slice(4));
  const allFilteredSelected = filtered.every(obj => isSelected(obj));

  const totalColumns = selectedTables.reduce((sum, name) => {
    const t = schemaData?.tables.find(t => t.name === name);
    return sum + (t?.columns.length || 0);
  }, 0);

  const saveSelection = async () => {
    if (!id || !project) return;
    setSaving(true);
    try {
      // Build table overrides with target modes
      const existingOverrides = project.migration_config?.table_overrides || {};
      const overrides: Record<string, any> = { ...existingOverrides };
      for (const table of selectedTables) {
        const mode = getTargetMode(table);
        const override: any = { ...(overrides[table] || {}), target_mode: mode };
        // Save partition filter if any
        const pf = partitionFilters[table];
        if (pf && pf.size > 0) {
          override.partition_filter = [...pf];
        } else {
          delete override.partition_filter;
        }
        // Save where filter if any
        const wf = whereFilters[table];
        if (wf && wf.trim()) {
          override.where = wf.trim();
        } else {
          delete override.where;
        }
        // Save index mode if not default
        const im = indexModes[table];
        if (im && im !== 'create') {
          override.index_mode = im;
        } else {
          delete override.index_mode;
        }
        overrides[table] = override;
      }

      await projects.update(id, {
        ...project,
        migration_config: {
          ...project.migration_config,
          include_tables: selectedTables,
          include_sequences: selectedSequences,
          table_overrides: overrides,
        },
      });
      navigate(`/projects/${id}/ddl`);
    } catch (e: unknown) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  };

  const renderPGStatus = (obj: SelectableObject) => {
    if (!pgStatus) return null;

    if (obj.type === 'sequence') {
      const ss = pgSeqMap.get(obj.name);
      if (!ss) return null;
      return ss.exists ? (
        <span className="badge badge-success" style={{ fontSize: 10, padding: '1px 6px' }}>
          <CheckCircle2 size={10} /> PG
        </span>
      ) : null;
    }

    const ts = pgTableMap.get(obj.name);
    if (!ts) return null;

    if (ts.exists) {
      return (
        <span className="badge badge-success" style={{ fontSize: 10, padding: '1px 6px' }}>
          <Database size={10} /> PG: {ts.row_count.toLocaleString()}
        </span>
      );
    }

    return (
      <span className="badge badge-pending" style={{ fontSize: 10, padding: '1px 6px' }}>
        PG: New
      </span>
    );
  };

  const renderPartitionStatus = (tableName: string, partitionName: string) => {
    const ts = pgTableMap.get(tableName);
    if (!ts?.partitions) return null;
    const ps = ts.partitions.find(p => p.name === partitionName);
    if (!ps) return null;

    if (ps.exists && ps.row_count > 0) {
      return (
        <span className="badge badge-success" style={{ fontSize: 9, padding: '0 4px' }}>
          {ps.row_count.toLocaleString()}
        </span>
      );
    }
    if (ps.exists) {
      return (
        <span className="badge badge-warning" style={{ fontSize: 9, padding: '0 4px' }}>
          empty
        </span>
      );
    }
    return null;
  };

  if (!project) return <div className="loading-page"><span className="spinner" /> Loading...</div>;

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="breadcrumb"><Link to={`/projects/${id}`}>Dashboard</Link> / Select Objects</div>
          <h2>Select Migration Objects</h2>
          {schemaData && (
            <span className="text-muted text-sm">
              {schemaData.owner} — {schemaData.tables.length} tables, {schemaData.sequences.length} sequences
            </span>
          )}
        </div>
        <div className="flex gap-8">
          <button className="btn btn-secondary" onClick={() => { fetchSchema(); fetchTargetStatus(); }} disabled={loading}>
            <RefreshCw size={14} /> {loading ? 'Fetching...' : 'Refresh'}
          </button>
          <button className="btn btn-primary btn-lg" onClick={saveSelection} disabled={saving || selectedTables.length === 0}>
            {saving ? <span className="spinner" /> : <ArrowRight size={16} />}
            Continue ({selectedTables.length} tables)
          </button>
        </div>
      </div>

      {error && <div className="alert alert-error">{error}</div>}

      {loading && (
        <div className="loading-page">
          <span className="spinner" />
          <div>
            <div>Extracting Oracle metadata...</div>
            <div className="text-muted text-sm" style={{ marginTop: 4 }}>This may take a few seconds for large schemas</div>
          </div>
        </div>
      )}

      {schemaData && !loading && (
        <>
          {/* Summary bar */}
          <div className="selection-summary">
            <div className="selection-stats">
              <div className="selection-stat">
                <span className="selection-stat-value">{selectedTables.length}</span>
                <span className="selection-stat-label">Tables</span>
              </div>
              <div className="selection-stat-divider" />
              <div className="selection-stat">
                <span className="selection-stat-value">{selectedSequences.length}</span>
                <span className="selection-stat-label">Sequences</span>
              </div>
              <div className="selection-stat-divider" />
              <div className="selection-stat">
                <span className="selection-stat-value">{totalColumns}</span>
                <span className="selection-stat-label">Columns</span>
              </div>
              {pgStatus && (
                <>
                  <div className="selection-stat-divider" />
                  <div className="selection-stat">
                    <span className="selection-stat-value" style={{ color: 'var(--success)' }}>
                      {pgStatus.tables.filter(t => t.exists).length}
                    </span>
                    <span className="selection-stat-label">Exist in PG</span>
                  </div>
                  <div className="selection-stat-divider" />
                  <div className="selection-stat">
                    <span className="selection-stat-value" style={{ color: 'var(--info)' }}>
                      {pgStatus.tables.reduce((s, t) => s + t.row_count, 0).toLocaleString()}
                    </span>
                    <span className="selection-stat-label">PG Rows</span>
                  </div>
                </>
              )}
            </div>
          </div>

          {/* Toolbar */}
          <div className="selector-toolbar">
            <div className="selector-search">
              <Search size={14} />
              <input
                type="text"
                placeholder="Search tables and sequences..."
                value={search}
                onChange={e => setSearch(e.target.value)}
              />
            </div>

            <div className="selector-filters">
              <button className={`filter-chip ${typeFilter === 'all' ? 'active' : ''}`} onClick={() => setTypeFilter('all')}>
                All ({objects.length})
              </button>
              <button className={`filter-chip ${typeFilter === 'table' ? 'active' : ''}`} onClick={() => setTypeFilter('table')}>
                <Table2 size={12} /> Tables ({schemaData.tables.filter(t => !t.partitioning).length})
              </button>
              <button className={`filter-chip ${typeFilter === 'partitioned' ? 'active' : ''}`} onClick={() => setTypeFilter('partitioned')}>
                <Layers size={12} /> Partitioned ({schemaData.tables.filter(t => t.partitioning).length})
              </button>
              <button className={`filter-chip ${typeFilter === 'sequence' ? 'active' : ''}`} onClick={() => setTypeFilter('sequence')}>
                <Hash size={12} /> Sequences ({schemaData.sequences.length})
              </button>
            </div>

            <div className="selector-sort">
              <ArrowUpDown size={12} />
              <button className={`filter-chip ${sortBy === 'name' ? 'active' : ''}`} onClick={() => setSortBy('name')}>Name</button>
              <button className={`filter-chip ${sortBy === 'rows' ? 'active' : ''}`} onClick={() => setSortBy('rows')}>Rows</button>
              <button className={`filter-chip ${sortBy === 'size' ? 'active' : ''}`} onClick={() => setSortBy('size')}>Size</button>
            </div>

            <div className="selector-actions" style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <select
                className="input"
                style={{ padding: '4px 8px', fontSize: 12, width: 'auto' }}
                value=""
                onChange={e => {
                  const mode = e.target.value as TargetMode;
                  if (!mode) return;
                  const modes: Record<string, TargetMode> = {};
                  selectedTables.forEach(t => {
                    const tbl = schemaData?.tables.find(tb => tb.name === t);
                    const isPartTbl = !!tbl?.partitioning;
                    // "Incremental" only applies to partitioned tables
                    if (mode === 'truncate_partition' && !isPartTbl) {
                      modes[t] = 'recreate';
                    } else {
                      modes[t] = mode;
                    }
                  });
                  setTableModes(prev => ({ ...prev, ...modes }));
                }}
              >
                <option value="">Set All Mode...</option>
                <option value="recreate" title="Drop table, create from scratch">Drop & Create</option>
                <option value="truncate" title="Keep structure, reload all data">Clear & Reload</option>
                <option value="truncate_partition" title="Partitioned → Incremental, others → Drop & Create">Incremental (partitioned only)</option>
                <option value="append" title="Add to existing data (may duplicate)">Append</option>
              </select>
              <button className="btn btn-sm btn-secondary" onClick={allFilteredSelected ? deselectAll : selectAll}>
                {allFilteredSelected ? <ToggleRight size={14} /> : <ToggleLeft size={14} />}
                {allFilteredSelected ? 'Deselect All' : 'Select All'}
              </button>
            </div>
          </div>

          {/* Object list */}
          <div className="object-list">
            {filtered.map(obj => {
              const key = obj.type === 'sequence' ? `SEQ:${obj.name}` : obj.name;
              const checked = selected.has(key);
              const isExpanded = expandedTable === obj.name && obj.type === 'table';
              const isPartitioned = obj.type === 'table' && !!obj.table?.partitioning;
              const mode = obj.type === 'table' ? getTargetMode(obj.name) : undefined;

              return (
                <div key={key} className={`object-item ${checked ? 'selected' : ''}`}>
                  <div className="object-item-main" onClick={() => toggleSelect(key)}>
                    <div className="object-item-check">
                      {checked ? <CheckSquare size={18} className="check-on" /> : <Square size={18} className="check-off" />}
                    </div>

                    <div className="object-item-icon">
                      {obj.type === 'table' ? (
                        isPartitioned ? <Layers size={16} /> : <Table2 size={16} />
                      ) : (
                        <Hash size={16} />
                      )}
                    </div>

                    <div className="object-item-info">
                      <div className="object-item-name">
                        {obj.name}
                        {renderPGStatus(obj)}
                      </div>
                      <div className="object-item-meta">
                        {obj.type === 'table' && obj.table && (
                          <>
                            <span>{obj.table.columns.length} columns</span>
                            <span style={{ color: 'var(--info)' }}>
                              ~{formatRowCount(obj.table.num_rows)} rows
                            </span>
                            {obj.table.size_mb > 0 && (
                              <span style={{ color: 'var(--orange)' }}>
                                {formatSize(obj.table.size_mb)}
                              </span>
                            )}
                            {obj.table.primary_key && <span><Key size={10} /> PK: {obj.table.primary_key.columns.join(', ')}</span>}
                            {obj.table.constraints.length > 0 && <span>{obj.table.constraints.length} constraints</span>}
                            {obj.table.indexes.length > 0 && <span>{obj.table.indexes.length} indexes</span>}
                          </>
                        )}
                        {isPartitioned && obj.table?.partitioning && (
                          <span className="meta-partition">
                            <Layers size={10} /> {obj.table.partitioning.strategy} ({obj.table.partitioning.partitions.length} partitions)
                          </span>
                        )}
                        {obj.type === 'sequence' && obj.sequence && (
                          <>
                            <span>Last: {obj.sequence.last_value.toLocaleString()}</span>
                            <span>Inc: {obj.sequence.increment}</span>
                            {/* Show linked table */}
                            {(() => {
                              const linked = [...tableSeqMap.entries()].find(([, seqs]) => seqs.includes(obj.name));
                              return linked ? (
                                <span style={{ color: 'var(--accent)' }}>
                                  <Table2 size={10} /> {linked[0]}
                                </span>
                              ) : null;
                            })()}
                          </>
                        )}
                      </div>
                    </div>

                    {/* Mode selectors — fixed position right side */}
                    {obj.type === 'table' && checked && (
                      <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexShrink: 0, marginLeft: 'auto' }} onClick={e => e.stopPropagation()}>
                        {obj.table && obj.table.indexes.length > 0 && (
                          <select
                            value={indexModes[obj.name] || 'create'}
                            onChange={e => setIndexModes(prev => ({ ...prev, [obj.name]: e.target.value }))}
                            className="mode-select"
                            title="Index creation strategy"
                          >
                            <option value="create">Idx: Create</option>
                            <option value="concurrent">Idx: Concurrent</option>
                            <option value="skip">Idx: Skip</option>
                          </select>
                        )}
                        <select
                          value={mode}
                          onChange={e => setTargetMode(obj.name, e.target.value as TargetMode)}
                          className="mode-select"
                          title={(isPartitioned ? PARTITIONED_MODES : TARGET_MODES).find(m => m.value === mode)?.desc}
                        >
                          {(isPartitioned ? PARTITIONED_MODES : TARGET_MODES).map(m => (
                            <option key={m.value} value={m.value} title={m.desc}>{m.label}</option>
                          ))}
                        </select>
                      </div>
                    )}

                    {obj.type === 'table' && obj.table && (
                      <button
                        className="object-item-expand"
                        onClick={e => { e.stopPropagation(); setExpandedTable(isExpanded ? null : obj.name); }}
                      >
                        {isExpanded ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                      </button>
                    )}
                  </div>

                  {isExpanded && obj.table && (
                    <div className="object-item-detail">
                      <table className="data-table compact">
                        <thead>
                          <tr>
                            <th style={{ width: 40 }}>#</th>
                            <th>Column</th>
                            <th>Oracle Type</th>
                            <th><ArrowRight size={10} /> PG Type</th>
                            <th style={{ width: 70 }}>Nullable</th>
                          </tr>
                        </thead>
                        <tbody>
                          {obj.table.columns.map(c => (
                            <tr key={c.name}>
                              <td className="text-muted">{c.position}</td>
                              <td className="mono">
                                {obj.table!.primary_key?.columns.includes(c.name) && (
                                  <Key size={10} style={{ color: 'var(--warning)', marginRight: 4 }} />
                                )}
                                {c.name}
                              </td>
                              <td className="mono text-muted">{c.oracle_type}</td>
                              <td className="mono" style={{ color: 'var(--accent)' }}>{c.pg_type}</td>
                              <td>{c.nullable ? <span className="text-muted">YES</span> : <span style={{ color: 'var(--warning)' }}>NO</span>}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>

                      {obj.table.partitioning && (
                        <div style={{ marginTop: 12 }}>
                          <div className="flex-between" style={{ marginBottom: 8 }}>
                            <div className="text-sm text-muted" style={{ fontWeight: 600 }}>
                              <Layers size={11} /> Partitions — {obj.table.partitioning.strategy} by {obj.table.partitioning.key_columns.join(', ')}
                            </div>
                            <div className="flex gap-8">
                              <button
                                className="btn btn-sm btn-secondary"
                                style={{ fontSize: 10, padding: '2px 8px' }}
                                onClick={() => {
                                  setPartitionFilters(prev => {
                                    const next = { ...prev };
                                    delete next[obj.name];
                                    return next;
                                  });
                                }}
                              >
                                Select All
                              </button>
                              <button
                                className="btn btn-sm btn-secondary"
                                style={{ fontSize: 10, padding: '2px 8px' }}
                                onClick={() => {
                                  setPartitionFilters(prev => ({
                                    ...prev,
                                    [obj.name]: new Set<string>(),
                                  }));
                                }}
                              >
                                Deselect All
                              </button>
                            </div>
                          </div>
                          <table className="data-table compact" style={{ fontSize: 11 }}>
                            <thead>
                              <tr>
                                <th style={{ width: 30 }}></th>
                                <th>Partition</th>
                                <th>Range (High Value)</th>
                                <th style={{ textAlign: 'right' }}>Rows</th>
                                <th style={{ textAlign: 'right' }}>Size</th>
                                <th style={{ width: 60 }}>PG</th>
                              </tr>
                            </thead>
                            <tbody>
                              {obj.table.partitioning.partitions.map((p, idx) => {
                                const pf = partitionFilters[obj.name];
                                const isAllSelected = !pf;
                                const isPartSelected = isAllSelected || pf.has(p.name);
                                // Parse high_value to readable format
                                const hv = p.high_value || '';
                                const dateMatch = hv.match(/(\d{4}-\d{2}-\d{2})/);
                                const displayValue = dateMatch ? dateMatch[1] : hv.replace(/^TO_DATE\(.*$/, hv).slice(0, 40);
                                // Previous partition's high value = this partition's lower bound
                                const prevHV = idx > 0 ? obj.table!.partitioning!.partitions[idx - 1].high_value : '';
                                const prevDate = prevHV.match(/(\d{4}-\d{2}-\d{2})/);
                                const lowerDisplay = prevDate ? prevDate[1] : (idx === 0 ? 'MIN' : '');

                                return (
                                  <tr
                                    key={p.name}
                                    style={{ opacity: isPartSelected ? 1 : 0.4, cursor: 'pointer' }}
                                    onClick={() => {
                                      setPartitionFilters(prev => {
                                        const allParts = obj.table!.partitioning!.partitions.map(pp => pp.name);
                                        let current = prev[obj.name];
                                        if (!current) {
                                          current = new Set(allParts.filter(n => n !== p.name));
                                        } else if (current.has(p.name)) {
                                          current = new Set(current);
                                          current.delete(p.name);
                                        } else {
                                          current = new Set(current);
                                          current.add(p.name);
                                        }
                                        if (current.size === allParts.length) {
                                          const next = { ...prev };
                                          delete next[obj.name];
                                          return next;
                                        }
                                        return { ...prev, [obj.name]: current };
                                      });
                                    }}
                                  >
                                    <td>
                                      {isPartSelected
                                        ? <CheckSquare size={13} className="check-on" />
                                        : <Square size={13} className="check-off" />}
                                    </td>
                                    <td className="mono">{p.name}</td>
                                    <td className="mono text-muted">
                                      {lowerDisplay && displayValue
                                        ? `${lowerDisplay} → ${displayValue}`
                                        : displayValue || '-'}
                                    </td>
                                    <td style={{ textAlign: 'right' }}>
                                      {p.num_rows > 0 ? formatRowCount(p.num_rows) : '-'}
                                    </td>
                                    <td style={{ textAlign: 'right' }} className="text-muted">
                                      {p.size_mb > 0 ? formatSize(p.size_mb) : '-'}
                                    </td>
                                    <td>{renderPartitionStatus(obj.name, p.name)}</td>
                                  </tr>
                                );
                              })}
                            </tbody>
                          </table>
                          {partitionFilters[obj.name] && (
                            <div className="text-sm" style={{ marginTop: 6, color: 'var(--info)' }}>
                              {partitionFilters[obj.name].size} / {obj.table.partitioning.partitions.length} partitions selected
                              {' '}— ~{obj.table.partitioning.partitions
                                .filter(p => partitionFilters[obj.name].has(p.name))
                                .reduce((s, p) => s + p.num_rows, 0)
                                .toLocaleString()} rows
                            </div>
                          )}
                        </div>
                      )}

                      {/* WHERE filter */}
                      {obj.type === 'table' && checked && (
                        <div style={{ marginTop: 12 }}>
                          <div className="text-sm text-muted" style={{ marginBottom: 4, fontWeight: 600 }}>
                            <Filter size={10} /> WHERE Filter (optional)
                          </div>
                          <input
                            type="text"
                            className="input"
                            placeholder="e.g. status = 'ACTIVE' AND created_at > DATE '2024-01-01'"
                            value={whereFilters[obj.name] || ''}
                            onChange={e => setWhereFilters(prev => ({ ...prev, [obj.name]: e.target.value }))}
                            style={{ fontSize: 12, fontFamily: 'var(--font-mono)', width: '100%' }}
                            onClick={e => e.stopPropagation()}
                          />
                        </div>
                      )}

                      {obj.table.constraints.length > 0 && (
                        <div style={{ marginTop: 12 }}>
                          <div className="text-sm text-muted" style={{ marginBottom: 6, fontWeight: 600 }}>Constraints</div>
                          <div className="flex gap-8" style={{ flexWrap: 'wrap' }}>
                            {obj.table.constraints.map(c => (
                              <span key={c.name} className={`badge ${c.type === 'R' ? 'badge-info' : c.type === 'U' ? 'badge-success' : 'badge-pending'}`}>
                                {c.type === 'R' ? 'FK' : c.type === 'U' ? 'UNIQUE' : 'CHECK'} {c.name}
                              </span>
                            ))}
                          </div>
                        </div>
                      )}
                    </div>
                  )}
                </div>
              );
            })}

            {filtered.length === 0 && (
              <div className="empty-state" style={{ padding: 40 }}>
                <Filter size={32} />
                <h3>No matching objects</h3>
                <p>Try adjusting your search or filter.</p>
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}
