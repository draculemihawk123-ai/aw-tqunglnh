package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// TestSPK09QuarantineRecreateFencesStaleGeneration closes SPK-09's remaining
// gap: workspace recreate/quarantine recovery. W1 acquires write lease token
// T1 on repository workspace generation 1, then stops heartbeating; after
// TTL, W2 takes over the same durable job with token T2 > T1 and W1's T1
// grant is fenced. Because the fresh worker cannot prove W1's mutation never
// landed (worker.ReconcileMutatingAttempt, closed by V0-03/04), the
// workspace is quarantined: cleanup and any new writer are both blocked from
// that point on, even for W2's own grant, which is fully current by every
// job/lease measure. Reconciliation recreates the workspace at generation 2;
// only that generation can receive a writer or reach the finalizer's
// evidence authority, and generation 1 never becomes writable or
// finalizable again.
func TestSPK09QuarantineRecreateFencesStaleGeneration(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)
	seedValidWorkflowVersion(t, store)
	ctx := context.Background()

	// A run-scoped job stands in for the durable job a real worker holds
	// across both acquiring the write lease and finalizing the run:
	// FinalizeWorkflowRun requires the exact same job lease for both.
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: "job-spk09-run1", ProjectID: "project-1", Kind: "FINALIZE_RUN",
		AggregateType: "WorkflowRun", AggregateID: "run-1",
		MaxClaims: 5, IdempotencyKey: "job-spk09-run1-key",
	}); err != nil {
		t.Fatalf("EnqueueJob() error = %v", err)
	}

	// W1 claims the job with a short baseline TTL — see
	// TestWriteLeaseRequiresItsOriginalActiveJobFence for why 300ms, not a
	// few ms, keeps this deterministic on a busy Windows runner — and
	// acquires write lease token T1 on repo-user generation 1 with the same
	// short TTL, so both naturally expire together.
	_, w1JobLease, err := store.ClaimJob(ctx, "worker-1", 300*time.Millisecond)
	if err != nil {
		t.Fatalf("W1 ClaimJob() error = %v", err)
	}
	w1Grants, err := store.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
		JobLease: w1JobLease, AttemptID: runtime.ExecutionAttemptID("attempt-1"),
		Targets: []ports.WorkspaceLeaseTarget{{
			RepositoryID: "repo-user", RepositoryWorkspaceID: "rw-user", Generation: 1,
		}},
		TTL: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("W1 AcquireWriteLeases() error = %v", err)
	}

	// W1 stops heartbeating; both its job lease and write lease expire.
	waitForRecoveredJob(t, store)

	// W2 takes over the same durable job: T2 > T1.
	_, w2JobLease, err := store.ClaimJob(ctx, "worker-2", 5*time.Second)
	if err != nil {
		t.Fatalf("W2 ClaimJob() error = %v", err)
	}
	if w2JobLease.Token <= w1JobLease.Token {
		t.Fatalf("takeover job token = %d, want greater than %d", w2JobLease.Token, w1JobLease.Token)
	}

	// W1 wakes up and tries to use T1: fenced.
	if err := store.ValidateWriteLease(ctx, w1Grants[0]); !errors.Is(err, ports.ErrWriteLeaseLost) {
		t.Fatalf("stale ValidateWriteLease() error = %v, want ErrWriteLeaseLost", err)
	}

	// W2 legitimately acquires the same generation with its own, currently
	// valid job lease: a grant that is not stale by any job/lease measure.
	w2Grants, err := store.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
		JobLease: w2JobLease, AttemptID: runtime.ExecutionAttemptID("attempt-2"),
		Targets: []ports.WorkspaceLeaseTarget{{
			RepositoryID: "repo-user", RepositoryWorkspaceID: "rw-user", Generation: 1,
		}},
		TTL: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("W2 AcquireWriteLeases() error = %v", err)
	}
	if w2Grants[0].FenceToken <= w1Grants[0].FenceToken {
		t.Fatalf("W2 fence token = %d, want greater than %d", w2Grants[0].FenceToken, w1Grants[0].FenceToken)
	}

	// Whether W1's mutation landed is still unproven: the fresh worker
	// compares the attempt's pinned revision against the workspace's actual
	// current revision and gets a mutation-observed verdict, exactly as it
	// would if W1 had genuinely written something before its lease died.
	verdict, err := worker.ReconcileMutatingAttempt("user-base", "user-base-mutated-by-w1")
	if err != nil {
		t.Fatalf("ReconcileMutatingAttempt() error = %v", err)
	}
	if verdict != worker.ReconciliationMutationObserved {
		t.Fatalf("verdict = %s, want %s", verdict, worker.ReconciliationMutationObserved)
	}

	// Quarantine is the action ReconcileMutatingAttempt deliberately leaves
	// to V0-07: it fences the workspace out from under W2 too, even though
	// W2's grant above is fully current.
	if err := store.QuarantineRepositoryWorkspace(ctx, ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: "rw-user", ExpectedVersion: 1, Reason: string(verdict),
		EventID: "event-spk09-quarantine", CorrelationID: "spk09",
		OccurredAt: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("QuarantineRepositoryWorkspace() error = %v", err)
	}

	// Cleanup/release is blocked while quarantined...
	if err := store.ReleaseRepositoryWorkspace(ctx, ports.ReleaseRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: "rw-user", ExpectedVersion: 2,
		EventID: "event-spk09-release-blocked", CorrelationID: "spk09",
		OccurredAt: time.Date(2026, 8, 31, 0, 0, 1, 0, time.UTC),
	}); !errors.Is(err, ports.ErrWorkspaceQuarantined) {
		t.Fatalf("ReleaseRepositoryWorkspace() error = %v, want ErrWorkspaceQuarantined", err)
	}

	// ...and so is any new write lease on it, even one presenting W2's fully
	// valid, currently-leased job.
	if _, err := store.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
		JobLease: w2JobLease, AttemptID: runtime.ExecutionAttemptID("attempt-2"),
		Targets: []ports.WorkspaceLeaseTarget{{
			RepositoryID: "repo-user", RepositoryWorkspaceID: "rw-user", Generation: 1,
		}},
		TTL: 5 * time.Second,
	}); !errors.Is(err, ports.ErrWriteLeaseConflict) {
		t.Fatalf("AcquireWriteLeases() on quarantined workspace error = %v, want ErrWriteLeaseConflict", err)
	}

	// Reconcile by recreating: generation 2 is a new, independent row.
	recreated, err := store.RecreateRepositoryWorkspace(ctx, ports.RecreateRepositoryWorkspaceRequest{
		PreviousRepositoryWorkspaceID: "rw-user", PreviousExpectedVersion: 2,
		NewRepositoryWorkspaceID: "rw-user-gen2",
		Locator:                  "opaque:user-gen2",
		BranchRef:                "agentkit/family-1/user-gen2",
		BaseRevision:             "user-base",
		EventID:                  "event-spk09-recreate", CorrelationID: "spk09",
		OccurredAt: time.Date(2026, 8, 31, 0, 0, 2, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("RecreateRepositoryWorkspace() error = %v", err)
	}
	if recreated.Generation != 2 || recreated.State != workspace.RepositoryWorkspaceReady {
		t.Fatalf("recreated workspace = %+v, want generation 2 READY", recreated)
	}

	finalizer, err := worker.NewFinalizer(store)
	if err != nil {
		t.Fatalf("NewFinalizer() error = %v", err)
	}

	// W2's earlier grant is fully current by job/lease measures alone, yet
	// it can never reach evidence authority again: its generation is
	// quarantined permanently, even after generation 2 exists.
	_, err = finalizer.Finalize(ctx, worker.FinalizationInput{
		Finalization: ports.WorkerWorkflowRunFinalization{
			Transition: ports.WorkflowRunTransition{
				RunID: "run-1", ExpectedState: runtime.WorkflowRunRunning, ExpectedVersion: 1,
				NextState: runtime.WorkflowRunSucceeded, SharedState: json.RawMessage(`{"result":"stale-generation"}`),
				OccurredAt: time.Date(2026, 8, 31, 0, 0, 3, 0, time.UTC),
			},
			JobLease: w2JobLease, WriteLeases: []ports.WriteLeaseGrant{w2Grants[0]},
			EventID: "event-spk09-finalize-stale", CorrelationID: "spk09",
		},
	})
	if !errors.Is(err, ports.ErrWriteLeaseLost) {
		t.Fatalf("Finalize() with a quarantined-generation write lease error = %v, want ErrWriteLeaseLost", err)
	}

	// Only the current generation can receive a writer...
	currentGrants, err := store.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
		JobLease: w2JobLease, AttemptID: runtime.ExecutionAttemptID("attempt-2"),
		Targets: []ports.WorkspaceLeaseTarget{{
			RepositoryID: "repo-user", RepositoryWorkspaceID: "rw-user-gen2", Generation: 2,
		}},
		TTL: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("AcquireWriteLeases() on recreated generation error = %v", err)
	}

	// ...and only the current generation reaches evidence authority.
	finalized, err := finalizer.Finalize(ctx, worker.FinalizationInput{
		Finalization: ports.WorkerWorkflowRunFinalization{
			Transition: ports.WorkflowRunTransition{
				RunID: "run-1", ExpectedState: runtime.WorkflowRunRunning, ExpectedVersion: 1,
				NextState: runtime.WorkflowRunSucceeded, SharedState: json.RawMessage(`{"result":"reconciled"}`),
				OccurredAt: time.Date(2026, 8, 31, 0, 0, 4, 0, time.UTC),
			},
			JobLease: w2JobLease, WriteLeases: currentGrants,
			EventID: "event-spk09-finalize-current", CorrelationID: "spk09",
		},
	})
	if err != nil {
		t.Fatalf("Finalize() with the current generation's write lease error = %v", err)
	}
	if finalized.State != runtime.WorkflowRunSucceeded {
		t.Fatalf("finalized run state = %s, want SUCCEEDED", finalized.State)
	}

	var gen1State string
	if err := store.db.QueryRowContext(ctx,
		`SELECT state FROM repository_workspaces WHERE id = 'rw-user'`,
	).Scan(&gen1State); err != nil {
		t.Fatal(err)
	}
	if gen1State != string(workspace.RepositoryWorkspaceQuarantined) {
		t.Fatalf("generation 1 state = %s, want QUARANTINED", gen1State)
	}
	var readyGenerations int
	if err := store.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM repository_workspaces
WHERE workspace_set_id = 'workspace-set-1' AND repository_id = 'repo-user' AND state = 'READY'`,
	).Scan(&readyGenerations); err != nil {
		t.Fatal(err)
	}
	if readyGenerations != 1 {
		t.Fatalf("READY generations for repo-user = %d, want exactly 1", readyGenerations)
	}
}

// seedValidWorkflowVersion overwrites seedSchedulingFixture's placeholder
// workflow_versions row (canonical_content "{}", which has no START/END
// node) with a genuinely valid, self-consistent minimal graph. Every
// successful FinalizeWorkflowRun rebuilds and hash-verifies the run's pinned
// workflow version (loadWorkflowRun -> loadWorkflowVersion); the placeholder
// is only exercised by tests where finalize is expected to fail before
// reaching that step. Every column here is derived from the same compiled
// candidate, so the round trip (compile -> store -> reload -> recompile
// inside loadWorkflowVersion) is self-consistent regardless of exactly what
// feeds the content hash.
func seedValidWorkflowVersion(t *testing.T, store *Store) {
	t.Helper()
	projectID := project.ProjectID("project-1")
	definition := workflow.WorkflowDefinition{
		ID:        "definition-1",
		ProjectID: &projectID,
		Name:      "Spike workflow",
		Status:    workflow.DefinitionActive,
		Version:   1,
	}
	candidate := compileWorkflowVersion(t, definition, "workflow-version-1", 1, workflowDocumentV1(), "v1")
	manifestJSON, err := json.Marshal(candidate.Dependencies())
	if err != nil {
		t.Fatalf("encode dependency manifest: %v", err)
	}
	if _, err := store.db.ExecContext(context.Background(), `
UPDATE workflow_versions
SET version_no = ?, schema_version = ?, canonical_content = ?, content_hash = ?,
    dependency_manifest = ?, published_by = ?, published_at = ?
WHERE id = ?`,
		candidate.VersionNumber(), candidate.SchemaVersion(), string(candidate.CanonicalContent()),
		candidate.ContentHash(), string(manifestJSON), candidate.PublishedBy(),
		formatWorkflowTime(candidate.PublishedAt()), candidate.ID(),
	); err != nil {
		t.Fatalf("seed valid workflow version: %v", err)
	}
}
