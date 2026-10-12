import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Badge, Button, Checkbox, Dialog, InlineError, Skeleton, TextField } from '../components/ui';
import { getWorkItemRedoPreview, recreateWorkItem, retryWorkItem } from '../api/generated';
import type { RecreateWorkItemResponse, RetryWorkItemResponse } from '../api/generated';
import { withSessionToken } from '../api/session';
import {
  defaultOutOfScopeChoice, describeRedoError, explainRefusal, formatCost, outOfScopeFiles, redoWarning,
} from '../api/redo';
import type { OutOfScopeChoice, RedoPreview } from '../api/redo';

/**
 * V11-10 (ADR-041): the two dialogs that let an operator deal with a failed task without leaving the web.
 *
 * RetryDialog runs the SAME work item again on its pinned workflow version (note + blockers resolved + new run, one request).
 * RecreateDialog cancels the work item and creates a sibling with the same content on the NEWEST version of the same workflow
 * (one request). Both read the server's preview first — what would happen, why it would refuse, what the earlier attempts cost,
 * which files an earlier run left outside the scope — and send exactly one command.
 */

function usePreview(workItemId: string, isOffline?: boolean) {
  return useQuery({
    queryKey: ['workItemRedoPreview', workItemId],
    queryFn: async () => (await getWorkItemRedoPreview(workItemId, withSessionToken())) as unknown as RedoPreview,
    enabled: !isOffline,
    // an earlier run's leftovers and the cost are facts of the moment: never reuse a stale answer
    staleTime: 0, gcTime: 0,
  });
}

function VersionLine({ label, id, number }: { label: string; id: string; number: number }) {
  return (
    <div className="flex gap-3 items-center"><dt className="text-[#475569] w-40">{label}</dt>
      <dd className="flex items-center gap-2"><span className="font-mono text-[12px]">{id || '—'}</span>{number > 0 && <span className="text-[12px] text-[#475569]">version {number}</span>}</dd></div>
  );
}

function OutOfScopeSection({ preview, choice, onChoice }: { preview: RedoPreview; choice?: OutOfScopeChoice; onChoice: (c: OutOfScopeChoice) => void }) {
  const outside = outOfScopeFiles(preview);
  if (preview.filesUnavailableReason && preview.files.length === 0) {
    return <p className="text-[12px] text-[#475569]" data-testid="files-unavailable">Changed files could not be listed: {preview.filesUnavailableReason}.</p>;
  }
  if (preview.files.length === 0) return null;
  return (
    <fieldset className="mb-3 border border-[#CDD5DF] rounded-[8px] p-3" data-testid="leftover-files">
      <legend className="text-[12px] font-medium text-[#172033] px-1">Uncommitted files in the worktree ({preview.files.length})</legend>
      <ul className="max-h-32 overflow-y-auto text-[12px] font-mono space-y-0.5 mb-2">
        {preview.files.map(f => (
          <li key={`${f.repositoryId}:${f.path}`} className={f.outOfScope ? 'text-[#92400E]' : 'text-[#172033]'}>
            {f.repositoryId}:{f.path} <span className="text-[#475569]">({f.status.toLowerCase()})</span>
            {f.outOfScope && <span className="ml-1 font-sans font-medium">outside scope</span>}
          </li>
        ))}
      </ul>
      {preview.filesUnavailableReason && <p className="text-[12px] text-[#92400E] mb-2">The list may be incomplete: {preview.filesUnavailableReason}.</p>}
      {outside.length > 0 && (
        <div role="radiogroup" aria-label="Files outside the work item's scope" className="space-y-1.5 text-[13px]">
          <p className="text-[12px] text-[#172033]">{outside.length} {outside.length === 1 ? 'file is' : 'files are'} outside the scope and would make the next run fail with SCOPE_VIOLATION. What should happen to {outside.length === 1 ? 'it' : 'them'}?</p>
          <label className="flex items-start gap-2"><input type="radio" name="out-of-scope" checked={choice === 'KEEP'} onChange={() => onChoice('KEEP')} className="mt-1" />
            <span><strong>Keep</strong> them where they are <span className="text-[#92400E]">(the next run will probably fail again)</span></span></label>
          <label className="flex items-start gap-2"><input type="radio" name="out-of-scope" checked={choice === 'PARK'} onChange={() => onChoice('PARK')} className="mt-1" />
            <span><strong>Park</strong> them: move them out of the worktree into a hidden Git ref; nothing is deleted</span></label>
        </div>
      )}
    </fieldset>
  );
}

