import { useMemo, useState } from 'react';
import { useMutation, useQueries, useQuery } from '@tanstack/react-query';
import { appendMessage, getWorkspaceSetState, resolveApproval } from '../api/generated';
import { withSessionToken } from '../api/session';
import { decodeDiffPatch, fetchWorkingTreeDiff, workingTreeQueryKey } from '../api/workspaceinspection';
import type { WorkspaceSetState } from '../api/workspaceinspection';
import { Button, Dialog, InlineError } from '../components/ui';
import { parseUnifiedDiff, reviewMessage } from './diffReview';
import type { DiffFile, DiffLine, LineComment } from './diffReview';

/** The outcome of an approval node that sends the work back to the maker ("revise" in the kit, "rework" for a size review). */
export function requestChangesOutcome(outcomes: string[]): string | undefined {
  return outcomes.find(o => /^(revise|rework|changes_requested|request_changes)$/i.test(o));
}

/** The outcome that approves ("approved", "accept"). */
export function approveOutcome(outcomes: string[]): string | undefined {
  return outcomes.find(o => /^(approved?|accept(ed)?)$/i.test(o));
}

export interface DiffReviewDialogProps {
  projectId: string;
  workItemId: string;
  familyId: string;
  runId: string;
  approvalRequestId: string;
  approvalVersion: number;
  nodeKey: string;
  outcomes: string[];
  isOffline?: boolean;
  onClose: () => void;
  onDecided: (outcome: string) => void;
}

/**
 * V12-17 (docs/design/15-v12-ui-operations.md, UX-31): review the uncommitted changes at an approval gate inside aw — every file of
 * every READY repository of the family, line by line — and comment on lines. "Request changes" sends the comments to the task as a
 * USER message (the maker reads it on the next attempt) and decides the gate's revise outcome with the same text as the reason, so the
 * comments are recorded with the decision. Approve decides the approving outcome; an approval with a commit still goes through the
 * commit dialog of the approval strip.
 */
