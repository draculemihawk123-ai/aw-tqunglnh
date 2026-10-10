import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/react-query';
import { Badge, Button, Checkbox, Dialog, InlineError, TextField } from '../components/ui';
import { getApprovalRelease, getFamilyReleaseContext, getWorkspaceSetState, restoreParkedChanges } from '../api/generated';
import { withSessionToken } from '../api/session';
import { fetchWorkingTreeDiff, workingTreeQueryKey, summarizeUncommitted } from '../api/workspaceinspection';
import type { DiffContent, DiffFileChange, RepositoryWorkspaceState, WorkspaceSetState } from '../api/workspaceinspection';
import type { ApprovalRequestView } from '../api/rundetail';
import {
  buildCommitBody, checkStateOf, describeOperation, groupByDirectory,
} from '../api/approvalrelease';
import type { ApprovalCommitBody, ApprovalReleaseStatus, CheckState, ReleaseContext, Unselected } from '../api/approvalrelease';
import { ApiError } from '../api/generated';

/**
 * V11-07 (ADR-040): approve and commit in one action. ApprovalCommitDialog is what an approver sees when the
 * node's release policy offers a commit for the outcome they picked: the uncommitted files (grouped by directory
 * with tri-state boxes), the git author each commit would carry, what happens to the files not chosen, and the
 * message. ReleaseStatusStrip shows afterwards how each requested commit went, and puts parked files back.
 */

function errorText(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  return err instanceof Error ? err.message : 'unexpected error';
}

const STATUS_LABEL: Record<string, string> = { ADDED: 'added', MODIFIED: 'modified', DELETED: 'deleted', TYPE_CHANGED: 'type changed' };

function TriCheckbox({ label, state, onChange }: { label: string; state: CheckState; onChange: (checked: boolean) => void }) {
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = state === 'some';
  }, [state]);
  return (
    <label className="flex items-center gap-2 cursor-pointer">
      <input ref={ref} type="checkbox" aria-label={label} checked={state === 'all'} onChange={e => onChange(e.target.checked)}
        className="w-4 h-4 rounded border-[#CDD5DF] accent-[#3659E3]" />
      <span className="text-[12px] font-mono text-[#172033]">{label}</span>
    </label>
  );
}

interface RepositoryChanges {
  repo: RepositoryWorkspaceState;
  diff?: DiffContent;
  files: DiffFileChange[];
  loading: boolean;
  error?: unknown;
}

export interface ApprovalCommitDialogProps {
  projectId: string;
  familyId?: string;
  approval: ApprovalRequestView;
  outcome: string;
  defaultMessage: string;
  isOffline?: boolean;
  pending: boolean;
  error?: unknown;
  onClose: () => void;
  /** `undefined` means "decide without committing". */
  onSubmit: (commit: ApprovalCommitBody | undefined) => void;
}

