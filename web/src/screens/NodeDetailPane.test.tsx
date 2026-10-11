import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import { NodeDetailPane } from './NodeDetailPane';

// V11-11 / ADR-042 — the pane that opens when a graph node is picked: what the node was asked, what it is doing, what came of it,
// and why it failed.

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, getNodeRunDetail: vi.fn(), listAttemptEvents: vi.fn() };
});
vi.mock('../api/session', () => ({
  withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }),
  getSessionToken: () => 'test-session-token',
}));

function render(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

function detail(over: Record<string, unknown> = {}) {
  return {
    nodeRunId: 'nr-1', runId: 'run-1', workItemId: 'wi-1', projectId: 'proj-1', nodeKey: 'implement', nodeType: 'AGENT', state: 'FAILED',
    iteration: 1, activationSequence: 2,
    definition: { type: 'AGENT', role: 'MAKER', outcomes: ['done'], pins: [{ kind: 'agent_profile', definitionId: 'profile-def', versionId: 'profile-v1' }] },
    attempts: [{ kind: 'EXECUTION_ATTEMPT', activationSequence: 2, nodeRunId: 'nr-1', nodeKey: 'implement', iteration: 1, attemptId: 'att-1', attemptNumber: 1, attemptState: 'FAILED', failureCode: 'EXECUTION_FAILED' }],
    evidence: [{ evidenceId: 'att-1:AGENT_OUTPUT', attemptId: 'att-1', kind: 'AGENT_OUTPUT', verdict: 'RECORDED', createdAt: '2026-10-11T00:00:00Z', artifacts: [] }],
    result: { kind: 'AGENT', finalMessage: { text: 'I cannot find src/App.tsx, so I stopped.', partial: true }, changedFiles: [{ repositoryId: 'web', path: 'src/main.ts', status: 'MODIFIED' }] },
    progress: { attemptId: 'att-1', attemptState: 'FAILED', running: false, eventCount: 3, toolCalls: 1, messages: 1, lastKind: 'ASSISTANT_MESSAGE', lastActivity: 'said: I cannot find src/App.tsx' },
    failure: { code: 'EXECUTION_FAILED', title: 'The attempt ended with an error', explanation: 'The agent or command finished unsuccessfully.', suggestion: 'Read the output below.', action: 'RETRY_WORK_ITEM' },
    ...over,
  };
}

const EVENTS = {
  attemptId: 'att-1', nodeRunId: 'nr-1', attemptState: 'FAILED', finished: true, nextSequence: 3, hasMore: false,
  events: [
    { sequence: 1, kind: 'TOOL_CALL_STARTED', at: '2026-10-11T00:00:01Z', tool: { name: 'Read', input: '{"path":"spec.md"}' } },
    { sequence: 2, kind: 'TOOL_CALL_FINISHED', at: '2026-10-11T00:00:02Z', tool: { name: 'Read', output: 'file not found', isError: true } },
    { sequence: 3, kind: 'ASSISTANT_MESSAGE', at: '2026-10-11T00:00:03Z', message: 'I cannot find src/App.tsx, so I stopped. <b>not html</b>' },
  ],
};

function open(onOpenEvidence = vi.fn()) {
  return { onOpenEvidence, ...render(<NodeDetailPane nodeRunId="nr-1" onClose={vi.fn()} onOpenEvidence={onOpenEvidence} />) };
}

describe('NodeDetailPane (V11-11)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.listAttemptEvents).mockResolvedValue(EVENTS as never);
  });

  it('shows what the node was asked, what it did, what came of it and why it failed', async () => {
    vi.mocked(api.getNodeRunDetail).mockResolvedValue(detail() as never);
    open();

    const input = await screen.findByTestId('node-input');
    expect(within(input).getByText('MAKER')).toBeInTheDocument();
    expect(within(input).getByText(/profile-def/)).toBeInTheDocument();

    const progress = await screen.findByTestId('node-progress');
    expect((await within(progress).findAllByText(/Read/)).length).toBeGreaterThan(0);
    expect(within(progress).getByTestId('progress-line')).toHaveTextContent('1 tool call');
    // agent text is shown as text, never as markup
    expect(await within(progress).findByText(/<b>not html<\/b>/)).toBeInTheDocument();
    expect(progress.querySelector('b')).toBeNull();

    const result = screen.getByTestId('node-result');
    expect(within(result).getByText(/last words before it stopped/)).toBeInTheDocument();
    expect(within(result).getByText(/modified web:src\/main\.ts/)).toBeInTheDocument();

    const failure = screen.getByTestId('node-failure');
    expect(within(failure).getByRole('alert')).toHaveTextContent('The attempt ended with an error');
    expect(failure).toHaveTextContent('Use Run Again in the header.');
    expect(api.getNodeRunDetail).toHaveBeenCalledWith('nr-1', expect.objectContaining({ token: 'test-session-token' }));
  });

  it('a COMMAND node shows its exit code and output', async () => {
    vi.mocked(api.getNodeRunDetail).mockResolvedValue(detail({
      nodeKey: 'quality', nodeType: 'COMMAND', attempts: [], progress: undefined, failure: undefined,
      result: { kind: 'COMMAND', command: { exitCode: 2, durationMillis: 4300, stdout: 'ran 12 tests', stderr: '2 failed' } },
    }) as never);
    open();
    const command = await screen.findByTestId('command-result');
    expect(command).toHaveTextContent('Exit code 2');
    expect(command).toHaveTextContent('4s');
    expect(command).toHaveTextContent('ran 12 tests');
    expect(command).toHaveTextContent('2 failed');
  });

  it('a structural node is one line', async () => {
    vi.mocked(api.getNodeRunDetail).mockResolvedValue(detail({
      nodeKey: 'start', nodeType: 'START', state: 'SUCCEEDED', attempts: [], evidence: [], progress: undefined, failure: undefined,
      result: { kind: 'STRUCTURAL', note: 'passed through, took the go branch' },
    }) as never);
    open();
    expect(await screen.findByText('passed through, took the go branch')).toBeInTheDocument();
    expect(screen.queryByTestId('node-failure')).not.toBeInTheDocument();
    expect(screen.queryByTestId('node-progress')).not.toBeInTheDocument();
  });

  it('follows a running attempt: asks again from the last sequence until it finishes', async () => {
    vi.mocked(api.getNodeRunDetail).mockResolvedValue(detail({
      state: 'RUNNING', failure: undefined, attempts: [{ kind: 'EXECUTION_ATTEMPT', activationSequence: 2, nodeRunId: 'nr-1', nodeKey: 'implement', iteration: 1, attemptId: 'att-1', attemptNumber: 1, attemptState: 'RUNNING' }],
      progress: { attemptId: 'att-1', attemptState: 'RUNNING', running: true, eventCount: 1, toolCalls: 1, messages: 0, lastActivity: 'tool Read {"path":"spec.md"}' },
    }) as never);
    vi.mocked(api.listAttemptEvents)
      .mockResolvedValueOnce({ attemptId: 'att-1', nodeRunId: 'nr-1', attemptState: 'RUNNING', finished: false, hasMore: false, nextSequence: 1, events: [EVENTS.events[0]] } as never)
      .mockResolvedValueOnce({ attemptId: 'att-1', nodeRunId: 'nr-1', attemptState: 'SUCCEEDED', finished: true, hasMore: false, nextSequence: 3, events: [EVENTS.events[1], EVENTS.events[2]] } as never);
    open();

    const progress = await screen.findByTestId('node-progress');
    expect((await within(progress).findAllByText(/Read/)).length).toBeGreaterThan(0);
    expect(within(progress).getByTestId('progress-line')).toHaveTextContent('RUNNING');
    // the second ask names the sequence it left off at
    await waitFor(() => expect(api.listAttemptEvents).toHaveBeenCalledTimes(2), { timeout: 6000 });
    expect(vi.mocked(api.listAttemptEvents).mock.calls[1][1]).toEqual(expect.objectContaining({ query: { cursor: '1', limit: '200' } }));
    expect(await within(progress).findByText(/not html/)).toBeInTheDocument();
  }, 10000);

  it('offers the Evidence tab for the node', async () => {
    vi.mocked(api.getNodeRunDetail).mockResolvedValue(detail() as never);
    const { onOpenEvidence } = open();
    await userEvent.click(await screen.findByRole('button', { name: 'Evidence' }));
    expect(onOpenEvidence).toHaveBeenCalledWith('implement');
  });

  it('says so when the detail cannot be loaded', async () => {
    vi.mocked(api.getNodeRunDetail).mockRejectedValue(new api.ApiError(404, 'NOT_FOUND', 'the requested resource was not found'));
    open();
    expect(await screen.findByRole('alert')).toHaveTextContent('NOT_FOUND');
  });

  it('has no accessibility violations', async () => {
    vi.mocked(api.getNodeRunDetail).mockResolvedValue(detail() as never);
    open();
    const pane = await screen.findByTestId('node-detail');
    await within(pane).findByTestId('node-failure');
    await expectNoAxeViolations(pane);
  });
});
