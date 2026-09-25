import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { type ProjectEventSummary, type StreamState, watchProjectEvents } from './sse';

function streamResponse(chunks: string[], init: { status?: number } = {}): Response {
  const encoder = new TextEncoder();
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });
  return new Response(body, { status: init.status ?? 200 });
}

function resyncRequiredResponse(): Response {
  return new Response(
    JSON.stringify({ error: { code: 'RESYNC_REQUIRED', message: 'cursor too old', details: [] } }),
    { status: 409 },
  );
}

function invalidatedRecord(journalPosition: number): string {
  return `id: ${journalPosition}\nevent: project.invalidated\ndata: {"journalPosition":${journalPosition},"eventType":"WORK_ITEM_UPDATED","schemaVersion":1}\n\n`;
}

describe('watchProjectEvents', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('delivers real events and advances the tracked cursor to each one\'s journalPosition', async () => {
    const events: ProjectEventSummary[] = [];
    const states: StreamState[] = [];
    const fetchImpl = vi.fn().mockResolvedValueOnce(streamResponse([invalidatedRecord(10), invalidatedRecord(11)]));

    watchProjectEvents('proj-1', 5, { onEvent: e => events.push(e), onStateChange: s => states.push(s), fetchImpl });
    await vi.advanceTimersByTimeAsync(0);

    expect(fetchImpl).toHaveBeenCalledWith('/projects/proj-1/events/watch?cursor=5', expect.anything());
    expect(events.map(e => e.journalPosition)).toEqual([10, 11]);
    // The mocked stream ends right after delivering both events (a real
    // server-side stream would stay open indefinitely), so a reconnect is
    // correctly scheduled immediately after — this test only cares that
    // 'open' was reached and both events landed before that happened.
    expect(states.slice(0, 2)).toEqual(['connecting', 'open']);
  });

  it('ignores a heartbeat-only record (no id/event/data) without calling onEvent', async () => {
    const events: ProjectEventSummary[] = [];
    const fetchImpl = vi.fn().mockResolvedValueOnce(streamResponse([': heartbeat\n\n', invalidatedRecord(1)]));

    watchProjectEvents('proj-1', 0, { onEvent: e => events.push(e), onStateChange: () => {}, fetchImpl });
    await vi.advanceTimersByTimeAsync(0);

    expect(events).toHaveLength(1);
    expect(events[0].journalPosition).toBe(1);
  });

  it('reconnects with the advanced cursor after the stream ends normally', async () => {
    const fetchImpl = vi.fn()
      .mockResolvedValueOnce(streamResponse([invalidatedRecord(7)]))
      .mockResolvedValueOnce(streamResponse([]));

    watchProjectEvents('proj-1', 0, { onEvent: () => {}, onStateChange: () => {}, fetchImpl, baseDelayMs: 1000 });
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchImpl).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(1000);
    expect(fetchImpl).toHaveBeenCalledWith('/projects/proj-1/events/watch?cursor=7', expect.anything());
    expect(fetchImpl).toHaveBeenCalledTimes(2);
  });

  it('backs off exponentially across consecutive failures, capped at maxDelayMs', async () => {
    const fetchImpl = vi.fn().mockRejectedValue(new Error('network down'));
    const states: StreamState[] = [];

    watchProjectEvents('proj-1', 0, { onEvent: () => {}, onStateChange: s => states.push(s), fetchImpl, baseDelayMs: 1000, maxDelayMs: 5000 });
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchImpl).toHaveBeenCalledTimes(1); // attempt 1 (immediate)

    await vi.advanceTimersByTimeAsync(1000); // attempt 2 after 1000ms (base * 2^0)
    expect(fetchImpl).toHaveBeenCalledTimes(2);

    await vi.advanceTimersByTimeAsync(2000); // attempt 3 after 2000ms (base * 2^1)
    expect(fetchImpl).toHaveBeenCalledTimes(3);

    await vi.advanceTimersByTimeAsync(4000); // attempt 4 after 4000ms (base * 2^2)
    expect(fetchImpl).toHaveBeenCalledTimes(4);

    await vi.advanceTimersByTimeAsync(5000); // attempt 5 after 5000ms (capped at maxDelayMs, not 8000)
    expect(fetchImpl).toHaveBeenCalledTimes(5);

    expect(states.filter(s => s === 'reconnecting').length).toBeGreaterThanOrEqual(4);
  });

  it('a successful connection resets the backoff counter for whatever failure comes next', async () => {
    const fetchImpl = vi.fn()
      .mockRejectedValueOnce(new Error('down'))          // attempt 1 fails
      .mockResolvedValueOnce(streamResponse([]))          // attempt 2 succeeds (empty stream, ends immediately)
      .mockRejectedValueOnce(new Error('down again'));    // attempt 3 fails

    watchProjectEvents('proj-1', 0, { onEvent: () => {}, onStateChange: () => {}, fetchImpl, baseDelayMs: 1000, maxDelayMs: 30000 });
    await vi.advanceTimersByTimeAsync(0); // attempt 1
    await vi.advanceTimersByTimeAsync(1000); // attempt 2 (backoff was base*2^0=1000 after 1 failure)
    expect(fetchImpl).toHaveBeenCalledTimes(2);

    // Attempt 2 succeeded then immediately ended -> reconnects at base delay again (counter reset), not doubled to 2000.
    await vi.advanceTimersByTimeAsync(999);
    expect(fetchImpl).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(fetchImpl).toHaveBeenCalledTimes(3);
  });

  it('a 409 RESYNC_REQUIRED response stops auto-reconnecting and reports the resync_required state', async () => {
    const fetchImpl = vi.fn().mockResolvedValueOnce(resyncRequiredResponse());
    const states: StreamState[] = [];

    watchProjectEvents('proj-1', 999999, { onEvent: () => {}, onStateChange: s => states.push(s), fetchImpl });
    await vi.advanceTimersByTimeAsync(0);

    expect(states).toContain('resync_required');
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetchImpl).toHaveBeenCalledTimes(1); // never auto-retried the same stale cursor
  });

  it('resync(freshCursor) reconnects immediately with the new cursor after a resync_required state', async () => {
    const fetchImpl = vi.fn()
      .mockResolvedValueOnce(resyncRequiredResponse())
      .mockResolvedValueOnce(streamResponse([]));

    const handle = watchProjectEvents('proj-1', 999999, { onEvent: () => {}, onStateChange: () => {}, fetchImpl });
    await vi.advanceTimersByTimeAsync(0);

    handle.resync(42);
    await vi.advanceTimersByTimeAsync(0);

    expect(fetchImpl).toHaveBeenLastCalledWith('/projects/proj-1/events/watch?cursor=42', expect.anything());
  });

  it('close() stops future reconnect attempts', async () => {
    const fetchImpl = vi.fn().mockRejectedValue(new Error('down'));
    const states: StreamState[] = [];

    const handle = watchProjectEvents('proj-1', 0, { onEvent: () => {}, onStateChange: s => states.push(s), fetchImpl, baseDelayMs: 1000 });
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchImpl).toHaveBeenCalledTimes(1);

    handle.close();
    expect(states.at(-1)).toBe('closed');

    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetchImpl).toHaveBeenCalledTimes(1); // no further attempts after close()
  });
});