export function DiffReviewDialog(props: DiffReviewDialogProps) {
  const { projectId, workItemId, familyId, runId, approvalRequestId, approvalVersion, nodeKey, outcomes, isOffline, onClose, onDecided } = props;
  const [comments, setComments] = useState<LineComment[]>([]);
  const [editing, setEditing] = useState<{ repositoryId: string; path: string; line: DiffLine } | null>(null);
  const [draft, setDraft] = useState('');
  const [summary, setSummary] = useState('');
  const [idempotencyKey] = useState(() => crypto.randomUUID());

  const setQuery = useQuery({
    queryKey: ['workspaceSetState', projectId, familyId],
    queryFn: async () => (await getWorkspaceSetState(projectId, familyId, withSessionToken())) as unknown as WorkspaceSetState,
    enabled: !isOffline,
  });
  const repos = (setQuery.data?.repositoryWorkspaces ?? []).filter(r => r.state === 'READY' && !!r.currentRevision);
  const diffs = useQueries({
    queries: repos.map(repo => ({ queryKey: workingTreeQueryKey(projectId, repo), queryFn: () => fetchWorkingTreeDiff(projectId, repo), enabled: !isOffline })),
  });
  const files = useMemo(() => repos.flatMap((repo, i) => {
    const data = diffs[i]?.data;
    if (!data) return [];
    return parseUnifiedDiff(decodeDiffPatch(data.patch)).map(file => ({ repositoryId: repo.repositoryId, file, truncated: data.patchTruncated }));
  }), [repos, diffs]);

  const reviseOutcome = requestChangesOutcome(outcomes);
  const okOutcome = approveOutcome(outcomes);

  const decide = useMutation({
    mutationFn: async (outcome: string) => {
      if (outcome === reviseOutcome) {
        const text = reviewMessage(comments, summary);
        await appendMessage(projectId, workItemId, { role: 'USER', content: text, contentType: 'text/plain' }, withSessionToken({ idempotencyKey }));
        return resolveApproval(runId, approvalRequestId, { outcome, reason: text }, withSessionToken({ ifMatch: `"${approvalVersion}"` }));
      }
      return resolveApproval(runId, approvalRequestId, { outcome }, withSessionToken({ ifMatch: `"${approvalVersion}"` }));
    },
    onSuccess: (_r, outcome) => onDecided(outcome),
  });

  const lineNumber = (l: DiffLine) => (l.kind === 'del' ? l.oldNumber! : l.newNumber!);
  const commentsOn = (repositoryId: string, path: string, l: DiffLine) =>
    comments.filter(c => c.repositoryId === repositoryId && c.path === path && c.line === lineNumber(l) && !!c.deleted === (l.kind === 'del'));

  function addComment() {
    if (!editing || !draft.trim()) return;
    const l = editing.line;
    setComments(cs => [...cs, { repositoryId: editing.repositoryId, path: editing.path, line: lineNumber(l), deleted: l.kind === 'del', lineText: l.text, body: draft.trim() }]);
    setDraft('');
    setEditing(null);
  }

  const loading = setQuery.isPending || diffs.some(d => d.isPending);
  const failed = setQuery.error ?? diffs.find(d => d.isError)?.error;
  const canRequest = !!reviseOutcome && (comments.length > 0 || summary.trim().length > 0);

  return (
    <Dialog wide title={`Review changes — ${nodeKey}`} onClose={onClose}
      description="The uncommitted changes the maker left in the worktree. Click a line to comment on it; Request changes sends the comments back to the maker."
      actions={<>
        <Button intent="secondary" onClick={onClose} disabled={decide.isPending}>Close</Button>
        {reviseOutcome && (
          <Button intent="secondary" disabled={isOffline || !canRequest || decide.isPending} loading={decide.isPending && decide.variables === reviseOutcome}
            onClick={() => decide.mutate(reviseOutcome)}>Request changes{comments.length ? ` (${comments.length})` : ''}</Button>
        )}
        {okOutcome && (
          <Button intent="primary" disabled={isOffline || decide.isPending || comments.length > 0} loading={decide.isPending && decide.variables === okOutcome}
            onClick={() => decide.mutate(okOutcome)}>Approve</Button>
        )}
      </>}>
      {failed && <InlineError code="DIFF_UNAVAILABLE" message={failed instanceof Error ? failed.message : 'Could not read the changes.'} />}
      {decide.isError && <InlineError code="DECISION_FAILED" message={decide.error instanceof Error ? decide.error.message : 'The decision failed.'} />}
      {loading && !failed && <p className="text-[13px] text-[#475569]" role="status">Reading the changes…</p>}
      {!loading && !failed && files.length === 0 && <p className="text-[13px] text-[#475569]">No uncommitted changes in the worktree.</p>}
      {comments.length > 0 && <p className="text-[12px] text-[#92400E] mb-2">Approve is off while there are comments: send them with Request changes, or remove them.</p>}
      <div className="space-y-3">
        {files.map(({ repositoryId, file, truncated }) => (
          <FileDiff key={`${repositoryId}:${file.path}`} repositoryId={repositoryId} file={file} truncated={truncated}
            editing={editing} draft={draft} setDraft={setDraft} commentsOn={commentsOn}
            onPick={line => { setEditing({ repositoryId, path: file.path, line }); setDraft(''); }}
            onCancel={() => setEditing(null)} onAdd={addComment}
            onRemove={c => setComments(cs => cs.filter(x => x !== c))} />
        ))}
      </div>
      {reviseOutcome && (
        <label className="block mt-3 text-[12px] text-[#475569]">
          Overall comment (optional)
          <textarea className="mt-1 w-full border border-[#CDD5DF] rounded-[6px] p-2 text-[13px] text-[#172033]" rows={2}
            value={summary} onChange={e => setSummary(e.target.value)} />
        </label>
      )}
    </Dialog>
  );
}

const LINE_STYLE: Record<DiffLine['kind'], string> = {
  add: 'bg-[#ECFDF5] text-[#14532D]',
  del: 'bg-[#FEF2F2] text-[#7F1D1D]',
  ctx: 'text-[#172033]',
  hunk: 'bg-[#F1F5F9] text-[#475569]',
};

