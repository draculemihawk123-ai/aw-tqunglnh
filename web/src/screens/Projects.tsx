import React, { useState } from 'react';
import { Badge, StatusBadge, Button, Dialog, BlockerCard, CopyableId } from '../components/ui';
import { AlertTriangle, ArrowLeft, Boxes, CheckCircle2, ChevronRight, Columns3, FolderGit2, Plus } from '../components/icons';

type OnboardingState = 'REGISTERING' | 'PROBING' | 'ACTIVE' | 'BLOCKED';
type ProjectView = 'list' | 'overview' | 'components';

export interface RepositorySummary {
  id: string;
  name: string;
  defaultRef: string;
  state: OnboardingState;
  lastProbe: string;
}

export interface ComponentSummary {
  id: string;
  path: string;
  repo: string;
  health: 'ACTIVE' | 'BLOCKED';
  pack: string;
  compatible: boolean;
}

export interface ProjectSummary {
  id: string;
  name: string;
  freshness: string;
  state: 'ACTIVE';
  repositories: RepositorySummary[];
  components: ComponentSummary[];
}

export const INITIAL_PROJECTS: ProjectSummary[] = [
  {
    id: 'proj-alpha-001', name: 'platform-core', freshness: '2 min ago', state: 'ACTIVE',
    repositories: [
      { id: 'repo-a1b2', name: 'core-api', defaultRef: 'main', state: 'ACTIVE', lastProbe: '09:41' },
      { id: 'repo-c3d4', name: 'worker-service', defaultRef: 'main', state: 'BLOCKED', lastProbe: '09:39' },
    ],
    components: [
      { id: 'comp-001', path: 'src/api', repo: 'core-api', health: 'ACTIVE', pack: 'backend-pack@2.4.1', compatible: true },
      { id: 'comp-002', path: 'src/worker', repo: 'worker-service', health: 'BLOCKED', pack: 'backend-pack@2.4.1', compatible: false },
      { id: 'comp-003', path: 'src/shared', repo: 'core-api', health: 'ACTIVE', pack: 'shared-pack@1.2.0', compatible: true },
    ],
  },
  {
    id: 'proj-beta-002', name: 'data-pipeline', freshness: '5 min ago', state: 'ACTIVE',
    repositories: [
      { id: 'repo-data-01', name: 'ingestion-api', defaultRef: 'main', state: 'ACTIVE', lastProbe: '09:37' },
      { id: 'repo-data-02', name: 'transform-worker', defaultRef: 'main', state: 'ACTIVE', lastProbe: '09:36' },
      { id: 'repo-data-03', name: 'warehouse-writer', defaultRef: 'main', state: 'ACTIVE', lastProbe: '09:35' },
    ],
    components: [
      { id: 'comp-data-01', path: 'src/ingestion', repo: 'ingestion-api', health: 'ACTIVE', pack: 'backend-pack@2.4.1', compatible: true },
      { id: 'comp-data-02', path: 'src/transform', repo: 'transform-worker', health: 'ACTIVE', pack: 'worker-pack@1.8.0', compatible: true },
      { id: 'comp-data-03', path: 'src/writer', repo: 'warehouse-writer', health: 'ACTIVE', pack: 'worker-pack@1.8.0', compatible: true },
    ],
  },
  {
    id: 'proj-gamma-003', name: 'auth-service', freshness: '18 min ago', state: 'ACTIVE',
    repositories: [
      { id: 'repo-auth-01', name: 'identity-api', defaultRef: 'main', state: 'ACTIVE', lastProbe: '09:23' },
    ],
    components: [
      { id: 'comp-auth-01', path: 'src/auth', repo: 'identity-api', health: 'ACTIVE', pack: 'security-pack@1.1.0', compatible: true },
    ],
  },
];

