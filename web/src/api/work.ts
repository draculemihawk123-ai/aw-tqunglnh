/**
 * Hand-declared wire shapes for creating a root WorkItem —
 * internal/delivery/httpapi/workitem's own real request/response DTOs
 * (dto.go's scopeGrantBody/workItemContractBody/acceptanceCriterionBody)
 * are nested one level deeper than apicontract's generator expands, so the
 * generated client's own `CreateRootWorkItemRequest.initialScope`/
 * `.contract` come back as `unknown`/`unknown[]`. Mirrors those Go types
 * field-for-field — the same "narrow `unknown` at the call site" convention
 * every sibling API-gap file this session already established.
 */

export type RepositoryAccess = 'READ' | 'WRITE';

export interface ScopeGrant {
  repositoryId: string;
  access: RepositoryAccess;
  pathScopes?: string[];
  reason: string;
}

export interface AcceptanceCriterion {
  description: string;
  verificationRef?: string;
}

export interface WorkItemContract {
  schemaVersion?: number;
  behavior?: string;
  acceptanceCriteria?: AcceptanceCriterion[];
  verificationSpec?: string;
  riskLevel?: string;
  exclusions?: string[];
  workflowVersionId?: string;
}

export interface CreateRootWorkItemResult {
  workItemId: string;
  projectId: string;
  familyId: string;
  workspaceSetId: string;
  status: string;
  provisionedRepositories: { repositoryId: string; provisionJobId: string }[];
}
