package workspacereconcile_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// V11-01: AdoptWorkspaceHead against the real stack — real sqlite, a real Git
// worktree provisioned by the real workspace provisioner, and a commit made
// outside aw exactly the way an IDE or a terminal makes one.

func (f *realFixture) adoptDeps() workspacereconcile.AdoptWorkspaceHeadDeps {
	return workspacereconcile.AdoptWorkspaceHeadDeps{UnitOfWork: f.uow, Auditor: f.provider, Matcher: redact.NewMatcher()}
}

func (f *realFixture) adoptCommand(key string, expectedVersion uint64) ports.Command {
	return ports.Command{
		ID: "cmd-" + key, IdempotencyKey: key, Actor: "operator-1", CorrelationID: "corr-" + key,
		Scope: ports.ProjectScope(f.projectID), ExpectedVersion: expectedVersion, RequestedAt: time.Now().UTC(),
		Type: "AdoptWorkspaceHead", RequestHash: "hash-" + key,
	}
}

func (f *realFixture) adopt(t *testing.T, key string, expectedVersion uint64) (workspacereconcile.AdoptWorkspaceHeadResult, error) {
	t.Helper()
	return workspacereconcile.AdoptWorkspaceHead(context.Background(), f.adoptDeps(), f.adoptCommand(key, expectedVersion),
		workspacereconcile.AdoptWorkspaceHeadRequest{RepositoryWorkspaceID: string(f.rw.ID), ProjectID: f.projectID})
}

