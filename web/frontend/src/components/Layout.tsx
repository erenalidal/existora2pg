import { NavLink, Outlet, useParams, useLocation } from 'react-router-dom';
import { Database, FolderOpen, Play, CheckCircle, Code, LayoutDashboard, ListChecks, Eye, Lightbulb, Download, Upload, X } from 'lucide-react';
import { useEffect, useState, useRef } from 'react';
import { health, updates, UpdateInfo } from '../api';

export function Layout() {
  const { id } = useParams();
  const location = useLocation();
  const [updateInfo, setUpdateInfo] = useState<UpdateInfo | null>(null);
  const [updateDismissed, setUpdateDismissed] = useState(false);
  const [updating, setUpdating] = useState(false);
  const [updateMsg, setUpdateMsg] = useState('');

  const fileInputRef = useRef<HTMLInputElement>(null);
  const [appVersion, setAppVersion] = useState('');

  useEffect(() => {
    health.get().then(h => setAppVersion(h.version)).catch(() => {});
    updates.check().then(info => {
      if (info.available) setUpdateInfo(info);
    }).catch(() => {});
  }, []);

  const handleFileUpdate = async (file: File) => {
    setUpdating(true);
    setUpdateMsg('Güncelleniyor...');
    try {
      const res = await updates.applyLocal(file);
      setUpdateMsg(res.message || 'Güncelleme tamamlandı. Uygulamayı yeniden başlatın.');
    } catch (e: unknown) {
      setUpdateMsg('Hata: ' + (e as Error).message);
      setUpdating(false);
    }
  };

  const isActive = (path: string) => location.pathname === path;

  // Determine current step for visual indicator
  const getStepState = (path: string) => {
    if (!id) return '';
    const steps = [
      `/projects/${id}`,
      `/projects/${id}/objects`,
      `/projects/${id}/ddl`,
      `/projects/${id}/migrate`,
      `/projects/${id}/validate`,
    ];
    const currentIdx = steps.findIndex(s => location.pathname.startsWith(s) && location.pathname === s);
    const stepIdx = steps.indexOf(path);
    if (stepIdx < currentIdx) return 'done';
    if (stepIdx === currentIdx) return 'active';
    return '';
  };

  return (
    <div className="layout">
      <aside className="sidebar">
        <div className="sidebar-logo">
          <h1>existora2pg</h1>
          <span>Oracle → PostgreSQL</span>
          {appVersion && <span style={{ fontSize: 10, color: 'var(--text-muted, #666)', marginTop: 2 }}>{appVersion}</span>}
        </div>
        <nav className="sidebar-nav">
          <NavLink to="/projects" end>
            <FolderOpen size={16} /> Projects
          </NavLink>
          {id && (
            <>
              <div className="sidebar-divider" />
              <div className="sidebar-section">WORKFLOW</div>
              <NavLink to={`/projects/${id}`} end>
                <LayoutDashboard size={16} /> Dashboard
              </NavLink>
              <NavLink to={`/projects/${id}/objects`}>
                <ListChecks size={16} /> Select Objects
              </NavLink>
              <NavLink to={`/projects/${id}/ddl`}>
                <Code size={16} /> DDL Preview
              </NavLink>
              <NavLink to={`/projects/${id}/migrate`}>
                <Play size={16} /> Migration
              </NavLink>
              <NavLink to={`/projects/${id}/validate`}>
                <CheckCircle size={16} /> Validation
              </NavLink>
              <div className="sidebar-divider" />
              <div className="sidebar-section">TOOLS</div>
              <NavLink to={`/projects/${id}/schema`}>
                <Eye size={16} /> Schema Explorer
              </NavLink>
              <NavLink to={`/projects/${id}/recommendations`}>
                <Lightbulb size={16} /> Recommendations
              </NavLink>
            </>
          )}
        </nav>
        <div style={{ padding: '12px 16px', borderTop: '1px solid var(--border)', marginTop: 'auto' }}>
          <input
            ref={fileInputRef}
            type="file"
            accept=".zip"
            style={{ display: 'none' }}
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) handleFileUpdate(file);
              e.target.value = '';
            }}
          />
          <button
            onClick={() => fileInputRef.current?.click()}
            disabled={updating}
            style={{
              width: '100%',
              padding: '8px 12px',
              background: 'var(--bg-secondary)',
              color: 'var(--text-secondary)',
              border: '1px solid var(--border)',
              borderRadius: 6,
              cursor: updating ? 'not-allowed' : 'pointer',
              fontSize: 12,
              display: 'flex',
              alignItems: 'center',
              gap: 6,
            }}
          >
            <Upload size={13} />
            {updating ? updateMsg || 'Güncelleniyor...' : 'Dosyadan Güncelle'}
          </button>
        </div>
      </aside>
      <main className="main-content">
        {updateInfo && !updateDismissed && (
          <div style={{
            background: '#1a365d',
            color: '#bee3f8',
            padding: '10px 16px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            borderBottom: '1px solid #2a4365',
            fontSize: 13,
          }}>
            <span>
              <Download size={14} style={{ marginRight: 6, verticalAlign: -2 }} />
              Yeni versiyon mevcut: <strong>{updateInfo.latest_version}</strong>
              {updateInfo.current_version && <> (mevcut: {updateInfo.current_version})</>}
            </span>
            <span style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              {updateMsg && <span style={{ color: '#90cdf4' }}>{updateMsg}</span>}
              {updateInfo.download_url && !updating && (
                <button
                  onClick={async () => {
                    setUpdating(true);
                    setUpdateMsg('Indiriliyor...');
                    try {
                      const res = await updates.apply(updateInfo.download_url!);
                      setUpdateMsg(res.message || 'Güncelleme tamamlandı. Uygulamayı yeniden başlatın.');
                    } catch (e: unknown) {
                      setUpdateMsg('Hata: ' + (e as Error).message);
                      setUpdating(false);
                    }
                  }}
                  style={{
                    background: '#3182ce',
                    color: 'white',
                    border: 'none',
                    padding: '4px 12px',
                    borderRadius: 4,
                    cursor: 'pointer',
                    fontSize: 12,
                  }}
                >
                  Güncelle
                </button>
              )}
              {updateInfo.release_url && (
                <a
                  href={updateInfo.release_url}
                  target="_blank"
                  rel="noreferrer"
                  style={{ color: '#90cdf4', fontSize: 12 }}
                >
                  Release Notes
                </a>
              )}
              <button
                onClick={() => setUpdateDismissed(true)}
                style={{ background: 'none', border: 'none', color: '#bee3f8', cursor: 'pointer', padding: 2 }}
              >
                <X size={14} />
              </button>
            </span>
          </div>
        )}
        <Outlet />
      </main>
    </div>
  );
}
