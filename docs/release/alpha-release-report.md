# Alpha release report

**Verdict: `REWORK`** — `gatePass = false`.

The machine-readable record of this verdict is [`alpha-verdict.json`](alpha-verdict.json); this page is its
readable form, and a test keeps the two (and [`../00-start-here.md`](../00-start-here.md)) from disagreeing.

## What was assessed

| | |
|---|---|
| Commit | `6d3bab4f2568005186fc7edf5ed6655859096083` (master after V8-11) |
| Assessment | CI run [36786340396](https://github.com/draculemihawk123-ai/aw-tqunglnh/actions/runs/36786340396), artifact `v8-alpha-assessment` |
| Tool | `cmd/v8-alpha-gate` (`internal/alphagate`), V8-11 |
| Rule | `ALPHA_READY` only when `gatePass = true`; any other verdict names its blocker and the next narrow task (`docs/design/10-v8-alpha-hardening.md`, V8-12) |

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
| Final gates | **6 / 7** — cancel-vs-claim in both commit orders, route inventory = OpenAPI both directions, recovery-command owners (core/API/UI/CLI), SourceRef debt 0, `git diff --check`, `go test`/`go vet` on both platforms all pass |

## The blocker

**`parity-inventory-has-zero-debt` fails.** `internal/delivery/parity/ledger.go` still pins 15 debt entries that
V6-15O acknowledged but its own "no new leaf/route" rule forbade closing:

- 13 HTTP read operations with no `aw` mirror: `getEvidence`, `listArtifacts`, `getMessageContent`,
  `getMessageContextSnapshot`, `getReleaseSetLocalCommitStatus`, `getRepositoryWorkspaceState`,
  `getScopeExpansionRequest`, `listFamilyScopeExpansionRequests`, `getTaskFamily`, `listChildWorkItems`,
  `listWorkItemKanban`, `getWorkItemProjectedDetail`, `repositoriesGet`.
- 2 projection reads (`listWorkItemKanban`, `getWorkItemProjectedDetail` — two of the 13 above, so 13 distinct
  operations need work) that the HTTP layer answers straight from the projection port with no public application
  operation behind them (ADR-028 wants one).

Nothing in the product misbehaves because of this; it is a parity-completeness gap the Alpha gate explicitly requires
closed.

## Next narrow task

**V8-12R-01 — close the parity ledger and re-run the Alpha gate.** Add the 13 `aw` read leaves, give the 2 projection
reads an application operation, empty `Ledger()`. `TestParityLedgerIsEmpty` and the `v8-alpha-gate` job then report
`gatePass = true` on a fresh commit, a new verdict record is written for that commit, and (only then) the gate job is
switched to enforcing. No Beta work is part of it.

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

**Status: UNVERIFIED.** Every provider test — the conformance matrix, the admission/drift/isolation scenarios, the full
journeys — runs against the repository's own `fake-claude` and `fake-codex` wire-protocol stand-ins. No run against a
real Claude or Codex CLI exists, so compatibility with any real CLI build is **not** claimed. What the product does
provide is the safety net for that gap: `aw` probes the real executable (`--version`, protocol/capability identity) when
an operator registers an adapter build, re-probes it at admission, and refuses to run a node whose build no longer
matches the pinned one ([providers and isolation](../operator/05-providers-and-isolation.md)).

## Install artifacts and checksums

`aw-release-build` embeds the built UI, compiles, checksums and writes a release manifest; CI builds it twice and
requires identical checksums and manifests (outside `generatedAt`), then smoke-tests `aw doctor` and `aw serve`
serving the embedded UI from a fresh directory. For the assessed commit:

| Platform | File | SHA-256 | Built twice, identical |
|---|---|---|---|
| Windows | `aw.exe` | `dbce424dae5ba30c53fee3c8118915a267ef2538afdcf79efa239c48262d8130` | yes |
| Linux | `aw` | `3b71b34e5e9c02092dbdb683e57edbf2b4197b9924a2623ce1929ce4258b2e72` | yes |

Source: CI run 36786340396, jobs `release build (windows-latest)` and `release build (ubuntu-latest)`. Checksums are
per commit (the binary records its build revision), so a later commit has different ones. The job did not upload the
binaries for this commit; from the next push to master it uploads them as the `aw-release-windows-latest` and
`aw-release-ubuntu-latest` artifacts together with their `.sha256` file and manifest.

## Fresh-install smoke

The release-build job's smoke step is the fresh-install smoke: it runs the freshly built binary against a brand-new
database, artifact root and workspace root — `aw doctor` must report HEALTHY, and `aw serve` must answer `/` with the
embedded UI's own bootstrap script — plus `aw version --json` must report the embedded UI and the expected schema
version. It passed on both platforms for the assessed commit. The full operator walkthrough (register a repository,
publish a workflow, run it to `SUCCEEDED`) was verified by hand against a real binary in V8-09
([quickstart](../operator/01-quickstart.md)); it is not re-run by CI.

## Where to go next

- Operator documentation: [`../operator/00-start-here.md`](../operator/00-start-here.md).
- How the verdict is computed and the evidence behind it: `baocaov8checklist.md` (V8-11, V8-12) and
  `docs/design/10-v8-alpha-hardening.md`.
- Re-running the assessment locally: `go run ./cmd/v8-alpha-gate --repo-root . --out alpha-assessment.json` (CI supplies
  the suite results; without them every suite reads as missing evidence).