interface Props {
  view: ProjectView;
  projects: ProjectSummary[];
  project?: ProjectSummary | null;
  onSelectProject: (project: ProjectSummary) => void;
  onCreateProject: (name: string, description: string) => void;
  onRegisterRepository: (projectId: string, repository: RepositorySummary) => void;
  onSetRepositoryState: (projectId: string, repositoryId: string, state: OnboardingState) => void;
  onNavigate?: (route: 'projects' | 'project-board' | 'project-components') => void;
  isOffline?: boolean;
}

function ProjectIdentity({ project, onBack }: { project: ProjectSummary; onBack?: () => void }) {
  const blockerCount = project.repositories.filter(repository => repository.state === 'BLOCKED').length;
  return (
    <div>
      {onBack && (
        <button onClick={onBack} className="inline-flex items-center gap-1 text-[12px] text-[#475569] hover:text-[#3659E3] transition-colors">
          <ArrowLeft size={13} aria-hidden /> Projects
        </button>
      )}
      <div className="flex items-center gap-3 mt-2">
        <h1 className="text-xl font-semibold text-[#172033]">{project.name}</h1>
        <StatusBadge state="ACTIVE" entity="repository" />
        <Badge label="FRESH" intent="success" />
      </div>
      <div className="flex items-center gap-3 mt-1 text-[12px] text-[#475569]">
        <CopyableId value={project.id} />
        <span>updated {project.freshness}</span>
        <span>{project.repositories.length} repositories · {project.components.length} components · {blockerCount} {blockerCount === 1 ? 'blocker' : 'blockers'}</span>
      </div>
    </div>
  );
}

