import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { TaskDetailScreen } from './TaskDetail';

// V9-06 / ADR-033 (gap G6): a failed Run no longer forces a new WorkItem. The
// task detail shows how many Runs the WorkItem has had and lists them, offers
// "Resolve" for the RUN_FAILED blocker (RESOLVED only, never WAIVED), and, once
// the WorkItem is READY again, "Start Run" on the same WorkItem.

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return {
    ...actual,
    getWorkItem: vi.fn(), getTaskFamily: vi.fn(), getWorkItemProjectedDetail: vi.fn(), getRunDiagnostics: vi.fn(),
    cancelRun: vi.fn(), cancelWorkItem: vi.fn(), resolveWorkItemBlocker: vi.fn(), startWorkflowRun: vi.fn(),
    getRunGraph: vi.fn(), getRunTimeline: vi.fn(), getRunDetail: vi.fn(),
  };
});
vi.mock('../api/session', () => ({
  withSessionToken: (opts: object = {}) => ({ ...opts, token: 'test-session-token' }),
  getSessionToken: () => 'test-session-token',
}));

function render(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>);
}

const FAMILY = { familyId: 'fam-1', projectId: 'proj-1', rootWorkItemId: 'wi-1', scopeVersion: 1, status: 'ACTIVE', version: 1 };

const RUNS = [
  { runId: 'run-1', runNumber: 1, state: 'FAILED', workflowVersionId: 'wfv-1', startedAt: '2026-10-02T00:00:00Z', finishedAt: '2026-10-02T00:05:00Z' },
  { runId: 'run-2', runNumber: 2, state: 'FAILED', workflowVersionId: 'wfv-1', startedAt: '2026-10-02T01:00:00Z', finishedAt: '2026-10-02T01:05:00Z' },
];

function workItem(status: string) {
  return {
    workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Add distributed tracing',
    status, version: 5, workflowVersionId: 'wfv-1', contract: null,
  };
}

function detail(card: Record<string, unknown>, runs: unknown[] = RUNS) {
  return {
    card: {
      workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', title: 'Add distributed tracing', isRoot: true,
      blockerCount: 0, pendingScopeExpansionCount: 0, runCount: runs.length, ...card,
    },
    readiness: { workItemId: 'wi-1', status: 'BLOCKED', version: 5, ready: false },
    runs,
    freshness: { generation: 1, asOfJournalPosition: 5, status: 'LIVE' },
    validActions: [],
  };
}

function runFailedDiagnostics() {
  return {
    runId: 'run-2', projectId: 'proj-1', workItemId: 'wi-1', workItemStatus: 'BLOCKED', runState: 'FAILED',
    blockers: [{
      blockerId: 'run-2-run-failed-blocker', type: 'RUN_FAILED', state: 'OPEN', reason: 'workflow run run-2 failed (RUN_FAILED)',
      openedAt: '2026-10-02T01:05:00Z', version: 1, admissionReason: false,
      validActions: [{ operationId: 'resolveWorkItemBlocker', scopeKind: 'PROJECT', targetVersion: 1 }],
    }],
    orphanedAttempts: [], orphanedAttemptsTruncated: false, providers: [], isolation: [], repositoryWorkspaces: [],
    validActions: [{ operationId: 'cancelWorkItem', scopeKind: 'PROJECT', targetVersion: 0 }],
  };
}

function renderScreen() {
  return render(<TaskDetailScreen projectId="proj-1" projectName="platform-core" workItemId="wi-1" activeTab="task-overview" onTabChange={vi.fn()} />);
}

