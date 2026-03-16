import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { Layout } from './components/Layout';
import { ProjectList } from './pages/ProjectList';
import { ProjectCreate } from './pages/ProjectCreate';
import { ProjectDashboard } from './pages/ProjectDashboard';
import { ObjectSelector } from './pages/ObjectSelector';
import { SchemaExplorer } from './pages/SchemaExplorer';
import { DDLPreview } from './pages/DDLPreview';
import { MigrationRun } from './pages/MigrationRun';
import { ValidationPage } from './pages/ValidationPage';
import { RecommendationsPage } from './pages/RecommendationsPage';

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<Navigate to="/projects" replace />} />
          <Route path="/projects" element={<ProjectList />} />
          <Route path="/projects/new" element={<ProjectCreate />} />
          <Route path="/projects/:id" element={<ProjectDashboard />} />
          <Route path="/projects/:id/edit" element={<ProjectCreate />} />
          <Route path="/projects/:id/objects" element={<ObjectSelector />} />
          <Route path="/projects/:id/schema" element={<SchemaExplorer />} />
          <Route path="/projects/:id/ddl" element={<DDLPreview />} />
          <Route path="/projects/:id/migrate" element={<MigrationRun />} />
          <Route path="/projects/:id/validate" element={<ValidationPage />} />
          <Route path="/projects/:id/recommendations" element={<RecommendationsPage />} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}
