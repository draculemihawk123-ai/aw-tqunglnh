package spikeacceptance

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/evidence"
)

func allRequiredResults() []SPKResult {
	ids := RequiredSPKIDs()
	results := make([]SPKResult, 0, len(ids))
	for _, id := range ids {
		results = append(results, SPKResult{SPKID: id, Passed: true})
	}
	return results
}

func TestNewSPKManifestAcceptsExactlyRequiredIDs(t *testing.T) {
	now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	manifest, err := NewSPKManifest("full-suite-1", now, allRequiredResults())
	if err != nil {
		t.Fatalf("NewSPKManifest() error = %v, want nil", err)
	}
	if manifest.SuiteID != "full-suite-1" || !manifest.GeneratedAt.Equal(now) {
		t.Fatalf("unexpected manifest header: %+v", manifest)
	}
	if len(manifest.Results) != len(RequiredSPKIDs()) {
		t.Fatalf("got %d results, want %d", len(manifest.Results), len(RequiredSPKIDs()))
	}
}

func TestNewSPKManifestRejectsMissingIDs(t *testing.T) {
	results := allRequiredResults()
	results = results[:len(results)-1] // drop SPK-14, the last RequiredSPKIDs() entry
	_, err := NewSPKManifest("full-suite-missing", time.Now().UTC(), results)
	if !errors.Is(err, ErrSPKIDMissing) {
		t.Fatalf("NewSPKManifest() error = %v, want ErrSPKIDMissing", err)
	}
	if !strings.Contains(err.Error(), string(SPK14)) {
		t.Fatalf("error %q does not name the missing id %q", err.Error(), SPK14)
	}
}

func TestNewSPKManifestRejectsDuplicateIDs(t *testing.T) {
	results := append(allRequiredResults(), SPKResult{SPKID: SPK01, Passed: true})
	_, err := NewSPKManifest("full-suite-duplicate", time.Now().UTC(), results)
	if !errors.Is(err, ErrSPKIDDuplicate) {
		t.Fatalf("NewSPKManifest() error = %v, want ErrSPKIDDuplicate", err)
	}
	if !strings.Contains(err.Error(), string(SPK01)) {
		t.Fatalf("error %q does not name the duplicated id %q", err.Error(), SPK01)
	}
}

func TestNewSPKManifestRejectsUnknownID(t *testing.T) {
	results := allRequiredResults()
	results[len(results)-1] = SPKResult{SPKID: SPKID("SPK-99"), Passed: true} // replaces SPK-14
	_, err := NewSPKManifest("full-suite-unknown", time.Now().UTC(), results)
	if !errors.Is(err, ErrSPKIDUnknown) {
		t.Fatalf("NewSPKManifest() error = %v, want ErrSPKIDUnknown", err)
	}
	if !errors.Is(err, ErrSPKIDMissing) {
		t.Fatalf("NewSPKManifest() error = %v, want also ErrSPKIDMissing for SPK-14", err)
	}
}

func fullyCorrelatedIDs() CorrelationIDs {
	return CorrelationIDs{
		ProjectID: "project-1", FamilyID: "family-1", RunID: "run-1",
		NodeRunID: "node-run-1", AttemptID: "attempt-1",
		RepositoryID: "repo-1", Revision: "abc123",
	}
}

func blankCorrelationField(correlation *CorrelationIDs, field string) {
	switch field {
	case "projectId":
		correlation.ProjectID = ""
	case "familyId":
		correlation.FamilyID = ""
	case "runId":
		correlation.RunID = ""
	case "nodeRunId":
		correlation.NodeRunID = ""
	case "attemptId":
		correlation.AttemptID = ""
	case "repositoryId":
		correlation.RepositoryID = ""
	case "revision":
		correlation.Revision = ""
	default:
		panic("unknown correlation field: " + field)
	}
}

