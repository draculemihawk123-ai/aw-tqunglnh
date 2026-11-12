import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render as rtlRender, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as api from '../api/generated';
import { DiffReviewDialog, approveOutcome, requestChangesOutcome } from './DiffReviewDialog';

vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, getWorkspaceSetState: vi.fn(), getWorkspaceDiff: vi.fn(), appendMessage: vi.fn(), resolveApproval: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));

function render(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return rtlRender(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

const PATCH = [
  'diff --git a/src/Order.java b/src/Order.java',
  'index 1..2 100644',
  '--- a/src/Order.java',
  '+++ b/src/Order.java',
  '@@ -10,3 +10,3 @@ class Order {',
  '   void reopen() {',
  '-    status = CLOSED;',
  '+    status = OPEN;',
  '   }',
  '',
].join('\n');

describe('DiffReviewDialog (V12-17)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getWorkspaceSetState).mockResolvedValue({
      workspaceSetId: 'ws', familyId: 'f', projectId: 'p', state: 'READY', version: 1, hasBaseRevisionSet: true, validActions: [],
      repositoryWorkspaces: [{ repositoryWorkspaceId: 'rw', workspaceSetId: 'ws', repositoryId: 'api', generation: 1, state: 'READY', version: 3, currentRevision: 'abc', hasActiveWriteLease: false, validActions: [] }],
    } as never);
    vi.mocked(api.getWorkspaceDiff).mockResolvedValue({ files: [], patch: btoa(PATCH), patchTruncated: false, uncommitted: true } as never);
    vi.mocked(api.appendMessage).mockResolvedValue({ messageId: 'm1' } as never);
    vi.mocked(api.resolveApproval).mockResolvedValue({ advanced: true } as never);
  });

  it('picks the kit outcomes', () => {
    expect(requestChangesOutcome(['approved', 'revise', 'rejected'])).toBe('revise');
    expect(requestChangesOutcome(['accept', 'rework', 'rejected'])).toBe('rework');
    expect(approveOutcome(['accept', 'rework', 'rejected'])).toBe('accept');
    expect(requestChangesOutcome(['provided', 'abandon'])).toBeUndefined();
  });

  it('sends line comments to the maker and decides revise with them as the reason', async () => {
    const onDecided = vi.fn();
    render(<DiffReviewDialog projectId="p" workItemId="wi" familyId="f" runId="r" approvalRequestId="ar" approvalVersion={4}
      nodeKey="review" outcomes={['approved', 'revise', 'rejected']} onClose={() => {}} onDecided={onDecided} />);
    const dialog = await screen.findByRole('dialog', { name: /Review changes/ });
    const file = await within(dialog).findByRole('region', { name: 'Changes in src/Order.java' });
    await userEvent.click(within(file).getByRole('button', { name: 'Comment on src/Order.java line 11' }));
    await userEvent.type(within(file).getByLabelText('Line comment'), 'Keep the old status for CANCELLED orders');
    await userEvent.click(within(file).getByRole('button', { name: 'Add comment' }));
    expect(within(file).getByTestId('line-comment')).toHaveTextContent('Keep the old status');
    expect(within(dialog).getByRole('button', { name: 'Approve' })).toBeDisabled();

    await userEvent.click(within(dialog).getByRole('button', { name: /Request changes/ }));
    await waitFor(() => expect(onDecided).toHaveBeenCalledWith('revise'));
    const [, workItem, message] = vi.mocked(api.appendMessage).mock.calls[0];
    expect(workItem).toBe('wi');
    expect(message.content).toContain('src/Order.java (api):');
    expect(message.content).toContain('- line 11: `status = OPEN;`');
    const [run, approval, body, opts] = vi.mocked(api.resolveApproval).mock.calls[0];
    expect([run, approval, body.outcome, body.reason]).toEqual(['r', 'ar', 'revise', message.content]);
    expect(opts).toMatchObject({ ifMatch: '"4"' });
  });

  it('approves without comments', async () => {
    const onDecided = vi.fn();
    render(<DiffReviewDialog projectId="p" workItemId="wi" familyId="f" runId="r" approvalRequestId="ar" approvalVersion={4}
      nodeKey="review" outcomes={['approved', 'revise', 'rejected']} onClose={() => {}} onDecided={onDecided} />);
    const dialog = await screen.findByRole('dialog', { name: /Review changes/ });
    await within(dialog).findByRole('region', { name: 'Changes in src/Order.java' });
    expect(within(dialog).getByRole('button', { name: /Request changes/ })).toBeDisabled();
    await userEvent.click(within(dialog).getByRole('button', { name: 'Approve' }));
    await waitFor(() => expect(onDecided).toHaveBeenCalledWith('approved'));
    expect(api.appendMessage).not.toHaveBeenCalled();
  });
});
