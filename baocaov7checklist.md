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
