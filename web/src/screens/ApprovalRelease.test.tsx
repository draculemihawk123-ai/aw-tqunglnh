import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { expectNoAxeViolations } from '../test/axe';
import * as api from '../api/generated';
import { TaskDetailScreen } from './TaskDetail';

// V11-07 — approve and commit in one action: the dialog an approval node's release policy opens, and the
// status of the commits it requested.

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return {
    ...actual,
    getWorkItem: vi.fn(), getTaskFamily: vi.fn(), getWorkItemProjectedDetail: vi.fn(), getRunDiagnostics: vi.fn(),
    getRunGraph: vi.fn(), getRunTimeline: vi.fn(), getRunDetail: vi.fn(), resolveApproval: vi.fn(),
    getWorkspaceSetState: vi.fn(), getWorkspaceDiff: vi.fn(), getWorkspaceRepositoryLog: vi.fn(), requestWorkspaceReconciliation: vi.fn(),
    listFamilyScopeExpansionRequests: vi.fn(), getFamilyReleaseContext: vi.fn(), getApprovalRelease: vi.fn(), restoreParkedChanges: vi.fn(),
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
const REPO = {
  repositoryWorkspaceId: 'rw-1', workspaceSetId: 'ws-1', repositoryId: 'contracts', generation: 1, state: 'READY', version: 7,
  branchRef: 'main', baseRevision: 'aaaa1111aaaa', currentRevision: 'aaaa1111aaaa', hasActiveWriteLease: false, validActions: [],
};
const NODES = [{ key: 'start', type: 'START', outcomes: ['go'] }, { key: 'review', type: 'APPROVAL', outcomes: ['approved', 'rejected'] }, { key: 'end', type: 'END' }];
const EDGES = [{ key: 'e1', from: 'start', outcome: 'go', to: 'review' }, { key: 'e2', from: 'review', outcome: 'approved', to: 'end' }];

const DIFF = {
  baseRevision: {}, resultRevision: {}, uncommitted: true, totalFiles: 3, changeSetDigest: 'digest-1', byteLimit: 1000, fileLimit: 100,
  filesTruncated: false, patchTruncated: false, patch: '',
  files: [
    { path: 'spec/openapi.json', additions: 12, deletions: 0, binary: false, status: 'ADDED', untracked: true, sizeBytes: 400, contentHash: 'h1' },
    { path: 'spec/README.md', additions: 3, deletions: 1, binary: false, status: 'MODIFIED', sizeBytes: 90, contentHash: 'h2' },
    { path: 'notes.md', additions: 1, deletions: 0, binary: false, status: 'ADDED', untracked: true, sizeBytes: 9, contentHash: 'h3' },
  ],
};
const IDENTITY_OK = { name: 'Reviewer', email: 'reviewer@example.test', nameSource: 'global', emailSource: 'global', ready: true };

function context(identity: object = IDENTITY_OK) {
  return { familyId: 'fam-1', repositories: [{ repositoryId: 'contracts', repositoryWorkspaceId: 'rw-1', version: 7, state: 'READY', identity }], parked: [] } as never;
}

function setup(opts: { release?: object | null; approvalState?: string; diff?: object; identity?: object } = {}) {
  vi.clearAllMocks();
  vi.mocked(api.getTaskFamily).mockResolvedValue(FAMILY as never);
  vi.mocked(api.getWorkItem).mockResolvedValue({ workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', kind: 'ROOT', title: 'Contract: issue API', status: 'ACTIVE', version: 4, contract: null } as never);
  vi.mocked(api.getWorkItemProjectedDetail).mockResolvedValue({
    card: { workItemId: 'wi-1', projectId: 'proj-1', familyId: 'fam-1', title: 'Contract: issue API', isRoot: true, status: 'ACTIVE', blockerCount: 0, pendingScopeExpansionCount: 0, repositoryBadges: [], activeRunId: 'run-1' },
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
  const release = opts.release === undefined ? { commit: 'OFFERED', outcomes: ['approved'] } : opts.release;
  vi.mocked(api.getRunDetail).mockResolvedValue({
    approvalRequests: [{
      approvalRequestId: 'appr-1', nodeRunId: 'nr-review', nodeKey: 'review', state: opts.approvalState ?? 'PENDING', version: 1,
      authorizedRoles: ['operator'], dueAt: '2026-10-11T00:00:00Z', escalationOutcome: 'rejected', ...(release ? { release } : {}),
    }],
  } as never);
  vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
    workspaceSetId: 'ws-1', familyId: 'fam-1', projectId: 'proj-1', state: 'READY', version: 2, hasBaseRevisionSet: true, repositoryWorkspaces: [REPO], validActions: [],
  } as never);
  vi.mocked(api.getWorkspaceDiff).mockResolvedValue((opts.diff ?? DIFF) as never);
  vi.mocked(api.getFamilyReleaseContext).mockResolvedValue(context(opts.identity));
  vi.mocked(api.resolveApproval).mockResolvedValue({ approvalRequestId: 'appr-1', state: 'DECIDED', won: true, advanced: true, nextNodeKey: 'end', validActions: [] } as never);
  vi.mocked(api.getApprovalRelease).mockResolvedValue({ approvalRequestId: 'appr-1', projectId: 'proj-1', operations: [] } as never);
  return render(<TaskDetailScreen projectId="proj-1" projectName="platform-core" workItemId="wi-1" activeTab="task-graph" onTabChange={vi.fn()} />);
}

async function openDialog(outcome = 'approved') {
  await userEvent.click(await screen.findByRole('button', { name: outcome }));
  return screen.findByRole('dialog');
}

describe('Approval card — decide and commit (V11-07)', () => {
  beforeEach(() => vi.clearAllMocks());

  it('opens the commit dialog for an outcome the policy offers instead of resolving at once, with all files ticked', async () => {
    setup();
    const dialog = await openDialog();
    expect(api.resolveApproval).not.toHaveBeenCalled();
    // OFFERED: committing is a choice, off until ticked
    const commitBox = within(dialog).getByRole('checkbox', { name: /Commit the worktree's changes/ });
    expect(commitBox).not.toBeChecked();
    await userEvent.click(commitBox);
    expect(await within(dialog).findByText(/Author:/)).toHaveTextContent('Reviewer <reviewer@example.test> (git config, global)');
    expect(await within(dialog).findByRole('checkbox', { name: 'spec/openapi.json' })).toBeChecked();
    expect(within(dialog).getByRole('checkbox', { name: 'notes.md' })).toBeChecked();
  });

  it('commits exactly the chosen files with the status and hash the approver saw, and PARK for the rest', async () => {
    setup();
    const dialog = await openDialog();
    await userEvent.click(within(dialog).getByRole('checkbox', { name: /Commit the worktree's changes/ }));
    await userEvent.click(await within(dialog).findByRole('checkbox', { name: 'notes.md' })); // untick one
    await userEvent.click(within(dialog).getByRole('radio', { name: /Park/ }));
    const submit = within(dialog).getByRole('button', { name: 'approved and commit 2 files' });
    await userEvent.click(submit);

    await waitFor(() => expect(api.resolveApproval).toHaveBeenCalledTimes(1));
    const [runId, approvalId, body, opts] = vi.mocked(api.resolveApproval).mock.calls[0] as unknown as [string, string, { outcome: string; commit: unknown }, { ifMatch: string }];
    expect([runId, approvalId, opts.ifMatch]).toEqual(['run-1', 'appr-1', '"1"']);
    expect(body).toEqual({
      outcome: 'approved',
      commit: {
        message: 'Contract: issue API',
        repositories: [{
          repositoryWorkspaceId: 'rw-1', expectedWorkspaceVersion: 7, unselected: 'PARK',
          files: [
            { path: 'spec/openapi.json', status: 'ADDED', contentHash: 'h1' },
            { path: 'spec/README.md', status: 'MODIFIED', contentHash: 'h2' },
          ],
        }],
      },
    });
  });

  it('a directory box is tri-state and moves all of its files', async () => {
    setup();
    const dialog = await openDialog();
    await userEvent.click(within(dialog).getByRole('checkbox', { name: /Commit the worktree's changes/ }));
    const dir = await within(dialog).findByRole('checkbox', { name: 'spec/' });
    expect(dir).toBeChecked();
    await userEvent.click(within(dialog).getByRole('checkbox', { name: 'spec/README.md' }));
    expect((dir as HTMLInputElement).indeterminate).toBe(true);
    await userEvent.click(dir); // some -> all
    expect(within(dialog).getByRole('checkbox', { name: 'spec/README.md' })).toBeChecked();
    await userEvent.click(dir); // all -> none
    expect(within(dialog).getByRole('checkbox', { name: 'spec/openapi.json' })).not.toBeChecked();
    await userEvent.click(within(dialog).getByRole('checkbox', { name: 'notes.md' }));
    expect(within(dialog).getByText(/Select at least one file/)).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'approved' })).toBeDisabled();
  });

  it('warns that kept files block the next read-only node', async () => {
    setup();
    const dialog = await openDialog();
    await userEvent.click(within(dialog).getByRole('checkbox', { name: /Commit the worktree's changes/ }));
    await userEvent.click(await within(dialog).findByRole('checkbox', { name: 'notes.md' }));
    expect(within(dialog).getByText(/1 file will stay uncommitted/)).toBeInTheDocument();
  });

  it('a missing git author disables the commit and shows the commands that fix it', async () => {
    setup({ identity: { ready: false, problem: 'git user.email is not set', name: 'Only A Name' } });
    const dialog = await openDialog();
    await userEvent.click(within(dialog).getByRole('checkbox', { name: /Commit the worktree's changes/ }));
    const alert = await within(dialog).findByRole('alert');
    expect(alert).toHaveTextContent('git user.email is not set');
    expect(alert).toHaveTextContent('git config --global user.name');
    expect(within(dialog).getByRole('button', { name: 'approved and commit 3 files' })).toBeDisabled();
    // deciding without committing stays possible
    await userEvent.click(within(dialog).getByRole('checkbox', { name: /Commit the worktree's changes/ }));
    await userEvent.click(within(dialog).getByRole('button', { name: 'approved' }));
    await waitFor(() => expect(api.resolveApproval).toHaveBeenCalledTimes(1));
    expect(vi.mocked(api.resolveApproval).mock.calls[0][2]).toEqual({ outcome: 'approved' });
  });

  it('an outcome the policy does not offer resolves immediately, without a dialog', async () => {
    setup();
    await userEvent.click(await screen.findByRole('button', { name: 'rejected' }));
    await waitFor(() => expect(api.resolveApproval).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(vi.mocked(api.resolveApproval).mock.calls[0][2]).toEqual({ outcome: 'rejected' });
  });

  it('a node without a release policy behaves as before', async () => {
    setup({ release: null });
    await userEvent.click(await screen.findByRole('button', { name: 'approved' }));
    await waitFor(() => expect(api.resolveApproval).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('REQUIRED: the commit decision cannot be skipped, and an empty worktree is answered as nothing to commit', async () => {
    setup({ release: { commit: 'REQUIRED', outcomes: ['approved'] }, diff: { ...DIFF, totalFiles: 0, files: [] } });
    const dialog = await openDialog();
    expect(within(dialog).queryByRole('checkbox', { name: /Commit the worktree's changes/ })).not.toBeInTheDocument();
    expect(await within(dialog).findByText(/nothing will be committed/)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole('button', { name: 'approved' }));
    await waitFor(() => expect(api.resolveApproval).toHaveBeenCalledTimes(1));
    expect(vi.mocked(api.resolveApproval).mock.calls[0][2]).toEqual({ outcome: 'approved', commit: { message: 'Contract: issue API', repositories: [] } });
  });

  it('shows a refused decision inside the dialog and keeps it open', async () => {
    setup();
    vi.mocked(api.resolveApproval).mockRejectedValue(new api.ApiError(422, 'INVALID_REQUEST', 'COMMIT_IDENTITY_MISSING: git is not configured with an author for contracts'));
    const dialog = await openDialog();
    await userEvent.click(within(dialog).getByRole('checkbox', { name: /Commit the worktree's changes/ }));
    await userEvent.click(await within(dialog).findByRole('button', { name: 'approved and commit 3 files' }));
    expect((await screen.findAllByText(/COMMIT_IDENTITY_MISSING/)).length).toBeGreaterThan(0);
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });

  it('has no automated accessibility violations with the dialog open', async () => {
    const { container } = setup();
    const dialog = await openDialog();
    await userEvent.click(within(dialog).getByRole('checkbox', { name: /Commit the worktree's changes/ }));
    await within(dialog).findByRole('checkbox', { name: 'notes.md' });
    await expectNoAxeViolations(container);
  });
});

describe('Release status of a decided approval (V11-07)', () => {
  const op = (over: object) => ({
    releaseSetLocalCommitId: 'op-1', releaseSetId: 'rs-1', repositoryId: 'contracts', repositoryWorkspaceId: 'rw-1', state: 'COMMITTED',
    resultVcsObjectId: 'cafebabe0123456789', files: ['spec/openapi.json'], changeSetDigest: 'd', unselected: 'KEEP', ...over,
  });

  it('shows a committed operation', async () => {
    setup({ approvalState: 'DECIDED' });
    vi.mocked(api.getApprovalRelease).mockResolvedValue({ approvalRequestId: 'appr-1', projectId: 'proj-1', operations: [op({})] } as never);
    expect(await screen.findByText(/committed cafebabe01 · 1 file/)).toBeInTheDocument();
  });

  it('explains a drift failure and offers to review the changes', async () => {
    setup({ approvalState: 'DECIDED' });
    vi.mocked(api.getApprovalRelease).mockResolvedValue({
      approvalRequestId: 'appr-1', projectId: 'proj-1', operations: [op({ state: 'FAILED', failureReason: 'CHANGESET_DRIFT', resultVcsObjectId: undefined })],
    } as never);
    expect(await screen.findByText(/a reviewed file changed after the review/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Review changes' })).toBeInTheDocument();
  });

  it('puts parked files back', async () => {
    setup({ approvalState: 'DECIDED' });
    vi.mocked(api.getApprovalRelease).mockResolvedValue({
      approvalRequestId: 'appr-1', projectId: 'proj-1',
      operations: [op({ unselected: 'PARK', parkedRef: 'refs/agentkit/parked/rs-1', parkedPaths: ['scratch/a.txt', 'scratch/b.txt'] })],
    } as never);
    vi.mocked(api.restoreParkedChanges).mockResolvedValue({ releaseSetLocalCommitId: 'op-1', ref: 'refs/agentkit/parked/rs-1', restoredPaths: ['scratch/a.txt', 'scratch/b.txt'] } as never);
    await userEvent.click(await screen.findByRole('button', { name: 'Restore 2 parked files' }));
    await waitFor(() => expect(api.restoreParkedChanges).toHaveBeenCalledTimes(1));
    expect(vi.mocked(api.restoreParkedChanges).mock.calls[0].slice(0, 3)).toEqual(['proj-1', 'rw-1', { releaseSetLocalCommitId: 'op-1' }]);
  });

  it('shows nothing for a node with no release policy', async () => {
    setup({ approvalState: 'DECIDED', release: null });
    await screen.findByText('Workflow Graph');
    expect(api.getApprovalRelease).not.toHaveBeenCalled();
  });
});
