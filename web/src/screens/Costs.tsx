import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { getProjectCosts, setWorkItemBudget } from '../api/generated';
import { withSessionToken } from '../api/session';
import { Button, InlineError } from '../components/ui';

interface Usage { inputTokens: number; cachedInputTokens: number; outputTokens: number; costUsd: number }
interface TaskCost { workItemId: string; title: string; status: string; runCount: number; usage?: Usage }
interface BatchCost {
  rootWorkItemId: string; title: string; usage: Usage; budgetUsd?: number; overBudget: boolean; nearBudget: boolean; tasks: TaskCost[];
}
interface ProviderLimit { workItemId: string; title: string; nodeRunId?: string; message: string }
export interface ProjectCosts { projectId: string; usage: Usage; batches: BatchCost[]; providerLimits: ProviderLimit[] }

export const usd = (v: number) => '$' + v.toFixed(2);
const tokens = (u?: Usage) => (u ? `${Math.round((u.inputTokens + u.cachedInputTokens) / 1000)}k in · ${Math.round(u.outputTokens / 1000)}k out` : '—');

/**
 * V12-23 (docs/design/15-v12-ui-operations.md, UX-35, UX-36, ADR-057): what the project's work cost — by task, by batch (a root work
 * item and its tasks) and in all — from what the provider CLI reported; a budget per batch (going over pauses its queue); and the
 * provider usage limits hit right now with when aw resumes. The CLI reports no remaining quota, so a limit shows once it is hit.
 */
export function CostsScreen({ projectId, isOffline, onOpenTask }: { projectId: string; isOffline?: boolean; onOpenTask: (id: string) => void }) {
  const query = useQuery({
    queryKey: ['projectCosts', projectId],
    queryFn: async () => (await getProjectCosts(projectId, withSessionToken())) as unknown as ProjectCosts,
    enabled: !isOffline,
    refetchInterval: 30_000,
  });
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState('');
  const budget = useMutation({
    mutationFn: ({ rootId, value }: { rootId: string; value: number | null }) =>
      setWorkItemBudget(rootId, value === null ? {} : { budgetUsd: value }, withSessionToken({ idempotencyKey: crypto.randomUUID() })),
    onSuccess: () => { setEditing(null); void query.refetch(); },
  });
  const data = query.data;
  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="flex items-baseline gap-3 mb-1">
        <h1 className="text-[18px] font-semibold text-[#172033]">Costs</h1>
        {data && <span className="text-[13px] text-[#475569]" data-testid="project-total">Project total <span className="font-mono text-[#172033]">{usd(data.usage.costUsd)}</span> · {tokens(data.usage)}</span>}
      </div>
      <p className="text-[12px] text-[#475569] mb-4">As reported by the provider CLI for every attempt — not a bill. A batch over its budget starts no more queued tasks.</p>
      {query.isError && <InlineError code="COSTS" message="Could not load the costs." onRetry={() => { void query.refetch(); }} />}
      {budget.isError && <InlineError code="BUDGET" message={budget.error instanceof Error ? budget.error.message : 'The budget change failed.'} />}
      {data && data.providerLimits.length > 0 && (
        <section className="bg-[#FEF3C7] border border-[#FCD34D] rounded-[8px] p-3 mb-4" aria-label="Provider limits">
          <h2 className="text-[13px] font-semibold text-[#92400E]">Provider usage limit hit — queues are paused</h2>
          <ul className="mt-1 space-y-1">
            {data.providerLimits.map(l => (
              <li key={`${l.workItemId}:${l.nodeRunId ?? ''}`} className="text-[12px] text-[#92400E]">
                <button type="button" className="underline" onClick={() => onOpenTask(l.workItemId)}>{l.title}</button>: {l.message}
              </li>
            ))}
          </ul>
        </section>
      )}
      <div className="space-y-3">
        {data?.batches.map(b => (
          <section key={b.rootWorkItemId} className="bg-white border border-[#CDD5DF] rounded-[8px] p-4" aria-label={`Batch ${b.title}`} data-testid={`batch-${b.rootWorkItemId}`}>
            <div className="flex items-center gap-2 flex-wrap">
              <button type="button" className="text-[14px] font-semibold text-[#172033] hover:underline" onClick={() => onOpenTask(b.rootWorkItemId)}>{b.title}</button>
              <span className="font-mono text-[13px]">{usd(b.usage.costUsd)}</span>
              {b.budgetUsd !== undefined && (
                <span className={`text-[12px] px-1.5 py-0.5 rounded ${b.overBudget ? 'bg-[#FEE2E2] text-[#991B1B]' : b.nearBudget ? 'bg-[#FEF3C7] text-[#92400E]' : 'bg-[#F3F5F8] text-[#475569]'}`}>
                  {b.overBudget ? 'over budget' : b.nearBudget ? 'near budget' : 'budget'} {usd(b.budgetUsd)}
                </span>
              )}
              <span className="flex-1" />
              {editing === b.rootWorkItemId ? (
                <>
                  <input aria-label={`Budget of ${b.title} in USD`} type="number" min="0.01" step="0.5" value={draft} onChange={e => setDraft(e.target.value)}
                    className="w-24 border border-[#CDD5DF] rounded-[4px] px-1 py-0.5 text-[12px]" />
                  <Button size="compact" intent="primary" disabled={isOffline || !(Number(draft) > 0)} loading={budget.isPending}
                    onClick={() => budget.mutate({ rootId: b.rootWorkItemId, value: Number(draft) })}>Save</Button>
                  {b.budgetUsd !== undefined && <Button size="compact" intent="secondary" disabled={isOffline} onClick={() => budget.mutate({ rootId: b.rootWorkItemId, value: null })}>Remove</Button>}
                  <Button size="compact" intent="quiet" onClick={() => setEditing(null)}>Cancel</Button>
                </>
              ) : (
                <Button size="compact" intent="secondary" disabled={isOffline} onClick={() => { setEditing(b.rootWorkItemId); setDraft(b.budgetUsd ? String(b.budgetUsd) : ''); }}>
                  {b.budgetUsd !== undefined ? 'Change budget' : 'Set budget'}
                </Button>
              )}
            </div>
            <table className="w-full mt-2 text-[12px]" aria-label={`Tasks of ${b.title}`}>
              <thead><tr className="text-left text-[#475569]"><th className="py-1">Task</th><th>Status</th><th>Runs</th><th>Tokens</th><th className="text-right">Cost</th></tr></thead>
              <tbody>
                {b.tasks.map(t => (
                  <tr key={t.workItemId} className="border-t border-[#ECEFF4]">
                    <td className="py-1"><button type="button" className="hover:underline text-left" onClick={() => onOpenTask(t.workItemId)}>{t.title}</button></td>
                    <td>{t.status}</td>
                    <td>{t.runCount}</td>
                    <td>{tokens(t.usage)}</td>
                    <td className="text-right font-mono">{t.usage ? usd(t.usage.costUsd) : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
        ))}
        {data && data.batches.length === 0 && <p className="text-[13px] text-[#475569]">No work yet.</p>}
      </div>
    </div>
  );
}
