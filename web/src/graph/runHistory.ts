/**
 * V12-12 (docs/design/15-v12-ui-operations.md, decisions 6 and 7): the history of a work item's runs merged per node. The graph
 * is drawn from the NEWEST run's workflow version; every activation of every run counts towards `×n` and appears in the node's
 * history, oldest first; a node that is not in the newest version keeps its history, labelled with the older version.
 */
import type { NodeActivationView, RunGraphResponse, TakenEdgeView, TimelineEntryView } from '../api/rundetail';
import type { CanvasActivation, CanvasTakenEdge } from './RunGraphCanvas';

export interface RunInput {
  runId: string;
  runNumber: number;
  workflowVersionId?: string;
  graph?: Pick<RunGraphResponse, 'nodes' | 'activations' | 'takenEdges'>;
  timeline?: TimelineEntryView[];
}

export interface HistoryRow {
  runId: string;
  runNumber: number;
  nodeRunId: string;
  nodeKey: string;
  activationSequence: number;
  iteration: number;
  state: string;
  outcome?: string;
  summary?: string;
  startedAt?: string;
  finishedAt?: string;
  durationMillis?: number;
  costUsd?: number;
  attempts: number;
  /** Set when the row's run used another workflow version than the newest run. */
  olderVersion?: string;
}

export interface MergedHistory {
  activations: CanvasActivation[];
  takenEdges: CanvasTakenEdge[];
  rowsByNode: Map<string, HistoryRow[]>;
  /** Node keys with history that the newest run's version does not have. */
  missingFromLatest: string[];
}

export function mergeRunHistory(runs: RunInput[]): MergedHistory {
  const ordered = [...runs].sort((a, b) => a.runNumber - b.runNumber);
  const latest = ordered[ordered.length - 1];
  const latestKeys = new Set((latest?.graph?.nodes ?? []).map(n => n.key));
  const activations: CanvasActivation[] = [];
  const takenEdges: CanvasTakenEdge[] = [];
  const rowsByNode = new Map<string, HistoryRow[]>();
  const missing = new Set<string>();

  for (const run of ordered) {
    const attemptsOf = new Map<string, TimelineEntryView[]>();
    for (const e of run.timeline ?? []) {
      if (e.kind !== 'EXECUTION_ATTEMPT') continue;
      attemptsOf.set(e.nodeRunId, [...(attemptsOf.get(e.nodeRunId) ?? []), e]);
    }
    const older = latest && run.runId !== latest.runId && run.workflowVersionId && latest.workflowVersionId && run.workflowVersionId !== latest.workflowVersionId
      ? run.workflowVersionId : undefined;
    for (const a of run.graph?.activations ?? []) {
      activations.push({ nodeKey: a.nodeKey, state: a.state, selectedOutcome: a.selectedOutcome, activationSequence: a.activationSequence, runOrder: run.runNumber });
      const row = historyRow(run, a, attemptsOf.get(a.nodeRunId) ?? [], older);
      rowsByNode.set(a.nodeKey, [...(rowsByNode.get(a.nodeKey) ?? []), row]);
      if (latestKeys.size > 0 && !latestKeys.has(a.nodeKey)) missing.add(a.nodeKey);
    }
    for (const t of run.graph?.takenEdges ?? []) takenEdges.push(toCanvasTaken(t));
  }
  for (const rows of rowsByNode.values()) rows.sort((x, y) => x.runNumber - y.runNumber || x.activationSequence - y.activationSequence);
  return { activations, takenEdges, rowsByNode, missingFromLatest: [...missing].sort() };
}

function toCanvasTaken(t: TakenEdgeView): CanvasTakenEdge {
  return { edgeKey: t.edgeKey, fromNodeKey: t.fromNodeKey, outcome: t.outcome, toNodeKey: t.toNodeKey };
}

function historyRow(run: RunInput, a: NodeActivationView, attempts: TimelineEntryView[], olderVersion?: string): HistoryRow {
  let started: string | undefined;
  let finished: string | undefined;
  let duration = 0;
  let timed = 0;
  let cost: number | undefined;
  for (const at of attempts) {
    if (at.startedAt && (!started || at.startedAt < started)) started = at.startedAt;
    if (at.finishedAt && (!finished || at.finishedAt > finished)) finished = at.finishedAt;
    if (at.startedAt && at.finishedAt) {
      duration += Date.parse(at.finishedAt) - Date.parse(at.startedAt);
      timed++;
    }
    if (at.usage?.costUsd !== undefined) cost = (cost ?? 0) + at.usage.costUsd;
  }
  return {
    runId: run.runId, runNumber: run.runNumber, nodeRunId: a.nodeRunId, nodeKey: a.nodeKey,
    activationSequence: a.activationSequence, iteration: a.iteration, state: a.state, outcome: a.selectedOutcome,
    summary: a.resultSummary, startedAt: started, finishedAt: finished, durationMillis: timed > 0 ? duration : undefined,
    costUsd: cost, attempts: attempts.length, olderVersion,
  };
}

/** "1m 05s", "850 ms" */
export function formatDuration(ms?: number): string {
  if (ms === undefined || Number.isNaN(ms)) return '—';
  if (ms < 1000) return `${ms} ms`;
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`;
}