function PreviewState({ query }: { query: { isPending: boolean; isError: boolean; error: unknown; refetch: () => unknown } }) {
  if (query.isPending) return <div className="space-y-2" aria-busy="true"><Skeleton className="h-4 w-2/3" /><Skeleton className="h-4 w-1/2" /><Skeleton className="h-16 w-full" /></div>;
  if (query.isError) return <InlineError {...describeRedoError(query.error)} onRetry={() => { void query.refetch(); }} />;
  return null;
}

// ─── Run again ────────────────────────────────────────────────────────────────

export interface RetryDialogProps {
  workItemId: string;
  isOffline?: boolean;
  onClose: () => void;
  onDone: (result: RetryWorkItemResponse) => void;
}

export function RetryDialog({ workItemId, isOffline, onClose, onDone }: RetryDialogProps) {
  const query = usePreview(workItemId, isOffline);
  const preview = query.data;
  const [note, setNote] = useState('');
  const [chosen, setChosen] = useState<OutOfScopeChoice | undefined>(undefined);
  const choice = chosen ?? (preview ? defaultOutOfScopeChoice(preview) : undefined);

  const mutation = useMutation({
    mutationFn: async () => retryWorkItem(workItemId, { note: note.trim() || undefined, outOfScope: choice }, withSessionToken({ idempotencyKey: crypto.randomUUID() })),
    onSuccess: onDone,
  });

  const refusal = preview && !preview.canRetry ? explainRefusal(preview.retryRefusal) ?? 'This work item cannot be run again right now.' : undefined;
  const warning = preview && preview.canRetry ? redoWarning(preview.priorRunCount + 1, formatCost(preview)) : undefined;
  return (
    <Dialog
      title="Run Again"
      description="Runs this work item again from the start of its workflow, on the workflow version it is pinned to. Work already in the worktree is kept; the agents continue from it."
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={mutation.isPending}>Cancel</Button>
          <Button intent="primary" loading={mutation.isPending} disabled={isOffline || !preview?.canRetry} onClick={() => mutation.mutate()}>Run Again</Button>
        </>
      }
    >
      <PreviewState query={query} />
      {preview && (
        <>
          {refusal && <div role="alert" className="mb-3 p-3 rounded-[8px] bg-[#FEF3C7] border border-[#FCD34D] text-[13px] text-[#92400E]" data-testid="refusal">{refusal}</div>}
          <dl className="text-[13px] space-y-1.5 mb-3">
            <VersionLine label="Runs with (pinned)" id={preview.pinnedWorkflowVersion.workflowVersionId} number={preview.pinnedWorkflowVersion.versionNumber} />
            <div className="flex gap-3"><dt className="text-[#475569] w-40">Earlier runs</dt><dd>{preview.priorRunCount} {preview.priorRunCount === 1 ? 'run' : 'runs'}, cost {formatCost(preview)}</dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-40">Blockers resolved</dt>
              <dd>{preview.blockersToResolve.length === 0 ? 'none' : (
                <ul className="space-y-0.5">{preview.blockersToResolve.map(b => <li key={b.blockerId} className="flex items-center gap-2"><Badge label={b.type} intent="warning" /><span className="text-[12px] text-[#475569]">{b.reason}</span></li>)}</ul>
              )}</dd></div>
          </dl>
          {preview.newerVersionAvailable && (
            <p className="mb-3 text-[12px] text-[#1E40AF] bg-[#DBEAFE] rounded-[4px] px-2 py-1" data-testid="newer-version-hint">
              A newer version of this workflow exists (version {preview.latestWorkflowVersion.versionNumber}). Running again keeps the pinned version; use Redo with Latest Version to pick it up.
            </p>
          )}
          {warning && <p role="status" className="mb-3 text-[12px] text-[#92400E]" data-testid="attempt-warning">{warning}</p>}
          <OutOfScopeSection preview={preview} choice={choice} onChoice={setChosen} />
          <TextField label="Note for the agent" value={note} onChange={setNote} placeholder="Optional — sent as a message before the run starts" />
        </>
      )}
      {mutation.isError && <div className="mt-2"><InlineError {...describeRedoError(mutation.error)} /></div>}
    </Dialog>
  );
}

// ─── Redo with the latest version ─────────────────────────────────────────────

export interface RecreateDialogProps {
  workItemId: string;
  isOffline?: boolean;
  onClose: () => void;
  onDone: (result: RecreateWorkItemResponse) => void;
}