export function ProjectsScreen({ view, projects, project, onSelectProject, onCreateProject, onRegisterRepository, onSetRepositoryState, onNavigate, isOffline = false }: Props) {
  const [createDialog, setCreateDialog] = useState(false);
  const [projectName, setProjectName] = useState('');
  const [projectDescription, setProjectDescription] = useState('');
  const [registerDialog, setRegisterDialog] = useState(false);
  const [repositoryName, setRepositoryName] = useState('');
  const [defaultRef, setDefaultRef] = useState('main');
  const [retrying, setRetrying] = useState<string | null>(null);
  const [registering, setRegistering] = useState(false);
  const [assignTarget, setAssignTarget] = useState<ComponentSummary | null>(null);
  const [assignedPacks, setAssignedPacks] = useState<Record<string, string>>({});
  const activeProject = project ?? projects[0] ?? INITIAL_PROJECTS[0];
  const blocker = activeProject.repositories.find(repository => repository.state === 'BLOCKED');
  const normalizedProjectName = projectName.trim();
  const projectNameTaken = projects.some(existing => existing.name.toLowerCase() === normalizedProjectName.toLowerCase());

  const retryProbe = (repository: RepositorySummary) => {
    if (isOffline) return;
    setRetrying(repository.id);
    onSetRepositoryState(activeProject.id, repository.id, 'PROBING');
    window.setTimeout(() => {
      setRetrying(null);
      onSetRepositoryState(activeProject.id, repository.id, 'ACTIVE');
    }, 1200);
  };

  const registerRepository = () => {
    if (isOffline || !repositoryName.trim() || !defaultRef.trim()) return;
    const sequence = activeProject.repositories.length + 1;
    const repository: RepositorySummary = {
      id: `repo-local-${String(sequence).padStart(2, '0')}`,
      name: repositoryName.trim(),
      defaultRef: defaultRef.trim(),
      state: 'REGISTERING',
      lastProbe: 'pending',
    };
    const finalState: OnboardingState = sequence === 2 ? 'BLOCKED' : 'ACTIVE';
    setRegisterDialog(false);
    setRegistering(true);
    setRepositoryName('');
    setDefaultRef('main');
    onRegisterRepository(activeProject.id, repository);
    window.setTimeout(() => onSetRepositoryState(activeProject.id, repository.id, 'PROBING'), 600);
    window.setTimeout(() => {
      onSetRepositoryState(activeProject.id, repository.id, finalState);
      setRegistering(false);
    }, 1400);
  };

  if (view === 'list') {
    return (
      <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
        <div className="max-w-5xl mx-auto space-y-6">
          <div className="flex items-center justify-between">
            <div>
              <h1 className="text-xl font-semibold text-[#172033]">Projects</h1>
              <p className="text-[13px] text-[#475569] mt-0.5">{projects.length} projects in this installation.</p>
            </div>
            <Button intent="primary" onClick={() => setCreateDialog(true)} icon={<Plus size={14} aria-hidden />} disabled={isOffline} title={isOffline ? 'Reconnect to create a project' : undefined}>New Project</Button>
          </div>
          <div className="grid grid-cols-1 gap-3">
            {projects.map(proj => {
              const blockerCount = proj.repositories.filter(repository => repository.state === 'BLOCKED').length;
              return (
              <button key={proj.id} onClick={() => onSelectProject(proj)}
                className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow p-5 text-left hover:border-[#AAB4C3] transition-colors group">
                <div className="flex items-start justify-between gap-4">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-3 flex-wrap">
                      <span className="text-base font-semibold text-[#172033] group-hover:text-[#3659E3]">{proj.name}</span>
                      <StatusBadge state={proj.state} entity="repository" />
                      {blockerCount > 0 && <Badge label={`${blockerCount} blocker`} intent="warning" />}
                    </div>
                    <div className="flex items-center gap-4 mt-2 text-[12px] text-[#475569]">
                      <span>{proj.repositories.length} repositories</span><span>updated {proj.freshness}</span><span className="font-mono">{proj.id}</span>
                    </div>
                  </div>
                  <ChevronRight size={16} className="text-[#475569] group-hover:text-[#3659E3]" aria-hidden />
                </div>
              </button>
            );})}
          </div>
        </div>
        {createDialog && (
          <Dialog title="Create Project" description="A project groups repositories and WorkItems under a shared execution context."
            onClose={() => setCreateDialog(false)}
            actions={<><Button intent="secondary" onClick={() => setCreateDialog(false)}>Cancel</Button><Button intent="primary" disabled={!normalizedProjectName || projectNameTaken} onClick={() => { setCreateDialog(false); onCreateProject(normalizedProjectName, projectDescription.trim()); setProjectName(''); setProjectDescription(''); }}>Create</Button></>}>
            <div className="space-y-4">
              <div className="flex flex-col gap-1"><label htmlFor="project-name" className="text-[12px] font-medium">Project name</label><input id="project-name" value={projectName} onChange={event => setProjectName(event.target.value)} className="h-9 px-3 rounded-[6px] border border-[#CDD5DF] text-[13px]" placeholder="my-project" />{projectNameTaken && <p role="alert" className="text-[12px] text-[#991B1B]">A project with this name already exists.</p>}</div>
              <div className="flex flex-col gap-1"><label htmlFor="project-description" className="text-[12px] font-medium">Description</label><textarea id="project-description" value={projectDescription} onChange={event => setProjectDescription(event.target.value)} className="h-20 px-3 py-2 rounded-[6px] border border-[#CDD5DF] text-[13px] resize-none" placeholder="Optional." /></div>
            </div>
          </Dialog>
        )}
      </div>
    );
  }

  if (view === 'components') {
    return (
      <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
        <div className="max-w-5xl mx-auto space-y-5">
          <ProjectIdentity project={activeProject} onBack={() => onNavigate?.('projects')} />
          <div className="flex items-center justify-between">
            <div><h2 className="text-lg font-semibold text-[#172033]">Components</h2><p className="text-[13px] text-[#475569]">Discovered component identities and exact Engineering Pack assignments.</p></div>
            <Button intent="secondary" size="compact" onClick={() => onNavigate?.('project-board')}><Columns3 size={13} aria-hidden /> Open Board</Button>
          </div>
          <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
            <table className="w-full text-[13px]">
              <thead className="bg-[#F8FAFC] border-b border-[#CDD5DF]"><tr>
                <th className="text-left px-5 py-3 text-[12px]">Component / ID</th><th className="text-left px-5 py-3 text-[12px]">Repository / path</th><th className="text-left px-5 py-3 text-[12px]">Health</th><th className="text-left px-5 py-3 text-[12px]">Exact Pack</th><th className="text-left px-5 py-3 text-[12px]">Compatibility</th><th aria-label="Actions" />
              </tr></thead>
              <tbody className="divide-y divide-[#ECEFF4]">{activeProject.components.map(comp => (
                <tr key={comp.id} className="hover:bg-[#FAFBFC]">
                  <td className="px-5 py-4"><div className="font-medium">{comp.path.split('/').at(-1)}</div><CopyableId value={comp.id} /></td>
                  <td className="px-5 py-4"><div>{comp.repo}</div><div className="font-mono text-[12px] text-[#475569]">{comp.path}</div></td>
                  <td className="px-5 py-4"><StatusBadge state={comp.health} entity="repository" /></td>
                  <td className="px-5 py-4 font-mono text-[12px]">{assignedPacks[comp.id] ?? comp.pack}</td>
                  <td className="px-5 py-4">{comp.compatible ? <span className="inline-flex items-center gap-1 text-[#166534]"><CheckCircle2 size={13} aria-hidden /> Compatible</span> : <span className="inline-flex items-center gap-1 text-[#991B1B]"><AlertTriangle size={13} aria-hidden /> Incompatible with repository probe</span>}</td>
                  <td className="px-5 py-4 text-right"><Button size="compact" intent="quiet" disabled={isOffline || !comp.compatible} onClick={() => setAssignTarget(comp)}>Assign Pack</Button></td>
                </tr>
              ))}{activeProject.components.length === 0 && <tr><td colSpan={6} className="px-5 py-10 text-center text-[13px] text-[#475569]">No components discovered yet. Register and probe a repository first.</td></tr>}</tbody>
            </table>
          </div>
        </div>
        {assignTarget && <Dialog title="Assign Engineering Pack" description="Assign an exact immutable Pack version to this component."
          onClose={() => setAssignTarget(null)} actions={<><Button intent="secondary" onClick={() => setAssignTarget(null)}>Cancel</Button><Button intent="primary" onClick={() => { setAssignedPacks(v => ({ ...v, [assignTarget.id]: 'backend-pack@2.4.2' })); setAssignTarget(null); }}>Confirm assignment</Button></>}>
          <dl className="space-y-2 text-[13px]"><div className="flex gap-3"><dt className="w-32 text-[#475569]">Component</dt><dd><CopyableId value={assignTarget.id} /></dd></div><div className="flex gap-3"><dt className="w-32 text-[#475569]">Target</dt><dd>{assignTarget.repo} / <span className="font-mono">{assignTarget.path}</span></dd></div><div className="flex gap-3"><dt className="w-32 text-[#475569]">Exact version</dt><dd className="font-mono">backend-pack@2.4.2</dd></div></dl>
        </Dialog>}
      </div>
    );
  }

  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="max-w-5xl mx-auto space-y-5">
        <ProjectIdentity project={activeProject} onBack={() => onNavigate?.('projects')} />
        {blocker && <BlockerCard type="PROBE_UNREACHABLE" target={`${blocker.id} / ${blocker.name}`} reason="The configured repository probe failed. Review local credentials, then retry the named probe action." opened="09:39:12" actions={[]} />}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <section className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
            <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC] flex items-center justify-between"><h2 className="text-sm font-semibold">Repository onboarding</h2><FolderGit2 size={15} aria-hidden className="text-[#475569]" /></div>
            <div className="divide-y divide-[#ECEFF4]">{activeProject.repositories.map(repo => {
              const state = repo.state;
              return <div key={repo.id} className="px-5 py-4">
                <div className="flex items-start justify-between gap-3"><div><div className="flex items-center gap-2"><span className="font-medium">{repo.name}</span><StatusBadge state={state} entity="repository" />{state === 'PROBING' && <span className="text-[12px] text-[#1E40AF]">Probe running…</span>}</div><div className="flex gap-3 mt-1 text-[12px] text-[#475569]"><CopyableId value={repo.id} /><span>ref {repo.defaultRef}</span><span>observed {repo.lastProbe}</span></div></div>{state === 'BLOCKED' && <Button size="compact" intent="primary" loading={retrying === repo.id} disabled={isOffline} onClick={() => retryProbe(repo)}>Retry Probe</Button>}</div>
                <div className="flex items-center gap-1.5 mt-3" aria-label={`Onboarding state: ${state}`}>{(['REGISTERING', 'PROBING', state === 'ACTIVE' || state === 'BLOCKED' ? state : 'ACTIVE / BLOCKED'] as const).map((step, i) => <React.Fragment key={step}>{i > 0 && <div className="h-px w-6 bg-[#CDD5DF]" />}<Badge label={step} intent={step === state ? (state === 'ACTIVE' ? 'success' : state === 'BLOCKED' ? 'danger' : 'info') : i === 0 && state !== 'REGISTERING' ? 'success' : 'neutral'} /></React.Fragment>)}</div>
              </div>;
            })}{activeProject.repositories.length === 0 && <div className="px-5 py-10 text-center text-[13px] text-[#475569]">No repositories registered yet.</div>}</div>
            <div className="px-5 py-3 border-t border-[#CDD5DF] bg-[#F8FAFC]"><Button intent="secondary" size="compact" loading={registering} disabled={isOffline} onClick={() => setRegisterDialog(true)}>Register Repository</Button></div>
          </section>
          <section className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
            <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC] flex items-center justify-between"><h2 className="text-sm font-semibold">Discovered components</h2><Boxes size={15} aria-hidden className="text-[#475569]" /></div>
            <div className="divide-y divide-[#ECEFF4]">{activeProject.components.map(comp => <div key={comp.id} className="px-5 py-3 flex items-center gap-3"><div className="min-w-0 flex-1"><div className="font-mono text-[12px]">{comp.path}</div><div className="text-[12px] text-[#475569]">{comp.repo} · {comp.id}</div></div><StatusBadge state={comp.health} entity="repository" /></div>)}{activeProject.components.length === 0 && <div className="px-5 py-10 text-center text-[13px] text-[#475569]">Components appear after a repository probe succeeds.</div>}</div>
            <div className="px-5 py-3 border-t border-[#CDD5DF] bg-[#F8FAFC] flex justify-end"><Button intent="primary" size="compact" onClick={() => onNavigate?.('project-components')}>Open Components</Button></div>
          </section>
        </div>
        <div className="flex justify-end"><Button intent="primary" onClick={() => onNavigate?.('project-board')}><Columns3 size={14} aria-hidden /> Open Board</Button></div>
      </div>
      {registerDialog && (
        <Dialog
          title="Register Repository"
          description={`Add a local repository reference to ${activeProject.name}. Registration will run a probe before the repository becomes active.`}
          onClose={() => setRegisterDialog(false)}
          actions={<><Button intent="secondary" onClick={() => setRegisterDialog(false)}>Cancel</Button><Button intent="primary" disabled={!repositoryName.trim() || !defaultRef.trim()} onClick={registerRepository}>Register and Probe</Button></>}
        >
          <div className="space-y-4">
            <div className="flex flex-col gap-1"><label htmlFor="repository-name" className="text-[12px] font-medium">Repository name</label><input id="repository-name" value={repositoryName} onChange={event => setRepositoryName(event.target.value)} className="h-9 px-3 rounded-[6px] border border-[#CDD5DF] text-[13px]" placeholder="api-service" /></div>
            <div className="flex flex-col gap-1"><label htmlFor="repository-ref" className="text-[12px] font-medium">Default ref</label><input id="repository-ref" value={defaultRef} onChange={event => setDefaultRef(event.target.value)} className="h-9 px-3 rounded-[6px] border border-[#CDD5DF] text-[13px] font-mono" /></div>
          </div>
        </Dialog>
      )}
    </div>
  );
}
