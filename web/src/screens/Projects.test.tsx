import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import { ProjectsScreen } from './Projects';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return {
    ...actual,
    projectsList: vi.fn(), projectsCreate: vi.fn(),
    projectRepositoriesList: vi.fn(), projectRepositoriesRegister: vi.fn(), repositoriesRetryProbe: vi.fn(),
    projectRepositoriesCheck: vi.fn(), repositoriesUpdate: vi.fn(), repositoriesDisable: vi.fn(), repositoriesDelete: vi.fn(), repositoriesOnboarding: vi.fn(),
    projectComponentsList: vi.fn(), componentPackAssignmentsList: vi.fn(), componentPackAssignmentsAssign: vi.fn(),
  };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }) }));

function render(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

const PROJECTS = { projects: [
  { id: 'proj-1', name: 'platform-core', status: 'ACTIVE', version: 1 },
  { id: 'proj-2', name: 'data-pipeline', status: 'ACTIVE', version: 1 },
] };

const REPOS = { repositories: [
  { id: 'repo-1', projectId: 'proj-1', name: 'core-api', remoteLocator: '/repos/core-api', defaultRef: 'main', status: 'ACTIVE', version: 2,
    validActions: [{ operationId: 'repositoriesDisable', targetVersion: 2 }] },
  { id: 'repo-2', projectId: 'proj-1', name: 'worker-service', remoteLocator: '/repos/worker', defaultRef: 'main', status: 'BLOCKED', lastProbeErrorCode: 'PROBE_UNREACHABLE', version: 3,
    validActions: ['repositoriesRetryProbe', 'repositoriesUpdate', 'repositoriesDisable', 'repositoriesDelete'].map(operationId => ({ operationId, targetVersion: 3 })) },
] };

// V11-12: a BLOCKED repository whose probe said what was wrong and what to do.
const BLOCKED_WITH_REASON = { repositories: [
  { id: 'repo-9', projectId: 'proj-1', name: 'bpm-gl', remoteLocator: 'D:/project/bpm-gl', defaultRef: 'main', status: 'BLOCKED', lastProbeErrorCode: 'INVALID_ARGUMENT', version: 3,
    lastProbeError: { failureKind: 'NOT_A_GIT_REPOSITORY', message: 'The folder D:/project/bpm-gl is not a Git repository yet.', hint: 'Run: git -C D:/project/bpm-gl init -b main' },
    validActions: ['repositoriesRetryProbe', 'repositoriesUpdate', 'repositoriesDisable', 'repositoriesDelete'].map(operationId => ({ operationId, targetVersion: 3 })) },
] };

describe('ProjectsScreen — list', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows a loading skeleton before the query resolves', () => {
    vi.mocked(api.projectsList).mockReturnValue(new Promise(() => {}));
    const { container } = render(<ProjectsScreen view="list" onSelectProject={vi.fn()} />);
    expect(container.querySelectorAll('[aria-hidden="true"]').length).toBeGreaterThan(0);
  });

  it('renders real projects with their real status', async () => {
    vi.mocked(api.projectsList).mockResolvedValue(PROJECTS as never);
    render(<ProjectsScreen view="list" onSelectProject={vi.fn()} />);
    expect(await screen.findByText('platform-core')).toBeInTheDocument();
    expect(screen.getByText('data-pipeline')).toBeInTheDocument();
    expect(screen.getByText('2 projects in this installation.')).toBeInTheDocument();
  });

  it('shows an empty state with no projects', async () => {
    vi.mocked(api.projectsList).mockResolvedValue({ projects: [] } as never);
    render(<ProjectsScreen view="list" onSelectProject={vi.fn()} />);
    expect(await screen.findByText('No projects yet')).toBeInTheDocument();
  });

  it('clicking a project calls onSelectProject with its real id', async () => {
    vi.mocked(api.projectsList).mockResolvedValue(PROJECTS as never);
    const onSelectProject = vi.fn();
    render(<ProjectsScreen view="list" onSelectProject={onSelectProject} />);
    await userEvent.click(await screen.findByText('platform-core'));
    expect(onSelectProject).toHaveBeenCalledWith('proj-1');
  });

  it('creating a project requires a name, then calls the real API and selects the new project', async () => {
    vi.mocked(api.projectsList).mockResolvedValue(PROJECTS as never);
    vi.mocked(api.projectsCreate).mockResolvedValue({ projectId: 'proj-new', name: 'new-svc', status: 'ACTIVE' } as never);
    const onSelectProject = vi.fn();
    render(<ProjectsScreen view="list" onSelectProject={onSelectProject} />);
    await screen.findByText('platform-core');

    await userEvent.click(screen.getByRole('button', { name: /New Project/ }));
    await userEvent.click(screen.getByRole('button', { name: 'Create' }));
    expect(await screen.findByText('required')).toBeInTheDocument();
    expect(api.projectsCreate).not.toHaveBeenCalled();

    await userEvent.type(screen.getByLabelText(/Project name/), 'new-svc');
    await userEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(onSelectProject).toHaveBeenCalledWith('proj-new'));
    expect(api.projectsCreate).toHaveBeenCalledWith({ name: 'new-svc' }, expect.anything());
  });

  it('has no automated accessibility violations once loaded', async () => {
    vi.mocked(api.projectsList).mockResolvedValue(PROJECTS as never);
    const { container } = render(<ProjectsScreen view="list" onSelectProject={vi.fn()} />);
    await screen.findByText('platform-core');
    await expectNoAxeViolations(container);
  });
});

