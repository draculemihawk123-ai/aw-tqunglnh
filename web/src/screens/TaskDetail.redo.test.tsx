import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import { TaskDetailScreen } from './TaskDetail';

// V11-10 / ADR-041 — "Run Again" and "Redo with Latest Version": two buttons advertised by the server's validActions, each opening a
// dialog that shows what would happen (from getWorkItemRedoPreview) and sends exactly one command.

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return {
    ...actual,
    getWorkItem: vi.fn(), getTaskFamily: vi.fn(), getWorkItemProjectedDetail: vi.fn(), getRunDiagnostics: vi.fn(),
    getRunGraph: vi.fn(), getRunTimeline: vi.fn(), getRunDetail: vi.fn(),
    getWorkItemRedoPreview: vi.fn(), retryWorkItem: vi.fn(), recreateWorkItem: vi.fn(),
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

const FAMILY = { familyId: 'fam-1', projectId: 'proj-1', rootWorkItemId: 'wi-root', scopeVersion: 1, status: 'ACTIVE', version: 1 };

function diagnostics(validActions: string[]) {
  return {
    runId: 'run-1', projectId: 'proj-1', workItemId: 'wi-1', workItemStatus: 'BLOCKED', runState: 'FAILED',
    blockers: [], orphanedAttempts: [], orphanedAttemptsTruncated: false, providers: [], isolation: [], repositoryWorkspaces: [],
    validActions: validActions.map(operationId => ({ operationId, scopeKind: 'PROJECT', targetVersion: 4 })),
  };
}

function preview(over: Record<string, unknown> = {}) {
  return {
    workItemId: 'wi-1', projectId: 'proj-1', title: 'T-01: add the endpoint', status: 'BLOCKED', isChild: true,
    canRetry: true, canRecreate: true,
    pinnedWorkflowVersion: { workflowVersionId: 'wfv-1', versionNumber: 1 },
    latestWorkflowVersion: { workflowVersionId: 'wfv-2', versionNumber: 2 },
    newerVersionAvailable: true,
    blockersToResolve: [{ blockerId: 'b-1', type: 'RUN_FAILED', reason: 'workflow run run-1 failed' }],
    operatorMessageCount: 2, redoNumber: 2, priorRunCount: 1, priorCostUsd: 0.36, costKnown: true,
    files: [], outOfScopeCount: 0,
    ...over,
  };
}

function setup(opts: { validActions?: string[]; card?: Record<string, unknown>; preview?: Record<string, unknown> } = {}) {
  vi.clearAllMocks();
  vi.mocked(api.getTaskFamily).mockResolvedValue(FAMILY as never);
  vi.mocked(api.getWorkItem).mockResolvedValue({
    workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'CHILD', title: 'T-01: add the endpoint', status: 'BLOCKED', version: 4,
    workflowVersionId: 'wfv-1', contract: null,
  } as never);
  vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({
    card: {
      workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', title: 'T-01: add the endpoint', isRoot: false, status: 'BLOCKED',
      activeRunId: 'run-1', activeRunStatus: 'FAILED', blockerCount: 1, pendingScopeExpansionCount: 0, runCount: 1, ...opts.card,
    },
    readiness: { workItemId: 'wi-1', status: 'BLOCKED', version: 4, ready: false }, runs: [],
    freshness: { generation: 1, asOfJournalPosition: 1, status: 'LIVE' }, validActions: [],
  } as never);
  vi.mocked(api.getRunDiagnostics).mockResolvedValue(diagnostics(opts.validActions ?? ['retryWorkItem', 'recreateWorkItem', 'cancelWorkItem']) as never);
  vi.mocked(api.getWorkItemRedoPreview).mockResolvedValue(preview(opts.preview) as never);
  const onTabChange = vi.fn();
  const view = render(<TaskDetailScreen projectId="proj-1" projectName="platform-core" workItemId="wi-1" activeTab="task-overview" onTabChange={onTabChange} />);
  return { onTabChange, ...view };
}

describe('TaskDetailScreen — run again and redo (V11-10)', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows both buttons only when the server advertises them', async () => {
    setup({ validActions: ['cancelWorkItem'] });
    await screen.findByRole('button', { name: 'Cancel WorkItem' });
    expect(screen.queryByRole('button', { name: 'Run Again' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Redo with Latest Version' })).not.toBeInTheDocument();
  });

  it('shows Run Again alone for a root work item (no redo is advertised)', async () => {
    setup({ validActions: ['retryWorkItem', 'cancelWorkItem'] });
    expect(await screen.findByRole('button', { name: 'Run Again' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Redo with Latest Version' })).not.toBeInTheDocument();
  });

  it('Run Again: previews, sends the note once and reports the new run', async () => {
    setup();
    vi.mocked(api.retryWorkItem).mockResolvedValue({
      workItemId: 'wi-1', previousRunId: 'run-1', runId: 'run-2', runState: 'RUNNING', nodeRunId: 'n', jobId: 'j',
      workflowVersionId: 'wfv-1', resolvedBlockerIds: ['b-1'],
    } as never);
    await userEvent.click(await screen.findByRole('button', { name: 'Run Again' }));

    const dialog = await screen.findByRole('dialog', { name: 'Run Again' });
    expect(await within(dialog).findByText('wfv-1')).toBeInTheDocument();           // the pinned version it will run with
    expect(within(dialog).getByText('RUN_FAILED')).toBeInTheDocument();             // the blocker it will resolve
    expect(within(dialog).getByText(/cost \$0\.36/)).toBeInTheDocument();           // what the earlier run cost
    expect(within(dialog).getByTestId('newer-version-hint')).toHaveTextContent('version 2');

    await userEvent.type(within(dialog).getByLabelText('Note for the agent'), 'use the fixed script');
    await userEvent.click(within(dialog).getByRole('button', { name: 'Run Again' }));

    await waitFor(() => expect(api.retryWorkItem).toHaveBeenCalledTimes(1));
    expect(api.retryWorkItem).toHaveBeenCalledWith(
      'wi-1', { note: 'use the fixed script', outOfScope: undefined },
      expect.objectContaining({ token: 'test-session-token', idempotencyKey: expect.any(String) }),
    );
    expect(await screen.findByText(/Run started \(run-2\)/)).toBeInTheDocument();
  });

  it('Run Again: files outside the scope need a choice, KEEP is preselected and PARK is sent when chosen', async () => {
    setup({
      preview: {
        files: [
          { repositoryId: 'web', repositoryWorkspaceId: 'rw-1', path: 'src/app.ts', status: 'MODIFIED', outOfScope: false },
          { repositoryId: 'web', repositoryWorkspaceId: 'rw-1', path: 'docs/needs-info.md', status: 'ADDED', outOfScope: true },
        ],
        outOfScopeCount: 1,
      },
    });
    vi.mocked(api.retryWorkItem).mockResolvedValue({
      workItemId: 'wi-1', previousRunId: 'run-1', runId: 'run-2', runState: 'RUNNING', nodeRunId: 'n', jobId: 'j', workflowVersionId: 'wfv-1', resolvedBlockerIds: [],
    } as never);
    await userEvent.click(await screen.findByRole('button', { name: 'Run Again' }));
    const dialog = await screen.findByRole('dialog', { name: 'Run Again' });

    const files = await within(dialog).findByTestId('leftover-files');
    expect(within(files).getByText(/docs\/needs-info\.md/).closest('li')).toHaveTextContent('outside scope');
    expect(within(files).getByText(/src\/app\.ts/).closest('li')).not.toHaveTextContent('outside scope');
    expect(within(files).getByRole('radio', { name: /Keep/ })).toBeChecked();

    await userEvent.click(within(files).getByRole('radio', { name: /Park/ }));
    await userEvent.click(within(dialog).getByRole('button', { name: 'Run Again' }));
    await waitFor(() => expect(api.retryWorkItem).toHaveBeenCalledWith(
      'wi-1', { note: undefined, outOfScope: 'PARK' }, expect.anything(),
    ));
  });

  it('explains a refusal in words and disables the button', async () => {
    setup({ preview: { canRetry: false, retryRefusal: { code: 'WORKSPACE_QUARANTINED', message: 'quarantined' } } });
    await userEvent.click(await screen.findByRole('button', { name: 'Run Again' }));
    const dialog = await screen.findByRole('dialog', { name: 'Run Again' });
    expect(await within(dialog).findByTestId('refusal')).toHaveTextContent('QUARANTINED');
    expect(within(dialog).getByRole('button', { name: 'Run Again' })).toBeDisabled();
  });

  it('warns from the third attempt on', async () => {
    setup({ preview: { priorRunCount: 2 } });
    await userEvent.click(await screen.findByRole('button', { name: 'Run Again' }));
    const dialog = await screen.findByRole('dialog', { name: 'Run Again' });
    expect(await within(dialog).findByTestId('attempt-warning')).toHaveTextContent('attempt 3');
  });

  it('Redo: shows both versions, needs a reason, carries notes by default and opens the new work item', async () => {
    const { onTabChange } = setup();
    vi.mocked(api.recreateWorkItem).mockResolvedValue({
      oldWorkItemId: 'wi-1', newWorkItemId: 'wi-2', parentWorkItemId: 'wi-root', title: 'T-01: add the endpoint (lần 2)', redoNumber: 2,
      previousWorkflowVersionId: 'wfv-1', workflowVersionId: 'wfv-2', runId: 'run-2', runState: 'RUNNING', nodeRunId: 'n', jobId: 'j',
      oldWorkItemStatus: 'CANCELLED', carriedMessageCount: 2,
    } as never);
    await userEvent.click(await screen.findByRole('button', { name: 'Redo with Latest Version' }));

    const dialog = await screen.findByRole('dialog', { name: 'Redo with Latest Version' });
    expect(await within(dialog).findByText('wfv-1')).toBeInTheDocument();
    expect(within(dialog).getByText('wfv-2')).toBeInTheDocument();
    const carry = within(dialog).getByRole('checkbox', { name: /Carry over my 2 notes/ });
    expect(carry).toBeChecked();
    const submit = within(dialog).getByRole('button', { name: 'Cancel and Redo' });
    expect(submit).toBeDisabled(); // no reason yet

    await userEvent.type(within(dialog).getByLabelText(/^Reason/), 'the kit was fixed');
    expect(submit).toBeEnabled();
    await userEvent.click(submit);

    await waitFor(() => expect(api.recreateWorkItem).toHaveBeenCalledTimes(1));
    expect(api.recreateWorkItem).toHaveBeenCalledWith(
      'wi-1', { reason: 'the kit was fixed', note: undefined, carryMessages: true, outOfScope: undefined },
      expect.objectContaining({ idempotencyKey: expect.any(String) }),
    );
    await waitFor(() => expect(onTabChange).toHaveBeenCalledWith('task-overview', { projectId: 'proj-1', taskId: 'wi-2' }));
  });

  it('Redo: the notes can be left behind, and a missing newer version is said plainly', async () => {
    setup({ preview: { newerVersionAvailable: false, latestWorkflowVersion: { workflowVersionId: 'wfv-1', versionNumber: 1 } } });
    vi.mocked(api.recreateWorkItem).mockResolvedValue({ newWorkItemId: 'wi-2', workflowVersionId: 'wfv-1', oldWorkItemStatus: 'CANCELLED' } as never);
    await userEvent.click(await screen.findByRole('button', { name: 'Redo with Latest Version' }));
    const dialog = await screen.findByRole('dialog', { name: 'Redo with Latest Version' });
    expect(await within(dialog).findByTestId('same-version-hint')).toHaveTextContent('no newer version');

    await userEvent.click(within(dialog).getByRole('checkbox', { name: /Carry over/ }));
    await userEvent.type(within(dialog).getByLabelText(/^Reason/), 'clean start');
    await userEvent.click(within(dialog).getByRole('button', { name: 'Cancel and Redo' }));
    await waitFor(() => expect(api.recreateWorkItem).toHaveBeenCalledWith(
      'wi-1', expect.objectContaining({ carryMessages: false }), expect.anything(),
    ));
  });

  it('shows the work item this one replaced, and the one that replaced it', async () => {
    setup({ card: { supersedesWorkItemId: 'wi-0', supersededByWorkItemId: 'wi-9' }, validActions: ['cancelWorkItem'] });
    expect(await screen.findByTestId('supersedes-link')).toHaveTextContent('replaces wi-0');
    expect(screen.getByTestId('superseded-by-link')).toHaveTextContent('replaced by wi-9');
  });

  it('the dialog has no accessibility violations', async () => {
    setup();
    await userEvent.click(await screen.findByRole('button', { name: 'Redo with Latest Version' }));
    const dialog = await screen.findByRole('dialog', { name: 'Redo with Latest Version' });
    await within(dialog).findByText('wfv-2');
    await expectNoAxeViolations(dialog);
  });
});
