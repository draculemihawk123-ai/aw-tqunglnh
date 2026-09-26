import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import * as workspaceinspection from '../api/workspaceinspection';
import { TaskDetailScreen } from './TaskDetail';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return {
    ...actual,
    getWorkItem: vi.fn(), getTaskFamily: vi.fn(), getWorkItemProjectedDetail: vi.fn(), getRunDiagnostics: vi.fn(),
    cancelRun: vi.fn(), cancelWorkItem: vi.fn(), resolveWorkItemBlocker: vi.fn(),
    getRunGraph: vi.fn(), getRunTimeline: vi.fn(), retryBlockedActivation: vi.fn(),
    getWorkspaceSetState: vi.fn(), getWorkspaceDiff: vi.fn(), getWorkspaceRepositoryLog: vi.fn(), requestWorkspaceReconciliation: vi.fn(),
    listReleaseSetsForFamily: vi.fn(), createReleaseSet: vi.fn(), sealReleaseSet: vi.fn(), abandonReleaseSet: vi.fn(),
    requestReleaseSetLocalCommit: vi.fn(), getReleaseSetLocalCommitStatus: vi.fn(), getReleaseSet: vi.fn(),
    getRepositoryWorkspaceState: vi.fn(), requestWorkspaceSetRelease: vi.fn(),
    listEvidence: vi.fn(), listArtifacts: vi.fn(),
  };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }) }));
vi.mock('../api/workspaceinspection', async () => {
  const actual = await vi.importActual<typeof import('../api/workspaceinspection')>('../api/workspaceinspection');
  return { ...actual, fetchWorkspaceSource: vi.fn() };
});
vi.mock('../api/evidence', async () => {
  const actual = await vi.importActual<typeof import('../api/evidence')>('../api/evidence');
  return { ...actual, fetchArtifactContent: vi.fn() };
});

