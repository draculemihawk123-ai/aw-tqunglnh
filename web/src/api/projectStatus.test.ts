import { describe, expect, it } from 'vitest';
import type { KanbanCard } from './kanban';
import { batchesOf, progressOf, statusReport } from './projectStatus';

const card = (over: Partial<KanbanCard>): KanbanCard => ({
  workItemId: 'x', projectId: 'p', familyId: 'f', title: 'x', isRoot: false, status: 'READY', blockerCount: 0, pendingScopeExpansionCount: 0, ...over,
} as KanbanCard);

const CARDS = [
  card({ workItemId: 'root', title: 'Order status', isRoot: true, status: 'ACTIVE' }),
  card({ workItemId: 't1', parentWorkItemId: 'root', title: 'T-01: API', status: 'DONE' }),
  card({ workItemId: 't2', parentWorkItemId: 'root', title: 'T-02: UI', status: 'ACTIVE', activeRunId: 'r2', activeRunStatus: 'RUNNING' }),
  card({ workItemId: 't3', parentWorkItemId: 't2', title: 'T-02a: fix', status: 'BLOCKED', blockerCount: 1, topBlockerType: 'RUN_FAILED' }),
  card({ workItemId: 't4', parentWorkItemId: 'root', title: 'T-03: old', status: 'CANCELLED' }),
  card({ workItemId: 'root2', title: 'Docs', isRoot: true, status: 'DONE' }),
];

describe('project status (V12-24)', () => {
  it('groups tasks by batch through every level of parents', () => {
    const [orders, docs] = batchesOf(CARDS);
    expect(orders.rootWorkItemId).toBe('root');
    expect(orders).toMatchObject({ total: 4, done: 1, cancelled: 1, active: 1, blocked: 1 });
    expect(progressOf(orders)).toBeCloseTo(1 / 3);
    expect(docs).toMatchObject({ rootWorkItemId: 'root2', total: 0 });
    expect(progressOf(docs)).toBe(1);
  });

  it('writes a Markdown report with batches, tasks, costs and what waits', () => {
    const report = statusReport({
      projectName: 'shop', generatedAt: new Date('2026-11-03T12:00:00Z'), cards: CARDS,
      needsYou: [{ kind: 'APPROVAL_PENDING', workItemTitle: 'T-02: UI', nodeKey: 'review', message: 'review is waiting for a decision' }],
      costs: { usage: { costUsd: 4.5 }, batches: [{ rootWorkItemId: 'root', usage: { costUsd: 4.5 }, budgetUsd: 4, overBudget: true }] },
    });
    expect(report).toContain('# Status — shop');
    expect(report).toContain('- Cost so far: $4.50');
    expect(report).toContain('### Order status — ACTIVE');
    expect(report).toContain('1/3 tasks done (33%), 1 blocked · $4.50 of $4.00 (over budget)');
    expect(report).toContain('- [x] T-01: API — DONE');
    expect(report).toContain('- [ ] T-02: UI — ACTIVE — running (RUNNING)');
    expect(report).toContain('- [ ] T-02a: fix — BLOCKED — 1 blocker (RUN_FAILED)');
    expect(report).toContain('- T-02: UI / review: decision — review is waiting for a decision');
  });
});
