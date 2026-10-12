import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Badge, Button, InlineError, Skeleton, StatusBadge } from '../components/ui';
import { ApiError, getNodeRunDetail, listAttemptEvents } from '../api/generated';
import { withSessionToken } from '../api/session';
import { formatDurationMillis } from '../api/nodedetail';
import type { AttemptEvent, AttemptEventsPage, ExplainedFailure, NodeResult, NodeRunDetail } from '../api/nodedetail';

/**
 * V11-11 (ADR-042): the pane that opens when a graph node is picked. Four parts, each read from data the server really has:
 * what the node was asked to do (its pinned definition), what it is doing (the live line and the event stream), what came of it
 * (the result by kind of node) and, when it failed, why and what next. It follows a running attempt by polling; a finished node
 * is fetched once. All text is shown as escaped text, never HTML.
 */

const POLL_MS = 3000;

function errorText(err: unknown): { code: string; message: string } {
  if (err instanceof ApiError) return { code: err.code, message: err.message };
  return { code: 'UNKNOWN', message: err instanceof Error ? err.message : 'unexpected error' };
}

function Section({ title, children, testId }: { title: string; children: ReactNode; testId?: string }) {
  return (
    <section className="border border-[#ECEFF4] rounded-[8px] bg-white p-3" data-testid={testId} aria-label={title}>
      <h3 className="text-[12px] font-semibold text-[#475569] mb-2">{title}</h3>
      {children}
    </section>
  );
}

/** Follows an attempt's event stream: pages by sequence, keeps asking while the attempt runs. */
export function useAttemptEvents(attemptId: string | undefined, enabled: boolean) {
  const [events, setEvents] = useState<AttemptEvent[]>([]);
  const [finished, setFinished] = useState(false);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    setEvents([]);
    setFinished(false);
    setError(null);
    if (!attemptId || !enabled) return undefined;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let after = 0;
    const tick = async () => {
      try {
        const page = (await listAttemptEvents(attemptId, { ...withSessionToken(), query: { cursor: String(after), limit: '200' } })) as unknown as AttemptEventsPage;
        if (cancelled) return;
        if (page.events.length > 0) setEvents(prev => [...prev, ...page.events]);
        after = page.nextSequence;
        if (page.hasMore) {
          void tick();
          return;
        }
        if (page.finished) {
          setFinished(true);
          return;
        }
        timer = setTimeout(() => { void tick(); }, POLL_MS);
      } catch (err) {
        if (!cancelled) setError(err);
      }
    };
    void tick();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [attemptId, enabled]);
  return { events, finished, error };
}

function EventLine({ event }: { event: AttemptEvent }) {
  const time = new Date(event.at).toLocaleTimeString();
  if (event.kind === 'ASSISTANT_MESSAGE') {
    return <li className="text-[12px]"><span className="text-[#475569]">{time}</span> <span className="font-medium text-[#172033]">said</span> <span className="whitespace-pre-wrap text-[#172033]">{event.message}</span>{event.truncated && <span className="text-[#92400E]"> (cut)</span>}</li>;
  }
  if ((event.kind === 'TOOL_CALL_STARTED' || event.kind === 'TOOL_CALL_FINISHED') && event.tool) {
    const finished = event.kind === 'TOOL_CALL_FINISHED';
    return (
      <li className="text-[12px]">
        <span className="text-[#475569]">{time}</span>{' '}
        <span className={`font-mono ${event.tool.isError ? 'text-[#991B1B]' : 'text-[#172033]'}`}>{finished ? '↳' : '→'} {event.tool.name}</span>
        {event.tool.input && <span className="font-mono text-[#475569]"> {event.tool.input}</span>}
        {finished && event.tool.output && (
          <details className="ml-4"><summary className="cursor-pointer text-[#475569]">output{event.tool.isError ? ' (error)' : ''}{event.truncated ? ' (cut)' : ''}</summary>
            <pre className="whitespace-pre-wrap font-mono text-[11px] text-[#172033] bg-[#F3F5F8] rounded-[4px] p-2 max-h-40 overflow-auto">{event.tool.output}</pre></details>
        )}
      </li>
    );
  }
  if (event.kind === 'DIAGNOSTIC' && event.diagnostic) {
    return <li className="text-[12px] text-[#92400E]"><span className="text-[#475569]">{time}</span> ⚠ {event.diagnostic.code}{event.diagnostic.message ? `: ${event.diagnostic.message}` : ''}</li>;
  }
  if (event.kind === 'USAGE_REPORTED') return null;
  return <li className="text-[12px] text-[#475569]"><span>{time}</span> {event.kind.toLowerCase().replace(/_/g, ' ')}</li>;
}