function render(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

const FAMILY = { familyId: 'fam-1', projectId: 'proj-1', rootWorkItemId: 'wi-1', scopeVersion: 1, status: 'ACTIVE', version: 1 };

const CARD = {
  workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', title: 'Add distributed tracing',
  isRoot: true, status: 'ACTIVE', blockerCount: 0, pendingScopeExpansionCount: 0,
  repositoryBadges: [{ repositoryId: 'core-api', state: 'ACTIVE' }],
} as const;

function cardDetail(overrides: Partial<typeof CARD> = {}) {
  return {
    card: { ...CARD, ...overrides },
    readiness: { workItemId: 'wi-1', status: 'ACTIVE', version: 3, ready: false },
    freshness: { generation: 1, asOfJournalPosition: 5, status: 'LIVE' },
    validActions: [],
  };
}

function renderScreen() {
  return render(<TaskDetailScreen projectId="proj-1" projectName="platform-core" workItemId="wi-1" activeTab="task-overview" onTabChange={vi.fn()} />);
}

describe('TaskDetailScreen (V7-11)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getTaskFamily).mockResolvedValue(FAMILY as never);
  });

  it('active fixture: a running run offers Cancel Run and Cancel WorkItem, no blocker banner', async () => {
    vi.mocked(api.getWorkItem).mockResolvedValue({
      workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Add distributed tracing',
      status: 'ACTIVE', version: 4, contract: null,
    } as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ ...cardDetail({ activeRunId: 'run-1' } as never) } as never);
    vi.mocked(api.getRunDiagnostics).mockResolvedValue({
      runId: 'run-1', projectId: 'proj-1', workItemId: 'wi-1', workItemStatus: 'ACTIVE', runState: 'RUNNING',
      blockers: [], orphanedAttempts: [], orphanedAttemptsTruncated: false, providers: [], isolation: [], repositoryWorkspaces: [],
      validActions: [{ operationId: 'cancelRun', scopeKind: 'PROJECT', targetVersion: 0 }, { operationId: 'cancelWorkItem', scopeKind: 'PROJECT', targetVersion: 0 }],
    } as never);
    renderScreen();

    expect(await screen.findByRole('button', { name: 'Cancel Run' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel WorkItem' })).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('blocked fixture: an open non-admission blocker shows a Resolve action, and resolving it dispatches with fresh confirmation', async () => {
    vi.mocked(api.getWorkItem).mockResolvedValue({
      workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Add distributed tracing',
      status: 'BLOCKED', version: 4, contract: null,
    } as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ ...cardDetail({ activeRunId: 'run-1', status: 'BLOCKED' } as never) } as never);
    const diag = {
      runId: 'run-1', projectId: 'proj-1', workItemId: 'wi-1', workItemStatus: 'BLOCKED', runState: 'WAITING',
      blockers: [{
        blockerId: 'blk-1', type: 'COMPLETION_POLICY_FAILED', state: 'OPEN', reason: 'A gate reported FAIL',
        openedAt: '2026-09-26T00:00:00Z', version: 2, admissionReason: false,
        validActions: [{ operationId: 'resolveWorkItemBlocker', scopeKind: 'PROJECT', targetVersion: 2 }],
      }],
      orphanedAttempts: [], orphanedAttemptsTruncated: false, providers: [], isolation: [], repositoryWorkspaces: [],
      validActions: [{ operationId: 'cancelRun', scopeKind: 'PROJECT', targetVersion: 0 }],
    };
    vi.mocked(api.getRunDiagnostics).mockResolvedValue(diag as never);
    vi.mocked(api.resolveWorkItemBlocker).mockResolvedValue({ blockerId: 'blk-1', alreadyResolved: false, state: 'RESOLVED', workItemUnblocked: true, workItemStatus: 'READY' } as never);
    renderScreen();

    expect(await screen.findByText('A gate reported FAIL')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Resolve' }));
    await userEvent.type(screen.getByLabelText(/Reason/), 'fixed the underlying test');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm' }));

    await waitFor(() => expect(api.resolveWorkItemBlocker).toHaveBeenCalledWith(
      'blk-1', { mode: 'RESOLVED', reason: 'fixed the underlying test', policyGrantRef: undefined }, expect.objectContaining({ token: 'test-session-token' }),
    ));
    expect(await screen.findByText(/resolved/)).toBeInTheDocument();
  });

  it('an admission-reason blocker shows summary + a Graph & Timeline deep link only, never a resolve or retry button', async () => {
    vi.mocked(api.getWorkItem).mockResolvedValue({
      workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Add distributed tracing',
      status: 'BLOCKED', version: 4, contract: null,
    } as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ ...cardDetail({ activeRunId: 'run-1', status: 'BLOCKED' } as never) } as never);
    vi.mocked(api.getRunDiagnostics).mockResolvedValue({
      runId: 'run-1', projectId: 'proj-1', workItemId: 'wi-1', workItemStatus: 'BLOCKED', runState: 'WAITING',
      blockers: [{
        blockerId: 'blk-2', type: 'ADAPTER_BUILD_DRIFT', state: 'OPEN', reason: 'Pinned adapter build drifted',
        sourceNodeRunId: 'nr-1', openedAt: '2026-09-26T00:00:00Z', version: 1, admissionReason: true, sourceNodeRunVersion: 3,
        validActions: [{ operationId: 'retryBlockedActivation', scopeKind: 'PROJECT', targetVersion: 3 }],
      }],
      orphanedAttempts: [], orphanedAttemptsTruncated: false, providers: [], isolation: [], repositoryWorkspaces: [],
      validActions: [],
    } as never);
    const onTabChange = vi.fn();
    render(<TaskDetailScreen projectId="proj-1" projectName="platform-core" workItemId="wi-1" activeTab="task-overview" onTabChange={onTabChange} />);

    expect(await screen.findByText('Pinned adapter build drifted')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Resolve' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /retry/i })).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'View in Graph & Timeline' }));
    expect(onTabChange).toHaveBeenCalledWith('task-graph', { projectId: 'proj-1', taskId: 'wi-1' });
  });

  it('done fixture: a terminal WorkItem with a terminal run offers neither Cancel Run nor Cancel WorkItem', async () => {
    vi.mocked(api.getWorkItem).mockResolvedValue({
      workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Add distributed tracing',
      status: 'DONE', version: 9, contract: null,
    } as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ ...cardDetail({ activeRunId: 'run-1', status: 'DONE' } as never) } as never);
    vi.mocked(api.getRunDiagnostics).mockResolvedValue({
      runId: 'run-1', projectId: 'proj-1', workItemId: 'wi-1', workItemStatus: 'DONE', runState: 'SUCCEEDED',
      blockers: [], orphanedAttempts: [], orphanedAttemptsTruncated: false, providers: [], isolation: [], repositoryWorkspaces: [],
      validActions: [],
    } as never);
    renderScreen();

    await screen.findByText('DONE', { selector: 'span[aria-hidden]' });
    expect(screen.queryByRole('button', { name: 'Cancel Run' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Cancel WorkItem' })).not.toBeInTheDocument();
  });

  it('a BACKLOG WorkItem with no active run can still offer Cancel WorkItem, derived from its own authoritative status', async () => {
    vi.mocked(api.getWorkItem).mockResolvedValue({
      workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Add distributed tracing',
      status: 'BACKLOG', version: 1, contract: null,
    } as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ ...cardDetail({ status: 'BACKLOG' } as never) } as never);
    renderScreen();

    expect(await screen.findByRole('button', { name: 'Cancel WorkItem' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Cancel Run' })).not.toBeInTheDocument();
    expect(api.getRunDiagnostics).not.toHaveBeenCalled();
  });

  it('optimistic conflict: a fresh recheck immediately before dispatch refuses a Cancel Run that is no longer valid, and never calls the mutation', async () => {
    vi.mocked(api.getWorkItem).mockResolvedValue({
      workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Add distributed tracing',
      status: 'ACTIVE', version: 4, contract: null,
    } as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ ...cardDetail({ activeRunId: 'run-1' } as never) } as never);
    // First load reports cancelRun as valid (button renders); the fresh recheck right before dispatch reports it is no longer valid.
    vi.mocked(api.getRunDiagnostics)
      .mockResolvedValueOnce({
        runId: 'run-1', projectId: 'proj-1', workItemId: 'wi-1', workItemStatus: 'ACTIVE', runState: 'RUNNING',
        blockers: [], orphanedAttempts: [], orphanedAttemptsTruncated: false, providers: [], isolation: [], repositoryWorkspaces: [],
        validActions: [{ operationId: 'cancelRun', scopeKind: 'PROJECT', targetVersion: 0 }],
      } as never)
      .mockResolvedValueOnce({
        runId: 'run-1', projectId: 'proj-1', workItemId: 'wi-1', workItemStatus: 'ACTIVE', runState: 'CANCELLING',
        blockers: [], orphanedAttempts: [], orphanedAttemptsTruncated: false, providers: [], isolation: [], repositoryWorkspaces: [],
        validActions: [],
      } as never);
    renderScreen();

    await userEvent.click(await screen.findByRole('button', { name: 'Cancel Run' }));
    const dialog = await screen.findByRole('dialog', { name: 'Cancel Run' });
    await userEvent.type(within(dialog).getByLabelText(/Reason/), 'no longer needed');
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel Run' }));

    expect(await within(dialog).findByText(/no longer be cancelled/)).toBeInTheDocument();
    expect(api.cancelRun).not.toHaveBeenCalled();
  });

  it('has no automated accessibility violations once loaded', async () => {
    vi.mocked(api.getWorkItem).mockResolvedValue({
      workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Add distributed tracing',
      status: 'ACTIVE', version: 4, contract: null,
    } as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ ...cardDetail() } as never);
    const { container } = renderScreen();
    await screen.findByRole('button', { name: 'Cancel WorkItem' });
    await expectNoAxeViolations(container);
  });
});

function renderGraphTab() {
  return render(<TaskDetailScreen projectId="proj-1" projectName="platform-core" workItemId="wi-1" activeTab="task-graph" onTabChange={vi.fn()} />);
}

const FORK_JOIN_NODES = [
  { key: 'start', type: 'START', outcomes: ['go'] },
  { key: 'fork', type: 'FORK', outcomes: ['a', 'b'] },
  { key: 'agent-a', type: 'AGENT', outcomes: ['done'] },
  { key: 'agent-b', type: 'AGENT', outcomes: ['done'] },
  { key: 'join', type: 'JOIN', outcomes: ['go'] },
  { key: 'gate', type: 'MACHINE_GATE', outcomes: ['pass', 'rework'] },
  { key: 'end', type: 'END' },
];
const FORK_JOIN_EDGES = [
  { key: 'e1', from: 'start', outcome: 'go', to: 'fork' },
  { key: 'e2', from: 'fork', outcome: 'a', to: 'agent-a' },
  { key: 'e3', from: 'fork', outcome: 'b', to: 'agent-b' },
  { key: 'e4', from: 'agent-a', outcome: 'done', to: 'join' },
  { key: 'e5', from: 'agent-b', outcome: 'done', to: 'join' },
  { key: 'e6', from: 'join', outcome: 'go', to: 'gate' },
  { key: 'e7', from: 'gate', outcome: 'pass', to: 'end' },
  { key: 'e8', from: 'gate', outcome: 'rework', to: 'agent-a', kind: 'COMPLETION_REWORK' },
];

function fixtureActivation(overrides: Record<string, unknown>) {
  return { nodeRunId: 'nr-x', nodeKey: 'x', activationSequence: 1, iteration: 0, state: 'SUCCEEDED', ...overrides };
}

describe('GraphTimelineTab (V7-12)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getTaskFamily).mockResolvedValue(FAMILY as never);
    vi.mocked(api.getWorkItem).mockResolvedValue({
      workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Add distributed tracing',
      status: 'ACTIVE', version: 4, contract: null,
    } as never);
  });

  it('shows a real empty state when the WorkItem has never started a run, without ever calling getRunGraph', async () => {
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ ...cardDetail() } as never);
    renderGraphTab();

    expect(await screen.findByText(/never started one/)).toBeInTheDocument();
    expect(api.getRunGraph).not.toHaveBeenCalled();
  });

  it('renders a real fork/join/rework graph in both canvas and accessible-list modes, deriving current state from the real activations', async () => {
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ ...cardDetail({ activeRunId: 'run-1' } as never) } as never);
    vi.mocked(api.getRunDiagnostics).mockResolvedValue({
      runId: 'run-1', projectId: 'proj-1', workItemId: 'wi-1', workItemStatus: 'ACTIVE', runState: 'RUNNING',
      blockers: [], orphanedAttempts: [], orphanedAttemptsTruncated: false, providers: [], isolation: [], repositoryWorkspaces: [],
      validActions: [{ operationId: 'cancelRun', scopeKind: 'PROJECT', targetVersion: 0 }],
    } as never);
    vi.mocked(api.getRunGraph).mockResolvedValue({
      runId: 'run-1', manifestRevision: 1, nodes: FORK_JOIN_NODES, possibleEdges: FORK_JOIN_EDGES,
      takenEdges: [{ fromNodeRunId: 'nr-start', fromNodeKey: 'start', outcome: 'go', toNodeKey: 'fork', activationSequence: 1 }],
      activations: [
        fixtureActivation({ nodeRunId: 'nr-start', nodeKey: 'start', activationSequence: 1, state: 'SUCCEEDED' }),
        fixtureActivation({ nodeRunId: 'nr-fork', nodeKey: 'fork', activationSequence: 2, state: 'SUCCEEDED' }),
        fixtureActivation({ nodeRunId: 'nr-a', nodeKey: 'agent-a', activationSequence: 3, state: 'SUCCEEDED' }),
        fixtureActivation({ nodeRunId: 'nr-b', nodeKey: 'agent-b', activationSequence: 4, state: 'RUNNING' }),
      ],
      branchTokens: [], freshness: { generation: 1, asOfJournalPosition: 4, status: 'LIVE' },
    } as never);
    vi.mocked(api.getRunTimeline).mockResolvedValue({ runId: 'run-1', entries: [], freshness: { generation: 1, asOfJournalPosition: 4, status: 'LIVE' } } as never);
    renderGraphTab();

    await screen.findByRole('img', { name: /Workflow graph for run run-1/ });
    // agent-b is still RUNNING; join/gate/end were never reached — not yet fabricated as any other state.
    expect(screen.getAllByText('agent-b').length).toBeGreaterThan(0);

    await userEvent.click(screen.getByRole('button', { name: /Accessible List/ }));
    expect(screen.getAllByText('not yet reached').length).toBeGreaterThan(0);
    const joinRow = screen.getByText('join').closest('button')!;
    expect(within(joinRow).getByText('not yet reached')).toBeInTheDocument();
  });

  it('an admission-blocked node offers a real Retry action; a failed retry never fabricates a second blocked activation', async () => {
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ ...cardDetail({ activeRunId: 'run-1', status: 'BLOCKED' } as never) } as never);
    const diagnostics = {
      runId: 'run-1', projectId: 'proj-1', workItemId: 'wi-1', workItemStatus: 'BLOCKED', runState: 'WAITING',
      blockers: [{
        blockerId: 'blk-1', type: 'ADAPTER_BUILD_DRIFT', state: 'OPEN', reason: 'Pinned adapter build drifted',
        sourceNodeRunId: 'nr-a', openedAt: '2026-09-26T00:00:00Z', version: 1, admissionReason: true,
        validActions: [{ operationId: 'retryBlockedActivation', scopeKind: 'PROJECT', targetVersion: 1 }],
      }],
      orphanedAttempts: [], orphanedAttemptsTruncated: false, providers: [], isolation: [], repositoryWorkspaces: [],
      validActions: [],
    };
    vi.mocked(api.getRunDiagnostics).mockResolvedValue(diagnostics as never);
    vi.mocked(api.getRunGraph).mockResolvedValue({
      runId: 'run-1', manifestRevision: 1, nodes: FORK_JOIN_NODES, possibleEdges: FORK_JOIN_EDGES,
      activations: [fixtureActivation({ nodeRunId: 'nr-a', nodeKey: 'agent-a', activationSequence: 1, state: 'BLOCKED' })],
      freshness: { generation: 1, asOfJournalPosition: 1, status: 'LIVE' },
    } as never);
    vi.mocked(api.getRunTimeline).mockResolvedValue({ runId: 'run-1', entries: [], freshness: { generation: 1, asOfJournalPosition: 1, status: 'LIVE' } } as never);
    vi.mocked(api.retryBlockedActivation).mockResolvedValue({
      nodeRunId: 'nr-a', alreadyRetried: false, retried: false, failureReason: 'ADAPTER_BUILD_DRIFT', failureDetail: 'build still drifted',
    } as never);
    renderGraphTab();

    await userEvent.click(await screen.findByRole('button', { name: /Accessible List/ }));
    await userEvent.click(await screen.findByRole('button', { name: 'Retry' }));
    const dialog = await screen.findByRole('dialog', { name: 'Retry Blocked Activation' });
    await userEvent.type(within(dialog).getByLabelText(/Reason/), 'trying again');
    await userEvent.click(within(dialog).getByRole('button', { name: 'Retry' }));

    await waitFor(() => expect(api.retryBlockedActivation).toHaveBeenCalledWith('nr-a', { reason: 'trying again' }, expect.objectContaining({ token: 'test-session-token' })));
    expect(await screen.findByText(/cannot recover this adapter build drift/)).toBeInTheDocument();
    // Still exactly one BLOCKED node in the (unchanged, re-fetched) fixture data — the failed retry fabricated nothing.
    expect(screen.getAllByText('BLOCKED', { selector: 'span[aria-hidden]' })).toHaveLength(1);
  });

  it('has no automated accessibility violations once loaded', async () => {
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ ...cardDetail({ activeRunId: 'run-1' } as never) } as never);
    vi.mocked(api.getRunDiagnostics).mockResolvedValue({
      runId: 'run-1', projectId: 'proj-1', workItemId: 'wi-1', workItemStatus: 'ACTIVE', runState: 'RUNNING',
      blockers: [], orphanedAttempts: [], orphanedAttemptsTruncated: false, providers: [], isolation: [], repositoryWorkspaces: [],
      validActions: [],
    } as never);
    vi.mocked(api.getRunGraph).mockResolvedValue({
      runId: 'run-1', manifestRevision: 1, nodes: FORK_JOIN_NODES, possibleEdges: FORK_JOIN_EDGES,
      activations: [fixtureActivation({ nodeRunId: 'nr-start', nodeKey: 'start', activationSequence: 1, state: 'SUCCEEDED' })],
      freshness: { generation: 1, asOfJournalPosition: 1, status: 'LIVE' },
    } as never);
    vi.mocked(api.getRunTimeline).mockResolvedValue({ runId: 'run-1', entries: [], freshness: { generation: 1, asOfJournalPosition: 1, status: 'LIVE' } } as never);
    const { container } = renderGraphTab();
    await screen.findByRole('img', { name: /Workflow graph/ });
    await expectNoAxeViolations(container);
  });
});

