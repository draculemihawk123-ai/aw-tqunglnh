import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  projectsCreate, projectsList, projectsRestore, projectRepositoriesList, projectRepositoriesRegister,
  repositoriesRetryProbe, projectComponentsList, ApiError,
} from '../api/generated';
import type {
  ComponentView, CreateProjectResult, ProjectView, RegisterRepositoryResult, RepositoryView,
} from '../api/catalog';
import { withSessionToken } from '../api/session';
import {
  Button, CopyableId, Dialog, EmptyState, InlineError, Skeleton, StatusBadge, TextField,
  ToastViewport, useToasts,
} from '../components/ui';
import { ArrowLeft, Boxes, ChevronRight, Columns3, FolderGit2, Plus } from '../components/icons';
import { AssignPackDialog } from './AssignPackDialog';
import { RepositoryReadinessDialog } from './RepositoryReadinessDialog';
import { ArchiveProjectDialog, RenameProjectDialog } from './ProjectDialogs';
import {
  CheckResult, DeleteRepositoryDialog, DisableRepositoryDialog, EditRepositoryDialog, RepositoryFailure, checkRepositoryPath,
} from './RepositoryDialogs';
import type { CheckOutcome } from './RepositoryDialogs';

export type { ProjectView, RepositoryView, ComponentView };

/**
 * A minimal, real-data-backed compatibility shape for the screens this
 * task does not touch (Kanban's board filter chips, Definitions' scope
 * label) — both were built against the prototype's old fake ProjectSummary
 * and only ever read id/name/repository-names off it. Rewriting either
 * screen onto the real catalog API in full is a separate, later task
 * (V7-09/V7-10 per docs/design/09-v7-alpha-ui.md); this keeps their inputs
 * real (App.tsx builds one from the same real queries this screen uses)
 * without expanding this task's own scope into rewriting them.
 */
export interface ProjectSummary {
  id: string;
  name: string;
  repositories: { id: string; name: string; state: string }[];
}

type Screen = 'list' | 'overview' | 'components';

interface Props {
  view: Screen;
  projectId?: string;
  onSelectProject: (projectId: string) => void;
  onNavigate?: (route: 'projects' | 'project-board' | 'project-components') => void;
  isOffline?: boolean;
}

/** Advisory: the server says which lifecycle operations a repository allows; the command re-checks. */
function allows(repo: RepositoryView, operationId: string): boolean {
  return (repo.validActions ?? []).some(a => a.operationId === operationId);
}

function apiErrorMessage(err: unknown): { code: string; message: string } {
  if (err instanceof ApiError) return { code: err.code, message: err.message };
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

/** A repository is only ever polled while it is still in a transient state — REGISTERING/PROBING resolve via a background worker job this browser tab has no other way to observe. */
function hasTransientRepository(repos: RepositoryView[] | undefined): boolean {
  return (repos ?? []).some(r => r.status === 'REGISTERING' || r.status === 'PROBING');
}

function useProjectsQuery(isOffline: boolean) {
  return useQuery({
    queryKey: ['projects'],
    // Shares the ['projects'] cache entry with App.tsx, which asks for archived projects too; the list view filters them out unless asked.
    queryFn: async () => (await projectsList(withSessionToken({ query: { includeArchived: 'true' } }))) as unknown as { projects: ProjectView[] },
    enabled: !isOffline,
  });
}

/** Advisory: the server says which lifecycle operations a project allows; the command re-checks. */
function projectAllows(project: ProjectView, operationId: string): boolean {
  return (project.validActions ?? []).some(a => a.operationId === operationId);
}

function useProjectRepositoriesQuery(projectId: string | undefined, isOffline: boolean, includeDisabled = false) {
  return useQuery({
    queryKey: ['projectRepositories', projectId, includeDisabled],
    queryFn: async () => (await projectRepositoriesList(projectId!, withSessionToken(includeDisabled ? { query: { includeDisabled: 'true' } } : {}))) as unknown as { repositories: RepositoryView[] },
    enabled: !isOffline && !!projectId,
    refetchInterval: query => (hasTransientRepository(query.state.data?.repositories) ? 1500 : false),
  });
}

function CreateProjectDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (result: CreateProjectResult) => void }) {
  const [name, setName] = useState('');
  const [submitted, setSubmitted] = useState(false);
  const mutation = useMutation({
    mutationFn: (n: string) => projectsCreate({ name: n }, withSessionToken()) as unknown as Promise<CreateProjectResult>,
  });
  const trimmed = name.trim();

  function handleCreate() {
    setSubmitted(true);
    if (!trimmed) return;
    mutation.mutate(trimmed, { onSuccess: onCreated });
  }

  return (
    <Dialog
      title="Create Project"
      description="A project groups repositories and WorkItems under a shared execution context."
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={mutation.isPending}>Cancel</Button>
          <Button intent="primary" loading={mutation.isPending} onClick={handleCreate}>Create</Button>
        </>
      }
    >
      <div className="space-y-3">
        <TextField label="Project name" required value={name} error={submitted && !trimmed ? 'required' : undefined}
          placeholder="my-project" onChange={setName} />
        {mutation.isError && <InlineError {...apiErrorMessage(mutation.error)} />}
      </div>
    </Dialog>
  );
}

