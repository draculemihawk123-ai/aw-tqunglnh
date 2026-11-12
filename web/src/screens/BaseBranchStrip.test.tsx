import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { BaseBranchStrip } from './BaseBranchStrip';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, getWorkspaceBaseStatus: vi.fn(), syncWorkspaceWithBase: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));

const REPO = {
  repositoryWorkspaceId: 'rw-1', workspaceSetId: 'ws', repositoryId: 'api', generation: 1, state: 'READY', version: 4,
  currentRevision: 'abc', hasActiveWriteLease: false, validActions: [],
};

function renderStrip(onChanged = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(<QueryClientProvider client={client}><BaseBranchStrip projectId="p" repo={REPO} onChanged={onChanged} /></QueryClientProvider>);
  return onChanged;
}

describe('BaseBranchStrip (V12-20)', () => {
  beforeEach(() => vi.clearAllMocks());

  it('shows what is new on the base branch and syncs it with the workspace version', async () => {
    vi.mocked(api.getWorkspaceBaseStatus).mockResolvedValue({ baseRef: 'main', baseRevision: 'm', head: 'abc', merged: true, ahead: 0, behind: 2, fetched: false } as never);
    vi.mocked(api.syncWorkspaceWithBase).mockResolvedValue({ sync: { baseRef: 'main', baseRevision: 'm', head: 'm', outcome: 'FAST_FORWARDED', merged: true, ahead: 0, behind: 0, fetched: true } } as never);
    const onChanged = renderStrip();
    expect(await screen.findByTestId('base-branch')).toHaveTextContent('Base branch main: 2 new commits');
    expect(screen.getByText('merged into main')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Sync with base branch' }));
    await waitFor(() => expect(api.syncWorkspaceWithBase).toHaveBeenCalledWith('p', 'rw-1', {}, expect.objectContaining({ ifMatch: '"4"' })));
    expect(await screen.findByRole('status')).toHaveTextContent('Fast-forwarded to main');
    expect(onChanged).toHaveBeenCalled();
  });

  it('says a diverged branch needs a merge', async () => {
    vi.mocked(api.getWorkspaceBaseStatus).mockResolvedValue({ baseRef: 'main', baseRevision: 'm', head: 'abc', merged: false, ahead: 1, behind: 1, fetched: false } as never);
    vi.mocked(api.syncWorkspaceWithBase).mockResolvedValue({ sync: { baseRef: 'main', baseRevision: 'm', head: 'abc', outcome: 'NEEDS_MERGE', merged: false, ahead: 1, behind: 1, fetched: true } } as never);
    renderStrip();
    await screen.findByTestId('base-branch');
    await userEvent.click(screen.getByRole('button', { name: 'Sync with base branch' }));
    expect(await screen.findByRole('status')).toHaveTextContent('diverged from main — merge it in the worktree');
  });
});
