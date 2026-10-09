import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import { TaskDetailScreen } from './TaskDetail';

// V11-06 — the Workspace tab's Diff shows the worktree's UNCOMMITTED changes (an approval gate has no commit to
// diff), and an approval card says how big that change is.

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return {
    ...actual,
    getWorkItem: vi.fn(), getTaskFamily: vi.fn(), getWorkItemProjectedDetail: vi.fn(), getRunDiagnostics: vi.fn(),
    getRunGraph: vi.fn(), getRunTimeline: vi.fn(), getRunDetail: vi.fn(), resolveApproval: vi.fn(),
    getWorkspaceSetState: vi.fn(), getWorkspaceDiff: vi.fn(), getWorkspaceRepositoryLog: vi.fn(), requestWorkspaceReconciliation: vi.fn(),
    listFamilyScopeExpansionRequests: vi.fn(),
  };
});
vi.mock('../api/session', () => ({
  withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }),
  getSessionToken: () => 'test-session-token',
}));
vi.mock('../api/workspaceinspection', async () => {
  const actual = await vi.importActual<typeof import('../api/workspaceinspection')>('../api/workspaceinspection');
  return { ...actual, fetchWorkspaceSource: vi.fn() };
});

function render(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

const FAMILY = { familyId: 'fam-1', projectId: 'proj-1', rootWorkItemId: 'wi-1', scopeVersion: 1, status: 'ACTIVE', version: 1 };

function repoFixture(overrides: Record<string, unknown> = {}) {
  return {
    repositoryWorkspaceId: 'rw-1', workspaceSetId: 'ws-1', repositoryId: 'contracts', generation: 1,
    state: 'READY', version: 5, branchRef: 'main', baseRevision: 'aaaa1111aaaa', currentRevision: 'aaaa1111aaaa',
    hasActiveWriteLease: false, validActions: [],
    ...overrides,
  };
}

function setState(repos = [repoFixture()]) {
  return {
    workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'READY', version: 2, hasBaseRevisionSet: true,
    repositoryWorkspaces: repos, validActions: [],
  } as never;
}

const EMPTY_COMMITTED = {
  baseRevision: {}, resultRevision: {}, files: [], patch: '', byteLimit: 1000, fileLimit: 100, filesTruncated: false, patchTruncated: false,
};

function b64(text: string) {
  return btoa(text);
}

const UNCOMMITTED = {
  baseRevision: {}, resultRevision: {}, uncommitted: true, totalFiles: 3, changeSetDigest: 'digest-1',
  byteLimit: 1000, fileLimit: 100, filesTruncated: false, patchTruncated: false,
  patch: b64('diff --git a/spec/openapi.json b/spec/openapi.json\n+++ b/spec/openapi.json\n@@ -0,0 +1,1 @@\n+{"openapi":"3.0"}\n'),
  files: [
    { path: 'spec/openapi.json', additions: 12, deletions: 0, binary: false, status: 'ADDED', untracked: true, sizeBytes: 400, contentHash: 'h1' },
    { path: 'README.md', additions: 3, deletions: 1, binary: false, status: 'MODIFIED', sizeBytes: 90, contentHash: 'h2' },
    { path: 'assets/logo.png', additions: 0, deletions: 0, binary: true, status: 'ADDED', untracked: true, sizeBytes: 4096, contentHash: 'h3' },
  ],
};

function mockDiff(workingTree: unknown, committed: unknown = EMPTY_COMMITTED) {
  vi.mocked(api.getWorkspaceDiff).mockImplementation((async (_p: string, _rw: string, opts: { query?: Record<string, string> }) => {
    if (opts?.query?.result === 'WORKING_TREE') {
      if (workingTree instanceof Error) throw workingTree;
      return workingTree;
    }
    return committed;
  }) as never);
}

function renderWorkspaceTab(onTabChange = vi.fn()) {
  return render(<TaskDetailScreen projectId="proj-1" projectName="platform-core" workItemId="wi-1" activeTab="task-workspace" onTabChange={onTabChange} />);
}

describe('Workspace tab — uncommitted changes (V11-06)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getTaskFamily).mockResolvedValue(FAMILY as never);
    vi.mocked(api.getWorkItem).mockResolvedValue({ workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Contract', status: 'ACTIVE', version: 4, contract: null } as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ card: { workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', title: 'Contract', isRoot: true, status: 'ACTIVE', blockerCount: 0, pendingScopeExpansionCount: 0, repositoryBadges: [] }, readiness: { workItemId: 'wi-1', status: 'ACTIVE', version: 3, ready: false }, freshness: { generation: 1, asOfJournalPosition: 1, status: 'LIVE' }, validActions: [] } as never);
    vi.mocked(api.listFamilyScopeExpansionRequests).mockResolvedValue({ items: [] } as never);
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue(setState());
  });

  it('shows the uncommitted changes by default when the worktree has some, instead of "No changes"', async () => {
    mockDiff(UNCOMMITTED);
    renderWorkspaceTab();

    expect(await screen.findByText('UNCOMMITTED', { selector: 'span[aria-hidden]' })).toBeInTheDocument();
    const list = screen.getByRole('list', { name: 'Uncommitted files' });
    expect(within(list).getByText('spec/openapi.json')).toBeInTheDocument();
    expect(screen.getByText('· 3 files changed')).toBeInTheDocument();
    expect(screen.getByText('+15')).toBeInTheDocument();
    expect(screen.queryByText(/No changes between the base and current revision/)).not.toBeInTheDocument();
    expect(within(list).getAllByText(/added · untracked/)).toHaveLength(2);
    // a binary file is a name and a size, never content
    const binaryRow = within(list).getByText('assets/logo.png').closest('li')!;
    expect(within(binaryRow).getByText('binary · 4096 bytes')).toBeInTheDocument();
    // the patch is rendered
    expect(screen.getByText('+{"openapi":"3.0"}')).toBeInTheDocument();
    // the request asked for the worktree on top of the current revision
    expect(api.getWorkspaceDiff).toHaveBeenCalledWith('proj-1', 'rw-1', expect.objectContaining({
      query: expect.objectContaining({ result: 'WORKING_TREE', base: 'aaaa1111aaaa', repositoryId: 'contracts', workspaceSetId: 'ws-1' }),
    }));
  });

  it('a clean worktree falls back to the committed diff and says so', async () => {
    mockDiff({ ...UNCOMMITTED, totalFiles: 0, files: [], patch: '' });
    renderWorkspaceTab();

    expect(await screen.findByText(/No committed changes between the base and current revision/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Uncommitted changes (0)' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Uncommitted changes (0)' }));
    expect(await screen.findByText('The worktree matches the current revision — nothing is uncommitted.')).toBeInTheDocument();
  });

  it('the committed view points at the uncommitted files when it is empty and the worktree is not', async () => {
    mockDiff(UNCOMMITTED);
    renderWorkspaceTab();
    await screen.findByRole('list', { name: 'Uncommitted files' });

    await userEvent.click(screen.getByRole('button', { name: 'Committed' }));
    expect(await screen.findByText(/The worktree has 3 uncommitted files/)).toBeInTheDocument();
  });

  it('while an attempt is writing the server refuses; the committed diff still shows, with the reason', async () => {
    const { ApiError } = await vi.importActual<typeof import('../api/generated')>('../api/generated');
    mockDiff(new ApiError(409, 'CONFLICT', 'an attempt is writing to this repository workspace, so its uncommitted state is in flux'));
    renderWorkspaceTab();

    expect(await screen.findByText(/Could not read the uncommitted changes: an attempt is writing/)).toBeInTheDocument();
    expect(screen.getByText(/No committed changes between the base and current revision/)).toBeInTheDocument();
  });

  it('has no automated accessibility violations', async () => {
    mockDiff(UNCOMMITTED);
    const { container } = renderWorkspaceTab();
    await screen.findByRole('list', { name: 'Uncommitted files' });
    await expectNoAxeViolations(container);
  });
});

