/**
 * Hand-declared wire shapes plus two hand-written real fetches for V6-07's
 * message routes, extended by V7-15's own new getMessageContent route (a
 * real, previously-missing route — see
 * internal/delivery/httpapi/message/content.go's own doc comment for why
 * no route anywhere in this codebase could otherwise ever read a Message's
 * own actual text).
 *
 * `listMessages`'s own `items` nests one level deeper than apicontract's
 * shallow generator expands, so the generated client returns `unknown[]`
 * for it — the same "narrow `unknown` at the call site" convention every
 * sibling API-gap file this session already established.
 *
 * `getMessageContent` and `appendConversationAttachment` are both entirely
 * excluded from the generated client (tsclient.go's own `rawContentOperations`/
 * `rawUploadOperations` sets): the first because its real response is raw
 * bytes, never JSON (mirroring `getWorkspaceSource`/`getArtifactContent`);
 * the second because its real REQUEST body is raw bytes, never JSON (the
 * generator would otherwise JSON-marshal `attachmentMetadata` as the POST
 * body, which the real handler would reject outright — a write-side twin of
 * the read-side gap V7-13 already found). `fetchMessageContent` and
 * `uploadAttachment` below are the two hand-written real fetches this task
 * adds in their place.
 */

import { ApiError } from './generated';

export type MessageRole = 'USER' | 'ASSISTANT' | 'SYSTEM' | 'TOOL';

export interface MessageRef {
  messageId: string;
  projectId: string;
  workItemId: string;
  attemptId?: string;
  sequence: number;
  actor: string;
  role: MessageRole | string;
  contentArtifactId: string;
  correlationId?: string;
  createdAt: string;
}

export function messageContentUrl(projectId: string, workItemId: string, messageId: string): string {
  return `/projects/${projectId}/work-items/${workItemId}/messages/${messageId}/content`;
}

export interface MessageContentResult {
  contentType: string;
  blob: Blob;
}

/**
 * fetchMessageContent calls GET .../content directly (never through the
 * generated client — see this file's own doc comment). A message's own
 * ContentType is caller-declared at append/attach time — a chat-typed USER/
 * ASSISTANT message is always text, but an uploaded attachment can be any
 * real media type — so this returns the raw Blob plus the real
 * Content-Type header, exactly like evidence.ts's own fetchArtifactContent,
 * and leaves the caller to decide how to render it (evidence.ts's own
 * isInlineSafeMediaType/PREVIEW_SIZE_LIMIT_BYTES are reused for that
 * decision — see ChatTab's own MessageBody component). Text is always
 * rendered as plain, React-escaped text (never innerHTML) even though the
 * server's own Content-Disposition header already guarantees a raw
 * text/html body would never be served inline in a real browser tab either
 * way — a raw HTML/script message body can never execute.
 */
export async function fetchMessageContent(projectId: string, workItemId: string, messageId: string): Promise<MessageContentResult> {
  const url = new URL(messageContentUrl(projectId, workItemId, messageId), window.location.origin);
  const res = await fetch(url);
  if (!res.ok) {
    const errBody = await res.json().catch(() => null);
    throw new ApiError(res.status, errBody?.error?.code ?? 'UNKNOWN', errBody?.error?.message ?? res.statusText, errBody?.error?.details);
  }
  const blob = await res.blob();
  return { contentType: res.headers.get('Content-Type') ?? 'application/octet-stream', blob };
}

/** Header names internal/delivery/httpapi/message/attachment.go's own wire contract defines — see that file's own doc comment. */
const ATTACHMENT_SHA256_HEADER = 'X-Attachment-Sha256';
const ATTACHMENT_ROLE_HEADER = 'X-Attachment-Role';
const ATTACHMENT_SENSITIVITY_HEADER = 'X-Attachment-Sensitivity';
const ATTACHMENT_ATTEMPT_ID_HEADER = 'X-Attachment-Attempt-Id';

export interface AppendMessageResult {
  messageId: string;
  projectId: string;
  workItemId: string;
  sequence: number;
  contentArtifactId: string;
}

export interface UploadAttachmentOptions {
  role: MessageRole;
  sensitivity?: 'PUBLIC' | 'SENSITIVE' | 'SECRET';
  attemptId?: string;
  /** The per-start session token — required, exactly like every other mutation (see api/generated.ts's own RequestOptions.token doc comment). */
  token: string;
  /** Reused across a retry of the SAME attempt so the server's own receipt-replay returns the SAME Message rather than creating a duplicate — mint a fresh one only for a genuinely new upload. */
  idempotencyKey: string;
}

/**
 * uploadAttachment calls POST .../attachments directly (never through the
 * generated client — see this file's own doc comment) with the file's own
 * real raw bytes as the body and its metadata carried via the real
 * X-Attachment-* headers this route's own wire contract defines, computing
 * the required declared SHA-256 digest via the real file bytes (never
 * trusting the browser-reported one) so the server's own digest-mismatch
 * check has something real to verify against.
 */
export async function uploadAttachment(
  projectId: string, workItemId: string, file: File, opts: UploadAttachmentOptions,
): Promise<AppendMessageResult> {
  const buffer = await file.arrayBuffer();
  const digest = await crypto.subtle.digest('SHA-256', buffer);
  const sha256 = Array.from(new Uint8Array(digest)).map(b => b.toString(16).padStart(2, '0')).join('');

  const headers: Record<string, string> = {
    'X-Aw-Session-Token': opts.token,
    'Idempotency-Key': opts.idempotencyKey,
    'Content-Type': file.type || 'application/octet-stream',
    [ATTACHMENT_SHA256_HEADER]: sha256,
    [ATTACHMENT_ROLE_HEADER]: opts.role,
  };
  if (opts.sensitivity) headers[ATTACHMENT_SENSITIVITY_HEADER] = opts.sensitivity;
  if (opts.attemptId) headers[ATTACHMENT_ATTEMPT_ID_HEADER] = opts.attemptId;

  const url = new URL(`/projects/${projectId}/work-items/${workItemId}/attachments`, window.location.origin);
  const res = await fetch(url, { method: 'POST', headers, body: buffer });
  if (!res.ok) {
    const errBody = await res.json().catch(() => null);
    throw new ApiError(res.status, errBody?.error?.code ?? 'UNKNOWN', errBody?.error?.message ?? res.statusText, errBody?.error?.details);
  }
  return res.json();
}
