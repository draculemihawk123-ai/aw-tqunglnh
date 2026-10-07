package workspacereconcile_test

// V9-18 — a recreated generation continues the family: it starts from the last
// commit aw recorded on the quarantined generation (not from the repository's
// default branch), and a repository with a readiness profile gets its baseline
// job, exactly as a first generation does.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/readinesscheck"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// commitInFamilyWorkspace makes a real commit in f's worktree and records it
// the way a ReleaseSet local commit does, returning the new HEAD.
func (f *realFixture) commitInFamilyWorkspace(t *testing.T, name string) string {
	t.Helper()
	directory := f.workingDirectory(t)
	if err := os.WriteFile(filepath.Join(directory, name), []byte("family work\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runReconcileFixtureGit(t, directory, "add", "-A")
	runReconcileFixtureGit(t, directory, "-c", "user.name=aw", "-c", "user.email=aw@example.invalid", "commit", "-q", "-m", "family task "+name)
	head := strings.TrimSpace(runReconcileFixtureGit(t, directory, "rev-parse", "HEAD"))
	if err := f.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Work().AdvanceRepositoryWorkspaceRevision(context.Background(), ports.AdvanceRepositoryWorkspaceRevisionUpdate{
			RepositoryWorkspaceID: f.rw.ID, ExpectedVersion: f.rw.Version, Revision: head, OccurredAt: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatalf("record the commit on the workspace: %v", err)
	}
	f.rw = f.reload(t)
	return head
}

func (f *realFixture) quarantine(t *testing.T, eventID string) workspace.RepositoryWorkspace {
	t.Helper()
	if err := f.store.QuarantineRepositoryWorkspace(context.Background(), ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: f.rw.ID, ExpectedVersion: f.rw.Version, Reason: "SIMULATED_MUTATION_OBSERVED",
		EventID: eventID, CorrelationID: "corr-" + eventID, OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("QuarantineRepositoryWorkspace: %v", err)
	}
	f.rw = f.reload(t)
	return f.rw
}

func (f *realFixture) generation(t *testing.T, generation uint64) workspace.RepositoryWorkspace {
	t.Helper()
	var rw workspace.RepositoryWorkspace
	if err := f.uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		rw, err = tx.Work().GetRepositoryWorkspace(context.Background(), f.workspaceSetID, f.repositoryID, generation)
		return err
	}); err != nil {
		t.Fatalf("read generation %d: %v", generation, err)
	}
	return rw
}

func (f *realFixture) setProfile(t *testing.T) readiness.Profile {
	t.Helper()
	profile, err := readiness.NewProfile(project.RepositoryID(f.repositoryID), nil, readiness.CommandSpec{Executable: "git", Argv: []string{"status"}, TimeoutSeconds: 30})
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}
	stored, err := readinesscheck.SetReadinessProfile(context.Background(), f.uow, profile)
	if err != nil {
		t.Fatalf("SetReadinessProfile: %v", err)
	}
	return stored
}

func (f *realFixture) reconcileRequest(quarantined workspace.RepositoryWorkspace, correlation string) workspacereconcile.ExecuteWorkspaceReconciliationRequest {
	return workspacereconcile.ExecuteWorkspaceReconciliationRequest{
		RepositoryWorkspaceID: string(quarantined.ID), ProjectID: f.projectID, FamilyID: f.familyID,
		WorkspaceSetID: f.workspaceSetID, RepositoryID: f.repositoryID,
		Generation: quarantined.Generation, ExpectedVersion: quarantined.Version, CorrelationID: correlation,
	}
}

func (f *realFixture) reconcileDeps() workspacereconcile.ExecuteWorkspaceReconciliationDeps {
	return workspacereconcile.ExecuteWorkspaceReconciliationDeps{UnitOfWork: f.uow, IDs: f.ids, Provider: f.provider, Lifecycle: f.store}
}

func baselineJobKey(rw workspace.RepositoryWorkspace, profile readiness.Profile) string {
	return "baseline-evidence-" + string(rw.ID) + "-p" + strconv.FormatUint(profile.Version, 10)
}