describe('ProjectsScreen — overview', () => {
  beforeEach(() => vi.clearAllMocks());

  it('renders real repositories with a Retry Probe action only on the BLOCKED one', async () => {
    vi.mocked(api.projectsList).mockResolvedValue(PROJECTS as never);
    vi.mocked(api.projectRepositoriesList).mockResolvedValue(REPOS as never);
    render(<ProjectsScreen view="overview" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);

    expect(await screen.findByText('core-api')).toBeInTheDocument();
    expect(screen.getByText('worker-service')).toBeInTheDocument();
    expect(screen.getByText('PROBE_UNREACHABLE')).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Retry Probe' })).toHaveLength(1);
  });

  it('retrying a probe sends the real repository id and its version as If-Match', async () => {
    vi.mocked(api.projectsList).mockResolvedValue(PROJECTS as never);
    vi.mocked(api.projectRepositoriesList).mockResolvedValue(REPOS as never);
    vi.mocked(api.repositoriesRetryProbe).mockResolvedValue({} as never);
    render(<ProjectsScreen view="overview" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);

    await userEvent.click(await screen.findByRole('button', { name: 'Retry Probe' }));
    await waitFor(() => expect(api.repositoriesRetryProbe).toHaveBeenCalledWith('repo-2', {}, expect.objectContaining({ ifMatch: '"3"' })));
  });

  it('registering a repository requires every field and never lets the operator skip the repository ID', async () => {
    vi.mocked(api.projectsList).mockResolvedValue(PROJECTS as never);
    vi.mocked(api.projectRepositoriesList).mockResolvedValue({ repositories: [] } as never);
    render(<ProjectsScreen view="overview" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);

    await userEvent.click(await screen.findByRole('button', { name: 'Register Repository' }));
    await userEvent.click(screen.getByRole('button', { name: 'Register and Probe' }));
    expect(await screen.findAllByText('required')).not.toHaveLength(0);
    expect(api.projectRepositoriesRegister).not.toHaveBeenCalled();
  });

  it('registering a repository with real values calls the real API with the exact field names the backend expects', async () => {
    vi.mocked(api.projectsList).mockResolvedValue(PROJECTS as never);
    vi.mocked(api.projectRepositoriesList).mockResolvedValue({ repositories: [] } as never);
    vi.mocked(api.projectRepositoriesRegister).mockResolvedValue({ repositoryId: 'repo-new', projectId: 'proj-1', status: 'REGISTERING', probeJobId: 'job-1' } as never);
    render(<ProjectsScreen view="overview" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);

    await userEvent.click(await screen.findByRole('button', { name: 'Register Repository' }));
    await userEvent.type(screen.getByLabelText(/Repository ID/), 'repo-new');
    await userEvent.type(screen.getByLabelText(/^Name/), 'new-repo');
    await userEvent.type(screen.getByLabelText(/Local repository path/), '/repos/new-repo');
    await userEvent.clear(screen.getByLabelText(/Default ref/));
    await userEvent.type(screen.getByLabelText(/Default ref/), 'main');
    await userEvent.click(screen.getByRole('button', { name: 'Register and Probe' }));

    await waitFor(() => expect(api.projectRepositoriesRegister).toHaveBeenCalledWith(
      'proj-1',
      { repositoryId: 'repo-new', name: 'new-repo', remoteLocator: '/repos/new-repo', defaultRef: 'main' },
      expect.anything(),
    ));
    expect(await screen.findByText('Repository registered — probing now.')).toBeInTheDocument();
  });
});

