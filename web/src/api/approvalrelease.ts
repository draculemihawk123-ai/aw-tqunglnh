/**
 * Hand-declared wire shapes and pure helpers for V11-07's approve-and-commit (ADR-040).
 *
 * `resolveApproval`'s `commit` object, `getFamilyReleaseContext` and `getApprovalRelease` nest one level
 * deeper than apicontract's shallow generator expands, so the generated client types them `unknown`; these
 * mirror internal/app/approvalrelease and internal/delivery/httpapi/decision field-for-field, the same
 * "narrow `unknown` at the call site" convention every sibling API-gap file uses.
 */

import type { DiffFileChange } from './workspaceinspection';

export interface IdentityView {
  name?: string;
  email?: string;
  nameSource?: string;
  emailSource?: string;
  ready: boolean;
  problem?: string;
}

export interface RepositoryReleaseContext {
  repositoryId: string;
  repositoryWorkspaceId: string;
  version: number;
  state: string;
  currentRevision?: string;
  identity: IdentityView;
}

export interface ParkedSet {
  releaseSetLocalCommitId: string;
  repositoryId: string;
  repositoryWorkspaceId: string;
  ref: string;
  paths: string[];
  parkedAt: string;
}

export interface ReleaseContext {
  familyId: string;
  repositories: RepositoryReleaseContext[];
  parked: ParkedSet[];
}

export interface ReleaseOperationView {
  releaseSetLocalCommitId: string;
  releaseSetId: string;
  repositoryId: string;
  repositoryWorkspaceId: string;
  state: 'REQUESTED' | 'COMMITTED' | 'FAILED' | string;
  failureReason?: string;
  parentVcsObjectId?: string;
  resultVcsObjectId?: string;
  files: string[];
  changeSetDigest: string;
  unselected: 'KEEP' | 'PARK' | string;
  parkedRef?: string;
  parkedPaths?: string[];
  restored?: boolean;
  patchTruncated?: boolean;
  /** Base64 of the working-tree patch the approver reviewed (Go's []byte encoding). */
  reviewedPatch?: string;
}

export interface ApprovalReleaseStatus {
  approvalRequestId: string;
  projectId: string;
  operations: ReleaseOperationView[];
}

export type Unselected = 'KEEP' | 'PARK';

export interface ApprovalCommitFile {
  path: string;
  status?: string;
  contentHash?: string;
}

export interface ApprovalCommitRepository {
  repositoryWorkspaceId: string;
  expectedWorkspaceVersion: number;
  files: ApprovalCommitFile[];
  unselected: Unselected;
}

/** The `commit` object of a resolveApproval request. An empty `repositories` is the "nothing to commit" answer. */
export interface ApprovalCommitBody {
  message: string;
  repositories: ApprovalCommitRepository[];
}

/** The release policy of an approval node as getRunDetail reports it (omitted when DISABLED). */
export interface ApprovalReleasePolicy {
  commit: 'OFFERED' | 'REQUIRED' | string;
  outcomes: string[];
}

/** Whether a decision with `outcome` may carry a commit under `policy`. */
export function commitAllowedFor(policy: ApprovalReleasePolicy | undefined, outcome: string): boolean {
  return !!policy && (policy.commit === 'OFFERED' || policy.commit === 'REQUIRED') && policy.outcomes.includes(outcome);
}

/** The directory part of a repository-relative path ('' for a top-level file). */
export function directoryOf(path: string): string {
  const slash = path.lastIndexOf('/');
  return slash < 0 ? '' : path.slice(0, slash);
}

export type CheckState = 'all' | 'some' | 'none';

/** Tri-state of a group of files given the selected set. */
export function checkStateOf(paths: string[], selected: ReadonlySet<string>): CheckState {
  if (paths.length === 0) return 'none';
  const count = paths.filter(p => selected.has(p)).length;
  if (count === 0) return 'none';
  return count === paths.length ? 'all' : 'some';
}

/** Files grouped by directory, directories sorted, files in the given (path) order. */
export function groupByDirectory(files: DiffFileChange[]): { directory: string; files: DiffFileChange[] }[] {
  const groups = new Map<string, DiffFileChange[]>();
  for (const file of files) {
    const directory = directoryOf(file.path);
    const list = groups.get(directory) ?? [];
    list.push(file);
    groups.set(directory, list);
  }
  return [...groups.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([directory, list]) => ({ directory, files: list }));
}

/**
 * Builds the `commit` object from what the dialog holds: for each repository with at least one selected file,
 * the chosen files with the status and content hash the approver saw. A repository with nothing selected is left out.
 */
export function buildCommitBody(
  message: string,
  repositories: { repositoryWorkspaceId: string; version: number; files: DiffFileChange[]; selected: ReadonlySet<string> }[],
  unselected: Unselected,
): ApprovalCommitBody {
  const out: ApprovalCommitRepository[] = [];
  for (const repository of repositories) {
    const files = repository.files
      .filter(f => repository.selected.has(f.path))
      .map(f => ({ path: f.path, ...(f.status ? { status: f.status } : {}), ...(f.contentHash ? { contentHash: f.contentHash } : {}) }));
    if (files.length === 0) continue;
    out.push({ repositoryWorkspaceId: repository.repositoryWorkspaceId, expectedWorkspaceVersion: repository.version, files, unselected });
  }
  return { message: message.trim(), repositories: out };
}

/** A one-line, human reading of a commit operation's state. */
export function describeOperation(op: ReleaseOperationView): string {
  switch (op.state) {
    case 'REQUESTED': return 'committing…';
    case 'COMMITTED': return `committed ${(op.resultVcsObjectId ?? '').slice(0, 10)}`;
    case 'FAILED': return `failed — ${FAILURE_TEXT[op.failureReason ?? ''] ?? op.failureReason ?? 'unknown reason'}`;
    default: return op.state.toLowerCase();
  }
}

export const FAILURE_TEXT: Record<string, string> = {
  CHANGESET_DRIFT: 'a reviewed file changed after the review (nothing was committed); review the current changes and commit again',
  NO_CHANGES: 'the worktree had nothing to commit',
  HEAD_DRIFT: "the worktree's HEAD is not the revision aw recorded (a commit was made outside aw); adopt it or put the worktree back, then commit again",
  WORKSPACE_QUARANTINED: 'the repository workspace is quarantined',
  MARKER_DRIFT: 'the workspace HEAD does not match this operation; the workspace was quarantined',
};
