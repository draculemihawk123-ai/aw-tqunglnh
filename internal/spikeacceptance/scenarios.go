package spikeacceptance

import (
	"context"
	"fmt"
	"runtime"
	"time"
)

// notYetProven builds a ScenarioFunc that honestly reports an SPK as not yet
// provable through this registry. It is a real, callable handler (not a
// no-op: it runs, writes a real assertions artifact into its own sealed
// evidence bundle, stamps real platform/timing data and returns a
// well-formed SPKResult) — it simply has nothing further to assert until the
// cited blocking task lands. See docs/spikes/02-go-core-spike-report.md §5
// for the current per-SPK status this text is grounded in.
func notYetProven(id SPKID, detail string) ScenarioFunc {
	return func(ctx context.Context, writer EvidenceWriter) (SPKResult, error) {
		started := time.Now().UTC()
		assertion := Assertion{
			Name:   "full acceptance scenario runnable through the SPK registry",
			Passed: false,
			Detail: detail,
		}
		artifact, err := writer.PutJSON("assertions/report.json", assertion)
		if err != nil {
			return SPKResult{}, fmt.Errorf("write %s assertions report: %w", id, err)
		}
		return SPKResult{
			SPKID:      id,
			Passed:     false,
			Assertions: []Assertion{assertion},
			Platform:   Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
			Timing:     Timing{StartedAt: started, EndedAt: time.Now().UTC()},
			Artifacts:  []ArtifactRef{{Kind: ArtifactKindAssertions, Artifact: artifact}},
		}, nil
	}
}

// DefaultScenarios is the production SPK-01..SPK-14 registration list. Every
// SPK currently reports Passed: false: none of the fourteen has a full
// Arrange/Assert/Evidence acceptance scenario reachable through this
// registry yet — closing each gap is the job of the dependent V0 tasks named
// below, not this task (V0-01A scope is the registry/dispatcher mechanism
// only). Registering a scenario here that fabricated Passed: true from the
// primitive-level evidence already in the spike report would be exactly the
// "local unit pass as verdict" substitution docs/design/02-v0-spike-verdict.md
// forbids.
func DefaultScenarios() []ScenarioEntry {
	return []ScenarioEntry{
		{SPK01, notYetProven(SPK01, "canonical publish/hash/immutable-load proven at domain+SQLite unit-test level only; not yet re-run as its own SPK-01 acceptance scenario through this registry (no dedicated V0 task yet)")},
		{SPK02, notYetProven(SPK02, "version pin across restart/publish proven at domain+SQLite unit-test level only; not yet re-run as its own SPK-02 acceptance scenario through this registry (no dedicated V0 task yet)")},
		{SPK03, notYetProven(SPK03, "hard-crash checkpoint/context recovery proven at persistence/fresh-start level only; not yet joined into one Process.Kill acceptance flow — blocked on V0-02")},
		{SPK04, notYetProven(SPK04, "2 of 6 crash-transaction-boundary fault points covered locally; full six-boundary registry/evidence not complete — blocked on V0-03, V0-04, V0-04A")},
		{SPK05, notYetProven(SPK05, "multi-repository WorkspaceSet/child-reuse proven at adapter unit-test level only; not yet re-run as its own SPK-05 acceptance scenario through this registry (no dedicated V0 task yet)")},
		{SPK06, notYetProven(SPK06, "root-family worktree isolation proven at adapter unit-test level only; not yet re-run as its own SPK-06 acceptance scenario through this registry (no dedicated V0 task yet)")},
		{SPK07, notYetProven(SPK07, "scope guard blocks before fenced SQLite mutation; mount isolation + process-provider end-to-end not complete — blocked on V0-06")},
		{SPK08, notYetProven(SPK08, "100-iteration same-repository lease race proven at adapter unit-test level only; not yet re-run as its own SPK-08 acceptance scenario through this registry (see also V0-12 for the CI race-iteration gate)")},
		{SPK09, notYetProven(SPK09, "job/write token fencing proven at adapter unit-test level; full workspace recreate/quarantine recovery not complete — blocked on V0-07")},
		{SPK10, notYetProven(SPK10, "runtime CAS conflict proven at adapter unit-test level only; not yet re-run as its own SPK-10 acceptance scenario through this registry (no dedicated V0 task yet)")},
		{SPK11, notYetProven(SPK11, "fake Claude/Codex process contract proven at adapter unit-test level only; not yet re-run as its own SPK-11 acceptance scenario through this registry (no dedicated V0 task yet)")},
		{SPK12, notYetProven(SPK12, "ContextSnapshot/checkpoint survives restart and replacement always calls Start; invalid-provider-session-in-crash-flow injection not complete — blocked on V0-05")},
		{SPK13, notYetProven(SPK13, "Windows contract passes locally; Linux semantic run and normalized diff not complete — blocked on V0-10, V0-11")},
		{SPK14, notYetProven(SPK14, "evidence bundle verify/tamper detection proven at adapter level; not yet joined to a real run bundle — blocked on V0-09")},
	}
}
