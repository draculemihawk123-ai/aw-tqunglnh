package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// TestMigration46_UpgradeWithRealData_KeepsMessagesAndSnapshots proves migration
// 0046 (V9-07) on a database that already holds a message and a context snapshot
// written before it: apply every migration through 45, insert both with the
// pre-0046 columns, apply migration 46, then prove the message loads as not
// pinned, the snapshot still loads through the tamper check with no omitted
// message, and both new columns are usable afterwards.
func TestMigration46_UpgradeWithRealData_KeepsMessagesAndSnapshots(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-migration46-upgrade.db")
	store := openWithoutMigrating(t, databasePath)
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 45)

	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureExecutionAttempt(ctx, store, "project-1", "family-1", "work-item-1", "attempt-1"); err != nil {
		t.Fatalf("SeedFixtureExecutionAttempt: %v", err)
	}
	artifactID := seedTestArtifact(t, store, "project-1", "artifact-1")
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO messages (id, project_id, work_item_id, attempt_id, sequence, actor, role, content_artifact_id, correlation_id, created_at)
VALUES ('msg-1', 'project-1', 'work-item-1', NULL, 1, 'user-1', 'USER', ?, 'corr-1', ?)`, artifactID, formatWorkflowTime(time.Now())); err != nil {
		t.Fatalf("seed pre-0046 message: %v", err)
	}
	legacy := newTestSnapshot(t, "snap-1", "project-1", "work-item-1", "attempt-1")
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO attempt_context_snapshots (id, project_id, work_item_id, attempt_id, message_refs_json, resource_refs_json, evidence_refs_json, revision_set_json, manifest_hash, created_at)
VALUES ('snap-1', 'project-1', 'work-item-1', 'attempt-1', '[{"MessageID":"msg-1"}]', '[{"ResourceKey":"res-1","ContentHash":"hash-1"}]', '[{"EvidenceID":"evidence-1"}]', '[{"RepositoryID":"repo-1","VCSObjectID":"abc123","WorkspaceGeneration":1}]', ?, ?)`,
		legacy.ManifestHash, formatWorkflowTime(legacy.CreatedAt)); err != nil {
		t.Fatalf("seed pre-0046 snapshot: %v", err)
	}

	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	if err := applyOneMigration(ctx, conn, findMigration(t, 46)); err != nil {
		t.Fatalf("apply migration 46: %v", err)
	}
	// The repository reads the columns of every later migration too (it selects
	// them by name), so bring the database to the latest schema before loading
	// through it; what is under test is that rows written before 46 survive.
	for version := 47; ; version++ {
		later, err := loadMigrations()
		if err != nil {
			t.Fatalf("loadMigrations: %v", err)
		}
		var found *migration
		for i := range later {
			if later[i].Version == version {
				found = &later[i]
			}
		}
		if found == nil {
			break
		}
		if err := applyOneMigration(ctx, conn, *found); err != nil {
			t.Fatalf("apply migration %d: %v", version, err)
		}
	}

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		m, err := messageRepository{tx: tx}.GetMessage(ctx, "msg-1")
		if err != nil {
			t.Fatalf("GetMessage after migration 46: %v", err)
		}
		if m.Pinned {
			t.Fatal("a message written before migration 46 loads as pinned")
		}
		loaded, err := contextSnapshotRepository{tx: tx}.GetSnapshot(ctx, "snap-1")
		if err != nil {
			t.Fatalf("GetSnapshot after migration 46 (tamper check): %v", err)
		}
		if len(loaded.OmittedMessageRefs) != 0 || loaded.ManifestHash != legacy.ManifestHash {
			t.Fatalf("pre-0046 snapshot after migration = omitted %v hash %s, want none and %s", loaded.OmittedMessageRefs, loaded.ManifestHash, legacy.ManifestHash)
		}
		return nil
	})

	if _, err := store.db.ExecContext(ctx, `UPDATE messages SET pinned = 2 WHERE id = 'msg-1'`); err == nil {
		t.Fatal("the pinned CHECK accepted 2")
	}
}

