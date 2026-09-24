# V7 checklist — Local web UI Alpha

> V7 verification log follows the same discipline used for V0-V6 (see `baocaov6checklist.md` for the
> full history before it). Per the 2026-09-16 decision, all V7 entries are written in English from the
> start (prior Vietnamese checklists are left untouched). Full narrative per task: decision, reasoning,
> self-found questions and real verify output.

## V7-01 — UI framework decision (ADR-029)

### Context

V6 closed fully at V6-15P (PR #93, `b89565d`), and `docs/design/09-v7-alpha-ui.md` gates V7-01 as the
mandatory first task: choose the UI framework/toolchain by evidence, not by unstated preference, before
any V7-02 scaffold work starts. The user separately produced a Figma-Make-generated design draft outside
git (gitignored `UI draft/`) and, in a later session, a QA-reviewed revision of that same draft was
committed on a stray branch `codex/ui-design-qa` (commit `1f49e18`) — full React/Vite/TypeScript source
for the app shell plus 9 screens matching `docs/design/09a-v7-ui-component-spec.md`.

Two housekeeping steps happened ahead of this task, on that same branch (not on master):
1. `2de5f7f` — renamed `UI draft/` to `web/`, matching the layout already reserved for it in
   `docs/design/01-system-design.md:93` (`web/ — UI source after UI decision`).
2. `241ee98` — removed everything in `web/` that only served Figma Make's own hosted environment
   (`.figma/make/{analyze-routes,deploy,deploy-preview,dev,dev.json,format,install,langserver}`,
   `AGENTS.md`/`CLAUDE.md` describing their dev-server assumptions, unused Git LFS `.gitattributes`
   boilerplate, dead `src/imports/*.md` reference text) while keeping `.figma/make/site.json` because
   `vite.config.ts` still imports it for HTML meta tags/robots.txt. Verified with a clean
   `pnpm install` + `pnpm build` (1849 modules, 439ms, no errors) before and after the removal.

### Evidence

Real, measured build output from the cleaned `web/` prototype (`pnpm run build`):

```
dist/robots.txt                   0.02 kB │ gzip:  0.04 kB
dist/index.html                   0.90 kB │ gzip:  0.41 kB
dist/assets/index-CWpMfoom.css   25.47 kB │ gzip:  5.91 kB
dist/assets/index-C8r12K-K.js   358.41 kB │ gzip: 98.73 kB
✓ built in 439ms
```

Installed versions (`pnpm install` resolution): `react@19.2.4`, `react-dom@19.2.4`, `vite@8.0.5`,
`typescript@5.9.3`, `tailwindcss@4.2.2`, `@tailwindcss/vite@4.2.2`, `@vitejs/plugin-react@6.0.1`,
`lucide-react@1.41.0`, `oxfmt@0.2.0`.

The prototype already implements the app shell (`LeftNav`, `TopBar`) and all 9 screens named in
`docs/design/09a-v7-ui-component-spec.md`: `Kanban`, `TaskDetail`, `Definitions`, `Doctor`, `Projects`,
`AdapterBuilds`, `RunDiagnostics`, `Settings`, `CreateWorkItem`, `RepairAudit`.

### Decision

Recorded as ADR-029 in `docs/architecture/02-architecture-decisions.md` (section 31): **React 19 + Vite 8
+ TypeScript 5 + Tailwind CSS 4**, SPA only (no SSR, no Next.js/Remix), static-built bundle served by
`aw serve`'s static handler per ADR-016 / V6-01. No parallel Angular/Solid.js spike was built from
scratch — the cost of re-implementing all 9 screens just to get comparable numbers was judged
disproportionate to the benefit, given a working, measured prototype already exists and no team need for
Angular's heavier DI/RxJS surface was identified. The ADR records the qualitative comparison
(ecosystem maturity, bundle expectations, no-SSR requirement, maintainability) against Angular, Solid.js
and vanilla/htmx as the alternatives considered, plus the full decision matrix.

Consequences captured in the ADR: V7-02 scaffold adds a router (`wouter` or `react-router`),
`@tanstack/react-query` for SSE reconnect/query invalidation, `react-hook-form` + `zod` for form schema,
Vitest + Testing Library for unit/component tests, and Playwright for E2E — on top of the stack locked
here. `vite.config.ts` still carries two Figma-Make dev-only plugins (`figmaMakeKitPlugin`,
`figmaErrorOverlayReplay`/`figmaReactRefreshBoundaryFallback`) left untouched pending the real V7-02
scaffold rewrite, since removing them now was outside this task's verified-safe change set.

### Execution

- `docs/architecture/02-architecture-decisions.md`: added ADR-029 (section 31), renumbered the closing
  "Baseline sau review thiết kế" summary to section 32, added an ADR-029 bullet to its superseding
  matrix, and extended the file's header acceptance-date note.
- `baocaov7checklist.md` (this file): created.

### Verify

- `pnpm install` + `pnpm build` in `web/` (already run during the pre-task cleanup, both before and
  after removing Figma Make cruft): clean, no errors, output sizes as recorded above.
- No application code changed by this task — docs-only diff (ADR + checklist), so no Go test/build gate
  applies beyond the repository's standard CI.

## V7-02A — Static UI serving through `aw serve` (optional `--ui-dist`)

### Context

V7-02's own Verify line requires "production UI build phải được chính `aw serve` phục vụ qua static
handler của V6-01 — mọi E2E của V7 chạy qua binary-served UI, không qua dev server riêng." Two facts
surfaced during research made this its own task rather than a one-line wire-up: (1) no static file
handler exists anywhere in this codebase yet — `NewServer` only ever registers routes from
`RouteRegistry.Descriptors()`, and the one UI-adjacent route, `GET /` (`BootstrapHandler`, OperationID
`bootstrap`), renders a hardcoded minimal placeholder page with no reference to any built bundle; (2)
`web/dist` (the `pnpm build` output) is `.gitignore`d and not `go:embed`-able without either committing
build artifacts to git or making every existing Go CI job depend on a `pnpm build` step first.

Asked the user to pick between go:embed-into-binary (touches every existing `go build`/`go test ./...`
CI job) and serve-from-a-configurable-filesystem-directory (isolated — zero impact on any job that
doesn't pass the new flag). User chose the filesystem option explicitly.

### Decision

Added an optional `--ui-dist <dir>` flag to `aw serve` (default `""`, unlike the required
`--artifact-root`/`--workspace-root`): omitted, `aw serve` behaves byte-for-byte exactly as it always
has — same minimal bootstrap page, `/assets/` returns a typed 404. Given but pointing at an invalid
build directory, it fails closed at startup (mirrors `--artifact-root`'s own "given but wrong" check),
never silently serving the wrong thing.

`BootstrapHandler`'s signature gained a fourth parameter, `builtIndexHTML []byte`: nil (every existing
V6 call site) keeps the old minimal page; the real built `index.html` (read once by the composition
root) has the per-request bootstrap `<script nonce>` token injected right before `</body>`, preserving
the built app's own `<script type="module" src="/assets/...">`/`<link rel="stylesheet">` tags and
`<div id="root">` verbatim — never reconstructing the shell from scratch, so it works with Vite's
default content-hashed filenames unchanged. CSP widened from `default-src 'none'` (which would have
silently blocked the built app's own external stylesheet and any image/font) to also allow
`style-src 'self'`, `img-src 'self' data:`, `font-src 'self'` — `script-src 'self' 'nonce-N'` already
covered the JS module script since `'self'` allows any same-origin `src=` script, only the inline
bootstrap script itself needs the nonce.

New `StaticAssetHandler(assetsDir string) http.HandlerFunc` serves `<ui-dist>/assets/*` under
`GET /assets/`, relying on the stdlib `http.Dir`/`http.FileServer`'s own traversal guard (proven by a
dedicated `../` test) rather than a second hand-rolled check; `assetsDir == ""` (not configured) returns
the canonical `httpapi.WriteError` 404 envelope instead of ever resolving an arbitrary/relative path.
Sub-resource responses carry no CSP of their own (CSP only governs the top-level document that loads
them) but do set `Cache-Control: no-cache` — the built bundle is fixed for a process's whole lifetime,
but a browser tab open across an `aw serve` restart must not keep using a stale cached copy.

Per ADR-028's own already-anticipated language ("Ngoại lệ duy nhất là bootstrap/static asset của
browser"), the new `GET /assets/` route is registered with OperationID `staticAsset` and added to
`internal/delivery/parity`'s existing `BrowserBootstrap` exemption map right alongside `"bootstrap"` —
exempting it from BOTH the CLI-mirror requirement and the backing-application-operation requirement in
one place, exactly the mechanism that map already existed for.

### Execution

- `internal/delivery/httpapi/bootstrap.go`: added `builtIndexHTML []byte` parameter; nil path unchanged
  byte-for-byte; non-nil path injects the bootstrap script before the built page's last `</body>`, fails
  closed (500, no token leaked) if that tag is somehow missing; widened CSP with `style-src`/`img-src`/
  `font-src 'self'`.
- `internal/delivery/httpapi/staticassets.go` (new): `StaticAssetHandler`.
- `internal/delivery/httpcompose/compose.go`: `Dependencies.StaticAssetHandler` field; registers
  `GET /assets/` (OperationID `staticAsset`, `ScopeInstallation`, opaque schema) right after bootstrap.
- `cmd/aw/serve.go`: new optional `--ui-dist` flag; `loadBuiltUIIndex` helper (empty → zero values, no
  error; given-but-invalid → fail closed, checks directory exists, `index.html` exists and contains
  `</body>`, `assets/` subdirectory exists); wires both handlers into `ComposeRoutes`.
- `internal/delivery/parity/check.go`: `BrowserBootstrap` map gained `"staticAsset": true`.
- Mechanical fixes for the new route's ripple effects: `StaticAssetHandler` placeholder added to every
  existing `httpcompose.Dependencies{}` test literal (`composetest_test.go`, `fixture_test.go`,
  `realinputs_test.go`, `harness_test.go`); `nil` added to the one direct `BootstrapHandler(...)` call
  site (`security_test.go`); `wantRouteCount` 88→89 in both `apicontract/routeinventory_test.go` and
  `securitymatrix/transport_test.go`; `"staticAsset"` added to `TestUndocumentedOperationsSnapshot`'s
  pinned list (genuinely undocumented in the UX doc — an infra route, not a UX action/query row, so this
  is the correct outcome, not a gap); `testdata/golden/contract.json` regenerated (additive-only diff,
  reviewed).

### Verify

- `go build ./...` and `go vet ./...`: clean.
- `go test ./...` (whole repo, no `-race`): all packages pass.
- New tests: `internal/delivery/httpapi/staticassets_test.go` (not-configured 404 envelope, serves a
  real file, missing-file 404, cannot escape `assetsDir` via `../`); `internal/delivery/httpapi/
  bootstrap_builtui_test.go` (nil keeps the old page byte-for-byte, built HTML keeps the real app shell
  with the script injected before `</body>`, widened CSP, fails closed on a missing `</body>`);
  `cmd/aw/serve_uidist_test.go` (`loadBuiltUIIndex` unit tests for every branch; `TestServe_
  RejectsMisconfiguredUIDist`; `TestServe_ServesBuiltUIWhenUIDistConfigured` — a real running `aw serve`
  process, fixture `index.html` + `assets/app.js`, real HTTP round trip for both `/` and `/assets/app.js`).
- Manual end-to-end smoke against the REAL built UI (not just a fixture): built the real `web/` prototype
  (`codex/ui-design-qa` branch) with `pnpm build`, ran the `aw` binary with
  `--ui-dist <that dist>`, curled `/` — got the real Figma-Make-built `index.html` with its own
  `<script src="/assets/index-C8r12K-K.js">`/`<link href="/assets/index-CWpMfoom.css">` tags intact plus
  the injected `window.__AW_BOOTSTRAP__=...` token script before `</body>` — and curled both asset URLs,
  getting `200` with the exact built file sizes (358417 / 25470 bytes) and correct
  `text/javascript`/`text/css` content types.
