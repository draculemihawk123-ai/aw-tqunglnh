import { useMutation, useQuery } from '@tanstack/react-query';
import { getWorkspaceBaseStatus, syncWorkspaceWithBase } from '../api/generated';
import { withSessionToken } from '../api/session';
import type { RepositoryWorkspaceState } from '../api/workspaceinspection';
import { Button } from '../components/ui';

/** ports.BaseSyncResult (V12-20). */
export interface BaseSyncResult {
  baseRef: string;
  baseRevision: string;
  head: string;
  outcome?: 'UP_TO_DATE' | 'FAST_FORWARDED' | 'NEEDS_MERGE' | 'DIRTY' | string;
  merged: boolean;
  ahead: number;
  behind: number;
  fetched: boolean;
  fetchProblem?: string;
}

const OUTCOME_TEXT: Record<string, string> = {
  UP_TO_DATE: 'Already up to date with',
  FAST_FORWARDED: 'Fast-forwarded to',
  NEEDS_MERGE: 'The branch diverged from',
  DIRTY: 'Uncommitted changes: not moved to',
};

/**
 * V12-20 (docs/design/15-v12-ui-operations.md, UX-24, ADR-055): how the family worktree stands against the repository's base branch,
 * and "Sync with base branch" — fetch it from origin, fast-forward, record the new HEAD, in one audited step. Never a merge, never a
 * push: a diverged branch is reported for a person to merge.
 */
export function BaseBranchStrip({ projectId, repo, isOffline, onChanged }: { projectId: string; repo: RepositoryWorkspaceState; isOffline?: boolean; onChanged: () => void }) {
  const status = useQuery({
    queryKey: ['workspaceBaseStatus', projectId, repo.repositoryWorkspaceId, repo.version],
    queryFn: async () => (await getWorkspaceBaseStatus(projectId, repo.repositoryWorkspaceId, withSessionToken())) as unknown as BaseSyncResult,
    enabled: !isOffline && repo.state === 'READY',
    retry: false,
  });
  const sync = useMutation({
    mutationFn: async () => (await syncWorkspaceWithBase(projectId, repo.repositoryWorkspaceId, {},
      withSessionToken({ ifMatch: `"${repo.version}"`, idempotencyKey: crypto.randomUUID() }))) as unknown as { sync: BaseSyncResult },
    onSuccess: () => { onChanged(); void status.refetch(); },
  });
  if (repo.state !== 'READY' || status.isError) return null;
  const s = sync.data?.sync ?? status.data;
  if (!s) return null;
  return (
    <div className="px-4 py-2 bg-[#F8FAFC] border-b border-[#CDD5DF] flex items-center gap-3 flex-wrap text-[12px] text-[#475569] flex-shrink-0" data-testid="base-branch">
      <span>
        Base branch <span className="font-mono text-[#172033]">{s.baseRef}</span>:{' '}
        {s.behind > 0 ? `${s.behind} new commit${s.behind === 1 ? '' : 's'}` : 'nothing new'}
        {s.ahead > 0 ? `, ${s.ahead} commit${s.ahead === 1 ? '' : 's'} not in ${s.baseRef}` : ''}
      </span>
      {s.merged && <span className="px-1.5 py-0.5 rounded bg-[#DCFCE7] text-[#166534]">merged into {s.baseRef}</span>}
      {sync.data && (
        <span role="status" className={sync.data.sync.outcome === 'NEEDS_MERGE' || sync.data.sync.outcome === 'DIRTY' ? 'text-[#92400E]' : 'text-[#166534]'}>
          {OUTCOME_TEXT[sync.data.sync.outcome ?? ''] ?? sync.data.sync.outcome} {s.baseRef}
          {sync.data.sync.outcome === 'NEEDS_MERGE' ? ' — merge it in the worktree, then adopt the commits' : ''}
          {sync.data.sync.fetchProblem ? ` (fetch from origin failed: ${sync.data.sync.fetchProblem})` : ''}
        </span>
      )}
      {sync.isError && <span role="alert" className="text-[#991B1B]">{sync.error instanceof Error ? sync.error.message : 'Sync failed.'}</span>}
      <span className="flex-1" />
      <Button size="compact" intent="secondary" disabled={isOffline} loading={sync.isPending} onClick={() => sync.mutate()}>Sync with base branch</Button>
    </div>
  );
}
