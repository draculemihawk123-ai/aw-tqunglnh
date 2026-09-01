package spikeacceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// runSPK02Scenario closes SPK-02: R1 is started against workflow v1, the
// SQLite store is closed and reopened (a real process-restart stand-in — no
// object survives in memory across the close), v2 is published only after
// the restart, and R1 must still resolve the exact v1 graph/hash it was
// pinned to when it started, both immediately after restart and again after
// v2 exists.
func runSPK02Scenario(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
	started := time.Now().UTC()
	var assertions []Assertion
	passed := true
	record := func(name string, ok bool, detail string) {
		assertions = append(assertions, Assertion{Name: name, Passed: ok, Detail: detail})
		if !ok {
			passed = false
		}
	}

	const (
		projectID  = "spk02-project"
		familyID   = "spk02-family"
		workItemID = "spk02-work-item"
		runID      = domainruntime.WorkflowRunID("spk02-run")
		nodeRunID  = domainruntime.NodeRunID("spk02-node-run")
		attemptID  = domainruntime.ExecutionAttemptID("spk02-attempt")
	)

	tempDir, err := os.MkdirTemp("", "spk02-*")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk02: create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)
	databasePath := filepath.Join(tempDir, "agentkit.db")

	store, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk02: open sqlite: %w", err)
	}
	if err := sqlite.SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
		return SPKResult{}, fmt.Errorf("spk02: seed owners: %w", err)
	}

	definition := workflow.WorkflowDefinition{
		ID: "spk02-definition", Name: "SPK-02 fixture workflow", Status: workflow.DefinitionActive, Version: 1,
	}
	v1, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: "spk02-v1", VersionNumber: 1, Document: spk01WorkflowDocumentV1(),
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "v1", Hash: "sha256:spk02-v1"},
		}},
		PublishedBy: "spk02-scenario", PublishedAt: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk02: compile v1: %w", err)
	}
	if _, err := store.PublishWorkflowVersion(ctx, definition, v1); err != nil {
		return SPKResult{}, fmt.Errorf("spk02: publish v1: %w", err)
	}

	run, err := domainruntime.NewWorkflowRun(runID, projectID, workItemID, v1, familyID, 1, json.RawMessage(`{"checkpoint":"created"}`))
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk02: new workflow run: %w", err)
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		return SPKResult{}, fmt.Errorf("spk02: start workflow run: %w", err)
	}
	if _, err := store.CompareAndSwapWorkflowRun(ctx, ports.WorkflowRunTransition{
		RunID: runID, ExpectedState: domainruntime.WorkflowRunCreated, ExpectedVersion: 1,
		NextState: domainruntime.WorkflowRunRunning, SharedState: json.RawMessage(`{"checkpoint":"dispatched"}`),
		OccurredAt: time.Date(2026, 8, 28, 0, 1, 0, 0, time.UTC),
	}); err != nil {
		return SPKResult{}, fmt.Errorf("spk02: move run to RUNNING: %w", err)
	}
	if err := sqlite.SeedFixtureNodeRunAndAttempt(ctx, store, runID, nodeRunID, attemptID); err != nil {
		return SPKResult{}, fmt.Errorf("spk02: seed node run/attempt: %w", err)
	}
	if err := store.Close(); err != nil {
		return SPKResult{}, fmt.Errorf("spk02: close before restart: %w", err)
	}

	restarted, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk02: reopen after restart: %w", err)
	}
	defer restarted.Close()

	resumed, err := restarted.LoadWorkflowRun(ctx, runID)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk02: load run after restart: %w", err)
	}
	record("run survives restart pinned to v1",
		resumed.WorkflowVersionID == v1.ID() && resumed.WorkflowVersionHash == v1.ContentHash(),
		fmt.Sprintf("pinned=%s/%s want=%s/%s", resumed.WorkflowVersionID, resumed.WorkflowVersionHash, v1.ID(), v1.ContentHash()))

	v2, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: "spk02-v2", VersionNumber: 2, Document: spk01WorkflowDocumentV2(),
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "v2", Hash: "sha256:spk02-v2"},
		}},
		PublishedBy: "spk02-scenario", PublishedAt: time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC),
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk02: compile v2: %w", err)
	}
	if _, err := restarted.PublishWorkflowVersion(ctx, definition, v2); err != nil {
		return SPKResult{}, fmt.Errorf("spk02: publish v2 after restart: %w", err)
	}

	pinnedAfterV2, err := restarted.LoadWorkflowRun(ctx, runID)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk02: load run after v2 publish: %w", err)
	}
	record("run stays pinned to v1 after v2 is published",
		pinnedAfterV2.WorkflowVersionID == v1.ID() && pinnedAfterV2.WorkflowVersionHash == v1.ContentHash(),
		fmt.Sprintf("pinned=%s/%s want=%s/%s", pinnedAfterV2.WorkflowVersionID, pinnedAfterV2.WorkflowVersionHash, v1.ID(), v1.ContentHash()))

	runArtifact, err := sc.Bundle.PutJSON("runtime/run.json", map[string]any{
		"runId": runID, "pinnedVersionId": pinnedAfterV2.WorkflowVersionID,
		"pinnedVersionHash": pinnedAfterV2.WorkflowVersionHash, "state": pinnedAfterV2.State,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk02: write runtime evidence: %w", err)
	}
	versionArtifact, err := sc.Bundle.PutJSON("workflow/published-version.json", map[string]string{
		"v1VersionId": string(v1.ID()), "v1ContentHash": v1.ContentHash(),
		"v2VersionId": string(v2.ID()), "v2ContentHash": v2.ContentHash(),
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk02: write workflow evidence: %w", err)
	}

	return SPKResult{
		Passed:     passed,
		Assertions: assertions,
		Correlation: CorrelationIDs{
			ProjectID: projectID, FamilyID: familyID, RunID: string(runID),
			NodeRunID: string(nodeRunID), AttemptID: string(attemptID),
		},
		Platform: Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Timing:   Timing{StartedAt: started, EndedAt: time.Now().UTC()},
		Artifacts: []ArtifactRef{
			{Kind: ArtifactKindRuntime, Artifact: runArtifact},
			{Kind: ArtifactKindWorkflow, Artifact: versionArtifact},
		},
	}, nil
}
