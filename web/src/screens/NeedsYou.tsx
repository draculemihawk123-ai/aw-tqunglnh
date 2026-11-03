import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { listNeedsYou, resolveApproval } from '../api/generated';
import { withSessionToken } from '../api/session';
import { Button, InlineError } from '../components/ui';

/**
 * V12-16 (docs/design/15-v12-ui-operations.md, UX-28, UX-29): "Needs you" — every approval waiting for a decision and every open
 * blocker of the project's tasks on one page, with a one-click action: an approval's outcomes are decided here; a blocker opens the
 * task where its own actions are (Run Again, Retry, Use the new CLI, Resolve). The menu shows the count, and the browser notifies
 * when a new item appears (after the operator allowed notifications here). External channels (e-mail, webhook) send data out of
 * the machine and wait for their own ADR.
 */
export interface NeedsYouItem {
  kind: 'APPROVAL_PENDING' | 'BLOCKER' | string;
  workItemId: string;
  workItemTitle: string;
  runId?: string;
  nodeRunId?: string;
  nodeKey?: string;
  type?: string;
  outcomes?: string[];
  approvalRequestId?: string;
  approvalVersion?: number;
  authorizedRoles?: string[];
  blockerId?: string;
  message: string;
  since: string;
}

export interface NeedsYouList {
  projectId: string;
  count: number;
  items: NeedsYouItem[];
}

export const NEEDS_YOU_POLL_MS = 15_000;

export function needsYouQueryOptions(projectId: string | undefined, isOffline?: boolean) {
  return {
    queryKey: ['needsYou', projectId],
    queryFn: async () => (await listNeedsYou(projectId!, withSessionToken())) as unknown as NeedsYouList,
    enabled: !isOffline && !!projectId,
    refetchInterval: NEEDS_YOU_POLL_MS,
  };
}

/** Key of an item, to notice new ones between two polls. */
function itemKey(i: NeedsYouItem): string {
  return i.blockerId ?? i.approvalRequestId ?? `${i.workItemId}:${i.nodeRunId ?? ''}:${i.type ?? ''}`;
}

/** Notifies (when the browser allows it) about items that were not there on the previous poll. */
export function useNeedsYouNotifications(items: NeedsYouItem[] | undefined) {
  const seen = useRef<Set<string> | null>(null);
  useEffect(() => {
    if (!items) return;
    const keys = new Set(items.map(itemKey));
    if (seen.current) {
      const fresh = items.filter(i => !seen.current!.has(itemKey(i)));
      try {
        if (fresh.length > 0 && typeof Notification !== 'undefined' && Notification.permission === 'granted') {
          const first = fresh[0];
          new Notification(fresh.length === 1 ? `aw: ${first.workItemTitle}` : `aw: ${fresh.length} things need you`, { body: first.message, tag: 'aw-needs-you' });
        }
      } catch {
        /* notifications unavailable: the menu count still says it */
      }
    }
    seen.current = keys;
  }, [items]);
}

export function NeedsYouScreen({ projectId, isOffline, onOpenTask }: { projectId: string; isOffline?: boolean; onOpenTask: (taskId: string) => void }) {
  const queryClient = useQueryClient();
  const query = useQuery(needsYouQueryOptions(projectId, isOffline));
  const [permission, setPermission] = useState<string>(() => (typeof Notification === 'undefined' ? 'unsupported' : Notification.permission));
  const [message, setMessage] = useState('');

  const decide = useMutation({
    mutationFn: ({ item, outcome }: { item: NeedsYouItem; outcome: string }) =>
      resolveApproval(item.runId!, item.approvalRequestId!, { outcome }, withSessionToken({ ifMatch: `"${item.approvalVersion}"` })),
    onSuccess: (_r, { item, outcome }) => {
      setMessage(`${item.nodeKey}: ${outcome} recorded for ${item.workItemTitle}.`);
      void queryClient.invalidateQueries({ queryKey: ['needsYou', projectId] });
    },
  });

  const items = query.data?.items ?? [];
  return (
    <div className="flex-1 overflow-y-auto p-6 bg-[#E9EDF3]">
      <div className="flex items-center gap-3 mb-4">
        <h1 className="text-[18px] font-semibold text-[#172033]">Needs you</h1>
        <span className="text-[13px] text-[#475569]">{query.data ? `${query.data.count} item${query.data.count === 1 ? '' : 's'}` : ''}</span>
        <span className="flex-1" />
        {permission === 'default' && (
          <Button size="compact" intent="secondary" onClick={async () => {
            try { setPermission(await Notification.requestPermission()); } catch { setPermission('unsupported'); }
          }}>Notify me in this browser</Button>
        )}
        {permission === 'granted' && <span className="text-[12px] text-[#166534]">Browser notifications on</span>}
      </div>
      {message && <p className="text-[13px] text-[#166534] mb-3" role="status">{message}</p>}
      {query.isError && <InlineError code="NEEDS_YOU" message="Could not load what needs you." onRetry={() => { void query.refetch(); }} />}
      {decide.isError && <InlineError code="DECISION_FAILED" message={decide.error instanceof Error ? decide.error.message : 'The decision failed.'} />}
      {query.data && items.length === 0 && <p className="text-[13px] text-[#475569]">Nothing is waiting for you.</p>}
      <ul className="space-y-2" aria-label="Things that need you">
        {items.map(item => (
          <li key={itemKey(item)} className="bg-white border border-[#CDD5DF] rounded-[8px] p-3" data-testid={`needs-you-${itemKey(item)}`}>
            <div className="flex items-center gap-2 flex-wrap">
              <span className={`text-[11px] px-1.5 py-0.5 rounded ${item.kind === 'APPROVAL_PENDING' ? 'bg-[#EEF2FF] text-[#3730A3]' : 'bg-[#FEE2E2] text-[#991B1B]'}`}>
                {item.kind === 'APPROVAL_PENDING' ? 'decision' : item.type}
              </span>
              <span className="text-[13px] font-medium text-[#172033]">{item.workItemTitle}</span>
              {item.nodeKey && <span className="font-mono text-[12px] text-[#475569]">{item.nodeKey}</span>}
              <span className="flex-1" />
              <span className="text-[12px] text-[#475569]">{item.since && !item.since.startsWith('0001') ? new Date(item.since).toLocaleString() : ''}</span>
            </div>
            <p className="text-[13px] text-[#172033] mt-1">{item.message}</p>
            <div className="flex gap-2 mt-2 flex-wrap">
              {item.kind === 'APPROVAL_PENDING' && (item.outcomes ?? []).map(outcome => (
                <Button key={outcome} size="compact" intent="primary" disabled={isOffline || decide.isPending}
                  onClick={() => decide.mutate({ item, outcome })}>{outcome}</Button>
              ))}
              <Button size="compact" intent="secondary" onClick={() => onOpenTask(item.workItemId)}>Open task</Button>
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}
