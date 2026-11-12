package workspacereconcile_test

// V12-20 (ADR-055): Sync with base branch against the real stack — sqlite, a real worktree, and main moving on in the repository.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
)

func (f *realFixture) sync(t *testing.T, key string, expectedVersion uint64) (workspacereconcile.SyncWorkspaceWithBaseResult, error) {
	t.Helper()
	cmd := f.adoptCommand(key, expectedVersion)
	cmd.Type = "SyncWorkspaceWithBase"
	return workspacereconcile.SyncWorkspaceWithBase(context.Background(), workspacereconcile.SyncWorkspaceWithBaseDeps{Adopt: f.adoptDeps(), Syncer: f.provider},
		cmd, workspacereconcile.AdoptWorkspaceHeadRequest{RepositoryWorkspaceID: string(f.rw.ID), ProjectID: f.projectID})
}

func (f *realFixture) commitOnMain(t *testing.T, name string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.repoPath, name), []byte("merged elsewhere\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runReconcileFixtureGit(t, f.repoPath, "add", "-A")
	runReconcileFixtureGit(t, f.repoPath, "-c", "user.name=Forge", "-c", "user.email=forge@example.invalid", "commit", "-q", "-m", "merge PR "+name)
	return strings.TrimSpace(runReconcileFixtureGit(t, f.repoPath, "rev-parse", "HEAD"))
}

func TestSyncWorkspaceWithBase_FastForwardsAndRecordsTheNewHead(t *testing.T) {
	f := newRealFixture(t, "sync-ok.db")
	tip := f.commitOnMain(t, "pr-1.txt")

	result, err := f.sync(t, "sync-1", f.rw.Version)
	if err != nil {
		t.Fatalf("SyncWorkspaceWithBase: %v", err)
	}
	if result.Sync.Outcome != ports.BaseSyncFastForwarded || result.Sync.Head != tip || result.Adopted == nil || result.Adopted.AdoptedRevision != tip {
		t.Fatalf("result = %+v, want fast-forwarded to %s and adopted", result, tip)
	}
	got := f.reload(t)
	if got.CurrentRevision != tip || got.Version != result.Version {
		t.Fatalf("workspace = %+v, want current revision %s at version %d", got, tip, result.Version)
	}
	if len(f.adoptedEvents(t)) != 1 {
		t.Fatal("the sync must leave a WorkspaceHeadAdopted audit event")
	}
	replayed, err := f.sync(t, "sync-1", f.rw.Version)
	if err != nil || replayed.Version != result.Version {
		t.Fatalf("replay = %+v, %v; want the first result", replayed, err)
	}

	status, err := workspacereconcile.GetWorkspaceBaseStatus(context.Background(), f.uow, f.provider, f.projectID, string(f.rw.ID))
	if err != nil || status.Behind != 0 || status.Ahead != 0 {
		t.Fatalf("status = %+v, %v; want level with main", status, err)
	}
}

func TestSyncWorkspaceWithBase_RefusesUnrecordedCommitsAndReportsADivergence(t *testing.T) {
	f := newRealFixture(t, "sync-diverged.db")
	f.commitOutsideAw(t, "by-hand.txt", "outside aw")
	if _, err := f.sync(t, "sync-unrecorded", f.rw.Version); !errors.Is(err, workspacereconcile.ErrSyncHeadNotRecorded) {
		t.Fatalf("err = %v, want ErrSyncHeadNotRecorded (adopt first)", err)
	}
	adopted, err := f.adopt(t, "adopt-first", f.rw.Version)
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	f.commitOnMain(t, "pr-2.txt")
	result, err := f.sync(t, "sync-diverged", adopted.Version)
	if err != nil {
		t.Fatalf("SyncWorkspaceWithBase: %v", err)
	}
	if result.Sync.Outcome != ports.BaseSyncNeedsMerge || result.Adopted != nil || result.Version != adopted.Version {
		t.Fatalf("result = %+v, want NEEDS_MERGE and nothing recorded", result)
	}
}
