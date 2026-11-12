import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { FamilyQueuePanel } from './FamilyQueue';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, getWorkItemQueue: vi.fn(), setWorkItemQueue: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));

function renderPanel(workItemId: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={client}><FamilyQueuePanel workItemId={workItemId} /></QueryClientProvider>);
}

const QUEUE = {
  projectId: 'p', familyId: 'f', waiting: 'waiting for "T-01" to finish',
  entries: [
    { workItemId: 'wi-1', workItemTitle: 'T-01', workItemStatus: 'ACTIVE', position: 1, state: 'STARTED', runId: 'run-1', version: 2 },
    { workItemId: 'wi-2', workItemTitle: 'T-02', workItemStatus: 'READY', position: 2, state: 'QUEUED', waitsForWorkItemId: 'wi-1', version: 1 },
  ],
};

describe('FamilyQueuePanel (V12-19)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getWorkItemQueue).mockResolvedValue(QUEUE as never);
    vi.mocked(api.setWorkItemQueue).mockResolvedValue({} as never);
  });

  it('shows the queue in order, what it waits for, and takes a queued task out', async () => {
    renderPanel('wi-2');
    const list = await screen.findByRole('list', { name: 'Queued tasks' });
    const items = within(list).getAllByRole('listitem');
    expect(items[0]).toHaveTextContent('T-01');
    expect(items[0]).toHaveTextContent('started · ACTIVE');
    expect(items[1]).toHaveTextContent('after T-01');
    expect(screen.getByTestId('queue-waiting')).toHaveTextContent('waiting for "T-01" to finish');
    await userEvent.click(screen.getByRole('button', { name: 'Remove from queue' }));
    await waitFor(() => expect(api.setWorkItemQueue).toHaveBeenCalledWith('wi-2', { queued: false }, expect.objectContaining({ idempotencyKey: expect.any(String) })));
  });

  it('queues a task after a chosen one', async () => {
    renderPanel('wi-3');
    await screen.findByRole('list', { name: 'Queued tasks' });
    await userEvent.selectOptions(screen.getByLabelText('Wait for'), 'wi-1');
    await userEvent.click(screen.getByRole('button', { name: 'Add to queue' }));
    await waitFor(() => expect(api.setWorkItemQueue).toHaveBeenCalledWith('wi-3', { queued: true, waitsForWorkItemId: 'wi-1' }, expect.anything()));
  });
});
