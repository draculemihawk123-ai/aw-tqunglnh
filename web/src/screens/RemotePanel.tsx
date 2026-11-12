import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { createWorkspacePullRequest, getWorkspaceRemote, mergeWorkspacePullRequest, pushWorkspaceBranch } from '../api/generated';
import { withSessionToken } from '../api/session';
import type { RepositoryWorkspaceState } from '../api/workspaceinspection';
import { Button, Dialog, InlineError } from '../components/ui';

interface ForgeCheck { name: string; status: string; conclusion: string }
interface PullRequest { number: number; url: string; state: string; draft: boolean; headSha: string; base: string; head: string; title: string; mergeable?: string; checks: ForgeCheck[] }
export interface RemoteView {
  repositoryWorkspaceId: string;
  baseBranch: string;
  remoteUrl?: string;
  remoteBranch: string;
  suggestedBranch: string;
  pushed: boolean;
  pushedRevision?: string;
  head: string;
  unapprovedCommits: string[];
  pullRequest?: PullRequest;
  noChecks: boolean;
  mergeBlockers: string[];
  mergedRevision?: string;
}

type Confirm = { kind: 'push' } | { kind: 'pr' } | { kind: 'merge' } | null;
const short = (sha?: string) => (sha ? sha.slice(0, 8) : '');
const key = () => withSessionToken({ idempotencyKey: crypto.randomUUID() });
const checkOk = (c: ForgeCheck) => c.status === 'COMPLETED' && ['SUCCESS', 'NEUTRAL', 'SKIPPED'].includes(c.conclusion);

/**
 * V12-28 (docs/design/15-v12-ui-operations.md, UX-01, UX-39, ADR-060): the family branch on GitHub — push it (name chosen at the first
 * push; never the base branch, never forced, never over a branch aw did not create), open a pull request (ready, or Draft), read its
 * checks, merge it (a merge commit by default). Everything goes through the machine's own git credential helper and `gh`; aw keeps no
 * token. Every commit must have gone through an approval gate of aw.
 */