describe('ProjectsScreen — components', () => {
  beforeEach(() => vi.clearAllMocks());

  it('renders real discovered components', async () => {
    vi.mocked(api.projectsList).mockResolvedValue(PROJECTS as never);
    vi.mocked(api.projectComponentsList).mockResolvedValue({
      components: [{ id: 'comp-1', projectId: 'proj-1', repositoryId: 'repo-1', name: 'api', path: 'src/api', kind: 'service', version: 1 }],
    } as never);
    render(<ProjectsScreen view="components" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);
    expect(await screen.findByText('api')).toBeInTheDocument();
    expect(screen.getByText('src/api')).toBeInTheDocument();
  });

  it('shows an empty state when nothing has been discovered yet', async () => {
    vi.mocked(api.projectsList).mockResolvedValue(PROJECTS as never);
    vi.mocked(api.projectComponentsList).mockResolvedValue({ components: [] } as never);
    render(<ProjectsScreen view="components" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);
    expect(await screen.findByText(/No components discovered yet/)).toBeInTheDocument();
  });

  it('"Assign Pack" opens the real assignment dialog for the exact component clicked', async () => {
    vi.mocked(api.projectsList).mockResolvedValue(PROJECTS as never);
    vi.mocked(api.projectComponentsList).mockResolvedValue({
      components: [{ id: 'comp-1', projectId: 'proj-1', repositoryId: 'repo-1', name: 'api', path: 'src/api', kind: 'service', version: 1 }],
    } as never);
    vi.mocked(api.componentPackAssignmentsList).mockResolvedValue({ componentId: 'comp-1', projectId: 'proj-1', assignments: [], effective: null } as never);
    render(<ProjectsScreen view="components" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);
    await screen.findByText('api');

    await userEvent.click(screen.getByRole('button', { name: 'Assign Pack' }));
    expect(await screen.findByRole('heading', { name: 'Assign Engineering Pack' })).toBeInTheDocument();
    expect(api.componentPackAssignmentsList).toHaveBeenCalledWith('comp-1', expect.anything());
  });
});