func TestMessageRepository_PinnedRoundTrips(t *testing.T) {
	store := openCatalogTestStore(t, "messages-pinned.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	artifactID := seedTestArtifact(t, store, "project-1", "artifact-1")
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		repo := messageRepository{tx: tx}
		for _, req := range []ports.AppendMessageRequest{
			{ID: "msg-plain", ProjectID: "project-1", WorkItemID: "work-item-1", Actor: "user-1", Role: message.RoleUser, ContentArtifactID: artifactID, CorrelationID: "c1", CreatedAt: time.Now()},
			{ID: "msg-pinned", ProjectID: "project-1", WorkItemID: "work-item-1", Actor: "user-1", Role: message.RoleUser, ContentArtifactID: artifactID, CorrelationID: "c2", CreatedAt: time.Now(), Pinned: true},
		} {
			if _, err := repo.AppendMessage(ctx, req); err != nil {
				t.Fatalf("AppendMessage %s: %v", req.ID, err)
			}
		}
		all, err := repo.ListMessagesForWorkItem(ctx, "work-item-1")
		if err != nil {
			t.Fatalf("ListMessagesForWorkItem: %v", err)
		}
		if len(all) != 2 || all[0].Pinned || !all[1].Pinned {
			t.Fatalf("pinned flags = %+v, want [false true]", all)
		}
		return nil
	})
}

func TestContextSnapshotRepository_OmittedMessageRefsRoundTrip(t *testing.T) {
	store := openCatalogTestStore(t, "context-snapshots-omitted.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureExecutionAttempt(ctx, store, "project-1", "family-1", "work-item-1", "attempt-1"); err != nil {
		t.Fatalf("SeedFixtureExecutionAttempt: %v", err)
	}
	snap, err := contextsnapshot.NewSnapshot(
		"snap-omitted", project.ProjectID("project-1"), work.WorkItemID("work-item-1"), contextsnapshot.AttemptID("attempt-1"),
		[]contextsnapshot.MessageRef{{MessageID: "msg-3"}}, nil, nil, testRevisions(t), time.Now().UTC(),
		contextsnapshot.WithInstructionSchemaVersion(contextsnapshot.InstructionSchemaV2),
		contextsnapshot.WithOmittedMessageRefs([]contextsnapshot.OmittedMessageRef{
			{MessageID: "msg-1", Reason: contextsnapshot.OmittedMessageBudgetExceeded},
			{MessageID: "msg-2", Reason: contextsnapshot.OmittedMessageBudgetExceeded},
		}),
	)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		repo := contextSnapshotRepository{tx: tx}
		if _, err := repo.CreateSnapshot(ctx, snap); err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}
		loaded, err := repo.GetSnapshot(ctx, "snap-omitted")
		if err != nil {
			t.Fatalf("GetSnapshot: %v", err)
		}
		if len(loaded.OmittedMessageRefs) != 2 || loaded.OmittedMessageRefs[0].MessageID != "msg-1" || loaded.ManifestHash != snap.ManifestHash {
			t.Fatalf("loaded omitted refs = %+v hash %s, want msg-1,msg-2 and %s", loaded.OmittedMessageRefs, loaded.ManifestHash, snap.ManifestHash)
		}
		// A tampered omitted list no longer re-derives the recorded hash.
		if _, err := tx.ExecContext(ctx, `UPDATE attempt_context_snapshots SET omitted_message_refs_json = NULL WHERE id = 'snap-omitted'`); err != nil {
			t.Fatalf("tamper: %v", err)
		}
		if _, err := repo.GetSnapshot(ctx, "snap-omitted"); err == nil {
			t.Fatal("GetSnapshot accepted a snapshot whose omitted message list was removed")
		}
		return nil
	})
}
