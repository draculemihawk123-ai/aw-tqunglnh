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
