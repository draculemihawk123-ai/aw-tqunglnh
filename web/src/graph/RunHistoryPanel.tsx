import { useState } from 'react';
import type { ReactNode } from 'react';
import type { CanvasTakenEdge } from './RunGraphCanvas';
import type { LayoutInputEdge, LayoutInputNode } from './layoutRunGraph';
import { formatDuration } from './runHistory';
import type { HistoryRow } from './runHistory';

/**
 * V12-12 (docs/design/15-v12-ui-operations.md, "Panel lịch sử"): the panel to the right of the run graph for one node — what it
 * is, where each outcome leads and how often it was taken, and every activation of every run of the work item (oldest first),
 * each expandable into the node-run detail (agent output, changed files, command log, decision, evidence). Actions come only
 * from the caller, which takes them from the server's validActions; nothing here infers an action from a state word.
 */
export interface RunHistoryPanelProps {
  node: LayoutInputNode & { cyclePolicy?: unknown };
  edges: LayoutInputEdge[];
  takenEdges: CanvasTakenEdge[];
  rows: HistoryRow[];
  /** The node-run detail of an expanded row (NodeDetailPane). */
  renderDetail: (nodeRunId: string) => ReactNode;
  /** Server-advertised actions for this node (Retry, the outcomes of a pending approval). */
  actions?: ReactNode;
  onClose: () => void;
}

const TYPE_TEXT: Record<string, string> = {
  AGENT: 'An agent does the work', COMMAND: 'A command (a check or a step)', APPROVAL: 'A person decides',
  MACHINE_GATE: 'A machine gate', START: 'Start', END: 'End', FORK: 'Parallel branches start', JOIN: 'Parallel branches join',
};

function maxIterations(cyclePolicy: unknown): number | undefined {
  if (cyclePolicy && typeof cyclePolicy === 'object' && 'maxIterations' in cyclePolicy) {
    const n = (cyclePolicy as { maxIterations: unknown }).maxIterations;
    return typeof n === 'number' ? n : undefined;
  }
  return undefined;
}

export function RunHistoryPanel({ node, edges, takenEdges, rows, renderDetail, actions, onClose }: RunHistoryPanelProps) {
  // The newest activation starts expanded: picking a node shows what it last did, as the old node pane did.
  const [open, setOpen] = useState<string | null>(rows.length > 0 ? rows[rows.length - 1].nodeRunId : null);
  const limit = maxIterations(node.cyclePolicy);
  const outgoing = edges.filter(e => e.from === node.key);
  const taken = (e: LayoutInputEdge) =>
    takenEdges.filter(t => (t.edgeKey ? t.edgeKey === e.key : t.fromNodeKey === e.from && t.outcome === e.outcome)).length;
  const runs = new Set(rows.map(r => r.runNumber)).size;

  return (
    <aside className="flex flex-col h-full overflow-hidden bg-white" aria-label={`History of ${node.key}`} data-testid="node-history">
      <div className="px-4 py-2 border-b border-[#CDD5DF] bg-[#F3F5F8] flex items-start gap-2">
        <div className="flex-1 min-w-0">
          <h3 className="text-[14px] font-semibold text-[#172033] truncate">{node.key}</h3>
          <p className="text-[12px] text-[#475569]">
            <span className="font-mono">{node.type}</span> — {TYPE_TEXT[node.type] ?? node.type}
            {limit !== undefined && <span> · at most {limit + 1} rounds</span>}
          </p>
        </div>
        <button type="button" onClick={onClose} className="text-[12px] text-[#475569] hover:text-[#172033] px-1" aria-label="Close node history">✕</button>
      </div>
      {actions && <div className="px-4 py-2 border-b border-[#ECEFF4] flex gap-2 flex-wrap">{actions}</div>}
      <div className="flex-1 overflow-y-auto">
        {outgoing.length > 0 && (
          <section className="px-4 py-2 border-b border-[#ECEFF4]" aria-label="Outcomes">
            <h4 className="text-[12px] font-semibold text-[#475569] mb-1">Outcomes</h4>
            <ul className="text-[12px] space-y-0.5">
              {outgoing.map(e => (
                <li key={e.key} className="flex gap-2">
                  <span className="font-mono text-[#172033]">{e.outcome}</span>
                  <span className="text-[#475569]">→ {e.to}</span>
                  <span className="ml-auto text-[#475569]" data-testid={`outcome-count-${e.outcome}`}>{taken(e)}×</span>
                </li>
              ))}
            </ul>
          </section>
        )}
        <section className="px-4 py-2" aria-label="History">
          <h4 className="text-[12px] font-semibold text-[#475569] mb-1">
            History {rows.length > 0 && <span className="font-normal">({rows.length} run{rows.length === 1 ? '' : 's'} of this node{runs > 1 ? ` across ${runs} runs of the task` : ''})</span>}
          </h4>
          {rows.length === 0 && <p className="text-[12px] text-[#475569]">Not reached yet.</p>}
          <ol className="space-y-1">
            {rows.map(r => {
              const expanded = open === r.nodeRunId;
              const failed = r.state === 'FAILED' || r.state === 'BLOCKED';
              return (
                <li key={r.nodeRunId} data-testid={`history-row-${r.nodeRunId}`}>
                  <button type="button" aria-expanded={expanded} onClick={() => setOpen(expanded ? null : r.nodeRunId)}
                    className="w-full text-left px-2 py-1.5 rounded-[6px] border border-[#ECEFF4] hover:border-[#CDD5DF] bg-white">
                    <div className="flex items-center gap-2 text-[12px]">
                      <span className="text-[#475569]">Run {r.runNumber}</span>
                      <span className="font-mono text-[#475569]">#{r.activationSequence}</span>
                      {r.olderVersion && <span className="px-1 rounded bg-[#FEF3C7] text-[#92400E]" title={r.olderVersion}>older version</span>}
                      <span className={failed ? 'text-[#991B1B] font-medium' : 'text-[#172033]'}>{r.state.toLowerCase()}</span>
                      {r.outcome && <span className="font-mono text-[#3659E3]">{r.outcome}</span>}
                      <span className="ml-auto text-[#475569]">
                        {r.startedAt ? new Date(r.startedAt).toLocaleTimeString() : ''}{r.durationMillis !== undefined ? ` · ${formatDuration(r.durationMillis)}` : ''}
                        {r.costUsd !== undefined ? ` · $${r.costUsd.toFixed(2)}` : ''}
                      </span>
                    </div>
                    {r.summary && <p className={`text-[12px] mt-0.5 ${failed ? 'text-[#991B1B]' : 'text-[#172033]'}`}>{r.summary}</p>}
                  </button>
                  {expanded && <div className="mt-1 border border-[#ECEFF4] rounded-[6px] overflow-hidden">{renderDetail(r.nodeRunId)}</div>}
                </li>
              );
            })}
          </ol>
        </section>
      </div>
    </aside>
  );
}
