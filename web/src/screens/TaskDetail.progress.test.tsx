import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import * as messageApi from '../api/message';
import { nodeOneLiner } from '../api/nodedetail';
import { TaskDetailScreen } from './TaskDetail';

// V11-11 / ADR-042 — progress and results of each node on the web: one-line labels on the graph, the node pane, the Evidence tab with a
// node column and filter, and the Chat with the engine's activity messages.

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return {
    ...actual,
    getWorkItem: vi.fn(), getTaskFamily: vi.fn(), getWorkItemProjectedDetail: vi.fn(), getRunDiagnostics: vi.fn(),
    getRunGraph: vi.fn(), getRunTimeline: vi.fn(), getRunDetail: vi.fn(), getNodeRunDetail: vi.fn(), listAttemptEvents: vi.fn(),
    listEvidence: vi.fn(), listArtifacts: vi.fn(), listMessages: vi.fn(),
  };
});
vi.mock('../api/session', () => ({
  withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }),
  getSessionToken: () => 'test-session-token',
}));
vi.mock('../api/message', async () => {
  const actual = await vi.importActual<typeof import('../api/message')>('../api/message');
  return { ...actual, fetchMessageContent: vi.fn(), uploadAttachment: vi.fn() };
});

function render(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

const NODES = [
  { key: 'start', type: 'START', outcomes: ['go'] },
  { key: 'implement', type: 'AGENT', outcomes: ['done'] },
  { key: 'quality', type: 'COMMAND', outcomes: ['pass'] },
  { key: 'review', type: 'AGENT', outcomes: ['approved', 'rework'] },
  { key: 'end', type: 'END' },
];
const EDGES = [
  { key: 'e1', from: 'start', outcome: 'go', to: 'implement' },
  { key: 'e2', from: 'implement', outcome: 'done', to: 'quality' },
  { key: 'e3', from: 'quality', outcome: 'pass', to: 'review' },
  { key: 'e4', from: 'review', outcome: 'approved', to: 'end' },
];
const START = '2026-10-11T00:00:00Z';
const TIMELINE = [
  { kind: 'NODE_RUN', activationSequence: 2, nodeRunId: 'nr-impl', nodeKey: 'implement', iteration: 1, nodeState: 'SUCCEEDED', selectedOutcome: 'done' },
  { kind: 'EXECUTION_ATTEMPT', activationSequence: 2, nodeRunId: 'nr-impl', nodeKey: 'implement', iteration: 1, attemptId: 'att-impl', attemptNumber: 1, attemptState: 'SUCCEEDED', startedAt: START, finishedAt: '2026-10-11T00:00:41Z' },
  { kind: 'NODE_RUN', activationSequence: 3, nodeRunId: 'nr-quality', nodeKey: 'quality', iteration: 1, nodeState: 'FAILED' },
  { kind: 'EXECUTION_ATTEMPT', activationSequence: 3, nodeRunId: 'nr-quality', nodeKey: 'quality', iteration: 1, attemptId: 'att-q', attemptNumber: 1, attemptState: 'FAILED', failureCode: 'SCOPE_VIOLATION', startedAt: START, finishedAt: '2026-10-11T00:01:00Z' },
  { kind: 'NODE_RUN', activationSequence: 4, nodeRunId: 'nr-review', nodeKey: 'review', iteration: 1, nodeState: 'RUNNING' },
  { kind: 'EXECUTION_ATTEMPT', activationSequence: 4, nodeRunId: 'nr-review', nodeKey: 'review', iteration: 1, attemptId: 'att-r', attemptNumber: 1, attemptState: 'RUNNING', startedAt: new Date(Date.now() - 130_000).toISOString() },
];

function activation(over: Record<string, unknown>) {
  return { nodeRunId: 'nr-x', nodeKey: 'x', activationSequence: 1, iteration: 1, state: 'SUCCEEDED', ...over };
}

function setup(activeTab: 'task-graph' | 'task-evidence' | 'task-chat', prepare?: () => void, onTabChange = vi.fn()) {
  vi.clearAllMocks();
  vi.mocked(api.getTaskFamily).mockResolvedValue({ familyId: 'fam-1', projectId: 'proj-1', rootWorkItemId: 'wi-1', scopeVersion: 1, status: 'ACTIVE', version: 1 } as never);
  vi.mocked(api.getWorkItem).mockResolvedValue({ workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'T-01', status: 'ACTIVE', version: 4, contract: null } as never);
  vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({
    card: { workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', title: 'T-01', isRoot: true, status: 'ACTIVE', activeRunId: 'run-1', activeRunStatus: 'RUNNING', blockerCount: 0, pendingScopeExpansionCount: 0 },
    readiness: { workItemId: 'wi-1', status: 'ACTIVE', version: 4, ready: false }, runs: [], freshness: { generation: 1, asOfJournalPosition: 1, status: 'LIVE' }, validActions: [],
  } as never);
  vi.mocked(api.getRunDiagnostics).mockResolvedValue({
    runId: 'run-1', projectId: 'proj-1', workItemId: 'wi-1', workItemStatus: 'ACTIVE', runState: 'RUNNING',
    blockers: [], orphanedAttempts: [], orphanedAttemptsTruncated: false, providers: [], isolation: [], repositoryWorkspaces: [], validActions: [],
  } as never);
  vi.mocked(api.getRunDetail).mockResolvedValue({ approvalRequests: [] } as never);
  vi.mocked(api.getRunGraph).mockResolvedValue({
    runId: 'run-1', manifestRevision: 1, nodes: NODES, possibleEdges: EDGES, takenEdges: [],
    activations: [
      activation({ nodeRunId: 'nr-start', nodeKey: 'start', activationSequence: 1 }),
      activation({ nodeRunId: 'nr-impl', nodeKey: 'implement', activationSequence: 2 }),
      activation({ nodeRunId: 'nr-quality', nodeKey: 'quality', activationSequence: 3, state: 'FAILED' }),
      activation({ nodeRunId: 'nr-review', nodeKey: 'review', activationSequence: 4, state: 'RUNNING' }),
    ],
    branchTokens: [], freshness: { generation: 1, asOfJournalPosition: 4, status: 'LIVE' },
  } as never);
  vi.mocked(api.getRunTimeline).mockResolvedValue({ runId: 'run-1', entries: TIMELINE, freshness: { generation: 1, asOfJournalPosition: 4, status: 'LIVE' } } as never);
  vi.mocked(api.getNodeRunDetail).mockResolvedValue({
    nodeRunId: 'nr-quality', runId: 'run-1', workItemId: 'wi-1', projectId: 'proj-1', nodeKey: 'quality', nodeType: 'COMMAND', state: 'FAILED', iteration: 1, activationSequence: 3,
    definition: { type: 'COMMAND', outcomes: ['pass'] }, attempts: [], evidence: [], result: { kind: 'COMMAND', command: { exitCode: 1, durationMillis: 900, stderr: 'boom' } },
    failure: { code: 'SCOPE_VIOLATION', title: 'The agent changed files outside its scope', explanation: 'e', suggestion: 's', action: 'RETRY_WORK_ITEM' },
  } as never);
  prepare?.();
  return { onTabChange, ...render(<TaskDetailScreen projectId="proj-1" projectName="platform-core" workItemId="wi-1" activeTab={activeTab} onTabChange={onTabChange} />) };
}

describe('nodeOneLiner (V11-11)', () => {
  const now = Date.parse('2026-10-11T00:02:10Z');
  const entries = TIMELINE.map(e => (e.nodeRunId === 'nr-review' && e.kind === 'EXECUTION_ATTEMPT' ? { ...e, startedAt: START } : e));
  it('labels a finished, a failed and a running node', () => {
    expect(nodeOneLiner('nr-impl', entries as never, now)).toBe('✓ done · 41s');
    expect(nodeOneLiner('nr-quality', entries as never, now)).toBe('✗ SCOPE_VIOLATION');
    expect(nodeOneLiner('nr-review', entries as never, now)).toBe('● running 02:10');
    expect(nodeOneLiner(undefined, entries as never, now)).toBe('');
    expect(nodeOneLiner('nr-unknown', entries as never, now)).toBe('');
  });
});

describe('Graph & Timeline — node labels and the node pane (V11-11)', () => {
  beforeEach(() => vi.clearAllMocks());

  it('reads the state of each node without a click, and opens the pane for the node picked', async () => {
    setup('task-graph');
    await userEvent.click(await screen.findByRole('button', { name: /Accessible List/ }));
    expect(await screen.findByTestId('node-label-implement')).toHaveTextContent('✓ done · 41s');
    expect(screen.getByTestId('node-label-quality')).toHaveTextContent('✗ SCOPE_VIOLATION');
    expect(screen.getByTestId('node-label-review')).toHaveTextContent('● running');

    await userEvent.click(screen.getByTestId('node-label-quality').closest('button')!);
    const pane = await screen.findByTestId('node-detail');
    expect(api.getNodeRunDetail).toHaveBeenCalledWith('nr-quality', expect.objectContaining({ token: 'test-session-token' }));
    expect(await within(pane).findByTestId('command-result')).toHaveTextContent('Exit code 1');
    expect(await within(pane).findByTestId('node-failure')).toHaveTextContent('The agent changed files outside its scope');
  });
});

const EVIDENCE = [
  { evidenceId: 'e-1', projectId: 'proj-1', workItemId: 'wi-1', runId: 'run-1', nodeRunId: 'nr-impl', attemptId: 'att-impl', kind: 'AGENT_OUTPUT', verdict: 'RECORDED', revisionSetHash: 'sha256:aaaaaaaaaaaa', policyVersion: 'p', createdAt: START, nodeKey: 'implement', iteration: 1, attemptNumber: 1 },
  { evidenceId: 'e-2', projectId: 'proj-1', workItemId: 'wi-1', runId: 'run-1', nodeRunId: 'nr-quality', attemptId: 'att-q', kind: 'COMMAND_EXECUTION', verdict: 'FAILED', revisionSetHash: 'sha256:bbbbbbbbbbbb', policyVersion: 'p', createdAt: START, nodeKey: 'quality', iteration: 1, attemptNumber: 1 },
];

describe('Evidence — node column and filter (V11-11)', () => {
  it('names the node of each row and filters by it', async () => {
    setup('task-evidence', () => vi.mocked(api.listEvidence).mockResolvedValue({ items: EVIDENCE } as never));
    expect(await screen.findByTestId('evidence-node-e-1')).toHaveTextContent('implement · iteration 1 · attempt 1');
    expect(screen.getByTestId('evidence-node-e-2')).toHaveTextContent('quality');

    await userEvent.selectOptions(screen.getByLabelText('Filter evidence by node'), 'quality');
    expect(screen.queryByTestId('evidence-node-e-1')).not.toBeInTheDocument();
    expect(screen.getByTestId('evidence-node-e-2')).toBeInTheDocument();
  });
});

describe('Chat — the engine\'s activity messages (V11-11)', () => {
  it('marks them, links to the node, and the filter keeps only the operator\'s own messages', async () => {
    const onTabChange = vi.fn();
    const base = { projectId: 'proj-1', workItemId: 'wi-1', contentArtifactId: 'a', createdAt: START };
    setup('task-chat', () => {
      vi.mocked(api.listMessages).mockResolvedValue({
        items: [
          { ...base, messageId: 'm-user', sequence: 1, actor: 'operator', role: 'USER' },
          { ...base, messageId: 'm-act', sequence: 2, actor: 'aw', role: 'SYSTEM', displayOnly: true, nodeRunId: 'nr-quality', attemptId: 'att-q' },
        ],
      } as never);
      vi.mocked(messageApi.fetchMessageContent).mockImplementation(async (_p, _w, id) => ({
        contentType: 'text/plain', blob: new Blob([id === 'm-user' ? 'keep the schema frozen' : 'quality failed: scope'], { type: 'text/plain' }),
      }));
    }, onTabChange);

    const activity = await screen.findByTestId('activity-m-act');
    expect(within(activity).getByText('activity')).toBeInTheDocument();
    expect(await within(activity).findByText('quality failed: scope')).toBeInTheDocument();

    await userEvent.click(within(activity).getByRole('button', { name: 'view progress' }));
    expect(onTabChange).toHaveBeenCalledWith('task-graph', { projectId: 'proj-1', taskId: 'wi-1' });

    await userEvent.click(screen.getByRole('button', { name: 'only my messages' }));
    await waitFor(() => expect(screen.queryByTestId('activity-m-act')).not.toBeInTheDocument());
    expect(await screen.findByText('keep the schema frozen')).toBeInTheDocument();
  });

  it('shows no filter when there are no activity messages', async () => {
    setup('task-chat', () => vi.mocked(api.listMessages).mockResolvedValue({ items: [] } as never));
    await screen.findByText('No messages yet — start the conversation below.');
    expect(screen.queryByRole('group', { name: 'Message filter' })).not.toBeInTheDocument();
  });
});
