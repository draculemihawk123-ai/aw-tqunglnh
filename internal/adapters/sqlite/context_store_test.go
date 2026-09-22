package sqlite

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func TestContextSnapshotSurvivesRestartWithoutProviderSession(t *testing.T) {
	ctx := context.Background()
	databasePath := migratedDatabasePath(t, "agentkit-context.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	seedSchedulingFixture(t, store)
	snapshot := testContextSnapshot(t, "context-1")
	persisted, err := store.StoreContextSnapshot(ctx, "project-1", snapshot)
	if err != nil {
		t.Fatalf("StoreContextSnapshot() error = %v", err)
	}
	if persisted.ContentHash() != snapshot.ContentHash() {
		t.Fatalf("stored content hash = %s, want %s", persisted.ContentHash(), snapshot.ContentHash())
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	loaded, err := restarted.LoadContextSnapshot(ctx, snapshot.ID())
	if err != nil {
		t.Fatalf("LoadContextSnapshot() error = %v", err)
	}
	if loaded.AttemptID() != snapshot.AttemptID() || loaded.ContentHash() != snapshot.ContentHash() ||
		!bytes.Equal(loaded.CanonicalContent(), snapshot.CanonicalContent()) {
		t.Fatalf("loaded snapshot differs from persisted snapshot")
	}
	if _, err := restarted.StoreContextSnapshot(ctx, "project-1", snapshot); err != nil {
		t.Fatalf("idempotent StoreContextSnapshot() error = %v", err)
	}
}

func TestContextSnapshotRejectsTamperedCanonicalContent(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)
	snapshot := testContextSnapshot(t, "context-tamper")
	if _, err := store.StoreContextSnapshot(context.Background(), "project-1", snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(context.Background(), `
UPDATE context_snapshots SET canonical_content = '{"messages":[]}' WHERE id = ?`, snapshot.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadContextSnapshot(context.Background(), snapshot.ID()); !errors.Is(err, ports.ErrImmutableVersionConflict) {
		t.Fatalf("tampered LoadContextSnapshot() error = %v, want ErrImmutableVersionConflict", err)
	}
}

func testContextSnapshot(t *testing.T, id runtime.ContextSnapshotID) runtime.ContextSnapshot {
	t.Helper()
	revisions, err := workspace.NewRevisionSet([]workspace.Revision{
		{RepositoryID: project.RepositoryID("repo-user"), VCSObjectID: "user-base", WorkspaceGeneration: 1},
		{RepositoryID: project.RepositoryID("repo-web"), VCSObjectID: "web-base", WorkspaceGeneration: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtime.NewContextSnapshot(runtime.ContextSnapshotInput{
		ID:        id,
		AttemptID: "attempt-1",
		Messages: []runtime.ContextMessage{
			{Role: runtime.ContextRoleSystem, Content: "Use the pinned workflow and repository scope."},
			{Role: runtime.ContextRoleUser, Content: "Implement the requested change."},
		},
		Resources: []runtime.ContextResource{
			{Kind: "skill", Reference: "implement-code@v1", ContentHash: "sha256:skill"},
			{Kind: "layer", Reference: "go@v1", ContentHash: "sha256:layer"},
		},
		Revisions: revisions,
		CreatedAt: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
