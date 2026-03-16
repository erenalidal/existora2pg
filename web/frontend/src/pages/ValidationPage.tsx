import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { CheckCircle, XCircle, Play } from 'lucide-react';
import { validation, ValidationResult } from '../api';

export function ValidationPage() {
  const { id } = useParams<{ id: string }>();
  const [result, setResult] = useState<ValidationResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const runValidation = async () => {
    if (!id) return;
    setLoading(true);
    setError('');
    try {
      const r = await validation.run(id);
      setResult(r);
    } catch (e: unknown) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  };

  const allMatch = result?.results.every(r => r.match) ?? false;
  const totalOracle = result?.results.reduce((sum, r) => sum + r.oracle_count, 0) ?? 0;
  const totalPg = result?.results.reduce((sum, r) => sum + r.pg_count, 0) ?? 0;

  return (
    <div>
      <div className="page-header">
        <div>
          <div className="breadcrumb"><Link to={`/projects/${id}`}>Dashboard</Link> / Validation</div>
          <h2>Post-Migration Validation</h2>
        </div>
        <button className="btn btn-primary" onClick={runValidation} disabled={loading}>
          {loading ? <span className="spinner" /> : <Play size={14} />}
          Run Validation
        </button>
      </div>

      {error && <div className="alert alert-error">{error}</div>}

      {result && (
        <>
          <div className={`alert ${allMatch ? 'alert-success' : 'alert-error'}`} style={{ marginBottom: 20 }}>
            <div className="flex gap-8" style={{ alignItems: 'center' }}>
              {allMatch ? <CheckCircle size={16} /> : <XCircle size={16} />}
              <span style={{ fontWeight: 600 }}>
                {allMatch ? 'All tables validated successfully' : 'Validation failed — row count mismatches detected'}
              </span>
            </div>
          </div>

          <div className="card-grid card-grid-3 mb-16">
            <div className="card">
              <div className="stat">
                <div className="stat-value">{result.results.length}</div>
                <div className="stat-label">Tables</div>
              </div>
            </div>
            <div className="card">
              <div className="stat">
                <div className="stat-value">{totalOracle.toLocaleString()}</div>
                <div className="stat-label">Oracle Total Rows</div>
              </div>
            </div>
            <div className="card">
              <div className="stat">
                <div className="stat-value">{totalPg.toLocaleString()}</div>
                <div className="stat-label">PostgreSQL Total Rows</div>
              </div>
            </div>
          </div>

          <div className="card">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Table</th>
                  <th style={{ textAlign: 'right' }}>Oracle Rows</th>
                  <th style={{ textAlign: 'right' }}>PG Rows</th>
                  <th style={{ textAlign: 'right' }}>Diff</th>
                  <th>Status</th>
                  <th>Error</th>
                </tr>
              </thead>
              <tbody>
                {result.results.map(r => {
                  const diff = r.oracle_count - r.pg_count;
                  return (
                    <tr key={r.table}>
                      <td className="mono">{r.table}</td>
                      <td style={{ textAlign: 'right' }}>{r.oracle_count.toLocaleString()}</td>
                      <td style={{ textAlign: 'right' }}>{r.pg_count.toLocaleString()}</td>
                      <td style={{ textAlign: 'right', color: diff !== 0 ? 'var(--error)' : 'var(--success)' }}>
                        {diff !== 0 ? (diff > 0 ? `+${diff.toLocaleString()}` : diff.toLocaleString()) : '0'}
                      </td>
                      <td>
                        {r.match ? (
                          <span className="badge badge-success">MATCH</span>
                        ) : (
                          <span className="badge badge-error">MISMATCH</span>
                        )}
                      </td>
                      <td className="text-sm text-muted">{r.error || ''}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </>
      )}

      {!result && !loading && (
        <div className="empty-state">
          <CheckCircle size={40} />
          <h3>Validate your migration</h3>
          <p>Compare row counts between Oracle and PostgreSQL to verify data integrity.</p>
        </div>
      )}
    </div>
  );
}