function RegisterRepositoryDialog({ projectId, onClose, onRegistered }: {
  projectId: string; onClose: () => void; onRegistered: (result: RegisterRepositoryResult) => void;
}) {
  const [repositoryId, setRepositoryId] = useState('');
  const [name, setName] = useState('');
  const [remoteLocator, setRemoteLocator] = useState('');
  const [defaultRef, setDefaultRef] = useState('main');
  const [submitted, setSubmitted] = useState(false);
  // V11-12: the same probe a registration runs, without registering — a wrong path shows up here, before it is registered.
  const [outcome, setOutcome] = useState<CheckOutcome | null>(null);
  const [checking, setChecking] = useState(false);
  async function checkPath() {
    if (!remoteLocator.trim()) return;
    setChecking(true);
    try {
      setOutcome(await checkRepositoryPath(projectId, remoteLocator.trim(), defaultRef));
    } catch (err) {
      setOutcome({ ok: false, message: apiErrorMessage(err).message });
    } finally {
      setChecking(false);
    }
  }
  const mutation = useMutation({
    mutationFn: () => projectRepositoriesRegister(projectId, {
      repositoryId: repositoryId.trim(), name: name.trim(), remoteLocator: remoteLocator.trim(), defaultRef: defaultRef.trim(),
    }, withSessionToken()) as unknown as Promise<RegisterRepositoryResult>,
  });
  const errors: Record<string, boolean> = {
    repositoryId: !repositoryId.trim(), name: !name.trim(), remoteLocator: !remoteLocator.trim(), defaultRef: !defaultRef.trim(),
  };
  const valid = !Object.values(errors).some(Boolean);

  function handleRegister() {
    setSubmitted(true);
    if (!valid) return;
    mutation.mutate(undefined, { onSuccess: onRegistered });
  }

  return (
    <Dialog
      title="Register Repository"
      description="Add a repository already checked out on this machine. Registering starts a probe before the repository becomes ACTIVE."
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={mutation.isPending}>Cancel</Button>
          <Button intent="primary" loading={mutation.isPending} onClick={handleRegister}>Register and Probe</Button>
        </>
      }
    >
      <div className="space-y-3">
        <TextField label="Repository ID" required value={repositoryId} error={submitted && errors.repositoryId ? 'required' : undefined}
          helper="a stable identity you choose — never inferred from the path or name below" mono
          placeholder="repo-core-api" onChange={setRepositoryId} />
        <TextField label="Name" required value={name} error={submitted && errors.name ? 'required' : undefined}
          placeholder="core-api" onChange={setName} />
        <TextField label="Local repository path" required mono value={remoteLocator} error={submitted && errors.remoteLocator ? 'required' : undefined}
          helper="canonical absolute local path to the checked-out repository; must not live under a managed workspace root; aw checks it when you leave this field"
          placeholder="D:\repos\core-api" onChange={v => { setRemoteLocator(v); setOutcome(null); }} onBlur={checkPath} />
        <TextField label="Default ref" required value={defaultRef} error={submitted && errors.defaultRef ? 'required' : undefined}
          mono onChange={v => { setDefaultRef(v); setOutcome(null); }} onBlur={checkPath} />
        {/* Fixed height: the check answers while the operator is already moving to the button, and a dialog that grows under the pointer
            makes the click miss (found on a slow CI runner). */}
        <div className="h-28 overflow-y-auto" aria-live="polite">
          {checking && <p className="text-[12px] text-[#475569]" role="status">Checking the path…</p>}
          {outcome && <CheckResult outcome={outcome} />}
        </div>
        {mutation.isError && <InlineError {...apiErrorMessage(mutation.error)} />}
      </div>
    </Dialog>
  );
}

