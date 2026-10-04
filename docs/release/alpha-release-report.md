# Alpha release report

**Verdict: `ALPHA_READY`** — `gatePass = true`.

The machine-readable record of this verdict is [`alpha-verdict.json`](alpha-verdict.json); this page is its
readable form, and a test keeps the two (and [`../00-start-here.md`](../00-start-here.md)) from disagreeing.

## What was assessed

| | |
|---|---|
| Commit | `0b144f12b61e8f3aab0900d1cc9b2845650cb523` (master after V8-12R-01, PR #141) |
| Assessment | CI run [36803384120](https://github.com/draculemihawk123-ai/aw-tqunglnh/actions/runs/36803384120), artifact `v8-alpha-assessment` |
| Tool | `cmd/v8-alpha-gate` (`internal/alphagate`), V8-11 |
| Rule | `ALPHA_READY` only when `gatePass = true`; any other verdict names its blocker and the next narrow task (`docs/design/10-v8-alpha-hardening.md`, V8-12) |
| History | The first assessment (commit `6d3bab4`, run 36786340396) was `REWORK`: 6 of 7 final gates, the failing one being `parity-inventory-has-zero-debt` (15 ledger entries). V8-12R-01 closed it; an interim record (`CHƯA ĐỦ EVIDENCE`) said so honestly until a gate run of the fixed commit existed, and this verdict is that run's. |

The assessment reads the V1-00 coverage map (ADR-024 phase labels, never reclassified), what the repository's tests
cite, every CI suite's result, and the final-gate test run. It is produced even when suites fail.

## Result

| Area | Result |
|---|---|
| `ALPHA_MUST` criteria | **208 / 208 PASS** (plus 1 `CROSS_PHASE_GUARD`, also PASS) |
| Beta-labeled criteria | 2, reported as outside Alpha scope (not failures) |
| Not applicable | 1 (authority reason recorded) |
| System acceptance journeys | **23 / 23 PASS** |
| Version gates V0–V8 | **9 / 9 PASS** |
| Final gates | **7 / 7 PASS** — cancel-vs-claim in both commit orders, route inventory = OpenAPI both directions, recovery-command owners (core/API/UI/CLI), the parity inventory (UI, `operationId`, `aw` command and application operation) with zero debt, SourceRef debt 0, `git diff --check`, `go test`/`go vet` on both platforms |

## What `ALPHA_READY` says, and what it does not

It says every Alpha criterion has passing evidence at the granularity recorded for it, and every journey, version gate
and final gate passed on both platforms for the assessed commit. It does **not** say:

- that each criterion has its own test — only 47 of 209 do (LIM-01);
- that the product works cleanly with a real Claude or Codex CLI — compatibility is `PARTIAL` (below): Claude was run for real and showed defects, Codex was not run;
- that process isolation is enforced by the operating system (LIM-02);
- anything about Beta (PostgreSQL, login/RBAC, server workers), which is out of scope.

From this verdict on the gate job is enforcing (`--enforce=true`): a regression of any Alpha criterion, journey, version
gate or final gate turns its check red, and a test ties that flag to this record so the two cannot drift.

## Known limitations

These are real and are disclosed rather than smoothed over. None is a hidden failure: each is either out of Alpha scope
by design or an acknowledged gap with a named follow-up.

- **LIM-01 — Evidence granularity.** Only 47 of the 209 Alpha-gated criteria are named by a test; the other 162 are
  covered at the granularity of their owning versions' suite gates. The matrix records which is which (`evidenceLevel`).
  It proves "the suites that own this criterion pass", not a test-per-criterion trace.
- **LIM-02 — No real-OS sandbox.** `ENFORCED_ISOLATED` is unavailable; Alpha runs `OPERATOR_TRUSTED_LOCAL` isolation
  and a node pinned to `ENFORCED_ISOLATED` fails closed (by design, ADR-022).
- **LIM-03 — Windows UI checks are newly real.** Until 2026-10-01 (PR #139) the Windows legs of `web`, `e2e` and
  `release-build` ran pnpm under a shell that did not wait for it, so they passed without running. Earlier Windows UI
  evidence must not be relied on; the evidence for this commit is from after the fix.
- **LIM-04 — Older binaries open newer databases silently.** The downgrade guard (V8-10) protects binaries built from
  V8-10 onward. Take a backup with the running release before any upgrade
  ([upgrade and rollback](../operator/10-upgrade-and-rollback.md)).
- **LIM-05 — `aw-maintenance backup` migrates first.** It opens the database through the normal startup path, so a
  new build's tool upgrades the live database before backing it up; use the previous release's tool for a pre-upgrade
  backup.
- **LIM-06 — Duplicate repository id.** Registering a repository id that already exists returns a 500 instead of a typed
  conflict (finding from the V8-11 e2e retry behaviour).
- **LIM-07 — Large-graph/diff UI performance is unmeasured.** V8-07 measured Kanban, task detail, scheduler and
  projection rebuild; a Playwright performance smoke for a large workflow graph/diff was deferred.
- **LIM-08 — Some fault categories are only partly covered.** V8-02 covers the six crash-transaction boundaries against a
  real worker; artifact corruption beyond crash-during-put, workspace corruption at real topology, and lease expiry
  with no crash involved are acknowledged and not closed.
- **LIM-09 — One fault scenario is still "conditional" in the V6 verdict.** `CrashDuringRebuildBeforeCutover` was made
  deterministic (V8-11) but stays in the conditional list until it has been seen not to skip across runs.
- **LIM-10 — Six ADRs have no owner task** (ADR-001, 002, 003, 004, 007, 029). They are decision sources, not criteria,
  so they do not gate; the matrix lists them.
- **LIM-11 — Test-only flags exist.** `aw worker` accepts `--projection-rebuild-batch-size`,
  `--projection-rebuild-round-delay` and `--local-commit-write-lease-ttl` so acceptance tests can force races;
  production has no reason to set them.

Scope statement, not a limitation: Alpha is single-user, local, SQLite, artifacts on local disk, not encrypted at rest.
Beta (PostgreSQL, login/RBAC, server workers) is out of scope and no Beta backlog is created here.

## Live provider compatibility

**Status: PARTIAL.** Every provider test in CI — the conformance matrix, the admission/drift/isolation scenarios, the
full journeys — runs against the repository's own `fake-claude` and `fake-codex` wire-protocol stand-ins. V9-11 added a
run against a **real Claude CLI** (Claude Code 2.1.288, `claude-sonnet-4-6`, effort `medium`) through the same stack:
real git worktrees, sqlite, worker pool and `COMMAND` process. It is skipped unless `AW_LIVE_CLAUDE=1` (it spends
money), so CI does not repeat it; the evidence is in [`live-provider/`](live-provider/README.md).

- **Works with the real CLI:** registering the adapter build from the real `claude.exe` and re-probing it at every
  admission; the canonical event stream from the real `stream-json`; a check that fails and sends the run back to
  the maker with its message in the maker's prompt (V9-02); the reviewer choosing `approved`/`rework` from the prompt's
  `allowedOutcomes` with the marker protocol (V9-03); resources reaching only the role they are tagged for (V9-04); a
  checker running after a maker in the same run without a scope violation (V9-01); the agent process getting exactly
  the declared environment names with no wrapper (V9-05).
- **Found by the first run and fixed by V9-11a** (re-run: `live-provider/run-3-fixed`): F1 — a headless Claude denied
  every file write in the worktrees `aw` creates; `aw worker --claude-permission-mode` now sets the CLI's permission
  mode. F2 — a reviewer did not see the repository (its working directory is an empty scratch directory) and approved a
  file it wrote itself; the adapter now names the repository mounts to the CLI and the scenario fails if a reviewer
  approves without reading the maker's file. F5 — a `COMMAND` has no `PATH` unless its definition declares one;
  documented.
- **Fixed after the V9 verdict by V9-13a:** F4 — `aw worker --claude-effort` and `--claude-max-budget-usd` now pass
  the CLI an effort level and a per-attempt spend ceiling, and the run timeline (HTTP, `aw run timeline`, UI)
  shows what each attempt and the run reported using. (The first write-up said spend was not recorded; it was recorded
  as `USAGE_REPORTED` but never shown.)
- **Fixed after the V9 verdict by V9-13b:** F3 — the model did not act on the check's feedback in the first rework
  build (2–3 rounds). The v2 check-failure line now says the check is authoritative; two live runs after it needed one
  round (two runs, one model: direction, not statistics).
- **Codex was not run:** its compatibility remains `UNVERIFIED`.

What the product does provide as a safety net: `aw` probes the real executable (`--version`, protocol/capability
identity) when an operator registers an adapter build, re-probes it at admission, and refuses to run a node whose build
no longer matches the pinned one ([providers and isolation](../operator/05-providers-and-isolation.md)).

## Install artifacts and checksums

`aw-release-build` embeds the built UI, compiles, checksums and writes a release manifest; CI builds it twice and
requires identical checksums and manifests (outside `generatedAt`), then smoke-tests `aw doctor` and `aw serve`
serving the embedded UI from a fresh directory. For the assessed commit:

| Platform | File | SHA-256 | Built twice, identical |
|---|---|---|---|
| Windows | `aw.exe` | `961c11289554cd7fd0564be713444292191ca70e8a59750af6908127d6bb05e8` | yes |
| Linux | `aw` | `f1269112587740207128ab682d34bc3aa2fa05823b9802fb2a868c1940f5b053` | yes |

Source: CI run 36803384120, jobs `release build (windows-latest)` and `release build (ubuntu-latest)` — each built the
binary twice with identical checksums. The same run uploaded the binaries as the `aw-release-windows-latest` and
`aw-release-ubuntu-latest` artifacts (binary, `.sha256` and manifest, kept 90 days); both were downloaded and
re-hashed and match the table. Checksums are per commit (the binary records its build revision), so a later commit has
different ones.

## Fresh-install smoke

The release-build job's smoke step is the fresh-install smoke: it runs the freshly built binary against a brand-new
database, artifact root and workspace root — `aw doctor` must report HEALTHY, and `aw serve` must answer `/` with the
embedded UI's own bootstrap script — plus `aw version --json` must report the embedded UI and the expected schema
version. It passed on both platforms for the assessed commit. The full operator walkthrough (register a repository,
publish a workflow, run it to `SUCCEEDED`) was verified by hand against a real binary in V8-09
([quickstart](../operator/01-quickstart.md)); it is not re-run by CI.

## V9 addendum (harness alignment)

**Verdict V9: `V9_DONE`** — assessed on commit `e711581` (master after #160) by CI run
[37138162978](https://github.com/draculemihawk123-ai/aw-tqunglnh/actions/runs/37138162978) (`v8-alpha-gate`, enforcing, `gatePass = true`: 209/209 Alpha-gated criteria, 23/23 journeys,
9/9 version gates, 7/7 final gates, V8-01 golden workload unchanged). The record is [`v9-verdict.json`](v9-verdict.json);
the per-gap evidence (G1–G10, each with a test that reproduces the original failure mode) is in
[`../harness-engineering/15-doi-chieu-v9.md`](../harness-engineering/15-doi-chieu-v9.md). The `ALPHA_READY` verdict above
is unchanged; V9 adds to it and does not replace it. Live provider compatibility is `PARTIAL` (see its section): Claude
ran for real, Codex did not. (F3 and F4 were fixed after the verdict by V9-13b and V9-13a.)

## Where to go next

- Operator documentation: [`../operator/00-start-here.md`](../operator/00-start-here.md).
- How the verdict is computed and the evidence behind it: `baocaov8checklist.md` (V8-11, V8-12, V8-12R-01, V8-12R-02)
  and `docs/design/10-v8-alpha-hardening.md`.
- Re-running the assessment locally: `go run ./cmd/v8-alpha-gate --repo-root . --out alpha-assessment.json` (CI supplies
  the suite results; without them every suite reads as missing evidence).
