/**
 * Hand-declared wire shapes for GET /runs/{id}/graph and GET /runs/{id}/timeline
 * — internal/delivery/httpapi/rundetail's own response DTOs nest structural
 * graph/activation/timeline objects one level deeper than apicontract's
 * shallow generator expands, so the generated client returns `unknown[]`/
 * `unknown` for the fields that matter. Field-for-field mirror of
 * internal/app/runtime/run_detail_queries.go's own view types, the same
 * "narrow `unknown` at the call site" convention every sibling API-gap file
 * this session already established.
 */

import type { Freshness } from './kanban';

export type NodeType = 'START' | 'END' | 'AGENT' | 'MACHINE_GATE' | 'APPROVAL' | 'FORK' | 'JOIN' | 'ROUTER' | string;

export interface GraphNodeView {
  key: string;
  type: NodeType;
  outcomes?: string[];
  cyclePolicy?: unknown;
}

export type EdgeKind = 'FLOW' | 'COMPLETION_REWORK' | string;

export interface GraphEdgeView {
  key: string;
  from: string;
  outcome: string;
  to: string;
  kind?: EdgeKind;
  reworkPolicy?: unknown;
}

export type NodeRunState = 'PENDING' | 'READY' | 'QUEUED' | 'RUNNING' | 'WAITING' | 'BLOCKED' | 'SUCCEEDED' | 'FAILED' | 'SKIPPED' | 'CANCELLED';

export interface NodeActivationView {
  nodeRunId: string;
  nodeKey: string;
  activationSequence: number;
  iteration: number;
  state: NodeRunState | string;
  selectedOutcome?: string;
  manifestRevision?: number;
  branchTokenId?: string;
  reactivationReason?: string;
  blockReason?: string;
}

export interface BranchTokenView {
  branchTokenId: string;
  forkNodeRunId: string;
  forkKey: string;
  branchKey: string;
  currentNodeKey: string;
  state: string;
}

export interface TakenEdgeView {
  edgeKey?: string;
  fromNodeRunId: string;
  fromNodeKey: string;
  outcome: string;
  toNodeKey?: string;
  activationSequence: number;
}

export interface RunGraphResponse {
  runId: string;
  manifestRevision: number;
  nodes: GraphNodeView[];
  possibleEdges: GraphEdgeView[];
  takenEdges?: TakenEdgeView[];
  activations: NodeActivationView[];
  branchTokens?: BranchTokenView[];
  freshness: Freshness;
  nextCursor?: string;
}

export type TimelineEntryKind = 'NODE_RUN' | 'EXECUTION_ATTEMPT';

export interface TimelineEntryView {
  kind: TimelineEntryKind;
  activationSequence: number;
  nodeRunId: string;
  nodeKey: string;
  iteration: number;
  nodeState?: string;
  selectedOutcome?: string;
  branchTokenId?: string;
  reactivationReason?: string;
  blockReason?: string;

  attemptId?: string;
  attemptNumber?: number;
  attemptState?: string;
  providerKey?: string;
  startedAt?: string;
  finishedAt?: string;
  terminationReason?: string;
  failureCode?: string;
  lastCheckpointId?: string;
  contextSnapshotId?: string;
}

export interface RunTimelineResponse {
  runId: string;
  entries: TimelineEntryView[];
  freshness: Freshness;
  nextCursor?: string;
}

/**
 * ApprovalRequestView (V7-17B) — GET /runs/{id}'s own `approvalRequests`
 * nests one level deeper than apicontract's shallow generator expands, same
 * limit as this file's other types. Mirrors internal/app/runtime/
 * run_detail_queries.go's own ApprovalRequestView field-for-field. State
 * is the real runtimedomain.ApprovalRequestState string — 'PENDING' is the
 * only actionable state; every other value is history. The node's own
 * declared outcome vocabulary a caller may resolve a PENDING request to is
 * deliberately NOT carried here (an open vocabulary the node itself
 * declares, per this type's own Go doc comment) — cross-reference
 * GraphNodeView.outcomes for the matching nodeKey instead, exactly as
 * GraphTimelineTab already does for every other node-keyed decision.
 */
export interface ApprovalRequestView {
  approvalRequestId: string;
  nodeRunId: string;
  nodeKey: string;
  state: 'PENDING' | 'DECIDED' | 'ESCALATED' | 'CANCELLED' | string;
  version: number;
  authorizedRoles?: string[];
  requestedEvidenceKinds?: string[];
  dueAt: string;
  escalationOutcome: string;
  resolvedOutcome?: string;
}
