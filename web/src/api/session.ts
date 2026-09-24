import type { RequestOptions } from './generated';

/**
 * The exact shape httpapi.BootstrapHandler embeds into the page
 * (internal/delivery/httpapi/bootstrap.go's own bootstrapPayload) —
 * the per-start session token plus the trusted principal the UI needs to
 * display ("signed in as ...") but must never be able to influence.
 */
export interface BootstrapPayload {
  token: string;
  actor: string;
  roles: string[];
}

declare global {
  interface Window {
    __AW_BOOTSTRAP__?: BootstrapPayload;
  }
}

let cached: BootstrapPayload | null = null;
let hasReadWindow = false;

/**
 * Reads window.__AW_BOOTSTRAP__ exactly once, into module-scoped memory,
 * then deletes it off window — ADR-016 ("token không được ghi vào...
 * browser storage"; keeping a second live reference on window past this
 * module's own read would be an unnecessary extra place the token lives
 * in). Safe to call repeatedly; only the first call does anything.
 */
function readBootstrapOnce(): void {
  if (hasReadWindow) return;
  hasReadWindow = true;
  const payload = window.__AW_BOOTSTRAP__;
  if (!payload) return;
  cached = payload;
  delete window.__AW_BOOTSTRAP__;
}

/**
 * The per-start session token every mutation must send as the
 * X-Aw-Session-Token header (see the generated client's own `request`
 * helper). Throws if this page was not served by `aw serve`'s bootstrap
 * route (docs/design/09-v7-alpha-ui.md V7-02's own completion bar: no V7
 * journey depends on the raw Vite dev server, so a missing bootstrap
 * payload here is a real configuration error, not a condition normal
 * code should handle gracefully).
 */
export function getSessionToken(): string {
  readBootstrapOnce();
  if (!cached) {
    throw new Error(
      'no session token: window.__AW_BOOTSTRAP__ was not present when this module first ran — this page must be served by `aw serve`, not the Vite dev server',
    );
  }
  return cached.token;
}

/** The trusted LocalPrincipalSnapshot this server bound at startup (display-only — never sent back to the server, never used for an authorization decision client-side). */
export function getPrincipal(): { actor: string; roles: string[] } {
  readBootstrapOnce();
  if (!cached) {
    throw new Error(
      'no principal: window.__AW_BOOTSTRAP__ was not present when this module first ran — this page must be served by `aw serve`, not the Vite dev server',
    );
  }
  return { actor: cached.actor, roles: cached.roles };
}

/**
 * Attaches the session token to a set of generated-client RequestOptions —
 * every mutation call site should build its options through this rather
 * than reading getSessionToken() by hand, so there is exactly one place
 * that wires the two together.
 */
export function withSessionToken(opts: Omit<RequestOptions, 'token'> = {}): RequestOptions {
  return { ...opts, token: getSessionToken() };
}