describe('ProjectsScreen — repository lifecycle (V11-12)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.projectsList).mockResolvedValue(PROJECTS as never);
  });

  it('a BLOCKED card says what is wrong and what to do, not just a code', async () => {
    vi.mocked(api.projectRepositoriesList).mockResolvedValue(BLOCKED_WITH_REASON as never);
    render(<ProjectsScreen view="overview" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);
    const note = await screen.findByTestId('repo-error-repo-9');
    expect(note).toHaveTextContent('NOT_A_GIT_REPOSITORY');
    expect(note).toHaveTextContent('is not a Git repository yet');
    expect(note).toHaveTextContent('git -C D:/project/bpm-gl init -b main');
  });

  it('the Git output is fetched only when asked for', async () => {
    vi.mocked(api.projectRepositoriesList).mockResolvedValue(BLOCKED_WITH_REASON as never);
    vi.mocked(api.repositoriesOnboarding).mockResolvedValue({
      repositoryId: 'repo-9', projectId: 'proj-1', status: 'BLOCKED', version: 3, attempts: [],
      lastProbeError: { failureKind: 'NOT_A_GIT_REPOSITORY', message: 'x', detail: 'fatal: not a git repository' },
    } as never);
    render(<ProjectsScreen view="overview" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);
    await screen.findByTestId('repo-error-repo-9');
    expect(api.repositoriesOnboarding).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole('button', { name: 'Load Git output' }));
    await userEvent.click(await screen.findByRole('button', { name: 'Show Git output' }));
    expect(await screen.findByTestId('failure-detail')).toHaveTextContent('fatal: not a git repository');
  });

  it('buttons follow the validActions the server sent', async () => {
    vi.mocked(api.projectRepositoriesList).mockResolvedValue(REPOS as never);
    render(<ProjectsScreen view="overview" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);
    await screen.findByText('worker-service');
    // BLOCKED: retry, edit, disable, delete.  ACTIVE: disable only.
    expect(screen.getByRole('button', { name: 'Edit worker-service' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete worker-service' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Disable worker-service' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Disable core-api' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Edit core-api' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Delete core-api' })).not.toBeInTheDocument();
  });

  it('registering checks the path when leaving the field and shows the problem before anything is registered', async () => {
    vi.mocked(api.projectRepositoriesList).mockResolvedValue({ repositories: [] } as never);
    vi.mocked(api.projectRepositoriesCheck).mockResolvedValue({
      ok: false, failureKind: 'PATH_NOT_FOUND', message: 'The folder /d/x does not exist.', hint: 'Use D:/x instead.',
    } as never);
    render(<ProjectsScreen view="overview" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Register Repository' }));
    await userEvent.type(screen.getByLabelText(/Local repository path/), '/d/x');
    await userEvent.tab();
    const failure = await screen.findByTestId('check-failure');
    expect(failure).toHaveTextContent('PATH_NOT_FOUND');
    expect(failure).toHaveTextContent('Use D:/x instead.');
    expect(api.projectRepositoriesCheck).toHaveBeenCalledWith('proj-1', expect.objectContaining({ query: { path: '/d/x', ref: 'main' } }));
    expect(api.projectRepositoriesRegister).not.toHaveBeenCalled();
  });

  it('Edit sends only what changed, with the version as If-Match', async () => {
    vi.mocked(api.projectRepositoriesList).mockResolvedValue(BLOCKED_WITH_REASON as never);
    vi.mocked(api.projectRepositoriesCheck).mockResolvedValue({ ok: true, canonicalPath: 'D:/work/bpm-gl' } as never);
    vi.mocked(api.repositoriesUpdate).mockResolvedValue({ repositoryId: 'repo-9', projectId: 'proj-1', status: 'PROBING', version: 4, probeJobId: 'job-1' } as never);
    render(<ProjectsScreen view="overview" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Edit bpm-gl' }));
    const path = screen.getByLabelText(/Local repository path/);
    await userEvent.clear(path);
    await userEvent.type(path, 'D:/work/bpm-gl');
    await userEvent.tab();
    expect(await screen.findByTestId('check-ok')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Save and Probe' }));
    await waitFor(() => expect(api.repositoriesUpdate).toHaveBeenCalledWith(
      'repo-9', { name: null, remoteLocator: 'D:/work/bpm-gl', defaultRef: 'main' }, expect.objectContaining({ ifMatch: '"3"' }),
    ));
    expect(await screen.findByText('bpm-gl updated — probing again.')).toBeInTheDocument();
  });

  it('Disable names what still uses the repository when the server refuses', async () => {
    vi.mocked(api.projectRepositoriesList).mockResolvedValue(REPOS as never);
    vi.mocked(api.repositoriesDisable).mockRejectedValue(new api.ApiError(409, 'CONFLICT', 'REPOSITORY_IN_USE: repository repo-1 is still used by 1 unfinished item(s)', [
      { field: 'repository', message: 'REPOSITORY_IN_USE' }, { field: 'items', message: 'WORKSPACE rw-1 — workspace READY' },
    ]));
    render(<ProjectsScreen view="overview" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Disable core-api' }));
    await userEvent.click(screen.getByRole('button', { name: 'Disable' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('REPOSITORY_IN_USE');
    expect(screen.getByTestId('holders')).toHaveTextContent('WORKSPACE rw-1');
    expect(api.repositoriesDisable).toHaveBeenCalledWith('repo-1', { reason: '' }, expect.objectContaining({ ifMatch: '"2"' }));
  });

  it('Delete needs a reason and warns that it is permanent', async () => {
    vi.mocked(api.projectRepositoriesList).mockResolvedValue(BLOCKED_WITH_REASON as never);
    vi.mocked(api.repositoriesDelete).mockResolvedValue({ repositoryId: 'repo-9', projectId: 'proj-1', name: 'bpm-gl' } as never);
    render(<ProjectsScreen view="overview" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);
    await userEvent.click(await screen.findByRole('button', { name: 'Delete bpm-gl' }));
    expect(screen.getByText(/never used again/)).toBeInTheDocument();
    const confirm = screen.getByRole('button', { name: 'Delete permanently' });
    expect(confirm).toBeDisabled();
    await userEvent.type(screen.getByLabelText(/^Reason/), 'wrong folder');
    await userEvent.click(confirm);
    await waitFor(() => expect(api.repositoriesDelete).toHaveBeenCalledWith('repo-9', { reason: 'wrong folder' }, expect.objectContaining({ ifMatch: '"3"' })));
    expect(await screen.findByText('bpm-gl deleted.')).toBeInTheDocument();
  });

  it('Show disabled asks the server for disabled repositories too', async () => {
    vi.mocked(api.projectRepositoriesList).mockResolvedValue(REPOS as never);
    render(<ProjectsScreen view="overview" projectId="proj-1" onSelectProject={vi.fn()} onNavigate={vi.fn()} />);
    await screen.findByText('core-api');
    await userEvent.click(screen.getByLabelText('Show disabled'));
    await waitFor(() => expect(api.projectRepositoriesList).toHaveBeenLastCalledWith('proj-1', expect.objectContaining({ query: { includeDisabled: 'true' } })));
  });
});