function ProgressSection({ detail }: { detail: NodeRunDetail }) {
  const latest = detail.attempts[detail.attempts.length - 1];
  const { events, finished, error } = useAttemptEvents(latest?.attemptId, !!latest?.attemptId);
  const progress = detail.progress;
  if (!latest) return <Section title="Progress"><p className="text-[12px] text-[#475569]">This node has not started an attempt.</p></Section>;
  return (
    <Section title="Progress" testId="node-progress">
      {progress && (
        <p className="text-[12px] mb-2" data-testid="progress-line">
          {progress.running ? <Badge label="RUNNING" intent="runtime" /> : <StatusBadge state={progress.attemptState} entity="workitem" />}{' '}
          attempt {latest.attemptNumber} · {progress.toolCalls} tool call{progress.toolCalls === 1 ? '' : 's'} · {progress.messages} message{progress.messages === 1 ? '' : 's'}
          {progress.lastActivity && <> · last: <span className="font-mono">{progress.lastActivity}</span></>}
          {progress.running && progress.lastAt && <span className="text-[#475569]"> ({Math.max(0, Math.round((Date.now() - Date.parse(progress.lastAt)) / 1000))}s ago)</span>}
        </p>
      )}
      {error != null && <InlineError {...errorText(error)} />}
      {events.length === 0 && !error && <p className="text-[12px] text-[#475569]">{finished ? 'The attempt left no events.' : 'Waiting for the first event…'}</p>}
      <ol className="space-y-1 max-h-64 overflow-auto" aria-label="Attempt events">
        {events.map(e => <EventLine key={e.sequence} event={e} />)}
      </ol>
    </Section>
  );
}

function ResultSection({ result }: { result: NodeResult }) {
  return (
    <Section title="Result" testId="node-result">
      {result.selectedOutcome && <p className="text-[12px] mb-1">Outcome: <span className="font-mono font-medium">{result.selectedOutcome}</span></p>}
      {result.note && <p className="text-[12px] text-[#475569]">{result.note}</p>}
      {result.finalMessage && (
        <div className="mb-2">
          <p className="text-[12px] text-[#475569]">{result.finalMessage.partial ? "Agent's last words before it stopped" : "Agent's final message"}{result.finalMessage.truncated ? ' (cut)' : ''}</p>
          <pre className="whitespace-pre-wrap text-[12px] text-[#172033] bg-[#F3F5F8] rounded-[4px] p-2 max-h-48 overflow-auto">{result.finalMessage.text}</pre>
        </div>
      )}
      {(result.changedFiles?.length ?? 0) > 0 && (
        <div className="mb-2">
          <p className="text-[12px] text-[#475569]">{result.changedFiles!.length} file{result.changedFiles!.length === 1 ? '' : 's'} changed{result.changedFilesTruncated ? ' (list cut)' : ''}</p>
          <ul className="text-[12px] font-mono max-h-32 overflow-auto">
            {result.changedFiles!.map(f => <li key={`${f.repositoryId}:${f.path}`}>{f.status.toLowerCase()} {f.repositoryId}:{f.path}</li>)}
          </ul>
        </div>
      )}
      {result.command && (
        <div className="mb-2 text-[12px]" data-testid="command-result">
          <p>Exit code <span className={`font-mono font-medium ${result.command.exitCode === 0 ? 'text-[#166534]' : 'text-[#991B1B]'}`}>{result.command.exitCode}</span> · {formatDurationMillis(result.command.durationMillis)}{result.command.truncated ? ' · output cut by the runner' : ''}</p>
          {result.command.stdout && <><p className="text-[#475569] mt-1">stdout</p><pre className="whitespace-pre-wrap font-mono text-[11px] bg-[#F3F5F8] rounded-[4px] p-2 max-h-40 overflow-auto">{result.command.stdout}</pre></>}
          {result.command.stderr && <><p className="text-[#475569] mt-1">stderr</p><pre className="whitespace-pre-wrap font-mono text-[11px] bg-[#FEF2F2] rounded-[4px] p-2 max-h-40 overflow-auto">{result.command.stderr}</pre></>}
        </div>
      )}
      {(result.criteria?.length ?? 0) > 0 && (
        <ul className="text-[12px] space-y-0.5" aria-label="Gate criteria">
          {result.criteria!.map(c => <li key={c.evidenceId} className="flex items-center gap-2"><StatusBadge state={c.verdict} entity="workitem" /><span className="font-mono">{c.kind}</span></li>)}
        </ul>
      )}
      <p className="text-[12px] text-[#475569] mt-2">
        {result.durationMillis ? `Took ${formatDurationMillis(result.durationMillis)}` : ''}
        {result.usage && result.usage.costUsd > 0 ? ` · cost $${result.usage.costUsd.toFixed(4)}` : ''}
      </p>
    </Section>
  );
}

const ACTION_TEXT: Record<string, string> = {
  RETRY_WORK_ITEM: 'Use Run Again in the header.',
  RECREATE_WORK_ITEM: 'Use Redo with Latest Version in the header.',
  RECONCILE_WORKSPACE: 'Open the Workspace tab and reconcile it.',
  ADOPT_HEAD: 'Open the Workspace tab and adopt or undo the outside commit.',
  CHECK_PROVIDER: 'Check the provider with aw doctor.',
};

