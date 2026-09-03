package workspacerelease_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacerelease"
)

// This file exercises the real stack — real sqlite persistence, real
// write_leases/durable_jobs queries — for this task's own explicit Verify
// line items that the fake cannot faithfully exercise: "active lease"
// (fake.WorkRepository.HasActiveWriteLease always reports false, see its
// own doc comment) and "restart" (idempotent replay against a real,
// reopened database file). "Quarantined" and "partial release" already
// have real coverage in commands_test.go (the fake's own
// ListWorkspaceSetRepositoryWorkspaces/HasActiveJobForAggregateIDs are
// faithful there), but this file also proves the quarantine check against
// a real persisted QUARANTINED row for good measure.

func openReleaseTestStore(t *testing.T, path string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func releaseSQLiteCommand(idempotencyKey, requestHash, projectID string, expectedVersion uint64) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), ExpectedVersion: expectedVersion,
		RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type:        "RequestWorkspaceSetRelease", RequestHash: requestHash,
	}
}

// TestRequestWorkspaceSetRelease_SQLite_ActiveWriteLease_Rejected is this
// task's own "active lease" Verify-line scenario against real sqlite: a
// live, non-expired write_leases row on the set's own (only)
// RepositoryWorkspace must block the request, exercising
// ports.WorkRepository.HasActiveWriteLease's real implementation
// (internal/adapters/sqlite/work.go's own hasActiveWriteLeaseTx) rather
// than the fake's always-false stand-in.
func TestRequestWorkspaceSetRelease_SQLite_ActiveWriteLease_Rejected(t *testing.T) {
	ctx := context.Background()
	store := openReleaseTestStore(t, filepath.Join(t.TempDir(), "release-active-lease.db"))
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	if err := sqlite.SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-root"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := sqlite.SeedFixtureRepositoryWorkspace(ctx, store, "project-1", "family-1", "workspace-set-1", "repo-1", "rw-1"); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}
	if err := sqlite.SeedFixtureWriteLease(ctx, store, "project-1", "family-1", "work-root", "repo-1", "rw-1", 1); err != nil {
		t.Fatalf("SeedFixtureWriteLease: %v", err)
	}

	cmd := releaseSQLiteCommand("idem-1", "hash-a", "project-1", 1)
	_, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authorizedFake(), cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: "family-1", ProjectID: "project-1",
	})
	if !errors.Is(err, workspacerelease.ErrWorkspaceSetHasActiveWriteLease) {
		t.Fatalf("RequestWorkspaceSetRelease() error = %v, want ErrWorkspaceSetHasActiveWriteLease", err)
	}
}

// TestRequestWorkspaceSetRelease_SQLite_Quarantined_Rejected mirrors
// TestRequestWorkspaceSetRelease_QuarantinedRepository_Rejected
// (commands_test.go) against real sqlite, quarantining the fixture's own
// RepositoryWorkspace through the real, already-built
// ports.WorkspaceLifecycle.QuarantineRepositoryWorkspace (V3-10) — read-only
// reference to that method for test setup, never called from this
// package's own production code.
func TestRequestWorkspaceSetRelease_SQLite_Quarantined_Rejected(t *testing.T) {
	ctx := context.Background()
	store := openReleaseTestStore(t, filepath.Join(t.TempDir(), "release-quarantined.db"))
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	if err := sqlite.SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-root"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := sqlite.SeedFixtureRepositoryWorkspace(ctx, store, "project-1", "family-1", "workspace-set-1", "repo-1", "rw-1"); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}
	if err := store.QuarantineRepositoryWorkspace(ctx, ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: "rw-1", ExpectedVersion: 1, Reason: "test fixture quarantine",
		EventID: "evt-quarantine-1", CorrelationID: "corr-1", OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("QuarantineRepositoryWorkspace: %v", err)
	}

	cmd := releaseSQLiteCommand("idem-1", "hash-a", "project-1", 1)
	_, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authorizedFake(), cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: "family-1", ProjectID: "project-1",
	})
	if !errors.Is(err, workspacerelease.ErrWorkspaceSetHasQuarantinedRepository) {
		t.Fatalf("RequestWorkspaceSetRelease() error = %v, want ErrWorkspaceSetHasQuarantinedRepository", err)
	}
}

// TestRequestWorkspaceSetRelease_SQLite_Eligible_EnqueuesJobAndWritesEvent
// is the real-sqlite happy-path counterpart to commands_test.go's own fake
// version: proves HasActiveWriteLease/HasActiveJobForAggregateIDs both
// correctly report "nothing active" (not just that they correctly report
// "something active" when seeded, covered by the two tests above) against
// a clean real fixture.
func TestRequestWorkspaceSetRelease_SQLite_Eligible_EnqueuesJobAndWritesEvent(t *testing.T) {
	ctx := context.Background()
	store := openReleaseTestStore(t, filepath.Join(t.TempDir(), "release-eligible.db"))
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	if err := sqlite.SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-root"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := sqlite.SeedFixtureRepositoryWorkspace(ctx, store, "project-1", "family-1", "workspace-set-1", "repo-1", "rw-1"); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}

	cmd := releaseSQLiteCommand("idem-1", "hash-a", "project-1", 1)
	result, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow, ids, authorizedFake(), cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: "family-1", ProjectID: "project-1",
	})
	if err != nil {
		t.Fatalf("RequestWorkspaceSetRelease: %v", err)
	}
	if result.WorkspaceSetID != "workspace-set-1" || result.ReleaseJobID == "" {
		t.Fatalf("result = %+v, want WorkspaceSetID=workspace-set-1 and a non-empty ReleaseJobID", result)
	}
}

// TestRequestWorkspaceSetRelease_SQLite_Restart_ReplaysIdempotently is this
// task's own "restart" Verify-line scenario: a process crash/restart
// between the first request and a retry with the exact same
// IdempotencyKey/RequestHash must replay the first call's own persisted
// result — never mint a second job — the same "reopen against the same
// database file" pattern
// internal/app/workspaceprovision/handler_sqlite_test.go's own restart test
// establishes, applied here to the request layer alone (there is no
// executor to restart, per this task's own Phạm vi line — see
// commands.go's own doc comment).
func TestRequestWorkspaceSetRelease_SQLite_Restart_ReplaysIdempotently(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "release-restart.db")

	store1, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open (first): %v", err)
	}
	uow1 := sqlite.NewUnitOfWork(store1)
	ids := idsource.NewSequential("id")

	if err := sqlite.SeedFixtureOwners(ctx, store1, "project-1", "family-1", "work-root"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := sqlite.SeedFixtureRepositoryWorkspace(ctx, store1, "project-1", "family-1", "workspace-set-1", "repo-1", "rw-1"); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}

	cmd := releaseSQLiteCommand("idem-1", "hash-a", "project-1", 1)
	req := workspacerelease.RequestWorkspaceSetReleaseRequest{FamilyID: "family-1", ProjectID: "project-1"}
	first, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow1, ids, authorizedFake(), cmd, req)
	if err != nil {
		t.Fatalf("first RequestWorkspaceSetRelease: %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("Close (first): %v", err)
	}

	// --- restart: reopen against the same database file ---
	store2, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open (restart): %v", err)
	}
	t.Cleanup(func() { store2.Close() })
	uow2 := sqlite.NewUnitOfWork(store2)

	second, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow2, ids, authorizedFake(), cmd, req)
	if err != nil {
		t.Fatalf("second (post-restart, replayed) RequestWorkspaceSetRelease: %v", err)
	}
	if second != first {
		t.Fatalf("post-restart replayed result = %+v, want identical to pre-restart result %+v", second, first)
	}
}