export function ApprovalCommitDialog({ projectId, familyId, approval, outcome, defaultMessage, isOffline, pending, error, onClose, onSubmit }: ApprovalCommitDialogProps) {
  const required = approval.release?.commit === 'REQUIRED';
  const [commit, setCommit] = useState(required);
  const [message, setMessage] = useState(defaultMessage);
  const [unselected, setUnselected] = useState<Unselected>('KEEP');
  const [selected, setSelected] = useState<Record<string, Set<string>>>({});

  const setQuery = useQuery({
    queryKey: ['workspaceSetState', projectId, familyId],
    queryFn: async () => (await getWorkspaceSetState(projectId, familyId!, withSessionToken())) as unknown as WorkspaceSetState,
    enabled: !isOffline && !!familyId,
  });
  const contextQuery = useQuery({
    queryKey: ['releaseContext', projectId, familyId],
    queryFn: async () => (await getFamilyReleaseContext(projectId, familyId!, withSessionToken())) as unknown as ReleaseContext,
    enabled: !isOffline && !!familyId,
  });
  const repos = useMemo(
    () => (setQuery.data?.repositoryWorkspaces ?? []).filter(r => r.state === 'READY' && !!r.currentRevision),
    [setQuery.data],
  );
  const diffResults = useQueries({
    queries: repos.map(repo => ({
      queryKey: workingTreeQueryKey(projectId, repo),
      queryFn: () => fetchWorkingTreeDiff(projectId, repo),
      enabled: !isOffline,
    })),
  });
  const changes: RepositoryChanges[] = repos.map((repo, index) => {
    const result = diffResults[index];
    const diff = result?.data;
    return { repo, diff, files: diff?.uncommitted ? diff.files : [], loading: !result || result.isPending, error: result?.error ?? undefined };
  });

  // By default every changed file is selected; a repository's selection is seeded once its diff arrives.
  useEffect(() => {
    setSelected(current => {
      let next = current;
      for (const c of changes) {
        if (c.diff && !current[c.repo.repositoryWorkspaceId]) {
          if (next === current) next = { ...current };
          next[c.repo.repositoryWorkspaceId] = new Set(c.files.map(f => f.path));
        }
      }
      return next;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [diffResults.map(r => r.dataUpdatedAt).join(',')]);

  const loading = setQuery.isPending || contextQuery.isPending || changes.some(c => c.loading);
  const totalChanged = changes.reduce((sum, c) => sum + (c.diff?.uncommitted ? (c.diff.totalFiles ?? c.files.length) : 0), 0);
  const truncated = changes.some(c => c.diff?.filesTruncated);
  const identityOf = (repositoryWorkspaceId: string) => contextQuery.data?.repositories.find(r => r.repositoryWorkspaceId === repositoryWorkspaceId)?.identity;

  const repositoriesForBody = changes.map(c => ({
    repositoryWorkspaceId: c.repo.repositoryWorkspaceId, version: c.repo.version, files: c.files,
    selected: selected[c.repo.repositoryWorkspaceId] ?? new Set<string>(),
  }));
  const body = buildCommitBody(message, repositoriesForBody, unselected);
  const selectedCount = body.repositories.reduce((n, r) => n + r.files.length, 0);
  const unselectedCount = changes.reduce((n, c) => n + c.files.length, 0) - selectedCount;
  const identityBlocked = body.repositories.some(r => !identityOf(r.repositoryWorkspaceId)?.ready);

  let blocker = '';
  if (commit && !loading) {
    if (totalChanged > 0 && selectedCount === 0) blocker = 'Select at least one file, or untick Commit to decide without committing.';
    else if (selectedCount > 0 && !message.trim()) blocker = 'A commit message is required.';
    else if (identityBlocked) blocker = 'Git is not configured with an author for a repository you are committing to.';
  }
  const submit = () => {
    if (!commit) { onSubmit(undefined); return; }
    onSubmit(totalChanged === 0 ? { message: message.trim() || defaultMessage, repositories: [] } : body);
  };

  const toggleFile = (id: string, path: string, on: boolean) => setSelected(cur => {
    const next = new Set(cur[id] ?? []);
    if (on) next.add(path); else next.delete(path);
    return { ...cur, [id]: next };
  });
  const toggleGroup = (id: string, paths: string[], on: boolean) => setSelected(cur => {
    const next = new Set(cur[id] ?? []);
    for (const p of paths) { if (on) next.add(p); else next.delete(p); }
    return { ...cur, [id]: next };
  });

  return (
    <Dialog
      title={`Decide: ${outcome}`}
      description={required
        ? 'This approval requires a commit decision for this outcome.'
        : 'You can commit the worktree\'s changes in the same action as the decision.'}
      onClose={onClose}
      isDurable={pending}
      actions={
        <>
          <Button intent="secondary" onClick={onClose} disabled={pending}>Cancel</Button>
          <Button intent="primary" loading={pending} disabled={isOffline || loading || !!blocker} onClick={submit}>
            {commit && selectedCount > 0 ? `${outcome} and commit ${selectedCount} file${selectedCount === 1 ? '' : 's'}` : outcome}
          </Button>
        </>
      }
    >
      <div className="space-y-3 text-[13px]">
        {required
          ? <p className="text-[#475569]">Commit is required for <span className="font-mono">{outcome}</span>{totalChanged === 0 ? ' — the worktree has nothing to commit.' : '.'}</p>
          : <Checkbox label="Commit the worktree's changes with this decision" checked={commit} onChange={setCommit} />}

        {commit && loading && <p className="text-[#475569]" role="status">Reading the uncommitted changes…</p>}

        {commit && !loading && totalChanged === 0 && (
          <p className="text-[#475569]" role="status">The worktree has no uncommitted changes — nothing will be committed.</p>
        )}

        {commit && !loading && totalChanged > 0 && (
          <>
            {changes.filter(c => c.files.length > 0).map(c => {
              const id = c.repo.repositoryWorkspaceId;
              const identity = identityOf(id);
              const sel = selected[id] ?? new Set<string>();
              const allPaths = c.files.map(f => f.path);
              return (
                <section key={id} aria-label={`Changes in ${c.repo.repositoryId}`} className="border border-[#CDD5DF] rounded">
                  <header className="px-3 py-2 bg-[#F3F5F8] border-b border-[#CDD5DF] flex items-center gap-2 flex-wrap">
                    <span className="font-medium text-[#172033]">{c.repo.repositoryId}</span>
                    <span className="text-[#475569]">· {c.files.length} file{c.files.length === 1 ? '' : 's'} changed</span>
                    <div className="flex-1" />
                    <TriCheckbox label={`All files in ${c.repo.repositoryId}`} state={checkStateOf(allPaths, sel)} onChange={on => toggleGroup(id, allPaths, on)} />
                  </header>
                  <div className="px-3 py-2 border-b border-[#ECEFF4] text-[12px]">
                    {identity?.ready
                      ? <span className="text-[#475569]">Author: <span className="text-[#172033]">{identity.name} &lt;{identity.email}&gt;</span> (git config, {identity.nameSource === identity.emailSource ? identity.nameSource : `${identity.nameSource}/${identity.emailSource}`})</span>
                      : (
                        <div role="alert" className="text-[#991B1B] space-y-1">
                          <p>{identity?.problem ?? 'The git author cannot be read for this repository.'} — commits are disabled for it until git has an author.</p>
                          <p className="font-mono text-[11px] text-[#475569]">git config --global user.name &quot;Your Name&quot;<br />git config --global user.email &quot;you@example.com&quot;</p>
                          <Button size="compact" intent="secondary" onClick={() => contextQuery.refetch()}>Check again</Button>
                        </div>
                      )}
                  </div>
                  <div className="px-3 py-2 space-y-2 max-h-64 overflow-y-auto">
                    {groupByDirectory(c.files).map(group => {
                      const paths = group.files.map(f => f.path);
                      return (
                        <div key={group.directory || '.'}>
                          <TriCheckbox label={group.directory ? `${group.directory}/` : '(top level)'} state={checkStateOf(paths, sel)} onChange={on => toggleGroup(id, paths, on)} />
                          <ul className="ml-6 mt-1 space-y-0.5" aria-label={`Files in ${group.directory || 'top level'}`}>
                            {group.files.map(f => (
                              <li key={f.path} className="flex items-center gap-2 text-[12px]">
                                <input type="checkbox" aria-label={f.path} checked={sel.has(f.path)} onChange={e => toggleFile(id, f.path, e.target.checked)}
                                  className="w-4 h-4 rounded border-[#CDD5DF] accent-[#3659E3]" />
                                <span className="font-mono text-[#172033] flex-1 min-w-0 truncate">{f.path.slice(group.directory ? group.directory.length + 1 : 0)}</span>
                                <span className="text-[#475569]">{STATUS_LABEL[f.status ?? 'MODIFIED'] ?? f.status}{f.untracked ? ' · new' : ''}</span>
                                {f.symlink ? <span className="text-[#475569]">symlink</span>
                                  : f.binary ? <span className="text-[#475569]">binary · {f.sizeBytes ?? 0} bytes</span>
                                  : <><span className="text-[#166534]">+{f.additions}</span><span className="text-[#991B1B]">−{f.deletions}</span></>}
                              </li>
                            ))}
                          </ul>
                        </div>
                      );
                    })}
                  </div>
                </section>
              );
            })}
            {truncated && <p className="text-[#92400E]" role="status">The list is cut at the server's file limit; files beyond it cannot be selected.</p>}

            <TextField label="Commit message" required value={message} onChange={setMessage} placeholder="Describe the change" />

            <fieldset className="space-y-1">
              <legend className="text-[12px] font-semibold text-[#475569]">Files you do not select</legend>
              <label className="flex items-start gap-2"><input type="radio" name="unselected" checked={unselected === 'KEEP'} onChange={() => setUnselected('KEEP')} className="mt-1 accent-[#3659E3]" />
                <span><span className="font-medium">Keep</span> them in the worktree, uncommitted</span></label>
              <label className="flex items-start gap-2"><input type="radio" name="unselected" checked={unselected === 'PARK'} onChange={() => setUnselected('PARK')} className="mt-1 accent-[#3659E3]" />
                <span><span className="font-medium">Park</span> them in a hidden ref and clean the worktree (restorable)</span></label>
              {unselectedCount > 0 && unselected === 'KEEP' && (
                <p className="text-[12px] text-[#92400E]" role="status">
                  {unselectedCount} file{unselectedCount === 1 ? '' : 's'} will stay uncommitted: the next read-only check of this family will be blocked until they are committed or parked.
                </p>
              )}
            </fieldset>
          </>
        )}

        {blocker && <p className="text-[12px] text-[#991B1B]" role="status">{blocker}</p>}
        {error != null && <InlineError code={error instanceof ApiError ? error.code : 'ERROR'} message={errorText(error)} />}
      </div>
    </Dialog>
  );
}

// ─── Release status ──────────────────────────────────────────────────────────

interface ReleaseStatusStripProps {
  projectId: string;
  runId: string;
  approvals: ApprovalRequestView[];
  isOffline?: boolean;
  /** Opens the Workspace tab, where the changes can be reviewed again after a failed commit. */
  onOpenWorkspace?: () => void;
}

/** For every decided approval of a node that offers a commit: how each requested commit went. */
export function ReleaseStatusStrip({ projectId, runId, approvals, isOffline, onOpenWorkspace }: ReleaseStatusStripProps) {
  const decided = approvals.filter(a => a.release && a.state === 'DECIDED');
  if (decided.length === 0) return null;
  return (
    <div className="px-4 py-2 bg-[#F3F5F8] border-b border-[#CDD5DF] space-y-2 flex-shrink-0" aria-label="Commits requested with decisions">
      {decided.map(a => <ReleaseStatus key={a.approvalRequestId} projectId={projectId} runId={runId} approval={a} isOffline={isOffline} onOpenWorkspace={onOpenWorkspace} />)}
    </div>
  );
}

function ReleaseStatus({ projectId, runId, approval, isOffline, onOpenWorkspace }: { projectId: string; runId: string; approval: ApprovalRequestView; isOffline?: boolean; onOpenWorkspace?: () => void }) {
  const queryClient = useQueryClient();
  const query = useQuery({
    queryKey: ['approvalRelease', runId, approval.approvalRequestId],
    queryFn: async () => (await getApprovalRelease(runId, approval.approvalRequestId, withSessionToken())) as unknown as ApprovalReleaseStatus,
    enabled: !isOffline,
    refetchInterval: q => (q.state.data?.operations.some(o => o.state === 'REQUESTED') ? 2000 : false),
  });
  const restore = useMutation({
    mutationFn: ({ repositoryWorkspaceId, operationId }: { repositoryWorkspaceId: string; operationId: string }) =>
      restoreParkedChanges(projectId, repositoryWorkspaceId, { releaseSetLocalCommitId: operationId }, withSessionToken()),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['approvalRelease', runId, approval.approvalRequestId] });
      queryClient.invalidateQueries({ queryKey: ['workspaceWorkingTreeDiff'] });
      queryClient.invalidateQueries({ queryKey: ['releaseContext'] });
    },
  });

  const operations = query.data?.operations ?? [];
  if (query.isError) return <InlineError code={query.error instanceof ApiError ? query.error.code : 'ERROR'} message={errorText(query.error)} />;
  if (operations.length === 0) return null;
  return (
    <div className="text-[12px] space-y-1">
      <div className="text-[#475569]">Commit requested with the decision on <span className="font-mono">{approval.nodeKey}</span>:</div>
      <ul className="space-y-1">
        {operations.map(op => (
          <li key={op.releaseSetLocalCommitId} className="flex items-center gap-2 flex-wrap">
            <span className="font-medium text-[#172033]">{op.repositoryId}</span>
            <Badge label={op.state} intent={op.state === 'COMMITTED' ? 'success' : op.state === 'FAILED' ? 'danger' : 'info'} />
            <span className="text-[#475569]">{describeOperation(op)} · {op.files.length} file{op.files.length === 1 ? '' : 's'}</span>
            {op.state === 'FAILED' && onOpenWorkspace && <Button size="compact" intent="quiet" onClick={onOpenWorkspace}>Review changes</Button>}
            {op.parkedRef && !op.restored && (
              <Button size="compact" intent="secondary" disabled={isOffline}
                loading={restore.isPending && restore.variables?.operationId === op.releaseSetLocalCommitId}
                onClick={() => restore.mutate({ repositoryWorkspaceId: op.repositoryWorkspaceId, operationId: op.releaseSetLocalCommitId })}>
                Restore {op.parkedPaths?.length ?? 0} parked file{(op.parkedPaths?.length ?? 0) === 1 ? '' : 's'}
              </Button>
            )}
            {op.parkedRef && op.restored && <span className="text-[#475569]">parked files restored</span>}
          </li>
        ))}
      </ul>
      {restore.isError && <InlineError code={restore.error instanceof ApiError ? restore.error.code : 'ERROR'} message={errorText(restore.error)} />}
    </div>
  );
}

export { summarizeUncommitted };