function ProjectIdentity({ project, repoCount, componentCount, blockerCount, onBack, onRestore }: {
  project: ProjectView; repoCount: number; componentCount: number; blockerCount: number; onBack?: () => void; onRestore?: () => void;
}) {
  return (
    <div>
      {onBack && (
        <button onClick={onBack} className="inline-flex items-center gap-1 text-[12px] text-[#475569] hover:text-[#3659E3] transition-colors">
          <ArrowLeft size={13} aria-hidden /> Projects
        </button>
      )}
      <div className="flex items-center gap-3 mt-2">
        <h1 className="text-xl font-semibold text-[#172033]">{project.name}</h1>
        <StatusBadge state={project.status} entity="repository" />
      </div>
      <div className="flex items-center gap-3 mt-1 text-[12px] text-[#475569]">
        <CopyableId value={project.id} />
        <span>{repoCount} repositories · {componentCount} components · {blockerCount} {blockerCount === 1 ? 'blocker' : 'blockers'}</span>
      </div>
      {project.status === 'ARCHIVED' && (
        <div role="status" data-testid="archived-banner" className="mt-3 flex items-center justify-between gap-3 rounded-[8px] border border-[#FCD34D] bg-[#FEF3C7] px-3 py-2 text-[13px] text-[#92400E]">
          <span>This project is archived and read-only. Evidence and history stay readable; restore it to make changes.</span>
          {onRestore && <Button size="compact" intent="secondary" onClick={onRestore}>Restore</Button>}
        </div>
      )}
    </div>
  );
}

