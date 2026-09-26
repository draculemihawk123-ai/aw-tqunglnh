import React, { useState, useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useLocation, useRouter } from 'wouter';
import { TopBar } from './components/shell/TopBar';
import type { ConnectionState } from './components/shell/TopBar';
import { LeftNav } from './components/shell/LeftNav';
import type { NavRoute } from './components/shell/LeftNav';
import { ConnectionBanner, ProjectionBanner, OperationNotice } from './components/ui';
import { DoctorScreen } from './screens/Doctor';
import { ProjectsScreen } from './screens/Projects';
import type { ProjectSummary } from './screens/Projects';
import type { ProjectView, RepositoryView } from './api/catalog';
import { projectRepositoriesList, projectsList } from './api/generated';
import { withSessionToken } from './api/session';
import { KanbanScreen } from './screens/Kanban';
import { TaskDetailScreen } from './screens/TaskDetail';
import { DefinitionsScreen } from './screens/Definitions';
import { RunDiagnosticsScreen } from './screens/RunDiagnostics';
import { SettingsScreen } from './screens/Settings';
import { AdapterBuildsScreen } from './screens/AdapterBuilds';
import { Settings2 } from './components/icons';
import { ControlTooltip } from './components/ControlTooltip';
import { RepairAudit } from './screens/RepairAudit';
import { FIXTURE_TASK_ID, PROJECT_SCOPED_ROUTES, matchPath, pathFor } from './routes';

const TASK_ROUTES: NavRoute[] = ['task-overview', 'task-graph', 'task-workspace', 'task-evidence', 'task-chat'];

