import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import { TaskDetailScreen } from './TaskDetail';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return {
    ...actual,
    getWorkItem: vi.fn(), getTaskFamily: vi.fn(), getWorkItemProjectedDetail: vi.fn(), getRunDiagnostics: vi.fn(),
    cancelRun: vi.fn(), cancelWorkItem: vi.fn(), resolveWorkItemBlocker: vi.fn(),
    getRunGraph: vi.fn(), getRunTimeline: vi.fn(), retryBlockedActivation: vi.fn(),
  };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }) }));

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
