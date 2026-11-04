import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as message from '../api/message';
import { QAChecklistDialog, checklistOutcome, fixTaskDraft } from './QAChecklistDialog';
import type { ApprovalChecklistView } from '../api/rundetail';

vi.mock('../api/message', async () => {
  const actual = await vi.importActual<typeof import('../api/message')>('../api/message');
  return { ...actual, uploadAttachment: vi.fn() };
});
vi.mock('../api/session', () => ({ withSessionToken: (opts: object = {}) => ({ ...opts, token: 't' }), getSessionToken: () => 't' }));
vi.mock('../api/generated', async () => {
  const actual = await vi.importActual<typeof import('../api/generated')>('../api/generated');
  return { ...actual, getWorkItem: vi.fn().mockResolvedValue({ effectiveScope: [
    { repositoryId: 'web', access: 'WRITE', pathScopes: ['frontend/src'] }, { repositoryId: 'api', access: 'READ' },
  ] }) };
});
vi.mock('./NewTaskDialog', () => ({
  NewTaskDialog: ({ initial, onCreated }: { initial: { title: string; behavior: string; kind: string; scopes?: { repositoryId: string; pathScopes: string[] }[] }; onCreated: (id: string) => void }) => (
    <div data-testid="new-task">{initial.kind} | {initial.title} | {initial.behavior} | scope {(initial.scopes ?? []).map(s => `${s.repositoryId}:${s.pathScopes.join(',')}`).join(';')}<button onClick={() => onCreated('wi-fix-1')}>create</button></div>
  ),
}));

const checklist: ApprovalChecklistView = {
  passedOutcome: 'passed', failedOutcome: 'failed',
  items: [
    { id: 'ac-1', text: 'Given a mover, the quote shows the price', source: 'ACCEPTANCE_CRITERION' },
    { id: 'item-1', text: 'Works on a phone', source: 'MANUAL' },
  ],
};

function mount(onSubmit = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  render(<QueryClientProvider client={client}><QAChecklistDialog projectId="p" workItemId="wi-qa" qaTitle="QA quote" checklist={checklist} onClose={() => {}} onSubmit={onSubmit} /></QueryClientProvider>);
  return onSubmit;
}

describe('QAChecklistDialog (V12-31)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(message.uploadAttachment).mockResolvedValue({ messageId: 'msg-7', projectId: 'p', workItemId: 'wi-qa', sequence: 3, contentArtifactId: 'a' });
  });

  it('derives the outcome from the items', () => {
    expect(checklistOutcome(checklist, { 'ac-1': { result: 'PASSED', note: '', attachments: [] }, 'item-1': { note: '', attachments: [] } })).toBeUndefined();
    expect(checklistOutcome(checklist, { 'ac-1': { result: 'PASSED', note: '', attachments: [] }, 'item-1': { result: 'PASSED', note: '', attachments: [] } })).toBe('passed');
    expect(checklistOutcome(checklist, { 'ac-1': { result: 'FAILED', note: 'x', attachments: [] }, 'item-1': { result: 'PASSED', note: '', attachments: [] } })).toBe('failed');
    expect(fixTaskDraft(checklist.items[1], 'menu overflows', 'QA quote')).toMatchObject({ kind: 'bugfix', title: 'Fix: Works on a phone', criteria: [{ description: 'Works on a phone' }] });
    expect(fixTaskDraft(checklist.items[1], 'x', 'QA quote', [{ repositoryId: 'web', access: 'WRITE' }]).scopes).toEqual([{ repositoryId: 'web', pathScopes: [], reason: 'Fix found by the QA of "QA quote"' }]);
  });

  it('submits every result with notes and images; a failed item needs a note and offers Create fix task', async () => {
    const onSubmit = mount();
    const submit = () => screen.getByRole('button', { name: /^Submit/ });
    expect(submit()).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: 'Passed: Given a mover, the quote shows the price' }));
    await userEvent.click(screen.getByRole('button', { name: 'Failed: Works on a phone' }));
    expect(submit()).toHaveTextContent('Submit (failed)');
    expect(submit()).toBeDisabled(); // the failed item has no note yet
    await userEvent.type(screen.getByLabelText('Note: Works on a phone'), 'menu overflows');
    await userEvent.upload(screen.getByLabelText('Attach image: Works on a phone'), new File(['png'], 'phone.png', { type: 'image/png' }));
    expect(await screen.findByText('phone.png')).toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'Create fix task' }));
    expect(screen.getByTestId('new-task')).toHaveTextContent('bugfix | Fix: Works on a phone | QA of "QA quote" failed on: Works on a phone');
    expect(screen.getByTestId('new-task')).toHaveTextContent('What is wrong: menu overflows');
    expect(screen.getByTestId('new-task')).toHaveTextContent('scope web:frontend/src'); // the QA task's WRITE scope, not its READ ones
    await userEvent.click(screen.getByRole('button', { name: 'create' }));
    expect(screen.getByText('Fix task created: wi-fix-1')).toBeInTheDocument();

    await userEvent.click(submit());
    await waitFor(() => expect(onSubmit).toHaveBeenCalledWith('failed', [
      { id: 'ac-1', result: 'PASSED' },
      { id: 'item-1', result: 'FAILED', note: 'menu overflows', attachmentMessageIds: ['msg-7'] },
    ]));
  });
});
