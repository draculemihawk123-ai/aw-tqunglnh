package message_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// This file exercises AppendConversationAttachment's own genuine
// cross-goroutine concurrency against the REAL sqlite adapter — mirroring
// internal/app/artifactsweep/sweep_sqlite_test.go's own "this file exercises
// the real stack end to end" pattern, and (see attachment_test.go's own
// doc comment at the point these tests used to live) matching how every
// OTHER genuine-concurrency test in this codebase
// (internal/adapters/sqlite/scheduling_test.go's own
// TestWriteLeaseRaceHasOneWinnerForSameRepository) is written: fake.UnitOfWork
// is a single-call-at-a-time test double, unable to let two goroutines'
// own WithSerializedWrite calls genuinely overlap the way sqlite's own
// BEGIN IMMEDIATE does (queuing a second writer rather than rejecting it).
func setupSQLiteAttachmentFixture(t *testing.T, dbName string) (ports.UnitOfWork, ports.ArtifactStore, string) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), dbName))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: "project-1", Name: "project-1"})
		return err
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	regCmd := testCommand("idem-repo-1", "hash-repo-1")
	regCmd.Type = "RegisterRepository"
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-1", ProjectID: "project-1", Name: "repo-1",
		RemoteLocator: "https://example.invalid/repo-1.git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing,
		}); err != nil {
			return err
		}
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryActive,
		})
		return err
	}); err != nil {
		t.Fatalf("activate repository: %v", err)
	}

	rootCmd := testCommand("idem-root-1", "hash-root-1")
	rootCmd.Type = "CreateRootWorkItem"
	root, err := work.CreateRootWorkItem(ctx, uow, ids, rootCmd, work.CreateRootWorkItemRequest{
		ProjectID: "project-1", Title: "Implement the thing",
		InitialScope: []work.ScopeGrantRequest{{
			RepositoryID: "repo-1", Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"services/api"}, Reason: "implement the thing",
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}

	artStore, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}
	return uow, artStore, root.WorkItemID
}

// TestAppendConversationAttachment_SameKeyConcurrency_TwoIdenticalRetriesRacing
// forces the Verify checklist's own "same-key concurrency" scenario: two
// truly concurrent goroutines dispatch the IDENTICAL command (same
// Idempotency-Key, same content) against the SAME real sqlite-backed
// UnitOfWork/ArtifactStore. Both must succeed with the IDENTICAL result,
// and exactly one Message row must exist afterward.
func TestAppendConversationAttachment_SameKeyConcurrency_TwoIdenticalRetriesRacing(t *testing.T) {
	uow, store, workItemID := setupSQLiteAttachmentFixture(t, "attach-race-same-key.db")
	clk := clock.NewFixed(time.Now())
	content := []byte("two identical retries racing against each other")
	digest := sha256Hex(content)
	cmd := attachmentCommand("idem-race-1", "hash-race-1")

	var wg sync.WaitGroup
	results := make([]appmessage.AppendMessageResult, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = appmessage.AppendConversationAttachment(context.Background(), uow, store, idsource.Random{}, clk, cmd,
				baseAttachmentRequest(workItemID, content, "text/plain", digest))
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if results[0].MessageID != results[1].MessageID || results[0].Sequence != results[1].Sequence {
		t.Fatalf("expected identical results from both racers, got %+v vs %+v", results[0], results[1])
	}

	msgs, err := appmessage.ListMessages(context.Background(), uow, workItemID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1 (no duplicate from racing identical retries)", len(msgs))
	}
}

// TestAppendConversationAttachment_DifferentKeyConcurrency_SharedContentBytes
// forces the Verify checklist's own "different-key concurrency ... including
// two that happen to share content bytes" AND "shared blob" scenarios in
// one: two truly concurrent goroutines, DIFFERENT Idempotency-Keys, the
// IDENTICAL raw bytes, against the SAME real sqlite-backed
// UnitOfWork/ArtifactStore. Both must succeed independently — two different
// Message rows, two different Artifact rows — while sharing the SAME
// underlying content-addressed Locator (ArtifactStore's own Put-level
// dedup, one layer below this command).
func TestAppendConversationAttachment_DifferentKeyConcurrency_SharedContentBytes(t *testing.T) {
	uow, store, workItemID := setupSQLiteAttachmentFixture(t, "attach-race-diff-key.db")
	clk := clock.NewFixed(time.Now())
	content := []byte("shared content bytes across two independent uploads")
	digest := sha256Hex(content)
	keys := []string{"idem-diff-a", "idem-diff-b"}

	var wg sync.WaitGroup
	results := make([]appmessage.AppendMessageResult, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := attachmentCommand(keys[i], "hash-diff-"+keys[i])
			results[i], errs[i] = appmessage.AppendConversationAttachment(context.Background(), uow, store, idsource.Random{}, clk, cmd,
				baseAttachmentRequest(workItemID, content, "text/plain", digest))
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if results[0].MessageID == results[1].MessageID {
		t.Fatalf("expected two DIFFERENT message ids, got the same: %s", results[0].MessageID)
	}
	if results[0].ContentArtifactID == results[1].ContentArtifactID {
		t.Fatalf("expected two DIFFERENT artifact ids, got the same: %s", results[0].ContentArtifactID)
	}

	msgs, err := appmessage.ListMessages(context.Background(), uow, workItemID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("len(msgs) = %d, want 2", len(msgs))
	}

	var locatorA, locatorB string
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		a, err := tx.Artifacts().GetArtifact(context.Background(), results[0].ContentArtifactID)
		if err != nil {
			return err
		}
		locatorA = a.Locator
		b, err := tx.Artifacts().GetArtifact(context.Background(), results[1].ContentArtifactID)
		if err != nil {
			return err
		}
		locatorB = b.Locator
		return nil
	}); err != nil {
		t.Fatalf("read artifacts: %v", err)
	}
	if locatorA != locatorB {
		t.Fatalf("expected a shared Locator (content-addressed dedup), got %s vs %s", locatorA, locatorB)
	}
}
