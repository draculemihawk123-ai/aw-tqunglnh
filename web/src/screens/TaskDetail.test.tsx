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
