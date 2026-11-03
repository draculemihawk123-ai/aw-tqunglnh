import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { uploadAttachment } from '../api/message';
import type { TaskDraft } from '../api/newTask';
import type { ApprovalChecklistView, ChecklistItemResult, ChecklistItemView } from '../api/rundetail';
import { getSessionToken } from '../api/session';
import { Button, Dialog, InlineError } from '../components/ui';
import { NewTaskDialog } from './NewTaskDialog';

interface ItemState { result?: 'PASSED' | 'FAILED'; note: string; attachments: { messageId: string; name: string }[] }

/** The outcome a checklist leads to, or undefined while an item has no result. */
export function checklistOutcome(checklist: ApprovalChecklistView, states: Record<string, ItemState>): string | undefined {
  if (checklist.items.some(i => !states[i.id]?.result)) return undefined;
  return checklist.items.every(i => states[i.id].result === 'PASSED') ? checklist.passedOutcome : checklist.failedOutcome;
}

/** The draft "Create fix task" starts from: a bugfix whose behavior is the failed item and its note. */
export function fixTaskDraft(item: ChecklistItemView, note: string, qaTitle: string): Partial<TaskDraft> {
  return {
    kind: 'bugfix',
    title: `Fix: ${item.text}`.slice(0, 200),
    behavior: `QA of "${qaTitle}" failed on: ${item.text}\n\nWhat is wrong: ${note || '(no note)'}`,
    criteria: [{ description: item.text }],
  };
}

/**
 * V12-31 (docs/design/15-v12-ui-operations.md, UX-33, ADR-063): the QA checklist of a checklist approval. Each item (the acceptance
 * criteria of the task under QA, then the node's own items) is marked passed or failed, with a note (required when it failed) and
 * images attached to the task's conversation. The outcome follows the items; "Create fix task" opens New task prefilled from a failed
 * item. Open the application with the Workspace tab's Run app.
 */
export function QAChecklistDialog({ projectId, workItemId, qaTitle, checklist, pending, error, isOffline, onClose, onSubmit }: {
  projectId: string; workItemId: string; qaTitle: string; checklist: ApprovalChecklistView; pending?: boolean; error?: unknown; isOffline?: boolean;
  onClose: () => void; onSubmit: (outcome: string, results: ChecklistItemResult[]) => void;
}) {
  const [states, setStates] = useState<Record<string, ItemState>>(() =>
    Object.fromEntries(checklist.items.map(i => [i.id, { note: '', attachments: [] }])));
  const [fixing, setFixing] = useState<ChecklistItemView | null>(null);
  const [created, setCreated] = useState<string[]>([]);
  const set = (id: string, patch: Partial<ItemState>) => setStates(s => ({ ...s, [id]: { ...s[id], ...patch } }));
  const upload = useMutation({
    mutationFn: async ({ id, file }: { id: string; file: File }) => ({
      id, name: file.name,
      result: await uploadAttachment(projectId, workItemId, file, { role: 'USER', token: getSessionToken(), idempotencyKey: crypto.randomUUID() }),
    }),
    onSuccess: ({ id, name, result }) => setStates(s => ({ ...s, [id]: { ...s[id], attachments: [...s[id].attachments, { messageId: result.messageId, name }] } })),
  });
  const outcome = checklistOutcome(checklist, states);
  const missingNote = checklist.items.some(i => states[i.id]?.result === 'FAILED' && !states[i.id].note.trim());
  const submit = () => onSubmit(outcome!, checklist.items.map(i => {
    const st = states[i.id];
    return {
      id: i.id, result: st.result!, ...(st.note.trim() ? { note: st.note.trim() } : {}),
      ...(st.attachments.length ? { attachmentMessageIds: st.attachments.map(a => a.messageId) } : {}),
    };
  }));

  return (
    <>
      <Dialog title="QA checklist" onClose={onClose}
        description={`Mark every item. All passed → ${checklist.passedOutcome}; any failed → ${checklist.failedOutcome}. A failed item needs a note.`}
        actions={<>
          <Button intent="secondary" onClick={onClose} disabled={pending}>Cancel</Button>
          <Button intent="primary" loading={pending} disabled={isOffline || !outcome || missingNote} onClick={submit}>
            {outcome ? `Submit (${outcome})` : 'Submit'}
          </Button>
        </>}>
        <ol className="space-y-3" aria-label="Checklist items">
          {checklist.items.map(item => {
            const st = states[item.id];
            return (
              <li key={item.id} className="border border-[#CDD5DF] rounded-[6px] p-2" data-testid={`qa-item-${item.id}`}>
                <div className="flex items-start gap-2">
                  <span className="flex-1 text-[13px] text-[#172033]">{item.text}
                    {item.source === 'ACCEPTANCE_CRITERION' && <span className="ml-1 text-[11px] text-[#475569]">(acceptance criterion)</span>}
                  </span>
                  <Button size="compact" intent={st.result === 'PASSED' ? 'primary' : 'quiet'} aria-pressed={st.result === 'PASSED'}
                    aria-label={`Passed: ${item.text}`} onClick={() => set(item.id, { result: 'PASSED' })}>Passed</Button>
                  <Button size="compact" intent={st.result === 'FAILED' ? 'destructive' : 'quiet'} aria-pressed={st.result === 'FAILED'}
                    aria-label={`Failed: ${item.text}`} onClick={() => set(item.id, { result: 'FAILED' })}>Failed</Button>
                </div>
                <textarea aria-label={`Note: ${item.text}`} rows={2} value={st.note} onChange={e => set(item.id, { note: e.target.value })}
                  placeholder={st.result === 'FAILED' ? 'What is wrong (required)' : 'Note (optional)'}
                  className="w-full mt-1 border border-[#CDD5DF] rounded-[6px] p-1 text-[12px]" />
                <div className="flex items-center gap-2 flex-wrap text-[12px] text-[#475569]">
                  <label className="cursor-pointer text-[#3659E3]">Attach image
                    <input type="file" accept="image/*" className="hidden" aria-label={`Attach image: ${item.text}`} disabled={isOffline || upload.isPending}
                      onChange={e => { const file = e.target.files?.[0]; if (file) upload.mutate({ id: item.id, file }); e.target.value = ''; }} />
                  </label>
                  {st.attachments.map(a => <span key={a.messageId} className="font-mono">{a.name}</span>)}
                  <span className="flex-1" />
                  {st.result === 'FAILED' && (
                    <Button size="compact" intent="secondary" disabled={isOffline} onClick={() => setFixing(item)}>Create fix task</Button>
                  )}
                </div>
              </li>
            );
          })}
        </ol>
        {created.length > 0 && <p className="text-[12px] text-[#166534] mt-2">Fix task created: {created.join(', ')}</p>}
        {upload.isError && <InlineError code="ATTACH" message={upload.error instanceof Error ? upload.error.message : 'The upload failed.'} />}
        {error ? <InlineError code="QA" message={error instanceof Error ? error.message : 'The decision failed.'} /> : null}
      </Dialog>
      {fixing && (
        <NewTaskDialog projectId={projectId} parentWorkItemId={workItemId} isOffline={isOffline}
          initial={fixTaskDraft(fixing, states[fixing.id]?.note ?? '', qaTitle)}
          onClose={() => setFixing(null)} onCreated={id => { setFixing(null); setCreated(c => [...c, id]); }} />
      )}
    </>
  );
}
