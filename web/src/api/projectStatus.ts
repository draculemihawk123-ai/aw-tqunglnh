/**
 * V12-24 (docs/design/15-v12-ui-operations.md, UX-37): the project's status — progress by batch (a root work item and every task
 * under it), what waits for a person, what runs now, what it cost — and the same as a Markdown report the operator can save, in
 * place of the STATUS.md kept by hand. Pure, so the page and the report say the same thing.
 */
import type { KanbanCard } from './kanban';

export interface BatchProgress {
  rootWorkItemId: string;
  title: string;
  status: string;
  total: number;
  done: number;
  cancelled: number;
  active: number;
  blocked: number;
  tasks: KanbanCard[];
}

export interface StatusInputs {
  projectName: string;
  generatedAt: Date;
  cards: KanbanCard[];
  needsYou: { kind: string; workItemTitle: string; nodeKey?: string; type?: string; message: string }[];
  costs?: { usage: { costUsd: number }; batches: { rootWorkItemId: string; usage: { costUsd: number }; budgetUsd?: number; overBudget: boolean }[] };
}

const isDone = (s: string) => s === 'DONE';
const isCancelled = (s: string) => s === 'CANCELLED';

/** Groups the cards by batch: a root card and every card whose chain of parents reaches it. Children are counted, the root is not. */
export function batchesOf(cards: KanbanCard[]): BatchProgress[] {
  const byId = new Map(cards.map(c => [c.workItemId, c]));
  const rootOf = (c: KanbanCard): string => {
    let cur = c;
    for (let i = 0; i < 64 && cur.parentWorkItemId && byId.has(cur.parentWorkItemId); i++) cur = byId.get(cur.parentWorkItemId)!;
    return cur.workItemId;
  };
  const batches = new Map<string, BatchProgress>();
  for (const card of cards) {
    const root = rootOf(card);
    const rootCard = byId.get(root)!;
    let batch = batches.get(root);
    if (!batch) {
      batch = { rootWorkItemId: root, title: rootCard.title, status: rootCard.status, total: 0, done: 0, cancelled: 0, active: 0, blocked: 0, tasks: [] };
      batches.set(root, batch);
    }
    if (card.workItemId === root) continue;
    batch.tasks.push(card);
    batch.total++;
    if (isDone(card.status)) batch.done++;
    else if (isCancelled(card.status)) batch.cancelled++;
    if (card.activeRunId) batch.active++;
    if (card.blockerCount > 0 || card.status === 'BLOCKED') batch.blocked++;
  }
  return [...batches.values()].sort((a, b) => Number(isDone(a.status)) - Number(isDone(b.status)) || a.title.localeCompare(b.title));
}

/** Done share of a batch's tasks, cancelled ones left out; a batch without tasks counts its own root. */
export function progressOf(b: BatchProgress): number {
  const counted = b.total - b.cancelled;
  if (counted <= 0) return isDone(b.status) ? 1 : 0;
  return b.done / counted;
}

const money = (v: number) => '$' + v.toFixed(2);

/** The Markdown report. */
export function statusReport(input: StatusInputs): string {
  const batches = batchesOf(input.cards);
  const costOf = new Map((input.costs?.batches ?? []).map(b => [b.rootWorkItemId, b]));
  const out: string[] = [`# Status — ${input.projectName}`, '', `Generated ${input.generatedAt.toISOString()} by aw.`, ''];
  const running = input.cards.filter(c => c.activeRunId);
  out.push(`- Batches: ${batches.length}, tasks done ${batches.reduce((n, b) => n + b.done, 0)} of ${batches.reduce((n, b) => n + b.total - b.cancelled, 0)}`);
  out.push(`- Running now: ${running.length}`, `- Waiting for you: ${input.needsYou.length}`);
  if (input.costs) out.push(`- Cost so far: ${money(input.costs.usage.costUsd)} (as reported by the provider CLI)`);
  out.push('', '## Batches', '');
  for (const b of batches) {
    const cost = costOf.get(b.rootWorkItemId);
    const costText = cost ? ` · ${money(cost.usage.costUsd)}${cost.budgetUsd !== undefined ? ` of ${money(cost.budgetUsd)}${cost.overBudget ? ' (over budget)' : ''}` : ''}` : '';
    out.push(`### ${b.title} — ${b.status}`, '', `${b.done}/${b.total - b.cancelled} tasks done (${Math.round(progressOf(b) * 100)}%)${b.blocked ? `, ${b.blocked} blocked` : ''}${costText}`, '');
    for (const t of b.tasks) {
      const mark = isDone(t.status) ? 'x' : ' ';
      const extra = [t.activeRunId ? `running (${t.activeRunStatus ?? 'active'})` : '', t.blockerCount ? `${t.blockerCount} blocker${t.blockerCount === 1 ? '' : 's'}${t.topBlockerType ? ` (${t.topBlockerType})` : ''}` : '']
        .filter(Boolean).join(', ');
      out.push(`- [${mark}] ${t.title} — ${t.status}${extra ? ` — ${extra}` : ''}`);
    }
    if (b.tasks.length > 0) out.push('');
  }
  out.push('## Waiting for you', '');
  if (input.needsYou.length === 0) out.push('Nothing.', '');
  for (const n of input.needsYou) out.push(`- ${n.workItemTitle}${n.nodeKey ? ` / ${n.nodeKey}` : ''}: ${n.kind === 'APPROVAL_PENDING' ? 'decision' : n.type ?? n.kind} — ${n.message}`);
  if (input.needsYou.length > 0) out.push('');
  return out.join('\n');
}
