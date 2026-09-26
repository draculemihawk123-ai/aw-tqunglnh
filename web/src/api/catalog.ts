/**
 * Hand-declared wire shapes for the catalog operations (projects,
 * repositories, components) — internal/delivery/httpapi/catalog registers
 * every one of its routes with `RequestSchema: struct{}{}, ResponseSchema:
 * struct{}{}` (a known, self-documented gap: apicontract's own package doc
 * comment names catalog as one of three leaf packages that never supplied a
 * real schema), so the generated client returns `unknown` for all of them.
 * These interfaces mirror internal/delivery/httpapi/catalog/views.go's own
 * view types and internal/app/catalog/commands.go's own Request/Result
 * types field-for-field — the same "narrow `unknown` at the call site"
 * convention web/src/screens/Doctor.tsx already established for DoctorCheck.
 */

export type RepositoryStatus = 'REGISTERING' | 'PROBING' | 'ACTIVE' | 'BLOCKED' | 'DISABLED';

export interface ProjectView {
  id: string;
  name: string;
  status: string;
  version: number;
}

export interface RepositoryView {
  id: string;
  projectId: string;
  name: string;
  remoteLocator: string;
  defaultRef: string;
  status: RepositoryStatus;
  lastProbeErrorCode?: string;
  version: number;
}

export interface ProbeAttemptView {
  id: string;
  jobId: string;
  state: string;
  result?: string;
  errorCode?: string;
  errorMessage?: string;
  baseCommit?: string;
  dirty?: boolean;
  createdAt: string;
}

export interface OnboardingView {
  repositoryId: string;
  projectId: string;
  status: RepositoryStatus;
  lastProbeErrorCode?: string;
  version: number;
  attempts: ProbeAttemptView[];
}

export interface ComponentView {
  id: string;
  projectId: string;
  repositoryId: string;
  name: string;
  path: string;
  kind: string;
  version: number;
}

export interface CreateProjectResult {
  projectId: string;
  name: string;
  status: string;
}

export interface RegisterRepositoryResult {
  repositoryId: string;
  projectId: string;
  status: string;
  probeJobId: string;
}
