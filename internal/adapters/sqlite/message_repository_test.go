package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/message"
)

// seedFixtureSecondWorkItem adds a family + work_item row for an
// ALREADY-EXISTING project — SeedFixtureOwners' own sibling for a test
// that needs a second WorkItem in the same project (SeedFixtureOwners
// itself always inserts a fresh project row, so it cannot be called twice
// for the same projectID).
func seedFixtureSecondWorkItem(ctx context.Context, store *Store, projectID, familyID, workItemID string) error {
	const timestamp = "2026-08-28T16:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO task_families(id, project_id, root_work_item_id, scope_version, status, version, created_at, updated_at)
VALUES (?, ?, ?, 1, 'ACTIVE', 1, ?, ?);`,
		familyID, projectID, workItemID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture family: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO work_items(id, project_id, kind, parent_id, family_id, title, status, version, created_at, updated_at)
VALUES (?, ?, 'ROOT', NULL, ?, 'Test fixture work item', 'ACTIVE', 1, ?, ?);`,
		workItemID, projectID, familyID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture work item: %w", err)
	}
	return nil
}

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

// TestMessageRepository_AppendMessage_AttemptBelongsToDifferentWorkItem_Rejected
// is the audit finding (2026-09-08) fix: the original check only verified
// req.AttemptID named a real execution_attempts row, never that the row
// actually traced back to req.WorkItemID — a caller could otherwise link a
// Message to an Attempt from a different WorkItem in the SAME project.
func TestMessageRepository_AppendMessage_AttemptBelongsToDifferentWorkItem_Rejected(t *testing.T) {
	store := openCatalogTestStore(t, "messages-attempt-cross-workitem.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	// A second WorkItem in the SAME project — SeedFixtureOwners itself
	// always inserts a fresh project row, so it cannot be called twice for
	// project-1; this adds only the family + work_item rows a second
	// sibling WorkItem needs.
	if err := seedFixtureSecondWorkItem(ctx, store, "project-1", "family-2", "work-item-2"); err != nil {
		t.Fatalf("seedFixtureSecondWorkItem: %v", err)
	}
	// attempt-1 belongs to work-item-2, not work-item-1.
	if err := SeedFixtureExecutionAttempt(ctx, store, "project-1", "family-2", "work-item-2", "attempt-1"); err != nil {
		t.Fatalf("SeedFixtureExecutionAttempt: %v", err)
	}
	artifactID := seedTestArtifact(t, store, "project-1", "artifact-1")

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := messageRepository{tx: tx}.AppendMessage(ctx, ports.AppendMessageRequest{
			ID: "msg-1", ProjectID: "project-1", WorkItemID: "work-item-1", AttemptID: "attempt-1",
			Actor: "assistant", Role: message.RoleAssistant, ContentArtifactID: artifactID, CreatedAt: time.Now(),
		})
		if !errors.Is(err, ports.ErrCrossWorkItemReference) {
			t.Fatalf("err = %v, want ports.ErrCrossWorkItemReference", err)
		}
		return nil
	})
}

// TestMessageRepository_AppendMessage_AttemptBelongsToDifferentProject_Rejected
// is the same fix's other half: the Attempt's own resolved Project must
// also match req.ProjectID, not just its WorkItem.
func TestMessageRepository_AppendMessage_AttemptBelongsToDifferentProject_Rejected(t *testing.T) {
	store := openCatalogTestStore(t, "messages-attempt-cross-project.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureOwners(ctx, store, "project-2", "family-2", "work-item-2"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	// attempt-1 belongs to project-2's own work-item-2.
	if err := SeedFixtureExecutionAttempt(ctx, store, "project-2", "family-2", "work-item-2", "attempt-1"); err != nil {
		t.Fatalf("SeedFixtureExecutionAttempt: %v", err)
	}
	artifactID := seedTestArtifact(t, store, "project-1", "artifact-1")

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := messageRepository{tx: tx}.AppendMessage(ctx, ports.AppendMessageRequest{
			ID: "msg-1", ProjectID: "project-1", WorkItemID: "work-item-1", AttemptID: "attempt-1",
			Actor: "assistant", Role: message.RoleAssistant, ContentArtifactID: artifactID, CreatedAt: time.Now(),
		})
		if !errors.Is(err, ports.ErrCrossWorkItemReference) {
			t.Fatalf("err = %v, want ports.ErrCrossWorkItemReference", err)
		}
		return nil
	})
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
