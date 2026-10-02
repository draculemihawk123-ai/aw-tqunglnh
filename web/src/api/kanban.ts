/**
 * Hand-declared wire shapes for the Kanban projection operations —
 * internal/delivery/httpapi/kanban registers RequestSchema:struct{}{} on
 * both its routes (a plain GET, nothing to describe on the request side)
 * but its own real response DTOs (KanbanCardDTO, httpapi.Freshness,
 * WorkItemReadiness) are nested one level deeper than apicontract's
 * generator expands, so the generated client returns `unknown` for the
 * fields that matter. These interfaces mirror
 * internal/delivery/httpapi/kanban/dto.go's own view types field-for-field
 * — the same "narrow `unknown` at the call site" convention
 * web/src/api/catalog.ts and web/src/api/definitions.ts already
 * established for their own sibling packages' identical gap.
 */

export type WorkItemStatus = 'BACKLOG' | 'READY' | 'ACTIVE' | 'BLOCKED' | 'DONE' | 'CANCELLED';
export type FreshnessStatus = 'LIVE' | 'DEGRADED' | 'STALE';

export const WORKITEM_STATUS_COLUMNS: WorkItemStatus[] = ['BACKLOG', 'READY', 'ACTIVE', 'BLOCKED', 'DONE', 'CANCELLED'];

export interface Freshness {
  generation: number;
  asOfJournalPosition: number;
  status: FreshnessStatus;
}

export interface RepositoryBadge {
  repositoryId: string;
  state: string;
}

export interface KanbanCard {
  workItemId: string;
  projectId: string;
  familyId: string;
  title: string;
  parentWorkItemId?: string;
  isRoot: boolean;
  workspaceSetId?: string;
  status: WorkItemStatus;
  activeRunId?: string;
  activeRunStatus?: string;
  blockerCount: number;
  topBlockerType?: string;
  pendingScopeExpansionCount: number;
  /**
   * How many Runs the WorkItem has ever started (V9-06, ADR-033): after a failed
   * Run is resolved the same WorkItem runs again, so one card can stand for
   * several Runs. Projected like the other counts — `runs` on the detail
   * response is the authoritative list. Absent on a response from a server
   * that predates V9-06.
   */
  runCount?: number;
  repositoryBadges?: RepositoryBadge[];
}

export interface KanbanListResponse {
  items: KanbanCard[];
  nextCursor?: string;
  freshness: Freshness;
}

export interface ValidAction {
  operationId: string;
  scopeKind: string;
  targetVersion: number;
}

export interface WorkItemReadiness {
  workItemId: string;
  status: string;
  version: number;
  ready: boolean;
  problems?: string[];
}

/**
 * One Run of a WorkItem — an entry of getWorkItemProjectedDetail's authoritative
 * `runs` list (V9-06, ADR-033): oldest first, `runNumber` is the 1-based position
 * ("the second run"). Every Run of a WorkItem pins the same workflow version.
 */
export interface WorkItemRun {
  runId: string;
  runNumber: number;
  state: string;
  workflowVersionId: string;
  startedAt?: string;
  finishedAt?: string;
}

export interface WorkItemProjectedDetailResponse {
  card: KanbanCard;
  readiness: WorkItemReadiness;
  runs?: WorkItemRun[];
  freshness: Freshness;
  validActions: ValidAction[];
}
