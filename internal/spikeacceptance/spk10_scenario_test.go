package spikeacceptance

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/evidence"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// This file is the hotfix for the SPK-13 gate failure diagnosed against
// PR #22 (V4-12A): SPK-10's own scenario deliberately races two goroutines
// to CompareAndSwap the same WorkflowRun with no fixed winner (RUNNING or
// CANCELLED are both legitimate), but it used to embed that literal winning
// state into the Assertion.Detail string and the runtime/transitions.jsonl
// artifact SemanticDiff hashes byte-for-byte — so a genuinely legitimate
// race outcome (Windows happens to land on RUNNING, Linux on CANCELLED)
// tripped SPK-13's own strict cross-platform equality check. Confirmed with
// the user before writing this fix: keep the race itself genuinely
// nondeterministic (no fake tie-break), keep SemanticDiff itself strict (no
// broad per-SPK allowlist that could mask a real regression), and instead
// normalize SPK-10's own reported evidence into a platform-stable
// invariant (winners/stale/finalVersion/finalStateAllowed) that never
// carries the concrete RUNNING/CANCELLED value.

// TestSPK10TransitionsEvidence_RunningAndCancelled_ProduceIdenticalEvidence
// is this fix's own central proof: two otherwise-identical race outcomes
// that differ ONLY in which state legitimately won produce byte-identical
// evidence.
func TestSPK10TransitionsEvidence_RunningAndCancelled_ProduceIdenticalEvidence(t *testing.T) {
	runningDetail, runningPayload, runningConsistent := spk10TransitionsEvidence(1, 1, 2, domainruntime.WorkflowRunRunning)
	cancelledDetail, cancelledPayload, cancelledConsistent := spk10TransitionsEvidence(1, 1, 2, domainruntime.WorkflowRunCancelled)

	if !runningConsistent || !cancelledConsistent {
		t.Fatalf("consistent = (%t, %t), want both true (both are legitimate race winners)", runningConsistent, cancelledConsistent)
	}
	if runningDetail != cancelledDetail {
		t.Fatalf("detail differs by winning state: RUNNING=%q CANCELLED=%q, want identical", runningDetail, cancelledDetail)
	}
	if !reflect.DeepEqual(runningPayload, cancelledPayload) {
		t.Fatalf("payload differs by winning state: RUNNING=%+v CANCELLED=%+v, want identical", runningPayload, cancelledPayload)
	}
	if strings.Contains(runningDetail, "RUNNING") || strings.Contains(runningDetail, "CANCELLED") {
		t.Fatalf("detail = %q, must never leak the concrete winning state", runningDetail)
	}
}

// TestSPK10TransitionsEvidence_StateOutsideAllowedSet_NotConsistent proves
// the normalization does not widen what counts as a valid outcome: a state
// that is neither RUNNING nor CANCELLED (the two legitimate CAS winners)
// must still fail, exactly as before this fix.
func TestSPK10TransitionsEvidence_StateOutsideAllowedSet_NotConsistent(t *testing.T) {
	detail, payload, consistent := spk10TransitionsEvidence(1, 1, 2, domainruntime.WorkflowRunCreated)
	if consistent {
		t.Fatal("consistent = true for state CREATED, want false (not a legitimate CAS winner)")
	}
	if payload["finalStateAllowed"] != false {
		t.Fatalf("payload[finalStateAllowed] = %v, want false", payload["finalStateAllowed"])
	}
	if !strings.Contains(detail, "finalStateAllowed=false") {
		t.Fatalf("detail = %q, want it to report finalStateAllowed=false", detail)
	}
}

// TestSPK10TransitionsEvidence_WrongVersion_NotConsistent proves a lost
// update (final version not advanced to exactly 2) still fails even when
// the winning state itself is one of the two legitimate values.
func TestSPK10TransitionsEvidence_WrongVersion_NotConsistent(t *testing.T) {
	_, _, consistent := spk10TransitionsEvidence(1, 1, 1, domainruntime.WorkflowRunRunning)
	if consistent {
		t.Fatal("consistent = true for finalVersion=1, want false (exactly one transition must have committed, advancing to version 2)")
	}
}

