package spikeacceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// runSPK10Scenario closes SPK-10: two processes race to CompareAndSwap the
// same WorkflowRun from the same expected (state, version) with different
// next states, against a real SQLite database. Exactly one must win; the
// other must get ErrOptimisticConflict, never a lost update or two
// committed transitions.
func runSPK10Scenario(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
	started := time.Now().UTC()
	const (
		projectID  = "spk10-project"
		familyID   = "spk10-family"
		workItemID = "spk10-work-item"
		runID      = domainruntime.WorkflowRunID("spk10-run")
	)

	tempDir, err := os.MkdirTemp("", "spk10-*")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk10: create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)
	store, err := sqlite.Open(ctx, filepath.Join(tempDir, "agentkit.db"))
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk10: open sqlite: %w", err)
	}
	defer store.Close()

	if err := sqlite.SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
		return SPKResult{}, fmt.Errorf("spk10: seed owners: %w", err)
	}
	definition := workflow.WorkflowDefinition{
		ID: "spk10-definition", Name: "SPK-10 fixture workflow", Status: workflow.DefinitionActive, Version: 1,
	}
	version, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: "spk10-v1", VersionNumber: 1, Document: spk01WorkflowDocumentV1(),
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "v1", Hash: "sha256:spk10-v1"},
		}},
		PublishedBy: "spk10-scenario", PublishedAt: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk10: compile v1: %w", err)
	}
	if _, err := store.PublishWorkflowVersion(ctx, definition, version); err != nil {
		return SPKResult{}, fmt.Errorf("spk10: publish v1: %w", err)
	}
	run, err := domainruntime.NewWorkflowRun(runID, projectID, workItemID, version, familyID, 1, json.RawMessage(`{"winner":null}`))
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk10: new workflow run: %w", err)
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		return SPKResult{}, fmt.Errorf("spk10: start workflow run: %w", err)
	}
	nodeRunID := domainruntime.NodeRunID("spk10-node-run")
	attemptID := domainruntime.ExecutionAttemptID("spk10-attempt")
	if err := sqlite.SeedFixtureNodeRunAndAttempt(ctx, store, runID, nodeRunID, attemptID); err != nil {
		return SPKResult{}, fmt.Errorf("spk10: seed node run/attempt: %w", err)
	}

	transitions := []ports.WorkflowRunTransition{
		{
			RunID: runID, ExpectedState: domainruntime.WorkflowRunCreated, ExpectedVersion: 1,
			NextState: domainruntime.WorkflowRunRunning, SharedState: json.RawMessage(`{"winner":"worker-a"}`),
			OccurredAt: time.Date(2026, 8, 28, 0, 1, 0, 0, time.UTC),
		},
		{
			RunID: runID, ExpectedState: domainruntime.WorkflowRunCreated, ExpectedVersion: 1,
			NextState: domainruntime.WorkflowRunCancelled, SharedState: json.RawMessage(`{"winner":"worker-b"}`),
			OccurredAt: time.Date(2026, 8, 28, 0, 1, 1, 0, time.UTC),
		},
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, transition := range transitions {
		transition := transition
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := store.CompareAndSwapWorkflowRun(ctx, transition)
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	winners, stale := 0, 0
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ports.ErrOptimisticConflict):
			stale++
		default:
			return SPKResult{}, fmt.Errorf("spk10: unexpected CAS error: %w", err)
		}
	}
	passed := winners == 1 && stale == 1
	assertion := Assertion{
		Name:   "concurrent CompareAndSwap on the same expected version has exactly one winner",
		Passed: passed,
		Detail: fmt.Sprintf("winners=%d stale=%d", winners, stale),
	}

	final, err := store.LoadWorkflowRun(ctx, runID)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk10: load final run: %w", err)
	}
	consistent := final.Version == 2 &&
		(final.State == domainruntime.WorkflowRunRunning || final.State == domainruntime.WorkflowRunCancelled)
	consistencyAssertion := Assertion{
		Name:   "final run state reflects exactly one committed transition, no lost update",
		Passed: consistent,
		Detail: fmt.Sprintf("version=%d state=%s", final.Version, final.State),
	}
	if !consistent {
		passed = false
	}

	artifact, err := sc.Bundle.PutJSON("runtime/transitions.jsonl", map[string]any{
		"winners": winners, "stale": stale, "finalVersion": final.Version, "finalState": final.State,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk10: write evidence: %w", err)
	}

	return SPKResult{
		Passed:     passed,
		Assertions: []Assertion{assertion, consistencyAssertion},
		Correlation: CorrelationIDs{
			ProjectID: projectID, FamilyID: familyID, RunID: string(runID),
			NodeRunID: string(nodeRunID), AttemptID: string(attemptID),
		},
		Platform:  Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Timing:    Timing{StartedAt: started, EndedAt: time.Now().UTC()},
		Artifacts: []ArtifactRef{{Kind: ArtifactKindRuntime, Artifact: artifact}},
	}, nil
}
