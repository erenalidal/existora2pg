import { useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Plus, Trash2, Database } from 'lucide-react';
import { projects, Project } from '../api';

export function ProjectList() {
  const [list, setList] = useState<Project[]>([]);
  const [loading, setLoading] = useState(true);
  const navigate = useNavigate();

  useEffect(() => {
    projects.list().then(setList).catch(() => setList([])).finally(() => setLoading(false));
  }, []);

  const handleDelete = async (id: string, name: string) => {
    if (!confirm(`"${name}" projesini silmek istediginize emin misiniz?`)) return;
    await projects.delete(id);
    setList(prev => prev.filter(p => p.id !== id));
  };

  if (loading) return <div className="loading-page"><span className="spinner" /> Loading...</div>;

  return (
    <div>
      <div className="page-header">
        <div>
          <h2>Migration Projects</h2>
          <p className="text-muted text-sm">Oracle to PostgreSQL migration projects</p>
        </div>
        <Link to="/projects/new" className="btn btn-primary">
          <Plus size={14} /> New Project
        </Link>
      </div>

      {list.length === 0 ? (
        <div className="empty-state">
          <Database size={40} />
          <h3>No projects yet</h3>
          <p>Create your first migration project to get started.</p>
          <Link to="/projects/new" className="btn btn-primary">
            <Plus size={14} /> Create Project
          </Link>
        </div>
      ) : (
        <div className="card-grid card-grid-2">
          {list.map(p => (
            <div key={p.id} className="card" style={{ cursor: 'pointer' }} onClick={() => navigate(`/projects/${p.id}`)}>
              <div className="flex-between">
                <h3 style={{ fontSize: 16, fontWeight: 600 }}>{p.name}</h3>
                <button className="btn btn-sm btn-secondary" onClick={e => { e.stopPropagation(); handleDelete(p.id, p.name); }}>
                  <Trash2 size={12} />
                </button>
              </div>
              <div className="mt-12" style={{ fontSize: 12 }}>
                <div className="flex gap-8 mb-16">
                  <span className="badge badge-info">Oracle</span>
                  <span className="text-mono text-muted">{p.oracle_schema}</span>
                </div>
                <div className="flex gap-8">
                  <span className="badge badge-success">PG</span>
                  <span className="text-mono text-muted">{p.pg_schema || 'public'}</span>
                </div>
              </div>
              <div className="text-muted text-sm mt-12">
                Created: {new Date(p.created_at).toLocaleDateString()}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
