import { useQuery } from '@tanstack/react-query';
import { getProjectCosts, listWorkItemKanban } from '../api/generated';
import { withSessionToken } from '../api/session';
import type { KanbanCard } from '../api/kanban';
import { batchesOf, progressOf, statusReport } from '../api/projectStatus';
import { Button, InlineError } from '../components/ui';
import { needsYouQueryOptions } from './NeedsYou';
import type { ProjectCosts } from './Costs';

const MAX_PAGES = 20;

/** Every card of the project's board (the board pages at the server's limit). */
async function allCards(projectId: string): Promise<KanbanCard[]> {
  const cards: KanbanCard[] = [];
  let cursor: string | undefined;
  for (let i = 0; i < MAX_PAGES; i++) {
    const page = (await listWorkItemKanban(projectId, withSessionToken({ query: { limit: '200', ...(cursor ? { cursor } : {}) } }))) as unknown as { items: KanbanCard[]; nextCursor?: string };
    cards.push(...page.items);
    if (!page.nextCursor) break;
    cursor = page.nextCursor;
  }
  return cards;
}

/**
 * V12-24 (docs/design/15-v12-ui-operations.md, UX-37): the project's status on its Overview — each batch's progress, what runs now,
 * what waits for a person, what it cost — and Export report, the same as Markdown (replaces a STATUS.md kept by hand).
 */
export function ProjectStatus({ projectId, projectName, isOffline, onOpenTask }: { projectId: string; projectName: string; isOffline?: boolean; onOpenTask?: (id: string) => void }) {
  const cardsQuery = useQuery({ queryKey: ['projectStatusCards', projectId], queryFn: () => allCards(projectId), enabled: !isOffline, refetchInterval: 30_000 });
  const needsYou = useQuery(needsYouQueryOptions(projectId, isOffline));
  const costs = useQuery({
    queryKey: ['projectCosts', projectId],
    queryFn: async () => (await getProjectCosts(projectId, withSessionToken())) as unknown as ProjectCosts,
    enabled: !isOffline,
  });
  const cards = cardsQuery.data ?? [];
  const batches = batchesOf(cards);
  const running = cards.filter(c => c.activeRunId);
  const costOf = new Map((costs.data?.batches ?? []).map(b => [b.rootWorkItemId, b]));

  function exportReport() {
    const text = statusReport({ projectName, generatedAt: new Date(), cards, needsYou: needsYou.data?.items ?? [], costs: costs.data });
    const url = URL.createObjectURL(new Blob([text], { type: 'text/markdown' }));
    const a = document.createElement('a');
    a.href = url;
    a.download = `STATUS-${projectName.replace(/[^\w.-]+/g, '_')}-${new Date().toISOString().slice(0, 10)}.md`;
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <section className="bg-white border border-[#CDD5DF] rounded-[8px] p-4" aria-label="Project status">
      <div className="flex items-center gap-3 flex-wrap">
        <h2 className="text-lg font-semibold text-[#172033]">Status</h2>
        <span className="text-[13px] text-[#475569]" data-testid="status-summary">
          {batches.length} batch{batches.length === 1 ? '' : 'es'} · {running.length} running · {needsYou.data?.count ?? 0} waiting for you
          {costs.data ? ` · $${costs.data.usage.costUsd.toFixed(2)} so far` : ''}
        </span>
        <span className="flex-1" />
        <Button size="compact" intent="secondary" disabled={!cardsQuery.data} onClick={exportReport}>Export report</Button>
      </div>
      {cardsQuery.isError && <InlineError code="STATUS" message="Could not load the board." onRetry={() => { void cardsQuery.refetch(); }} />}
      <ul className="mt-3 space-y-2" aria-label="Batches">
        {batches.map(b => {
          const pct = Math.round(progressOf(b) * 100);
          const cost = costOf.get(b.rootWorkItemId);
          return (
            <li key={b.rootWorkItemId} data-testid={`status-batch-${b.rootWorkItemId}`}>
              <div className="flex items-center gap-2 text-[13px]">
                <button type="button" className="font-medium text-[#172033] hover:underline text-left" onClick={() => onOpenTask?.(b.rootWorkItemId)}>{b.title}</button>
                <span className="text-[12px] text-[#475569]">{b.status}</span>
                <span className="flex-1" />
                <span className="text-[12px] text-[#475569]">{b.done}/{b.total - b.cancelled} done{b.blocked ? ` · ${b.blocked} blocked` : ''}{b.active ? ` · ${b.active} running` : ''}</span>
                {cost && <span className={`text-[12px] font-mono ${cost.overBudget ? 'text-[#991B1B]' : 'text-[#475569]'}`}>${cost.usage.costUsd.toFixed(2)}</span>}
              </div>
              <div className="h-1.5 bg-[#ECEFF4] rounded mt-1" role="progressbar" aria-label={`${b.title} progress`} aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
                <div className={`h-1.5 rounded ${b.blocked ? 'bg-[#F59E0B]' : 'bg-[#16A34A]'}`} style={{ width: `${pct}%` }} />
              </div>
            </li>
          );
        })}
        {cardsQuery.data && batches.length === 0 && <li className="text-[13px] text-[#475569]">No work yet.</li>}
      </ul>
      {running.length > 0 && (
        <div className="mt-3">
          <h3 className="text-[12px] font-semibold text-[#475569]">Running now</h3>
          <ul className="text-[12px] mt-1 space-y-0.5" aria-label="Running now">
            {running.map(c => <li key={c.workItemId}><button type="button" className="hover:underline" onClick={() => onOpenTask?.(c.workItemId)}>{c.title}</button> <span className="text-[#475569]">{c.activeRunStatus}</span></li>)}
          </ul>
        </div>
      )}
    </section>
  );
}