function FileDiff({ repositoryId, file, truncated, editing, draft, setDraft, commentsOn, onPick, onCancel, onAdd, onRemove }: {
  repositoryId: string; file: DiffFile; truncated: boolean;
  editing: { repositoryId: string; path: string; line: DiffLine } | null; draft: string; setDraft: (s: string) => void;
  commentsOn: (repositoryId: string, path: string, l: DiffLine) => LineComment[];
  onPick: (l: DiffLine) => void; onCancel: () => void; onAdd: () => void; onRemove: (c: LineComment) => void;
}) {
  return (
    <section className="border border-[#CDD5DF] rounded-[8px] overflow-hidden" aria-label={`Changes in ${file.path}`}>
      <header className="flex items-center gap-2 px-3 py-1.5 bg-[#F3F5F8] border-b border-[#CDD5DF] text-[12px]">
        <span className="font-mono text-[#172033]">{file.path}</span>
        <span className="text-[#475569]">{repositoryId}</span>
        <span className="flex-1" />
        <span className="text-[#166534]">+{file.additions}</span>
        <span className="text-[#991B1B]">−{file.deletions}</span>
      </header>
      <div className="font-mono text-[12px] overflow-x-auto">
        {file.lines.map((l, i) => {
          const isEditing = editing?.repositoryId === repositoryId && editing.path === file.path && editing.line.kind === l.kind &&
            editing.line.oldNumber === l.oldNumber && editing.line.newNumber === l.newNumber;
          const existing = l.kind === 'hunk' ? [] : commentsOn(repositoryId, file.path, l);
          return (
            <div key={i}>
              {l.kind === 'hunk' ? (
                <div className={`px-3 whitespace-pre ${LINE_STYLE.hunk}`}>{l.text}</div>
              ) : (
                <button type="button" className={`w-full text-left flex whitespace-pre hover:outline hover:outline-1 hover:outline-[#93C5FD] ${LINE_STYLE[l.kind]}`}
                  data-testid={`diff-line-${file.path}-${l.kind}-${l.kind === 'del' ? l.oldNumber : l.newNumber}`}
                  aria-label={`Comment on ${file.path} line ${l.kind === 'del' ? `${l.oldNumber} (removed)` : l.newNumber}`}
                  onClick={() => onPick(l)}>
                  <span className="w-10 text-right pr-2 text-[#94A3B8] select-none">{l.oldNumber ?? ''}</span>
                  <span className="w-10 text-right pr-2 text-[#94A3B8] select-none">{l.newNumber ?? ''}</span>
                  <span className="w-4 select-none">{l.kind === 'add' ? '+' : l.kind === 'del' ? '-' : ' '}</span>
                  <span>{l.text}</span>
                </button>
              )}
              {existing.map((c, j) => (
                <div key={j} className="flex items-start gap-2 px-3 py-1 bg-[#FFFBEB] border-y border-[#FDE68A] font-sans" data-testid="line-comment">
                  <span className="text-[12px] text-[#78350F] whitespace-pre-wrap flex-1">{c.body}</span>
                  <Button size="compact" intent="quiet" onClick={() => onRemove(c)}>Remove</Button>
                </div>
              ))}
              {isEditing && (
                <div className="px-3 py-2 bg-[#F8FAFC] border-y border-[#CDD5DF] font-sans">
                  <textarea aria-label="Line comment" autoFocus rows={2} value={draft} onChange={e => setDraft(e.target.value)}
                    className="w-full border border-[#CDD5DF] rounded-[6px] p-2 text-[13px]" />
                  <div className="flex gap-2 justify-end mt-1">
                    <Button size="compact" intent="secondary" onClick={onCancel}>Cancel</Button>
                    <Button size="compact" intent="primary" disabled={!draft.trim()} onClick={onAdd}>Add comment</Button>
                  </div>
                </div>
              )}
            </div>
          );
        })}
        {truncated && <p className="px-3 py-1 text-[12px] text-[#92400E] font-sans">The diff was cut at the size limit; open the Workspace tab for the rest.</p>}
      </div>
    </section>
  );
}
