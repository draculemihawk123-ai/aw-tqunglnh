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
	return func(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
		started := time.Now().UTC()
		assertion := Assertion{
			Name:   "full acceptance scenario runnable through the SPK registry",
			Passed: false,
			Detail: detail,
		}
		artifact, err := sc.Bundle.PutJSON("assertions/report.json", assertion)
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

// DefaultScenarios is the production SPK-01..SPK-14 registration list.
// Twelve of the fourteen SPKs run a real Arrange/Act/Assert scenario against
// this codebase's real adapters (docs/design/02-v0-spike-verdict.md
// V0-10A/V0-10B); they are not wrappers around `go test`'s exit code — each
// one performs its own arrange/act/assert and writes its own evidence. Two
// are deliberately left as notYetProven, each for a reason specific to that
// SPK, not "no dedicated task":
//
//   - SPK-03: the interrupted execution_attempts row is left at RUNNING
//     instead of transitioning to LOST/INDETERMINATE (see
//     internal/adapters/sqlite/crash_resume_checkpoint_integration_test.go's
//     own "Known limitation" doc comment, confirmed against
//     TerminateInterruptedAttempt/ClassifyInterruptedAttempt: both exist,
//     but no crash-flow integration test calls them). Closing this is its
//     own task (V0-10C), deliberately not bundled into V0-10A/V0-10B's
//     wiring work.
//   - SPK-13: a single platform run cannot conclude Windows/Linux parity by
//     itself. This registry's own result is intentionally
//     "PENDING_PEER_PLATFORM"; the authoritative SPK-13 result comes from
//     the cross-platform semantic-diff job (V0-11), never from a
//     single-platform notYetProven-style guess.
func DefaultScenarios() []ScenarioEntry {
	return []ScenarioEntry{
		{SPK01, runSPK01Scenario},
		{SPK02, runSPK02Scenario},
		{SPK03, notYetProven(SPK03, "interrupted execution_attempts row is left at RUNNING instead of transitioning to LOST/INDETERMINATE after a hard crash (see crash_resume_checkpoint_integration_test.go's own doc comment); TerminateInterruptedAttempt/ClassifyInterruptedAttempt exist but no crash-flow integration test calls them yet — closing this is its own task (V0-10C), run before V0-11/V0-12 are declared closed")},
		{SPK04, runSPK04Scenario},
		{SPK05, runSPK05Scenario},
		{SPK06, runSPK06Scenario},
		{SPK07, runSPK07Scenario},
		{SPK08, runSPK08Scenario},
		{SPK09, runSPK09Scenario},
		{SPK10, runSPK10Scenario},
		{SPK11, runSPK11Scenario},
		{SPK12, runSPK12Scenario},
		{SPK13, notYetProven(SPK13, "PENDING_PEER_PLATFORM: a single-platform run cannot conclude Windows/Linux parity by itself; the authoritative SPK-13 result is produced by the cross-platform semantic-diff job once both platform manifests exist (docs/design/02-v0-spike-verdict.md V0-11)")},
		{SPK14, runSPK14Scenario},
	}
}
