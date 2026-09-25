/**
 * V7-04's own "SSE reconnect/backoff theo JournalPosition, typed full-
 * resync khi cursor quá cũ" — a manual fetch-based SSE client for
 * `GET /projects/{id}/events/watch?cursor=<uint64>`
 * (internal/delivery/httpapi/eventstream's own wire contract).
 *
 * Native `EventSource` cannot implement this contract: reconnecting must
 * send a `cursor` QUERY PARAMETER that changes on every attempt (the last
 * successfully observed journalPosition), which `EventSource` has no way
 * to set — it only ever reconnects to the exact same URL, adding a
 * `Last-Event-ID` HEADER this server never reads. Worse, `EventSource`
 * gives calling code no access to a non-200 response's status or body at
 * all, only a generic error event — but this contract's "cursor is too old"
 * case is a real HTTP 409 with a typed `RESYNC_REQUIRED` JSON body
 * (eventstream/errors.go's own `writeRetentionResyncRequired`) that a
 * client MUST be able to distinguish from an ordinary transient drop (the
 * correct reaction is a full state refetch, never "keep retrying the same
 * stale cursor"). A plain `fetch` with a streamed body is the only way to
 * see that distinction before committing to stream mode.
 */

export interface ProjectEventSummary {
  journalPosition: number;
  eventType: string;
  schemaVersion: number;
}

export type StreamState = 'connecting' | 'open' | 'reconnecting' | 'resync_required' | 'closed';

export interface SSEEvent {
  id?: string;
  event?: string;
  data?: string;
}

/**
 * Parses as many complete SSE records (each terminated by a blank line) as
 * `buffer` holds, returning them plus whatever incomplete trailing text
 * should be prepended to the next chunk. A pure function — no fetch, no
 * timers — so the wire-format edge cases (comment-only heartbeat lines,
 * multi-line data, a record split across two chunks) are testable without
 * a real stream at all.
 */
export function parseSSEBuffer(buffer: string): { events: SSEEvent[]; remainder: string } {
  const records = buffer.split('\n\n');
  const remainder = records.pop() ?? '';
  const events: SSEEvent[] = [];
  for (const record of records) {
    const event: SSEEvent = {};
    const dataLines: string[] = [];
    for (const line of record.split('\n')) {
      if (line === '' || line.startsWith(':')) continue; // blank or comment (heartbeat) line
      const colonIndex = line.indexOf(':');
      const field = colonIndex === -1 ? line : line.slice(0, colonIndex);
      const value = colonIndex === -1 ? '' : line.slice(colonIndex + 1).replace(/^ /, '');
      if (field === 'id') event.id = value;
      else if (field === 'event') event.event = value;
      else if (field === 'data') dataLines.push(value);
    }
    if (dataLines.length > 0) event.data = dataLines.join('\n');
    events.push(event);
  }
  return { events, remainder };
}

const EVENT_INVALIDATED = 'project.invalidated';
const EVENT_DISCONNECTED = 'stream.disconnected';

export interface WatchProjectEventsOptions {
  onEvent: (event: ProjectEventSummary) => void;
  onStateChange: (state: StreamState) => void;
  /** Injected for tests; defaults to the real global fetch. */
  fetchImpl?: typeof fetch;
  /** Base reconnect delay in ms (default 1000). Doubles per consecutive failed attempt, capped at maxDelayMs. */
  baseDelayMs?: number;
  /** Reconnect delay ceiling in ms (default 30000). */
  maxDelayMs?: number;
}

export interface ProjectEventStreamHandle {
  /** Stops reconnecting and aborts any in-flight connection. */
  close(): void;
  /**
   * Resumes from a caller-supplied fresh cursor — the required reaction to
   * a 'resync_required' state: refetch full authoritative state (e.g. a
   * Kanban board GET), read its own Freshness.AsOfJournalPosition, and
   * call resync with that value, never the same stale cursor.
   */
  resync(freshCursor: number): void;
}

/**
 * Opens (and keeps re-opening, with exponential backoff) the project event
 * stream starting from initialCursor. Every real event advances the
 * client's own tracked cursor to that event's own journalPosition — never
 * further, and never backward — so a reconnect always resumes from exactly
 * the last event this client actually saw, matching
 * eventstream/stream.go's own "LastCursor is the exact JournalPosition of
 * the last REAL event this connection actually, successfully wrote"
 * contract on the server side.
 */
export function watchProjectEvents(
  projectId: string,
  initialCursor: number,
  opts: WatchProjectEventsOptions,
): ProjectEventStreamHandle {
  const fetchImpl = opts.fetchImpl ?? fetch;
  const baseDelayMs = opts.baseDelayMs ?? 1000;
  const maxDelayMs = opts.maxDelayMs ?? 30000;

  let cursor = initialCursor;
  let attempt = 0;
  let closed = false;
  let abortController: AbortController | null = null;

  function scheduleReconnect() {
    if (closed) return;
    attempt += 1;
    opts.onStateChange('reconnecting');
    const delay = Math.min(baseDelayMs * 2 ** (attempt - 1), maxDelayMs);
    setTimeout(() => {
      if (!closed) void connectOnce();
    }, delay);
  }

  async function connectOnce() {
    abortController = new AbortController();
    opts.onStateChange(attempt === 0 ? 'connecting' : 'reconnecting');

    let res: Response;
    try {
      res = await fetchImpl(`/projects/${projectId}/events/watch?cursor=${cursor}`, {
        signal: abortController.signal,
        headers: { Accept: 'text/event-stream' },
      });
    } catch {
      scheduleReconnect();
      return;
    }
    if (closed) return;

    if (res.status === 409) {
      const body = await res.json().catch(() => null);
      if (body?.error?.code === 'RESYNC_REQUIRED') {
        opts.onStateChange('resync_required');
        return; // waits for an explicit resync() call — never auto-retries a stale cursor
      }
    }
    if (!res.ok || !res.body) {
      scheduleReconnect();
      return;
    }

    attempt = 0; // a successful open resets backoff for whatever failure comes next
    opts.onStateChange('open');

    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        const parsed = parseSSEBuffer(buffer);
        buffer = parsed.remainder;
        for (const evt of parsed.events) {
          if (evt.event === EVENT_INVALIDATED && evt.data) {
            const summary = JSON.parse(evt.data) as ProjectEventSummary;
            cursor = summary.journalPosition;
            opts.onEvent(summary);
          } else if (evt.event === EVENT_DISCONNECTED && evt.data) {
            const payload = JSON.parse(evt.data) as { reason: string; lastCursor: number };
            cursor = payload.lastCursor;
          }
          // anything else (a bare heartbeat comment record) carries no id/event/data and is ignored.
        }
      }
    } catch {
      // read error mid-stream (network drop) — fall through to reconnect.
    }
    if (closed) return;
    scheduleReconnect();
  }

  void connectOnce();

  return {
    close() {
      closed = true;
      abortController?.abort();
      opts.onStateChange('closed');
    },
    resync(freshCursor: number) {
      if (closed) return;
      cursor = freshCursor;
      attempt = 0;
      void connectOnce();
    },
  };
}
