/**
 * Hand-declared wire shapes plus one hand-written fetch for the V6-10B/
 * V6-10D workspace-inspection routes this task (V7-13) is the first real
 * consumer of.
 *
 * `getWorkspaceSetState`'s own `repositoryWorkspaces`/`getWorkspaceDiff`'s
 * `baseRevision`/`resultRevision`/`files`/`getWorkspaceRepositoryLog`'s
 * `anchor`/`entries` all nest one level deeper than apicontract's shallow
 * generator expands, so the generated client returns `unknown`/`unknown[]`
 * for them — the same "narrow `unknown` at the call site" convention every
 * sibling API-gap file this session already established, mirroring
 * internal/delivery/httpapi/workspacestate.go's and
 * internal/delivery/httpapi/workspaceinspection/dto.go's own DTOs
 * field-for-field.
 *
 * `getWorkspaceSource` needed a second, different kind of fix: its real
 * response is a raw byte stream (Content-Type/Content-Disposition/ETag plus
 * custom X-Aw-Source-* headers — internal/delivery/httpapi/workspaceinspection/
 * source.go's own writeSourceContent), never a JSON envelope, even though
 * `request()`'s shared helper unconditionally awaits `res.json()` on
 * success. `internal/delivery/httpapi/apicontract.GenerateTypeScriptClient`
 * now deliberately skips generating a callable function for this operation
 * (and evidence's own identically-shaped `getArtifactContent`) rather than
 * emit one that would always throw a JSON-parse error on real content — see
 * tsclient.go's own `rawContentOperations` doc comment. `fetchWorkspaceSource`
 * below is the one hand-written real fetch this task adds in its place.
 */

import { ApiError, getWorkspaceDiff } from './generated';
import { withSessionToken } from './session';
import type { ValidAction } from './kanban';

export interface RepositoryWorkspaceState {
  repositoryWorkspaceId: string;
  workspaceSetId: string;
  repositoryId: string;
  generation: number;
  state: string;
  version: number;
  branchRef?: string;
  baseRevision?: string;
  currentRevision?: string;
  lastProvisionErrorCode?: string | null;
  hasActiveWriteLease: boolean;
  validActions: ValidAction[];
}

export interface WorkspaceSetState {
  workspaceSetId: string;
  familyId: string;
  projectId: string;
  state: string;
  version: number;
  hasBaseRevisionSet: boolean;
  repositoryWorkspaces: RepositoryWorkspaceState[];
  validActions: ValidAction[];
}

export interface RevisionRef {
  repositoryId: string;
  vcsObjectId: string;
  workspaceGeneration: number;
}

export interface DiffFileChange {
  path: string;
  additions: number;
  deletions: number;
  binary: boolean;
  /** Working-tree diff only (V11-06). */
  status?: 'ADDED' | 'MODIFIED' | 'DELETED' | 'TYPE_CHANGED';
  untracked?: boolean;
  symlink?: boolean;
  sizeBytes?: number;
  contentHash?: string;
}

export interface DiffContent {
  baseRevision: RevisionRef;
  resultRevision: RevisionRef;
  files: DiffFileChange[];
  /** Base64-encoded (Go's own []byte JSON encoding) — decode with decodeDiffPatch below. */
  patch: string;
  byteLimit: number;
  fileLimit: number;
  filesTruncated: boolean;
  patchTruncated: boolean;
  /**
   * V11-06: set for a `result=WORKING_TREE` response — the worktree's UNCOMMITTED state on
   * top of `baseRevision` (there is no result commit). `totalFiles` counts the whole change set
   * even when `files` is cut at `fileLimit`.
   */
  uncommitted?: boolean;
  totalFiles?: number;
  changeSetDigest?: string;
}

/** The server's word for "the worktree's uncommitted state" in a diff request (V11-06). */
export const WORKING_TREE_RESULT = 'WORKING_TREE';

/** The worktree's uncommitted state on top of a repository workspace's current revision (V11-06). */
export function fetchWorkingTreeDiff(projectId: string, repo: RepositoryWorkspaceState): Promise<DiffContent> {
  return getWorkspaceDiff(projectId, repo.repositoryWorkspaceId, {
    ...withSessionToken(),
    query: {
      repositoryId: repo.repositoryId, workspaceSetId: repo.workspaceSetId,
      base: repo.currentRevision ?? '', baseGeneration: String(repo.generation), result: WORKING_TREE_RESULT,
    },
  }) as unknown as Promise<DiffContent>;
}

