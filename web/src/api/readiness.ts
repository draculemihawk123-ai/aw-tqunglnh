// View types for the repository readiness routes (V9-08, gap G8). The generated
// client types nested objects as `unknown`, so — like catalog.ts — the screens
// that read them cast to these hand-written shapes, which mirror
// internal/app/readinesscheck/operator.go field for field.

export interface ReadinessCommandView {
  executable: string;
  argv?: string[];
  workingDirectory?: string;
  timeoutSeconds: number;
}

export interface ReadinessProfileView {
  version: number;
  setup?: ReadinessCommandView;
  verification: ReadinessCommandView;
}

export type BaselineStateName = 'NOT_REQUIRED' | 'PENDING' | 'PASS' | 'FAIL' | 'EXCEPTION_ACCEPTED';

export interface BaselineAttemptView {
  attemptId: string;
  stage: string;
  outcome: 'GREEN' | 'RED' | 'ENVIRONMENT_ERROR';
  /** PRE_EXISTING_FAILURE (the repository was already red) or ENVIRONMENT_ERROR (the check could not run). */
  failureKind?: string;
  exitCode?: number;
  durationMs: number;
  stdoutExcerpt?: string;
  stderrExcerpt?: string;
  errorCode?: string;
  errorMessage?: string;
  profileVersion: number;
  createdAt: string;
}

export interface BaselineExceptionView {
  exceptionId: string;
  reason: string;
  acceptedBy: string;
  acceptedAt: string;
}

export interface WorkspaceBaselineView {
  repositoryWorkspaceId: string;
  workspaceSetId: string;
  generation: number;
  workspaceState: string;
  baselineState: BaselineStateName;
  admitsWriters: boolean;
  reason?: string;
  attempt?: BaselineAttemptView;
  exception?: BaselineExceptionView;
}

export interface RepositoryReadinessView {
  repositoryId: string;
  profile?: ReadinessProfileView;
  workspaces: WorkspaceBaselineView[];
}