function renderWorkspaceTab() {
  return render(<TaskDetailScreen projectId="proj-1" projectName="platform-core" workItemId="wi-1" activeTab="task-workspace" onTabChange={vi.fn()} />);
}

function repoFixture(overrides: Record<string, unknown> = {}) {
  return {
    repositoryWorkspaceId: 'rw-1', workspaceSetId: 'ws-1', repositoryId: 'core-api', generation: 3,
    state: 'READY', version: 5, branchRef: 'main', baseRevision: 'aaaa1111aaaa', currentRevision: 'bbbb2222bbbb',
    hasActiveWriteLease: false, validActions: [{ operationId: 'requestWorkspaceReconciliation', scopeKind: 'PROJECT', targetVersion: 5 }],
    ...overrides,
  };
}

const REPO_B_QUARANTINED = repoFixture({
  repositoryWorkspaceId: 'rw-2', repositoryId: 'worker-service', generation: 2, state: 'QUARANTINED', version: 7,
  baseRevision: 'cccc3333cccc', currentRevision: 'dddd4444dddd', lastProvisionErrorCode: 'SCOPE_VIOLATION',
  validActions: [{ operationId: 'requestWorkspaceReconciliation', scopeKind: 'PROJECT', targetVersion: 7 }],
});

const REPO_C_NO_REVISION = repoFixture({
  repositoryWorkspaceId: 'rw-3', repositoryId: 'stale-repo', generation: 1, state: 'PROVISIONING', version: 1,
  baseRevision: undefined, currentRevision: undefined, validActions: [],
});

