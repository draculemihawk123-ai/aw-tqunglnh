package spikeacceptance

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/evidence"
)

// goldenWindowsResult and goldenLinuxResult are the golden fixtures V0-10
// asks for: two SPKResults meant to represent the same underlying SPK-09 run
// captured once on Windows and once on Linux. They differ only in exactly
// the ways docs/design/02-v0-spike-verdict.md V0-10 allowlists: Platform,
// Timing and a process artifact's PID-bearing content.
func goldenWindowsResult() SPKResult {
	return SPKResult{
		SPKID:  SPK09,
		Passed: true,
		Assertions: []Assertion{
			{Name: "stale generation fenced at finalize", Passed: true, Detail: "quarantined generation rejected by evidence authority"},
		},
		Correlation: CorrelationIDs{
			ProjectID: "project-1", FamilyID: "family-1", RunID: "run-1",
			NodeRunID: "node-run-1", AttemptID: "attempt-1",
			RepositoryID: "repo-user", Revision: "abc123",
		},
		Platform: Platform{GOOS: "windows", GOARCH: "amd64"},
		Timing: Timing{
			StartedAt: time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC),
			EndedAt:   time.Date(2026, 8, 31, 10, 0, 1, 0, time.UTC),
		},
		Artifacts: []ArtifactRef{
			{Kind: ArtifactKindWorkflow, Artifact: evidence.Artifact{Path: "workflow/source-hash.json", SHA256: "sha256:aaa", Size: 10}},
			{Kind: ArtifactKindRuntime, Artifact: evidence.Artifact{Path: "runtime/run.json", SHA256: "sha256:bbb", Size: 20}},
			{Kind: ArtifactKindProcesses, Artifact: evidence.Artifact{Path: "processes/timeline.jsonl", SHA256: "sha256:windows-pid-1234", Size: 40}},
		},
	}
}

func goldenLinuxResult() SPKResult {
	result := goldenWindowsResult()
	result.Platform = Platform{GOOS: "linux", GOARCH: "amd64"}
	result.Timing = Timing{
		StartedAt: time.Date(2026, 8, 31, 10, 5, 0, 0, time.UTC),
		EndedAt:   time.Date(2026, 8, 31, 10, 5, 2, 0, time.UTC),
	}
	artifacts := append([]ArtifactRef(nil), result.Artifacts...)
	// A real, different PID and byte count from a genuinely separate process
	// on another OS — exactly the ArtifactKindProcesses case V0-10 allowlists.
	artifacts[2].Artifact.SHA256 = "sha256:linux-pid-5678"
	artifacts[2].Artifact.Size = 38
	result.Artifacts = artifacts
	return result
}

func TestSemanticDiffTreatsEquivalentWindowsAndLinuxResultsAsEqual(t *testing.T) {
	windows := goldenWindowsResult()
	linux := goldenLinuxResult()
	if windows.Platform.GOOS == linux.Platform.GOOS {
		t.Fatal("golden fixtures must actually differ in GOOS")
	}
	if diffs := SemanticDiff(windows, linux); len(diffs) != 0 {
		t.Fatalf("SemanticDiff(equivalent Windows/Linux results) = %v, want none", diffs)
	}
}

func TestSemanticDiffReportsIntentionalDomainMismatch(t *testing.T) {
	windows := goldenWindowsResult()
	linux := goldenLinuxResult()
	linux.Passed = false // a genuine behavioral divergence, not a platform artifact
	diffs := SemanticDiff(windows, linux)
	if len(diffs) != 1 || diffs[0].Field != "passed" {
		t.Fatalf("SemanticDiff(domain mismatch) = %v, want exactly one 'passed' difference", diffs)
	}
}

func TestSemanticDiffReportsCorrelationMismatch(t *testing.T) {
	windows := goldenWindowsResult()
	linux := goldenLinuxResult()
	linux.Correlation.Revision = "different-revision"
	diffs := SemanticDiff(windows, linux)
	if len(diffs) != 1 || diffs[0].Field != "correlation.revision" {
		t.Fatalf("SemanticDiff(revision mismatch) = %v, want exactly one 'correlation.revision' difference", diffs)
	}
}

func TestSemanticDiffReportsAssertionDetailMismatch(t *testing.T) {
	windows := goldenWindowsResult()
	linux := goldenLinuxResult()
	assertions := append([]Assertion(nil), linux.Assertions...)
	assertions[0].Detail = "a materially different explanation"
	linux.Assertions = assertions
	diffs := SemanticDiff(windows, linux)
	if len(diffs) != 1 || diffs[0].Field != "assertions[0].detail" {
		t.Fatalf("SemanticDiff(assertion detail mismatch) = %v, want exactly one 'assertions[0].detail' difference", diffs)
	}
}

// TestSemanticDiffReportsNonProcessArtifactHashMismatch proves the
// process-artifact allowlist is narrow: a workflow artifact's hash (which
// must be platform-invariant, per "giữ ID/hash" in V0-10) is not silently
// normalized away the way a processes-kind artifact's hash is.
func TestSemanticDiffReportsNonProcessArtifactHashMismatch(t *testing.T) {
	windows := goldenWindowsResult()
	linux := goldenLinuxResult()
	artifacts := append([]ArtifactRef(nil), linux.Artifacts...)
	artifacts[0].Artifact.SHA256 = "sha256:different-workflow-hash"
	linux.Artifacts = artifacts
	diffs := SemanticDiff(windows, linux)
	if len(diffs) != 1 || diffs[0].Field != "artifacts[workflow:workflow/source-hash.json].sha256" {
		t.Fatalf("SemanticDiff(non-process hash mismatch) = %v, want exactly one workflow artifact sha256 difference", diffs)
	}
}

func TestSemanticDiffReportsMissingArtifact(t *testing.T) {
	windows := goldenWindowsResult()
	linux := goldenLinuxResult()
	linux.Artifacts = []ArtifactRef{linux.Artifacts[0], linux.Artifacts[2]} // drop the runtime artifact
	diffs := SemanticDiff(windows, linux)
	if len(diffs) != 1 || diffs[0].Field != "artifacts[runtime:runtime/run.json]" {
		t.Fatalf("SemanticDiff(missing artifact) = %v, want exactly one missing runtime artifact difference", diffs)
	}
	if diffs[0].A != "present" || diffs[0].B != "absent" {
		t.Fatalf("SemanticDiff(missing artifact) presence = %+v, want present/absent", diffs[0])
	}
}

func TestSemanticDiffIsDeterministic(t *testing.T) {
	windows := goldenWindowsResult()
	linux := goldenLinuxResult()
	linux.Passed = false
	linux.Correlation.Revision = "different-revision"
	first := SemanticDiff(windows, linux)
	second := SemanticDiff(windows, linux)
	if len(first) != len(second) {
		t.Fatalf("SemanticDiff() is not deterministic across calls: %v vs %v", first, second)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("SemanticDiff() produced different order/content across calls: %v vs %v", first, second)
		}
	}
}
