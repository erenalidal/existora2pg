import { useEffect, useState, useCallback, useRef, useMemo } from 'react';
import { Link, useParams } from 'react-router-dom';
import { Play, Square, RotateCcw, RefreshCw, Loader, Trash2, Clock, Database, BarChart3, ChevronRight, ChevronDown } from 'lucide-react';
import { migration, streamProgress, Job, RunStatus, ProgressEvent } from '../api';

export function MigrationRun() {
  const { id } = useParams<{ id: string }>();
  const [status, setStatus] = useState<RunStatus | null>(null);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [progress, setProgress] = useState<Map<string, ProgressEvent>>(new Map());
  const [running, setRunning] = useState(false);
  const [starting, setStarting] = useState(false);
  const [error, setError] = useState('');
  const [tab, setTab] = useState<'all' | 'running' | 'failed' | 'completed'>('all');
  const [pipelineStats, setPipelineStats] = useState<Map<string, ProgressEvent>>(new Map());
  const [initialLoading, setInitialLoading] = useState(true);
  const [delayedResumeTimer, setDelayedResumeTimer] = useState<number | null>(null);
  const [delayedResumeCountdown, setDelayedResumeCountdown] = useState(0);
  const unsubRef = useRef<(() => void) | null>(null);

  const loadStatusRef = useRef<() => void>(() => {});

  const loadStatus = useCallback(() => {
    if (!id) return;
    migration.status(id).then(s => { setStatus(s); setInitialLoading(false); }).catch(() => setInitialLoading(false));
    migration.jobs(id).then(r => {
      const apiJobs = r.jobs || [];
      setJobs(prev => {
        if (!prev.length) return apiJobs;
        // Merge: keep higher rows_copied from SSE-updated state
        return apiJobs.map(aj => {
          const existing = prev.find(pj => pj.id === aj.id);
          if (existing && existing.rows_copied > aj.rows_copied && aj.state === 'RUNNING') {
            return { ...aj, rows_copied: existing.rows_copied };
          }
          return aj;
        });
      });
      // Load persisted pipeline stats from API
      if (r.pipeline_stats && r.pipeline_stats.length > 0) {
        setPipelineStats(prev => {
          const next = new Map(prev);
          for (const st of r.pipeline_stats!) {
            const key = st.table + (st.partition ? ':' + st.partition : '');
            next.set(key, st);
          }
          return next;
        });
      }
    }).catch(() => null);
  }, [id]);

  loadStatusRef.current = loadStatus;

  // Buffer SSE events and flush to React state at most every 500ms.
  // Without this, rapid events (e.g. 800 COPY batches) cause 1600+ re-renders and freeze the browser.
  const pendingEventsRef = useRef<Map<string, ProgressEvent>>(new Map());
  const flushTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const flushEvents = useCallback(() => {
    flushTimerRef.current = null;
    const pending = pendingEventsRef.current;
    if (pending.size === 0) return;
    const events = new Map(pending);
    pending.clear();

    // Batch-apply all buffered events in one render cycle
    setProgress(prev => {
      const next = new Map(prev);
      for (const [k, v] of events) next.set(k, v);
      return next;
    });
    setJobs(prev => prev.map(j => {
      const jobPartition = j.partition || '';
      for (const [, event] of events) {
        const evtPartition = event.partition || '';
        if (j.table_name === event.table && jobPartition === evtPartition && j.phase === 'data' && event.state !== 'STATS') {
          return { ...j, rows_copied: event.rows, state: event.state || j.state };
        }
      }
      return j;
    }));
    // Reload full status for terminal events
    if ([...events.values()].some(e => e.state === 'COMPLETED' || e.state === 'FAILED')) {
      loadStatusRef.current();
    }
  }, []);

  const subscribeProgress = useCallback(() => {
    if (!id) return;
    unsubRef.current?.();
    unsubRef.current = streamProgress(
      id,
      (event) => {
        const key = event.table + (event.partition ? ':' + event.partition : '');
        if (event.state === 'STATS') {
          setPipelineStats(prev => {
            const next = new Map(prev);
            next.set(key, event);
            return next;
          });
          return;
        }
        // Buffer event; flush on timer (2s to match backend throttle)
        if (event.state === 'COMPLETED' || event.state === 'FAILED') {
          // Terminal events: remove from live progress immediately, trigger status reload
          pendingEventsRef.current.delete(key);
          setProgress(prev => {
            if (!prev.has(key)) return prev;
            const next = new Map(prev);
            next.delete(key);
            return next;
          });
          loadStatusRef.current();
        } else {
          pendingEventsRef.current.set(key, event);
        }
        if (!flushTimerRef.current) {
          flushTimerRef.current = setTimeout(flushEvents, 500);
        }
      },
      () => {
        // Flush any remaining events before marking done
        if (flushTimerRef.current) clearTimeout(flushTimerRef.current);
        flushEvents();
        setRunning(false);
        loadStatusRef.current();
      },
      (msg) => {
        setError(msg);
        setRunning(false);
        loadStatusRef.current();
      }
    );
  }, [id, flushEvents]);

  useEffect(() => {
    loadStatus();
    const interval = setInterval(loadStatus, 3000);

    // RDP/tab switch fix: force re-render when page becomes visible again
    const onVisibility = () => {
      if (document.visibilityState === 'visible') {
        loadStatus();
        // Re-subscribe SSE if was running (RDP disconnect kills connections)
        if (running && !unsubRef.current) {
          subscribeProgress();
        }
      }
    };
    document.addEventListener('visibilitychange', onVisibility);

    return () => {
      clearInterval(interval);
      document.removeEventListener('visibilitychange', onVisibility);
    };
  }, [loadStatus, running, subscribeProgress]);

  // Cleanup SSE + flush timer only on unmount
  useEffect(() => {
    return () => {
      unsubRef.current?.();
      if (flushTimerRef.current) clearTimeout(flushTimerRef.current);
    };
  }, []);

  // Auto-detect running migration on page load/revisit and subscribe to SSE
  const autoSubscribedRef = useRef(false);
  useEffect(() => {
    if (!id || autoSubscribedRef.current) return;
    const hasRunning = jobs.some(j => j.state === 'RUNNING');
    const hasActivePhase = status?.phase && status.phase.phase !== 'completed' && status.phase.phase !== 'failed';
    if ((hasRunning || hasActivePhase) && !unsubRef.current) {
      autoSubscribedRef.current = true;
      setRunning(true);
      subscribeProgress();
    }
  }, [jobs, id, status, subscribeProgress]);

  const startMigration = async () => {
    if (!id) return;
    setError('');
    setStarting(true);
    setRunning(true);
    try {
      await migration.start(id);
      subscribeProgress();
      loadStatus();
    } catch (e: unknown) {
      setError((e as Error).message);
      setRunning(false);
    } finally {
      setStarting(false);
    }
  };

  const resumeMigration = async () => {
    if (!id) return;
    setError('');
    setRunning(true);
    try {
      await migration.resume(id);
      subscribeProgress();
      loadStatus();
    } catch (e: unknown) {
      setError((e as Error).message);
      setRunning(false);
    }
  };

  const delayedResume = () => {
    const minutes = prompt('Kaç dakika sonra resume?', '5');
    if (!minutes || isNaN(Number(minutes))) return;
    const ms = parseInt(minutes) * 60000;
    setDelayedResumeCountdown(parseInt(minutes) * 60);
    const countdown = setInterval(() => {
      setDelayedResumeCountdown(prev => {
        if (prev <= 1) {
          clearInterval(countdown);
          return 0;
        }
        return prev - 1;
      });
    }, 1000);
    const timer = window.setTimeout(() => {
      clearInterval(countdown);
      setDelayedResumeTimer(null);
      setDelayedResumeCountdown(0);
      resumeMigration();
    }, ms);
    setDelayedResumeTimer(timer);
  };

  const cancelDelayedResume = () => {
    if (delayedResumeTimer) {
      clearTimeout(delayedResumeTimer);
      setDelayedResumeTimer(null);
      setDelayedResumeCountdown(0);
    }
  };

  const cancelMigration = async () => {
    if (!id) return;
    try {
      await migration.cancel(id);
      unsubRef.current?.();
      unsubRef.current = null;
      setRunning(false);
      autoSubscribedRef.current = false;
      loadStatus();
    } catch (e: unknown) {
      setError((e as Error).message);
    }
  };

  const resetMigration = async () => {
    if (!id) return;
    if (!confirm('This will drop all target tables in PostgreSQL and clear job history. Are you sure?')) return;
    setError('');
    try {
      // Stop SSE and clear all state
      unsubRef.current?.();
      unsubRef.current = null;
      await migration.reset(id);
      setStatus(null);
      setJobs([]);
      setProgress(new Map());
      setPipelineStats(new Map());
      setRunning(false);
      autoSubscribedRef.current = false;
      loadStatus();
    } catch (e: unknown) {
      setError((e as Error).message);
    }
  };

  // Expanded sets — default is collapsed (not in set = collapsed)
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [liveExpanded, setLiveExpanded] = useState<Set<string>>(new Set());
  const [statsExpanded, setStatsExpanded] = useState<Set<string>>(new Set());

  const toggleTable = (tableName: string) => {
    setExpanded(prev => {
      const next = new Set(prev);
      if (next.has(tableName)) next.delete(tableName);
      else next.add(tableName);
      return next;
    });
  };

  const toggleLiveTable = (tableName: string) => {
    setLiveExpanded(prev => {
      const next = new Set(prev);
      if (next.has(tableName)) next.delete(tableName);
      else next.add(tableName);
      return next;
    });
  };

  // Group jobs by table, compute aggregate stats per table
  interface TableGroup {
    tableName: string;
    jobs: Job[];
    phases: string[];
    overallState: string;
    totalRowsExpected: number;
    totalRowsCopied: number;
    dataJobs: Job[];
    hasChildren: boolean; // true if multiple data jobs (partitions/chunks)
  }

  const filteredJobs = jobs.filter(j => {
    if (tab === 'all') return true;
    if (tab === 'running') return j.state === 'RUNNING';
    if (tab === 'failed') return j.state === 'FAILED';
    if (tab === 'completed') return j.state === 'COMPLETED' || j.state === 'SKIPPED';
    return true;
  });

  const tableGroups = useMemo((): TableGroup[] => {
    const map = new Map<string, Job[]>();
    for (const j of filteredJobs) {
      const list = map.get(j.table_name) || [];
      list.push(j);
      map.set(j.table_name, list);
    }
    const groups: TableGroup[] = [];
    for (const [tableName, tableJobs] of map) {
      const dataJobs = tableJobs.filter(j => j.phase === 'data');
      const phases = [...new Set(tableJobs.map(j => j.phase))];
      // Overall state: FAILED > RUNNING > PENDING > SKIPPED > COMPLETED
      let overallState = 'COMPLETED';
      const allSkipped = tableJobs.every(j => j.state === 'SKIPPED');
      if (tableJobs.some(j => j.state === 'FAILED')) overallState = 'FAILED';
      else if (tableJobs.some(j => j.state === 'RUNNING')) overallState = 'RUNNING';
      else if (tableJobs.some(j => j.state === 'PENDING' || j.state === 'RETRYING')) overallState = 'PENDING';
      else if (allSkipped) overallState = 'SKIPPED';
      // Sort partitions by rows_expected DESC, then partition name for stability
      dataJobs.sort((a, b) => b.rows_expected - a.rows_expected || (a.partition || '').localeCompare(b.partition || ''));
      groups.push({
        tableName,
        jobs: tableJobs,
        phases,
        overallState,
        totalRowsExpected: dataJobs.reduce((s, j) => s + j.rows_expected, 0),
        totalRowsCopied: dataJobs.reduce((s, j) => s + j.rows_copied, 0),
        dataJobs,
        hasChildren: dataJobs.length > 1,
      });
    }
    // Stable sort: rows DESC, then table name ASC as tiebreaker
    return groups.sort((a, b) => b.totalRowsExpected - a.totalRowsExpected || a.tableName.localeCompare(b.tableName));
  }, [filteredJobs]);

  const s = status?.summary;
  const hasFailed = s && s.failed > 0;
  const isActive = running || !!(status?.phase && status.phase.phase !== 'completed' && status.phase.phase !== 'failed');
  const progressPercent = s && s.total > 0 ? Math.round(((s.completed + s.skipped) / s.total) * 100) : 0;

  const formatDuration = (startStr?: string, endStr?: string) => {
    if (!startStr) return null;
    const start = new Date(startStr);
    const end = endStr ? new Date(endStr) : new Date();
    const diffMs = end.getTime() - start.getTime();
    const secs = Math.floor(diffMs / 1000);
    if (secs < 60) return `${secs}s`;
    const mins = Math.floor(secs / 60);
    const remSecs = secs % 60;
    if (mins < 60) return `${mins}m ${remSecs}s`;
    const hours = Math.floor(mins / 60);
    const remMins = mins % 60;
    return `${hours}h ${remMins}m`;
  };

  const formatTime = (iso?: string) => {
    if (!iso) return '-';
    return new Date(iso).toLocaleTimeString();
  };

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="breadcrumb"><Link to={`/projects/${id}`}>Dashboard</Link> / Migration</div>
          <h2>Migration</h2>
        </div>
        <div className="flex gap-8">
          {!isActive && (
            <>
              <button className="btn btn-primary" onClick={startMigration} disabled={starting}>
                {starting ? <><Loader size={14} className="spin" /> Starting...</> : <><Play size={14} /> Start Migration</>}
              </button>
              {hasFailed && (
                <>
                  <button className="btn btn-secondary" onClick={resumeMigration}>
                    <RotateCcw size={14} /> Resume Failed
                  </button>
                  {delayedResumeTimer ? (
                    <button className="btn btn-secondary" onClick={cancelDelayedResume}>
                      <Clock size={14} /> Cancel ({Math.floor(delayedResumeCountdown / 60)}:{String(delayedResumeCountdown % 60).padStart(2, '0')})
                    </button>
                  ) : (
                    <button className="btn btn-secondary" onClick={delayedResume}>
                      <Clock size={14} /> Delayed Resume
                    </button>
                  )}
                </>
              )}
              {s && s.total > 0 && (
                <button className="btn btn-danger" onClick={resetMigration}>
                  <Trash2 size={14} /> Reset
                </button>
              )}
            </>
          )}
          {isActive && (
            <button className="btn btn-danger" onClick={cancelMigration}>
              <Square size={14} /> Cancel
            </button>
          )}
          <button className="btn btn-secondary" onClick={loadStatus}>
            <RefreshCw size={14} />
          </button>
        </div>
      </div>

      {error && <div className="alert alert-error">{error}</div>}
      {!error && status?.phase?.phase === 'failed' && (
        <div className="alert alert-error">Migration failed: {status.phase.detail}</div>
      )}

      {/* Summary */}
      {status && s && s.total > 0 && (
        <div className="card mb-16">
          <div className="flex-between mb-16">
            <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
              <span className="text-sm text-muted">Run: {status.run_id?.slice(0, 8)}</span>
              {status.phase && status.phase.phase !== 'completed' && (
                <span className="badge badge-info" style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
                  <Loader size={10} className="spin" />
                  {status.phase.detail}
                </span>
              )}
            </div>
            <span className="text-sm" style={{ fontWeight: 600 }}>{progressPercent}%</span>
          </div>
          <div className="progress-bar mb-16">
            <div
              className={`progress-bar-fill ${s.failed > 0 ? 'error' : progressPercent === 100 ? 'success' : ''}`}
              style={{ width: `${progressPercent}%` }}
            />
          </div>
          <div className="card-grid card-grid-4">
            <div className="stat">
              <div className="stat-value">{s.total}</div>
              <div className="stat-label">Total Jobs</div>
            </div>
            <div className="stat">
              <div className="stat-value" style={{ color: 'var(--success)' }}>{s.completed + s.skipped}</div>
              <div className="stat-label">Completed</div>
            </div>
            <div className="stat">
              <div className="stat-value" style={{ color: 'var(--accent)' }}>{s.running}</div>
              <div className="stat-label">Running</div>
            </div>
            <div className="stat">
              <div className="stat-value" style={{ color: s.failed > 0 ? 'var(--error)' : undefined }}>{s.failed}</div>
              <div className="stat-label">Failed</div>
            </div>
          </div>
          {/* Timing & stats row */}
          <div style={{ display: 'flex', gap: 24, marginTop: 12, paddingTop: 12, borderTop: '1px solid var(--border)', flexWrap: 'wrap' }}>
            {s.started_at && (
              <span className="text-sm text-muted"><Clock size={12} style={{ marginRight: 4, verticalAlign: -2 }} />Started: {formatTime(s.started_at)}</span>
            )}
            {s.finished_at && s.running === 0 && (
              <span className="text-sm text-muted"><Clock size={12} style={{ marginRight: 4, verticalAlign: -2 }} />Finished: {formatTime(s.finished_at)}</span>
            )}
            {s.started_at && (
              <span className="text-sm" style={{ fontWeight: 500 }}>
                <Clock size={12} style={{ marginRight: 4, verticalAlign: -2 }} />
                Duration: {formatDuration(s.started_at, s.running === 0 ? s.finished_at : undefined)}
                {s.running > 0 && ' (running...)'}
              </span>
            )}
            {s.total_rows > 0 && (
              <span className="text-sm text-muted"><Database size={12} style={{ marginRight: 4, verticalAlign: -2 }} />Rows: {s.total_rows.toLocaleString()}</span>
            )}
            {s.total_rows > 0 && s.started_at && (
              <span className="text-sm text-muted">
                Avg: {(() => {
                  const start = new Date(s.started_at);
                  const end = s.finished_at && s.running === 0 ? new Date(s.finished_at) : new Date();
                  const secs = (end.getTime() - start.getTime()) / 1000;
                  return secs > 0 ? `${Math.round(s.total_rows / secs).toLocaleString()} rows/s` : '-';
                })()}
              </span>
            )}
          </div>
        </div>
      )}

      {/* Live progress — grouped by table */}
      {progress.size > 0 && (() => {
        const runningEvents = [...progress.values()].filter(p => p.state === 'RUNNING');
        if (runningEvents.length === 0) return null;
        // Group by table
        const tableMap = new Map<string, ProgressEvent[]>();
        for (const p of runningEvents) {
          const list = tableMap.get(p.table) || [];
          list.push(p);
          tableMap.set(p.table, list);
        }
        return (
        <div className="card mb-16">
          <div className="card-header"><h3>Live Progress</h3></div>
          <table className="data-table">
            <thead><tr><th style={{ width: 28 }}></th><th>Table</th><th>Detail</th><th>Rows</th><th>Speed</th><th>Progress</th></tr></thead>
            <tbody>
              {[...tableMap.entries()].map(([table, events]) => {
                const totalRows = events.reduce((s, e) => s + e.rows, 0);
                const totalSpeed = events.reduce((s, e) => s + e.speed, 0);
                const avgPct = events.length > 0 ? events.reduce((s, e) => s + e.percent, 0) / events.length : 0;
                const hasChildren = events.length > 1;
                const isOpen = liveExpanded.has(table);
                return (
                <>{/* Table aggregate row */}
                <tr key={table} style={{ background: 'var(--bg-secondary)', cursor: hasChildren ? 'pointer' : undefined }} onClick={() => hasChildren && toggleLiveTable(table)}>
                  <td style={{ width: 28, textAlign: 'center', padding: '6px 4px' }}>
                    {hasChildren && (isOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />)}
                  </td>
                  <td className="mono" style={{ fontWeight: 600 }}>{table}</td>
                  <td className="text-muted">{hasChildren ? `${events.length} active` : events[0]?.partition || '-'}</td>
                  <td>{totalRows.toLocaleString()}</td>
                  <td>{totalSpeed > 0 ? `${(totalSpeed / 1000).toFixed(1)}k/s` : '-'}</td>
                  <td style={{ width: 120 }}>
                    <div className="progress-bar">
                      <div className="progress-bar-fill" style={{ width: `${avgPct}%` }} />
                    </div>
                  </td>
                </tr>
                {/* Individual partitions — collapsible */}
                {hasChildren && isOpen && [...events].sort((a, b) => b.rows - a.rows || (a.partition || '').localeCompare(b.partition || '')).map(p => {
                  const key = p.table + ':' + (p.partition || '');
                  return (
                  <tr key={key} style={{ fontSize: 12 }}>
                    <td></td>
                    <td></td>
                    <td className="mono text-muted" style={{ paddingLeft: 8 }}>{p.partition || '-'}</td>
                    <td>{p.rows.toLocaleString()}</td>
                    <td>{p.speed > 0 ? `${(p.speed / 1000).toFixed(1)}k/s` : '-'}</td>
                    <td style={{ width: 120 }}>
                      <div className="progress-bar">
                        <div className="progress-bar-fill" style={{ width: `${p.percent}%` }} />
                      </div>
                    </td>
                  </tr>
                  );
                })}
                </>
                );
              })}
            </tbody>
          </table>
        </div>
        );
      })()}

      {/* Pipeline Analytics — grouped by table, collapsible */}
      {pipelineStats.size > 0 && (() => {
        const fmtMs = (ms?: number) => {
          if (!ms) return '-';
          if (ms < 1000) return `${ms}ms`;
          if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
          return `${Math.floor(ms / 60000)}m ${Math.round((ms % 60000) / 1000)}s`;
        };
        // Group stats by table
        const statsTableMap = new Map<string, ProgressEvent[]>();
        for (const st of pipelineStats.values()) {
          const list = statsTableMap.get(st.table) || [];
          list.push(st);
          statsTableMap.set(st.table, list);
        }
        const toggleStats = (t: string) => setStatsExpanded(prev => { const n = new Set(prev); n.has(t) ? n.delete(t) : n.add(t); return n; });

        return (
        <div className="card mb-16">
          <div className="card-header"><h3><BarChart3 size={16} style={{ marginRight: 6, verticalAlign: -3 }} />Pipeline Analytics</h3></div>
          <table className="data-table">
            <thead>
              <tr>
                <th style={{ width: 28 }}></th>
                <th>Table</th>
                <th>Detail</th>
                <th>Rows</th>
                <th>Duration</th>
                <th>Speed</th>
                <th>Read</th>
                <th>Write</th>
                <th>Batches</th>
                <th>Bottleneck</th>
              </tr>
            </thead>
            <tbody>
              {[...statsTableMap.entries()].sort((a, b) => {
                const rowsA = a[1].reduce((s, e) => s + e.rows, 0);
                const rowsB = b[1].reduce((s, e) => s + e.rows, 0);
                return rowsB - rowsA;
              }).map(([table, entries]) => {
                const hasChildren = entries.length > 1;
                const isOpen = statsExpanded.has(table);
                // Aggregate for parent row
                const aggRows = entries.reduce((s, e) => s + e.rows, 0);
                const aggDur = entries.reduce((s, e) => s + (e.duration_ms || 0), 0);
                const aggRead = entries.reduce((s, e) => s + (e.read_time_ms || 0), 0);
                const aggWrite = entries.reduce((s, e) => s + (e.write_time_ms || 0), 0);
                const aggBatches = entries.reduce((s, e) => s + (e.batches || 0), 0);
                const aggReadPct = aggDur > 0 ? Math.round(aggRead / aggDur * 100) : 0;
                const aggWritePct = aggDur > 0 ? Math.round(aggWrite / aggDur * 100) : 0;
                const aggSpeed = aggDur > 0 ? Math.round(aggRows * 1000 / aggDur) : 0;
                const aggAcquireWait = entries.reduce((s, e) => s + (e.acquire_wait_ms || 0), 0);
                const aggCommitWait = entries.reduce((s, e) => s + (e.commit_wait_ms || 0), 0);
                const aggAvgBatch = aggBatches > 0 ? Math.round(aggWrite / aggBatches) : 0;
                const poolContention = aggDur > 0 && aggAcquireWait > aggDur * 0.1;
                const bottleneck = aggReadPct > aggWritePct ? 'Oracle Read' : 'PG Write';
                const bottleneckColor = bottleneck === 'Oracle Read' ? '#e67e22' : '#3498db';
                return (
                <>{/* Table aggregate row */}
                <tr key={table} style={{ background: 'var(--bg-secondary)', cursor: hasChildren ? 'pointer' : undefined }} onClick={() => hasChildren && toggleStats(table)}>
                  <td style={{ width: 28, textAlign: 'center', padding: '6px 4px' }}>
                    {hasChildren && (isOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />)}
                  </td>
                  <td className="mono" style={{ fontWeight: 600 }}>{table}</td>
                  <td className="text-muted">{hasChildren ? `${entries.length} tasks` : entries[0]?.partition || '-'}</td>
                  <td>{aggRows.toLocaleString()}</td>
                  <td>{fmtMs(aggDur)}</td>
                  <td>{aggSpeed > 0 ? `${(aggSpeed / 1000).toFixed(1)}k/s` : '-'}</td>
                  <td>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                      <div style={{ width: 50, height: 6, background: 'var(--border)', borderRadius: 3, overflow: 'hidden' }}>
                        <div style={{ width: `${aggReadPct}%`, height: '100%', background: '#e67e22', borderRadius: 3 }} />
                      </div>
                      <span className="text-sm">{aggReadPct}%</span>
                    </div>
                  </td>
                  <td>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                      <div style={{ width: 50, height: 6, background: 'var(--border)', borderRadius: 3, overflow: 'hidden' }}>
                        <div style={{ width: `${aggWritePct}%`, height: '100%', background: '#3498db', borderRadius: 3 }} />
                      </div>
                      <span className="text-sm">{aggWritePct}%</span>
                    </div>
                  </td>
                  <td className="text-sm">{aggBatches || '-'}</td>
                  <td>
                    <span className="badge" style={{ background: bottleneckColor, color: '#fff', fontSize: 11 }}>
                      {bottleneck}
                    </span>
                    {bottleneck === 'PG Write' && aggBatches > 0 && (
                      <span className="text-sm text-muted" style={{ marginLeft: 4, fontSize: 10 }}>
                        batch:{fmtMs(aggAvgBatch)} commit:{fmtMs(aggCommitWait)} pool:{fmtMs(aggAcquireWait)}
                      </span>
                    )}
                    {poolContention && (
                      <span style={{ marginLeft: 4, fontSize: 10, color: '#e74c3c' }}>⚠ Pool contention</span>
                    )}
                  </td>
                </tr>
                {/* Child rows — individual partitions */}
                {hasChildren && isOpen && [...entries].sort((a, b) => b.rows - a.rows || (a.partition || '').localeCompare(b.partition || '')).map(st => {
                  const key = st.table + ':' + (st.partition || '');
                  const rdPct = st.read_pct || 0;
                  const wrPct = st.write_pct || 0;
                  const bn = rdPct > wrPct ? 'Oracle Read' : 'PG Write';
                  return (
                  <tr key={key} style={{ fontSize: 12 }}>
                    <td></td>
                    <td></td>
                    <td className="mono text-muted" style={{ paddingLeft: 8 }}>{st.partition || '-'}</td>
                    <td>{st.rows.toLocaleString()}</td>
                    <td>{fmtMs(st.duration_ms)}</td>
                    <td>{st.speed > 0 ? `${(st.speed / 1000).toFixed(1)}k/s` : '-'}</td>
                    <td>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                        <div style={{ width: 50, height: 6, background: 'var(--border)', borderRadius: 3, overflow: 'hidden' }}>
                          <div style={{ width: `${rdPct}%`, height: '100%', background: '#e67e22', borderRadius: 3 }} />
                        </div>
                        <span className="text-sm">{rdPct}%</span>
                      </div>
                    </td>
                    <td>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                        <div style={{ width: 50, height: 6, background: 'var(--border)', borderRadius: 3, overflow: 'hidden' }}>
                          <div style={{ width: `${wrPct}%`, height: '100%', background: '#3498db', borderRadius: 3 }} />
                        </div>
                        <span className="text-sm">{wrPct}%</span>
                      </div>
                    </td>
                    <td className="text-sm">{st.batches || '-'}</td>
                    <td>
                      <span className="badge" style={{ background: bn === 'Oracle Read' ? '#e67e22' : '#3498db', color: '#fff', fontSize: 11 }}>{bn}</span>
                    </td>
                  </tr>
                  );
                })}
                </>
                );
              })}
            </tbody>
          </table>
          {/* Aggregate summary */}
          {statsTableMap.size > 1 && (() => {
            const all = [...pipelineStats.values()];
            const totalRows = all.reduce((s, p) => s + p.rows, 0);
            const totalDur = all.reduce((s, p) => s + (p.duration_ms || 0), 0);
            const totalRead = all.reduce((s, p) => s + (p.read_time_ms || 0), 0);
            const totalWrite = all.reduce((s, p) => s + (p.write_time_ms || 0), 0);
            const avgReadPct = totalDur > 0 ? Math.round(totalRead / totalDur * 100) : 0;
            const avgWritePct = totalDur > 0 ? Math.round(totalWrite / totalDur * 100) : 0;
            return (
              <div style={{ display: 'flex', gap: 24, padding: '12px 0 0', borderTop: '1px solid var(--border)', marginTop: 8, flexWrap: 'wrap' }}>
                <span className="text-sm"><strong>Aggregate:</strong> {statsTableMap.size} tables</span>
                <span className="text-sm">Total rows: {totalRows.toLocaleString()}</span>
                <span className="text-sm" style={{ color: '#e67e22' }}>Avg read: {avgReadPct}%</span>
                <span className="text-sm" style={{ color: '#3498db' }}>Avg write: {avgWritePct}%</span>
                <span className="text-sm" style={{ fontWeight: 600 }}>
                  Primary bottleneck: {avgReadPct > avgWritePct ? 'Oracle Read' : 'PG Write'}
                </span>
              </div>
            );
          })()}
        </div>
        );
      })()}

      {/* Jobs table — grouped by table */}
      {jobs.length > 0 && (
        <div className="card">
          <div className="tabs">
            <button className={`tab ${tab === 'all' ? 'active' : ''}`} onClick={() => setTab('all')}>All ({jobs.length})</button>
            <button className={`tab ${tab === 'running' ? 'active' : ''}`} onClick={() => setTab('running')}>
              Running ({jobs.filter(j => j.state === 'RUNNING').length})
            </button>
            <button className={`tab ${tab === 'completed' ? 'active' : ''}`} onClick={() => setTab('completed')}>
              Completed ({jobs.filter(j => j.state === 'COMPLETED' || j.state === 'SKIPPED').length})
            </button>
            <button className={`tab ${tab === 'failed' ? 'active' : ''}`} onClick={() => setTab('failed')}>
              Failed ({jobs.filter(j => j.state === 'FAILED').length})
            </button>
          </div>
          <table className="data-table">
            <thead><tr><th style={{ width: 28 }}></th><th>Table</th><th>Phase</th><th>State</th><th>Progress</th><th>Started</th><th>Duration</th><th>Error</th></tr></thead>
            <tbody>
              {tableGroups.map(g => {
                const isExpanded = expanded.has(g.tableName);
                const groupPct = g.totalRowsExpected > 0 ? Math.round((g.totalRowsCopied / g.totalRowsExpected) * 100) : 0;
                const failedJobs = g.jobs.filter(j => j.state === 'FAILED');
                const firstErr = failedJobs[0]?.error || '';
                const earliest = g.dataJobs.filter(j => j.started_at).sort((a, b) => (a.started_at! > b.started_at! ? 1 : -1))[0];
                const latest = g.dataJobs.filter(j => j.finished_at).sort((a, b) => (a.finished_at! < b.finished_at! ? 1 : -1))[0];
                return (
                  <>{/* ── Parent row: table-level aggregate ── */}
                  <tr key={g.tableName} style={{ background: 'var(--bg-secondary)', cursor: g.hasChildren ? 'pointer' : undefined }} onClick={() => g.hasChildren && toggleTable(g.tableName)}>
                    <td style={{ width: 28, textAlign: 'center', padding: '6px 4px' }}>
                      {g.hasChildren && (isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />)}
                    </td>
                    <td className="mono" style={{ fontWeight: 600 }}>
                      {g.tableName}
                      {g.hasChildren && <span className="text-sm text-muted" style={{ marginLeft: 6, fontWeight: 400 }}>({g.dataJobs.length} partitions)</span>}
                    </td>
                    <td>{g.phases.map(p => <span key={p} className="badge badge-pending" style={{ marginRight: 3 }}>{p}</span>)}</td>
                    <td>
                      <span className={`badge badge-${g.overallState === 'COMPLETED' ? 'success' : g.overallState === 'SKIPPED' ? 'skipped' : g.overallState === 'FAILED' ? 'error' : g.overallState === 'RUNNING' ? 'running' : 'pending'}`}>
                        {g.overallState}
                      </span>
                    </td>
                    <td style={{ minWidth: 140 }}>
                      {g.totalRowsExpected > 0 || g.totalRowsCopied > 0 ? (
                        <div>
                          <div className="flex-between" style={{ fontSize: 11, marginBottom: 2 }}>
                            <span>{g.totalRowsCopied.toLocaleString()} / {g.totalRowsExpected > 0 ? g.totalRowsExpected.toLocaleString() : '?'}</span>
                            {g.totalRowsExpected > 0 && <span style={{ fontWeight: 600 }}>{groupPct}%</span>}
                          </div>
                          {g.totalRowsExpected > 0 && (
                            <div className="progress-bar" style={{ height: 4 }}>
                              <div className={`progress-bar-fill ${g.overallState === 'COMPLETED' || g.overallState === 'SKIPPED' ? 'success' : g.overallState === 'FAILED' ? 'error' : ''}`} style={{ width: `${groupPct}%` }} />
                            </div>
                          )}
                        </div>
                      ) : <span className="text-muted">-</span>}
                    </td>
                    <td className="text-sm text-muted">{earliest?.started_at ? new Date(earliest.started_at).toLocaleTimeString() : '-'}</td>
                    <td className="text-sm">{formatDuration(earliest?.started_at, g.overallState === 'RUNNING' ? undefined : latest?.finished_at) || '-'}</td>
                    <td className="text-sm" style={{ color: 'var(--error)', maxWidth: 300, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {firstErr}
                    </td>
                  </tr>
                  {/* ── Child rows: individual partitions/chunks ── */}
                  {g.hasChildren && isExpanded && g.dataJobs.map(j => {
                    const jobPct = j.rows_expected > 0 ? Math.round((j.rows_copied / j.rows_expected) * 100) : 0;
                    return (
                    <tr key={j.id} style={{ fontSize: 12 }}>
                      <td></td>
                      <td className="mono text-muted" style={{ paddingLeft: 24 }}>{j.partition || '(whole table)'}</td>
                      <td><span className="badge badge-pending">{j.phase}</span></td>
                      <td>
                        <span className={`badge badge-${j.state === 'COMPLETED' ? 'success' : j.state === 'SKIPPED' ? 'skipped' : j.state === 'FAILED' ? 'error' : j.state === 'RUNNING' ? 'running' : 'pending'}`}>
                          {j.state}
                        </span>
                      </td>
                      <td style={{ minWidth: 140 }}>
                        {j.rows_copied > 0 || j.rows_expected > 0 ? (
                          <div>
                            <div className="flex-between" style={{ fontSize: 11, marginBottom: 2 }}>
                              <span>{j.rows_copied.toLocaleString()} / {j.rows_expected > 0 ? j.rows_expected.toLocaleString() : '?'}</span>
                              {j.rows_expected > 0 && <span style={{ fontWeight: 600 }}>{jobPct}%</span>}
                            </div>
                            {j.rows_expected > 0 && (
                              <div className="progress-bar" style={{ height: 4 }}>
                                <div className={`progress-bar-fill ${j.state === 'COMPLETED' || j.state === 'SKIPPED' ? 'success' : j.state === 'FAILED' ? 'error' : ''}`} style={{ width: `${jobPct}%` }} />
                              </div>
                            )}
                          </div>
                        ) : <span className="text-muted">-</span>}
                      </td>
                      <td className="text-sm text-muted">{j.started_at ? new Date(j.started_at).toLocaleTimeString() : '-'}</td>
                      <td className="text-sm">{formatDuration(j.started_at, j.finished_at) || '-'}</td>
                      <td className="text-sm" style={{ color: 'var(--error)', maxWidth: 300, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {j.error}
                      </td>
                    </tr>
                    );
                  })}
                  {/* Non-data jobs (ddl, constraints, validate) shown as children too when expanded */}
                  {g.hasChildren && isExpanded && g.jobs.filter(j => j.phase !== 'data').map(j => (
                    <tr key={j.id} style={{ fontSize: 12, opacity: 0.7 }}>
                      <td></td>
                      <td className="mono text-muted" style={{ paddingLeft: 24 }}>{j.phase}</td>
                      <td><span className="badge badge-pending">{j.phase}</span></td>
                      <td>
                        <span className={`badge badge-${j.state === 'COMPLETED' ? 'success' : j.state === 'SKIPPED' ? 'skipped' : j.state === 'FAILED' ? 'error' : j.state === 'RUNNING' ? 'running' : 'pending'}`}>
                          {j.state}
                        </span>
                      </td>
                      <td className="text-muted">-</td>
                      <td className="text-sm text-muted">{j.started_at ? new Date(j.started_at).toLocaleTimeString() : '-'}</td>
                      <td className="text-sm">{formatDuration(j.started_at, j.finished_at) || '-'}</td>
                      <td className="text-sm" style={{ color: 'var(--error)', maxWidth: 300, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                        {j.error}
                      </td>
                    </tr>
                  ))}
                  </>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Starting overlay — shown during early phases (extract, plan) before jobs exist */}
      {isActive && jobs.length === 0 && (
        <div className="empty-state">
          <Loader size={40} className="spin" />
          <h3>{status?.phase?.detail || 'Starting migration...'}</h3>
          <p>
            {status?.phase?.phase === 'extracting' && 'Querying Oracle metadata: tables, columns, partitions, indexes...'}
            {status?.phase?.phase === 'planning' && 'Building migration plan: resolving partitions, computing chunks...'}
            {(!status?.phase || status.phase.phase === 'ddl') && 'This may take a moment for large schemas.'}
          </p>
        </div>
      )}

      {jobs.length === 0 && !running && !initialLoading && (
        <div className="empty-state">
          <Play size={40} />
          <h3>Ready to migrate</h3>
          <p>Click "Start Migration" to begin the Oracle to PostgreSQL migration.</p>
        </div>
      )}

      {initialLoading && !running && jobs.length === 0 && (
        <div className="empty-state">
          <Loader size={40} className="spin" />
          <h3>Loading migration status...</h3>
        </div>
      )}
    </div>
  );
}
