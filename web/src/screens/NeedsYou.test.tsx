import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, renderHook, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { NeedsYouScreen, useNeedsYouNotifications } from './NeedsYou';
import type { NeedsYouItem } from './NeedsYou';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, listNeedsYou: vi.fn(), resolveApproval: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));

function render(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

const approval: NeedsYouItem = {
  kind: 'APPROVAL_PENDING', workItemId: 'wi-1', workItemTitle: 'P2-02 notifications', runId: 'run-1', nodeRunId: 'nr-g', nodeKey: 'gate2',
  outcomes: ['approved', 'revise', 'rejected'], approvalRequestId: 'apr-1', approvalVersion: 3, message: 'gate2 is waiting for a decision', since: '2026-11-03T10:00:00Z',
};
const blocker: NeedsYouItem = {
  kind: 'BLOCKER', workItemId: 'wi-2', workItemTitle: 'P1-01 reopen', type: 'PROVIDER_QUOTA_EXCEEDED', blockerId: 'b-1',
  message: "You've hit your limit — aw resumes this node by itself at …", since: '2026-11-03T09:00:00Z',
};

describe('Needs you (V12-16)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.listNeedsYou).mockResolvedValue({ projectId: 'p', count: 2, items: [approval, blocker] } as never);
    vi.mocked(api.resolveApproval).mockResolvedValue({ advanced: true } as never);
  });

  it('lists what waits, decides an approval in one click and opens the task of a blocker', async () => {
    const onOpenTask = vi.fn();
    render(<NeedsYouScreen projectId="p" onOpenTask={onOpenTask} />);
    const list = await screen.findByRole('list', { name: 'Things that need you' });
    expect(await within(list).findAllByRole('listitem')).toHaveLength(2);
    const gate = screen.getByTestId('needs-you-apr-1');
    await userEvent.click(within(gate).getByRole('button', { name: 'approved' }));
    await waitFor(() => expect(api.resolveApproval).toHaveBeenCalledWith('run-1', 'apr-1', { outcome: 'approved' }, expect.objectContaining({ ifMatch: '"3"' })));
    const quota = screen.getByTestId('needs-you-b-1');
    expect(quota).toHaveTextContent('PROVIDER_QUOTA_EXCEEDED');
    await userEvent.click(within(quota).getByRole('button', { name: 'Open task' }));
    expect(onOpenTask).toHaveBeenCalledWith('wi-2');
  });

  it('notifies about a new item, not about those already seen', () => {
    const shown: string[] = [];
    class FakeNotification { static permission = 'granted'; constructor(title: string) { shown.push(title); } }
    vi.stubGlobal('Notification', FakeNotification);
    const { rerender } = renderHook(({ items }) => useNeedsYouNotifications(items), { initialProps: { items: [blocker] } });
    expect(shown).toEqual([]); // the first poll only learns what is there
    rerender({ items: [blocker, approval] });
    expect(shown).toEqual(['aw: P2-02 notifications']);
    rerender({ items: [blocker, approval] });
    expect(shown).toHaveLength(1);
    vi.unstubAllGlobals();
  });
});