describe('Approval card — uncommitted change summary (V11-06)', () => {
  const NODES = [{ key: 'start', type: 'START', outcomes: ['go'] }, { key: 'review', type: 'APPROVAL', outcomes: ['approved', 'rejected'] }, { key: 'end', type: 'END' }];
  const EDGES = [{ key: 'e1', from: 'start', outcome: 'go', to: 'review' }, { key: 'e2', from: 'review', outcome: 'approved', to: 'end' }];

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getTaskFamily).mockResolvedValue(FAMILY as never);
    vi.mocked(api.getWorkItem).mockResolvedValue({ workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Contract', status: 'ACTIVE', version: 4, contract: null } as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({
      card: { workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', title: 'Contract', isRoot: true, status: 'ACTIVE', blockerCount: 0, pendingScopeExpansionCount: 0, repositoryBadges: [], activeRunId: 'run-1' },
      readiness: { workItemId: 'wi-1', status: 'ACTIVE', version: 3, ready: false }, freshness: { generation: 1, asOfJournalPosition: 1, status: 'LIVE' }, validActions: [],
    } as never);
    vi.mocked(api.getRunDiagnostics).mockResolvedValue({
      runId: 'run-1', projectId: 'proj-1', workItemId: 'wi-1', workItemStatus: 'ACTIVE', runState: 'WAITING',
      blockers: [], orphanedAttempts: [], orphanedAttemptsTruncated: false, providers: [], isolation: [], repositoryWorkspaces: [], validActions: [],
    } as never);
    vi.mocked(api.getRunGraph).mockResolvedValue({
      runId: 'run-1', manifestRevision: 1, nodes: NODES, possibleEdges: EDGES,
      activations: [{ nodeRunId: 'nr-review', nodeKey: 'review', activationSequence: 1, iteration: 0, state: 'WAITING' }],
      freshness: { generation: 1, asOfJournalPosition: 1, status: 'LIVE' },
    } as never);
    vi.mocked(api.getRunTimeline).mockResolvedValue({ runId: 'run-1', entries: [], freshness: { generation: 1, asOfJournalPosition: 1, status: 'LIVE' } } as never);
    vi.mocked(api.getRunDetail).mockResolvedValue({
      approvalRequests: [{ approvalRequestId: 'appr-1', nodeRunId: 'nr-review', nodeKey: 'review', state: 'PENDING', version: 1, authorizedRoles: ['operator'] }],
    } as never);
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue(setState());
  });

  function renderGraph(onTabChange = vi.fn()) {
    return render(<TaskDetailScreen projectId="proj-1" projectName="platform-core" workItemId="wi-1" activeTab="task-graph" onTabChange={onTabChange} />);
  }

  it('says how many files changed and by how much, and opens the Workspace tab', async () => {
    mockDiff(UNCOMMITTED);
    const onTabChange = vi.fn();
    renderGraph(onTabChange);

    expect(await screen.findByText(/3 files changed, \+15 −1/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'View changes' }));
    expect(onTabChange).toHaveBeenCalledWith('task-workspace', { projectId: 'proj-1', taskId: 'wi-1' });
  });

  it('says so when there is nothing uncommitted, and gives no link', async () => {
    mockDiff({ ...UNCOMMITTED, totalFiles: 0, files: [], patch: '' });
    renderGraph();

    expect(await screen.findByText('No uncommitted changes in the worktree.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'View changes' })).not.toBeInTheDocument();
  });

  it('reports a read failure instead of pretending there is nothing to review', async () => {
    const { ApiError } = await vi.importActual<typeof import('../api/generated')>('../api/generated');
    mockDiff(new ApiError(409, 'CONFLICT', 'an attempt is writing to this repository workspace'));
    renderGraph();

    await waitFor(() => expect(screen.getByText(/Could not read the uncommitted changes: an attempt is writing/)).toBeInTheDocument());
    expect(screen.queryByText('No uncommitted changes in the worktree.')).not.toBeInTheDocument();
  });
});