// spk10GoldenResult builds a full SPKResult for SPK-10 the same way
// runSPK10Scenario does, using a REAL evidence.Bundle (so the artifact's own
// SHA256/Size are computed identically to production) rather than
// hand-rolling the hash — goos is only informational here (Platform is
// never compared by SemanticDiff).
func spk10GoldenResult(t *testing.T, goos string, winnersDetail string, consistencyDetail string, payload map[string]any) SPKResult {
	t.Helper()
	bundle, err := evidence.CreateAt(t.TempDir(), "spk10-"+goos, time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("evidence.CreateAt: %v", err)
	}
	artifact, err := bundle.PutJSON("runtime/transitions.jsonl", payload)
	if err != nil {
		t.Fatalf("PutJSON: %v", err)
	}
	consistent := payload["finalStateAllowed"] == true && payload["finalVersion"] == uint64(2)
	winnersOK := payload["winners"] == 1 && payload["stale"] == 1
	return SPKResult{
		SPKID:  SPK10,
		Passed: consistent && winnersOK,
		Assertions: []Assertion{
			{Name: "concurrent CompareAndSwap on the same expected version has exactly one winner", Passed: winnersOK, Detail: winnersDetail},
			{Name: "final run state reflects exactly one committed transition, no lost update", Passed: consistent, Detail: consistencyDetail},
		},
		Correlation: CorrelationIDs{
			ProjectID: "spk10-project", FamilyID: "spk10-family", RunID: "spk10-run",
			NodeRunID: "spk10-node-run", AttemptID: "spk10-attempt",
		},
		Platform:  Platform{GOOS: goos, GOARCH: "amd64"},
		Timing:    Timing{StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()},
		Artifacts: []ArtifactRef{{Kind: ArtifactKindRuntime, Artifact: artifact}},
	}
}

// TestSemanticDiff_SPK10_WindowsRunningVsLinuxCancelled_AreEqual is the
// acceptance test the user asked for directly: a Windows run that legitimately
// won RUNNING and a Linux run that legitimately won CANCELLED must produce
// ZERO differences under SPK-13's own byte-exact SemanticDiff, now that the
// concrete winning state is never part of the compared evidence.
func TestSemanticDiff_SPK10_WindowsRunningVsLinuxCancelled_AreEqual(t *testing.T) {
	runningDetail, runningPayload, _ := spk10TransitionsEvidence(1, 1, 2, domainruntime.WorkflowRunRunning)
	cancelledDetail, cancelledPayload, _ := spk10TransitionsEvidence(1, 1, 2, domainruntime.WorkflowRunCancelled)

	windows := spk10GoldenResult(t, "windows", "winners=1 stale=1", runningDetail, runningPayload)
	linux := spk10GoldenResult(t, "linux", "winners=1 stale=1", cancelledDetail, cancelledPayload)

	if diffs := SemanticDiff(windows, linux); len(diffs) != 0 {
		t.Fatalf("SemanticDiff(SPK-10 windows-RUNNING vs linux-CANCELLED) = %v, want none", diffs)
	}
}

// TestSemanticDiff_SPK10_GenuineInvariantViolation_StillReported proves the
// normalization does not weaken SPK-13's own gate: a REAL divergence (here,
// a second CAS call incorrectly also winning — winners=2 is never a
// legitimate outcome of this race) must still be reported, not swallowed by
// the finalStateAllowed normalization.
func TestSemanticDiff_SPK10_GenuineInvariantViolation_StillReported(t *testing.T) {
	goodDetail, goodPayload, _ := spk10TransitionsEvidence(1, 1, 2, domainruntime.WorkflowRunRunning)
	buggyDetail, buggyPayload, _ := spk10TransitionsEvidence(2, 1, 2, domainruntime.WorkflowRunRunning)

	windows := spk10GoldenResult(t, "windows", "winners=1 stale=1", goodDetail, goodPayload)
	linux := spk10GoldenResult(t, "linux", "winners=2 stale=1", buggyDetail, buggyPayload)

	diffs := SemanticDiff(windows, linux)
	if len(diffs) == 0 {
		t.Fatal("SemanticDiff(good vs winners=2 bug) = none, want at least one reported difference")
	}
}

// TestRunSPK10Scenario_RealRace_NeverLeaksConcreteWinningState is an actual
// end-to-end run of the modified production scenario (not just the
// extracted helper): regardless of which goroutine's CAS actually wins on
// this machine, the returned Assertion.Detail must never contain the
// literal "RUNNING" or "CANCELLED" — the exact leak that broke SPK-13.
func TestRunSPK10Scenario_RealRace_NeverLeaksConcreteWinningState(t *testing.T) {
	bundle, err := evidence.CreateAt(t.TempDir(), "spk10-real", time.Now().UTC())
	if err != nil {
		t.Fatalf("evidence.CreateAt: %v", err)
	}
	result, err := runSPK10Scenario(context.Background(), ScenarioContext{SPKID: SPK10, Bundle: bundle})
	if err != nil {
		t.Fatalf("runSPK10Scenario: %v", err)
	}
	if !result.Passed {
		t.Fatalf("result.Passed = false, want true: %+v", result.Assertions)
	}
	for _, a := range result.Assertions {
		if strings.Contains(a.Detail, "RUNNING") || strings.Contains(a.Detail, "CANCELLED") {
			t.Fatalf("assertion %q detail = %q, must never leak the concrete winning state", a.Name, a.Detail)
		}
	}
}