describe('TaskDetailScreen — rerun after a failed run (V9-06)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getTaskFamily).mockResolvedValue(FAMILY as never);
  });

  it('shows how many runs the WorkItem has had and lists every one of them, oldest first', async () => {
    vi.mocked(api.getWorkItem).mockResolvedValue(workItem('BLOCKED') as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue(detail({ status: 'BLOCKED', activeRunId: 'run-2', activeRunStatus: 'FAILED' }) as never);
    vi.mocked(api.getRunDiagnostics).mockResolvedValue(runFailedDiagnostics() as never);
    renderScreen();

    expect(await screen.findByTestId('run-count')).toHaveTextContent('2 runs');
    const list = await screen.findByRole('list', { name: 'Runs of this WorkItem' });
    const entries = within(list).getAllByRole('listitem');
    expect(entries).toHaveLength(2);
    expect(entries[0]).toHaveTextContent('Run 1');
    expect(entries[0]).toHaveTextContent('run-1');
    expect(entries[1]).toHaveTextContent('Run 2');
    expect(entries[1]).toHaveTextContent('run-2');
  });

  it('a WorkItem that never ran shows no count and says so', async () => {
    vi.mocked(api.getWorkItem).mockResolvedValue(workItem('READY') as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue(detail({ status: 'READY' }, []) as never);
    renderScreen();

    expect(await screen.findByText('This WorkItem has not been run yet.')).toBeInTheDocument();
    expect(screen.queryByTestId('run-count')).not.toBeInTheDocument();
  });

  it('a RUN_FAILED blocker is resolved with mode RESOLVED, and the dialog offers no WAIVED mode', async () => {
    vi.mocked(api.getWorkItem).mockResolvedValue(workItem('BLOCKED') as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue(detail({ status: 'BLOCKED', activeRunId: 'run-2', activeRunStatus: 'FAILED' }) as never);
    vi.mocked(api.getRunDiagnostics).mockResolvedValue(runFailedDiagnostics() as never);
    vi.mocked(api.resolveWorkItemBlocker).mockResolvedValue({
      blockerId: 'run-2-run-failed-blocker', alreadyResolved: false, state: 'RESOLVED', workItemUnblocked: true, workItemStatus: 'READY',
    } as never);
    renderScreen();

    await userEvent.click(await screen.findByRole('button', { name: 'Resolve' }));
    const mode = screen.getByLabelText(/Mode/) as HTMLSelectElement;
    expect(Array.from(mode.options).map(o => o.value)).toEqual(['RESOLVED']);
    expect(screen.queryByLabelText(/Policy grant reference/)).not.toBeInTheDocument();
    expect(screen.getByText(/A failed Run cannot be waived/)).toBeInTheDocument();

    await userEvent.type(screen.getByLabelText(/^Reason/), 'looked at the failure, undid the half-done edit');
    await userEvent.click(screen.getByRole('button', { name: 'Confirm' }));

    await waitFor(() => expect(api.resolveWorkItemBlocker).toHaveBeenCalledWith(
      'run-2-run-failed-blocker',
      { mode: 'RESOLVED', reason: 'looked at the failure, undid the half-done edit', policyGrantRef: undefined },
      expect.objectContaining({ token: 'test-session-token' }),
    ));
  });

  it('a waivable blocker type still offers WAIVED', async () => {
    vi.mocked(api.getWorkItem).mockResolvedValue(workItem('BLOCKED') as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue(detail({ status: 'BLOCKED', activeRunId: 'run-2', activeRunStatus: 'FAILED' }) as never);
    const diagnostics = runFailedDiagnostics();
    diagnostics.blockers[0].type = 'RUN_CANCELLED';
    vi.mocked(api.getRunDiagnostics).mockResolvedValue(diagnostics as never);
    renderScreen();

    await userEvent.click(await screen.findByRole('button', { name: 'Resolve' }));
    const mode = screen.getByLabelText(/Mode/) as HTMLSelectElement;
    expect(Array.from(mode.options).map(o => o.value)).toEqual(['RESOLVED', 'WAIVED']);
  });

  it('once the blocker is resolved the WorkItem is READY and Start Run starts the next run with the pinned workflow version, even while the card still names the failed run', async () => {
    vi.mocked(api.getWorkItem).mockResolvedValue(workItem('READY') as never);
    // A card from before the projection caught up: still pointing at the failed run.
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue(detail({ status: 'BLOCKED', activeRunId: 'run-2', activeRunStatus: 'FAILED' }) as never);
    vi.mocked(api.getRunDiagnostics).mockResolvedValue({ ...runFailedDiagnostics(), workItemStatus: 'READY', blockers: [] } as never);
    vi.mocked(api.startWorkflowRun).mockResolvedValue({
      runId: 'run-3', projectId: 'proj-1', workItemId: 'wi-1', familyId: 'fam-1', state: 'RUNNING', nodeRunId: 'nr-3', jobId: 'job-3', validActions: [],
    } as never);
    renderScreen();

    await userEvent.click(await screen.findByRole('button', { name: 'Start Run' }));

    await waitFor(() => expect(api.startWorkflowRun).toHaveBeenCalledWith(
      'wi-1', { workflowVersionId: 'wfv-1' }, expect.objectContaining({ token: 'test-session-token', idempotencyKey: expect.any(String) }),
    ));
    expect(await screen.findByText(/Run started \(run-3\)/)).toBeInTheDocument();
  });

  it('a live run on the card still hides Start Run', async () => {
    vi.mocked(api.getWorkItem).mockResolvedValue(workItem('ACTIVE') as never);
    vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue(detail({ status: 'ACTIVE', activeRunId: 'run-3', activeRunStatus: 'ACTIVE' }) as never);
    vi.mocked(api.getRunDiagnostics).mockResolvedValue({
      ...runFailedDiagnostics(), runId: 'run-3', runState: 'RUNNING', workItemStatus: 'ACTIVE', blockers: [],
      validActions: [{ operationId: 'cancelRun', scopeKind: 'PROJECT', targetVersion: 0 }],
    } as never);
    renderScreen();

    await screen.findByRole('button', { name: 'Cancel Run' });
    expect(screen.queryByRole('button', { name: 'Start Run' })).not.toBeInTheDocument();
  });
});