export function ProjectsScreen({ view, projectId, onSelectProject, onNavigate, isOffline = false }: Props) {
  const [createDialog, setCreateDialog] = useState(false);
  const [registerDialog, setRegisterDialog] = useState(false);
  const [retryingId, setRetryingId] = useState<string | null>(null);
  const [assignPackTarget, setAssignPackTarget] = useState<ComponentView | null>(null);
  const [readinessTarget, setReadinessTarget] = useState<RepositoryView | null>(null);
  const [editTarget, setEditTarget] = useState<RepositoryView | null>(null);
  const [disableTarget, setDisableTarget] = useState<RepositoryView | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<RepositoryView | null>(null);
  const [showDisabled, setShowDisabled] = useState(false);
  const [showArchived, setShowArchived] = useState(false);
  const [renameTarget, setRenameTarget] = useState<ProjectView | null>(null);
  const [archiveTarget, setArchiveTarget] = useState<ProjectView | null>(null);
  const { toasts, show, dismiss } = useToasts();
  const queryClient = useQueryClient();

  const projectsQuery = useProjectsQuery(isOffline);
  const allProjects = projectsQuery.data?.projects ?? [];
  const projects = showArchived ? allProjects : allProjects.filter(p => p.status !== 'ARCHIVED');
  const project = allProjects.find(p => p.id === projectId);
  // An ARCHIVED project is read-only: every writing control below is disabled and the header says so.
  const readOnly = project?.status === 'ARCHIVED';

  const reposQuery = useProjectRepositoriesQuery(projectId, isOffline, showDisabled);
  const repos = reposQuery.data?.repositories ?? [];
  const blockerCount = repos.filter(r => r.status === 'BLOCKED').length;

  const componentsQuery = useQuery({
    queryKey: ['projectComponents', projectId],
    queryFn: async () => (await projectComponentsList(projectId!, withSessionToken())) as unknown as { components: ComponentView[] },
    enabled: !isOffline && !!projectId && view === 'components',
  });
  const components = componentsQuery.data?.components ?? [];

  const retryMutation = useMutation({
    mutationFn: (repo: RepositoryView) =>
      repositoriesRetryProbe(repo.id, {}, withSessionToken({ ifMatch: `"${repo.version}"` })) as unknown as Promise<unknown>,
  });

  const restoreMutation = useMutation({
    mutationFn: (target: ProjectView) => projectsRestore(target.id, {}, withSessionToken({ ifMatch: `"${target.version}"` })),
  });

  function restoreProject(target: ProjectView) {
    restoreMutation.mutate(target, {
      onSuccess: () => {
        queryClient.invalidateQueries({ queryKey: ['projects'] });
        show({ intent: 'success', message: `${target.name} restored.` });
      },
      onError: err => show({ intent: 'danger', message: apiErrorMessage(err).message, duration: 0 }),
    });
  }

  function refreshProjects(message: string) {
    queryClient.invalidateQueries({ queryKey: ['projects'] });
    show({ intent: 'success', message });
  }

  function refreshRepositories() {
    queryClient.invalidateQueries({ queryKey: ['projectRepositories', projectId] });
  }

  function retryProbe(repo: RepositoryView) {
    if (isOffline) return;
    setRetryingId(repo.id);
    retryMutation.mutate(repo, {
      onSuccess: () => {
        setRetryingId(null);
        queryClient.invalidateQueries({ queryKey: ['projectRepositories', projectId] });
      },
      onError: err => {
        setRetryingId(null);
        show({ intent: 'danger', message: apiErrorMessage(err).message, duration: 0 });
      },
    });
  }

  if (view === 'list') {
    return (
      <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
        <div className="max-w-5xl mx-auto space-y-6">
          <div className="flex items-center justify-between">
            <div>
              <h1 className="text-xl font-semibold text-[#172033]">Projects</h1>
              <p className="text-[13px] text-[#475569] mt-0.5">{projects.length} projects in this installation.</p>
              <label className="mt-1 flex items-center gap-2 text-[12px] text-[#475569]">
                <input type="checkbox" checked={showArchived} onChange={e => setShowArchived(e.target.checked)} />
                Show archived
              </label>
            </div>
            <Button intent="primary" onClick={() => setCreateDialog(true)} icon={<Plus size={14} aria-hidden />} disabled={isOffline} title={isOffline ? 'Reconnect to create a project' : undefined}>New Project</Button>
          </div>

          {projectsQuery.isPending && (
            <div className="space-y-2" aria-hidden><Skeleton className="h-20 w-full" /><Skeleton className="h-20 w-full" /></div>
          )}
          {projectsQuery.isError && <InlineError {...apiErrorMessage(projectsQuery.error)} onRetry={() => projectsQuery.refetch()} />}

          {!projectsQuery.isPending && !projectsQuery.isError && projects.length === 0 && (
            <EmptyState title="No projects yet" description="Create a project to start registering repositories and running WorkItems." />
          )}

          <div className="grid grid-cols-1 gap-3">
            {projects.map(proj => (
              <div key={proj.id} className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow hover:border-[#AAB4C3] transition-colors flex items-stretch">
                <button onClick={() => onSelectProject(proj.id)} className="flex-1 min-w-0 p-5 text-left group">
                  <div className="flex items-start justify-between gap-4">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-3 flex-wrap">
                        <span className="text-base font-semibold text-[#172033] group-hover:text-[#3659E3]">{proj.name}</span>
                        <StatusBadge state={proj.status} entity="repository" />
                      </div>
                      <div className="flex items-center gap-4 mt-2 text-[12px] text-[#475569]">
                        <span className="font-mono">{proj.id}</span>
                      </div>
                    </div>
                    <ChevronRight size={16} className="text-[#475569] group-hover:text-[#3659E3]" aria-hidden />
                  </div>
                </button>
                <div className="flex items-center gap-2 pr-4">
                  {projectAllows(proj, 'projectsRename') && (
                    <Button size="compact" intent="quiet" disabled={isOffline} aria-label={`Rename ${proj.name}`} onClick={() => setRenameTarget(proj)}>Rename</Button>
                  )}
                  {projectAllows(proj, 'projectsArchive') && (
                    <Button size="compact" intent="quiet" disabled={isOffline} aria-label={`Archive ${proj.name}`} onClick={() => setArchiveTarget(proj)}>Archive</Button>
                  )}
                  {projectAllows(proj, 'projectsRestore') && (
                    <Button size="compact" intent="secondary" disabled={isOffline} loading={restoreMutation.isPending && restoreMutation.variables?.id === proj.id}
                      aria-label={`Restore ${proj.name}`} onClick={() => restoreProject(proj)}>Restore</Button>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
        {renameTarget && (
          <RenameProjectDialog project={renameTarget} onClose={() => setRenameTarget(null)}
            onDone={message => { setRenameTarget(null); refreshProjects(message); }} />
        )}
        {archiveTarget && (
          <ArchiveProjectDialog project={archiveTarget} onClose={() => setArchiveTarget(null)}
            onDone={message => { setArchiveTarget(null); refreshProjects(message); }} />
        )}
        {createDialog && (
          <CreateProjectDialog
            onClose={() => setCreateDialog(false)}
            onCreated={result => {
              // A plain invalidateQueries here would only SCHEDULE a
              // refetch — navigating immediately after would race it: the
              // URL changes to the new project before the fetch resolves,
              // and App.tsx's own route guard (reading this SAME ['projects']
              // cache entry) would see the still-stale list, conclude the
              // new project does not exist yet, and bounce straight back to
              // /projects. Writing the real, already-known result into the
              // cache directly is synchronous — no race, and no wasted
              // network round-trip before the navigation this dialog is
              // about to trigger.
              queryClient.setQueryData(['projects'], (current: { projects: ProjectView[] } | undefined) => ({
                projects: [...(current?.projects ?? []), { id: result.projectId, name: result.name, status: result.status, version: 1 }],
              }));
              setCreateDialog(false);
              onSelectProject(result.projectId);
            }}
          />
        )}
        <ToastViewport toasts={toasts} onDismiss={dismiss} />
      </div>
    );
  }

  if (!project) {
    return (
      <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
        {projectsQuery.isPending
          ? <div className="max-w-5xl mx-auto space-y-2" aria-hidden><Skeleton className="h-20 w-full" /></div>
          : <EmptyState title="Project not found" description="This project could not be loaded." action={{ label: 'Back to Projects', onClick: () => onNavigate?.('projects') }} />}
      </div>
    );
  }

  if (view === 'components') {
    return (
      <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
        <div className="max-w-5xl mx-auto space-y-5">
          <ProjectIdentity project={project} repoCount={repos.length} componentCount={components.length} blockerCount={blockerCount} onBack={() => onNavigate?.('projects')} onRestore={() => restoreProject(project)} />
          <div className="flex items-center justify-between">
            <div><h2 className="text-lg font-semibold text-[#172033]">Components</h2><p className="text-[13px] text-[#475569]">Component identities discovered by a repository probe.</p></div>
            <Button intent="secondary" size="compact" onClick={() => onNavigate?.('project-board')}><Columns3 size={13} aria-hidden /> Open Board</Button>
          </div>
          <div className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
            {componentsQuery.isPending ? (
              <div className="p-5 space-y-2" aria-hidden><Skeleton className="h-10 w-full" /><Skeleton className="h-10 w-full" /></div>
            ) : componentsQuery.isError ? (
              <div className="p-5"><InlineError {...apiErrorMessage(componentsQuery.error)} onRetry={() => componentsQuery.refetch()} /></div>
            ) : (
              <table className="w-full text-[13px]">
                <thead className="bg-[#F8FAFC] border-b border-[#CDD5DF]"><tr>
                  <th className="text-left px-5 py-3 text-[12px]">Component / ID</th><th className="text-left px-5 py-3 text-[12px]">Repository / path</th><th className="text-left px-5 py-3 text-[12px]">Kind</th><th aria-label="Actions" />
                </tr></thead>
                <tbody className="divide-y divide-[#ECEFF4]">{components.map(comp => (
                  <tr key={comp.id} className="hover:bg-[#FAFBFC]">
                    <td className="px-5 py-4"><div className="font-medium">{comp.name}</div><CopyableId value={comp.id} /></td>
                    <td className="px-5 py-4"><div className="font-mono text-[12px] text-[#475569]">{comp.path}</div><CopyableId value={comp.repositoryId} /></td>
                    <td className="px-5 py-4">{comp.kind}</td>
                    <td className="px-5 py-4 text-right"><Button size="compact" intent="quiet" disabled={isOffline || readOnly} onClick={() => setAssignPackTarget(comp)}>Assign Pack</Button></td>
                  </tr>
                ))}{components.length === 0 && <tr><td colSpan={4} className="px-5 py-10 text-center text-[13px] text-[#475569]">No components discovered yet. Register and probe a repository first.</td></tr>}</tbody>
              </table>
            )}
          </div>
        </div>
        {assignPackTarget && (
          <AssignPackDialog
            component={assignPackTarget}
            onClose={() => setAssignPackTarget(null)}
            onAssigned={message => show({ intent: 'success', message })}
          />
        )}
        <ToastViewport toasts={toasts} onDismiss={dismiss} />
      </div>
    );
  }

  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="max-w-5xl mx-auto space-y-5">
        <ProjectIdentity project={project} repoCount={repos.length} componentCount={components.length} blockerCount={blockerCount} onBack={() => onNavigate?.('projects')} onRestore={() => restoreProject(project)} />
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <section className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
            <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC] flex items-center justify-between"><h2 className="text-sm font-semibold">Repository onboarding</h2><FolderGit2 size={15} aria-hidden className="text-[#475569]" /></div>
            {reposQuery.isPending ? (
              <div className="p-5 space-y-2" aria-hidden><Skeleton className="h-14 w-full" /><Skeleton className="h-14 w-full" /></div>
            ) : reposQuery.isError ? (
              <div className="p-5"><InlineError {...apiErrorMessage(reposQuery.error)} onRetry={() => reposQuery.refetch()} /></div>
            ) : (
              <div className="divide-y divide-[#ECEFF4]">
                {repos.map(repo => (
                  <div key={repo.id} className="px-5 py-4">
                    <div className="flex items-start justify-between gap-3">
                      <div>
                        <div className="flex items-center gap-2">
                          <span className="font-medium">{repo.name}</span>
                          <StatusBadge state={repo.status} entity="repository" />
                        </div>
                        <div className="flex gap-3 mt-1 text-[12px] text-[#475569]"><CopyableId value={repo.id} /><span>ref {repo.defaultRef}</span></div>
                        {repo.lastProbeError ? (
                          <RepositoryFailure repository={repo} />
                        ) : repo.lastProbeErrorCode && (
                          <p className="text-xs text-[#92400E] mt-1 bg-[#FEF3C7] border border-[#FCD34D] rounded-[4px] px-2 py-1 font-mono">{repo.lastProbeErrorCode}</p>
                        )}
                      </div>
                      <div className="flex items-center gap-2">
                        <Button size="compact" intent="quiet" disabled={isOffline} aria-label={`Readiness of ${repo.name}`} onClick={() => setReadinessTarget(repo)}>Readiness</Button>
                        {allows(repo, 'repositoriesRetryProbe') && (
                          <Button size="compact" intent="primary" loading={retryingId === repo.id} disabled={isOffline || readOnly} onClick={() => retryProbe(repo)}>Retry Probe</Button>
                        )}
                        {allows(repo, 'repositoriesUpdate') && (
                          <Button size="compact" intent="secondary" disabled={isOffline || readOnly} aria-label={`Edit ${repo.name}`} onClick={() => setEditTarget(repo)}>Edit</Button>
                        )}
                        {allows(repo, 'repositoriesDisable') && (
                          <Button size="compact" intent="quiet" disabled={isOffline || readOnly} aria-label={`Disable ${repo.name}`} onClick={() => setDisableTarget(repo)}>Disable</Button>
                        )}
                        {allows(repo, 'repositoriesDelete') && (
                          <Button size="compact" intent="quiet" disabled={isOffline || readOnly} aria-label={`Delete ${repo.name}`} onClick={() => setDeleteTarget(repo)}>Delete</Button>
                        )}
                      </div>
                    </div>
                  </div>
                ))}
                {repos.length === 0 && <div className="px-5 py-10 text-center text-[13px] text-[#475569]">No repositories registered yet.</div>}
              </div>
            )}
            <div className="px-5 py-3 border-t border-[#CDD5DF] bg-[#F8FAFC] flex items-center justify-between gap-3">
              <Button intent="secondary" size="compact" disabled={isOffline || readOnly} title={readOnly ? 'Archived projects are read-only' : undefined} onClick={() => setRegisterDialog(true)}>Register Repository</Button>
              <label className="flex items-center gap-2 text-[12px] text-[#475569]">
                <input type="checkbox" checked={showDisabled} onChange={e => setShowDisabled(e.target.checked)} />
                Show disabled
              </label>
            </div>
          </section>
          <section className="bg-white rounded-[12px] border border-[#CDD5DF] island-shadow overflow-hidden">
            <div className="px-5 py-3 border-b border-[#CDD5DF] bg-[#F8FAFC] flex items-center justify-between"><h2 className="text-sm font-semibold">Discovered components</h2><Boxes size={15} aria-hidden className="text-[#475569]" /></div>
            <div className="px-5 py-10 text-center text-[13px] text-[#475569]">Open Components to view discovered component identities.</div>
            <div className="px-5 py-3 border-t border-[#CDD5DF] bg-[#F8FAFC] flex justify-end"><Button intent="primary" size="compact" onClick={() => onNavigate?.('project-components')}>Open Components</Button></div>
          </section>
        </div>
        <div className="flex justify-end"><Button intent="primary" onClick={() => onNavigate?.('project-board')}><Columns3 size={14} aria-hidden /> Open Board</Button></div>
      </div>
      {readinessTarget && (
        <RepositoryReadinessDialog
          repository={readinessTarget}
          onClose={() => setReadinessTarget(null)}
          onDone={message => show({ intent: 'success', message })}
        />
      )}
      {editTarget && (
        <EditRepositoryDialog repository={editTarget} onClose={() => setEditTarget(null)}
          onDone={message => { setEditTarget(null); refreshRepositories(); show({ intent: 'success', message }); }} />
      )}
      {disableTarget && (
        <DisableRepositoryDialog repository={disableTarget} onClose={() => setDisableTarget(null)}
          onDone={message => { setDisableTarget(null); refreshRepositories(); show({ intent: 'success', message }); }} />
      )}
      {deleteTarget && (
        <DeleteRepositoryDialog repository={deleteTarget} onClose={() => setDeleteTarget(null)}
          onDone={message => { setDeleteTarget(null); refreshRepositories(); show({ intent: 'success', message }); }} />
      )}
      {registerDialog && (
        <RegisterRepositoryDialog
          projectId={project.id}
          onClose={() => setRegisterDialog(false)}
          onRegistered={() => {
            setRegisterDialog(false);
            queryClient.invalidateQueries({ queryKey: ['projectRepositories', project.id] });
            show({ intent: 'success', message: 'Repository registered — probing now.' });
          }}
        />
      )}
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </div>
  );
}