// resultWithEveryArtifactKind builds one SPKResult that carries all six
// evidence-bundle artifact kinds from docs/spikes/01-go-core-spike-plan.md
// §12, so a test can exercise every kind's correlation requirement (or
// absence of one) at once.
func resultWithEveryArtifactKind(id SPKID, correlation CorrelationIDs) SPKResult {
	return SPKResult{
		SPKID:       id,
		Passed:      true,
		Correlation: correlation,
		Artifacts: []ArtifactRef{
			{Kind: ArtifactKindWorkflow, Artifact: evidence.Artifact{Path: "workflow/source-hash.json"}},
			{Kind: ArtifactKindRuntime, Artifact: evidence.Artifact{Path: "runtime/run.json"}},
			{Kind: ArtifactKindWorkspace, Artifact: evidence.Artifact{Path: "workspace/workspace-set.json"}},
			{Kind: ArtifactKindProviders, Artifact: evidence.Artifact{Path: "providers/normalized.jsonl"}},
			{Kind: ArtifactKindProcesses, Artifact: evidence.Artifact{Path: "processes/timeline.jsonl"}},
			{Kind: ArtifactKindAssertions, Artifact: evidence.Artifact{Path: "assertions/report.json"}},
		},
	}
}

// resultsSubstituting returns allRequiredResults() with one entry replaced,
// so tests can focus on a single SPK's correlation behavior while keeping a
// manifest-shaped (all fourteen ids present) input to NewSPKManifest.
func resultsSubstituting(target SPKID, replacement SPKResult) []SPKResult {
	results := allRequiredResults()
	for i := range results {
		if results[i].SPKID == target {
			results[i] = replacement
		}
	}
	return results
}

func TestNewSPKManifestAcceptsFullyCorrelatedArtifacts(t *testing.T) {
	results := resultsSubstituting(SPK09, resultWithEveryArtifactKind(SPK09, fullyCorrelatedIDs()))
	if _, err := NewSPKManifest("full-suite-correlated", time.Now().UTC(), results); err != nil {
		t.Fatalf("NewSPKManifest() error = %v, want nil", err)
	}
}

func TestNewSPKManifestRejectsMissingExecutionCorrelation(t *testing.T) {
	for _, field := range []string{"projectId", "familyId", "runId", "nodeRunId", "attemptId"} {
		t.Run(field, func(t *testing.T) {
			correlation := fullyCorrelatedIDs()
			blankCorrelationField(&correlation, field)
			results := resultsSubstituting(SPK09, resultWithEveryArtifactKind(SPK09, correlation))
			_, err := NewSPKManifest("full-suite-missing-"+field, time.Now().UTC(), results)
			if !errors.Is(err, ErrCorrelationMissing) {
				t.Fatalf("NewSPKManifest() error = %v, want ErrCorrelationMissing", err)
			}
			if !strings.Contains(err.Error(), field) {
				t.Fatalf("error %q does not name missing field %q", err.Error(), field)
			}
		})
	}
}

func TestNewSPKManifestRejectsMissingWorkspaceCorrelation(t *testing.T) {
	for _, field := range []string{"repositoryId", "revision"} {
		t.Run(field, func(t *testing.T) {
			correlation := fullyCorrelatedIDs()
			blankCorrelationField(&correlation, field)
			results := resultsSubstituting(SPK09, resultWithEveryArtifactKind(SPK09, correlation))
			_, err := NewSPKManifest("full-suite-missing-"+field, time.Now().UTC(), results)
			if !errors.Is(err, ErrCorrelationMissing) {
				t.Fatalf("NewSPKManifest() error = %v, want ErrCorrelationMissing", err)
			}
			if !strings.Contains(err.Error(), field) {
				t.Fatalf("error %q does not name missing field %q", err.Error(), field)
			}
		})
	}
}

// TestNewSPKManifestIgnoresCorrelationForWorkflowAndAssertionsOnlyResults
// proves the kind-specific requirement is not accidentally universal: a
// result whose only artifacts are workflow/assertions (SPK-01's shape: a
// publish/hash check that predates any run) needs no run/family/repository
// correlation at all.
func TestNewSPKManifestIgnoresCorrelationForWorkflowAndAssertionsOnlyResults(t *testing.T) {
	result := SPKResult{
		SPKID:  SPK01,
		Passed: true,
		Artifacts: []ArtifactRef{
			{Kind: ArtifactKindWorkflow, Artifact: evidence.Artifact{Path: "workflow/source-hash.json"}},
			{Kind: ArtifactKindAssertions, Artifact: evidence.Artifact{Path: "assertions/report.json"}},
		},
	}
	results := resultsSubstituting(SPK01, result)
	if _, err := NewSPKManifest("full-suite-workflow-only", time.Now().UTC(), results); err != nil {
		t.Fatalf("NewSPKManifest() error = %v, want nil (workflow/assertions carry no correlation requirement)", err)
	}
}