describe('WorkspaceTab (V7-13)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getTaskFamily).mockResolvedValue(FAMILY as never);
    vi.mocked(api.getWorkItem).mockResolvedValue({
      workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Add distributed tracing',
      status: 'ACTIVE', version: 4, contract: null,
    } as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ ...cardDetail() } as never);
  });

  it('multi-repo: renders every real repository workspace as its own tab, and switching tabs never dispatches a mutation', async () => {
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'RUNNING', version: 2, hasBaseRevisionSet: true,
      repositoryWorkspaces: [repoFixture(), REPO_B_QUARANTINED], validActions: [],
    } as never);
    vi.mocked(api.getWorkspaceDiff).mockResolvedValue({
      baseRevision: {}, resultRevision: {}, files: [], patch: '', byteLimit: 1000, fileLimit: 100, filesTruncated: false, patchTruncated: false,
    } as never);
    renderWorkspaceTab();

    expect(await screen.findByRole('button', { name: /core-api revision bbbb2222bb/ })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /worker-service revision dddd4444dd, quarantined/ })).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: /worker-service revision/ }));
    expect(await screen.findByText('SCOPE_VIOLATION')).toBeInTheDocument();
    expect(api.requestWorkspaceReconciliation).not.toHaveBeenCalled();
    expect(api.cancelWorkItem).not.toHaveBeenCalled();
  });

  it('binary/large output: a binary diff file shows "binary" instead of a +/- count, and a binary source fetch never renders decoded content', async () => {
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'RUNNING', version: 2, hasBaseRevisionSet: true,
      repositoryWorkspaces: [repoFixture()], validActions: [],
    } as never);
    vi.mocked(api.getWorkspaceDiff).mockResolvedValue({
      baseRevision: {}, resultRevision: {}, patch: '', byteLimit: 1000, fileLimit: 100, filesTruncated: true, patchTruncated: false,
      files: [
        { path: 'src/renamed-module.ts', additions: 3, deletions: 1, binary: false },
        { path: 'assets/logo.png', additions: 0, deletions: 0, binary: true },
      ],
    } as never);
    vi.mocked(workspaceinspection.fetchWorkspaceSource).mockResolvedValue({
      content: '', totalBytes: 2_000_000, lineCount: 0, byteLimit: 262144, lineLimit: 2000, truncated: false, binary: true,
      revision: 'bbbb2222bbbb', workspaceGeneration: 3,
    } as never);
    renderWorkspaceTab();

    expect(await screen.findByText('src/renamed-module.ts')).toBeInTheDocument();
    expect(screen.getByText('FILES TRUNCATED', { selector: 'span[aria-hidden]' })).toBeInTheDocument();
    const binaryRow = screen.getByText('assets/logo.png').closest('div')!;
    expect(within(binaryRow).getByText('binary')).toBeInTheDocument();

    await userEvent.click(within(screen.getByText('src/renamed-module.ts').closest('div')!).getByRole('button', { name: 'View Source' }));
    await waitFor(() => expect(workspaceinspection.fetchWorkspaceSource).toHaveBeenCalledWith('proj-1', 'rw-1', expect.objectContaining({ path: 'src/renamed-module.ts', revision: 'bbbb2222bbbb' })));
    expect(await screen.findByText(/Binary file \(2000000 bytes\)/)).toBeInTheDocument();
  });

  it('stale/no-revision: a repository workspace with no recorded revision shows an honest message and never calls diff or log', async () => {
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'REQUESTED', version: 1, hasBaseRevisionSet: false,
      repositoryWorkspaces: [REPO_C_NO_REVISION], validActions: [],
    } as never);
    renderWorkspaceTab();

    expect(await screen.findByText('This repository workspace has no revision recorded yet.')).toBeInTheDocument();
    expect(api.getWorkspaceDiff).not.toHaveBeenCalled();
    expect(api.getWorkspaceRepositoryLog).not.toHaveBeenCalled();
  });

  it('scope violation: a real ErrScopeMismatch-mapped error surfaces as a real InlineError, never a crash', async () => {
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'RUNNING', version: 2, hasBaseRevisionSet: true,
      repositoryWorkspaces: [repoFixture()], validActions: [],
    } as never);
    const { ApiError } = await vi.importActual<typeof import('../api/generated')>('../api/generated');
    vi.mocked(api.getWorkspaceDiff).mockRejectedValue(new ApiError(404, 'RESOURCE_HIDDEN', 'not found'));
    renderWorkspaceTab();

    expect(await screen.findByText('not found')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
  });

  it('a real quarantined repository with the reconcile action offers Request Reconcile, and dispatches it for real', async () => {
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'RUNNING', version: 2, hasBaseRevisionSet: true,
      repositoryWorkspaces: [REPO_B_QUARANTINED], validActions: [],
    } as never);
    vi.mocked(api.getWorkspaceDiff).mockResolvedValue({
      baseRevision: {}, resultRevision: {}, files: [], patch: '', byteLimit: 1000, fileLimit: 100, filesTruncated: false, patchTruncated: false,
    } as never);
    vi.mocked(api.requestWorkspaceReconciliation).mockResolvedValue({
      repositoryWorkspaceId: 'rw-2', projectId: 'proj-1', state: 'RECONCILING', reconciliationJobId: 'job-1',
    } as never);
    renderWorkspaceTab();

    await userEvent.click(await screen.findByRole('button', { name: 'Request Reconcile' }));
    await waitFor(() => expect(api.requestWorkspaceReconciliation).toHaveBeenCalledWith('proj-1', 'rw-2', {}, expect.objectContaining({ ifMatch: '"7"' })));
  });

  it('log: paginates real repository-log entries via Load more', async () => {
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'RUNNING', version: 2, hasBaseRevisionSet: true,
      repositoryWorkspaces: [repoFixture()], validActions: [],
    } as never);
    vi.mocked(api.getWorkspaceDiff).mockResolvedValue({
      baseRevision: {}, resultRevision: {}, files: [], patch: '', byteLimit: 1000, fileLimit: 100, filesTruncated: false, patchTruncated: false,
    } as never);
    vi.mocked(api.getWorkspaceRepositoryLog)
      .mockResolvedValueOnce({
        anchor: {}, limit: 1, byteLimit: 1000, truncated: false, nextCursor: 'cursor-2',
        entries: [{ commitId: 'commit-one-long-hash', parentIds: [], authorName: 'Ada', authorEmail: 'ada@example.invalid', authoredAt: '2026-09-26T00:00:00Z', subject: 'First commit', subjectTruncated: false }],
      } as never)
      .mockResolvedValueOnce({
        anchor: {}, limit: 1, byteLimit: 1000, truncated: false,
        entries: [{ commitId: 'commit-two-long-hash', parentIds: ['commit-one-long-hash'], authorName: 'Ada', authorEmail: 'ada@example.invalid', authoredAt: '2026-09-26T01:00:00Z', subject: 'Second commit', subjectTruncated: false }],
      } as never);
    renderWorkspaceTab();

    await userEvent.click(await screen.findByRole('button', { name: 'Log' }));
    expect(await screen.findByText('First commit')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Load more' }));
    expect(await screen.findByText('Second commit')).toBeInTheDocument();
  });

  it('never renders any control that could execute a command in the workspace', async () => {
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'RUNNING', version: 2, hasBaseRevisionSet: true,
      repositoryWorkspaces: [repoFixture()], validActions: [],
    } as never);
    vi.mocked(api.getWorkspaceDiff).mockResolvedValue({
      baseRevision: {}, resultRevision: {}, files: [], patch: '', byteLimit: 1000, fileLimit: 100, filesTruncated: false, patchTruncated: false,
    } as never);
    renderWorkspaceTab();
    await screen.findByRole('button', { name: /core-api revision/ });

    expect(screen.queryByRole('textbox', { name: /command/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /run|execute|terminal|shell/i })).not.toBeInTheDocument();
  });

  it('has no automated accessibility violations once loaded', async () => {
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'RUNNING', version: 2, hasBaseRevisionSet: true,
      repositoryWorkspaces: [repoFixture()], validActions: [],
    } as never);
    vi.mocked(api.getWorkspaceDiff).mockResolvedValue({
      baseRevision: {}, resultRevision: {}, files: [], patch: '', byteLimit: 1000, fileLimit: 100, filesTruncated: false, patchTruncated: false,
    } as never);
    const { container } = renderWorkspaceTab();
    await screen.findByRole('button', { name: /core-api revision/ });
    await expectNoAxeViolations(container);
  });
});

