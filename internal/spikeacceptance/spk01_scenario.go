package spikeacceptance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// runSPK01Scenario closes SPK-01 against a real SQLite database: publishing
// the same canonical content twice under different candidate identities
// deduplicates to one immutable version; a semantically different v2
// publishes as a distinct version; reusing v1's version id with different
// content is rejected; and the reloaded version is byte-identical to what
// was published.
func runSPK01Scenario(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
	started := time.Now().UTC()
	var assertions []Assertion
	passed := true
	record := func(name string, ok bool, detail string) {
		assertions = append(assertions, Assertion{Name: name, Passed: ok, Detail: detail})
		if !ok {
			passed = false
		}
	}

	tempDir, err := os.MkdirTemp("", "spk01-*")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk01: create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)
	store, err := sqlite.Open(ctx, filepath.Join(tempDir, "agentkit.db"))
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk01: open sqlite: %w", err)
	}
	defer store.Close()

	definition := workflow.WorkflowDefinition{
		ID: "spk01-definition", Name: "SPK-01 fixture workflow", Status: workflow.DefinitionActive, Version: 1,
	}
	documentV1 := spk01WorkflowDocumentV1()
	v1Candidate, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: "spk01-v1", VersionNumber: 1, Document: documentV1,
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "v1", Hash: "sha256:spk01-v1"},
		}},
		PublishedBy: "spk01-scenario", PublishedAt: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk01: compile v1: %w", err)
	}
	publishedV1, err := store.PublishWorkflowVersion(ctx, definition, v1Candidate)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk01: publish v1: %w", err)
	}
	record("publish creates an immutable version with a stable hash",
		publishedV1.ID() == v1Candidate.ID() && publishedV1.ContentHash() == v1Candidate.ContentHash(), "")

	retryCandidate, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: "spk01-v1-retry", VersionNumber: 2, Document: documentV1,
		Dependencies: v1Candidate.Dependencies(),
		PublishedBy:  "spk01-scenario", PublishedAt: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk01: compile retry candidate: %w", err)
	}
	dedupedResult, err := store.PublishWorkflowVersion(ctx, definition, retryCandidate)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk01: publish retry candidate: %w", err)
	}
	record("republishing identical content deduplicates to the existing version",
		dedupedResult.ID() == publishedV1.ID() && dedupedResult.ContentHash() == publishedV1.ContentHash(),
		fmt.Sprintf("existing=%s got=%s", publishedV1.ID(), dedupedResult.ID()))

	documentV2 := spk01WorkflowDocumentV2()
	v2Candidate, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: "spk01-v2", VersionNumber: 2, Document: documentV2,
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "v2", Hash: "sha256:spk01-v2"},
		}},
		PublishedBy: "spk01-scenario", PublishedAt: time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC),
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk01: compile v2: %w", err)
	}
	publishedV2, err := store.PublishWorkflowVersion(ctx, definition, v2Candidate)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk01: publish v2: %w", err)
	}
	record("a semantically different version publishes as a distinct version",
		publishedV2.ID() != publishedV1.ID() && publishedV2.ContentHash() != publishedV1.ContentHash(), "")

	// A distinct dependency pin from both v1 and v2 keeps this candidate's
	// canonical content hash unique: reusing v1 or v2's exact content here
	// would make PublishWorkflowVersion's idempotent-by-content-hash check
	// return that existing version before ever reaching the "same id,
	// different hash" conflict this assertion means to exercise.
	conflictingCandidate, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: v1Candidate.ID(), VersionNumber: 3, Document: documentV1,
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "v1-conflict", Hash: "sha256:spk01-v1-conflict"},
		}},
		PublishedBy: "spk01-scenario", PublishedAt: time.Date(2026, 8, 28, 2, 0, 0, 0, time.UTC),
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk01: compile conflicting candidate: %w", err)
	}
	_, publishErr := store.PublishWorkflowVersion(ctx, definition, conflictingCandidate)
	record("reusing an immutable version id with different content is rejected",
		errors.Is(publishErr, ports.ErrImmutableVersionConflict), fmt.Sprintf("error=%v", publishErr))

	reloadedV1, err := store.LoadWorkflowVersion(ctx, v1Candidate.ID())
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk01: reload v1: %w", err)
	}
	record("reloaded version has identical canonical content and hash",
		reloadedV1.ContentHash() == v1Candidate.ContentHash() &&
			string(reloadedV1.CanonicalContent()) == string(v1Candidate.CanonicalContent()), "")

	sourceHashArtifact, err := sc.Bundle.PutJSON("workflow/source-hash.json", map[string]string{
		"v1VersionId": string(v1Candidate.ID()), "v1ContentHash": v1Candidate.ContentHash(),
		"v2VersionId": string(v2Candidate.ID()), "v2ContentHash": v2Candidate.ContentHash(),
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk01: write source-hash evidence: %w", err)
	}
	publishedVersionArtifact, err := sc.Bundle.PutJSON("workflow/published-version.json", map[string]any{
		"assertions": assertions,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk01: write published-version evidence: %w", err)
	}

	return SPKResult{
		Passed:     passed,
		Assertions: assertions,
		Platform:   Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Timing:     Timing{StartedAt: started, EndedAt: time.Now().UTC()},
		Artifacts: []ArtifactRef{
			{Kind: ArtifactKindWorkflow, Artifact: sourceHashArtifact},
			{Kind: ArtifactKindWorkflow, Artifact: publishedVersionArtifact},
		},
	}, nil
}

func spk01WorkflowDocumentV1() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{{Key: "start-to-end", From: "start", Outcome: "next", To: "end"}},
	}
}

func spk01WorkflowDocumentV2() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "implement", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef: definition.DependencyPin{
					Kind:         definition.KindAgentProfile,
					DefinitionID: "agent-default",
					VersionID:    "agent-default-v1",
				},
			}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-implement", From: "start", Outcome: "next", To: "implement"},
			{Key: "implement-to-end", From: "implement", Outcome: "done", To: "end"},
		},
	}
}
