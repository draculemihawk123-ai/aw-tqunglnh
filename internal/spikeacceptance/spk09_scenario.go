package spikeacceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// runSPK09Scenario closes SPK-09 against a real SQLite database: W1 acquires
// write lease token T1, its job lease expires, W2 takes over with token
// T2 > T1. Because the fresh worker cannot prove W1's mutation never landed
// (worker.ReconcileMutatingAttempt), the workspace is quarantined: cleanup
// and any new writer are blocked from that point on, even for W2's own
// fully-current grant. Reconciliation recreates the workspace at generation
// 2; only that generation can receive a writer or reach the finalizer's
// evidence authority.
func runSPK09Scenario(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
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
		projectID             = "spk09-project"
		familyID              = "spk09-family"
		workItemID            = "spk09-work-item"
		workspaceSetID        = "spk09-workspace-set"
		repositoryID          = "spk09-repo"
		repositoryWorkspaceID = "spk09-rw"
		runID                 = domainruntime.WorkflowRunID("spk09-run")
	)

	tempDir, err := os.MkdirTemp("", "spk09-*")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)
	store, err := sqlite.Open(ctx, filepath.Join(tempDir, "agentkit.db"))
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: open sqlite: %w", err)
	}
	defer store.Close()

	if err := sqlite.SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
		return SPKResult{}, fmt.Errorf("spk09: seed owners: %w", err)
	}
	if err := sqlite.SeedFixtureRepositoryWorkspace(ctx, store, projectID, familyID, workspaceSetID, repositoryID, repositoryWorkspaceID); err != nil {
		return SPKResult{}, fmt.Errorf("spk09: seed repository workspace: %w", err)
	}
	attempt1 := domainruntime.ExecutionAttemptID("spk09-attempt-1")
	attempt2 := domainruntime.ExecutionAttemptID("spk09-attempt-2")
	nodeRunID := domainruntime.NodeRunID("spk09-node-run")

	definition := workflow.WorkflowDefinition{
		ID: "spk09-definition", Name: "SPK-09 fixture workflow", Status: workflow.DefinitionActive, Version: 1,
	}
	version, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: "spk09-v1", VersionNumber: 1, Document: spk01WorkflowDocumentV1(),
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "v1", Hash: "sha256:spk09-v1"},
		}},
		PublishedBy: "spk09-scenario", PublishedAt: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: compile workflow version: %w", err)
	}
	if _, err := store.PublishWorkflowVersion(ctx, definition, version); err != nil {
		return SPKResult{}, fmt.Errorf("spk09: publish workflow version: %w", err)
	}
	run, err := domainruntime.NewWorkflowRun(runID, projectID, workItemID, version, familyID, 1, json.RawMessage(`{}`))
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: new workflow run: %w", err)
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		return SPKResult{}, fmt.Errorf("spk09: start workflow run: %w", err)
	}
	if err := sqlite.SeedFixtureNodeRunAndAttempt(ctx, store, runID, nodeRunID, attempt1); err != nil {
		return SPKResult{}, fmt.Errorf("spk09: seed attempt-1: %w", err)
	}
	if err := sqlite.SeedFixtureSecondAttempt(ctx, store, nodeRunID, attempt2); err != nil {
		return SPKResult{}, fmt.Errorf("spk09: seed attempt-2: %w", err)
	}
	if _, err := store.CompareAndSwapWorkflowRun(ctx, ports.WorkflowRunTransition{
		RunID: runID, ExpectedState: domainruntime.WorkflowRunCreated, ExpectedVersion: 1,
		NextState: domainruntime.WorkflowRunRunning, SharedState: json.RawMessage(`{}`),
		OccurredAt: time.Date(2026, 8, 28, 0, 0, 1, 0, time.UTC),
	}); err != nil {
		return SPKResult{}, fmt.Errorf("spk09: move run to RUNNING: %w", err)
	}
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: "spk09-job-run1", ProjectID: projectID, Kind: "FINALIZE_RUN",
		AggregateType: "WorkflowRun", AggregateID: string(runID),
		MaxClaims: 5, IdempotencyKey: "spk09-job-run1-key",
	}); err != nil {
		return SPKResult{}, fmt.Errorf("spk09: enqueue finalize job: %w", err)
	}

	_, w1JobLease, err := store.ClaimJob(ctx, "worker-1", 300*time.Millisecond)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: W1 claim job: %w", err)
	}
	w1Grants, err := store.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
		JobLease: w1JobLease, AttemptID: attempt1,
		Targets: []ports.WorkspaceLeaseTarget{{RepositoryID: repositoryID, RepositoryWorkspaceID: repositoryWorkspaceID, Generation: 1}},
		TTL:     300 * time.Millisecond,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: W1 acquire write lease: %w", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		recovered, err := store.RecoverExpiredJobs(ctx)
		if err != nil {
			return SPKResult{}, fmt.Errorf("spk09: recover expired jobs: %w", err)
		}
		if recovered == 1 {
			break
		}
		if time.Now().After(deadline) {
			return SPKResult{}, errors.New("spk09: durable job lease did not expire before deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}

	_, w2JobLease, err := store.ClaimJob(ctx, "worker-2", 5*time.Second)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: W2 claim job: %w", err)
	}
	record("takeover job token is strictly greater than the stale one", w2JobLease.Token > w1JobLease.Token,
		fmt.Sprintf("t1=%d t2=%d", w1JobLease.Token, w2JobLease.Token))

	staleErr := store.ValidateWriteLease(ctx, w1Grants[0])
	record("W1 waking up and reusing T1 is fenced", errors.Is(staleErr, ports.ErrWriteLeaseLost), fmt.Sprintf("error=%v", staleErr))

	w2Grants, err := store.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
		JobLease: w2JobLease, AttemptID: attempt2,
		Targets: []ports.WorkspaceLeaseTarget{{RepositoryID: repositoryID, RepositoryWorkspaceID: repositoryWorkspaceID, Generation: 1}},
		TTL:     5 * time.Second,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: W2 acquire write lease: %w", err)
	}
	record("W2's takeover fence token exceeds W1's", len(w2Grants) == 1 && w2Grants[0].FenceToken > w1Grants[0].FenceToken, "")

	verdict, err := worker.ReconcileMutatingAttempt("user-base", "user-base-mutated-by-w1")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: reconcile mutating attempt: %w", err)
	}
	record("reconciliation reports mutation observed", verdict == worker.ReconciliationMutationObserved, string(verdict))

	quarantineErr := store.QuarantineRepositoryWorkspace(ctx, ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: repositoryWorkspaceID, ExpectedVersion: 1, Reason: string(verdict),
		EventID: "spk09-event-quarantine", CorrelationID: "spk09", OccurredAt: time.Date(2026, 8, 28, 0, 1, 0, 0, time.UTC),
	})
	record("workspace quarantine succeeds", quarantineErr == nil, fmt.Sprintf("error=%v", quarantineErr))

	releaseErr := store.ReleaseRepositoryWorkspace(ctx, ports.ReleaseRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: repositoryWorkspaceID, ExpectedVersion: 2,
		EventID: "spk09-event-release-blocked", CorrelationID: "spk09", OccurredAt: time.Date(2026, 8, 28, 0, 1, 1, 0, time.UTC),
	})
	record("cleanup/release is blocked while quarantined", errors.Is(releaseErr, ports.ErrWorkspaceQuarantined), fmt.Sprintf("error=%v", releaseErr))

	_, blockedAcquireErr := store.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
		JobLease: w2JobLease, AttemptID: attempt2,
		Targets: []ports.WorkspaceLeaseTarget{{RepositoryID: repositoryID, RepositoryWorkspaceID: repositoryWorkspaceID, Generation: 1}},
		TTL:     5 * time.Second,
	})
	record("new writer is blocked while quarantined", errors.Is(blockedAcquireErr, ports.ErrWriteLeaseConflict), fmt.Sprintf("error=%v", blockedAcquireErr))

	recreated, err := store.RecreateRepositoryWorkspace(ctx, ports.RecreateRepositoryWorkspaceRequest{
		PreviousRepositoryWorkspaceID: repositoryWorkspaceID, PreviousExpectedVersion: 2,
		NewRepositoryWorkspaceID: repositoryWorkspaceID + "-gen2",
		Locator:                  "opaque:" + repositoryID + "-gen2", BranchRef: "agentkit/" + repositoryID + "-gen2",
		BaseRevision: "base-rev", EventID: "spk09-event-recreate", CorrelationID: "spk09",
		OccurredAt: time.Date(2026, 8, 28, 0, 1, 2, 0, time.UTC),
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: recreate workspace: %w", err)
	}
	record("recreate produces generation 2, READY", recreated.Generation == 2, fmt.Sprintf("generation=%d state=%s", recreated.Generation, recreated.State))

	finalizer, err := worker.NewFinalizer(store)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: build finalizer: %w", err)
	}
	_, staleFinalizeErr := finalizer.Finalize(ctx, worker.FinalizationInput{
		Finalization: ports.WorkerWorkflowRunFinalization{
			Transition: ports.WorkflowRunTransition{
				RunID: runID, ExpectedState: domainruntime.WorkflowRunRunning, ExpectedVersion: 2,
				NextState: domainruntime.WorkflowRunSucceeded, SharedState: json.RawMessage(`{"result":"stale-generation"}`),
				OccurredAt: time.Date(2026, 8, 28, 0, 1, 3, 0, time.UTC),
			},
			JobLease: w2JobLease, WriteLeases: []ports.WriteLeaseGrant{w2Grants[0]},
			EventID: "spk09-event-finalize-stale", CorrelationID: "spk09",
		},
	})
	record("finalize with the quarantined generation's grant is rejected", errors.Is(staleFinalizeErr, ports.ErrWriteLeaseLost), fmt.Sprintf("error=%v", staleFinalizeErr))

	currentGrants, err := store.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
		JobLease: w2JobLease, AttemptID: attempt2,
		Targets: []ports.WorkspaceLeaseTarget{{RepositoryID: repositoryID, RepositoryWorkspaceID: recreated.ID, Generation: 2}},
		TTL:     5 * time.Second,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: acquire write lease on recreated generation: %w", err)
	}
	finalized, err := finalizer.Finalize(ctx, worker.FinalizationInput{
		Finalization: ports.WorkerWorkflowRunFinalization{
			Transition: ports.WorkflowRunTransition{
				RunID: runID, ExpectedState: domainruntime.WorkflowRunRunning, ExpectedVersion: 2,
				NextState: domainruntime.WorkflowRunSucceeded, SharedState: json.RawMessage(`{"result":"reconciled"}`),
				OccurredAt: time.Date(2026, 8, 28, 0, 1, 4, 0, time.UTC),
			},
			JobLease: w2JobLease, WriteLeases: currentGrants,
			EventID: "spk09-event-finalize-current", CorrelationID: "spk09",
		},
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: finalize with current generation: %w", err)
	}
	record("finalize with the current generation succeeds", finalized.State == domainruntime.WorkflowRunSucceeded, string(finalized.State))

	revisionsArtifact, err := sc.Bundle.PutJSON("workspace/revisions-after.json", map[string]any{
		"previousGeneration": 1, "currentGeneration": recreated.Generation, "repositoryId": repositoryID,
		"revision": "base-rev",
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: write workspace evidence: %w", err)
	}
	leasesArtifact, err := sc.Bundle.PutJSON("runtime/leases.json", map[string]any{
		"staleFenceToken": w1Grants[0].FenceToken, "currentFenceToken": currentGrants[0].FenceToken,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk09: write runtime evidence: %w", err)
	}

	return SPKResult{
		Passed:     passed,
		Assertions: assertions,
		Correlation: CorrelationIDs{
			ProjectID: projectID, FamilyID: familyID, RunID: string(runID),
			NodeRunID: string(nodeRunID), AttemptID: string(attempt2),
			RepositoryID: repositoryID, Revision: "base-rev",
		},
		Platform: Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Timing:   Timing{StartedAt: started, EndedAt: time.Now().UTC()},
		Artifacts: []ArtifactRef{
			{Kind: ArtifactKindWorkspace, Artifact: revisionsArtifact},
			{Kind: ArtifactKindRuntime, Artifact: leasesArtifact},
		},
	}, nil
}
