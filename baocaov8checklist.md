# V8 checklist — Alpha hardening and release verdict

> V8 verification log follows the same discipline as V0-V7 (see `baocaov7checklist.md` for the full
> history before it). Written in English throughout, per the V7-era decision to keep all checklists from
> that point on in English. Full narrative per task: decision, reasoning, self-found questions and real
> verify output.

## V8-01 — Golden workload and trace completeness

### Context

V7 closed fully at V7-17C (PR #121, `c98c6b0`), and `docs/design/10-v8-alpha-hardening.md` gates V8-01 as
the mandatory first V8 task: fix a deterministic multi-repo/graph/provider/gate workload as a regression
baseline (HE-11-S03: "Project SHOULD have a golden workload/journey to compare trace and failure across
harness versions"), with real assertions over trace IDs, event sequence, and revision/evidence links
(AK-ARCH-021: "Evidence must be traceable WorkItem → Run → NodeRun → Attempt → invocation → artifact →
exact repository revision"). Every later V8 task that names V8-01 as its own dependency (V8-02's
fault-injection matrix, V8-03's concurrency soak, V8-07's performance budgets) implies this workload must
be a real, reusable fixture, not a one-off script.

`internal/integration/v6accept` already runs a real, proven, HTTP-driven acceptance journey (V6-14) against
real `aw serve`/`aw worker` processes with a real fake-provider executable — but it is single-repository,
single-provider (`internal/integration/v6accept/journey_test.go` only ever registers one repository and
one provider, `claude`). It does, however, already contain everything else V8-01 needs: `newStack`'s own
real-process harness, `apiClient`'s real HTTP client, and `definitions_test.go`'s own proven
START→AGENT(maker)→COMMAND→MACHINE_GATE→AGENT(checker)→END graph builder (`publishVerificationWorkflow`),
already exercising a real MACHINE_GATE and real Evidence chain (`stage_evidence_test.go`).

### Decision

Add the golden workload as a NEW test file inside `internal/integration/v6accept` (not a new package)
rather than duplicating ~500 lines of already-proven stack/apiClient/definition-publishing machinery. This
keeps V8-01 fully isolated from the existing, already-locked V6-14 journey and its 20+ stage files — zero
risk of regressing V6 — while reusing nearly all of the real-process harness. `binaries`/`builtBinaries`
(`build_test.go`) gained a `fakeCodex` field, built unconditionally alongside `fakeClaude`: one more small
`go build` is cheap next to the real process/HTTP journeys this package already runs, and every existing
caller that never references it is unaffected. `stack` (`stack_test.go`) gained an optional
`codexExecutable` field, wired into `startServe`/`startWorker`'s own `--codex-executable` flag only when
non-empty — every existing test leaves it `""` and gets today's claude-only behavior unchanged.

The golden workload itself (`golden_workload_test.go`) is a fixed, deterministic graph exercising all four
dimensions the design doc's own Mục tiêu line names: two real local git repositories (multi-repo), a
five-node graph START→AGENT(maker, claude)→COMMAND→MACHINE_GATE→AGENT(checker, codex)→END (graph + gate),
with the maker and checker AGENT nodes pinned to two real, distinct, separately-registered AdapterBuilds
(claude and codex — multi-provider). The graph itself is adapted directly from
`definitions_test.go`'s own proven `publishVerificationWorkflow` shape (same marker-file maker/gate script
pattern, same policy/skill/command/gate document literals) rather than invented from scratch, since that
shape is already known to execute correctly end-to-end.

"Trace completeness" (`trace_completeness_test.go`) is defined concretely, in two independent halves,
rather than as a vague aspiration:

1. **AK-ARCH-021's own evidence chain** — checked entirely through the real public evidence routes V7-14's
   own EvidenceTab already uses (`GET .../evidence`, `.../evidence/{id}`, `.../evidence/{id}/artifacts`,
   `.../artifacts/{id}/content`). `runtime.EvidenceDetail` already carries every identity that chain names
   (WorkItemID/RunID/NodeRunID/AttemptID) plus a per-repository `RevisionView` (the "exact repository
   revision" tail) — no internal package needed touching for this half. The checker verifies: every
   Evidence row's WorkItemID/RunID match, NodeRunID/AttemptID are non-empty (the "invocation" link), the
   single-item GET agrees with the list entry (a second, independent read of the identical chain), every
   repository this workload spans has a non-empty revision entry, and every referenced artifact is
   genuinely downloadable with the size the API itself reported.
2. **HE-11-S03's own canonical event envelope** — checked via `sqlite.Store.ListDomainEventsForProject`
   (the same raw domain_events introspection V4-14's own `runtimeengine_test.go` already established as
   this repository's precedent for a test reading back its own causal event log), opened read-only AFTER
   the real serve/worker processes have cleanly stopped. Checks: `journal_position` never repeats and only
   increases (matches `allocateJournalPosition`'s own doc comment — database-monotonic, not necessarily
   gapless); every event's `correlation_id` is non-empty (HE-11's own minimal envelope); every non-empty
   `causation_id` points at a real event in the same project's journal (no dangling causation pointer);
   and, per (aggregate_type, aggregate_id), `Sequence` values never repeat and only increase as
   journal_position advances.

**A real, initially-wrong assumption was found and corrected while building this checker**: the first
version asserted a per-aggregate `Sequence` must start at 1 and increase by exactly 1 with no gap. Running
the real workload immediately produced 9 "violations" — every `NodeRun`/`ExecutionAttempt` aggregate's
first-ever logged event had `Sequence` 2 or 3, never 1. Reading the actual runtime code
(`internal/app/runtime/advance.go` and siblings) confirmed why: every real event-append call site sets
`Sequence` from that aggregate's own optimistic-concurrency `Version` field AS OF that event (e.g.
`Sequence: int64(completedNodeRun.Version)`), not a separate "N-th event for this aggregate" counter — and
not every version transition logs an event (a NodeRun's own PENDING→RUNNING transition logs nothing; only
its first real terminal/decision event does). "Gap-free starting at 1" was never something this domain's
own design promised, so asserting it would have made this checker itself the source of false failures on
every future real run. Fixed to the two invariants the domain's real optimistic-concurrency design actually
guarantees: no two events for the same aggregate ever claim the identical `Sequence` value, and `Sequence`
only ever increases as `journal_position` advances (an event for an older version can never be appended
after a newer one already was).

**A second real bug was found live**: the workflow's own COMMAND node (`test_a`) failed every run with
`VALIDATION_FAILED` ("cwd repository target ... is not in this node run's effective scope") when the
workflow ran directly on the ROOT WorkItem. Reading `internal/app/runtime/schedule.go`'s own
`ListWorkItemEffectiveScopes` call confirmed the real cause: a workflow only ever runs on a CHILD WorkItem
— the root's own `work_item_effective_scopes` row is not what a NodeRun's execution resolves
`EffectiveScope` against. `journey_test.go`'s own `workItemAndRun`/`createChild` already established this
exact pattern (create a root for the family, then a real child carrying its own `effectiveScope` + contract
+ pinned workflow version, and run on the child) — the golden workload's own first draft had simply skipped
that step, assuming (wrongly) that a root-level `initialScope` was sufficient on its own. Fixed by adding
the same real child-creation call before marking ready/starting the run.

### Execution

- `internal/integration/v6accept/build_test.go`: `binaries.fakeCodex` field + build target.
- `internal/integration/v6accept/stack_test.go`: `stack.codexExecutable` optional field, wired into
  `startServe`/`startWorker`'s own `--codex-executable` flag.
- `internal/integration/v6accept/golden_workload_test.go` (new): `TestV8GoldenWorkload_TraceCompleteness`
  — registers two real adapter builds (claude, codex) via the real probe→register HTTP flow; creates a
  project and two real local git repositories; publishes context/attempt/permission policies, two agent
  profiles (one per provider), a skill with maker/gate scripts, two commands, a MACHINE_GATE, a completion
  policy, and the five-node workflow graph; creates a root WorkItem scoped to both repositories, a child
  WorkItem carrying the readiness contract, marks it READY, starts a real Run, and waits for real terminal
  `SUCCEEDED`; runs the evidence-chain check while serve is still up, stops the stack gracefully, then runs
  the domain-event journal check; logs the JSON report always (`t.Logf`) and optionally writes it to
  `AW_V8_GOLDEN_REPORT_PATH` when set; fails with every violation listed if the trace is not genuinely
  complete.
- `internal/integration/v6accept/trace_completeness_test.go` (new): `traceCompletenessReport`,
  `verifyEvidenceChain`, `verifyDomainEventJournal` — the two-half checker described above.
- No new CI job: this test lives in the same package and is gated by the same `AW_HTTP_ACCEPTANCE=1`
  opt-in `requireAcceptance(t)` every other file in this package already uses, so it runs automatically
  inside the EXISTING `v6 acceptance` CI job on both platforms with zero workflow changes — matching V8-01's
  own "Hoàn thành khi: workload chạy clean checkout không cần secret/network" exactly (a default
  `go test ./...` compiles and skips it, same as every other file here).

### Verify

- `go build ./...`, `go vet ./...`: clean.
- `go test -count=1 ./...` (full offline suite, no acceptance opt-in): clean, zero failures across every
  package — confirms the additive `binaries`/`stack` changes did not disturb anything.
- `AW_HTTP_ACCEPTANCE=1 go test -run '^TestV6HTTPAcceptance_CleanDatabaseJourney$' ./internal/integration/v6accept/...`:
  clean — confirms the existing V6-14 journey is unaffected by this task's additive changes to shared
  package infrastructure.
- `AW_HTTP_ACCEPTANCE=1 go test -count=1 -run '^TestV8GoldenWorkload_TraceCompleteness$' ./internal/integration/v6accept/...`:
  passes cleanly and repeatably (3 consecutive fresh runs, ~15s each) — real run reaches terminal
  `SUCCEEDED` across a real 5-node graph spanning 2 repositories and 2 distinct real providers; trace
  completeness report shows `evidenceRowsChecked: 2` (COMMAND_EXECUTION + the gate's own evidence kind),
  `artifactsVerified: 2`, `domainEventsChecked: 23`, `aggregatesChecked: 17`, `violations: null`.

## V8-02 — Full fault-injection matrix Alpha

### Context

`docs/design/10-v8-alpha-hardening.md` V8-02 asks for the six standard crash-transaction boundaries
(GC-ACC-16, `docs/spikes/01-go-core-spike-plan.md` §9: `before_intent_job_commit`,
`after_job_commit_before_claim`, `after_job_claim_before_process_spawn`,
`after_checkpoint_before_process_exit`, `after_process_exit_before_outcome_commit`,
`after_outcome_commit_before_next_dispatch`) repeated "with the real serve/worker topology," plus artifact
failure, projection poison/interruption, periodic lease expiry and workspace corruption.

These six boundaries are NOT a new invention: `internal/spikeacceptance`'s own SPK-04 scenario
(`spk04_scenario.go`) already proves every one of them, using a real, standalone `cmd/spike-worker`
process (hard-killed at each exact fault point via `internal/adapters/sqlite/crashworker.go` — shared,
non-test production code, deliberately exported so a caller outside that package's own tests can seed and
recognize the identical fixtures) and real, exported seed helpers
(`SeedCrashResumeOwners`/`SeedCrashCheckpointNodeRunAndAttempt`/etc. in
`internal/adapters/sqlite/crashworker_fixtures.go`). What SPK-04 does NOT prove: whether the PRODUCTION
`aw worker` binary's own real, continuously-polling reaper/scheduler loop actually discovers and reclaims
each crashed job on its own — SPK-04's own recovery half manually calls `store.RecoverExpiredJobs`/
`store.ClaimJob` directly, never starting a real `aw worker` process at all.

### Decision

New package `internal/integration/v8fault` (not folded into `v6accept`, since it shares none of that
package's HTTP/apiClient/journey machinery — its own dependency is `internal/adapters/sqlite`'s exported
crash fixtures plus a real `aw`/`cmd/spike-worker` binary pair): for each of the six boundaries (seven
tests — boundary 5 has a read-only and a mutating variant, matching SPK-04's own split), arrange/seed is
copied structurally from `spk04_scenario.go` (same fixture IDs, same seed calls — that sequence is already
proven correct, nothing new to invent there), the crash injection reuses the exact same
`spawnAndHardKillSpikeWorker` mechanism (copied rather than imported: SPK-04's own scenario functions are
unexported), and then — the actual new value — a real `aw worker` process is started against the crashed
database and left to reclaim the stale job through its own real polling loop, verified either by watching
`durable_jobs.claim_count` increase past what the crashed worker itself set (via the debug-only
`Store.DebugListJobsByKind` projection) or, for boundary 1 (nothing was ever committed) and boundary 6
(the outcome was already durably committed before the crash), by confirming the real worker caused no
change/no duplicate. The worker is deliberately started with no `--claude-executable`/`--codex-executable`
— every crash fixture pins a fake AgentProfile that never resolves to a real published definition (SPK-04's
own established convention), so this test only ever needs the job-lease reaper layer, never real dispatch.

**A real, initially-wrong assumption about the real worker's own timing was found and fixed**: the first
version of boundaries 3/4/5 all timed out after 15s waiting for a reclaim that never happened, while
boundaries 1/2 (which never need a stale-lease RECOVERY, only a first claim) passed immediately. Reading
`internal/app/workerpool/pool.go` found the real cause: `Pool.Run`'s periodic `RecoverExpiredJobs` scan
(the ONLY thing that turns a stale `LEASED` job back to `AVAILABLE` — a startup scan plus a periodic one) has
its own interval, `workerpool.Config.RecoveryInterval`, with NO dedicated CLI flag of its own;
`cmd/aw/worker.go`'s own composition never sets it, so `workerpool.Config.Validate` silently defaults it to
`LeaseTTL` itself — the production default is far above a 15s test budget. `--reaper-interval` (which the
first draft had set to a short value, expecting it to control this) is a COMPLETELY different reaper
(`runtime.RecoveryReaperJobKind`, orphaned attempts/stranded cancellation intents) that never touches
`durable_jobs` lease recovery at all. Fixed by passing a short `--lease-ttl` (1s) to the real worker
(with `--lease-heartbeat` lowered to 300ms alongside it, since `workerpool.Config.Validate` rejects a
heartbeat that does not stay below the lease TTL) — this only governs the REAL worker's own periodic
recovery-scan cadence and its own future claims, entirely independent of the crashed worker's own short,
separately-configured lease TTL (`faultCrashedWorkerTTL`, still 900ms, matching SPK-04's own
`spk04CrashedWorkerTTL`) that made the job reclaimable in the first place.

**A second, smaller issue was found in boundary 6's own verification**: it originally called
`store.ClaimJob` itself to confirm the downstream job existed post-recovery — racing the real worker this
test had just started, which is itself genuinely polling and eligible to claim that exact job. Fixed to
read the job's own row via `DebugListJobsByKind` instead (asserting it appears exactly once), which proves
"never duplicated" without competing with the very process under test for the same claim.

**Scoping decision, stated explicitly rather than silently under-delivered**: this task closes V8-02's own
primary, concretely-specified deliverable (GC-ACC-16's "all six crash boundaries," now proven against the
real worker topology). The other four named categories are NOT built fresh here, since real, evidenced
coverage already exists elsewhere and re-deriving it would be pure duplication: projection poison
(`internal/integration/v6accept/stage_fault_poison_test.go`, `TestV6HTTPAcceptance_Fault_PoisonProjection`)
and projection interruption (`stage_fault_projection_during_test.go`/`stage_fault_projection_after_test.go`)
are already proven against the real serve/worker topology by V6-14A; artifact failure has a real crash
scenario in `stage_fault_attachment_test.go`
(`TestV6HTTPAcceptance_Fault_CrashAfterAttachmentPut`) though not yet a genuinely corrupted/missing-artifact
scenario; workspace corruption has real spike-level evidence (SPK-09's own quarantine/recreate proof) but
not yet against the real HTTP-visible topology; periodic lease expiry (a natural, no-crash-involved
expiry/reclaim, as opposed to every scenario above which is crash-triggered) has no dedicated real-topology
test yet. These three gaps (artifact corruption, workspace corruption at the real-topology level, and
genuine periodic lease expiry with no crash) are real, acknowledged, and worth a fast, narrowly-scoped
follow-up — not silently declared "done" here.

### Execution

- `internal/integration/v8fault/build_test.go` (new): builds `cmd/aw` + `cmd/spike-worker` once per test
  binary run, gated behind the same `AW_HTTP_ACCEPTANCE=1` opt-in every real-process suite in this repo
  already uses.
- `internal/integration/v8fault/harness_test.go` (new): `spawnAndHardKillSpikeWorker` (copied from
  `spk04_scenario.go`, same real crash-injection mechanism), `startRealWorker`/`realWorker.stop` (a real
  `aw worker` child process with a short `--lease-ttl`/`--lease-heartbeat` for a fast real recovery-scan
  cadence), `waitFor` (mirrors `v6accept`'s own helper of the same name).
- `internal/integration/v8fault/fault_matrix_test.go` (new): `arrangeFault`/`faultFixture` (seeding copied
  structurally from `spk04_scenario.go`'s own `spk04Arrange`), `waitForRealWorkerReclaim`, and the seven
  boundary tests (`TestV8Fault_Boundary1..6`, boundary 5 split read-only/mutating).
- `.github/workflows/spike-gate.yml`: new `v8-fault-matrix` job (`needs: contract`, both platforms,
  self-contained like `spike-acceptance`/`v6-acceptance` — the package builds its own binaries, the job
  just sets `AW_HTTP_ACCEPTANCE=1` and runs `go test`).

### Verify

- `go build ./...`, `go vet ./...`: clean.
- `go test -count=1 ./...` (full offline suite, no acceptance opt-in): clean, zero failures — `v8fault`
  itself skips (opt-in) in 0.6s.
- `AW_HTTP_ACCEPTANCE=1 go test -count=1 -v ./internal/integration/v8fault/...`: all seven boundary tests
  pass, confirmed stable across 4 consecutive fresh local runs (~13s each): `TestV8Fault_Boundary1_
  BeforeIntentJobCommit`, `_Boundary2_AfterJobCommitBeforeClaim`, `_Boundary3_
  AfterJobClaimBeforeProcessSpawn`, `_Boundary4_AfterCheckpointBeforeProcessExit`, `_Boundary5_
  AfterProcessExitBeforeOutcomeCommit_ReadOnly`, `_Boundary5_..._Mutating`, `_Boundary6_
  AfterOutcomeCommitBeforeNextDispatch`.

## V8-03 — Concurrency/race/stability soak

### Context

`docs/design/10-v8-alpha-hardening.md` V8-03 asks for multiple root families/workers running
concurrently while the lease/workspace/projection invariant (AK-ARCH-009: "an old lease/fencing token
cannot commit after expiry or reassignment") stays correct — a bounded parallel workload, same/different
repository, duplicate commands/events, 10 runs, `go test -race`, and "stale fence success == 0."

The existing concurrency coverage in this repo is real but narrower than this: `internal/integration/
v6accept/stage_fault_concurrency_test.go` proves idempotent dedup for ONE family's own duplicate command
(sequentially replayed, not truly racing), and `internal/spikeacceptance`'s own SPK-08 proves the
write-lease race invariant with 100 real concurrent iterations — but at the isolated spike/adapter level,
never against multiple real, independently-running root WorkItem families sharing a real `aw serve`+`aw
worker` topology the way V8-01/V8-02 both established as this phase's own standard.

### Decision

New test in `internal/integration/v6accept` (same package as V8-01/V8-02's own real-process harness,
reusing `newStack`/`apiClient`/`registerGoldenRepository`/`publishGolden` verbatim):
`TestV8ConcurrencySoak_MultiFamilyRaceInvariants` drives FOUR real root WorkItem families concurrently —
two (`family-a`/`family-b`) scoped to the SAME repository (`soak-repo-shared`, real contention for that
repository's own write lease) and two (`family-c`/`family-d`) each on their OWN distinct repository (free
to run independently) — and for EACH family, `soakDuplicateSubmissions` (5) goroutines fire the real
`POST .../runs` start-run command with the IDENTICAL Idempotency-Key simultaneously (the "duplicate
commands/events" half of V8-03's own Thực hiện line, genuinely racing rather than sequentially replayed).

AK-ARCH-009's own two halves are checked as two separate, decisive assertions: (1) all
`soakDuplicateSubmissions` concurrent identically-keyed calls must resolve to the SAME `runId` — real
idempotent dedup under genuine concurrent load; (2) after every family's run settles, its own real timeline
(`GET /runs/{id}/timeline`) must show EXACTLY one `NODE_RUN` and one `EXECUTION_ATTEMPT` reaching terminal
`SUCCEEDED` for its single node — never two, which is what a stale/fenced token being allowed to also
commit would look like. This is "stale fence success == 0" made concrete and checkable, not a vague
aspiration.

**A real graph-design finding, made twice while building this**: the soak's own workflow graph needed to be
usable, UNMODIFIED, across all four families despite them spanning three DIFFERENT repositories — ruling
out an AGENT or COMMAND node (`CwdRepositoryTarget`/a real provider profile would each need to be pinned to
ONE specific repository at publish time, per V8-01's own established finding). A single `START ->
MACHINE_GATE -> END` graph solves this cleanly: `GateNodeExecutor` always runs its own command against a
fresh SCRATCH directory, never a real repository workspace mount, so the SAME published workflow version
genuinely works regardless of which repository a given family happens to be scoped to. The first draft also
omitted a `CompletionPolicyRef` entirely (assuming a graph with no evidence requirement needed none) and
every run settled `FAILED` — `resolveCompletionPolicy`'s own nil-ref case resolves to
`ReasonNoCompletionPolicyPinned`/FAIL (the same finding V7-17C already made for a different reason). An
EMPTY `CompletionRules{}` was tried next and rejected AT PUBLISH TIME ("neither requiredEvidenceKinds nor
requiredAssurance is declared... a completion policy that requires nothing can never distinguish NOT_RUN
from a real pass," GC-INV-12/13) — a completion policy must declare at least one real requirement. Fixed by
giving the MACHINE_GATE its own always-PASS gate script (mirroring `definitions_test.go`'s own proven gate
pattern) and requiring exactly that one evidence kind.

### Execution

- `internal/integration/v6accept/concurrency_soak_test.go` (new):
  `TestV8ConcurrencySoak_MultiFamilyRaceInvariants`, `driveSoakFamily` (per-family end-to-end: root+child
  creation, mark-ready, the duplicate-command race, wait-terminal), `verifySoakNoStaleFenceSuccess` (the
  real-timeline check described above), `publishSoakWorkflow`/`soakGateScript` (the single reusable
  MACHINE_GATE-only graph).
- `.github/workflows/spike-gate.yml`: two new jobs — `v8-concurrency-soak-race` (ubuntu-latest only,
  matching `linux-race-and-stability` (V0-12)'s own established platform choice for `-race`: 10 repeats of
  this one test with the race detector, aggregated JSON report) and `v8-concurrency-soak-windows`
  (windows-latest, the same 10 repeats without `-race`, for the leak/flake half of V8-03's own Verify line
  — "leak/flake report Windows/Linux").

### Verify

- `go build ./...`, `go vet ./...`: clean.
- `go test -count=1 ./...` (full offline suite, no acceptance opt-in): clean.
- `AW_HTTP_ACCEPTANCE=1 go test -count=1 -run '^TestV8ConcurrencySoak_MultiFamilyRaceInvariants$'
  ./internal/integration/v6accept/...`: passes cleanly and repeatably — 5 consecutive fresh local runs
  (14-17s each), zero stale-fence-success across every run.
- `AW_HTTP_ACCEPTANCE=1 go test -count=1 -run '^TestV6HTTPAcceptance_CleanDatabaseJourney$'
  ./internal/integration/v6accept/...` and the whole package with the acceptance opt-in
  (`AW_HTTP_ACCEPTANCE=1 go test -count=1 ./internal/integration/v6accept/...`, 268s): both clean — confirms
  the new soak test coexists correctly with V6-14's own journey and V8-01/V8-02's own tests in the same
  package.
- `-race` could not be exercised locally (this dev machine has no C compiler for CGO); left to the new
  `v8-concurrency-soak-race` CI job, matching this repo's own established `-race`-on-Linux-only convention.

## V8-04A — Filesystem and path abuse suite

### Context

`docs/design/10-v8-alpha-hardening.md` V8-04A (Nguồn: AK-ARCH-027) asks for a filesystem/path-boundary
negative suite on both OS covering 5 scenarios: (1) traversal, (2) symlink/reparse escape, (3) a repository
registered UNDER the workspace root, (4) artifact/workspace-root containment, (5) a cleanup/sweep operation
only ever touching paths it owns — completion bar: "mọi từ chối có safe diagnostic và evidence."

Research before writing any code found that MOST of the underlying containment is already real, existing
production code, already unit-tested at the package level:
- `internal/adapters/gitworktree/provider.go`: `ensureLexicallyWithin`/`isWithin` (lexical containment via
  `filepath.Rel`), `canonicalExistingDirectory`/`canonicalExistingPath` (real cross-platform symlink/reparse
  resolution — `filepath.EvalSymlinks` non-Windows, a real open-handle `GetFinalPathNameByHandle` call on
  Windows), `validateManagedDirectory` (symlink rejection + canonical containment for the workspace side),
  `validateLocalRepository` (the scenario-3 bidirectional `isWithin(root, repo) || isWithin(repo, root)`
  check, `ErrUnsafePath`).
- `TestProviderRejectsRepositoryInsideManagedRoot` (`internal/adapters/gitworktree/provider_test.go`) already
  unit-tests scenario 3 directly against `Provider.Provision`.
- `internal/adapters/artifactstore/filesystem.go`'s `locatorPattern` (`^sha256:[0-9a-f]{64}$`) validates
  `ref.Locator` before ever building a filesystem path — a well-formed hex digest structurally cannot contain
  `..`, `/`, `\`, or a NUL byte, so this is airtight by construction, not merely tested; already unit-tested
  twice (`TestPut/Open/Delete_MalformedLocator_RejectedBeforeTouchingFilesystem`).
- `internal/app/artifactsweep/sweep.go` never builds or touches a filesystem path itself — its own delete
  step (line 391) calls `deps.Store.Delete(ctx, ref)`, the SAME `ports.ArtifactStore` port whose filesystem
  implementation enforces the `locatorPattern` check above. Scenario 5's artifact half is therefore already
  structurally guaranteed by scenario 4's own containment, with no separate code path to test.
- `Provider.Release` (provider.go) never calls a raw `os.RemoveAll` on caller-influenced input either: it
  resolves its own target via `p.workspacePath` (itself validated through
  `ensureLexicallyWithin(p.worktreesRoot, ...)`), then removes content only via a real `git worktree remove`.

### Decision

Given this existing coverage, duplicating it at the unit level would add nothing. Following V8-01/V8-02/
V8-03's own established precedent this session (real `aw serve`+`aw worker` topology via
`internal/integration/v6accept`'s own harness, not just isolated package tests), V8-04A's real, new value is
proving the SAME containment surfaces correctly through the full real public HTTP surface end to end — with
a safe, non-path-leaking diagnostic as real evidence — for the two scenarios where a genuinely new attack
surface exists at that layer:

- **Scenario 3 (repo nested under workspace root)**: `RegisterRepository`
  (`internal/app/catalog/commands.go`) does ZERO path validation at registration time — confirmed by reading
  it directly. The exact string an operator supplies as `remoteLocator` flows straight through, unvalidated,
  into `ports.ProvisionSpec.LocalRepository` only later, when a child WorkItem's own `effectiveScope` first
  triggers real workspace `Provision` (confirmed via `LocalRepository: repo.RemoteLocator` at
  `internal/app/workspaceprovision/handler.go:195`). `repoprobe.Prober` has no `--workspace-root` awareness
  at all (identity/dedup only), so a malicious path probes ACTIVE exactly like any other real repository —
  the real end-to-end attack surface is registration-time-unvalidated, provision-time-rejected.
- **Scenario 2 (symlink/reparse escape), the strictly stronger real-topology case**: a repository path whose
  raw, LEXICAL string is completely unrelated to `--workspace-root` (so a naive `strings.HasPrefix` check
  would wave it through) but is a real symlink resolving, canonically, to a location INSIDE the workspace
  root — proving `validateLocalRepository`'s own containment check runs against the CANONICAL path end to
  end, not the caller-supplied one, through the real HTTP surface.
- **Scenario 5 (cleanup only touches owned paths)**: NOT an HTTP-level test — releasing a WorkspaceSet
  through the real public API requires a real, sealed-or-abandoned ReleaseSet first
  (`ports.ReleaseEligibilityAuthority`, GC-INV-26), confirmed live when this suite's own first attempt at an
  HTTP-level release test was correctly rejected with `403 FORBIDDEN` ("release is not authorized until this
  family's release set is sealed or abandoned"). Reproducing journey_test.go's own full release-set-seal
  chain just to reach a releasable WorkspaceSet would dwarf this suite's own scope for no added containment
  coverage. Instead: a direct, real-fixture test against `gitworktree.Provider` itself (real git, real
  filesystem, the exact same Provision/Release code the two HTTP-level tests above already exercise) —
  planting a bystander directory as a sibling of a real provisioned worktree, inside the SAME managed
  `worktreesRoot`, and confirming Release leaves it byte-for-byte untouched.
- Scenario 1 (plain `..` traversal) and scenario 4 (artifact-root containment) are deliberately NOT
  duplicated: both are already airtight (`ensureLexicallyWithin`'s `filepath.Rel`-based rejection;
  `locatorPattern`'s structural regex) and already unit-tested; there is no real-topology surface that would
  exercise either one differently — an artifact `Locator` is never caller-supplied free text in the real
  product, always the digest the store itself computed on `Put`.

### Execution

- `internal/integration/v6accept/filesystem_path_abuse_test.go` (new): `pathAbuseFixture` (a minimal project
  + one repository, registered through the real HTTP surface — this suite never needs a workflow, run, or
  command/agent definition at all, since real `Provision` runs as soon as a child WorkItem's own
  `effectiveScope` names the repository, well before any run could start), `registerRepositoryAt`,
  `createFamilyScopedTo`, `requireSafeProvisionFailure` (asserts the real WorkspaceSet is BLOCKED, the one
  abusive RepositoryWorkspace is FAILED with the real, short, typed `PROVISION_FAILED`
  `LastProvisionErrorCode`, and the raw attacker-controlled filesystem path never appears anywhere in the
  response body — the "safe diagnostic" bar made concrete).
  - `TestV8PathAbuse_RepositoryNestedUnderWorkspaceRoot_Rejected` (scenario 3).
  - `TestV8PathAbuse_SymlinkDisguisedNestedRepository_Rejected` (scenario 2) — follows this repo's own
    established convention (`internal/adapters/repoprobe/prober_test.go`'s identical symlink test) of
    treating "`os.Symlink` unavailable outside Developer Mode/admin" as a Windows environment limitation to
    skip past, not a product bug.
- `internal/adapters/gitworktree/provider_test.go` (extended):
  `TestProviderRelease_BystanderSiblingUnderManagedRootSurvives` (scenario 5) — real `Provision`+`Release`
  against a real git repository, with a bystander directory/file planted directly inside
  `provider.worktreesRoot` before release.
- No CI wiring needed: both new tests land inside packages the existing `contract` (`go test ./...`, covers
  `internal/adapters/gitworktree`) and `v6-acceptance` (`AW_HTTP_ACCEPTANCE=1`, covers
  `internal/integration/v6accept`) jobs already run in full — the same "no new CI job" precedent V8-01 set.

**A real finding, useful for any future WorkspaceSet-provisioning-failure fixture**: a WorkspaceSet whose
required repository fails to provision moves to set-level state `BLOCKED`, never `"FAILED"` — there is no
`WorkspaceSetFailed` transition in the real code path (`internal/app/workspaceprovision/handler.go`'s own
`aggregateWorkspaceSet`); `workspace.WorkspaceSetFailed` exists as a domain constant but this handler never
produces it for a provisioning failure. Only the individual `RepositoryWorkspace` row itself reaches state
`"FAILED"` (with `LastProvisionErrorCode` set). The first draft of this suite waited for `"FAILED"` at the
set level and timed out for a full minute before this was found by reading `aggregateWorkspaceSet` directly.

### Verify

- `go build ./...`, `go vet ./...`: clean.
- `go test -count=1 ./...` (full offline suite, no acceptance opt-in): clean, all packages pass.
- `go test ./internal/adapters/gitworktree/... -timeout 5m`: clean (full package, including the new
  bystander test 3x in isolation beforehand — 1.6-1.9s each, all pass).
- `AW_HTTP_ACCEPTANCE=1 go test ./internal/integration/v6accept/... -run 'TestV8PathAbuse' -count=1 -timeout
  6m`: clean across 3 consecutive fresh local runs (~8-9s each) —
  `TestV8PathAbuse_RepositoryNestedUnderWorkspaceRoot_Rejected` PASS every run;
  `TestV8PathAbuse_SymlinkDisguisedNestedRepository_Rejected` SKIP every run on this dev machine
  (`os.Symlink` needs Developer Mode/admin here — the established, accepted convention for this exact
  situation), so its real pass/fail is left to CI's own windows-latest/ubuntu-latest legs.
- `AW_HTTP_ACCEPTANCE=1 go test ./internal/integration/v6accept/... -run
  'TestV6HTTPAcceptance_CleanDatabaseJourney' -timeout 6m`: clean — confirms the new suite coexists
  correctly with V6-14's own core journey in the same package.

## V8-04C — Local HTTP trust boundary suite

### Context

`docs/design/10-v8-alpha-hardening.md` V8-04C (ADR-016, AK-ARCH-025A) asks for a negative suite covering 6
scenarios: external bind, DNS-rebinding Host, foreign/missing Origin, missing/wrong session token, CORS
deny-by-default, content/MIME injection — completion bar: "mọi mutation không hợp lệ bị từ chối trước khi
chạm application command," verified by "assert token không xuất hiện trong URL/log/durable state."

Research before writing any code found this ENTIRE scope already closed by V6-13's own work
(`internal/delivery/httpapi/securitymatrix`, `internal/delivery/httpapi/security_test.go`) — more completely
than either V8-04A or V8-04B's own equivalent research found, and run against EVERY registered route (the
row set comes from `httpapi.RouteRegistry.Descriptors()` on a registry built by the real
`internal/delivery/httpcompose.ComposeRoutes`, never a hand-written list):

- **External bind**: `TestLoopbackOnlyBind` — `httpapi.NewServer` refuses to construct at all for
  `0.0.0.0`/`::`/any routable address/any non-loopback-resolving hostname/empty host, checked BEFORE
  `net.Listen` ever runs (`server.go`'s `validateLoopbackHost`), so a misconfigured host can never even
  momentarily listen on a routable interface. Positive control confirms `127.0.0.1`/`localhost` still work.
- **DNS-rebinding Host**: `TestTransportGuardMatrix_EveryRoute` sends `evil.example.com:80`,
  `localhost:1`, `127.0.0.1:1` as the `Host` header against every route and asserts `403` +
  `invalid_host` + no CORS headers leaked on the rejection. `TestHostOriginGuard_RejectsForeignHost`/
  `_RejectsHostnameMismatchEvenWhenBothLoopback`/`_AcceptsExactBoundHost` cover the same check at the unit
  level.
- **Foreign/missing Origin**: same matrix test sends `http://evil.example.com`, `https://<this server's own
  host>` (right host, wrong scheme — the same-Host DNS-rebinding-adjacent case), and the literal string
  `"null"` as `Origin` against every route, asserting `403` + `invalid_origin` + no CORS headers.
  `TestHostOriginGuard_MissingOriginAllowed`/`_AcceptsExactMatchingOrigin`/`_RejectsRightHostWrongPortOrigin`
  cover the same check at the unit level — missing Origin is explicitly ALLOWED (a non-browser client such
  as the `aw` CLI's own transport sends none), proven not to be a blanket requirement.
- **Missing/wrong session token**: the same matrix test's third case, for every MUTATING route, a missing or
  wrong `httpapi.SessionTokenHeader` is rejected with `invalid_session_token` before the route's own handler
  ever runs. `TestRequireSessionToken_MissingTokenRejectsMutation`/`_WrongTokenRejectsMutation`/
  `_CorrectTokenAllowsMutation`/`_SafeMethodNeedsNoToken` cover the same check at the unit level (safe
  methods never require the token at all).
- **CORS deny-by-default**: `TestCORSPreflightIsNeverAnswered` proves a real browser preflight (`OPTIONS` +
  `Origin` + `Access-Control-Request-Method`) from a foreign origin is rejected by the same Host/Origin
  guard, never answered with a permissive preflight response — deny-by-default is implemented by OMISSION
  (`security.go`'s `HostOriginGuard` never sets an `Access-Control-*` header on ANY outcome), and
  `assertNoCORSHeaders` checks this is true for literally every response in every other matrix case too.
  `TestCORS_NeverEmitsAccessControlAllowOriginHeader` covers the same check at the unit level.
- **Content/MIME injection**: `TestArtifactContentMediaHandling` proves script-capable content
  (`text/html`) is forced to `Content-Disposition: attachment` with `X-Content-Type-Options: nosniff` (never
  rendered inline from this origin), an allow-listed type keeps `inline`, an unsatisfiable `Range` is
  refused with `416`, an artifact only reachable through the Evidence row that actually references it is
  hidden (leakage-normalized `404`) for a cross-project probe, and no response header can be injected
  (CR/LF) through stored metadata. `TestOversizedBodyIsRefusedBeforeAnyHandlerRuns` proves
  `Config.MaxBodyBytes` bounds every mutating route server-wide, before any handler reads the body into
  memory. `TestPathTraversalIsNeverServed`/`TestQueryParameterPathIsNeverResolvedOutsideTheWorkspace` (raw
  TCP probes, bypassing `net/http`'s own client-side path normalization) prove no request-target or
  query-parameter path value can walk out of the registered route space.
- **"Token never appears in URL/log/durable state"** (V8-04C's own Verify line, word for word): already
  implemented and already tested —
  `TestSecretScan_TokenNeverAppearsInLogOutput` (a real request with the correct token, one with a wrong
  token, and one with the correct token again, asserting the real token string never appears verbatim in
  captured log output) and `TestSecretScan_BootstrapHTMLNeverPutsTokenInAURLOrQueryString`
  (`security_test.go`).

### Decision

No new code, and no new test, is needed for V8-04C: every one of its 6 design-doc scenarios, plus its own
specific Verify bar (token never in URL/log/durable state), already has real, matrix-style coverage — run
against every registered route via the real composed route registry, not a hand-picked sample — built during
V6-13 (`docs/design/08-v6-api-projections.md` V6-01A). Duplicating any of this would add no new coverage.

This is itself a legitimate, honest V8-04C outcome (a verification-only closure), not a shortcut: the full
`internal/delivery/httpapi/...` suite (including `securitymatrix` and `security_test.go`) was re-run against
current `master` to confirm the "already closed" claim holds under today's code, not just at whatever commit
V6-13 originally landed at.

### Execution

- No production or test files changed. This entry (and this PR) exists purely to record the verification
  outcome in the checklist, matching this repo's own "every task gets a checklist entry" discipline even
  when the task's own real conclusion is "already done."

### Verify

- `go build ./...`, `go vet ./...`: clean.
- `go test ./internal/delivery/httpapi/... -timeout 5m` (the full package tree, including `securitymatrix`
  and `security_test.go`): clean — `internal/delivery/httpapi` 10.3s, `internal/delivery/httpapi/
  securitymatrix` 17.6s, all 19 subpackages pass.

## V8-04D — Content, redaction and secret-scan suite

### Context

`docs/design/10-v8-alpha-hardening.md` V8-04D (ADR-017, AK-ARCH-024) asks for: malicious artifact/YAML
handling, a redaction corpus, and a "retained-data secret scan trên DB/event/log/artifact" — completion bar:
"không có sink nào bỏ qua redactor dùng chung" (no sink bypasses the shared redactor), verified by "secret
fixture search bằng 0; artifact không execute như trusted HTML."

Research before writing any code found:
- **Malicious artifact / "artifact không execute như trusted HTML"**: already fully closed by V6-13's own
  `TestArtifactContentMediaHandling` (`internal/delivery/httpapi/securitymatrix/injection_test.go`, also the
  closing evidence for V8-04C above) — script-capable content is forced to `Content-Disposition: attachment`
  with `X-Content-Type-Options: nosniff`, never rendered inline. **Nothing to add.**
- **Redaction corpus**: `internal/app/redact/redact_test.go` already has an extensive unit corpus (23 test
  functions) covering exact-match, false-positive allowlisting, tagged sensitivity, nested maps/slices/
  structs/pointers, Argv/Env-shaped values, max-depth, non-mutation, and free-text embedding. **Nothing to
  add.**
- **Log sink**: `internal/app/logging/logger_test.go` already has its own real, dedicated "zero occurrences"
  suite (`TestLogger_JSON_SecretField_ZeroOccurrences`, `TestLogger_Text_SecretField_ZeroOccurrences`,
  `TestLogger_SecretNestedInFieldValue_ZeroOccurrences`) against the actual production
  `internal/app/logging.Logger`. **Nothing to add.**
- **Artifact/DB/event sink, at the real end-to-end level**: `internal/app/runtime/truncation_redaction_test.go`
  already proves a resolved secret is scrubbed out of ONE persisted command-output artifact — but only
  against a fake, in-memory `*fake.UnitOfWork`, never a real sqlite database or a real filesystem
  `ArtifactStore`, and never as a whole-corpus scan (only the one artifact the test already knows the ID of).
  This is the one genuine real-topology gap, in the same shape V8-04B's own research found for
  multi-repository-write: a real mechanism, well unit-tested in isolation, never proven against the real
  retained stores this codebase actually persists to.

### Decision

New test in `internal/integration/v5accept` (same package/fixture V8-04B's own addition reused, and the
same real sqlite/git/artifact-store/ProcessSupervisor composition V5-15's own scenarios already established):
`TestV5AcceptRetainedDataSecretScan_RealSecretNeverPersistedUnredacted` sets a real secret value on the TEST
process's own environment (`t.Setenv`, exactly what `secretenv.Resolver`'s own doc comment names as Alpha's
"trust the local machine" secret store), runs a real, minimal COMMAND-only graph (`START -> echo_secret ->
END`, no gate needed — a COMMAND node's own successful execution already produces
`runtimedomain.EvidenceKindCommandExecution` evidence, which is all a `CompletionPolicy` naming only that one
`RequiredEvidenceKind` ever needs) whose real script genuinely echoes the resolved secret to stdout, then:
1. Reads the one legitimate command-output artifact back through the real `ArtifactStore` — the positive
   control: it must NOT contain the raw secret, and MUST contain the redactor's own `[REDACTED]` marker,
   proving redaction genuinely ran rather than merely being absent because nothing happened.
2. Walks EVERY real object under the real filesystem `ArtifactStore`'s own root directory on disk — not just
   the one artifact already known — searching each for the raw secret value.
3. Closes the real store and reads the raw bytes of the real, on-disk sqlite database FILE itself, searching
   for the raw secret value — this covers "DB" and "event" from V8-04D's own scope in one sweep, since every
   `domain_events` row this run ever logged lives in that same file.

`v5AcceptDriftPermissionPolicyDocument` (adapter_drift_test.go, `OperatorTrustedLocal`, no granted
capabilities) is reused directly rather than adding a new permission-policy fixture, since this scenario
needs neither isolation enforcement nor any special capability.

### Execution

- `internal/integration/v5accept/retained_data_secret_scan_test.go` (new): `v8d04dEchoSecretScript` (a real,
  OS-appropriate script echoing the resolved secret), `v8d04dDocument` (the minimal COMMAND-only graph), and
  the test itself.
- No CI wiring needed: the new test lands inside `internal/integration/v5accept`, a package the existing
  `contract` job (`go test ./...`) already runs in full.

### Verify

- `go build ./...`, `go vet ./...`: clean.
- `go test -count=1 ./...` (full offline suite): clean.
- `go test ./internal/integration/v5accept/... -run
  'TestV5AcceptRetainedDataSecretScan_RealSecretNeverPersistedUnredacted' -count=1 -timeout 3m`: clean across
  3 consecutive fresh local runs (2.1-2.2s each).
- `go test ./internal/integration/v5accept/... -timeout 5m` (whole package): clean — confirms the new test
  coexists correctly with every existing V5-15 scenario in the same package.

## V8-04B — Process, executable and isolation abuse suite

### Context

`docs/design/10-v8-alpha-hardening.md` V8-04B (ADR-013, ADR-023, AK-ARCH-015B, AK-ARCH-020A) asks for a
negative suite covering 7 scenarios: argv/env injection, oversized output, adapter build drift, multi-repo
write missing capability, checker source-write, remote Git, and isolation-profile lie (auto-downgrade +
`ISOLATION_ENFORCEMENT_UNAVAILABLE`) — completion bar: "không có đường nào thực thi vượt policy đã publish,"
verified by "spawn count bằng 0 cho case isolation; remote mutation call count bằng 0."

Research before writing any code found this task's scope is almost entirely ALREADY real, existing,
already-tested production code — more so than V8-04A's own equivalent research found:
- **Isolation-profile lie**: `internal/integration/v5accept/isolation_unavailable_test.go`
  (`TestV5AcceptIsolationUnavailable_RealAdmissionRejectsBeforeSpawn`) already proves, end to end, that a
  real COMMAND node pinned to `IsolationTierEnforcedIsolated` is rejected by real admission BEFORE
  `ProcessSupervisor.Run` is ever called (`process.IsolationChecker{}`, the REAL non-fake checker,
  structurally always returns `ErrIsolationEnforcementUnavailable` — ADR-013/ADR-023's own "không bao giờ
  auto-downgrade": there is no silent downgrade path to prove absent, because none exists), proven not by an
  error string but by the real maker script's own marker file never existing on disk. **Already fully DONE at
  the real end-to-end level. Nothing to add.**
- **Adapter build drift**: `internal/integration/v5accept/adapter_drift_test.go`
  (`TestV5AcceptAdapterDrift_RealAdmissionRejectsMismatchedPin`) already proves a real AGENT node pinned to an
  `AdapterBuild` whose `ExecutableContentHash` was deliberately perturbed after being derived from the real
  `fake-claude` binary is rejected when a real, live re-probe of that SAME untouched binary can only ever
  re-derive its true hash. **Already fully DONE at the real end-to-end level. Nothing to add.**
- **Checker source-write**: `assemble_execution_request.go` (V5-12 contract 3, 2026-09-10) already forces
  EVERY mount of a CHECKER-role AGENT node to `ports.WorkspaceReadOnly`, unconditionally, regardless of what
  the WorkItem's own `EffectiveScope` grants — `forceReadOnlyMounts`, the identical mechanism
  `GateNodeExecutor` already relies on for "a Gate never mutates anything." This is real, not merely
  declarative: `resolveExecutionResources` only ever calls `AcquireWriteLeases` for a WRITE-access mount, so
  forcing every mount READ_ONLY here structurally means a checker attempt never acquires one — already
  unit-tested (`TestAssembleAgentExecutionRequest_CheckerRole_MountsForcedReadOnly`,
  `agent_node_executor_test.go`'s own "write lease count == 0" assertion). **Already DONE; the guarantee is
  "never acquires a write lease," not an OS-level filesystem permission — the real system never claims the
  latter, so there is no further real-topology gap to close.**
- **Multi-repo write missing capability**: `checkMultiRepositoryWriteGrant` (`admission.go`, GC-INV-24) is
  real production code, already unit-tested
  (`TestAdmission_MultiRepositoryWriteWithoutGrant_BlocksBeforeSpawn`) — but ONLY against hand-built
  `evaluateAdmission` inputs, never through the real production path (`CreateRootWorkItem`/
  `CreateChildWorkItem` -> real `WORKSPACE_PROVISION` jobs -> real `ExecuteNodeHandler`) the way isolation and
  adapter-drift already are. **The one genuine real-topology gap this task closes.**
- **Argv/env injection, oversized output**: `internal/adapters/process/supervisor_test.go`
  (`TestSupervisorRunsExecutableWithoutShell`, `TestSupervisorBoundsOversizedOutput`) already prove these
  against the REAL `ProcessSupervisor` with a REAL spawned OS process — not a fake. `command.CommandDocument`
  itself structurally prevents shell interpolation (`Argv []ArgvElement`, never a free-form string; an
  unknown `PLACEHOLDER` name is a publish-time rejection,
  `TestValidateDocument_RejectsUnknownPlaceholder`/`TestCommandNodeExecutor_UnknownArgvPlaceholder_
  FailsClosedWithoutSpawning`). **Already proven with a real process at the adapter layer — the missing piece
  would only be threading this through a full workflow run, which would exercise the identical
  `ProcessSupervisor` code path already proven, for no new coverage.**
- **Remote Git**: confirmed by reading every non-test `.go` file in `internal/adapters/gitworktree` — the
  package NEVER calls `git push`, `git fetch`, or `git clone` anywhere. Every git operation it performs
  (`worktree add`, `worktree remove`, `rev-parse`, diff/log reads) is local-only, against a repository already
  present on disk. "Remote mutation call count == 0" is therefore not a runtime check to test — it is a
  structural fact about this codebase today: there is no code path that could ever attempt one. `NetworkAccess`
  (`command.CommandDocument`) is a real, already-enforced, already-tested policy-grant check
  (`TestCommandNodeExecutor_NetworkAccessAllowedWithoutGrant_FailsClosedWithoutSpawning`) for a COMMAND's own
  spawned process reaching the network — a completely separate concern from git remote operations, which
  simply do not exist in this codebase.

### Decision

Given six of seven scenarios are already closed — two with existing real end-to-end proof
(isolation-profile lie, adapter build drift), one structurally impossible to violate (remote Git), and three
with solid coverage at the adapter/unit level that a full-workflow-run test would not meaningfully strengthen
(checker source-write, argv/env injection, oversized output) — this task's real, new value is the ONE
scenario identified above that was never proven past hand-built `evaluateAdmission` inputs: multi-repository
write without the `INTEGRATION_MULTI_REPOSITORY_WRITE` grant, driven through the real production path.

New test in `internal/integration/v5accept` (same package V5-15C's own isolation-unavailable/adapter-drift
scenarios live in, reusing `v5AcceptFixture` exactly as they do — never
`internal/integration/v6accept`'s own real-HTTP-surface harness, since this scenario needs no HTTP layer at
all, only the real `CreateRootWorkItem`/`CreateChildWorkItem`/`StartWorkflowRun` application commands
V5-15C's own scenarios already call directly):
`TestV5AcceptMultiRepositoryWriteWithoutGrant_RealAdmissionRejectsBeforeSpawn` registers a SECOND real git
repository (`repo-b`, alongside the fixture's own default `repo-a`), grants WRITE on BOTH through a real
root+child WorkItem pair (so both really provision — two real `WORKSPACE_PROVISION` jobs), pins a permission
policy at `OperatorTrustedLocal` (never `EnforcedIsolated` — `admissionPriority` checks isolation before
multi-repository-write, and this scenario is about the write-grant check specifically, not isolation) with NO
granted capabilities, and confirms the real Attempt is BLOCKED with
`TerminationReasonWriteCapabilityOrGrantMissing`/`BlockerWriteCapabilityOrGrantMissing` — using the identical
"real maker script's own marker file never exists on disk" proof technique
`isolation_unavailable_test.go`/`adapter_drift_test.go` already established, making "spawn count == 0" for
this case concrete rather than asserted from an error string alone.

### Execution

- `internal/integration/v5accept/multi_repository_write_grant_test.go` (new):
  `registerSecondV5AcceptRepository` (a second real git repository, driven to ACTIVE exactly like
  `seedActiveProjectAndRepository` does for the fixture's own default one),
  `v5AcceptMultiRepoNoGrantPermissionPolicyDocument`, `v5AcceptMultiRepoDocument` (the same
  single-real-COMMAND-node shape `v5AcceptIsolationUnavailableDocument` uses), and the test itself.
- No CI wiring needed: the new test lands inside `internal/integration/v5accept`, a package the existing
  `contract` job (`go test ./...`) already runs in full — matching V8-01's own "no new CI job" precedent.

### Verify

- `go build ./...`, `go vet ./...`: clean.
- `go test -count=1 ./...` (full offline suite): clean on a second run. The FIRST full-suite run showed one
  unrelated failure — `TestEndToEnd_MultiRepoProvision_BothReachReadyWithBaseRevisionSet`
  (`internal/app/workspaceprovision`, a package this task never touches) — reproduced 3/3 clean in isolation
  immediately after, confirming a load-induced flake under full-parallel `go test ./...` (this session's own
  established "Windows CI runner contention" pattern, [[agent-kit-ci-known-flakes]]), not a regression from
  this change.
- `go test ./internal/integration/v5accept/... -run
  'TestV5AcceptMultiRepositoryWriteWithoutGrant_RealAdmissionRejectsBeforeSpawn' -count=1 -timeout 3m`: clean
  across 3 consecutive fresh local runs (4.8-6.9s each).
- `go test ./internal/integration/v5accept/... -timeout 5m` (whole package): clean — confirms the new test
  coexists correctly with every existing V5-15 scenario in the same package, including the two it reuses
  fixture machinery from (isolation-unavailable, adapter-drift).

## V8-04E — Security aggregate gate

### Context

`docs/design/10-v8-alpha-hardening.md` V8-04E (ADR-013, ADR-016, ADR-017, ADR-023) asks for a single verdict
over the four V8-04A..D suites: "chạy toàn bộ suite, tổng hợp kết quả theo criterion và ghi evidence" (run the
whole suite, aggregate the result by criterion, write evidence) — completion bar: "deny-by-default failures
có safe diagnostic/evidence và không suite nào bị bỏ qua" (every deny-by-default rejection has a safe
diagnostic/evidence, and no suite is silently skipped), verified by "aggregate report; một suite fail làm
gate fail."

V8-04A/B/C/D's own checklists already established that most of each design-doc scope was already-closed,
pre-existing production code — each task's own real, new contribution is a SMALL, named set of tests (or, for
V8-04C, a representative subset of pre-existing V6-13 tests), not a whole dedicated package. This repo
already has an exact structural precedent for exactly this shape of gate: `internal/v6gate`/`cmd/v6-gate`
(V6-14C, `docs/design/08-v6-api-projections.md`) — a pure reader over already-produced `go test -json` CI
artifacts, checking a CLOSED list of named scenarios (never "did the package pass," since a shared package
passing trivially would not prove a specific new test ever ran), mapping every finding to the Task ID that
owns it, and collapsing to one of three verdicts (PASS / REWORK / CHƯA ĐỦ EVIDENCE, missing evidence always
dominating a mere failure).

Checking which of A/B/C/D's own real load-bearing tests already have a structured `-json` evidence artifact
in CI found a split:
- V8-04A's two `internal/integration/v6accept`-hosted tests
  (`TestV8PathAbuse_RepositoryNestedUnderWorkspaceRoot_Rejected`,
  `TestV8PathAbuse_SymlinkDisguisedNestedRepository_Rejected`) already ride the EXISTING `v6-acceptance`
  job's own `acceptance.jsonl` for free — that job already runs
  `go test -count=1 -v -timeout 15m ./internal/integration/v6accept/...` (the WHOLE package, not just one
  test) and converts the log to `-json` via `go tool test2json`, on both platforms, today. No new CI needed
  for these two.
- Every other named test — V8-04A's own `TestProviderRelease_BystanderSiblingUnderManagedRootSurvives`
  (`internal/adapters/gitworktree`), V8-04B's three `internal/integration/v5accept` tests, V8-04C's seven
  representative `internal/delivery/httpapi`/`securitymatrix` tests, and V8-04D's own
  `TestV5AcceptRetainedDataSecretScan_RealSecretNeverPersistedUnredacted` (also `v5accept`) — passes today
  only as part of the `contract` job's plain `go test -count=1 ./...`, which produces no structured per-test
  evidence at all. This is the one genuine new-CI gap this task closes.

### Decision

New package `internal/v8gate` (mirroring `internal/v6gate`'s exact shape deliberately, generalized to read
from TWO different artifact families instead of one) plus `cmd/v8-security-gate` (mirroring `cmd/v6-gate`).
A closed list of 14 named scenarios, each tagged with its owning suite (V8-04A/B/C/D) and which artifact
family it lives in — 2 read from the existing `v6-acceptance-report-<os>` artifact, 12 from a NEW
`v8-04e-evidence-<os>` artifact. `TestV8PathAbuse_SymlinkDisguisedNestedRepository_Rejected` is the one
scenario with a per-platform `SkipAllowedOn` exception (Windows only) — a real, deterministic
Developer-Mode privilege gap already established as this repo's own accepted convention
(`internal/adapters/repoprobe/prober_test.go`), never a blanket "skip anywhere" the way V6-14C's own
race-timing `conditionalScenarios` are allowed.

CI wiring: a new "Security suite evidence (V8-04E)" step in the existing `contract` job (both platforms,
`if: always()` so it still runs and reports real failures by name even if something unrelated already failed
the main offline suite) re-runs `go test -count=1 -json` over exactly the three package trees
(`internal/adapters/gitworktree`, `internal/integration/v5accept`, `internal/delivery/httpapi/...`) that
carry V8-04E's own genuinely new evidence — the identical "standalone, redundant, structured report on top of
an already-enforcing suite" precedent `V0-13`'s own Boundary/dependency report step already established for
this exact job, generalized from `-v` (a log) to `-json` (per-test outcomes) because that is what this gate
needs to read. A new `v8-04e-gate` job (`needs: [contract, v6-acceptance]`, `if: always()`) downloads both
artifact families for both platforms and runs `cmd/v8-security-gate`, uploading its own `v8-04e-verdict`
artifact and exiting non-zero unless every scenario is PASS.

### Execution

- `internal/v8gate/gate.go` (new): `Verdict`, `scenario`/`scenarios` (the closed 14-entry list),
  `Finding`, `ScenarioOutcome`, `Report`, `Inputs`, `Run` (reads both artifact families, checks every named
  scenario on every platform, collapses to one verdict) — a close structural port of `internal/v6gate`'s own
  `Run`/`readPlatform`/`parseTestEvents`/`verdictFor`, generalized to a caller-chosen artifact-name prefix per
  scenario's own `Source` field.
- `internal/v8gate/gate_test.go` (new): 9 tests mirroring `internal/v6gate/gate_test.go`'s own fixture-mutate
  pattern — complete green pass, a missing platform artifact, a required-scenario failure, a required-scenario
  skip (not tolerated), a required-scenario absence, the one per-platform-tolerated skip (both the tolerated
  and the NOT-tolerated platform), a cross-commit evidence mismatch, and "one suite failing fails the whole
  gate."
- `cmd/v8-security-gate/main.go` (new): CLI wrapper mirroring `cmd/v6-gate/main.go` exactly (`--evidence-dir`,
  `--commit`, `--out`; prints notes/findings; exits 1 unless PASS).
- `.github/workflows/spike-gate.yml`: new "Security suite evidence (V8-04E)" + "Upload V8-04E security suite
  evidence" steps inside the existing `contract` job (both platforms); new `v8-04e-gate` job.

### Verify

- `go build ./...`, `go vet ./...`: clean.
- `go test -count=1 ./...` (full offline suite): clean, including `internal/v8gate`'s own 9 new tests.
- `go test ./internal/v8gate/... -v`: all 9 tests pass.
- Ran the EXACT new CI command locally (`go test -count=1 -json ./internal/adapters/gitworktree/...
  ./internal/integration/v5accept/... ./internal/delivery/httpapi/...`): exit 0, 4473 JSON lines, all 12
  `v8-04e-evidence`-sourced named scenarios individually confirmed present with a real `pass` action (grepped
  by exact test name).
- Ran the real `v6-acceptance`-style command locally
  (`AW_HTTP_ACCEPTANCE=1 go test -count=1 -v -timeout 5m -run 'TestV8PathAbuse'
  ./internal/integration/v6accept/...` + `go tool test2json`): both named scenarios present;
  `TestV8PathAbuse_RepositoryNestedUnderWorkspaceRoot_Rejected` pass, `..._Symlink..._Rejected` skip (this dev
  machine has no Developer Mode — the same already-established, accepted local-environment limitation).
- Assembled a real two-platform evidence directory from the above (both simulated platforms fed from this
  one Windows machine's own log, so the symlink test's real local skip appears on BOTH simulated platforms)
  and ran the real `go run ./cmd/v8-security-gate` binary end to end: correctly reported 13/14 scenarios PASS
  on both platforms, correctly tolerated the skip on the simulated "windows-latest" leg (a note, not a
  finding), and correctly flagged the same skip as `CHƯA ĐỦ EVIDENCE` on the simulated "ubuntu-latest" leg
  (since `SkipAllowedOn` only names Windows) — exactly the intended behavior; a REAL ubuntu-latest CI leg
  would genuinely PASS this scenario (`os.Symlink` works unprivileged on real Linux), so this one "failure" is
  purely an artifact of simulating two platforms from a single machine's log, not a defect in the gate logic.
- CI wiring itself (the new `contract`-job step and the new `v8-04e-gate` job) could not be exercised locally
  (no local GitHub Actions runner) — left to the real PR's own CI run.

## V8-05 — Retention-class, cleanup and disk-pressure behavior

### Context

`docs/design/10-v8-alpha-hardening.md` V8-05 (AK-ARCH-025B) asks for: "TTL 7 ngày của raw output/evidence
tạm không lan sang canonical conversation/context, không phá recovery evidence và báo disk full rõ" (a 7-day
TTL on raw temp output/evidence must never leak into canonical conversation/context, must never break
recovery evidence, and disk-full must be reported clearly) — Thực hiện: fake clock aging by class,
holds/references/orphans, canonical Message/context/audit preservation, low-disk simulation, cleanup
recovery/idempotency. Completion bar: cleanup only ever deletes owned eligible payload; conversation,
referenced context, holds, and metadata audit all remain intact.

Research before writing any code found `internal/app/artifactsweep` (V5-14) already implements and
exhaustively unit-tests almost this entire scope:
- `artifact.RetentionClass` (`internal/domain/artifact/artifact.go`) is already a real, closed two-value
  domain type: `RAW_OUTPUT_TEMP` (always a non-nil `ExpiresAt`, `ComputeExpiresAt` computing `createdAt+7d`)
  and `CANONICAL_CONTEXT` (always `nil` `ExpiresAt` — structurally can never age out) — already unit-tested
  (`artifact_test.go`).
- `classifyLocatorGroup`'s own decision table (`sweep_test.go`,
  `TestClassifyLocatorGroup_DecisionTable`) already exhaustively covers every branch this task's own scope
  names: a single row past grace purges; ANY row in a locator group being Attached, Held, or not yet past
  grace blocks the WHOLE group; a content-hash or size mismatch across a shared locator is flagged corrupt.
  Pure, no I/O, already proven.
- `ExecuteArtifactSweep` (`sweep_sqlite_test.go`) already proves, against a REAL sqlite database and a REAL
  filesystem `ArtifactStore`: dry-run-by-default reports without touching anything, a real run genuinely
  deletes content and marks `Purged` (never actually removing the row, per ADR-017's own audit bar), an
  Attached sibling sharing a Locator blocks the whole group for real, and — the "cleanup recovery/
  idempotency" half of this task's own scope — a crashed claim (a `ClaimArtifactLocatorForPurge` left behind
  by an earlier, interrupted attempt of this exact singleton job) resumes correctly rather than being treated
  as a foreign conflict. All of this is **already closed**; nothing here needed duplicating.
- **A real, load-bearing finding**: NO real production code path in this entire codebase ever constructs a
  `RetentionRawOutputTemp` artifact today. Grepping every real (non-test) `artifact.NewArtifact`/
  `artifact.Retention*` call site in the app layer found `internal/app/message`
  (`AppendMessage`/`AppendConversationAttachment`) and all three of `internal/app/runtime`'s node executors
  (`command_node_executor.go`, `gate_node_executor.go`, `agent_node_executor_resources.go`) hardcode
  `RetentionCanonicalContext` — every real artifact this product ever produces today is immortal
  (`ExpiresAt: nil`). The domain type, its TTL math, and the sweeper's own full decision table are real and
  already well-tested; there is simply no real PRODUCER wired to the temp class yet. This means the
  "TTL doesn't leak into canonical" property can only be exercised end to end today by seeding synthetic
  raw-temp fixtures alongside a REAL canonical artifact — the same technique
  `sweep_sqlite_test.go`'s own `putAndInsertOrphan` already establishes at the unit level, generalized here
  to run against a REAL canonical conversation Message in the SAME real stores.
- **A second real gap**: "báo disk full rõ" (report disk-full clearly) has NO existing implementation
  anywhere — `internal/adapters/artifactstore/filesystem.go`'s own `Put` wraps every write-path I/O error
  with the SAME generic `fmt.Errorf("artifactstore: <op>: %w", err)`, indistinguishable from any other
  failure. This is the one genuinely new-code gap this task closes.

### Decision

1. **Real end-to-end retention test**, `internal/integration/v5accept/retention_sweep_real_topology_test.go`
   (reusing `v5AcceptFixture` — same real sqlite/artifact-store composition V8-04B/D's own additions already
   established in this package): a REAL canonical conversation Message (via the real
   `message.AppendConversationAttachment` application command, against a real root WorkItem) sits in the SAME
   real database and real filesystem `ArtifactStore` as three seeded raw-temp artifacts — an eligible orphan
   (past the real 7-day grace, no hold, no reference), a held orphan (past grace but `Hold: true`), and an
   orphan sharing its real content-addressed Locator with a live `Attached` sibling (a reference) — then runs
   the real `ExecuteArtifactSweep` (real run, not dry-run) with a fixed clock. Before/after assertions (V8-05's
   own "before/after manifest" Verify bar, made concrete): the canonical Message artifact is
   byte-for-byte unchanged and still Attached; the held and referenced orphans both survive, `Orphan` state
   and content intact; the eligible orphan alone is genuinely `Purged`, its content genuinely gone; and the
   real `Manifest.Groups` reports exactly 1 `PURGED` + 2 `BLOCKED` groups, never silently omitting either
   outcome.
2. **Disk-full clear diagnostic**: a new `classifyWriteError` helper in
   `internal/adapters/artifactstore/filesystem.go`, wired into every write-path error `Put` can return
   (`CreateTemp`, `io.Copy`, `Sync`, `Close`, `MkdirAll`, `Rename`). `errors.Is(err, syscall.ENOSPC)` —
   confirmed a real, portable errno value on windows-latest too, not POSIX-only — classifies into
   `apperror.Wrap(apperror.CodeUnavailable, "...disk full...", retryable: true, cause)`, never a NEW top-level
   error code (`go-core-spec` §18's own 22-value enum is explicitly closed;
   `internal/domain/errorcode/errorcode.go`'s own doc comment says so). `CodeUnavailable` already means "a
   transient condition a bounded retry may resolve on its own" — exactly what freeing disk space and retrying
   the identical `Put` is. Every other real I/O error keeps the original generic wrap unchanged (a pure
   addition, not a behavior change for anything already passing). Tested against the classifier directly with
   a synthetic wrapped `syscall.ENOSPC`, not by actually filling a real disk — neither portable nor safe to
   attempt in CI on either OS, and Go's own `os`/`io` calls already return a real wrapped `syscall.Errno` on a
   genuine ENOSPC on every GOOS this repo targets, so the classifier sees the identical shape either way.

### Execution

- `internal/integration/v5accept/retention_sweep_real_topology_test.go` (new):
  `seedRawTempArtifact`/`reloadArtifactByID` (fixture helpers), `requireArtifactBytesUnchanged`, and
  `TestV5AcceptRetentionSweep_CanonicalConversationHeldAndReferencedSurviveRealEligibleOrphanPurge`.
- `internal/adapters/artifactstore/filesystem.go`: new `classifyWriteError` function; `Put`'s own six
  write-path error returns now call it instead of a bare `fmt.Errorf`.
- `internal/adapters/artifactstore/filesystem_test.go`: two new tests,
  `TestClassifyWriteError_DiskFull_ReturnsSafeRetryableUnavailable` and
  `TestClassifyWriteError_OtherError_KeepsTheOriginalGenericWrap`.
- No CI wiring needed: both additions land inside packages the existing `contract` job (`go test ./...`)
  already runs in full.

### Verify

- `go build ./...`, `go vet ./...`: clean.
- `go test -count=1 ./...` (full offline suite): clean, including all 4 new tests.
- `go test ./internal/adapters/artifactstore/... -run TestClassifyWriteError -v`: both new tests pass.
- `go test ./internal/adapters/artifactstore/... -v`: full package, all existing tests still pass unchanged
  (the disk-full classification is additive; every non-ENOSPC error keeps its original wrap text).
- `go test ./internal/integration/v5accept/... -run
  'TestV5AcceptRetentionSweep_CanonicalConversationHeldAndReferencedSurviveRealEligibleOrphanPurge' -count=3
  -timeout 2m`: clean across 3 consecutive fresh local runs (~1.3-1.5s each).
- `go test ./internal/integration/v5accept/... -timeout 5m` (whole package): clean, 79.6s — confirms the new
  test coexists correctly with every existing V5-15/V8-04B/V8-04D scenario in the same package.

## V8-06 — SQLite backup/restore and corruption diagnostics

### Context

`docs/design/10-v8-alpha-hardening.md` V8-06 (ROADMAP-§7) asks for: "local operator sao lưu/khôi phục
consistent DB + artifact manifest, không hứa sync/merge hai installs" (a local operator backs up and restores
a consistent database plus an artifact inventory manifest, never promising to sync or merge two separate
installations). Completion bar: a real backup opens cleanly, a real restore into a fresh location verifies
clean, and missing/corrupt artifacts are reported, not silently ignored.

Research before writing any code found there is currently **no backup/restore feature at all** anywhere in
this codebase — no port, no adapter method, no CLI surface. Two real building blocks needed identifying
first:

- **SQLite's own `VACUUM INTO` statement** is SQLite's documented safe, consistent, online-backup mechanism —
  confirmed via a direct throwaway smoke test that it works with `modernc.org/sqlite` (this repo's own
  driver) and that the resulting file reopens cleanly through the real `sqlite.Open` path. It only ever takes
  the same SHARED read lock an ordinary read transaction would, so it is safe to run while a real `aw
  serve`/`aw worker` process is actively reading and writing the same database in WAL mode — no new
  "pause writes for a backup window" mechanism was needed anywhere in the application layer.
- **`ports.ArtifactRepository` has no existing "list every artifact" method** — only `ListOrphanedArtifacts`
  (filtered by age) and `ListArtifactsByLocator` (filtered by locator). A backup's own manifest needs every
  retained row regardless of `AttachState`/`RetentionClass`, so this is a genuine new interface method, not
  a reuse of an existing one.
- **`cmd/aw`'s own `CLI_LOCAL` dispatch set is a closed list** — found via
  `docs/design/08-v6-api-projections.md:799`: "**Không làm:** no new leaf/route. `CLI_LOCAL` closed set is
  serve/worker/help/version/evidence verify." This is a real, documented architectural constraint from an
  earlier V6 task that forbids adding a new process-level subcommand to `cmd/aw/cli.go`'s own `subcommands`
  map. A backup/restore tool also has no natural HTTP-parity counterpart to route through the resource-command
  registry (it operates on files the running process isn't even using). `cmd/v6-gate`, `cmd/v8-security-gate`,
  and `cmd/docs-coverage-check` already establish the precedent this repo uses for exactly this situation: a
  brand-new, standalone `cmd/` binary with its own composition root, confirmed compatible with
  `internal/archtest`'s own `TestCompositionRootIsCmdAwNotCmdAgentkit` check.

### Decision

1. **New minimal port**, `ports.DatabaseBackup` (`internal/app/ports/databasebackup.go`) — one method,
   `BackupTo(ctx, destPath)` — kept as its own port rather than growing `UnitOfWork`, since it is the one
   durable-store operation that is NOT a transaction: it operates on the store as a whole, at a single
   consistent point in time, never inside a caller-managed `Tx`. `internal/adapters/sqlite/backup.go`
   implements it on `*Store` via `VACUUM INTO`, refusing to overwrite an existing destination file — the same
   "never silently clobber" discipline this codebase's other adapters already follow — and confirmed via a
   compile-time assertion (`var _ ports.DatabaseBackup = (*Store)(nil)`) that this stays clear of the
   domain/app-never-imports-adapters archtest rule (the new `internal/app/maintenance` package depends only on
   the port, never on `internal/adapters/sqlite` directly).
2. **New `ports.ArtifactRepository.ListAllArtifacts(ctx)` method**, implemented in both real implementers
   found via `grep -rln "ports.ArtifactRepository\b"`: the real sqlite adapter
   (`internal/adapters/sqlite/artifact_repository.go`, mirroring `ListOrphanedArtifacts`'s own two-phase
   collect-IDs-then-load pattern, ordered by `(created_at, id)` for a deterministic result) and the fake
   (`internal/app/ports/fake/unitofwork.go`, same ordering via `sort.Slice`).
3. **New `internal/app/maintenance` package** — the real application logic, composed from ports only:
   - `Backup` (`backup.go`): reads the full artifact inventory first, then calls `BackupTo` (in that order —
     a manifest describing a moment strictly later than the DB snapshot it accompanies is always the safe
     direction for a restore-time cross-check), then writes a versioned JSON `Manifest`
     (`ManifestSchemaVersion = 1`) recording only durable metadata per artifact (ID, ProjectID, Locator,
     ContentHash, Size, MediaType, RetentionClass, AttachState) — never artifact bytes, since those live in
     the DB snapshot's own `artifacts` table already. Refuses to overwrite an existing manifest destination.
     Deliberately does NOT copy the artifact-store's own real content files: an operator's own separate
     filesystem-level backup of the artifact-store root is assumed; this package only ever proves whether
     that separately-backed-up content still matches what the manifest says should be there.
   - `VerifyRestoredArtifacts` (`restore.go`): checks every manifest entry against a real
     `ports.ArtifactStore.Verify` call, classifying each into `OK` / `MISSING` (a `CodeNotFound` from
     `Verify`) / `CORRUPT` (any other verify failure — hash or size mismatch), collected into a
     `RestoreVerificationReport` with a single `.Clean()` bool a CLI can branch on. A `Purged` manifest entry
     is deliberately skipped — its real content was legitimately, durably deleted by a real V5-14 sweep before
     the backup was even taken, so having no real content at its Locator is expected, not evidence of
     corruption.
4. **New standalone binary `cmd/aw-maintenance`** (`backup`/`restore` subcommands, its own composition root
   wiring the real sqlite/artifactstore adapters directly) rather than touching `cmd/aw` — per the `CLI_LOCAL`
   finding above. `restore` materializes the backup's DB snapshot into a caller-specified FRESH temp root
   (refusing if a `restored.db` already exists there — never overwrite a live installation or a previous
   restore attempt silently), confirms it genuinely opens through the real production `sqlite.Open` path
   (migrations included — a failure here means the backup is not usable), then cross-checks the manifest
   against an operator-supplied, separately-restored artifact-store root and prints/returns the verification
   report, exiting non-zero if anything is missing or corrupt.

### Execution

- `internal/app/ports/artifactrecord.go`: new `ListAllArtifacts(ctx) ([]artifact.Artifact, error)` method on
  `ArtifactRepository`.
- `internal/adapters/sqlite/artifact_repository.go`: `ListAllArtifacts` implementation.
- `internal/app/ports/fake/unitofwork.go`: `ListAllArtifacts` fake implementation.
- `internal/app/ports/databasebackup.go` (new): `DatabaseBackup` port.
- `internal/adapters/sqlite/backup.go` (new): `Store.BackupTo` via `VACUUM INTO`.
- `internal/app/maintenance/backup.go` (new): `ManifestSchemaVersion`, `ManifestEntry`, `Manifest`,
  `BackupDeps`, `BackupRequest`, `BackupResult`, `Backup`, `ReadManifest`.
- `internal/app/maintenance/restore.go` (new): `ArtifactRestoreStatus`, `ArtifactRestoreFinding`,
  `RestoreVerificationReport` + `.Clean()`, `VerifyRestoredArtifacts`.
- `internal/app/maintenance/maintenance_sqlite_test.go` (new): real sqlite + real filesystem
  `ArtifactStore` fixture (`backupFixture`, mirroring `artifactsweep/sweep_sqlite_test.go`'s own "real stack,
  not fakes" discipline for this job family) — `TestBackup_ProducesOpenableConsistentSnapshotAndAccurateManifest`,
  `TestBackup_RefusesToOverwriteAnExistingManifest`,
  `TestVerifyRestoredArtifacts_MissingAndCorruptAreDistinguished` (the last one genuinely corrupts one real
  on-disk object file's bytes and confirms MISSING and CORRUPT are told apart, never collapsed into one
  generic bucket).
- `cmd/aw-maintenance/main.go` (new): `backup`/`restore` subcommands, each its own `flag.FlagSet`.
- `cmd/aw-maintenance/main_test.go` (new): calls the real `runBackup`/`runRestore` entrypoints directly
  (the same functions the compiled binary's own `main()` calls) —
  `TestBackupThenRestore_RealBinaryEntrypoints_RoundTrips` (full real round trip: seed a real project row,
  back it up, restore it into a fresh temp root, independently re-read the real row back out of the restored
  database), `TestRunBackup_MissingFlags_ReturnsUsageError`, `TestRunRestore_MissingFlags_ReturnsUsageError`,
  `TestRunRestore_RefusesAnAlreadyMaterializedTempRoot`.
- No CI wiring needed: `cmd/aw-maintenance` and `internal/app/maintenance` are both plain Go packages the
  existing `contract` job's `go test ./...` already covers in full.

### Verify

- Real, manual end-to-end run of the actual compiled binary (stronger than `go test` alone): built
  `aw-maintenance`, seeded a real project row into a real sqlite database via a throwaway in-module scratch
  program, ran `aw-maintenance backup` against it, then ran `aw-maintenance restore` against that backup's own
  output into a fresh temp root, then independently re-opened the restored database and read the row back out
  — real output confirmed: `real project found in restored DB: {ID:p1 Name:p1 Status:ACTIVE Version:1}`. The
  scratch program and all its temp artifacts were deleted immediately after.
- `go build ./...`, `go vet ./...`: clean across the whole repo.
- `go test ./internal/app/maintenance/... -v` and `-count=3`: all 3 tests pass, stable across 3 consecutive
  runs.
- `go test ./cmd/aw-maintenance/... -v`: all 4 tests pass, including the real backup→restore round trip
  through the actual production entrypoints.
- `internal/archtest` full suite (including `TestCompositionRootIsCmdAwNotCmdAgentkit` and
  `TestDomainAppNeverImportAdapters`): clean — confirms the new standalone binary and the new
  port-based `internal/app/maintenance` package both stay architecturally sound.
- `go test -count=1 ./...` (full repo, every package): clean except one isolated, non-reproducible local
  flake, `TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing` in `internal/app/workerpool` — a package this
  task never touches. Reran that single test 5x in isolation immediately after: 5/5 pass, confirming it was
  local timing contention from the long full-suite run, not a real regression.

## V8-07 — Performance budgets and large-state checks

### Context

`docs/design/10-v8-alpha-hardening.md` V8-07 (ROADMAP-§6, depends on V8-01) asks for: "đo trước khi phát
hành, không lấy số lecture làm SLO" (measure before releasing, never treat an arbitrary number as an SLO) —
Thực hiện: baseline project/task/event/artifact sizes; measure startup, Kanban, task detail, projection
rebuild, scheduler latency, UI large graph/diff; record hardware/profile then freeze a numeric threshold with
owner/reason before measuring the final release candidate. Verify: a reproducible benchmark report;
pre-frozen threshold pass/fail, never set after seeing the RC. Completion bar: no unbounded query/render/
memory path anywhere in the Alpha workload.

No pre-existing numeric SLO exists anywhere in this repo's docs (checked every design doc mentioning
startup/latency/performance) — this task is the first to define and freeze any of these numbers, which is
consistent with its own "don't take an arbitrary number as SLO" framing: there was nothing to check against
before this task ran.

Research corrected two wrong initial assumptions before any code was written — both caught by reading the
real, current code rather than trusting a package's own doc comment, matching this session's own repeated
lesson (V8-06's `CLI_LOCAL` finding, V8-05's `RetentionRawOutputTemp` finding) that a doc comment can go
stale once a LATER task lands without updating an EARLIER package's own "not yet built" note:
- `internal/app/projectionrebuild`'s own package doc comment says the actual rebuild worker is "V6-09A, a
  separate, not-yet-built task" this package has no dependency on. Grepping every real (non-test) reference
  to `ProjectionRebuildJobKind` found `internal/app/projectionrebuildworker` (a real, complete
  `ExecuteProjectionRebuild` implementing the full SNAPSHOTTING→BUILDING→CUTTING_OVER→SUCCEEDED state
  machine) IS real and already wired into `cmd/aw/worker.go`'s own real job registry — V6-09A was, in fact,
  already built; only that one doc comment (and `projectionrebuildworker`'s own `Handler` doc comment, which
  separately claims "no established which task owns real worker-process wiring pattern exists yet" — also
  stale by the same grep) never got updated. This meant "measure projection rebuild" was a real, achievable
  benchmark, not an N/A gap as first assumed.
- `internal/delivery/httpapi/kanban/list.go`'s own handler (`handleListKanban`) calls
  `ports.ProjectionRepository.ListProjectionRows` unconditionally on every single page request, loading and
  decoding EVERY projection row this project's own generation currently has before applying the requested
  page's own limit/cursor in memory — list.go's own doc comment already explains why (an unordered-UUID sort
  key needs the whole row set to pin a consistent `UpperWatermark`). This is a genuine O(project size) cost
  per Kanban page request, a real finding matching this task's own "no unbounded ... path" framing — though,
  as the Decision section below explains, the actual bar is "not worse than linear," which this handler
  meets.
- By contrast, `detail.go`'s own `resolveWorkspaceSetIDForDetail` doc comment already states the single-
  WorkItem detail route deliberately never scans the whole project the way `list.go` does — confirmed as a
  real positive control below.
- `internal/adapters/sqlite/scheduling.go`'s own `ClaimJob` filters on `state = 'AVAILABLE'` under a
  documented partial index (migration 0023) — a completed/historical job, no matter how old or how many
  exist, is never a row the query even inspects. Confirmed as a second real positive control below.

### Decision

**A scaling-ratio assertion, not an absolute wall-clock threshold, is this task's own frozen numeric bar for
every backend measurement.** This session's own CI investigations (see `agent-kit-ci-known-flakes` memory,
most recently PR #131's three consecutive full-run reruns each hitting a DIFFERENT deadline-sensitive test
under runner contention) already proved absolute wall-clock assertions on a shared CI runner are a real flake
source, independent of any actual regression. A scaling ratio between two real, seeded data sizes (typically
10x apart) is hardware-independent modulo noise and still directly operationalizes "unbounded" in a testable
way: a purely linear O(n) path predicts a ratio near the data-size multiplier itself, while a real O(n²)-or-
worse regression would blow that up by roughly the SQUARE of the multiplier — leaving a wide, safely-
distinguishable gap to set a frozen threshold inside. Every threshold below is owned by "V8-07 task (this
session, 2026-09-29)"; reason is recorded next to each one in its own test file's doc comment, and all of
them explicitly note they must be revisited before the final Alpha release candidate is measured, per this
task's own "không đặt ngưỡng sau khi xem RC" line.

Five real, seeded-at-scale benchmarks, one per named area, each living in the package that already owns the
real code path under test (matching this session's own established "reuse the closest existing real fixture"
convention) rather than one new mega-package duplicating fixtures V6/V8 already built:

1. **Startup** (`internal/integration/v6accept/performance_budget_test.go`,
   `TestV8PerformanceBudget_ColdStartLatency`) — real `aw serve`+`aw worker` cold-start-to-ready latency,
   using the package's own real-process stack; `builtBinaries` warmed up OUTSIDE the timed section so one-
   time compile cost never pollutes the measurement. Threshold: < 20s (measured locally: 916ms) — a large,
   deliberate multiple over the local baseline specifically to survive the same CI-contention class this
   session has proven real, not a tuned target.
2. **Kanban** (`internal/delivery/httpapi/kanban/performance_budget_test.go`,
   `TestV8PerformanceBudget_KanbanListLatencyScalesBoundedWithProjectSize`) — seeds 200 then 2000 synthetic
   flat projection rows directly (real sqlite, real `httpapi.Server`, no real WorkItem needed since list.go
   never touches `tx.Work()`), measures real first-page GET latency (minimum of 3 samples) at each size.
   Threshold: ratio < 30x for a 10x data increase (measured locally: 9.12x — confirms the full-project-scan
   documented above is genuinely linear, not quadratic; NOT fixed in this task — a full pagination-layer
   rewrite of a deliberate existing design is out of this hardening task's own scope, and the completion bar
   is "not unbounded," not "no full scan ever").
3. **Task detail** (same file, `TestV8PerformanceBudget_WorkItemDetailLatencyStaysFlatAsProjectGrows`) — one
   real target WorkItem's own detail-route latency measured before and after 1800 unrelated synthetic rows
   are added to the same project. Threshold: ratio < 3x for a 10x growth in unrelated rows (measured locally:
   0.98x — confirms detail.go's own "never scan the whole project" claim holds under real, growing scale).
4. **Scheduler latency** (`internal/app/workerpool/performance_budget_test.go`,
   `TestV8PerformanceBudget_ClaimLatencyStaysBoundedAsHistoricalJobsAccumulate`) — claims+completes 100 then
   5000 real jobs to build up historical SUCCEEDED rows, then measures a FRESH job's own `ClaimJob` latency at
   each historical volume. Threshold: ratio < 5x for a 50x historical row-count increase (measured locally:
   0.97x — confirms the `state='AVAILABLE'` partial index keeps claim latency independent of table history,
   the real risk an audit-trail-forever (`ADR-017`) durable-jobs table could otherwise pose over an
   installation's lifetime).
5. **Projection rebuild** (`internal/app/projectionrebuildworker/performance_budget_test.go`,
   `TestV8PerformanceBudget_FullRebuildLatencyScalesBoundedWithEventCount`) — a real end-to-end rebuild
   (bootstrap path, W0=0, so `BUILDING` replays the WHOLE seeded event journal — the real worst case for a
   given event count) driven by ONE real `ExecuteProjectionRebuild` call (its own internal loop already
   drives every BUILDING round to completion regardless of event count) at 500 then 5000 seeded events.
   Threshold: ratio < 30x for a 10x event-count increase (measured locally: 9.74x — confirms the bounded
   `ReplayGenerationBatch` round loop is genuinely linear).

**UI large graph/diff — explicitly deferred, not measured in this task.** No Playwright perf harness exists
anywhere in `web/e2e` today, and this session's own memory (`agent-kit-ci-known-flakes`) already documents
`web/e2e` as a recurring source of reload-poll flakes — building a new, heavier browser-perf suite under this
task's own remaining time budget risked exactly the kind of low-value, high-flake-risk addition this
session's CI investigations have repeatedly had to spend time unwinding elsewhere. Spawned a follow-up task
(`task_0d99ca87`, "Add Playwright perf smoke for large workflow graph/diff") rather than fabricate a
rubber-stamp measurement, mirroring V8-02's own precedent for a partially-closed task (explicitly acknowledge
the gap, spawn a scoped follow-up, close what IS real now).

### Execution

- `internal/integration/v6accept/performance_budget_test.go` (new): `TestV8PerformanceBudget_ColdStartLatency`.
- `internal/delivery/httpapi/kanban/performance_budget_test.go` (new): `seedManyFlatCards`,
  `measureKanbanListLatency`, `TestV8PerformanceBudget_KanbanListLatencyScalesBoundedWithProjectSize`,
  `TestV8PerformanceBudget_WorkItemDetailLatencyStaysFlatAsProjectGrows`.
- `internal/app/workerpool/performance_budget_test.go` (new): `drainAndCompleteJobs`,
  `measureFreshClaimLatency`, `TestV8PerformanceBudget_ClaimLatencyStaysBoundedAsHistoricalJobsAccumulate`.
- `internal/app/projectionrebuildworker/performance_budget_test.go` (new): `appendManyEvents`,
  `measureFullRebuildLatency`, `TestV8PerformanceBudget_FullRebuildLatencyScalesBoundedWithEventCount`.
- No CI wiring needed: all four new files land inside packages the existing `contract` job (`go test
  ./...`) or the existing `v6-acceptance` job (for the opt-in `v6accept` package) already run in full.
- Follow-up spawned (not part of this PR's diff): `task_0d99ca87` for the UI large graph/diff measurement.

### Verify

- `go build ./...`, `go vet ./...`: clean across the whole repo.
- Each new test run individually with `-v`, confirming real measured numbers (all logged via `t.Logf` as this
  task's own "reproducible benchmark report" — rerunnable any time, not a one-off captured document):
  cold-start 916ms (threshold 20s); Kanban list ratio 9.12x/10x (threshold 30x); WorkItem detail ratio
  0.98x/10x (threshold 3x); ClaimJob ratio 0.97x/50x (threshold 5x); projection rebuild ratio 9.74x/10x
  (threshold 30x) — every one comfortably inside its own frozen threshold, and every ratio close to (Kanban,
  rebuild) or well below (detail, claim) what pure linear scaling would predict, positively confirming no
  worse-than-linear path in any of the five measured areas.
- `go test -count=1 ./...` (full repo): clean except two isolated, non-reproducible local flakes in packages
  this task never touches — `TestSPK04FaultAfterProcessExitMutatingAttemptBecomesIndeterminate`
  (`internal/adapters/sqlite`, real error string "durable job lease is no longer authoritative" — already a
  documented recurring lease-race string per `agent-kit-ci-known-flakes` memory) and
  `TestAppendConversationAttachment_SameKeyConcurrency_TwoIdenticalRetriesRacing` (`internal/app/message`,
  already a documented recurring flake in the same memory file). Reran both individually with `-count=3`
  immediately after: 3/3 clean for each, confirming local machine contention (this session had just run two
  back-to-back heavy scale benchmarks — 5100 durable jobs and 5500 domain events — immediately beforehand),
  not a real regression.
