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

/**
 * OrphanedAttemptDiagnostic (V7-16's own real "job/lease/recovery
 * diagnostics" line) — internal/delivery/httpapi/diagnostics/dto.go's own
 * orphanedAttemptResponse: an attempt whose owning job/lease was
 * interrupted (crash, lease expiry) and is now a candidate the recovery
 * reaper reconciles — never a PID/argv/cwd, per that package's own
 * doc comment.
 */
export interface OrphanedAttemptDiagnostic {
  attemptId: string;
  nodeRunId: string;
  attemptNumber: number;
  providerKey?: string;
  startedAt?: string;
  repositoryWorkspaceId?: string;
  hasWriteLease: boolean;
}

/** ProviderDiagnostic — AdapterBuildID/ProviderKey/ProviderConfigured only, never a live drift re-probe or ExecutablePath. */
export interface ProviderDiagnostic {
  adapterBuildId: string;
  providerKey: string;
  providerConfigured: boolean;
  nodeRunIds?: string[];
}

/** IsolationDiagnostic — the isolation tier this run's own nodes are pinned to, and whether it is actually enforceable in this environment. */
export interface IsolationDiagnostic {
  tier: string;
  enforceable: boolean;
}

/**
 * RepositoryWorkspaceDiagnostic — "Fence" is Generation, "Quarantine" is
 * State === 'QUARANTINED', "Lease" is HasActiveWriteLease (mirrors
 * internal/delivery/httpapi/workspacestate.go's own vocabulary).
 */
export interface RepositoryWorkspaceDiagnostic {
  repositoryWorkspaceId: string;
  repositoryId: string;
  state: string;
  generation: number;
  hasActiveWriteLease: boolean;
}

export interface RunDiagnosticsResponse {
  runId: string;
  projectId: string;
  workItemId: string;
  workItemStatus: string;
  runState: string;
  blockers: BlockerDiagnostic[];
  orphanedAttempts: OrphanedAttemptDiagnostic[];
  orphanedAttemptsTruncated: boolean;
  providers: ProviderDiagnostic[];
  isolation: IsolationDiagnostic[];
  repositoryWorkspaces: RepositoryWorkspaceDiagnostic[];
  validActions: ValidAction[];
}