function releaseSetFixture(overrides: Record<string, unknown> = {}) {
  return {
    releaseSetId: 'rset-1', projectId: 'proj-1', familyId: 'fam-1', state: 'CREATED', contentHash: 'sha256:abcabcabc', version: 1,
    entries: [
      { repositoryId: 'core-api', baseVcsObjectId: 'aaaa1111aaaa', resultVcsObjectId: 'bbbb2222bbbb', verdict: 'PASS' },
      { repositoryId: 'worker-service', baseVcsObjectId: 'cccc3333cccc', resultVcsObjectId: 'dddd4444dddd', verdict: 'FAIL' },
    ],
    ...overrides,
  };
}

const REPO_WORKER = repoFixture({
  repositoryWorkspaceId: 'rw-2', repositoryId: 'worker-service', version: 5,
  baseRevision: 'cccc3333cccc', currentRevision: 'dddd4444dddd',
});

async function openReleaseSetPanel() {
  await userEvent.click(await screen.findByRole('button', { name: 'ReleaseSet' }));
  return screen.findByRole('button', { name: 'Hide ReleaseSet' });
}

describe('ReleaseSetPanel (V7-13A)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getTaskFamily).mockResolvedValue(FAMILY as never);
    vi.mocked(api.getWorkItem).mockResolvedValue({
      workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Add distributed tracing',
      status: 'ACTIVE', version: 4, contract: null,
    } as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ ...cardDetail() } as never);
  });

  it('creates a real ReleaseSet pre-filled from each repository\'s own real base/current revision', async () => {
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'RUNNING', version: 2, hasBaseRevisionSet: true,
      repositoryWorkspaces: [repoFixture(), REPO_WORKER], validActions: [],
    } as never);
    vi.mocked(api.getWorkspaceDiff).mockResolvedValue({
      baseRevision: {}, resultRevision: {}, files: [], patch: '', byteLimit: 1000, fileLimit: 100, filesTruncated: false, patchTruncated: false,
    } as never);
    vi.mocked(api.listReleaseSetsForFamily).mockResolvedValue({ items: [] } as never);
    vi.mocked(api.createReleaseSet).mockResolvedValue({ releaseSetId: 'rset-1', projectId: 'proj-1', familyId: 'fam-1', state: 'CREATED', contentHash: 'sha256:x', version: 1 } as never);
    renderWorkspaceTab();

    await openReleaseSetPanel();
    await userEvent.click(await screen.findByRole('button', { name: 'Create' }));

    const dialog = await screen.findByRole('dialog', { name: 'Create ReleaseSet' });
    const baseFields = within(dialog).getAllByLabelText('Base revision') as HTMLInputElement[];
    const resultFields = within(dialog).getAllByLabelText('Result revision') as HTMLInputElement[];
    expect(baseFields.map(f => f.value)).toEqual(['aaaa1111aaaa', 'cccc3333cccc']);
    expect(resultFields.map(f => f.value)).toEqual(['bbbb2222bbbb', 'dddd4444dddd']);

    const verdictSelects = within(dialog).getAllByLabelText(/^Verdict/);
    await userEvent.selectOptions(verdictSelects[0], 'PASS');
    await userEvent.selectOptions(verdictSelects[1], 'FAIL');
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(api.createReleaseSet).toHaveBeenCalledWith('proj-1', 'fam-1', {
      repositories: [
        { repositoryId: 'core-api', baseVcsObjectId: 'aaaa1111aaaa', resultVcsObjectId: 'bbbb2222bbbb', verdict: 'PASS' },
        { repositoryId: 'worker-service', baseVcsObjectId: 'cccc3333cccc', resultVcsObjectId: 'dddd4444dddd', verdict: 'FAIL' },
      ],
    }, expect.objectContaining({ token: 'test-session-token' })));
  });

  it('partial release: a mixed-verdict ReleaseSet can be sealed with no all-PASS gate — the confirm dialog shows the real per-repository verdicts', async () => {
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'RUNNING', version: 2, hasBaseRevisionSet: true,
      repositoryWorkspaces: [repoFixture(), REPO_WORKER], validActions: [],
    } as never);
    vi.mocked(api.getWorkspaceDiff).mockResolvedValue({
      baseRevision: {}, resultRevision: {}, files: [], patch: '', byteLimit: 1000, fileLimit: 100, filesTruncated: false, patchTruncated: false,
    } as never);
    vi.mocked(api.listReleaseSetsForFamily).mockResolvedValue({ items: [releaseSetFixture()] } as never);
    vi.mocked(api.sealReleaseSet).mockResolvedValue({ releaseSetId: 'rset-1', projectId: 'proj-1', familyId: 'fam-1', state: 'SEALED', contentHash: 'sha256:x', version: 2 } as never);
    renderWorkspaceTab();

    await openReleaseSetPanel();
    await userEvent.click(await screen.findByRole('button', { name: 'Seal' }));

    const dialog = await screen.findByRole('dialog', { name: 'Seal ReleaseSet' });
    expect(within(dialog).getByText('PASS', { selector: 'span[aria-hidden]' })).toBeInTheDocument();
    expect(within(dialog).getByText('FAIL', { selector: 'span[aria-hidden]' })).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole('button', { name: 'Seal' }));

    await waitFor(() => expect(api.sealReleaseSet).toHaveBeenCalledWith('proj-1', 'rset-1', {}, expect.objectContaining({ ifMatch: '"1"' })));
  });

  it('duplicate seal: a real conflict from the server surfaces as a real error, not a crash', async () => {
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'RUNNING', version: 2, hasBaseRevisionSet: true,
      repositoryWorkspaces: [repoFixture(), REPO_WORKER], validActions: [],
    } as never);
    vi.mocked(api.getWorkspaceDiff).mockResolvedValue({
      baseRevision: {}, resultRevision: {}, files: [], patch: '', byteLimit: 1000, fileLimit: 100, filesTruncated: false, patchTruncated: false,
    } as never);
    vi.mocked(api.listReleaseSetsForFamily).mockResolvedValue({ items: [releaseSetFixture()] } as never);
    const { ApiError } = await vi.importActual<typeof import('../api/generated')>('../api/generated');
    vi.mocked(api.sealReleaseSet).mockRejectedValue(new ApiError(409, 'CONFLICT', 'release set is not open (already sealed or abandoned)'));
    renderWorkspaceTab();

    await openReleaseSetPanel();
    await userEvent.click(await screen.findByRole('button', { name: 'Seal' }));
    const dialog = await screen.findByRole('dialog', { name: 'Seal ReleaseSet' });
    await userEvent.click(within(dialog).getByRole('button', { name: 'Seal' }));

    expect(await within(dialog).findByText('release set is not open (already sealed or abandoned)')).toBeInTheDocument();
  });

  it('stale revision: local commit dispatches with freshly refetched release-set/workspace versions, never the stale ones already in cache', async () => {
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'RUNNING', version: 2, hasBaseRevisionSet: true,
      repositoryWorkspaces: [repoFixture(), REPO_WORKER], validActions: [],
    } as never);
    vi.mocked(api.getWorkspaceDiff).mockResolvedValue({
      baseRevision: {}, resultRevision: {}, files: [], patch: '', byteLimit: 1000, fileLimit: 100, filesTruncated: false, patchTruncated: false,
    } as never);
    const sealedReleaseSet = releaseSetFixture({ state: 'SEALED', version: 1 });
    vi.mocked(api.listReleaseSetsForFamily).mockResolvedValue({ items: [sealedReleaseSet] } as never);
    // Both queries report a NEWER version than what the initial page load already cached —
    // the real state moved (e.g. another operation touched it) between page load and dispatch.
    vi.mocked(api.getReleaseSet).mockResolvedValue({ ...sealedReleaseSet, version: 9 } as never);
    vi.mocked(api.getRepositoryWorkspaceState).mockResolvedValue({ ...REPO_WORKER, version: 42 } as never);
    vi.mocked(api.requestReleaseSetLocalCommit).mockResolvedValue({
      releaseSetLocalCommitId: 'lc-1', releaseSetId: 'rset-1', repositoryWorkspaceId: 'rw-2', state: 'REQUESTED', jobId: 'job-1', marker: 'm-1',
    } as never);
    vi.mocked(api.getReleaseSetLocalCommitStatus).mockResolvedValue({ state: 'REQUESTED' } as never);
    renderWorkspaceTab();

    await openReleaseSetPanel();
    await userEvent.click((await screen.findAllByRole('button', { name: 'Local Commit' }))[1]);
    const dialog = await screen.findByRole('dialog', { name: 'Create Local Commit' });
    await userEvent.type(within(dialog).getByLabelText(/Message/), 'release commit');
    await userEvent.click(within(dialog).getByRole('button', { name: 'Create Local Commit' }));

    await waitFor(() => expect(api.requestReleaseSetLocalCommit).toHaveBeenCalledWith('proj-1', 'rset-1', expect.objectContaining({
      expectedReleaseSetVersion: 9, repositoryWorkspaceId: 'rw-2', expectedWorkspaceVersion: 42,
    }), expect.objectContaining({ token: 'test-session-token' })));
  });

  it('a real requestWorkspaceSetRelease ValidAction offers Release Workspace, dispatching with a real If-Match and honest async wording', async () => {
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'READY', version: 2, hasBaseRevisionSet: true,
      repositoryWorkspaces: [repoFixture(), REPO_WORKER],
      validActions: [{ operationId: 'requestWorkspaceSetRelease', scopeKind: 'PROJECT', targetVersion: 2 }],
    } as never);
    vi.mocked(api.getWorkspaceDiff).mockResolvedValue({
      baseRevision: {}, resultRevision: {}, files: [], patch: '', byteLimit: 1000, fileLimit: 100, filesTruncated: false, patchTruncated: false,
    } as never);
    vi.mocked(api.listReleaseSetsForFamily).mockResolvedValue({ items: [] } as never);
    // The real requestWorkspaceSetRelease command never itself transitions the
    // WorkspaceSet — it only enqueues an async job; result.state is the
    // PRE-release state (confirmed by reading internal/app/workspacerelease's
    // own command source), never the state the set is "entering". The toast
    // must not claim otherwise.
    vi.mocked(api.requestWorkspaceSetRelease).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'READY', releaseJobId: 'job-9',
    } as never);
    renderWorkspaceTab();

    await openReleaseSetPanel();
    await userEvent.click(await screen.findByRole('button', { name: 'Release Workspace' }));
    const dialog = await screen.findByRole('dialog', { name: 'Release Workspace' });
    expect(within(dialog).getByText(/asynchronous/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole('button', { name: 'Request Release' }));

    await waitFor(() => expect(api.requestWorkspaceSetRelease).toHaveBeenCalledWith('proj-1', 'fam-1', {}, expect.objectContaining({ ifMatch: '"2"' })));
    expect(await screen.findByText(/job-9/)).toBeInTheDocument();
    expect(screen.queryByText(/entering READY/)).not.toBeInTheDocument();
  });

  it('never renders any push/PR/merge/force-push action anywhere in the panel', async () => {
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'RUNNING', version: 2, hasBaseRevisionSet: true,
      repositoryWorkspaces: [repoFixture(), REPO_WORKER],
      validActions: [{ operationId: 'requestWorkspaceSetRelease', scopeKind: 'PROJECT', targetVersion: 2 }],
    } as never);
    vi.mocked(api.getWorkspaceDiff).mockResolvedValue({
      baseRevision: {}, resultRevision: {}, files: [], patch: '', byteLimit: 1000, fileLimit: 100, filesTruncated: false, patchTruncated: false,
    } as never);
    vi.mocked(api.listReleaseSetsForFamily).mockResolvedValue({ items: [releaseSetFixture(), releaseSetFixture({ releaseSetId: 'rset-2', state: 'SEALED' })] } as never);
    const { container } = renderWorkspaceTab();

    await openReleaseSetPanel();
    await screen.findByRole('button', { name: 'Seal' });
    const text = container.textContent ?? '';
    expect(text).not.toMatch(/\bpush\b/i);
    expect(text).not.toMatch(/\bpull request\b|\bPR\b/i);
    expect(text).not.toMatch(/\bmerge\b/i);
    expect(text).not.toMatch(/force[- ]push/i);
  });
});

