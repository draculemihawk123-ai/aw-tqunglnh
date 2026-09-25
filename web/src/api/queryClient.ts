import { QueryClient } from '@tanstack/react-query';

/**
 * The one QueryClient every screen shares. `retry: false` is deliberate:
 * this app talks to its own loopback `aw serve` process, not a flaky
 * remote API — a failed GET almost always means a real server error worth
 * surfacing immediately, and the SSE client (web/src/api/sse.ts) already
 * owns the "retry with backoff" story for the one thing that genuinely
 * needs it (a live stream connection). A query silently retrying
 * underneath would just double that story with a second, uncoordinated
 * one. `refetchOnWindowFocus: false` for the same reason: nothing here
 * depends on browser-tab-focus heuristics to decide when data might be
 * stale — real invalidation (a future task, once a screen has a real SSE
 * subscription to react to) is what should trigger a refetch.
 */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: false,
      refetchOnWindowFocus: false,
    },
  },
});
