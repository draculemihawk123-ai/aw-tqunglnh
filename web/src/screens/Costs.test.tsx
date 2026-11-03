import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { CostsScreen } from './Costs';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, getProjectCosts: vi.fn(), setWorkItemBudget: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));

const usage = (costUsd: number) => ({ inputTokens: 120000, cachedInputTokens: 0, outputTokens: 8000, costUsd });

describe('CostsScreen (V12-23)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getProjectCosts).mockResolvedValue({
      projectId: 'p', usage: usage(3.4),
      batches: [{
        rootWorkItemId: 'root-1', title: 'Order status feature', usage: usage(3.4), budgetUsd: 3, overBudget: true, nearBudget: false,
        tasks: [{ workItemId: 'wi-1', title: 'T-01: API', status: 'DONE', runCount: 2, usage: usage(2.9) }, { workItemId: 'wi-2', title: 'T-02: UI', status: 'READY', runCount: 0 }],
      }],
      providerLimits: [{ workItemId: 'wi-1', title: 'T-01: API', message: "You've hit your limit — aw resumes this node by itself at 2026-11-03T15:00:00Z" }],
    } as never);
    vi.mocked(api.setWorkItemBudget).mockResolvedValue({} as never);
  });

  it('shows spend by batch and task, the budget state and the provider limit, and changes a budget', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={client}><CostsScreen projectId="p" onOpenTask={() => {}} /></QueryClientProvider>);
    expect(await screen.findByTestId('project-total')).toHaveTextContent('$3.40');
    const batch = screen.getByTestId('batch-root-1');
    expect(batch).toHaveTextContent('over budget $3.00');
    const rows = within(within(batch).getByRole('table', { name: 'Tasks of Order status feature' })).getAllByRole('row');
    expect(rows[1]).toHaveTextContent('T-01: API');
    expect(rows[1]).toHaveTextContent('$2.90');
    expect(rows[2]).toHaveTextContent('—');
    expect(screen.getByRole('region', { name: 'Provider limits' })).toHaveTextContent('resumes this node by itself');

    await userEvent.click(within(batch).getByRole('button', { name: 'Change budget' }));
    const input = within(batch).getByLabelText('Budget of Order status feature in USD');
    await userEvent.clear(input);
    await userEvent.type(input, '5');
    await userEvent.click(within(batch).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.setWorkItemBudget).toHaveBeenCalledWith('root-1', { budgetUsd: 5 }, expect.objectContaining({ idempotencyKey: expect.any(String) })));
  });
});
