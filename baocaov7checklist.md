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