// commitOutsideAw commits a file in the worktree without telling aw.
func (f *realFixture) commitOutsideAw(t *testing.T, name, message string) string {
	t.Helper()
	directory := f.workingDirectory(t)
	if err := os.WriteFile(filepath.Join(directory, name), []byte("by hand\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runReconcileFixtureGit(t, directory, "add", "-A")
	runReconcileFixtureGit(t, directory, "-c", "user.name=Ide User", "-c", "user.email=ide@example.invalid", "commit", "-q", "-m", message)
	return strings.TrimSpace(runReconcileFixtureGit(t, directory, "rev-parse", "HEAD"))
}

func (f *realFixture) adoptedEvents(t *testing.T) []sqlite.DomainEventRecord {
	t.Helper()
	records, err := f.store.ListDomainEventsForProject(context.Background(), f.projectID)
	if err != nil {
		t.Fatalf("list domain events: %v", err)
	}
	var adopted []sqlite.DomainEventRecord
	for _, record := range records {
		if record.EventType == workspacereconcile.WorkspaceHeadAdoptedEventType {
			adopted = append(adopted, record)
		}
	}
	return adopted
}

func TestAdoptWorkspaceHead_RecordsTheOutsideCommitAsTheCurrentRevision(t *testing.T) {
	f := newRealFixture(t, "adopt-ok.db")
	recorded := f.rw.CurrentRevision
	first := f.commitOutsideAw(t, "a.txt", "first by hand")
	head := f.commitOutsideAw(t, "b.txt", "second by hand")

	result, err := f.adopt(t, "adopt-1", f.rw.Version)
	if err != nil {
		t.Fatalf("AdoptWorkspaceHead: %v", err)
	}
	if result.PreviousRevision != recorded || result.AdoptedRevision != head || result.AdoptedCommitCount != 2 || len(result.AdoptedCommits) != 2 {
		t.Fatalf("result = %+v, want %s -> %s with 2 commits", result, recorded, head)
	}
	if result.AdoptedCommits[0].CommitID != first || result.AdoptedCommits[0].AuthorName != "Ide User" || result.AdoptedCommits[0].Subject != "first by hand" {
		t.Fatalf("first adopted commit = %+v", result.AdoptedCommits[0])
	}
	got := f.reload(t)
	if got.CurrentRevision != head || got.Version != f.rw.Version+1 || result.Version != got.Version || got.State != workspace.RepositoryWorkspaceReady {
		t.Fatalf("workspace after adoption = %+v, want READY at %s version %d", got, head, f.rw.Version+1)
	}
	events := f.adoptedEvents(t)
	if len(events) != 1 {
		t.Fatalf("WorkspaceHeadAdopted events = %d, want 1", len(events))
	}
	var payload struct {
		Actor              string `json:"actor"`
		AdoptedRevision    string `json:"adoptedRevision"`
		AdoptedCommitCount int    `json:"adoptedCommitCount"`
		AdoptedCommits     []struct {
			CommitID, AuthorEmail, Subject string
		} `json:"adoptedCommits"`
	}
	if err := json.Unmarshal([]byte(events[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("decode event payload: %v", err)
	}
	if payload.Actor != "operator-1" || payload.AdoptedRevision != head || payload.AdoptedCommitCount != 2 || len(payload.AdoptedCommits) != 2 {
		t.Fatalf("event payload = %+v, want the operator, the revision and both commits", payload)
	}
}

func TestAdoptWorkspaceHead_ReplayWithTheSameKeyDoesNotAdoptTwice(t *testing.T) {
	f := newRealFixture(t, "adopt-replay.db")
	f.commitOutsideAw(t, "a.txt", "by hand")
	first, err := f.adopt(t, "adopt-replay", f.rw.Version)
	if err != nil {
		t.Fatalf("first AdoptWorkspaceHead: %v", err)
	}
	// The same command again, naming the version the caller originally observed.
	second, err := f.adopt(t, "adopt-replay", f.rw.Version)
	if err != nil {
		t.Fatalf("replayed AdoptWorkspaceHead: %v", err)
	}
	if first.AdoptedRevision != second.AdoptedRevision || first.Version != second.Version {
		t.Fatalf("replay = %+v, want the original %+v", second, first)
	}
	if got := f.reload(t); got.Version != f.rw.Version+1 {
		t.Fatalf("version = %d, want it advanced exactly once (%d)", got.Version, f.rw.Version+1)
	}
	if n := len(f.adoptedEvents(t)); n != 1 {
		t.Fatalf("WorkspaceHeadAdopted events = %d, want 1", n)
	}

	conflicting := f.adoptCommand("adopt-replay", f.rw.Version)
	conflicting.RequestHash = "a-different-body"
	_, err = workspacereconcile.AdoptWorkspaceHead(context.Background(), f.adoptDeps(), conflicting,
		workspacereconcile.AdoptWorkspaceHeadRequest{RepositoryWorkspaceID: string(f.rw.ID), ProjectID: f.projectID})
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("same key, different body: err = %v, want ErrReceiptConflict", err)
	}
}

func TestAdoptWorkspaceHead_RefusesADirtyWorktree(t *testing.T) {
	f := newRealFixture(t, "adopt-dirty.db")
	f.commitOutsideAw(t, "a.txt", "by hand")
	if err := os.WriteFile(filepath.Join(f.workingDirectory(t), "wip.txt"), []byte("work in progress\n"), 0o644); err != nil {
		t.Fatalf("write wip file: %v", err)
	}
	_, err := f.adopt(t, "adopt-dirty", f.rw.Version)
	if !errors.Is(err, workspacereconcile.ErrAdoptWorktreeDirty) {
		t.Fatalf("err = %v, want ErrAdoptWorktreeDirty", err)
	}
	if got := f.reload(t); got.CurrentRevision != f.rw.CurrentRevision || got.Version != f.rw.Version {
		t.Fatalf("a refused adoption changed the workspace: %+v", got)
	}
}

// After `git reset --hard` to an older commit, HEAD no longer descends from what
// aw recorded: adopting it would drop that history.
func TestAdoptWorkspaceHead_RefusesAHeadThatIsNotADescendant(t *testing.T) {
	f := newRealFixture(t, "adopt-reset.db")
	recordedHead := f.commitInFamilyWorkspace(t, "family.txt")
	directory := f.workingDirectory(t)
	runReconcileFixtureGit(t, directory, "reset", "-q", "--hard", f.rw.BaseRevision)
	if f.rw.CurrentRevision != recordedHead {
		t.Fatalf("fixture: recorded revision %s, want %s", f.rw.CurrentRevision, recordedHead)
	}

	_, err := f.adopt(t, "adopt-reset", f.rw.Version)
	if !errors.Is(err, workspacereconcile.ErrAdoptHeadNotDescendant) {
		t.Fatalf("err = %v, want ErrAdoptHeadNotDescendant", err)
	}
}

func TestAdoptWorkspaceHead_RefusesWhenNothingWasCommittedOutsideAw(t *testing.T) {
	f := newRealFixture(t, "adopt-nothing.db")
	_, err := f.adopt(t, "adopt-nothing", f.rw.Version)
	if !errors.Is(err, workspacereconcile.ErrAdoptNothingToAdopt) {
		t.Fatalf("err = %v, want ErrAdoptNothingToAdopt", err)
	}
}

func TestAdoptWorkspaceHead_RefusesAStaleVersion(t *testing.T) {
	f := newRealFixture(t, "adopt-stale.db")
	f.commitOutsideAw(t, "a.txt", "by hand")
	_, err := f.adopt(t, "adopt-stale", f.rw.Version+7)
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("err = %v, want ErrOptimisticConflict", err)
	}
}

func TestAdoptWorkspaceHead_RefusesAQuarantinedWorkspace(t *testing.T) {
	f := newRealFixture(t, "adopt-quarantined.db")
	f.commitOutsideAw(t, "a.txt", "by hand")
	quarantined := f.quarantine(t, "evt-adopt-q")
	_, err := f.adopt(t, "adopt-quarantined", quarantined.Version)
	if !errors.Is(err, ports.ErrWorkspaceQuarantined) {
		t.Fatalf("err = %v, want ErrWorkspaceQuarantined", err)
	}
}

func TestAdoptWorkspaceHead_RefusesWhileAnAttemptHoldsTheWriteLease(t *testing.T) {
	f := newRealFixture(t, "adopt-writer.db")
	f.commitOutsideAw(t, "a.txt", "by hand")
	if err := sqlite.SeedFixtureWriteLease(context.Background(), f.store, f.projectID, f.familyID, f.workItemID, f.repositoryID, string(f.rw.ID), f.rw.Generation); err != nil {
		t.Fatalf("seed a live write lease: %v", err)
	}
	_, err := f.adopt(t, "adopt-writer", f.rw.Version)
	if !errors.Is(err, workspacereconcile.ErrAdoptWriterActive) {
		t.Fatalf("err = %v, want ErrAdoptWriterActive", err)
	}
}

func TestAdoptWorkspaceHead_RefusesAWorkspaceOfAnotherProject(t *testing.T) {
	f := newRealFixture(t, "adopt-project.db")
	f.commitOutsideAw(t, "a.txt", "by hand")
	_, err := workspacereconcile.AdoptWorkspaceHead(context.Background(), f.adoptDeps(), f.adoptCommand("adopt-project", f.rw.Version),
		workspacereconcile.AdoptWorkspaceHeadRequest{RepositoryWorkspaceID: string(f.rw.ID), ProjectID: "some-other-project"})
	if !errors.Is(err, ports.ErrCrossProjectReference) {
		t.Fatalf("err = %v, want ErrCrossProjectReference", err)
	}
}

// V11-01 end to end on the data side: once HEAD is adopted, a Run started
// afterwards pins the new HEAD, so the machine gate that failed before has a
// matching revision. startRevisionSet (internal/app/runtime) reads exactly the
// CurrentRevision this command wrote.
func TestAdoptWorkspaceHead_AdoptedRevisionIsWhatTheNextRunPins(t *testing.T) {
	f := newRealFixture(t, "adopt-pin.db")
	head := f.commitOutsideAw(t, "a.txt", "by hand")
	if _, err := f.adopt(t, "adopt-pin", f.rw.Version); err != nil {
		t.Fatalf("AdoptWorkspaceHead: %v", err)
	}
	handle, err := ports.NewWorkspaceHandle(f.reload(t).Locator)
	if err != nil {
		t.Fatalf("NewWorkspaceHandle: %v", err)
	}
	live, err := f.provider.CaptureRevision(context.Background(), handle)
	if err != nil {
		t.Fatalf("CaptureRevision: %v", err)
	}
	if got := f.reload(t).CurrentRevision; got != head || got != live.VCSObjectID {
		t.Fatalf("recorded %s, live HEAD %s, want both %s", got, live.VCSObjectID, head)
	}
}
