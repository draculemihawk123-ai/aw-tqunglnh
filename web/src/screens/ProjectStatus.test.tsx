import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { ProjectStatus } from './ProjectStatus';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, listWorkItemKanban: vi.fn(), listNeedsYou: vi.fn(), getProjectCosts: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));

describe('ProjectStatus (V12-24)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.listWorkItemKanban)
      .mockResolvedValueOnce({ items: [
        { workItemId: 'root', title: 'Order status', isRoot: true, status: 'ACTIVE', blockerCount: 0 },
        { workItemId: 't1', parentWorkItemId: 'root', title: 'T-01', status: 'DONE', blockerCount: 0 },
      ], nextCursor: 'c2' } as never)
      .mockResolvedValueOnce({ items: [
        { workItemId: 't2', parentWorkItemId: 'root', title: 'T-02', status: 'ACTIVE', activeRunId: 'r', activeRunStatus: 'RUNNING', blockerCount: 0 },
      ] } as never);
    vi.mocked(api.listNeedsYou).mockResolvedValue({ projectId: 'p', count: 1, items: [{ kind: 'APPROVAL_PENDING', workItemTitle: 'T-02', message: 'm' }] } as never);
    vi.mocked(api.getProjectCosts).mockResolvedValue({ projectId: 'p', usage: { costUsd: 1.25 }, batches: [], providerLimits: [] } as never);
  });

  it('reads every page of the board, shows progress per batch and exports the report', async () => {
    const createObjectURL = vi.fn(() => 'blob:x');
    Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={client}><ProjectStatus projectId="p" projectName="shop" /></QueryClientProvider>);
    expect(await screen.findByRole('progressbar', { name: 'Order status progress' })).toHaveAttribute('aria-valuenow', '50');
    expect(api.listWorkItemKanban).toHaveBeenLastCalledWith('p', expect.objectContaining({ query: { limit: '200', cursor: 'c2' } }));
    expect(await screen.findByTestId('status-summary')).toHaveTextContent('1 batch · 1 running · 1 waiting for you · $1.25 so far');
    await userEvent.click(screen.getByRole('button', { name: 'Export report' }));
    expect(createObjectURL).toHaveBeenCalledTimes(1);
  });
});
