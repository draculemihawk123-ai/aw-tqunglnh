package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/message"
)

func TestMessageRepository_AppendMessage_GetMessage_RoundTrip(t *testing.T) {
	store := openCatalogTestStore(t, "messages-roundtrip.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	artifactID := seedTestArtifact(t, store, "project-1", "artifact-1")

	createdAt := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	var appended message.Message
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		appended, err = messageRepository{tx: tx}.AppendMessage(ctx, ports.AppendMessageRequest{
			ID: "msg-1", ProjectID: "project-1", WorkItemID: "work-item-1", Actor: "user-1",
			Role: message.RoleUser, ContentArtifactID: artifactID, CorrelationID: "corr-1", CreatedAt: createdAt,
		})
		return err
	})
	if appended.Sequence != 1 {
		t.Fatalf("Sequence = %d, want 1", appended.Sequence)
	}
	if appended.AttemptID != nil {
		t.Fatalf("AttemptID = %v, want nil", appended.AttemptID)
	}

	var loaded message.Message
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = messageRepository{tx: tx}.GetMessage(ctx, "msg-1")
		return err
	})
	if loaded != appended {
		t.Fatalf("loaded = %+v, want %+v", loaded, appended)
	}
}

func TestMessageRepository_AppendMessage_WorkItemNotFound(t *testing.T) {
	store := openCatalogTestStore(t, "messages-workitem-notfound.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	artifactID := seedTestArtifact(t, store, "project-1", "artifact-1")

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := messageRepository{tx: tx}.AppendMessage(ctx, ports.AppendMessageRequest{
			ID: "msg-1", ProjectID: "project-1", WorkItemID: "missing-work-item", Actor: "user-1",
			Role: message.RoleUser, ContentArtifactID: artifactID, CreatedAt: time.Now(),
		})
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

func TestMessageRepository_AppendMessage_CrossProjectReference_Rejected(t *testing.T) {
	store := openCatalogTestStore(t, "messages-cross-project.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureOwners(ctx, store, "project-2", "family-2", "work-item-2"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	artifactID := seedTestArtifact(t, store, "project-2", "artifact-1")

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		// work-item-1 belongs to project-1's own stored row — claiming
		// project-2 here must be rejected using the WorkItem's own stored
		// project, never the caller's claim.
		_, err := messageRepository{tx: tx}.AppendMessage(ctx, ports.AppendMessageRequest{
			ID: "msg-1", ProjectID: "project-2", WorkItemID: "work-item-1", Actor: "user-1",
			Role: message.RoleUser, ContentArtifactID: artifactID, CreatedAt: time.Now(),
		})
		if !errors.Is(err, ports.ErrCrossProjectReference) {
			t.Fatalf("err = %v, want ports.ErrCrossProjectReference", err)
		}
		return nil
	})
}

func TestMessageRepository_AppendMessage_AttemptLinkage(t *testing.T) {
	store := openCatalogTestStore(t, "messages-attempt-linkage.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureExecutionAttempt(ctx, store, "project-1", "family-1", "work-item-1", "attempt-1"); err != nil {
		t.Fatalf("SeedFixtureExecutionAttempt: %v", err)
	}
	artifactID := seedTestArtifact(t, store, "project-1", "artifact-1")

	var appended message.Message
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		appended, err = messageRepository{tx: tx}.AppendMessage(ctx, ports.AppendMessageRequest{
			ID: "msg-1", ProjectID: "project-1", WorkItemID: "work-item-1", AttemptID: "attempt-1",
			Actor: "assistant", Role: message.RoleAssistant, ContentArtifactID: artifactID, CreatedAt: time.Now(),
		})
		return err
	})
	if appended.AttemptID == nil || string(*appended.AttemptID) != "attempt-1" {
		t.Fatalf("AttemptID = %v, want attempt-1", appended.AttemptID)
	}
}

