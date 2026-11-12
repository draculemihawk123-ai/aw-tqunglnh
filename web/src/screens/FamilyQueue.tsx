import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { getWorkItemQueue, setWorkItemQueue } from '../api/generated';
import { withSessionToken } from '../api/session';
import { Button, InlineError } from '../components/ui';

/**
 * V12-19 (docs/design/15-v12-ui-operations.md, UX-30, ADR-054): the family task queue on a task's Overview. The family shares one
 * worktree, so its queue runs one task at a time: the worker starts the next queued task once the one before is DONE (a task that
 * failed pauses the queue), and a task can also wait for another one.
 */
export interface QueueEntry {
  workItemId: string;
  workItemTitle: string;
  workItemStatus: string;
  position: number;
  waitsForWorkItemId?: string;
  state: 'QUEUED' | 'STARTED' | string;
  runId?: string;
  problem?: string;
  version: number;
}

export interface FamilyQueue {
  projectId: string;
  familyId: string;
  entries: QueueEntry[];
  waiting?: string;
}

export function FamilyQueuePanel({ workItemId, isOffline }: { workItemId: string; isOffline?: boolean }) {
  const query = useQuery({
    queryKey: ['familyQueue', workItemId],
    queryFn: async () => (await getWorkItemQueue(workItemId, withSessionToken())) as unknown as FamilyQueue,
    enabled: !isOffline,
    refetchInterval: 15_000,
  });
  const [waitsFor, setWaitsFor] = useState('');
  const change = useMutation({
    mutationFn: (queued: boolean) => setWorkItemQueue(workItemId, { queued, ...(queued && waitsFor ? { waitsForWorkItemId: waitsFor } : {}) },
      withSessionToken({ idempotencyKey: crypto.randomUUID() })),
    onSuccess: () => { void query.refetch(); },
  });

  const entries = query.data?.entries ?? [];
  const mine = entries.find(e => e.workItemId === workItemId);
  const titleOf = (id?: string) => entries.find(e => e.workItemId === id)?.workItemTitle ?? id;
  return (
    <section className="bg-white rounded-[8px] border border-[#CDD5DF] p-4" aria-label="Task queue">
      <div className="flex items-center gap-2 mb-2">
        <h2 className="text-[14px] font-semibold text-[#172033]">Queue</h2>
        <span className="text-[12px] text-[#475569]">one task of this family runs at a time; the next starts when the one before is done</span>
      </div>
      {query.isError && <InlineError code="QUEUE" message="Could not load the queue." onRetry={() => { void query.refetch(); }} />}
      {change.isError && <InlineError code="QUEUE_CHANGE" message={change.error instanceof Error ? change.error.message : 'The queue change failed.'} />}
      {query.data?.waiting && <p className="text-[12px] text-[#92400E] mb-2" role="status" data-testid="queue-waiting">{query.data.waiting}</p>}
      {entries.length > 0 ? (
        <ol className="space-y-1 mb-3" aria-label="Queued tasks">
          {entries.map(e => (
            <li key={e.workItemId} className="flex items-center gap-2 text-[13px]" data-testid={`queue-entry-${e.workItemId}`}>
              <span className="font-mono text-[12px] text-[#475569] w-6">{e.position}</span>
              <span className={e.workItemId === workItemId ? 'font-semibold text-[#172033]' : 'text-[#172033]'}>{e.workItemTitle}</span>
              <span className={`text-[11px] px-1.5 py-0.5 rounded ${e.state === 'STARTED' ? 'bg-[#DCFCE7] text-[#166534]' : 'bg-[#EEF2FF] text-[#3730A3]'}`}>
                {e.state === 'STARTED' ? `started · ${e.workItemStatus}` : 'queued'}
              </span>
              {e.waitsForWorkItemId && <span className="text-[12px] text-[#475569]">after {titleOf(e.waitsForWorkItemId)}</span>}
              {e.problem && <span className="text-[12px] text-[#991B1B]" title={e.problem}>could not start: {e.problem}</span>}
            </li>
          ))}
        </ol>
      ) : query.data && <p className="text-[13px] text-[#475569] mb-3">No task of this family is queued.</p>}
      {mine?.state === 'QUEUED' ? (
        <Button size="compact" intent="secondary" disabled={isOffline} loading={change.isPending} onClick={() => change.mutate(false)}>Remove from queue</Button>
      ) : mine?.state !== 'STARTED' && (
        <div className="flex items-center gap-2 flex-wrap">
          <label className="text-[12px] text-[#475569] flex items-center gap-1">
            Wait for
            <select aria-label="Wait for" value={waitsFor} onChange={e => setWaitsFor(e.target.value)} className="border border-[#CDD5DF] rounded-[4px] px-1 py-0.5 text-[12px] bg-white">
              <option value="">the task before it</option>
              {entries.filter(e => e.workItemId !== workItemId).map(e => <option key={e.workItemId} value={e.workItemId}>{e.workItemTitle}</option>)}
            </select>
          </label>
          <Button size="compact" intent="primary" disabled={isOffline} loading={change.isPending} onClick={() => change.mutate(true)}>Add to queue</Button>
        </div>
      )}
    </section>
  );
}
