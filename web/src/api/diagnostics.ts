/**
 * Hand-declared wire shapes for GET /projects/{projectId}/runs/{runId}/diagnostics
 * — internal/delivery/httpapi/diagnostics/dto.go's own blockerResponse and
 * RunDiagnosticsResponse are nested one level deeper than apicontract's
 * shallow generator expands (blockers/validActions come back as `unknown[]`),
 * the same "narrow `unknown` at the call site" convention every sibling
 * API-gap file this session already established. Field-for-field mirror of
 * that package's own DTOs.
 */

import type { ValidAction } from './kanban';

export type BlockerType =
  | 'RUN_CANCELLED'
  | 'COMPLETION_POLICY_FAILED'
  | 'SCOPE_EXPANSION_REQUIRED'
  | 'ISOLATION_ENFORCEMENT_UNAVAILABLE'
  | 'ADAPTER_BUILD_DRIFT'
  | 'CAPABILITY_REQUIREMENT_UNSATISFIED'
  | 'WRITE_CAPABILITY_OR_GRANT_MISSING';

export interface BlockerDiagnostic {
  blockerId: string;
  type: BlockerType | string;
  state: string;
  reason: string;
  sourceNodeRunId?: string;
  sourceAttemptId?: string;
  openedAt: string;
  version: number;
  admissionReason: boolean;
  validActions: ValidAction[];
}

export interface RunDiagnosticsResponse {
  runId: string;
  projectId: string;
  workItemId: string;
  workItemStatus: string;
  runState: string;
  blockers: BlockerDiagnostic[];
  orphanedAttempts: unknown[];
  orphanedAttemptsTruncated: boolean;
  providers: unknown[];
  isolation: unknown[];
  repositoryWorkspaces: unknown[];
  validActions: ValidAction[];
}
