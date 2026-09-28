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
