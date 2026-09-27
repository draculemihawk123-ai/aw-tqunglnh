/**
 * Hand-declared wire shape for `listFamilyScopeExpansionRequests`'s own
 * `items` (V7-17): a real, previously-missing query
 * (internal/app/work/queries.go's own `ListFamilyScopeExpansionRequests`,
 * `GET /projects/{projectId}/task-families/{familyId}/scope-expansions`)
 * added because there was otherwise no way for an operator to discover a
 * PENDING ScopeExpansionRequest's own RequestID — the one thing
 * `approveScopeExpansion`/`rejectScopeExpansion` both require —
 * `Kanban.tsx`'s own `pendingScopeExpansionCount` badge is a count, never an
 * ID. `items` nests one level deeper than apicontract's shallow generator
 * expands (the same limit `web/src/api/evidence.ts`'s own doc comment
 * explains), so the generated client returns `unknown[]`; this mirrors
 * `internal/app/work/queries.go`'s own `ScopeExpansionRequestDetail`/
 * `RequestedGrantView` field-for-field.
 */

export interface RequestedGrant {
  repositoryId: string;
  access: string;
  pathScopes?: string[];
  reason: string;
}

export interface ScopeExpansionRequestDetail {
  requestId: string;
  familyId: string;
  projectId: string;
  requestedGrants: RequestedGrant[];
  reason: string;
  referencedWorkItemId?: string;
  status: 'PENDING' | 'APPROVED' | 'REJECTED' | 'WITHDRAWN';
  requestedBy: string;
  requestedAt: string;
  decidedBy?: string;
  decidedAt?: string | null;
  decisionNote?: string;
  approvedScopeVersion?: number | null;
  version: number;
}
