package workspacestate_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacestate"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// newStateTestStore mirrors internal/delivery/httpapi/receiptreplay_test.go's
// own newReceiptTestStore exactly — a fresh real sqlite file per test, so
// this package's own real-adapter proofs (an active write lease genuinely
// reflected, a genuine quarantine genuinely reflected) run against the same
// kind of store production code uses, not fake.WorkRepository's own
// always-false HasActiveWriteLease stub (see that method's own doc comment
// for exactly why real sqlite is required for this proof).
func newStateTestStore(t *testing.T) (*sqlite.Store, ports.UnitOfWork) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "workspacestate.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store, sqlite.NewUnitOfWork(store)
}

// TestGetRepositoryWorkspaceState_ActiveWriteLease_ReflectsTrueAgainstRealSqlite
// is the one proof fake.WorkRepository structurally cannot give (its own
// HasActiveWriteLease always returns false, nil): sqlite.SeedFixtureWriteLease
// drives the real EnqueueJob/ClaimJob/AcquireWriteLeases production path to
// place one real, live write_leases row, and this test confirms
// GetRepositoryWorkspaceState reads it back as HasActiveWriteLease=true — the
// exact "lease" half of this task's own Mục tiêu line ("expose ...
// lease/fence/quarantine"), proven for real.
func TestGetRepositoryWorkspaceState_ActiveWriteLease_ReflectsTrueAgainstRealSqlite(t *testing.T) {
	ctx := context.Background()
	store, uow := newStateTestStore(t)

	const projectID, familyID, workItemID = "project-1", "family-1", "work-1"
	const workspaceSetID, repositoryID, repositoryWorkspaceID = "set-1", "repo-1", "rw-1"
	if err := sqlite.SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := sqlite.SeedFixtureRepositoryWorkspace(ctx, store, projectID, familyID, workspaceSetID, repositoryID, repositoryWorkspaceID); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}

	before, err := workspacestate.GetRepositoryWorkspaceState(ctx, uow, workspacestate.GetRepositoryWorkspaceStateRequest{
		ProjectID: projectID, RepositoryWorkspaceID: repositoryWorkspaceID,
	})
	if err != nil {
		t.Fatalf("GetRepositoryWorkspaceState (before lease): %v", err)
	}
	if before.HasActiveWriteLease {
		t.Fatal("HasActiveWriteLease = true before any lease was ever acquired")
	}

	if err := sqlite.SeedFixtureWriteLease(ctx, store, projectID, familyID, workItemID, repositoryID, repositoryWorkspaceID, 1); err != nil {
		t.Fatalf("SeedFixtureWriteLease: %v", err)
	}

	after, err := workspacestate.GetRepositoryWorkspaceState(ctx, uow, workspacestate.GetRepositoryWorkspaceStateRequest{
		ProjectID: projectID, RepositoryWorkspaceID: repositoryWorkspaceID,
	})
	if err != nil {
		t.Fatalf("GetRepositoryWorkspaceState (after lease): %v", err)
	}
	if !after.HasActiveWriteLease {
		t.Fatal("HasActiveWriteLease = false after SeedFixtureWriteLease acquired a real, live write lease")
	}

	// The same lease is also visible through the WorkspaceSet-level query,
	// on the exact repository workspace that holds it — not just the
	// standalone single-row query above.
	setState, err := workspacestate.GetWorkspaceSetState(ctx, uow, workspacestate.GetWorkspaceSetStateRequest{
		ProjectID: projectID, FamilyID: familyID,
	})
	if err != nil {
		t.Fatalf("GetWorkspaceSetState: %v", err)
	}
	if len(setState.RepositoryWorkspaces) != 1 || !setState.RepositoryWorkspaces[0].HasActiveWriteLease {
		t.Fatalf("GetWorkspaceSetState RepositoryWorkspaces = %+v, want exactly one entry with HasActiveWriteLease=true", setState.RepositoryWorkspaces)
	}
}

// TestGetRepositoryWorkspaceState_RealQuarantine_ReflectsQuarantinedState
// proves the "quarantine" half of this task's own Mục tiêu line against the
// real, production ports.WorkspaceLifecycle.QuarantineRepositoryWorkspace
// transition (internal/adapters/sqlite/workspace_lifecycle.go) — not a
// fabricated row, the real fenced READY->QUARANTINED CAS every other real
// quarantine in this codebase goes through.
func TestGetRepositoryWorkspaceState_RealQuarantine_ReflectsQuarantinedState(t *testing.T) {
	ctx := context.Background()
	store, uow := newStateTestStore(t)

	const projectID, familyID, workItemID = "project-1", "family-1", "work-1"
	const workspaceSetID, repositoryID, repositoryWorkspaceID = "set-1", "repo-1", "rw-1"
	if err := sqlite.SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := sqlite.SeedFixtureRepositoryWorkspace(ctx, store, projectID, familyID, workspaceSetID, repositoryID, repositoryWorkspaceID); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}

	if err := store.QuarantineRepositoryWorkspace(ctx, ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(repositoryWorkspaceID), ExpectedVersion: 1,
		Reason: "test: simulated lease-losing writer", EventID: "evt-quarantine-1",
	}); err != nil {
		t.Fatalf("QuarantineRepositoryWorkspace: %v", err)
	}

	state, err := workspacestate.GetRepositoryWorkspaceState(ctx, uow, workspacestate.GetRepositoryWorkspaceStateRequest{
		ProjectID: projectID, RepositoryWorkspaceID: repositoryWorkspaceID,
	})
	if err != nil {
		t.Fatalf("GetRepositoryWorkspaceState: %v", err)
	}
	if state.State != workspace.RepositoryWorkspaceQuarantined {
		t.Fatalf("State = %q, want QUARANTINED", state.State)
	}
	if state.Version != 2 {
		t.Fatalf("Version = %d, want 2 (the quarantine CAS increments it)", state.Version)
	}
}
