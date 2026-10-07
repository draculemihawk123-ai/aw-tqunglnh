package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// TestAdvanceRepositoryWorkspaceRevision pins the V9-16 fenced CAS: a READY
// workspace at the expected version moves to the committed revision (version
// bumped); a stale version or a non-READY workspace is an optimistic conflict
// and leaves the recorded revision untouched.
func TestAdvanceRepositoryWorkspaceRevision(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)
	ctx := context.Background()
	uow := NewUnitOfWork(store)
	at := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)

	advance := func(version uint64, revision string) error {
		return uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			return tx.Work().AdvanceRepositoryWorkspaceRevision(ctx, ports.AdvanceRepositoryWorkspaceRevisionUpdate{
				RepositoryWorkspaceID: "rw-user", ExpectedVersion: version, Revision: revision, OccurredAt: at,
			})
		})
	}
	loadVersion := func() uint64 {
		t.Helper()
		var version uint64
		if err := store.db.QueryRowContext(ctx, `SELECT version FROM repository_workspaces WHERE id = 'rw-user'`).Scan(&version); err != nil {
			t.Fatalf("load version: %v", err)
		}
		return version
	}
	loadRevision := func() string {
		t.Helper()
		revision, err := store.LoadRepositoryWorkspaceRevision(ctx, "rw-user")
		if err != nil {
			t.Fatalf("LoadRepositoryWorkspaceRevision: %v", err)
		}
		return revision
	}

	if err := advance(1, "user-commit-1"); err != nil {
		t.Fatalf("advance at the current version: %v", err)
	}
	if got := loadRevision(); got != "user-commit-1" {
		t.Fatalf("current revision = %q, want user-commit-1", got)
	}
	if got := loadVersion(); got != 2 {
		t.Fatalf("version = %d, want 2", got)
	}

	// The same (now stale) version is fenced out.
	if err := advance(1, "user-commit-stale"); !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("advance at a stale version error = %v, want ErrOptimisticConflict", err)
	}
	if got := loadRevision(); got != "user-commit-1" {
		t.Fatalf("current revision after a fenced advance = %q, want user-commit-1", got)
	}

	// A quarantined workspace is never advanced.
	if err := store.QuarantineRepositoryWorkspace(ctx, ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: "rw-user", ExpectedVersion: 2, Reason: "test",
		EventID: "event-advance-quarantine", CorrelationID: "advance", OccurredAt: at,
	}); err != nil {
		t.Fatalf("QuarantineRepositoryWorkspace: %v", err)
	}
	if err := advance(3, "user-commit-2"); !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("advance on a quarantined workspace error = %v, want ErrOptimisticConflict", err)
	}
	if got := loadRevision(); got != "user-commit-1" {
		t.Fatalf("current revision after a fenced advance = %q, want user-commit-1", got)
	}
}