func TestEndToEnd_Recreate_ContinuesFromTheLastRecordedCommit(t *testing.T) {
	f := newRealFixture(t, "e2e-recreate-continuity.db")
	provisioned := f.rw.CurrentRevision
	firstCommit := f.commitInFamilyWorkspace(t, "task-1.txt")
	secondCommit := f.commitInFamilyWorkspace(t, "task-2.txt")
	if firstCommit == provisioned || secondCommit == firstCommit {
		t.Fatal("the fixture commits did not move HEAD")
	}
	quarantined := f.quarantine(t, "event-continuity-quarantine")
	if quarantined.CurrentRevision != secondCommit {
		t.Fatalf("quarantined generation records %q, want the last commit %q", quarantined.CurrentRevision, secondCommit)
	}

	if err := f.requestAndRunReconciliation(t, "idem-continuity-1", quarantined.Version); err != nil {
		t.Fatalf("requestAndRunReconciliation: %v", err)
	}

	next := f.generation(t, quarantined.Generation+1)
	if next.State != workspace.RepositoryWorkspaceReady {
		t.Fatalf("recreated generation state = %s, want READY", next.State)
	}
	if next.BaseRevision != secondCommit || next.CurrentRevision != secondCommit {
		t.Fatalf("recreated generation starts from base %q / current %q, want the last recorded commit %q (the provisioning revision was %q)",
			next.BaseRevision, next.CurrentRevision, secondCommit, provisioned)
	}
	handle, err := ports.NewWorkspaceHandle(next.Locator)
	if err != nil {
		t.Fatalf("NewWorkspaceHandle: %v", err)
	}
	directory, err := f.provider.WorkingDirectory(context.Background(), handle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}
	for _, name := range []string{"task-1.txt", "task-2.txt"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("the recreated worktree is missing %s, a task the family had already committed: %v", name, err)
		}
	}
	if head := strings.TrimSpace(runReconcileFixtureGit(t, directory, "rev-parse", "HEAD")); head != secondCommit {
		t.Fatalf("recreated worktree HEAD = %s, want %s", head, secondCommit)
	}
}

func TestEndToEnd_Recreate_EnqueuesTheBaselineOfARepositoryWithAProfile(t *testing.T) {
	f := newRealFixture(t, "e2e-recreate-baseline.db")
	ctx := context.Background()
	profile := f.setProfile(t)
	quarantined := f.quarantine(t, "event-baseline-quarantine")

	if err := f.requestAndRunReconciliation(t, "idem-baseline-1", quarantined.Version); err != nil {
		t.Fatalf("requestAndRunReconciliation: %v", err)
	}
	next := f.generation(t, quarantined.Generation+1)
	key := baselineJobKey(next, profile)
	if count, err := f.store.CountDurableJobsByIdempotencyKey(ctx, key); err != nil || count != 1 {
		t.Fatalf("baseline jobs for the recreated generation (%s) = %d (%v), want 1", key, count, err)
	}

	// Replaying the same reconciliation request (a reclaimed job) enqueues no second one.
	if err := workspacereconcile.ExecuteWorkspaceReconciliation(ctx, f.reconcileDeps(), f.reconcileRequest(quarantined, "corr-replay")); err != nil {
		t.Fatalf("replayed ExecuteWorkspaceReconciliation: %v", err)
	}
	if count, err := f.store.CountDurableJobsByIdempotencyKey(ctx, key); err != nil || count != 1 {
		t.Fatalf("baseline jobs after the replay = %d (%v), want still 1", count, err)
	}
}

// A repository without a readiness profile has nothing to check: recreating
// enqueues no baseline job.
func TestEndToEnd_Recreate_NoProfileEnqueuesNoBaseline(t *testing.T) {
	f := newRealFixture(t, "e2e-recreate-no-profile.db")
	ctx := context.Background()
	quarantined := f.quarantine(t, "event-no-profile-quarantine")

	if err := f.requestAndRunReconciliation(t, "idem-no-profile-1", quarantined.Version); err != nil {
		t.Fatalf("requestAndRunReconciliation: %v", err)
	}
	jobs, err := f.store.DebugListJobsByKind(ctx, readinesscheck.BaselineEvidenceJobKind)
	if err != nil {
		t.Fatalf("DebugListJobsByKind: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("baseline jobs = %+v, want none for a repository without a readiness profile", jobs)
	}
}

// A recreate that committed but crashed before its baseline job was enqueued is
// repaired by the replay of the same reconciliation job. The window is
// reproduced by giving the repository its profile only after the recreate.
func TestEndToEnd_Recreate_ReplayRepairsAMissingBaselineJob(t *testing.T) {
	f := newRealFixture(t, "e2e-recreate-baseline-repair.db")
	ctx := context.Background()
	quarantined := f.quarantine(t, "event-repair-quarantine")

	request := f.reconcileRequest(quarantined, "corr-repair")
	if err := workspacereconcile.ExecuteWorkspaceReconciliation(ctx, f.reconcileDeps(), request); err != nil {
		t.Fatalf("first ExecuteWorkspaceReconciliation: %v", err)
	}
	next := f.generation(t, quarantined.Generation+1)
	profile := f.setProfile(t)
	key := baselineJobKey(next, profile)
	if count, err := f.store.CountDurableJobsByIdempotencyKey(ctx, key); err != nil || count != 0 {
		t.Fatalf("baseline jobs before the repair = %d (%v), want none", count, err)
	}

	if err := workspacereconcile.ExecuteWorkspaceReconciliation(ctx, f.reconcileDeps(), request); err != nil {
		t.Fatalf("replayed ExecuteWorkspaceReconciliation: %v", err)
	}
	if count, err := f.store.CountDurableJobsByIdempotencyKey(ctx, key); err != nil || count != 1 {
		t.Fatalf("baseline jobs after the repairing replay = %d (%v), want 1", count, err)
	}
}