export default function App() {
  const [location, setLocation] = useLocation();
  const router = useRouter();
  const [connection, setConnection] = useState<ConnectionState>('Live');
  const [navCollapsed, setNavCollapsed] = useState(false);
  const [protoOpen, setProtoOpen] = useState(false);
  const [freshness, setFreshness] = useState('just now');
  const [rebuildPending, setRebuildPending] = useState(false);
  const [auditOpen, setAuditOpen] = useState(false);

  const isOffline = connection === 'Offline';

  // Shared with Projects.tsx's own 'list' view via the identical
  // ['projects'] query key/queryFn — TanStack Query serves both from one
  // cache entry, so this never double-fetches.
  const projectsQuery = useQuery({
    queryKey: ['projects'],
    queryFn: async () => (await projectsList(withSessionToken())) as unknown as { projects: ProjectView[] },
    enabled: !isOffline,
  });

  // The URL is the one source of truth for "which route/project/task" —
  // V7-04's own "browser cache không tự quyết runtime state" completion
  // bar. `project` is DERIVED by resolving the URL's projectId against the
  // real, API-fetched projects list, never stored separately.
  const matched = auditOpen ? null : matchPath(router.parser, location);
  const route: NavRoute = matched?.route ?? 'doctor';
  const project = matched?.projectId ? projectsQuery.data?.projects.find(p => p.id === matched.projectId) ?? null : null;
  const taskSelected = TASK_ROUTES.includes(route);

  // Kanban's board filter chips and Definitions' scope label both still
  // read off the old prototype's ProjectSummary shape (see that type's own
  // doc comment in screens/Projects.tsx) — fed here from the SAME real
  // ['projectRepositories', projectId] query key Projects.tsx's own
  // overview uses, so it shares that cache entry rather than double-fetching
  // whenever the operator has already opened the project overview first.
  const repositoriesQuery = useQuery({
    queryKey: ['projectRepositories', project?.id],
    queryFn: async () => (await projectRepositoriesList(project!.id, withSessionToken())) as unknown as { repositories: RepositoryView[] },
    enabled: !isOffline && !!project,
  });
  const projectSummary: ProjectSummary | null = project
    ? { id: project.id, name: project.name, repositories: (repositoriesQuery.data?.repositories ?? []).map(r => ({ id: r.id, name: r.name, state: r.status })) }
    : null;

  // Route guard (V7-04's own "route guards by selected project only"): a
  // project- or task-scoped URL whose projectId does not resolve to a real
  // project — typed directly, a stale bookmark, a project since removed —
  // redirects to the project list rather than rendering a screen with a
  // null project it was never designed to handle. Never redirects while the
  // projects query is still loading — a real fetch takes real time, and a
  // deep link must not bounce to /projects just because the list has not
  // resolved yet.
  useEffect(() => {
    if (auditOpen || !matched || projectsQuery.isPending) return;
    if (PROJECT_SCOPED_ROUTES.has(matched.route) && !project) {
      setLocation(pathFor('projects'), { replace: true });
    }
  }, [auditOpen, matched?.route, matched?.projectId, project, projectsQuery.isPending, setLocation]);

  // An unmatched path (including "/") settles on a real URL rather than
  // rendering "doctor" while the address bar shows something else.
  useEffect(() => {
    if (!auditOpen && !matched && location !== pathFor('doctor')) {
      setLocation(pathFor('doctor'), { replace: true });
    }
  }, [auditOpen, matched, location, setLocation]);

  useEffect(() => {
    const mq = window.matchMedia('(max-width: 1024px)');
    setNavCollapsed(mq.matches);
    const handler = (e: MediaQueryListEvent) => setNavCollapsed(e.matches);
    mq.addEventListener('change', handler);
    return () => mq.removeEventListener('change', handler);
  }, []);

  // Derive projection state from connection
  type ProjState = 'Fresh' | 'Stale' | 'Degraded';
  const projectionState: ProjState =
    connection === 'Stale'    ? 'Stale'
    : connection === 'Degraded' ? 'Degraded'
    : 'Fresh';

  const navigate = (r: NavRoute, ids: { projectId?: string; taskId?: string } = {}) => {
    setAuditOpen(false);
    const projectId = ids.projectId ?? project?.id;
    const taskId = ids.taskId ?? FIXTURE_TASK_ID;
    setLocation(pathFor(r, { projectId, taskId }));
  };

  const handleSelectProject = (selectedProjectId: string) => {
    navigate('project-overview', { projectId: selectedProjectId });
  };

  const handleOpenTask = (workItemId: string) => {
    if (!project) return;
    navigate('task-overview', { projectId: project.id, taskId: workItemId });
  };

  const switchConnection = (state: ConnectionState) => {
    setConnection(state);
    if (state === 'Live') setFreshness('just now');
    else if (state === 'Stale') setFreshness('4 min ago');
    else if (state === 'Offline') setFreshness('12 min ago (last sync)');
    else if (state === 'Degraded') setFreshness('8 min ago');
    else if (state === 'Reconnecting') setFreshness('2 min ago');
  };

  const renderContent = () => {
    if (auditOpen) {
      return <RepairAudit onOpen={r => navigate(r, PROJECT_SCOPED_ROUTES.has(r) ? { projectId: projectsQuery.data?.projects[0]?.id } : {})} />;
    }
    if (route === 'doctor') return <DoctorScreen isOffline={isOffline} />;
    const projectProps = { projectId: project?.id, onSelectProject: handleSelectProject, isOffline };
    if (route === 'projects') return <ProjectsScreen view="list" {...projectProps} />;
    if (route === 'project-overview') return <ProjectsScreen view="overview" {...projectProps} onNavigate={navigate} />;
    if (route === 'project-components') return <ProjectsScreen view="components" {...projectProps} onNavigate={navigate} />;
    if (route === 'project-board') {
      return projectSummary ? <KanbanScreen key={projectSummary.id} project={projectSummary} onOpenTask={handleOpenTask} isOffline={isOffline} /> : null;
    }
    if (TASK_ROUTES.includes(route)) {
      return <TaskDetailScreen activeTab={route} onTabChange={navigate} isOffline={isOffline} />;
    }
    if (route === 'project-definitions') return <DefinitionsScreen project={projectSummary} initialScope="project" isOffline={isOffline} />;
    if (route === 'global-definitions') return <DefinitionsScreen project={projectSummary} initialScope="global" isOffline={isOffline} />;
    if (route === 'system-diagnostics') return <RunDiagnosticsScreen isOffline={isOffline} projectionState={projectionState} freshness={freshness} />;
    if (route === 'system-settings') return <SettingsScreen isOffline={isOffline} />;
    if (route === 'system-adapters') return <AdapterBuildsScreen isOffline={isOffline} />;
    return (
      <div className="flex-1 flex items-center justify-center bg-[#E9EDF3]">
        <p className="text-[13px] text-[#475569]">Select a section from the navigation.</p>
      </div>
    );
  };

  const CONN_STATES: { state: ConnectionState; label: string; style: string }[] = [
    { state: 'Live',         label: 'Live',         style: 'bg-[#DCFCE7] text-[#166534] border-[#86EFAC]' },
    { state: 'Reconnecting', label: 'Reconnecting', style: 'bg-[#DBEAFE] text-[#1E40AF] border-[#93C5FD]' },
    { state: 'Stale' as ConnectionState, label: 'Stale', style: 'bg-[#FEF3C7] text-[#92400E] border-[#FCD34D]' },
    { state: 'Offline',      label: 'Offline',      style: 'bg-[#FEE2E2] text-[#991B1B] border-[#FCA5A5]' },
    { state: 'Degraded',     label: 'Degraded',     style: 'bg-[#FEE2E2] text-[#991B1B] border-[#FCA5A5]' },
  ];

  return (
    <div className="h-full flex flex-col bg-[#E9EDF3]">
      <TopBar
        project={project?.name ?? null}
        connectionState={connection}
        freshness={freshness}
        onProjectSwitch={() => navigate('projects')}
      />

      {/* Persistent banners below top bar */}
      <ConnectionBanner state={connection} />
      {(projectionState === 'Stale' || projectionState === 'Degraded') && (
        <ProjectionBanner
          state={projectionState}
          watermark={freshness}
          onRefresh={projectionState === 'Stale' ? () => switchConnection('Live') : undefined}
          onRebuild={projectionState === 'Degraded' && !rebuildPending ? () => {
            setRebuildPending(true);
            window.setTimeout(() => { setRebuildPending(false); switchConnection('Live'); }, 1600);
          } : undefined}
          onDiagnostics={() => navigate('system-diagnostics')}
        />
      )}
      {rebuildPending && <OperationNotice state="Requested" message="Prototype rebuild requested; waiting for simulated projection catch-up." ref="op-rebuild-1842" />}

      <div className="flex flex-1 overflow-hidden gap-2 p-2">
        <LeftNav
          route={route}
          onNavigate={navigate}
          projectSelected={!!project}
          taskSelected={taskSelected}
          collapsed={navCollapsed}
        />

        <main
          className="flex-1 flex flex-col overflow-hidden bg-white rounded-[12px] border border-[#CDD5DF] island-shadow min-w-0"
          aria-label="Main content"
        >
          {renderContent()}
        </main>
      </div>

      {/* Prototype state controls — hidden by default, not part of production chrome */}
      <div className="fixed bottom-4 right-4 z-50">
        <button
          onClick={() => setProtoOpen(!protoOpen)}
          aria-label={protoOpen ? 'Hide prototype controls' : 'Show prototype controls'}
          title="Prototype state controls"
          className="w-8 h-8 flex items-center justify-center rounded-full bg-[#172033] text-white shadow-lg hover:bg-[#2D3748] transition-colors"
        >
          <Settings2 size={14} aria-hidden />
        </button>
        {protoOpen && (
          <div
            role="region"
            aria-label="Prototype state controls — not part of production handoff"
            className="absolute bottom-10 right-0 bg-white border border-[#CDD5DF] rounded-[12px] shadow-xl p-4 w-56"
          >
            <p className="text-[12px] font-semibold text-[#475569] uppercase tracking-wider mb-3">Prototype Controls</p>
            <p className="text-[12px] text-[#475569] mb-2">Simulate connection and projection states.</p>
            <button className="text-[12px] text-[#3659E3] underline mb-3" onClick={() => { setAuditOpen(true); setProtoOpen(false); }}>Repair audit</button>
            <div className="space-y-1.5">
              {CONN_STATES.map(cs => (
                <button
                  key={cs.state}
                  onClick={() => switchConnection(cs.state)}
                  className={`w-full flex items-center gap-2 px-3 py-2 rounded-[6px] border text-[12px] font-medium transition-colors ${
                    connection === cs.state ? cs.style : 'bg-white text-[#475569] border-[#CDD5DF] hover:bg-[#F3F5F8]'
                  }`}
                >
                  <span className={`w-2 h-2 rounded-full flex-shrink-0 ${connection === cs.state ? 'bg-current' : 'bg-[#CDD5DF]'}`} aria-hidden />
                  {cs.label}
                </button>
              ))}
            </div>
          </div>
        )}
      </div>
      <ControlTooltip />
    </div>
  );
}
