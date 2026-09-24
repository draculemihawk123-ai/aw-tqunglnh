import React, { useState, useEffect } from 'react';
import { TopBar } from './components/shell/TopBar';
import type { ConnectionState } from './components/shell/TopBar';
import { LeftNav } from './components/shell/LeftNav';
import type { NavRoute } from './components/shell/LeftNav';
import { ConnectionBanner, ProjectionBanner, OperationNotice } from './components/ui';
import { DoctorScreen } from './screens/Doctor';
import { INITIAL_PROJECTS, ProjectsScreen } from './screens/Projects';
import type { ProjectSummary, RepositorySummary } from './screens/Projects';
import { KanbanScreen, INITIAL_CARDS } from './screens/Kanban';
import type { WorkItemCard } from './screens/Kanban';
import { TaskDetailScreen } from './screens/TaskDetail';
import { DefinitionsScreen } from './screens/Definitions';
import { RunDiagnosticsScreen } from './screens/RunDiagnostics';
import { SettingsScreen } from './screens/Settings';
import { AdapterBuildsScreen } from './screens/AdapterBuilds';
import { Settings2 } from './components/icons';
import { ControlTooltip } from './components/ControlTooltip';
import { RepairAudit } from './screens/RepairAudit';

const TASK_ROUTES: NavRoute[] = ['task-overview', 'task-graph', 'task-workspace', 'task-evidence', 'task-chat'];

export default function App() {
  const [route, setRoute] = useState<NavRoute>('doctor');
  const [projects, setProjects] = useState<ProjectSummary[]>(INITIAL_PROJECTS);
  const [boards, setBoards] = useState<Record<string, WorkItemCard[]>>({ 'proj-alpha-001': INITIAL_CARDS });
  const [project, setProject] = useState<ProjectSummary | null>(null);
  const [taskSelected, setTaskSelected] = useState(false);
  const [connection, setConnection] = useState<ConnectionState>('Live');
  const [navCollapsed, setNavCollapsed] = useState(false);
  const [protoOpen, setProtoOpen] = useState(false);
  const [freshness, setFreshness] = useState('just now');
  const [rebuildPending, setRebuildPending] = useState(false);
  const [auditOpen, setAuditOpen] = useState(false);

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

  const isOffline = connection === 'Offline';

  const navigate = (r: NavRoute) => {
    setAuditOpen(false);
    setRoute(r);
    setTaskSelected(TASK_ROUTES.includes(r));
  };

  const handleSelectProject = (selectedProject: ProjectSummary) => {
    setProject(selectedProject);
    setTaskSelected(false);
    setRoute('project-overview');
  };

  const handleCreateProject = (name: string, _description: string) => {
    const newProject: ProjectSummary = {
      id: `proj-local-${String(projects.length + 1).padStart(3, '0')}`,
      name,
      freshness: 'just now',
      state: 'ACTIVE',
      repositories: [],
      components: [],
    };
    setProjects(current => [...current, newProject]);
    handleSelectProject(newProject);
  };

  const handleRegisterRepository = (projectId: string, repository: RepositorySummary) => {
    setProjects(current => current.map(item => item.id === projectId ? { ...item, repositories: [...item.repositories, repository], freshness: 'just now' } : item));
    setProject(current => current?.id === projectId ? { ...current, repositories: [...current.repositories, repository], freshness: 'just now' } : current);
  };

  const handleSetRepositoryState = (projectId: string, repositoryId: string, state: RepositorySummary['state']) => {
    const updateProject = (item: ProjectSummary) => item.id === projectId ? {
      ...item,
      freshness: 'just now',
      repositories: item.repositories.map(repository => repository.id === repositoryId ? { ...repository, state, lastProbe: state === 'REGISTERING' ? 'pending' : 'now' } : repository),
    } : item;
    setProjects(current => current.map(updateProject));
    setProject(current => current ? updateProject(current) : current);
  };

  const handleOpenTask = () => {
    setTaskSelected(true);
    setRoute('task-overview');
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
    if (auditOpen) return <RepairAudit onOpen={r => { if (r.startsWith('project-') || r.startsWith('task-')) setProject(projects[0]); navigate(r); }} />;
    if (route === 'doctor') return <DoctorScreen isOffline={isOffline} />;
    const projectProps = { projects, project, onSelectProject: handleSelectProject, onCreateProject: handleCreateProject, onRegisterRepository: handleRegisterRepository, onSetRepositoryState: handleSetRepositoryState, isOffline };
    if (route === 'projects') return <ProjectsScreen view="list" {...projectProps} />;
    if (route === 'project-overview') return <ProjectsScreen view="overview" {...projectProps} onNavigate={navigate} />;
    if (route === 'project-components') return <ProjectsScreen view="components" {...projectProps} onNavigate={navigate} />;
    if (route === 'project-board') {
      return project ? <KanbanScreen key={project.id} project={project} cards={boards[project.id] ?? []} setCards={update => setBoards(current => ({ ...current, [project.id]: typeof update === 'function' ? update(current[project.id] ?? []) : update }))} onOpenTask={handleOpenTask} isOffline={isOffline} /> : null;
    }
    if (TASK_ROUTES.includes(route)) {
      return <TaskDetailScreen activeTab={route} onTabChange={navigate} isOffline={isOffline} />;
    }
    if (route === 'project-definitions') return <DefinitionsScreen project={project} initialScope="project" isOffline={isOffline} />;
    if (route === 'global-definitions') return <DefinitionsScreen project={project} initialScope="global" isOffline={isOffline} />;
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
        onProjectSwitch={() => { setRoute('projects'); setProject(null); }}
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
