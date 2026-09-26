import { useMemo, useState } from 'react';
import { useInfiniteQuery, useQueryClient } from '@tanstack/react-query';
import { listWorkItemKanban, getWorkItemProjectedDetail, markWorkItemReady, ApiError } from '../api/generated';
import { WORKITEM_STATUS_COLUMNS } from '../api/kanban';
import type { KanbanCard, KanbanListResponse, WorkItemProjectedDetailResponse } from '../api/kanban';
import { withSessionToken } from '../api/session';
import {
  Badge, Button, CopyableId, InlineError, ProjectionBanner, Skeleton, StatusBadge,
  ToastViewport, useToasts,
} from '../components/ui';
import { AlertTriangle } from '../components/icons';
import type { ProjectSummary } from './Projects';

interface Props {
  project: ProjectSummary;
  onOpenTask: (workItemId: string) => void;
  isOffline?: boolean;
}

function apiErrorMessage(err: unknown): { code: string; message: string } {
  if (err instanceof ApiError) return { code: err.code, message: err.message };
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

/**
 * KanbanScreen — V7-09's own real project-level WorkItem projection.
 * docs/design/09-v7-alpha-ui.md's own line: "status columns, repository/
 * component filter, pagination và server-provided named valid actions...
 * Không tồn tại generic TransitionWorkItemStatus/set-status."
 *
 * There is no `component` field on the real projected card
 * (`internal/delivery/httpapi/kanban/dto.go`'s own `KanbanCardDTO`) — only
 * `repositoryBadges` — so only a repository filter is real; a "component"
 * filter would have no real data behind it and is not built here.
 *
 * "Mark Ready" is the ONE real named action `internal/delivery/httpapi/
 * kanban` itself ever advertises (`validActionsForReadiness`'s own doc
 * comment), and only once a FRESH authoritative recheck
 * (`GET /work-items/{id}/detail`) says so — the projected card's own
 * `status === 'BACKLOG'` is display-only and may already be stale (this
 * package's own "projection không decide readiness/ValidAction" rule), so
 * clicking the button always re-fetches detail first and only then
 * dispatches the mutation with that fresh `targetVersion` as `If-Match`.
 * Every other status transition (start a run, cancel, resolve a blocker) is
 * a DIFFERENT named command with its own real context this screen does not
 * yet have a UI for (V7-11/V7-12) — never approximated here as a generic
 * status change.
 */
export function KanbanScreen({ project, onOpenTask, isOffline = false }: Props) {
  const [repoFilter, setRepoFilter] = useState<string>('ALL');
  const [markingReady, setMarkingReady] = useState<string | null>(null);
  const { toasts, show, dismiss } = useToasts();
  const queryClient = useQueryClient();

  const query = useInfiniteQuery({
    queryKey: ['kanban', project.id],
    queryFn: async ({ pageParam }) => {
      const q: Record<string, string> = { limit: '200' };
      if (pageParam) q.cursor = pageParam;
      return (await listWorkItemKanban(project.id, { ...withSessionToken(), query: q })) as unknown as KanbanListResponse;
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: lastPage => lastPage.nextCursor,
    enabled: !isOffline,
  });

  const cards = useMemo(() => query.data?.pages.flatMap(p => p.items) ?? [], [query.data]);
  const latestFreshness = query.data?.pages.at(-1)?.freshness;

  const allRepositoryIds = useMemo(() => {
    const ids = new Set<string>();
    for (const card of cards) for (const badge of card.repositoryBadges ?? []) ids.add(badge.repositoryId);
    return [...ids].sort();
  }, [cards]);

  const filtered = repoFilter === 'ALL' ? cards : cards.filter(c => (c.repositoryBadges ?? []).some(b => b.repositoryId === repoFilter));

  async function markReady(card: KanbanCard) {
    if (isOffline) return;
    setMarkingReady(card.workItemId);
    try {
      const detail = (await getWorkItemProjectedDetail(card.workItemId, withSessionToken())) as unknown as WorkItemProjectedDetailResponse;
      const action = detail.validActions.find(a => a.operationId === 'markWorkItemReady');
      if (!action) {
        const reason = detail.readiness.problems?.length ? detail.readiness.problems.join('; ') : `not eligible (status ${detail.readiness.status})`;
        show({ intent: 'danger', message: `${card.title}: cannot mark ready — ${reason}`, duration: 0 });
        return;
      }
      await markWorkItemReady(card.workItemId, {}, withSessionToken({ ifMatch: `"${action.targetVersion}"` }));
      queryClient.invalidateQueries({ queryKey: ['kanban', project.id] });
      show({ intent: 'success', message: `${card.title} marked ready.` });
    } catch (err) {
      show({ intent: 'danger', message: apiErrorMessage(err).message, duration: 0 });
    } finally {
      setMarkingReady(null);
    }
  }

  const freshnessState = latestFreshness?.status === 'DEGRADED' ? 'Degraded' : latestFreshness?.status === 'STALE' ? 'Stale' : 'Fresh';

  return (
    <div className="flex flex-col flex-1 overflow-hidden">
      <div className="px-6 py-3 border-b border-[#CDD5DF] bg-white flex items-center gap-3 flex-wrap flex-shrink-0">
        <h1 className="text-sm font-semibold text-[#172033] mr-2">Board</h1>
        <span className="text-xs text-[#5D697A] mr-2">{project.name}</span>
        <div className="flex-1" />
        <div className="flex items-center gap-2">
          <span className="text-xs text-[#5D697A]">Repository</span>
          <select aria-label="Filter by repository" value={repoFilter} onChange={e => setRepoFilter(e.target.value)}
            className="h-7 px-2 rounded-[6px] border border-[#CDD5DF] bg-white text-xs focus:border-[#3659E3] outline-none">
            <option value="ALL">All</option>
            {allRepositoryIds.map(r => <option key={r} value={r}>{r}</option>)}
          </select>
        </div>
      </div>

      {latestFreshness && (
        <ProjectionBanner state={freshnessState} journalPosition={latestFreshness.asOfJournalPosition} onRefresh={() => query.refetch()} />
      )}

      {query.isPending ? (
        <div className="flex-1 p-4 space-y-2" aria-hidden><Skeleton className="h-24 w-full" /><Skeleton className="h-24 w-full" /></div>
      ) : query.isError ? (
        <div className="p-4"><InlineError {...apiErrorMessage(query.error)} onRetry={() => query.refetch()} /></div>
      ) : (
        <div className="flex-1 overflow-x-auto p-4" tabIndex={0} role="region" aria-label="Board columns, scroll horizontally for more states">
          <div className="flex gap-3 h-full min-w-max">
            {WORKITEM_STATUS_COLUMNS.map(col => {
              const colCards = filtered.filter(c => c.status === col);
              return (
                <div key={col} className="flex flex-col w-64 rounded-[12px] bg-[#F3F5F8] border border-[#CDD5DF] island-shadow overflow-hidden">
                  <div className="px-3 py-2.5 border-b border-[#CDD5DF] flex items-center gap-2">
                    <StatusBadge state={col} entity="workitem" />
                    <span className="text-xs text-[#5D697A] font-medium">{colCards.length}</span>
                  </div>
                  <div className="flex-1 overflow-y-auto p-2 space-y-2">
                    {colCards.length === 0 ? (
                      <div className="text-center py-8 text-xs text-[#5D697A]">No items</div>
                    ) : colCards.map(card => (
                      <div key={card.workItemId} className="bg-white rounded-[8px] border border-[#CDD5DF] p-3 hover:border-[#AAB4C3] transition-colors">
                        <button onClick={() => onOpenTask(card.workItemId)} className="text-xs font-medium text-[#172033] text-left hover:text-[#3659E3] leading-snug line-clamp-2 block w-full">
                          {card.title}
                        </button>

                        {(card.repositoryBadges?.length ?? 0) > 0 && (
                          <div className="flex flex-wrap gap-1 mt-2">
                            {card.repositoryBadges!.map(b => (
                              <span key={b.repositoryId} className="text-[12px] px-1.5 py-0.5 rounded-[4px] bg-[#F1F5F9] text-[#475569] border border-[#CBD5E1]">{b.repositoryId}</span>
                            ))}
                          </div>
                        )}

                        {card.blockerCount > 0 && (
                          <div className="flex items-center gap-1 mt-2">
                            <AlertTriangle size={12} aria-hidden className="text-[#92400E]" />
                            <span className="text-[12px] text-[#92400E]">{card.blockerCount} blocker{card.blockerCount > 1 ? 's' : ''}{card.topBlockerType ? ` (${card.topBlockerType})` : ''}</span>
                          </div>
                        )}

                        {card.pendingScopeExpansionCount > 0 && (
                          <div className="mt-2 text-[12px] text-[#1E40AF] bg-[#DBEAFE] rounded-[4px] px-1.5 py-0.5">
                            {card.pendingScopeExpansionCount} pending scope expansion{card.pendingScopeExpansionCount > 1 ? 's' : ''}
                          </div>
                        )}

                        {card.activeRunStatus && (
                          <div className="mt-2 flex items-center gap-1.5">
                            <Badge label={card.activeRunStatus} intent={card.activeRunStatus === 'VERIFYING' ? 'warning' : 'runtime'} />
                            {card.activeRunStatus === 'VERIFYING' && <span className="text-[11px] text-[#92400E]">completion not yet gate-verified</span>}
                          </div>
                        )}

                        <div className="flex items-center justify-between mt-2">
                          <CopyableId value={card.workItemId} />
                          {card.status === 'BACKLOG' && (
                            <Button size="compact" intent="quiet" loading={markingReady === card.workItemId} disabled={isOffline} onClick={() => markReady(card)}>Mark Ready</Button>
                          )}
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {query.hasNextPage && (
        <div className="px-4 py-2 border-t border-[#CDD5DF] bg-white flex justify-center flex-shrink-0">
          <Button intent="secondary" size="compact" loading={query.isFetchingNextPage} onClick={() => query.fetchNextPage()}>Load more</Button>
        </div>
      )}
      <ToastViewport toasts={toasts} onDismiss={dismiss} />
    </div>
  );
}