/** The query key under which a repository workspace's working-tree diff is cached (shared by every consumer). */
export function workingTreeQueryKey(projectId: string, repo?: RepositoryWorkspaceState) {
  return ['workspaceWorkingTreeDiff', projectId, repo?.repositoryWorkspaceId, repo?.currentRevision, repo?.version] as const;
}

/** Totals of an uncommitted diff, for one-line summaries ("3 files changed, +41 −2"). */
export function summarizeUncommitted(diff: DiffContent | undefined): { files: number; additions: number; deletions: number } {
  if (!diff?.uncommitted) return { files: 0, additions: 0, deletions: 0 };
  let additions = 0;
  let deletions = 0;
  for (const file of diff.files) {
    additions += file.additions;
    deletions += file.deletions;
  }
  return { files: diff.totalFiles ?? diff.files.length, additions, deletions };
}

/** Decodes GetWorkspaceDiffResponse.patch's own base64 body into the real UTF-8 patch text. */
export function decodeDiffPatch(base64Patch: string): string {
  if (!base64Patch) return '';
  try {
    return new TextDecoder('utf-8', { fatal: false }).decode(Uint8Array.from(atob(base64Patch), c => c.charCodeAt(0)));
  } catch {
    return '';
  }
}

export interface RepositoryLogEntry {
  commitId: string;
  parentIds: string[];
  authorName: string;
  authorEmail: string;
  authoredAt: string;
  subject: string;
  subjectTruncated: boolean;
}

export interface RepositoryLogPage {
  anchor: RevisionRef;
  entries: RepositoryLogEntry[];
  nextCursor?: string;
  limit: number;
  byteLimit: number;
  truncated: boolean;
}

export interface SourceContentResult {
  content: string;
  totalBytes: number;
  lineCount: number;
  byteLimit: number;
  lineLimit: number;
  truncated: boolean;
  binary: boolean;
  revision: string;
  workspaceGeneration: number;
}

/**
 * fetchWorkspaceSource calls GET .../source directly (never through the
 * generated client — see this file's own doc comment) and parses the real
 * response headers/body into a typed result. Binary content's own body is
 * never decoded as text (the server already tells us via X-Aw-Source-Binary
 * — decoding arbitrary binary bytes as UTF-8 would just produce mojibake,
 * never anything a caller could use).
 */
export async function fetchWorkspaceSource(
  projectId: string,
  repositoryWorkspaceId: string,
  params: { repositoryId: string; workspaceSetId: string; path: string; revision: string; generation: number; byteLimit?: number; lineLimit?: number },
): Promise<SourceContentResult> {
  const url = new URL(`/projects/${projectId}/repository-workspaces/${repositoryWorkspaceId}/source`, window.location.origin);
  url.searchParams.set('repositoryId', params.repositoryId);
  url.searchParams.set('workspaceSetId', params.workspaceSetId);
  url.searchParams.set('path', params.path);
  url.searchParams.set('revision', params.revision);
  url.searchParams.set('generation', String(params.generation));
  if (params.byteLimit) url.searchParams.set('byteLimit', String(params.byteLimit));
  if (params.lineLimit) url.searchParams.set('lineLimit', String(params.lineLimit));

  const res = await fetch(url);
  if (!res.ok) {
    const errBody = await res.json().catch(() => null);
    throw new ApiError(res.status, errBody?.error?.code ?? 'UNKNOWN', errBody?.error?.message ?? res.statusText, errBody?.error?.details);
  }
  const binary = res.headers.get('X-Aw-Source-Binary') === 'true';
  const content = binary ? '' : await res.text();
  return {
    content,
    totalBytes: Number(res.headers.get('X-Aw-Source-Total-Bytes') ?? 0),
    lineCount: Number(res.headers.get('X-Aw-Source-Line-Count') ?? 0),
    byteLimit: Number(res.headers.get('X-Aw-Source-Byte-Limit') ?? 0),
    lineLimit: Number(res.headers.get('X-Aw-Source-Line-Limit') ?? 0),
    truncated: res.headers.get('X-Aw-Source-Truncated') === 'true',
    binary,
    revision: res.headers.get('X-Aw-Source-Revision') ?? '',
    workspaceGeneration: Number(res.headers.get('X-Aw-Source-Workspace-Generation') ?? 0),
  };
}
