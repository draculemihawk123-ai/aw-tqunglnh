/**
 * Hand-declared wire shapes plus one hand-written fetch for the V6-07B
 * evidence/artifact routes this task (V7-14) is the first real UI consumer
 * of.
 *
 * `listEvidence`'s own `items`, `listArtifacts`'s own `items`, and
 * `getEvidence`'s own `revisions` all nest one level deeper than
 * apicontract's shallow generator expands, so the generated client returns
 * `unknown[]`/`unknown[]` for them — the same "narrow `unknown` at the call
 * site" convention every sibling API-gap file this session already
 * established, mirroring `internal/app/runtime/queries.go`'s own
 * `EvidenceDetail`/`ArtifactSummary`/`RevisionView` field-for-field.
 *
 * `getArtifactContent` is already excluded from the generated client
 * entirely (`internal/delivery/httpapi/apicontract/tsclient.go`'s own
 * `rawContentOperations` set, added proactively by V7-13 for exactly this
 * route): its real response is a raw byte stream (Content-Type/
 * Content-Disposition/ETag — `internal/delivery/httpapi/evidence/artifact.go`'s
 * own `handleGetArtifactContent`), never a JSON envelope. `fetchArtifactContent`
 * below is the one hand-written real fetch this task adds in its place,
 * mirroring `workspaceinspection.ts`'s own `fetchWorkspaceSource`.
 */

import { ApiError } from './generated';
// gate.Verdict's own closed 5-value set (already hand-declared for ReleaseSet's
// identical field) — reused here rather than duplicated.
import type { Verdict } from './releaseset';

export interface RevisionView {
  repositoryId: string;
  vcsObjectId: string;
  workspaceGeneration: number;
}

export interface EvidenceDetail {
  evidenceId: string;
  projectId: string;
  workItemId: string;
  runId: string;
  nodeRunId: string;
  attemptId: string;
  kind: string;
  verdict: Verdict | string;
  artifactReferences?: string[];
  revisions?: RevisionView[];
  revisionSetHash: string;
  policyVersion: string;
  createdAt: string;
}

/** redact.Sensitivity's own closed wire vocabulary (internal/app/runtime/queries.go's own sensitivityWireName). */
export type Sensitivity = 'PUBLIC' | 'SENSITIVE' | 'SECRET';

/** artifact.RetentionClass's own closed set. */
export type RetentionClass = 'RAW_OUTPUT_TEMP' | 'CANONICAL_CONTEXT';

/** artifact.AttachState's own closed set — PURGED means the underlying content is gone; the row's own metadata is kept for audit. */
export type AttachState = 'ORPHAN' | 'ATTACHED' | 'PURGED';

export interface ArtifactSummary {
  artifactId: string;
  projectId: string;
  contentHash: string;
  size: number;
  mediaType: string;
  sensitivity: Sensitivity | string;
  redacted: boolean;
  retentionClass: RetentionClass | string;
  attachState: AttachState | string;
  hold: boolean;
  expiresAt?: string;
  createdAt: string;
  version: number;
}

/**
 * The same closed inline-safe allow-list `internal/delivery/httpapi/media.go`'s
 * own `inlineSafeContentTypes` enforces server-side (Content-Disposition
 * forces attachment for anything else, including text/html and
 * image/svg+xml) — kept here only so the UI can decide up front whether to
 * offer a real Preview action at all, never as a second security boundary:
 * the server's own Content-Disposition header is the actual enforcement.
 */
const INLINE_SAFE_MEDIA_TYPES = new Set([
  'text/plain', 'text/csv', 'application/json', 'image/png', 'image/jpeg', 'image/gif', 'application/pdf',
]);

export function isInlineSafeMediaType(mediaType: string): boolean {
  const base = mediaType.split(';')[0]?.trim().toLowerCase() ?? '';
  return INLINE_SAFE_MEDIA_TYPES.has(base);
}

/** Preview is refused above this size — matches the Source viewer's own SourceContentResult byte-limit convention (256 KiB) rather than inventing a second limit. */
export const PREVIEW_SIZE_LIMIT_BYTES = 262_144;

export function artifactContentUrl(projectId: string, workItemId: string, evidenceId: string, artifactId: string): string {
  return `/projects/${projectId}/work-items/${workItemId}/evidence/${evidenceId}/artifacts/${artifactId}/content`;
}

export interface ArtifactContentResult {
  contentType: string;
  blob: Blob;
}

/**
 * fetchArtifactContent calls GET .../content directly (never through the
 * generated client — see this file's own doc comment). A tampered artifact
 * (bytes no longer matching their own recorded hash) or a purged one both
 * surface here as a real, thrown ApiError — never served silently, this
 * task's own "tamper... surfaced as a typed error" Verify bullet.
 */
export async function fetchArtifactContent(
  projectId: string, workItemId: string, evidenceId: string, artifactId: string,
): Promise<ArtifactContentResult> {
  const url = new URL(artifactContentUrl(projectId, workItemId, evidenceId, artifactId), window.location.origin);
  const res = await fetch(url);
  if (!res.ok) {
    const errBody = await res.json().catch(() => null);
    throw new ApiError(res.status, errBody?.error?.code ?? 'UNKNOWN', errBody?.error?.message ?? res.statusText, errBody?.error?.details);
  }
  const blob = await res.blob();
  return { contentType: res.headers.get('Content-Type') ?? 'application/octet-stream', blob };
}
