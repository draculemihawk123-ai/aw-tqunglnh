import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { RemotePanel } from './RemotePanel';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, getWorkspaceRemote: vi.fn(), pushWorkspaceBranch: vi.fn(), createWorkspacePullRequest: vi.fn(), mergeWorkspacePullRequest: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));

const REPO = { repositoryWorkspaceId: 'rw-1', workspaceSetId: 'ws', repositoryId: 'api', generation: 1, state: 'READY', version: 3, currentRevision: 'abc', hasActiveWriteLease: false, validActions: [] };
const base = { repositoryWorkspaceId: 'rw-1', baseBranch: 'main', remoteUrl: 'https://github.com/o/r.git', suggestedBranch: 'agentkit/w-1', head: 'abc12345', unapprovedCommits: [], noChecks: false, mergeBlockers: [] };

function renderPanel() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={client}><RemotePanel projectId="p" repo={REPO} defaultTitle="" /></QueryClientProvider>);
}

describe('RemotePanel (V12-28)', () => {
  beforeEach(() => vi.clearAllMocks());

  it('pushes under a name chosen at the first push, then opens a ready pull request', async () => {
    vi.mocked(api.getWorkspaceRemote)
      .mockResolvedValueOnce({ ...base, remoteBranch: '', pushed: false } as never)
      .mockResolvedValue({ ...base, remoteBranch: 'feature/x', pushed: true, pushedRevision: 'abc12345' } as never);
    vi.mocked(api.pushWorkspaceBranch).mockResolvedValue({} as never);
    vi.mocked(api.createWorkspacePullRequest).mockResolvedValue({} as never);
    renderPanel();
    await userEvent.click(await screen.findByRole('button', { name: 'Push…' }));
    const push = await screen.findByRole('dialog', { name: 'Push to GitHub' });
    await userEvent.type(within(push).getByLabelText('Remote branch'), 'feature/x');
    await userEvent.click(within(push).getByRole('button', { name: 'Push' }));
    await waitFor(() => expect(api.pushWorkspaceBranch).toHaveBeenCalledWith('p', 'rw-1', { remoteBranch: 'feature/x' }, expect.objectContaining({ idempotencyKey: expect.any(String) })));

    await userEvent.click(await screen.findByRole('button', { name: 'Open pull request…' }));
    const pr = await screen.findByRole('dialog', { name: 'Open a pull request' });
    await userEvent.type(within(pr).getByLabelText('Pull request title'), 'feat: x');
    await userEvent.click(within(pr).getByRole('button', { name: 'Open pull request' }));
    await waitFor(() => expect(api.createWorkspacePullRequest).toHaveBeenCalledWith('p', 'rw-1', { title: 'feat: x', body: '' }, expect.anything()));
  });

  it('merges a pull request without CI only after the acknowledgment, with a merge commit by default', async () => {
    vi.mocked(api.getWorkspaceRemote).mockResolvedValue({
      ...base, remoteBranch: 'feature/x', pushed: true, pushedRevision: 'abc12345', noChecks: true,
      pullRequest: { number: 5, url: 'https://github.com/o/r/pull/5', state: 'OPEN', draft: false, headSha: 'abc12345', base: 'main', head: 'feature/x', title: 't', checks: [] },
    } as never);
    vi.mocked(api.mergeWorkspacePullRequest).mockResolvedValue({} as never);
    renderPanel();
    expect(await screen.findByText('no CI on this repository')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Merge…' }));
    const dialog = await screen.findByRole('dialog', { name: 'Merge PR #5' });
    expect(within(dialog).getByRole('button', { name: 'Merge' })).toBeDisabled();
    await userEvent.click(within(dialog).getByRole('checkbox', { name: /has no CI/ }));
    await userEvent.click(within(dialog).getByRole('button', { name: 'Merge' }));
    await waitFor(() => expect(api.mergeWorkspacePullRequest).toHaveBeenCalledWith('p', 'rw-1', { method: 'merge', acknowledgeNoChecks: true }, expect.anything()));
  });

  it('says why nothing can be pushed when a commit skipped the approval gate', async () => {
    vi.mocked(api.getWorkspaceRemote).mockResolvedValue({ ...base, remoteBranch: '', pushed: false, unapprovedCommits: ['def'] } as never);
    renderPanel();
    expect(await screen.findByText(/did not go through an approval gate/)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Push…' })).toBeNull();
  });
});