function renderEvidenceTab() {
  return render(<TaskDetailScreen projectId="proj-1" projectName="platform-core" workItemId="wi-1" activeTab="task-evidence" onTabChange={vi.fn()} />);
}

function evidenceFixture(overrides: Record<string, unknown> = {}) {
  return {
    evidenceId: 'ev-1', projectId: 'proj-1', workItemId: 'wi-1', runId: 'run-1', nodeRunId: 'nr-1', attemptId: 'att-1',
    kind: 'VERIFY', verdict: 'PASS', artifactReferences: ['art-1'],
    revisions: [{ repositoryId: 'core-api', vcsObjectId: 'a3f9e8babcdef012', workspaceGeneration: 1 }],
    revisionSetHash: 'sha256:rset-abcdef0123456789', policyVersion: 'policy-v1', createdAt: '2026-09-26T09:00:00Z',
    ...overrides,
  };
}

function artifactFixture(overrides: Record<string, unknown> = {}) {
  return {
    artifactId: 'art-1', projectId: 'proj-1', contentHash: 'sha256:contenthashabcdef0123456789',
    size: 128, mediaType: 'application/json', sensitivity: 'PUBLIC', redacted: false,
    retentionClass: 'RAW_OUTPUT_TEMP', attachState: 'ATTACHED', hold: false,
    createdAt: '2026-09-26T09:00:00Z', version: 1,
    ...overrides,
  };
}

