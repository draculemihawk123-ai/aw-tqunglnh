package spikeacceptance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// runSPK08Scenario closes SPK-08: two workers race to acquire the write
// lease for the same repository workspace, at least 100 times, against a
// real SQLite database. Exactly one must win and one must get
// ErrWriteLeaseConflict every iteration, and each winning fence token must
// be strictly greater than the previous one — proving there is never a
// window where two workers both hold a valid write grant for the same key.
func runSPK08Scenario(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
	started := time.Now().UTC()
	const (
		projectID             = "spk08-project"
		familyID              = "spk08-family"
		workItemID            = "spk08-work-item"
		workspaceSetID        = "spk08-workspace-set"
		repositoryID          = "spk08-repo"
		repositoryWorkspaceID = "spk08-rw"
		nodeRunID             = domainruntime.NodeRunID("spk08-node-run")
		iterations            = 100
	)

	tempDir, err := os.MkdirTemp("", "spk08-*")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk08: create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)
	store, err := sqlite.Open(ctx, filepath.Join(tempDir, "agentkit.db"))
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk08: open sqlite: %w", err)
	}
	defer store.Close()

	if err := sqlite.SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
		return SPKResult{}, fmt.Errorf("spk08: seed owners: %w", err)
	}
	if err := sqlite.SeedFixtureRepositoryWorkspace(ctx, store, projectID, familyID, workspaceSetID, repositoryID, repositoryWorkspaceID); err != nil {
		return SPKResult{}, fmt.Errorf("spk08: seed repository workspace: %w", err)
	}
	definition := workflow.WorkflowDefinition{
		ID: "spk08-definition", Name: "SPK-08 fixture workflow", Status: workflow.DefinitionActive, Version: 1,
	}
	version, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: "spk08-v1", VersionNumber: 1, Document: spk01WorkflowDocumentV1(),
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "v1", Hash: "sha256:spk08-v1"},
		}},
		PublishedBy: "spk08-scenario", PublishedAt: started,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk08: compile workflow version: %w", err)
	}
	if _, err := store.PublishWorkflowVersion(ctx, definition, version); err != nil {
		return SPKResult{}, fmt.Errorf("spk08: publish workflow version: %w", err)
	}
	run, err := domainruntime.NewWorkflowRun("spk08-run", projectID, workItemID, version, familyID, 1, []byte(`{}`))
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk08: new workflow run: %w", err)
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		return SPKResult{}, fmt.Errorf("spk08: start workflow run: %w", err)
	}
	attemptA := domainruntime.ExecutionAttemptID("spk08-attempt-a")
	attemptB := domainruntime.ExecutionAttemptID("spk08-attempt-b")
	if err := sqlite.SeedFixtureNodeRunAndAttempt(ctx, store, "spk08-run", nodeRunID, attemptA); err != nil {
		return SPKResult{}, fmt.Errorf("spk08: seed attempt-a: %w", err)
	}
	if err := sqlite.SeedFixtureSecondAttempt(ctx, store, nodeRunID, attemptB); err != nil {
		return SPKResult{}, fmt.Errorf("spk08: seed attempt-b: %w", err)
	}

	target := ports.WorkspaceLeaseTarget{
		RepositoryID: repositoryID, RepositoryWorkspaceID: repositoryWorkspaceID, Generation: 1,
	}

	var lastFence uint64
	timeline := make([]map[string]any, 0, iterations)
	for iteration := 0; iteration < iterations; iteration++ {
		jobA := fmt.Sprintf("spk08-job-a-%d", iteration)
		jobB := fmt.Sprintf("spk08-job-b-%d", iteration)
		if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(jobA), ProjectID: projectID, Kind: "EXECUTE_NODE",
			AggregateType: "ExecutionAttempt", AggregateID: string(attemptA),
			MaxClaims: 1, IdempotencyKey: jobA,
		}); err != nil {
			return SPKResult{}, fmt.Errorf("spk08: enqueue job a iteration %d: %w", iteration, err)
		}
		if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(jobB), ProjectID: projectID, Kind: "EXECUTE_NODE",
			AggregateType: "ExecutionAttempt", AggregateID: string(attemptB),
			MaxClaims: 1, IdempotencyKey: jobB,
		}); err != nil {
			return SPKResult{}, fmt.Errorf("spk08: enqueue job b iteration %d: %w", iteration, err)
		}
		_, leaseA, err := store.ClaimJob(ctx, "worker-a", 30*time.Second)
		if err != nil {
			return SPKResult{}, fmt.Errorf("spk08: claim job a iteration %d: %w", iteration, err)
		}
		_, leaseB, err := store.ClaimJob(ctx, "worker-b", 30*time.Second)
		if err != nil {
			return SPKResult{}, fmt.Errorf("spk08: claim job b iteration %d: %w", iteration, err)
		}

		type raceResult struct {
			grants []ports.WriteLeaseGrant
			err    error
		}
		results := make(chan raceResult, 2)
		start := make(chan struct{})
		for _, race := range []struct {
			lease     ports.JobLease
			attemptID domainruntime.ExecutionAttemptID
		}{{leaseA, attemptA}, {leaseB, attemptB}} {
			race := race
			go func() {
				<-start
				grants, err := store.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
					JobLease: race.lease, AttemptID: race.attemptID,
					Targets: []ports.WorkspaceLeaseTarget{target}, TTL: 5 * time.Second,
				})
				results <- raceResult{grants: grants, err: err}
			}()
		}
		close(start)

		var winner []ports.WriteLeaseGrant
		conflicts := 0
		for count := 0; count < 2; count++ {
			result := <-results
			switch {
			case result.err == nil:
				if winner != nil {
					return SPKResult{}, fmt.Errorf("spk08: iteration %d produced two winners", iteration)
				}
				winner = result.grants
			case errors.Is(result.err, ports.ErrWriteLeaseConflict):
				conflicts++
			default:
				return SPKResult{}, fmt.Errorf("spk08: iteration %d unexpected acquire error: %w", iteration, result.err)
			}
		}
		if len(winner) != 1 || conflicts != 1 {
			return SPKResult{}, fmt.Errorf("spk08: iteration %d winners=%d conflicts=%d, want 1/1", iteration, len(winner), conflicts)
		}
		if winner[0].FenceToken <= lastFence {
			return SPKResult{}, fmt.Errorf("spk08: iteration %d fence=%d, want greater than %d", iteration, winner[0].FenceToken, lastFence)
		}
		lastFence = winner[0].FenceToken
		if err := store.ReleaseWriteLeases(ctx, winner); err != nil {
			return SPKResult{}, fmt.Errorf("spk08: iteration %d release: %w", iteration, err)
		}
		if iteration < 5 || iteration == iterations-1 {
			timeline = append(timeline, map[string]any{"iteration": iteration, "fenceToken": winner[0].FenceToken})
		}
	}

	assertion := Assertion{
		Name:   fmt.Sprintf("%d race iterations each had exactly one winner and strictly increasing fence tokens", iterations),
		Passed: true,
	}
	leasesArtifact, err := sc.Bundle.PutJSON("runtime/leases.json", map[string]any{
		"iterations": iterations, "finalFenceToken": lastFence, "sampleTimeline": timeline,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk08: write leases evidence: %w", err)
	}

	return SPKResult{
		Passed:     true,
		Assertions: []Assertion{assertion},
		Correlation: CorrelationIDs{
			ProjectID: projectID, FamilyID: familyID, RunID: "spk08-run",
			NodeRunID: string(nodeRunID), AttemptID: string(attemptA),
			RepositoryID: repositoryID, Revision: "base-rev",
		},
		Platform:  Platform{GOOS: goruntime.GOOS, GOARCH: goruntime.GOARCH},
		Timing:    Timing{StartedAt: started, EndedAt: time.Now().UTC()},
		Artifacts: []ArtifactRef{{Kind: ArtifactKindRuntime, Artifact: leasesArtifact}},
	}, nil
}