func TestMessageRepository_AppendMessage_AttemptNotFound(t *testing.T) {
	store := openCatalogTestStore(t, "messages-attempt-notfound.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	artifactID := seedTestArtifact(t, store, "project-1", "artifact-1")

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := messageRepository{tx: tx}.AppendMessage(ctx, ports.AppendMessageRequest{
			ID: "msg-1", ProjectID: "project-1", WorkItemID: "work-item-1", AttemptID: "missing-attempt",
			Actor: "user-1", Role: message.RoleUser, ContentArtifactID: artifactID, CreatedAt: time.Now(),
		})
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

func TestMessageRepository_AppendMessage_DuplicateID_ReturnsExistingRow_NeverReallocatesSequence(t *testing.T) {
	store := openCatalogTestStore(t, "messages-duplicate-id.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	artifactA := seedTestArtifact(t, store, "project-1", "artifact-a")
	artifactB := seedTestArtifact(t, store, "project-1", "artifact-b")

	var first message.Message
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		first, err = messageRepository{tx: tx}.AppendMessage(ctx, ports.AppendMessageRequest{
			ID: "msg-1", ProjectID: "project-1", WorkItemID: "work-item-1", Actor: "user-1",
			Role: message.RoleUser, ContentArtifactID: artifactA, CreatedAt: time.Now(),
		})
		return err
	})

	// A retry with the same ID but different content-artifact must return
	// the ALREADY-stored row (Sequence 1) rather than resolving a fresh
	// Sequence for it.
	var retried message.Message
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		retried, err = messageRepository{tx: tx}.AppendMessage(ctx, ports.AppendMessageRequest{
			ID: "msg-1", ProjectID: "project-1", WorkItemID: "work-item-1", Actor: "user-1",
			Role: message.RoleUser, ContentArtifactID: artifactB, CreatedAt: time.Now(),
		})
		return err
	})
	if retried != first {
		t.Fatalf("retried = %+v, want the original %+v", retried, first)
	}
}

func TestMessageRepository_ListMessagesForWorkItem_OrderedBySequence(t *testing.T) {
	store := openCatalogTestStore(t, "messages-ordering.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	artifactID := seedTestArtifact(t, store, "project-1", "artifact-1")

	for i, id := range []string{"msg-1", "msg-2", "msg-3"} {
		withCatalogTx(t, store, func(tx *sql.Tx) error {
			_, err := messageRepository{tx: tx}.AppendMessage(ctx, ports.AppendMessageRequest{
				ID: id, ProjectID: "project-1", WorkItemID: "work-item-1", Actor: "user-1",
				Role: message.RoleUser, ContentArtifactID: artifactID, CreatedAt: time.Now().Add(time.Duration(i) * time.Second),
			})
			return err
		})
	}

	var listed []message.Message
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		listed, err = messageRepository{tx: tx}.ListMessagesForWorkItem(ctx, "work-item-1")
		return err
	})
	if len(listed) != 3 {
		t.Fatalf("len(listed) = %d, want 3", len(listed))
	}
	for i, m := range listed {
		if m.Sequence != uint64(i+1) {
			t.Fatalf("listed[%d].Sequence = %d, want %d", i, m.Sequence, i+1)
		}
		if string(m.ID) != []string{"msg-1", "msg-2", "msg-3"}[i] {
			t.Fatalf("listed[%d].ID = %s, want %s", i, m.ID, []string{"msg-1", "msg-2", "msg-3"}[i])
		}
	}
}

// seedTestArtifact inserts a minimal, valid artifacts row (V5-01) and
// returns its ID, for tests in this package that only need a real FK
// target for content_artifact_id — not the full attach flow.
func seedTestArtifact(t *testing.T, store *Store, projectID, artifactID string) string {
	t.Helper()
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO artifacts (id, project_id, locator, content_hash, size, media_type, sensitivity, redacted,
                        retention_class, attach_state, hold, expires_at, created_at, version)
VALUES (?, ?, ?, ?, 0, 'text/plain', 'PUBLIC', 0, 'CANONICAL_CONTEXT', 'ATTACHED', 0, NULL, ?, 1)`,
		artifactID, projectID, "sha256:"+artifactID, "sha256:"+artifactID, formatWorkflowTime(time.Now()),
	); err != nil {
		t.Fatalf("seed test artifact: %v", err)
	}
	return artifactID
}
