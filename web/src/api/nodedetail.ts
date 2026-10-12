/**
 * Hand-declared wire shapes and pure helpers for V11-11's node detail pane (ADR-042).
 *
 * `getNodeRunDetail` and `listAttemptEvents` nest deeper than apicontract's shallow generator expands, so the generated client types
 * most of their fields `unknown`; these mirror internal/app/runtime/node_run_detail.go and attempt_events.go field-for-field, the same
 * "narrow `unknown` at the call site" convention every sibling API-gap file uses.
 */

import type { TimelineEntryView, UsageView } from './rundetail';

export interface NodePin {
  kind: string;
  definitionId: string;
  versionId: string;
}

export interface NodeDefinition {
  type: string;
  outcomes?: string[];
  role?: string;
  pins?: NodePin[];
  failureOutcome?: string;
  authorizedRoles?: string[];
  timeoutSeconds?: number;
}

export interface NodeArtifact {
  artifactId: string;
  mediaType: string;
  sizeBytes: number;
}

export interface NodeEvidence {
  evidenceId: string;
  attemptId: string;
  kind: string;
  verdict: string;
  createdAt: string;
  artifacts: NodeArtifact[];
}

export interface NodeText {
  text: string;
  truncated?: boolean;
  evidenceId?: string;
  /** The words of an attempt that did not succeed: what the agent had said when it stopped. */
  partial?: boolean;
}

export interface NodeChangedFile {
  repositoryId: string;
  path: string;
  status: string;
}

export interface NodeCommandResult {
  exitCode: number;
  durationMillis: number;
  truncated?: boolean;
  argv?: string[];
  stdout?: string;
  stderr?: string;
  evidenceId?: string;
  textTruncated?: boolean;
}

export interface NodeCriterion {
  evidenceId: string;
  kind: string;
  verdict: string;
}

export interface NodeResult {
  kind: 'AGENT' | 'COMMAND' | 'MACHINE_GATE' | 'APPROVAL' | 'WAIT' | 'STRUCTURAL' | string;
  selectedOutcome?: string;
  finalMessage?: NodeText;
  changedFiles?: NodeChangedFile[];
  changedFilesTruncated?: boolean;
  command?: NodeCommandResult;
  criteria?: NodeCriterion[];
  usage?: UsageView;
  durationMillis?: number;
  note?: string;
}

export interface NodeProgress {
  attemptId: string;
  attemptState: string;
  running: boolean;
  eventCount: number;
  toolCalls: number;
  messages: number;
  lastKind?: string;
  lastActivity?: string;
  lastAt?: string;
}

export interface ExplainedFailure {
  code: string;
  title: string;
  explanation: string;
  suggestion: string;
  detail?: string;
  /** RETRY_WORK_ITEM, RECREATE_WORK_ITEM, RECONCILE_WORKSPACE, ADOPT_HEAD, CHECK_PROVIDER or absent. */
  action?: string;
}

export interface NodeApproval {
  approvalRequestId: string;
  state: string;
  authorizedRoles?: string[];
  resolvedOutcome?: string;
  dueAt: string;
}

export interface NodeRunDetail {
  nodeRunId: string;
  runId: string;
  workItemId: string;
  projectId: string;
  nodeKey: string;
  nodeType: string;
  state: string;
  iteration: number;
  activationSequence: number;
  selectedOutcome?: string;
  blockReason?: string;
  reactivationReason?: string;
  definition: NodeDefinition;
  attempts: TimelineEntryView[];
  evidence: NodeEvidence[];
  approval?: NodeApproval;
  result: NodeResult;
  progress?: NodeProgress;
  failure?: ExplainedFailure;
}

export interface AttemptTool {
  callId?: string;
  name: string;
  input?: string;
  output?: string;
  isError?: boolean;
  exitCode?: number;
}

export interface AttemptEvent {
  sequence: number;
  kind: string;
  at: string;
  message?: string;
  tool?: AttemptTool;
  diagnostic?: { code: string; message?: string };
  usage?: UsageView;
  truncated?: boolean;
}

export interface AttemptEventsPage {
  attemptId: string;
  nodeRunId: string;
  attemptState: string;
  finished: boolean;
  events: AttemptEvent[];
  nextSequence: number;
  hasMore: boolean;
}

/** Whole seconds as m:ss ("02:10"), the way a running node shows how long it has been going. */
export function formatElapsed(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  const minutes = Math.floor(s / 60);
  const seconds = s % 60;
  return `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
}

/** A duration in milliseconds as a short phrase ("41s", "2m 05s", "850ms"). */
export function formatDurationMillis(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const seconds = Math.round(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  return `${Math.floor(seconds / 60)}m ${String(seconds % 60).padStart(2, '0')}s`;
}

/**
 * The one line a graph node shows so its state is readable without a click: "✓ approved · 41s", "✗ SCOPE_VIOLATION", "● running 02:10".
 * `entries` are the run timeline's entries; `now` is injected so the label is testable.
 */
export function nodeOneLiner(nodeRunId: string | undefined, entries: TimelineEntryView[], now: number): string {
  if (!nodeRunId) return '';
  const mine = entries.filter(e => e.nodeRunId === nodeRunId);
  const node = mine.find(e => e.kind === 'NODE_RUN');
  const attempts = mine.filter(e => e.kind === 'EXECUTION_ATTEMPT').sort((a, b) => (a.attemptNumber ?? 0) - (b.attemptNumber ?? 0));
  const last = attempts[attempts.length - 1];
  if (last && (last.attemptState === 'RUNNING' || last.attemptState === 'QUEUED')) {
    const started = last.startedAt ? Date.parse(last.startedAt) : NaN;
    const elapsed = Number.isNaN(started) ? '' : ` ${formatElapsed((now - started) / 1000)}`;
    return `● ${last.attemptState === 'QUEUED' ? 'queued' : 'running'}${elapsed}`;
  }
  if (last && last.failureCode && ['FAILED', 'TIMED_OUT'].includes(last.attemptState ?? '')) return `✗ ${last.failureCode}`;
  const state = node?.nodeState ?? last?.attemptState ?? '';
  if (state === 'SUCCEEDED' || state === 'COMPLETED') {
    const outcome = node?.selectedOutcome ?? last?.selectedOutcome;
    const duration = attempts.reduce((sum, a) => (a.startedAt && a.finishedAt ? sum + (Date.parse(a.finishedAt) - Date.parse(a.startedAt)) : sum), 0);
    return ['✓', outcome, duration > 0 ? `· ${formatDurationMillis(duration)}` : ''].filter(Boolean).join(' ');
  }
  if (state === 'FAILED') return '✗ failed';
  if (state === 'WAITING' || state === 'BLOCKED') return `⏸ ${state.toLowerCase()}`;
  return '';
}