export function RemotePanel({ projectId, repo, isOffline, defaultTitle }: { projectId: string; repo: RepositoryWorkspaceState; isOffline?: boolean; defaultTitle: string }) {
  const query = useQuery({
    queryKey: ['workspaceRemote', projectId, repo.repositoryWorkspaceId, repo.version],
    queryFn: async () => (await getWorkspaceRemote(projectId, repo.repositoryWorkspaceId, withSessionToken())) as unknown as RemoteView,
    enabled: !isOffline && repo.state === 'READY',
    retry: false,
    refetchInterval: q => (q.state.data?.pullRequest?.state === 'OPEN' ? 30_000 : false),
  });
  const [confirm, setConfirm] = useState<Confirm>(null);
  const [branch, setBranch] = useState('');
  const [title, setTitle] = useState(defaultTitle);
  const [body, setBody] = useState('');
  const [draft, setDraft] = useState(false);
  const [method, setMethod] = useState('merge');
  const [noCiAck, setNoCiAck] = useState(false);
  const done = () => { setConfirm(null); void query.refetch(); };
  const push = useMutation({
    mutationFn: () => pushWorkspaceBranch(projectId, repo.repositoryWorkspaceId, branch.trim() ? { remoteBranch: branch.trim() } : {}, key()),
    onSuccess: done,
  });
  const openPr = useMutation({
    mutationFn: () => createWorkspacePullRequest(projectId, repo.repositoryWorkspaceId, { title: title.trim(), body, ...(draft ? { draft } : {}) }, key()),
    onSuccess: done,
  });
  const merge = useMutation({
    mutationFn: () => mergeWorkspacePullRequest(projectId, repo.repositoryWorkspaceId, { method, ...(noCiAck ? { acknowledgeNoChecks: true } : {}) }, key()),
    onSuccess: done,
  });

  if (repo.state !== 'READY') return null;
  if (query.isError) {
    return <div className="px-4 py-2 border-b border-[#CDD5DF] text-[12px] text-[#475569]" data-testid="remote">GitHub: {query.error instanceof Error ? query.error.message : 'unavailable'}</div>;
  }
  const v = query.data;
  if (!v) return null;
  const pr = v.pullRequest;
  const error = push.error ?? openPr.error ?? merge.error;
  const pending = push.isPending || openPr.isPending || merge.isPending;
  const remoteBranch = v.remoteBranch || branch.trim() || v.suggestedBranch;

  return (
    <div className="px-4 py-2 bg-white border-b border-[#CDD5DF] text-[12px] text-[#475569] flex-shrink-0 space-y-1" data-testid="remote">
      <div className="flex items-center gap-2 flex-wrap">
        <span className="font-medium text-[#172033]">GitHub</span>
        {!v.remoteUrl && <span>this repository has no <span className="font-mono">origin</span> remote</span>}
        {v.remoteBranch && <span>branch <span className="font-mono text-[#172033]">{v.remoteBranch}</span> {v.pushed ? '· up to date' : `· pushed ${short(v.pushedRevision)}, HEAD is ${short(v.head)}`}</span>}
        {v.unapprovedCommits.length > 0 && <span className="text-[#991B1B]">{v.unapprovedCommits.length} commit{v.unapprovedCommits.length === 1 ? '' : 's'} did not go through an approval gate: nothing can be pushed</span>}
        {pr && (
          <a className="text-[#3659E3] hover:underline" href={pr.url} target="_blank" rel="noreferrer">
            PR #{pr.number} · {pr.state === 'OPEN' && pr.draft ? 'draft' : pr.state.toLowerCase()}
          </a>
        )}
        {pr && pr.checks.length > 0 && (
          <span data-testid="checks">checks {pr.checks.filter(checkOk).length}/{pr.checks.length} passed</span>
        )}
        {pr && v.noChecks && pr.state === 'OPEN' && <span className="text-[#92400E]">no CI on this repository</span>}
        <span className="flex-1" />
        {v.remoteUrl && v.unapprovedCommits.length === 0 && !v.pushed && !v.mergedRevision && (
          <Button size="compact" intent="secondary" disabled={isOffline || pending} onClick={() => setConfirm({ kind: 'push' })}>Push…</Button>
        )}
        {v.pushed && !pr && <Button size="compact" intent="secondary" disabled={isOffline || pending} onClick={() => setConfirm({ kind: 'pr' })}>Open pull request…</Button>}
        {pr?.state === 'OPEN' && (
          <Button size="compact" intent="primary" disabled={isOffline || pending || v.mergeBlockers.length > 0} onClick={() => { setNoCiAck(false); setConfirm({ kind: 'merge' }); }}>Merge…</Button>
        )}
      </div>
      {pr?.state === 'OPEN' && v.mergeBlockers.length > 0 && (
        <ul className="text-[#92400E]" aria-label="Why it cannot be merged">{v.mergeBlockers.map(b => <li key={b}>{b}</li>)}</ul>
      )}
      {error && <InlineError code="GITHUB" message={error instanceof Error ? error.message : 'The GitHub action failed.'} />}

      {confirm?.kind === 'push' && (
        <Dialog title="Push to GitHub" onClose={() => setConfirm(null)}
          description={`Pushes ${short(v.head)} to origin. Never forced; aw never pushes to ${v.baseBranch}.`}
          actions={<>
            <Button intent="secondary" onClick={() => setConfirm(null)} disabled={push.isPending}>Cancel</Button>
            <Button intent="primary" loading={push.isPending} onClick={() => push.mutate()}>Push</Button>
          </>}>
          {!v.remoteBranch ? (
            <label className="block text-[12px] text-[#475569]">Branch on origin (chosen once)
              <input aria-label="Remote branch" value={branch} placeholder={v.suggestedBranch} onChange={e => setBranch(e.target.value)}
                className="block w-full mt-1 border border-[#CDD5DF] rounded-[6px] px-2 py-1 font-mono text-[12px]" />
            </label>
          ) : <p className="text-[12px]">Branch <span className="font-mono">{remoteBranch}</span>.</p>}
          {push.isError && <InlineError code="PUSH" message={push.error instanceof Error ? push.error.message : 'Push failed.'} />}
        </Dialog>
      )}
      {confirm?.kind === 'pr' && (
        <Dialog title="Open a pull request" onClose={() => setConfirm(null)} description={`${v.remoteBranch} → ${v.baseBranch}`}
          actions={<>
            <Button intent="secondary" onClick={() => setConfirm(null)} disabled={openPr.isPending}>Cancel</Button>
            <Button intent="primary" loading={openPr.isPending} disabled={!title.trim()} onClick={() => openPr.mutate()}>Open pull request</Button>
          </>}>
          <label className="block text-[12px] text-[#475569]">Title
            <input aria-label="Pull request title" value={title} onChange={e => setTitle(e.target.value)} className="block w-full mt-1 border border-[#CDD5DF] rounded-[6px] px-2 py-1 text-[13px]" />
          </label>
          <label className="block text-[12px] text-[#475569] mt-2">Description
            <textarea aria-label="Pull request description" rows={3} value={body} onChange={e => setBody(e.target.value)} className="block w-full mt-1 border border-[#CDD5DF] rounded-[6px] p-2 text-[13px]" />
          </label>
          <label className="flex items-center gap-2 text-[12px] text-[#475569] mt-2">
            <input type="checkbox" checked={draft} onChange={e => setDraft(e.target.checked)} /> Draft
          </label>
          {openPr.isError && <InlineError code="PR" message={openPr.error instanceof Error ? openPr.error.message : 'Opening the pull request failed.'} />}
        </Dialog>
      )}
      {confirm?.kind === 'merge' && pr && (
        <Dialog title={`Merge PR #${pr.number}`} onClose={() => setConfirm(null)}
          description={`Merges ${v.remoteBranch} into ${v.baseBranch} on GitHub, only if its head is still ${short(pr.headSha)}.`}
          actions={<>
            <Button intent="secondary" onClick={() => setConfirm(null)} disabled={merge.isPending}>Cancel</Button>
            <Button intent="primary" loading={merge.isPending} disabled={v.noChecks && !noCiAck} onClick={() => merge.mutate()}>Merge</Button>
          </>}>
          <label className="block text-[12px] text-[#475569]">Merge method
            <select aria-label="Merge method" value={method} onChange={e => setMethod(e.target.value)} className="block mt-1 border border-[#CDD5DF] rounded-[4px] px-1 py-0.5 text-[12px] bg-white">
              <option value="merge">merge commit (keeps each task's commits)</option>
              <option value="squash">squash</option>
              <option value="rebase">rebase</option>
            </select>
          </label>
          {v.noChecks && (
            <label className="flex items-center gap-2 text-[12px] text-[#92400E] mt-2">
              <input type="checkbox" checked={noCiAck} onChange={e => setNoCiAck(e.target.checked)} /> This pull request has no CI — merge it without CI
            </label>
          )}
          {merge.isError && <InlineError code="MERGE" message={merge.error instanceof Error ? merge.error.message : 'Merge failed.'} />}
        </Dialog>
      )}
    </div>
  );
}