function FailureSection({ failure }: { failure: ExplainedFailure }) {
  return (
    <Section title="Why it failed" testId="node-failure">
      <p role="alert" className="text-[13px] font-medium text-[#991B1B]">{failure.title} <span className="font-mono text-[12px] font-normal">({failure.code})</span></p>
      <p className="text-[12px] mt-1">{failure.explanation}</p>
      {failure.detail && <p className="text-[12px] mt-1 font-mono text-[#475569] whitespace-pre-wrap">{failure.detail}</p>}
      <p className="text-[12px] mt-1"><strong>Next:</strong> {failure.suggestion} {failure.action && ACTION_TEXT[failure.action]}</p>
    </Section>
  );
}

export interface NodeDetailPaneProps {
  nodeRunId: string;
  isOffline?: boolean;
  onClose: () => void;
  /** Opens the Evidence tab filtered to this node. */
  onOpenEvidence?: (nodeKey: string) => void;
}

export function NodeDetailPane({ nodeRunId, isOffline, onClose, onOpenEvidence }: NodeDetailPaneProps) {
  const query = useQuery({
    queryKey: ['nodeRunDetail', nodeRunId],
    queryFn: async () => (await getNodeRunDetail(nodeRunId, withSessionToken())) as unknown as NodeRunDetail,
    enabled: !isOffline,
    refetchInterval: q => (q.state.data?.progress?.running || ['RUNNING', 'QUEUED', 'PENDING', 'READY'].includes(q.state.data?.state ?? '') ? POLL_MS : false),
  });
  const detail = query.data;
  const closeRef = useRef<HTMLButtonElement>(null);
  useEffect(() => { closeRef.current?.focus(); }, [nodeRunId]);

  return (
    <aside className="border-b border-[#CDD5DF] bg-[#F8FAFC] p-3 space-y-2 overflow-y-auto" style={{ maxHeight: '60%' }} aria-label="Node detail" data-testid="node-detail">
      <div className="flex items-center gap-2">
        <h2 className="text-[13px] font-semibold text-[#172033]">{detail ? detail.nodeKey : 'Node'}</h2>
        {detail && <><span className="font-mono text-[12px] text-[#475569]">{detail.nodeType}</span><StatusBadge state={detail.state} entity="workitem" /><span className="text-[12px] text-[#475569]">iteration {detail.iteration}</span></>}
        <div className="flex-1" />
        {detail && onOpenEvidence && <Button size="compact" intent="quiet" onClick={() => onOpenEvidence(detail.nodeKey)}>Evidence</Button>}
        <button ref={closeRef} type="button" className="text-[12px] text-[#475569] hover:text-[#172033] px-2" onClick={onClose} aria-label="Close node detail">Close</button>
      </div>
      {query.isPending && <div aria-busy="true" className="space-y-2"><Skeleton className="h-4 w-1/2" /><Skeleton className="h-16 w-full" /></div>}
      {query.isError && <InlineError {...errorText(query.error)} onRetry={() => { void query.refetch(); }} />}
      {detail && (
        <>
          <Section title="Asked to do" testId="node-input">
            <dl className="text-[12px] space-y-1">
              {detail.definition.role && <div className="flex gap-2"><dt className="text-[#475569] w-24">Role</dt><dd>{detail.definition.role}</dd></div>}
              {(detail.definition.outcomes?.length ?? 0) > 0 && <div className="flex gap-2"><dt className="text-[#475569] w-24">Outcomes</dt><dd className="font-mono">{detail.definition.outcomes!.join(', ')}</dd></div>}
              {detail.definition.failureOutcome && <div className="flex gap-2"><dt className="text-[#475569] w-24">On failure</dt><dd className="font-mono">{detail.definition.failureOutcome}</dd></div>}
              {(detail.definition.authorizedRoles?.length ?? 0) > 0 && <div className="flex gap-2"><dt className="text-[#475569] w-24">Decided by</dt><dd>{detail.definition.authorizedRoles!.join(', ')}</dd></div>}
              {(detail.definition.pins ?? []).map(p => (
                <div key={`${p.kind}:${p.definitionId}:${p.versionId}`} className="flex gap-2"><dt className="text-[#475569] w-24">{p.kind.toLowerCase()}</dt><dd className="font-mono">{p.definitionId} <span className="text-[#475569]">@ {p.versionId}</span></dd></div>
              ))}
              {detail.reactivationReason && <div className="flex gap-2"><dt className="text-[#475569] w-24">Reactivated</dt><dd className="font-mono">{detail.reactivationReason}</dd></div>}
              {detail.blockReason && <div className="flex gap-2"><dt className="text-[#475569] w-24">Blocked</dt><dd className="text-[#991B1B]">{detail.blockReason}</dd></div>}
            </dl>
          </Section>
          {detail.attempts.length > 0 && <ProgressSection key={detail.attempts[detail.attempts.length - 1].attemptId} detail={detail} />}
          <ResultSection result={detail.result} />
          {detail.failure && <FailureSection failure={detail.failure} />}
          {detail.evidence.length > 0 && (
            <Section title="Evidence" testId="node-evidence">
              <ul className="text-[12px] space-y-0.5">
                {detail.evidence.map(e => <li key={e.evidenceId} className="flex items-center gap-2"><span className="font-mono">{e.kind}</span><StatusBadge state={e.verdict} entity="workitem" /></li>)}
              </ul>
            </Section>
          )}
        </>
      )}
    </aside>
  );
}