export function RecreateDialog({ workItemId, isOffline, onClose, onDone }: RecreateDialogProps) {
  const query = usePreview(workItemId, isOffline);
  const preview = query.data;
  const [reason, setReason] = useState('');
  const [note, setNote] = useState('');
  const [carry, setCarry] = useState(true);
  const [chosen, setChosen] = useState<OutOfScopeChoice | undefined>(undefined);
  const choice = chosen ?? (preview ? defaultOutOfScopeChoice(preview) : undefined);

  const mutation = useMutation({
    mutationFn: async () => {
      return recreateWorkItem(workItemId, {
        reason: reason.trim(), note: note.trim() || undefined, carryMessages: carry, outOfScope: choice,
      }, withSessionToken({ idempotencyKey: crypto.randomUUID() }));
    },
    onSuccess: onDone,
  });

  const refusal = preview && !preview.canRecreate ? explainRefusal(preview.recreateRefusal) ?? 'This work item cannot be redone right now.' : undefined;
  const warning = preview && preview.canRecreate ? redoWarning(preview.redoNumber, formatCost(preview)) : undefined;
  return (
    <Dialog
      title="Redo with Latest Version"
      description="Cancels this work item and creates a new one under the same parent with the same title, scope and contract, pinned to the newest version of the same workflow. The new work item starts running at once."
      onClose={onClose}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={mutation.isPending}>Keep Work Item</Button>
          <Button intent="destructive" loading={mutation.isPending} disabled={isOffline || !preview?.canRecreate || !reason.trim()} onClick={() => mutation.mutate()}>Cancel and Redo</Button>
        </>
      }
    >
      <PreviewState query={query} />
      {preview && (
        <>
          {refusal && <div role="alert" className="mb-3 p-3 rounded-[8px] bg-[#FEF3C7] border border-[#FCD34D] text-[13px] text-[#92400E]" data-testid="refusal">{refusal}</div>}
          <dl className="text-[13px] space-y-1.5 mb-3">
            <VersionLine label="Pinned workflow version" id={preview.pinnedWorkflowVersion.workflowVersionId} number={preview.pinnedWorkflowVersion.versionNumber} />
            <VersionLine label="Newest version" id={preview.latestWorkflowVersion.workflowVersionId} number={preview.latestWorkflowVersion.versionNumber} />
            <div className="flex gap-3"><dt className="text-[#475569] w-40">Attempt</dt><dd>{preview.redoNumber} — title becomes “… (lần {preview.redoNumber})”</dd></div>
            <div className="flex gap-3"><dt className="text-[#475569] w-40">Earlier attempts</dt><dd>{preview.priorRunCount} {preview.priorRunCount === 1 ? 'run' : 'runs'}, cost {formatCost(preview)}</dd></div>
          </dl>
          {!preview.newerVersionAvailable && (
            <p className="mb-3 text-[12px] text-[#475569]" data-testid="same-version-hint">
              There is no newer version of this workflow: the redo runs on the same version, from a clean work item.
            </p>
          )}
          {warning && <p role="status" className="mb-3 text-[12px] text-[#92400E]" data-testid="attempt-warning">{warning}</p>}
          <OutOfScopeSection preview={preview} choice={choice} onChoice={setChosen} />
          <div className="space-y-3">
            <Checkbox label={`Carry over my ${preview.operatorMessageCount} ${preview.operatorMessageCount === 1 ? 'note' : 'notes'} to the new work item`} checked={carry} onChange={setCarry} />
            <TextField label="Reason" required value={reason} onChange={setReason} placeholder="Why this task is being redone (recorded on the cancellation)" />
            <TextField label="Note for the agent" value={note} onChange={setNote} placeholder="Optional — an extra message for the new work item" />
          </div>
        </>
      )}
      {mutation.isError && <div className="mt-2"><InlineError {...describeRedoError(mutation.error)} /></div>}
    </Dialog>
  );
}

/** The two links a redo leaves between work items, shown on the task header. */
export function SupersessionLinks({ supersedes, supersededBy, onOpen }: { supersedes?: string; supersededBy?: string; onOpen: (workItemId: string) => void }) {
  if (!supersedes && !supersededBy) return null;
  return (
    <>
      {supersedes && (
        <span className="text-[12px] text-[#475569]" data-testid="supersedes-link">
          replaces <button type="button" className="font-mono text-[#3659E3] hover:underline" onClick={() => onOpen(supersedes)}>{supersedes}</button>
        </span>
      )}
      {supersededBy && (
        <span className="text-[12px] text-[#92400E]" data-testid="superseded-by-link">
          replaced by <button type="button" className="font-mono text-[#3659E3] hover:underline" onClick={() => onOpen(supersededBy)}>{supersededBy}</button>
        </span>
      )}
    </>
  );
}
