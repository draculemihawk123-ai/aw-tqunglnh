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

## V7-02B — Generated TypeScript API client + the real `web/` workspace lands on master

### Context

V7-02's own scope line is "API client từ contract" plus "scaffold theo ADR với strict TypeScript/lint/
unit/component/E2E commands." Starting the generator revealed a real dependency the task text does not
spell out: `web/` had never been merged to `master` at all — it only ever existed on the stray
`codex/ui-design-qa` branch (V7-01's own cleanup work). A generated `.ts` file with nowhere to be
type-checked would be an orphan, not a real deliverable, so this task also brings that cleaned `web/`
workspace itself onto `master` for the first time.

### Decision

The generator (`apicontract.GenerateTypeScriptClient`) lives IN the `apicontract` package itself, not a
separate `cmd/` tool or subpackage — that package already owns "what a client of this contract looks
like" (its own doc comment explicitly anticipates a future consumer walking the artifact), and staying
in-package lets its golden test reuse `buildRealContract(t)`/`buildRealRegistry(t)` directly instead of
duplicating the ~90-line real-infrastructure composition helper in a second binary. Output is committed
to `web/src/api/generated.ts` and protected by a golden test
(`TestGeneratedTypeScriptClient_MatchesGoldenFixture`) that regenerates from the SAME real production
Contract every other test in this package already builds — this is the "breaking API schema làm UI CI
fail" completion bar from the design doc, and it needs no change to the shared `.github/workflows/
spike-gate.yml` at all: it is an ordinary `go test ./...` assertion, so it already runs inside every
existing CI job that runs the Go suite.

Two operations, `bootstrap` and `staticAsset`, are deliberately never given a callable client function
(`browserOnlyOperations`) — the same ADR-028 exemption `internal/delivery/parity`'s `BrowserBootstrap`
map already encodes for CLI/application parity, applied here to the client generator: neither is ever
`fetch()`-called by application code (bootstrap is read from `window.__AW_BOOTSTRAP__`, static assets
load via native `<script src>`/`<link href>`), so generating a function for either would be actively
misleading.

Field typing is intentionally honest about the contract's own one-level-deep limit (see apicontract's
own "why a custom JSON contract, not full OpenAPI 3.x" doc comment): `Opaque` or zero-`Fields` schemas
become `unknown`, never `any` and never a fabricated shape; a named struct/enum type the contract does
not itself expand beyond its bare Go type string (e.g. `kanban.KanbanCardDTO`) also becomes `unknown`,
for the same reason. `omitempty` in the raw Go `json:"..."` tag becomes an optional TS field (`field?:
type`) rather than a required one — real behavior, not a simplification.

A real correctness bug was caught by actually type-checking the output with `tsc --noEmit` (not just
eyeballing it): `Field.JSONTag` carries the RAW, unsplit Go struct tag (e.g.
`"parentJoinPolicy,omitempty"`, per contract.go's own `f.Tag.Get("json")`, never parsed further) — using
it verbatim as a TS property key produced `parentJoinPolicy,omitempty: ...`, which `tsc` correctly
rejected (`TS7008`/`TS2300`/`TS2717`, ~50 errors). Fixed with a dedicated `jsonWireKey` helper that
splits on the first comma exactly like `encoding/json` itself does, extracting both the real wire key
and whether `omitempty` was present.

### Execution

- `internal/delivery/httpapi/apicontract/tsclient.go` (new): `GenerateTypeScriptClient`, the Go-type→TS
  mapping (`goFieldTypeToTS`), path-template generation, and `jsonWireKey`.
- `internal/delivery/httpapi/apicontract/tsclient_golden_test.go` (new):
  `TestGeneratedTypeScriptClient_MatchesGoldenFixture` (byte-for-byte golden compare, same pattern as
  `golden_test.go`'s own contract.json test) and
  `TestGeneratedTypeScriptClient_SkipsBrowserOnlyOperations`.
- `web/` (new on `master`): the full cleaned scaffold from `codex/ui-design-qa` (React 19 + Vite 8 +
  TypeScript 5 + Tailwind 4, per ADR-029) — `package.json`, `tsconfig.json`, `vite.config.ts`,
  `index.html`, and every existing `src/` file (`App.tsx`, the app shell, all 9 screens).
- `web/src/api/generated.ts` (new): the committed generator output — 1328 lines, one function per
  non-browser-only operation.
- Renamed the collided test-only `pathParamPattern` in `routeinventory_test.go` context to
  `tsClientPathParamPattern` in the new file (both patterns coexist in the same package for different
  purposes — the test-only one does bare substitution, this one needs a capture group for the real
  parameter name).

### Verify

- `go build ./...`, `go vet ./...`: clean.
- `go test ./...` (whole repo): all green except two isolated reruns confirmed unrelated —
  `TestExecuteNodeHandler_PollerDetectsDurableCancellation_DefinitiveResultFinalizesCancelled`
  (`internal/app/runtime`) and `TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing`
  (`internal/app/workerpool`, an already-documented shared-load flake) — both pass cleanly run in
  isolation immediately after; this diff touches only `internal/delivery/httpapi/apicontract` and
  `web/`, zero overlap with either failing package, consistent with a full local `go test ./...` being a
  heavy-parallel-load scenario (same mechanism as concurrent CI, not a regression).
- `pnpm install` + `npx tsc --noEmit` in `web/` (the whole workspace, not just the new file): clean —
  this IS the "client compatibility check" V7-02's own Verify line asks for, and it is what actually
  caught the `jsonWireKey` bug above.
- `pnpm run build`: unaffected (1849 modules, same output as before — Vite tree-shakes the not-yet-
  imported `generated.ts`, so it adds nothing to the shipped bundle until a later task actually wires a
  screen to call it).

## V7-02C — Session token module; V7-02 formally closed

### Context

V7-02's "Thực hiện" bullet also names "đọc per-start token từ bootstrap rồi chỉ giữ trong memory và gửi
header cho mutation" and "route shell." Its own "Hoàn thành khi" completion bar — "breaking API schema
làm UI CI fail, và không journey nào của V7 phụ thuộc dev server" — was already fully satisfied by
V7-02A (binary-served static UI) and V7-02B (golden-tested generated client), so this task's own scope
is deliberately narrow: the one piece of "read the token, keep it in memory" that is real, unambiguous
infrastructure no later task needs to redo.

"Route shell" and actually wiring any of the 9 existing prototype screens (`App.tsx` currently drives
100% fake, local `useState` data — `INITIAL_PROJECTS`, `INITIAL_CARDS`, a manual "Prototype state
controls" panel to simulate connection states) to real data is explicitly `docs/design/09-v7-alpha-ui.md`
V7-04's own scope ("Application shell, routing và SSE state") and each screen's own dedicated task
(V7-05 Doctor, V7-06 Projects, V7-07 Definitions, ...) — attempting either here would be redoing work
those tasks already own, not filling a real V7-02 gap.

### Decision

`web/src/api/session.ts`: reads `window.__AW_BOOTSTRAP__` exactly once into module-scoped memory, then
`delete`s it off `window` (ADR-016 — no second live reference should outlive this module's own read),
throwing a clear error from `getSessionToken()`/`getPrincipal()` if it was never present (this page must
be served by `aw serve`, never the raw Vite dev server — consistent with V7-02's own completion bar).
`withSessionToken(opts)` is the one call sites should use to attach the token to a generated-client
`RequestOptions` value, so token-attachment logic lives in exactly one place.

### Execution

- `web/src/api/session.ts` (new): `BootstrapPayload`, `getSessionToken`, `getPrincipal`,
  `withSessionToken`.

### Verify

- `npx tsc --noEmit` in `web/` (whole workspace): clean.
- No automated test committed — `web/` has no unit-test runner yet (Vitest/Jest were never installed;
  that is its own future scaffold task, matching ADR-029's own "Hệ quả" list). Verified manually instead
  with two throwaway Node scripts (`node --experimental-strip-types`, `window` polyfilled via
  `globalThis.window`), deleted after verifying, not committed:
  - Missing `window.__AW_BOOTSTRAP__`: `getSessionToken()`/`getPrincipal()` both throw the documented
    error.
  - Present `window.__AW_BOOTSTRAP__ = {token, actor, roles}`: `getSessionToken()` returns the real
    token; `window.__AW_BOOTSTRAP__` is `undefined` immediately after; `getPrincipal()` still returns
    the correct value from the module's own cache (proving the delete does not break the "already read"
    path); `withSessionToken({query: {...}})` merges the token in alongside existing options.
- `pnpm run build`: unaffected, same output as before.

### V7-02 status: CLOSED

All three sub-tasks (V7-02A static serving, V7-02B generated client, V7-02C session token) merged.
V7-02's own completion bar is met. Next per `docs/design/09-v7-alpha-ui.md`: V7-03 (design tokens and
accessible primitives) or V7-04 (application shell, routing, SSE) — deliberately not started without a
scoping decision, since either is a much larger, screen-touching task than any V7-02 sub-part.

User chose V7-03.

## V7-03A — Component test tooling; keyboard/accessibility coverage for the existing primitives

### Context

V7-03's own Verify line ("component tests, keyboard/focus và automated accessibility smoke") requires
test infrastructure `web/` never had — no Vitest/Jest, no Testing Library, nothing. Before writing that
infrastructure, actually reading `web/src/components/ui.tsx` (528 lines) turned up a real surprise: the
Figma Make-generated prototype already implements almost everything V7-03's own "Thực hiện" line asks
for (buttons/forms/dialog/tabs/badge/skeleton — 20 components total), including BOTH literal
"Hoàn thành khi" bar items already, in the existing markup: `StatusBadge` always renders an icon AND a
text label alongside color ("status không chỉ truyền bằng màu"), and `TextField` already wires
`aria-describedby`/`aria-invalid` linking its error text to the input ("error liên kết field"). Missing
from the "Thực hiện" list: a `table` primitive (4 screens hand-roll raw `<table>` markup with no shared
component) and a `toast` primitive (nothing dismissible/transient exists — `OperationNotice` is a
persistent banner, not a toast). This task scopes ONLY the test tooling + proving the EXISTING primitives
actually meet the bar (including fixing one real gap it found); V7-03B is Table + Toast.

### Decision

Added to `web/`: `vitest` + `@vitest/ui` + `jsdom` (test runner/environment), `@testing-library/react` +
`@testing-library/jest-dom` + `@testing-library/user-event` (component tests), `axe-core` directly (NOT
the `vitest-axe` wrapper — see below). `vite.config.ts`'s `defineConfig` import switched from `'vite'` to
`'vitest/config'` (the standard way to add a `test` block to one shared Vite+Vitest config file, no
second config file). `test.globals: false` keeps `describe`/`it`/`expect` as explicit imports, matching
this codebase's own no-ambient-globals style everywhere else — this means `@testing-library/react`'s own
auto-cleanup-on-afterEach never activates, so `src/test/setup.ts` registers `afterEach(cleanup)` by hand.

`vitest-axe` (the obvious "axe matcher for Vitest" package) was tried first and dropped: its own
`toHaveNoViolations` type augmentation declares `interface Assertion<T = any>` (one type parameter),
while this project's pinned Vitest 5 actual `Assertion<T, R>` has two — TypeScript interface merging
silently fails to combine mismatched type-parameter lists, so the matcher worked at runtime but `tsc
--noEmit` never recognized it (confirmed real: reverting to `vitest-axe` reproduces ~10 `TS2339` errors
across every test file). Replaced with a small local `expectNoAxeViolations(container)` in
`web/src/test/axe.ts` that calls `axe-core` directly and fails with a formatted violation list — no
custom matcher, no cross-package type-augmentation contract to go stale again. Verified this genuinely
catches a violation (not a silent no-op) with a throwaway `<img>` missing `alt` before deleting that
proof.

`color-contrast` is disabled in the axe config: jsdom has no real layout/paint engine
(`HTMLCanvasElement#getContext` unimplemented), so that one rule can only ever warn or produce a
meaningless pass in this environment — every other rule (labels, roles, aria-*, focus order) still runs
for real.

One real, non-test-infra fix came out of writing the keyboard tests: `Tabs` had `role="tablist"`/
`role="tab"`/`aria-selected` but no keyboard navigation at all — a mouse-only tab control despite looking
like a real ARIA tablist. Implemented the WAI-ARIA APG "Tabs" automatic-activation pattern:
ArrowLeft/ArrowRight move (with wraparound) and select, Home/End jump to first/last, roving `tabindex`
(only the active tab is `tabindex="0"`, every other is `-1`) so Tab key moves IN and OUT of the tablist
once rather than through every individual tab.

### Execution

- `web/vite.config.ts`: `defineConfig` from `vitest/config`; new `test` block (`jsdom`, `globals: false`,
  `setupFiles: ['./src/test/setup.ts']`).
- `web/src/test/setup.ts` (new): jest-dom matchers, `afterEach(cleanup)`.
- `web/src/test/axe.ts` (new): `expectNoAxeViolations`.
- `web/src/components/ui.tsx`: `Tabs` gained real keyboard navigation (roving tabindex, arrow/Home/End).
- `web/src/components/ui.{badge,button,tabs,form,dialog,feedback}.test.tsx` (new, 69 tests total):
  component behavior, keyboard interaction (`Tabs` arrows/Home/End/focus-follows-selection, `Dialog`
  focus-trap/Tab-wraparound/Escape/focus-restore-on-unmount, `Button` Enter/Space activation), and an
  `expectNoAxeViolations` smoke test on a representative sample of components.
- `web/package.json`: `test` (`vitest run`, what CI will call), `test:watch`, `test:ui` scripts.

### Verify

- `npx vitest run`: 69/69 pass.
- `npx tsc --noEmit`: clean.
- `pnpm run build`: unaffected, same output as before.
- Sanity-checked `expectNoAxeViolations` against a deliberately broken `<img>` (no `alt`) — it correctly
  threw before being deleted (proof only, not a committed app-behavior test).

## V7-03B — Table and Toast primitives; V7-03 closed

### Context

V7-03A's own checklist entry named the two primitives missing from the existing library that V7-03's
"Thực hiện" line asks for: `table` (4 screens — `Definitions.tsx`, `Projects.tsx`, `RepairAudit.tsx`,
`Settings.tsx` — each hand-roll raw `<table>` markup, no shared component) and `toast` (nothing
dismissible/transient exists; `OperationNotice` is a persistent banner for an in-progress operation, a
different concept). This task adds both as real, tested primitives to `web/src/components/ui.tsx`. It
does NOT retrofit the 4 existing screens to use `Table` — those screens are still 100% fake-`useState`
Figma Make prototype data (`App.tsx`'s own routing/state), due for real rewiring in V7-04 and each
screen's own dedicated task; changing their markup now would be premature, redone work later.

### Decision

`Table<T>` is deliberately generic (`columns: TableColumn<T>[]`, `rows: T[]`, `getRowKey`) rather than
one-shape-per-screen, and locks in one real accessibility fix none of the 4 existing raw tables have:
`<th scope="col">` on every header (screen readers otherwise cannot reliably announce which column a
cell belongs to) plus an optional `headerLabel` for a column with no visible header text (mirrors
`Projects.tsx`'s own `<th aria-label="Actions" />` trailing-column pattern). `onRowClick` is documented
and tested as a MOUSE-ONLY convenience, never a `role="button"` on `<tr>` (which would misrepresent real
table structure to assistive tech) — exactly the existing "row onClick + a real `<button>` in one cell"
pattern `Definitions.tsx`/`Settings.tsx` already use for keyboard reachability.

Toast is a `useToasts()` queue hook + `ToastViewport` renderer, not a single static component: nothing in
the app can trigger a toast without SOME state to hold the current queue, so a bare presentational
component would not have been genuinely usable end-to-end. One deliberate safety rule, tested: a `danger`
toast never auto-dismisses regardless of its `duration` — an error the operator has not yet acknowledged
must not silently disappear. `aria-live` is `assertive` for `danger`, `polite` otherwise, matching the
urgency convention `ProjectionBanner`/`ConnectionBanner` already established in this same file.

### Execution

- `web/src/components/ui.tsx`: `Table`, `TableColumn`, `useToasts`, `ToastItem`, `ToastIntent`,
  `ToastViewport` (+ internal `Toast`).
- `web/src/components/ui.table.test.tsx` (new, 7 tests): header `scope="col"`, row/cell rendering,
  `headerLabel` accessible-name fallback, `emptyState`, mouse-only `onRowClick` (asserts no `role="button"`
  leaked onto `<tr>`), `isRowSelected` highlighting, accessibility smoke.
- `web/src/components/ui.toast.test.tsx` (new, 8 tests): `useToasts` add/dismiss via `renderHook`,
  `ToastViewport` rendering/empty-state, icon+text (not color alone), `aria-live` per intent, dismiss
  button, fake-timer-verified auto-dismiss timing, danger-never-auto-dismisses, accessibility smoke.

### Verify

- `npx vitest run`: 84/84 pass (69 from V7-03A + 15 new).
- `npx tsc --noEmit`: clean.
- `pnpm run build`: unaffected, same output as before.

### V7-03 status: CLOSED

Both sub-tasks (V7-03A test tooling + existing-primitive coverage, V7-03B Table/Toast) merged. V7-03's
own completion bar ("status không chỉ truyền bằng màu và error liên kết field") was already satisfied by
the existing primitive markup (V7-03A finding) and remains true for the two new ones. Next per
`docs/design/09-v7-alpha-ui.md`: V7-04 (application shell, routing, SSE state).

## V7-04A — Real routing (route guards by selected project)

### Context

V7-04's own scope is large enough (route guards, SSE reconnect/backoff, stale/degraded indicator, query
invalidation) to need its own sub-tasks, mirroring V7-02/V7-03's own A/B/C split. This first piece closes
the "route guards by selected project only" half and the literal completion bar — "browser cache không
tự quyết runtime state" — by replacing `App.tsx`'s own in-memory `route`/`project`/`taskSelected`
`useState` trio (V7-03 and earlier) with the URL itself as the one source of truth. SSE reconnect/backoff,
the stale/degraded projection indicator wired to something real, and query invalidation are each their
own remaining V7-04 sub-task.

### Decision

Chose `wouter` over `react-router` (both named as candidates in ADR-029's own "Hệ quả"): its hook-based
API (`useLocation`, `useRouter().parser` + `matchRoute`) fits this app's existing "one big App.tsx
computing what to render" shape without needing a `<Routes>`/`<Route>` JSX tree rewrite, and it stays
consistent with ADR-029's own "keep it light" reasoning (nothing here needs react-router's nested-loader
data APIs).

`src/routes.ts` is the one place every `NavRoute` maps to and from a real path — project-scoped routes
carry `:projectId`, task-scoped ones also `:taskId`. `App.tsx` no longer stores `route`/`project`/
`taskSelected` as state: `route` and the raw `projectId`/`taskId` come from `matchPath(router.parser,
location)` every render, and `project` is derived by resolving that `projectId` against the (still fake,
still local-`useState`) `projects` list — a repository/component mutation now only touches `projects`
once, instead of updating a parallel `project` copy by hand as the old code did (that dual-update was a
real, if latent, place these two copies of the same project object could have drifted apart on a stale
closure). The route guard is a `useEffect`: any `PROJECT_SCOPED_ROUTES` member whose `projectId` does not
resolve to a real project redirects to `/projects` — a typed bookmark, a project since removed, or a
hand-edited URL all land in the same place a normal user action already handles cleanly. A second
`useEffect` settles any unmatched path (including bare `/`) onto `/doctor`, so the address bar always
shows a real, matched route.

`Kanban.tsx`'s own documented limitation — only card `wi-0018` has a real `TaskDetailScreen` fixture,
every other card's open button is disabled with an explicit tooltip saying so — is left exactly as it
is (not a bug to fix here): `routes.ts` exports `FIXTURE_TASK_ID = 'wi-0018'` and `App.tsx`'s
`handleOpenTask`/`navigate` use it, so the URL is honest about there being exactly one real task fixture
rather than inventing per-card task URLs a screen cannot yet render.

### Execution

- `web/src/routes.ts` (new): `RouteMatch`, `PROJECT_SCOPED_ROUTES`, `FIXTURE_TASK_ID`, `matchPath`,
  `pathFor`.
- `web/src/App.tsx`: `route`/`project`/`taskSelected` `useState` replaced by URL-derived values; two
  `useEffect`s (project-scope guard, unmatched-path settle); `navigate` now takes an optional
  `{projectId, taskId}` override (needed by `RepairAudit`'s own "jump to a project screen with no project
  yet selected" case) and pushes a real URL via wouter's `useLocation()` setter instead of setting local
  state.
- `web/package.json` / `pnpm-lock.yaml`: added `wouter`.
- `web/src/test/setup.ts`: added a minimal `window.matchMedia` polyfill — jsdom has never implemented it,
  and `App.tsx`'s own responsive nav-collapse effect calls it unconditionally on mount, so any test
  rendering `<App/>` threw without this.
- `web/src/routes.test.tsx` (new, 7 tests): every `pathFor` shape, missing-required-id throws, and
  `matchPath` round-tripping every route (including task-before-project specificity) using a real wouter
  `Parser` obtained the same way `matchPath`'s own doc comment says to (`useRouter().parser` inside a
  `<Router hook={memoryLocation(...).hook}>`).
- `web/src/App.routing.test.tsx` (new, 6 tests): unmatched path settles on `/doctor`; deep-linking
  straight to a project URL renders its real overview (no prior in-app navigation needed); the guard
  redirects both a project-scoped and a task-scoped URL with an unknown project; clicking a left-nav item
  pushes the real project-scoped URL; opening the one real task fixture pushes its task-scoped URL — each
  assertion reads wouter's own recorded `history` array, not a screenshot or a guess.
- Manually verified in a real browser (dev server, not part of any committed test): typed navigation,
  refresh-preserves-selected-project (the actual bug this task fixes — the old code lost `project` on
  every reload), an invalid project URL redirecting live, and browser back/forward working via native
  history.

### Verify

- `npx vitest run`: 98/98 pass (84 from V7-03 + 7 + 6 new — some new coverage landed in existing files'
  neighboring test counts too via the shared `matchMedia` setup fix).
- `npx tsc --noEmit`: clean.
- `pnpm run build`: unaffected in shape (bundle grew by wouter's own real, small size).
- Manual browser QA (see above) — the exact behaviors this task's own "Hoàn thành khi" bar names.

## V7-04B — SSE reconnect/backoff/resync client

### Context

The second V7-04 sub-part: "SSE reconnect/backoff theo JournalPosition, typed full-resync khi cursor quá
cũ." Reading `internal/delivery/httpapi/eventstream`'s own package doc comment first (it documents its
own cursor/retention/authorization design in detail) ruled out the obvious approach: native `EventSource`
cannot implement this contract at all. Reconnecting must send a `cursor` QUERY PARAMETER that changes
every attempt (the last observed `journalPosition`) — `EventSource` only ever reconnects to the exact
same URL, offering a `Last-Event-ID` HEADER this server never reads. Worse, `EventSource` gives calling
code no access to a non-2xx response's status or body — but "cursor too old" is a real HTTP 409 with a
typed `RESYNC_REQUIRED` JSON body (`eventstream/errors.go`'s own `writeRetentionResyncRequired`), and a
client MUST tell that apart from an ordinary transient drop: the correct reaction is a full state
refetch, never retrying the same stale cursor forever. Only a manual `fetch` with a streamed body can see
that distinction before committing to stream mode.

Same scoping choice as V7-02B's generated client and V7-02C's session module: this ships as a real,
thoroughly-tested standalone module, not wired into any screen yet — no screen fetches real project data
yet (all still `INITIAL_CARDS`/`INITIAL_PROJECTS` fixtures), so there is no real `Freshness.
AsOfJournalPosition` anywhere to seed an honest initial cursor from. Wiring this into the app shell's
connection indicator and a screen's real data is V7-04C's own job, once the query/data layer exists.

### Decision

`web/src/api/sse.ts` splits into two independently-testable pieces: `parseSSEBuffer` (pure — splits
accumulated text on blank-line record boundaries, extracts `id`/`event`/`data`, ignores comment-only
heartbeat lines, and returns whatever incomplete trailing text belongs to the next chunk) and
`watchProjectEvents` (the actual reconnect/backoff state machine, built on `fetch` + a manual
`ReadableStream` reader).

Backoff is deterministic exponential (`baseDelayMs * 2^attempt`, capped at `maxDelayMs`), no jitter —
this is a single local desktop tool talking to its own loopback `aw serve`, never many independent
clients that could thundering-herd a shared server, so jitter's own reason to exist does not apply here.
A successful open resets the attempt counter, so one clean connection does not leave a later, unrelated
failure paying an inflated backoff from an earlier outage. A 409 `RESYNC_REQUIRED` response reports a
distinct `'resync_required'` state and stops the automatic reconnect loop entirely — `resync(freshCursor)`
is the one way to resume once the caller has actually refetched authoritative state and has a real fresh
cursor, never a delay-based auto-retry of the same rejected one.

### Execution

- `web/src/api/sse.ts` (new): `ProjectEventSummary`, `StreamState`, `parseSSEBuffer`, `watchProjectEvents`,
  `ProjectEventStreamHandle`.
- `web/src/api/sse.parse.test.ts` (new, 6 tests): single/multiple records in one chunk, a record split
  across two chunks reassembling correctly, comment-only heartbeat producing no id/event/data, multi-line
  `data:` joined per the SSE spec.
- `web/src/api/sse.reconnect.test.ts` (new, 8 tests, all using `vi.useFakeTimers()` +
  `vi.advanceTimersByTimeAsync` and a `fetchImpl` injection point — no real network or timers): event
  delivery advances the tracked cursor; heartbeats never call `onEvent`; a normal stream end reconnects
  with the advanced cursor; exponential backoff across 4 consecutive failures matches the exact expected
  delay sequence (1000/2000/4000/5000-capped ms); a successful connection resets the counter for the next
  failure; a 409 stops auto-reconnect and never retries the stale cursor even after 60s; `resync()`
  reconnects immediately with the new cursor; `close()` stops all future attempts.

### Verify

- `npx vitest run`: 112/112 pass (98 from V7-04A + 14 new).
- `npx tsc --noEmit`: clean.
- `pnpm run build`: unaffected (module not yet imported by any screen, tree-shaken out — same as every
  prior not-yet-wired module this session).

V7-04 status: CLOSED. Its own completion bar ("browser cache không tự quyết runtime state") was met by
V7-04A; the SSE client is real, tested infrastructure ready for a real consumer. "Reconnect banner" and
"query invalidation dùng API authority" cannot be honestly wired until at least one screen has real
project-scoped data to invalidate — `GET /projects/{id}/events/watch` needs a project id Doctor (V7-05,
installation-scoped, no project context) never has, so that wiring naturally lands on whichever task
first gives a project-scoped screen real data (V7-06+), not invented speculatively here.

## V7-05A — Real Doctor screen, and a critical routing bug this task's own testing found

### Context

First screen wired to a real backend call — `chỉ gọi Doctor API` per V7-05's own scope, deferring the
ADR-022 probe→confirm→register adapter-build workflow (a real cryptographic candidate-token/nonce/
signature flow, `ProbeAdapterBuildRequest`/`RegisterAdapterBuildRequest`) to V7-05B: too large and too
security-sensitive to bundle into the first real-data screen. Reading `internal/app/doctor/checks.go`/
`internal/delivery/httpapi/doctor/{dto,queries}.go` directly first (necessary: the generated client's
own contract is one-level-shallow, so `DoctorResponse.checks` comes back as `unknown[]` — the real
per-check shape had to be read from the Go source, not guessed from the existing FAKE prototype's own
invented fixture data) also revealed the fake `DoctorScreen`'s entire displayed check set
("PostgreSQL 16.2", `openai`/`anthropic` with specific fake version strings, `go-executor`/
`python-executor` fake adapters) does not correspond to any real backend check at all — this was always
going to be a full rewrite, never a data-source swap.

Manually verifying the finished screen against a REAL running `aw serve` (not just `tsc`/`vitest`, which
cannot catch this class of bug) surfaced two real, previously-undiscovered defects, both fixed in this
same PR because shipping the Doctor screen without them would ship a screen broken on refresh:

1. **A CSP violation silently breaking custom fonts.** `web/src/index.css`'s own `@import` of Google
   Fonts (from the original Figma Make prototype) is blocked by V7-02A's own `style-src 'self'` CSP —
   invisible via `pnpm dev` (no CSP there) or a bare `curl` (V7-02A's own verification method), only
   visible via a real browser's console once BOTH pieces existed together. Rather than widening the CSP
   to allow an external CDN, removed the external font dependency entirely: this is a local, loopback-
   only tool (ADR-016's own posture), and silently calling out to Google's font CDN on every page load —
   even before the CSP started blocking it — was already in tension with that. Falls back to
   `system-ui`/`ui-monospace`, already declared as the second choice in both font stacks.

2. **A real routing collision, found the same way.** `web/src/routes.ts`'s own client-side paths at the
   time (`/doctor`, `/projects`, `/projects/{id}`, `/projects/{id}/components`) are IDENTICAL strings to
   real REST API paths this same `aw serve` process also serves. A client-side `pushState` navigation
   never round-trips to the server, so V7-04A's own manual QA (against `pnpm dev`, which has no real API
   behind it at all) never could have caught this — only a direct browser hit on `/doctor` against a REAL
   backend does, and it returned the raw JSON API response instead of the SPA shell. Worse, this exposed
   a SECOND, more fundamental gap underneath it: `aw serve` had no SPA-fallback route at all — only the
   exact path `/` ever served the bootstrap shell, so EVERY other client-side route (even a hypothetically
   non-colliding one) already 404'd on a direct deep-link or refresh; V7-04A's own routing work was never
   actually exercised against the real binary end-to-end before now.

   Fixed both at once: every client-side route now lives under a `/ui/` prefix (`web/src/routes.ts`'s own
   `UI_PREFIX` constant) — no REST resource in this codebase has ever used "ui" as a top-level segment
   (every one is a domain noun) — and `httpcompose/compose.go` gained a new catch-all route,
   `GET /ui/{path...}` (OperationID `uiShell`), registered with the SAME `BootstrapHandler` already
   serving exact `/`, so ANY path under `/ui/` gets the SPA shell regardless of which screen's URL a
   bookmark or refresh names. `uiShell` joins `bootstrap`/`staticAsset` in ADR-028's own browser-only
   parity exemption (`internal/delivery/parity`) and the TypeScript client generator's own
   `browserOnlyOperations` (no callable function generated for it — a UI action never fetches it, a
   browser navigation does). One securitymatrix test needed a narrow, explicit exemption too:
   `TestScopeMatrix_UnknownIdentifierIsNeverServed` treats any `{...}`-containing path as an "identifier
   lookup that must never answer 2xx for an unknown id" — correct for every real resource route, but
   `/ui/{path...}` is not a lookup at all, it is a wildcard that MUST answer 200 for any path by design;
   excluded by OperationID with a doc comment explaining why, the same way `emptyCollectionOnUnknownProject`
   already documents its own narrow exemptions in that same file.

### Decision

`web/src/screens/Doctor.tsx` is a full rewrite (not a data-source swap, per Context above): real checks
grouped under the exact three categories `internal/app/doctor`'s own package doc comment names
(Liveness/Readiness/Capability — never an invented taxonomy), generic per-check rendering (name/status/
detail/remediation) with no hardcoded check-name table (the real check set is installation-dependent —
one `provider:<name>` check per configured provider), a real overall HEALTHY/DEGRADED/BLOCKED summary
banner, a real `restartRequired` banner, and "Re-run all checks" as a plain `refetch()` — Doctor's own
checks are computed live on every GET, so no separate "probe" mutation is needed for this scope. Also
renders the REAL registered-adapter-builds list (`listAdapterBuilds()`, read-only) so "hiển thị adapter
build là registered hay unregistered" is partially real today; the interactive register flow (ADR-022)
is V7-05B.

The real per-check DTO shape (`DoctorCheck` in `Doctor.tsx`) is hand-declared, not generated — the same
one-level-shallow-contract limitation `apicontract`'s own doc comment already names, hit again here for
an array ELEMENT'S shape rather than a top-level response's.

### Execution

- `web/src/api/queryClient.ts` (new): the one shared `QueryClient` (`retry: false` — this app talks to
  its own loopback `aw serve`, not a flaky remote API; a failed GET is a real error worth surfacing
  immediately, and the SSE client already owns the "retry with backoff" story for the one thing that
  needs it).
- `web/src/main.tsx`: wraps `<App/>` in `<QueryClientProvider>`.
- `web/src/screens/Doctor.tsx`: full rewrite, real data.
- `web/src/index.css`: removed the external Google Fonts `@import`s and the 'Inter'/'JetBrains Mono'
  family names from both font stacks (system-ui/ui-monospace fallbacks were already declared).
- `web/src/routes.ts` / `web/src/App.tsx`: every path now lives under `/ui/`; `App.tsx`'s two literal
  `/doctor`/`/projects` string comparisons replaced with `pathFor(...)` calls so this can never drift
  again.
- `internal/delivery/httpcompose/compose.go`: new `GET /ui/{path...}` route (OperationID `uiShell`),
  reusing `deps.BootstrapHandler`.
- `internal/delivery/parity/check.go`: `BrowserBootstrap["uiShell"] = true`.
- `internal/delivery/httpapi/apicontract/tsclient.go`: `browserOnlyOperations["uiShell"] = true`.
- `internal/delivery/httpapi/securitymatrix/scope_test.go`: `uiShell` excluded from
  `TestScopeMatrix_UnknownIdentifierIsNeverServed`'s identifier-lookup assumption, with a doc comment.
- Pinned-count/golden fallout (same mechanical pattern as V7-02A's own `staticAsset` addition):
  `wantRouteCount` 89→90 in both `apicontract/routeinventory_test.go` and
  `securitymatrix/transport_test.go`; `uiShell` added to `TestUndocumentedOperationsSnapshot`'s pinned
  list; `testdata/golden/contract.json` regenerated (additive-only diff, reviewed).
- `web/src/screens/Doctor.test.tsx` (new, 10 tests): loading state, category grouping, HEALTHY/DEGRADED/
  BLOCKED summaries (status not by color alone), remediation text, restart-required banner, error state,
  rerun refetches both queries, real adapter-build rendering, empty state, accessibility smoke.
- `web/src/routes.test.tsx` / `web/src/App.routing.test.tsx`: updated to the `/ui`-prefixed paths;
  `App.routing.test.tsx` now mocks `../api/generated` (Doctor is the default landing screen, so every
  routing test renders it) and wraps with a fresh per-test `QueryClientProvider`; added a regression case
  proving a bare `/doctor` (no `/ui` prefix — the exact collision this task fixed) is never matched as a
  client route and settles on `/ui/doctor` instead.

### Verify

- `go build ./...`, `go vet ./...`, full `go test ./...` (whole repo): clean.
- `npx vitest run`: 123/123 pass (112 from V7-04B + 10 Doctor + fixed-up routing tests).
- `npx tsc --noEmit`: clean.
- `pnpm run build`: clean.
- Manual end-to-end verification against a REAL running `aw serve` (not `pnpm dev`, not `curl` — a real
  browser, the only way either bug above was ever going to surface): built `web/`, ran the real `aw`
  binary with `--ui-dist`, confirmed zero console errors (font fix), confirmed `GET /doctor` (bare, the
  REST API) still returns raw JSON directly while `GET /ui/doctor` and a DIFFERENT deep link
  (`GET /ui/projects`) both correctly serve the real SPA shell with real rendered data — verified via the
  actual network request log, not just visual inspection.

## V7-05B — Adapter build probe → confirm → register (ADR-022)

### Context

V7-05A shipped the Doctor screen's read-only half (checks, registered-builds list) and explicitly
deferred the mutating half: `docs/design/09-v7-alpha-ui.md` V7-05's own line requires "cung cấp action
probe → xác nhận → đăng ký theo ADR-022" — Doctor must let the operator run the whole ADR-022 registration
flow from the browser, not just view its outcome. The backend side of this (`internal/app/adapterbuild`,
`internal/delivery/httpapi/adapterbuild`, `internal/delivery/cli/adapterbuild`) was already fully built
and tested in V6 (V6-10I/V6-10J) — this task is a pure UI consumer of an existing, hardened surface.

### Decision

Mirror `aw adapter probe`/`aw adapter register`'s own real flag set (read directly from
`internal/delivery/cli/adapterbuild/probe.go`/`register.go`) as a two-step dialog rather than a single
combined form: the underlying protocol is genuinely two separate commands with a real operator decision
in between (ADR-022 point 2: "Hiển thị candidate fingerprint... Operator xác nhận"). Step 1 collects every
field `ProbeRequest` needs and calls `POST /adapter-builds/probe`; step 2 shows the SERVER-MEASURED
candidate token (never a client-side guess) read-only, and "Confirm & register" calls `POST
/adapter-builds` with the token echoed back byte-for-byte plus the SAME capability-manifest object step 1
already built — never re-typed, since `RegisterAdapterBuild` re-hashes and rejects on any mismatch
(`ErrCapabilityManifestDrift`), so a second editable copy would only invite an accidental rejection.

### Execution — a second real, previously-undiscovered gap (same category as V7-05A's routing bug)

Before writing any UI, traced how a mutation actually reaches the wire: `web/src/api/generated.ts`'s
generated `request()` helper (`internal/delivery/httpapi/apicontract/tsclient.go`) attached
`X-Aw-Session-Token` for a mutation but **never attached `Idempotency-Key` at all** —
`httpapi.RequireIdempotencyKey` (`internal/delivery/httpapi/commandenvelope.go`) rejects any mutation
missing that header with 400. Every V7 screen shipped through V7-05A only ever called GET endpoints
(`doctor`, `listAdapterBuilds`), so this was invisible until now: V7-05B is the first screen to call a
real mutation from the browser, and it would have 400'd on its very first submit. Fixed at the generator
(`tsclient.go`'s `tsClientPreamble`): a non-safe request now sends `Idempotency-Key:
crypto.randomUUID()` unless the caller passes `opts.idempotencyKey` explicitly (mirrors `aw`'s own CLI
mutations, which generate one when `--idempotency-key` is omitted — same default behavior, two
transports). Regenerated `web/src/api/generated.ts` via the documented golden-fixture recipe (temporary
`zzregen_test.go`, deleted after use) — an additive-only diff (new `idempotencyKey` field on
`RequestOptions`, new header line in `request()`).

New files:
- `web/src/screens/AdapterProbeDialog.tsx`: the two-step dialog described above. Client-side validation
  (required fields, `supportsStart` must be checked) blocks the API call entirely rather than letting the
  server's own 400 be the only feedback — mirrors `TextField`'s existing `required`/`error` convention.
  Register errors (expired token, drift, no signing key yet) surface via the existing `InlineError`
  primitive with a "Retry" action that goes back to step 1 with the form state intact, never silently
  discarding what the operator typed.

Changed files:
- `internal/delivery/httpapi/apicontract/tsclient.go` / `web/src/api/generated.ts`: the Idempotency-Key
  fix described above.
- `web/src/screens/Doctor.tsx`: added a "Probe new build" button (disabled while offline, same as
  "Re-run all checks") in the Registered Adapter Builds section header, opening `AdapterProbeDialog`;
  wired `useToasts()`/`ToastViewport` (V7-03B's own primitive, never previously mounted by any real
  screen) to show a success toast and invalidate the `['adapterBuilds']` query on registration.

### Verify

- `go test ./internal/delivery/... ./internal/archtest/...` (whole tree, `-count=1`): clean, including
  `TestGeneratedTypeScriptClient_MatchesGoldenFixture`/`_SkipsBrowserOnlyOperations`.
- `npx tsc --noEmit`: clean.
- `npx vitest run`: 132/132 pass (123 prior + 8 new `AdapterProbeDialog` tests: empty-form validation,
  `supportsStart`-unchecked rejection client-side before any API call, probe→candidate-review transition
  with the server-measured hashes shown verbatim, register success reporting and closing, a probe API
  error surfaced inline without ever reaching the candidate view, a register error (expired token)
  round-tripping back to step 1 with form state preserved, and accessibility smoke on both steps + 1 new
  `Doctor.test.tsx` case wiring the whole dialog through the real screen).
- `pnpm build`: clean.
- Manual end-to-end verification against a REAL running `aw serve --ui-dist` (not mocks, not `pnpm dev`):
  opened `/ui/doctor` in a real browser, clicked "Probe new build", filled every field with a real local
  executable path (the just-built `aw.exe` binary itself), clicked Probe — the candidate review step
  showed a REAL server-computed `sha256:...` content hash (proving `ProbeAdapterBuild` actually read and
  hashed the file, not an echo of client input), clicked "Confirm & register" — the dialog closed, a
  success toast appeared, and the Registered Adapter Builds list immediately showed the new entry with
  `REGISTERED` and the same hash. Confirmed via the real network request log that both
  `POST /adapter-builds/probe` and the register call returned `200 OK` — impossible before the
  Idempotency-Key fix, which is exactly the kind of gap only a real mutation attempt against a real
  backend surfaces (the same lesson V7-05A's routing bug already taught).

## V7-06A — Project list/create and repository register/onboarding/retry-probe

### Context

`docs/design/09-v7-alpha-ui.md` V7-06's own "Thực hiện" line: "create project, register/probe repo,
view components/health ... canonical local locator input, state REGISTERING|PROBING|ACTIVE|BLOCKED|
DISABLED, actionable retry, multi-repo project view và exact-version Engineering Pack assignment cho
component." This is genuinely two natural halves: (A) project/repository onboarding — the state machine
and the operator-facing forms — and (B) the Engineering Pack exact-version assignment UI, which needs its
own investigation into where a Pack VERSION ID comes from (the definitions catalog) and is deliberately
scoped to a separate task rather than bloating this one, per the repo's own "don't split by default,
only when genuinely large" doctrine — this split mirrors the same natural feature boundary V7-04A/B and
V7-05A/B already used.

The existing `web/src/screens/Projects.tsx` was still the Figma-Make prototype's fully fake, in-memory
`INITIAL_PROJECTS` scaffold (hardcoded array, `window.setTimeout` calls simulating state transitions) —
this task replaces it entirely with real data, the same "read the existing prototype's own real backend
before touching the screen" discipline every V7-0x task so far has followed.

### Decision

`internal/delivery/httpapi/catalog` (V6-03A) registers every one of its 9 routes with `RequestSchema:
struct{}{}, ResponseSchema: struct{}{}` — a KNOWN, self-documented gap (apicontract's own package doc
comment names catalog as one of three leaf packages that never supplied a real schema, and explicitly
says fixing it is a later task's job, not V6-12's). Rather than retrofit that Go-side gap (out of scope,
touches a different subsystem, and the same gap exists in 2 other packages this task does not need), this
screen hand-declares its own TS interfaces mirroring `internal/delivery/httpapi/catalog/views.go`'s real
view types field-for-field (`web/src/api/catalog.ts`) — the same "narrow `unknown` at the call site"
convention `Doctor.tsx`'s own `DoctorCheck` already established.

A Repository's own identity (`RepositoryID`) is caller-chosen, not minted by the server
(`appcatalog.RegisterRepositoryRequest`'s own doc comment) — unlike a Project. The register-repository
form therefore has an explicit, required "Repository ID" field, separate from Name and the local path,
matching V7-06's own "không suy repo từ path/name" line: the UI never derives an identity by slugifying
the path or name, the operator must state it.

`RemoteLocator` is, despite its name, a canonical LOCAL filesystem path
(`internal/app/repositoryprobe/handler.go`'s own `h.prober.Probe(ctx, repo.RemoteLocator, ...)`, and
`docs/design/01-system-design.md`'s own "Repository locator được canonicalize và không nằm dưới managed
workspace root") — the form labels it "Local repository path" with that exact constraint as helper text,
never "Remote URL", to avoid misleading the operator about what this installation actually does with it.

### Execution — a third real gap found the same way the first two were (missing If-Match support)

`POST /repositories/{id}/retry-probe` is this task's first-ever call from any V7 screen to an
update-shaped mutation (`httpapi.RequireIfMatch`) — every mutation up through V7-05B was create-shaped.
The generated client had no way to send an `If-Match` header at all. Fixed the same way the V7-05B
Idempotency-Key gap was fixed: `RequestOptions` gained an `ifMatch?: string` field
(`internal/delivery/httpapi/apicontract/tsclient.go`'s `tsClientPreamble`), sent as the `If-Match` header
on any non-safe request that supplies it. The caller builds the value itself from a response's own
`version` field (`` `"${version}"` ``, matching `httpapi.ETagFromVersion`'s exact wire format) — no need
for the client to ever read response headers, since every relevant view (`repositoryView`) already
carries `version` in its JSON body.

**A second real bug found only by testing the actual create-project-then-navigate flow in a real
browser**: `CreateProjectDialog`'s success handler called `queryClient.invalidateQueries({queryKey:
['projects']})` (schedules an async refetch) immediately followed by `onSelectProject(result.projectId)`
(navigates to the new project's URL). App.tsx's own route guard — reading that SAME `['projects']` cache
entry to check "does this URL's projectId exist" — ran on the very next render, before the invalidated
query's refetch had resolved, saw the STALE (pre-create) list, concluded the brand-new project did not
exist, and bounced straight back to `/ui/projects`. Manually walking through "create a project, watch it
open" in the real browser caught this on the first attempt; no unit/component test happened to exercise
this exact interleaving. Fixed by writing the create result directly into the query cache via
`queryClient.setQueryData` (synchronous, and `CreateProjectResult` already carries everything a fresh
`ProjectView` needs — id/name/status, version always starts at 1 per `project.NewProject`) instead of an
async invalidate-and-hope-it-resolves-in-time.

New files:
- `web/src/api/catalog.ts`: hand-declared `ProjectView`/`RepositoryView`/`ProbeAttemptView`/
  `OnboardingView`/`ComponentView`/`CreateProjectResult`/`RegisterRepositoryResult`.
- `web/src/screens/Projects.test.tsx` (new, 12 tests).

Changed files:
- `internal/delivery/httpapi/apicontract/tsclient.go` / `web/src/api/generated.ts`: the If-Match fix.
- `web/src/screens/Projects.tsx`: full rewrite onto real `projectsList`/`projectsCreate`/
  `projectRepositoriesList`/`projectRepositoriesRegister`/`repositoriesRetryProbe`/`projectComponentsList`.
  Retains a narrow `ProjectSummary` compatibility type (id/name/repositories with id/name/state) for the
  two screens this task deliberately does not touch (Kanban's board filter chips, Definitions' scope
  label) — both are still fake-data prototypes of their own, out of scope here (V7-09/V7-10).
- `web/src/App.tsx`: removed all fake `projects`/`handleCreateProject`/`handleRegisterRepository`/
  `handleSetRepositoryState` local state; `project` is now derived from a real `useQuery(['projects'])`
  (shared cache with Projects.tsx's own identical query); the route guard now skips redirecting while
  that query is still pending, never bounces a fresh deep link away just because the fetch has not
  resolved yet; builds the `ProjectSummary` compatibility shim for Kanban/Definitions from a second real
  `['projectRepositories', projectId]` query (sharing that cache key with Projects.tsx's own overview).
- `web/src/App.routing.test.tsx`: updated mock to `importActual`-merge real exports (needed once real
  `ApiError`/`projectsList`/etc. are reachable) plus fixture project data for the existing routing
  assertions.

### Verify

- `go build ./...`, `go vet ./...`, `go test ./internal/delivery/... ./internal/archtest/...` (`-count=1`):
  clean.
- `npx tsc --noEmit`: clean.
- `npx vitest run`: 144/144 pass (132 prior + 12 new `Projects.test.tsx` tests: loading/empty states, real
  project rendering and selection, create-project validation and real API call, real repository rendering
  with Retry Probe shown only on BLOCKED, retry sending the correct repository id and If-Match version,
  register-repository validation and the exact field names the backend expects, real component rendering
  and its own empty state).
- `pnpm build`: clean.
- Manual end-to-end verification against a REAL running `aw serve` AND a REAL running `aw worker` (the
  durable job that actually executes a repository probe needs a worker process, not just the API server):
  created a real local git repository on disk, created a project through the UI, registered that real
  repository through the UI, and watched it transition REGISTERING → PROBING → BLOCKED for a genuine
  reason (`main` did not resolve — the repo's real default branch was `master`; not a bug, a wrong test
  fixture assumption caught by the real probe actually running real `git` commands), clicked Retry Probe
  after fixing the branch, watched it reach ACTIVE. Registered a second repository pointing at a version
  of that same path with a real `src/` subdirectory and confirmed the Components view showed a real
  discovered `DIRECTORY` component linked to the correct repository ID. Also caught and fixed the
  create-project navigation race described above — found only by actually clicking through the flow, not
  by any automated test.

## V7-06B — Engineering Pack exact-version assignment for a component; V7-06 closed

### Context

The other half of V7-06's own scope, deliberately deferred from V7-06A: "exact-version Engineering Pack
assignment cho component" (`docs/design/09-v7-alpha-ui.md`). Backend surface
(`GET/POST /components/{id}/pack-assignments`, `internal/app/catalog.AssignComponentPack`) was already
built and tested in V6-03A; this task is a pure UI consumer.

### Decision

`internal/domain/project/component.go`'s own `PackVersionID` doc comment states plainly: assigning a pack
version "never resolves or validates that the pack version actually exists ... a later task ... owns that
check if one is ever needed" — the exact same behavior `aw pack-assignment assign`
(`internal/delivery/cli/catalog/packassignment.go`) already has today (a raw string, no catalog
cross-reference). A browsable picker of published Engineering Pack versions is explicitly `docs/design/
09-v7-alpha-ui.md`'s own V7-07 ("Definition catalog và version detail"), a separate, not-yet-started task —
building one here would either duplicate that later work or invent a listing capability this leaf was
never given. `AssignPackDialog` therefore takes a single required, plain text "Pack version ID" field,
honestly labeled as not yet validated against the definitions catalog, exactly matching what the CLI and
the backend actually do today.

### Execution

New file `web/src/screens/AssignPackDialog.tsx`: shows the component's identity, its currently-effective
assignment (or "No pack assigned yet"), the full append-only assignment history when more than one
assignment exists, and the assign form. Wired into `Projects.tsx`'s components-view table as a new
"Assign Pack" button per row (disabled while offline, same convention as every other mutating action this
screen already has), reusing the same `useToasts()`/`ToastViewport` this screen's overview already
mounts.

Added `PackAssignmentView`/`PackAssignmentListView` to `web/src/api/catalog.ts` — the same hand-declared,
`views.go`-mirroring convention V7-06A already established for this package's known opaque-schema gap.

### Verify

- `npx tsc --noEmit`: clean.
- `npx vitest run`: 151/151 pass (144 prior + 6 new `AssignPackDialog.test.tsx` tests — no-assignment
  empty state, real effective assignment plus history rendering, required-field validation before any API
  call, assigning the exact operator-typed version string and reporting success, an API error surfaced
  inline, accessibility smoke — + 1 new `Projects.test.tsx` case wiring "Assign Pack" through the real
  components table).
- `pnpm build`: clean. No Go files touched by this task.
- Manual end-to-end verification against a REAL running `aw serve` and `aw worker`: created a project,
  registered a real local git repository with a `src/` subdirectory (this time with its real default
  branch actually named `main`, learning from V7-06A's fixture mistake), watched it reach ACTIVE and the
  `src` component get discovered, opened Assign Pack and confirmed "No pack assigned yet", assigned
  `engpack-backend@2.4.2`, confirmed via the real network log the call returned `201 Created`, and
  reopened the dialog to confirm "Currently effective" now shows that exact version with a real
  timestamp — the full real round trip, not a mocked one.

**V7-06 is now fully closed** (V7-06A + V7-06B, PR #105 and this task's own PR).

## V7-07A — Backend: expose `listDefinitions`/`listProjectDefinitions` over HTTP, closing a real V6 parity gap

### Context

Starting V7-07 ("Definition catalog và version detail", `docs/design/09-v7-alpha-ui.md`) by researching the
real backend surface (the same discipline every V7-0x task has followed) surfaced something that made
building the UI directly impossible: `internal/delivery/httpapi/definitions` (V6-05) exposes create/
validate/publish/get-one/list-versions/diff for a Definition, but **no route at all can enumerate "every
Definition of Kind X in scope Y"** — every existing route needs the caller to already know a specific
DefinitionID first. There is no way for a browser to "browse the catalog" without this.

This is not a newly-discovered bug — it is a REAL, ALREADY-DOCUMENTED, ALREADY-PINNED gap the repo's own
V6-15O parity gate found and intentionally left open, tracked verbatim in
`internal/delivery/parity/ledger.go`:

> `{ClassCLILocalNotAllowed, "cli:definition list@INSTALLATION", "V6-05 (route) + V6-15E (leaf descriptor)",
> noRoute + "; the leaf must then register listDefinitions instead of CLI_LOCAL"}`

`internal/app/definitions.ListDefinitions` (the real query) has existed since V6-15E; `aw definition list`
(a real, working CLI command) has called it since the same task — but its own CLI descriptor was
registered with `cli.CLILocalOperation` (no HTTP twin) specifically because V6-15O's own "Không làm: no
new leaf/route" line forbade the parity-gate task itself from adding the missing route, and no later task
had needed `listDefinitions` from a delivery adapter other than the CLI until now. `internal/delivery/
httpapi/apicontract/uxgap.go`'s own `knownUnimplementedGaps` map independently tracked the identical gap
for the UX-doc-cross-reference checker, with an almost prophetic comment on the gate test itself
(`uxgap_test.go`'s `TestCheckUXGaps_NoUnresolvedGap`): "if a future leaf task closes `listDefinitions`,
this test starts failing — not because anything is wrong, but as the forcing function to go delete that
now-stale entry."

### Decision

Add exactly the two routes the ledger's own remediation text names — `GET /definitions/{kind}`
(`listDefinitions`, installation scope) and `GET /projects/{projectId}/definitions/{kind}`
(`listProjectDefinitions`, project scope) — as a thin HTTP wrapper over the already-existing, already-
tested `appdefinitions.ListDefinitions`, mirroring every sibling route in the same package exactly
(`pathKind` for `{kind}` validation, `writeQueryError` for error mapping, a `definitionListView` DTO
wrapping the collection the same way `versionListResponse` already does). Then flip the CLI descriptor
from `cli.CLILocalOperation` to the two new real operationIds, and delete the now-resolved entries from
both the parity ledger and `knownUnimplementedGaps` — exactly the remediation the codebase had already
written down for whoever picked this up.

Deliberately scoped as its own task (V7-07A) rather than folded into the UI work (V7-07B, next): this
touches already-closed V6 code across four different subsystems (HTTP routes, CLI descriptors, the parity
gate, the API contract generator) and is a real, independent, mechanically-verifiable unit of work with
its own clear "done" condition (`internal/delivery/parity`'s own gate tests all pass, debt count drops)
before any UI work depends on it.

### Execution

New route handlers `handleListDefinitions`/`handleListProjectDefinitions`/`listDefinitionsCore` in
`internal/delivery/httpapi/definitions/detail.go`; new `definitionListView` DTO in `dto.go`; two new
`RouteDescriptor` registrations in `routes.go` (doc comment's own route-inventory table and "8 operations
× 2 scopes = 16 routes" count updated to match). `internal/delivery/cli/definitions/descriptor.go`'s two
`definition list` descriptors now carry `HTTPOperationID: "listDefinitions"`/`"listProjectDefinitions"`
instead of `cli.CLILocalOperation` — `doc.go`'s own historical explanation updated to describe what
actually happened rather than leaving a stale "no HTTP route exists" claim in a comment. `internal/
delivery/parity/registry.go`'s `ListDefinitions` entry gained real `HTTP: []HTTPBinding{...}` bindings
(previously commented "no HTTP route exists (V6-05 gap)"); `ledger.go` lost its four now-resolved entries
(the exact ones the ledger's own doc comment said this exact remediation would let disappear).
`apicontract/uxgap.go`'s `knownUnimplementedGaps` map is now empty (kept as a real, typed empty map, not
deleted, so the gate's own "ACKNOWLEDGED set matches this map's key set" assertion still has something to
compare against).

New test file `internal/delivery/httpapi/definitions/list_test.go` (4 tests, real HTTP round-trips against
a real `*sqlite.Store` — the same `newTestEnv` harness every sibling test file in this package already
uses, never a mock): kind+scope filtering never leaks a wrong-kind or wrong-scope Definition into the
list, an empty/unknown scope returns a present empty array rather than an error or 404 (matching
`ListDefinitions`' own documented contract), and an invalid `{kind}` path segment is a 400. Discovered
along the way and fixed in the test fixtures themselves (not application code): the `definitions` table's
own real schema (`internal/adapters/sqlite/migrations/0004_shared_definitions.sql`) makes `id` (the
caller-supplied DefinitionID) a single-column PRIMARY KEY with no scope component at all — a DefinitionID
is globally unique across every scope, not just unique-per-scope as an initial draft of these tests
wrongly assumed (reusing the same ID across a global and a project-scoped fixture 500'd on the second
create) — corrected by giving every fixture its own distinct ID and re-describing what the project-scope
isolation test actually proves (a WHERE-clause bug, not an ID collision).

### Verify

- `go build ./...`, `go vet ./...`: clean.
- `go test ./internal/delivery/... ./internal/app/... ./internal/archtest/...` (`-count=1`): clean (one
  unrelated, previously-documented flake — `TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing`,
  confirmed diff-unrelated and did not reproduce on a second run).
- `go test ./internal/delivery/parity/...`: clean, including `TestRealInventoryParityGate` (debt dropped
  from 17 to 13 — the exact four ledger entries this task closes) and `TestCLILocalClosedSetIsExactlyThe
  DesignedOne`/`TestEveryRealCLILocalDescriptorIsInTheClosedSetOrLedgered` (the `definition list`
  descriptors no longer need either exemption at all, having a real HTTP twin now).
- `go test ./internal/delivery/httpapi/apicontract/...`: clean, including `TestCheckUXGaps_NoUnresolvedGap`
  (the exact forcing-function failure its own doc comment predicted, now resolved by clearing
  `knownUnimplementedGaps`) and the regenerated `testdata/golden/contract.json`/`web/src/api/generated.ts`
  (additive-only diff: two new operations, `listDefinitions`/`listProjectDefinitions`).
- `go test ./internal/delivery/httpapi/securitymatrix/...`: clean (route count 90→92; `listProjectDefinitions`
  added to the reviewed `noCrossProjectProof` list under the same category its sibling
  `getProjectDefinition`/`listProjectDefinitionVersions` routes already document — a `{kind}` path segment
  is a closed-vocabulary type discriminator, never a per-instance identifier a cross-project leak proof
  could meaningfully swap).
- `go test ./internal/delivery/cli/definitions/...`: clean, including the updated
  `TestDescriptorsRegisterAllSixteenCommandsWithConsistentMetadata`.
- No web files touched by this task; V7-07B (the actual catalog-browsing UI, now unblocked) is next.