describe('EvidenceTab (V7-14)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getTaskFamily).mockResolvedValue(FAMILY as never);
    vi.mocked(api.getWorkItem).mockResolvedValue({
      workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Add distributed tracing',
      status: 'ACTIVE', version: 4, contract: null,
    } as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({ ...cardDetail() } as never);
    // jsdom has no real createObjectURL implementation.
    globalThis.URL.createObjectURL = vi.fn(() => 'blob:mock-url');
    globalThis.URL.revokeObjectURL = vi.fn();
  });

  it('shows a real empty state when the WorkItem has no evidence yet', async () => {
    vi.mocked(api.listEvidence).mockResolvedValue({ items: [] } as never);
    renderEvidenceTab();

    expect(await screen.findByText('No evidence recorded yet for this WorkItem.')).toBeInTheDocument();
    expect(api.listArtifacts).not.toHaveBeenCalled();
  });

  it('renders real Evidence rows and only fetches artifacts lazily once a row is expanded', async () => {
    vi.mocked(api.listEvidence).mockResolvedValue({ items: [evidenceFixture()] } as never);
    vi.mocked(api.listArtifacts).mockResolvedValue({ items: [artifactFixture()] } as never);
    renderEvidenceTab();

    const row = await screen.findByRole('button', { name: /VERIFY/ });
    expect(within(row).getByText('PASS')).toBeInTheDocument();
    expect(screen.getByText(/core-api@a3f9e8babc/)).toBeInTheDocument();
    expect(api.listArtifacts).not.toHaveBeenCalled();

    await userEvent.click(row);
    await waitFor(() => expect(api.listArtifacts).toHaveBeenCalledWith('proj-1', 'wi-1', 'ev-1', expect.objectContaining({ token: 'test-session-token' })));
    expect(await screen.findByText('art-1')).toBeInTheDocument();
  });

  it('a redacted, on-hold, sensitive, expired artifact shows the real badges honestly', async () => {
    vi.mocked(api.listEvidence).mockResolvedValue({ items: [evidenceFixture()] } as never);
    vi.mocked(api.listArtifacts).mockResolvedValue({
      items: [artifactFixture({ sensitivity: 'SECRET', redacted: true, hold: true, expiresAt: '2020-01-01T00:00:00Z' })],
    } as never);
    renderEvidenceTab();

    await userEvent.click(await screen.findByRole('button', { name: /VERIFY/ }));
    await screen.findByText('art-1');
    expect(screen.getByText('SECRET')).toBeInTheDocument();
    expect(screen.getByText('REDACTED')).toBeInTheDocument();
    expect(screen.getByText('HOLD')).toBeInTheDocument();
    expect(screen.getByText('EXPIRED')).toBeInTheDocument();
    // Still not purged — a real Download action remains available.
    expect(screen.getByRole('link', { name: 'Download' })).toBeInTheDocument();
  });

  it('a purged artifact offers neither Preview nor Download — there is nothing left to fetch', async () => {
    vi.mocked(api.listEvidence).mockResolvedValue({ items: [evidenceFixture()] } as never);
    vi.mocked(api.listArtifacts).mockResolvedValue({ items: [artifactFixture({ attachState: 'PURGED' })] } as never);
    renderEvidenceTab();

    await userEvent.click(await screen.findByRole('button', { name: /VERIFY/ }));
    await screen.findByText('art-1');
    expect(screen.getByText('PURGED')).toBeInTheDocument();
    expect(screen.getByText(/Content purged by retention/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Preview' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Download' })).not.toBeInTheDocument();
  });

  it('a large safe-media artifact shows a truncation indicator instead of a Preview action, but Download stays available', async () => {
    vi.mocked(api.listEvidence).mockResolvedValue({ items: [evidenceFixture()] } as never);
    vi.mocked(api.listArtifacts).mockResolvedValue({
      items: [artifactFixture({ mediaType: 'text/plain', size: 5_000_000 })],
    } as never);
    renderEvidenceTab();

    await userEvent.click(await screen.findByRole('button', { name: /VERIFY/ }));
    await screen.findByText('art-1');
    expect(screen.getByText(/Too large to preview/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Preview' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Download' })).toBeInTheDocument();
  });

  it('never offers a Preview action for a non-inline-safe media type (e.g. text/html) — only Download, which the server itself forces to save rather than render', async () => {
    vi.mocked(api.listEvidence).mockResolvedValue({ items: [evidenceFixture()] } as never);
    vi.mocked(api.listArtifacts).mockResolvedValue({
      items: [artifactFixture({ mediaType: 'text/html', size: 200 })],
    } as never);
    renderEvidenceTab();

    await userEvent.click(await screen.findByRole('button', { name: /VERIFY/ }));
    await screen.findByText('art-1');
    expect(screen.queryByRole('button', { name: 'Preview' })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Download' })).toBeInTheDocument();
  });

  it('previews real JSON/text content as escaped text — a raw script payload is shown as literal text, never executed', async () => {
    vi.mocked(api.listEvidence).mockResolvedValue({ items: [evidenceFixture()] } as never);
    vi.mocked(api.listArtifacts).mockResolvedValue({ items: [artifactFixture()] } as never);
    const { fetchArtifactContent } = await import('../api/evidence');
    const payload = '{"note":"<script>window.__pwned = true;</script>"}';
    vi.mocked(fetchArtifactContent).mockResolvedValue({
      contentType: 'application/json', blob: new Blob([payload], { type: 'application/json' }),
    });
    renderEvidenceTab();

    await userEvent.click(await screen.findByRole('button', { name: /VERIFY/ }));
    await userEvent.click(await screen.findByRole('button', { name: 'Preview' }));

    expect(await screen.findByText(payload)).toBeInTheDocument();
    expect(document.querySelector('script[data-injected]')).toBeNull();
    expect((globalThis as { __pwned?: boolean }).__pwned).toBeUndefined();
  });

  it('a tampered/purged content fetch surfaces as a real InlineError inside the preview dialog, never served silently', async () => {
    vi.mocked(api.listEvidence).mockResolvedValue({ items: [evidenceFixture()] } as never);
    vi.mocked(api.listArtifacts).mockResolvedValue({ items: [artifactFixture()] } as never);
    const { fetchArtifactContent, ApiError: EvidenceApiError } = await import('../api/evidence').then(async m => ({
      ...m, ApiError: (await import('../api/generated')).ApiError,
    }));
    vi.mocked(fetchArtifactContent).mockRejectedValue(new EvidenceApiError(500, 'INTERNAL', 'internal error'));
    renderEvidenceTab();

    await userEvent.click(await screen.findByRole('button', { name: /VERIFY/ }));
    await userEvent.click(await screen.findByRole('button', { name: 'Preview' }));

    expect(await screen.findByText('internal error')).toBeInTheDocument();
  });

  it('has no automated accessibility violations once loaded', async () => {
    vi.mocked(api.listEvidence).mockResolvedValue({ items: [evidenceFixture()] } as never);
    vi.mocked(api.listArtifacts).mockResolvedValue({ items: [artifactFixture()] } as never);
    const { container } = renderEvidenceTab();
    await screen.findByRole('button', { name: /VERIFY/ });
    await expectNoAxeViolations(container);
  });
});
