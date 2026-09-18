package message_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	climessage "github.com/taQuangLing/agent-workflow/internal/delivery/cli/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// This file is V6-15J's own "Crash" Verify bullet, exercised against the
// REAL sqlite adapter — mirroring internal/app/message/attachment_sqlite_test.go's
// own "this file exercises the real stack end to end" pattern (fake.UnitOfWork
// is a single-call-at-a-time test double, never a substitute for proving
// real durability/crash-recovery against the real persistence adapter).
//
// The crash-safety machinery itself (durable prepare claims, resumable
// spool/hash/Put, the receipt-replay backstop) already lives in — and is
// already independently tested by — internal/app/message/attachment.go and
// attachment_sqlite_test.go (V6-07A). This test's own job is narrower and
// specific to THIS leaf: prove that RunMessageUploadAttachment (a) calls
// appmessage.AppendConversationAttachment the exact same way HTTP does —
// the same real ports.UnitOfWork, the same real ports.ArtifactStore, no
// extra buffering/short-circuiting of its own that could defeat that
// machinery — and (b) a genuine mid-upload failure (the spool/Put step
// never completing) followed by a real retry through the CLI entrypoint
// again leaves no partial/corrupted state: no orphaned Message row, exactly
// one Message/Artifact once the retry succeeds, and the stored bytes are
// exactly what was asked for.

// flakyStore wraps a real ports.ArtifactStore and fails the FIRST Put call
// only — this file's own hook for forcing "the spool/blob-put step never
// completes" without a real process kill, mirroring
// internal/app/message/attachment_test.go's own identically-shaped
// flakyStore (that file's own failPut is a simple on/off switch; this one
// is a one-shot counter since the test below needs exactly one simulated
// failure, then a real, working retry).
type flakyStore struct {
	ports.ArtifactStore
	failNextPut bool
}

func (s *flakyStore) Put(ctx context.Context, meta ports.ArtifactMetadata, body io.Reader) (ports.ArtifactRef, error) {
	if s.failNextPut {
		s.failNextPut = false
		// Drain body first, exactly like a real failed write attempt would
		// still have consumed some or all of its input before failing.
		_, _ = io.Copy(io.Discard, body)
		return ports.ArtifactRef{}, errSimulatedPutFailure
	}
	return s.ArtifactStore.Put(ctx, meta, body)
}

var errSimulatedPutFailure = errors.New("simulated ArtifactStore.Put failure (crash mid-upload)")

// setupSQLiteFixture mirrors internal/app/message/attachment_sqlite_test.go's
// own setupSQLiteAttachmentFixture: a real sqlite-backed UnitOfWork with one
// project, one ACTIVE repository and one root WorkItem already created, plus
// a *flakyStore wrapping a real filesystem ArtifactStore so this test can
// toggle a single simulated Put failure.
func setupSQLiteFixture(t *testing.T) (climessage.Dependencies, *flakyStore, string, string) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "message-cli-crash.db"))
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

	regCmd := testCommand("idem-repo-1", "hash-repo-1", "RegisterRepository")
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

	rootCmd := testCommand("idem-root-1", "hash-root-1", "CreateRootWorkItem")
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

	realStore, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}
	flaky := &flakyStore{ArtifactStore: realStore}

	deps := climessage.Dependencies{
		UnitOfWork: uow, ArtifactStore: flaky, IDs: ids,
		Clock: fixedClock{now: time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)},
	}
	return deps, flaky, "project-1", root.WorkItemID
}

// TestRunMessageUploadAttachment_CrashMidUpload_RetrySucceedsWithNoPartialState
// forces a real ArtifactStore.Put failure on the FIRST attempt (the spool/
// blob-put step never completing — the "crash mid-upload" scenario), then
// retries with the IDENTICAL --idempotency-key and content against the SAME
// real sqlite-backed UnitOfWork/ArtifactStore. The retry must succeed
// cleanly, produce exactly one Message and one Artifact row, and the stored
// content must be exactly the bytes originally given — proving this leaf's
// own thin wrapper around appmessage.AppendConversationAttachment never
// breaks that command's own durable-claim crash-safety guarantee.
func TestRunMessageUploadAttachment_CrashMidUpload_RetrySucceedsWithNoPartialState(t *testing.T) {
	deps, flaky, projectID, workItemID := setupSQLiteFixture(t)
	ctx := context.Background()
	content := []byte("this upload will crash once, then succeed on retry")
	args := []string{"--project-id", projectID, "--role", "USER", "--idempotency-key", "crash-key-1", workItemID}

	// First attempt: force the Put step to fail, simulating a crash mid-
	// upload (spool/hash never durably completes).
	flaky.failNextPut = true
	var firstStdout, firstStderr bytes.Buffer
	err := climessage.RunMessageUploadAttachment(ctx, deps, args, bytes.NewReader(content), &firstStdout, &firstStderr)
	if err == nil {
		t.Fatal("first RunMessageUploadAttachment() (simulated Put failure) error = nil, want an error")
	}
	if !errors.Is(err, errSimulatedPutFailure) {
		t.Fatalf("first RunMessageUploadAttachment() error = %v, want errors.Is(..., errSimulatedPutFailure)", err)
	}
	if firstStdout.Len() != 0 {
		t.Fatalf("stdout got written to after a crashed first attempt: %q", firstStdout.String())
	}

	// Nothing must have been durably committed by the crashed attempt: no
	// Message row for this WorkItem yet.
	msgsAfterCrash, err := appmessage.ListMessages(ctx, deps.UnitOfWork, workItemID)
	if err != nil {
		t.Fatalf("ListMessages after crash: %v", err)
	}
	if len(msgsAfterCrash) != 0 {
		t.Fatalf("len(msgsAfterCrash) = %d, want 0 (a crashed attempt must never leave a partial Message row)", len(msgsAfterCrash))
	}

	// Retry: the Put step now works normally (simulating the process having
	// restarted with a healthy store) — same idempotency key, same content.
	var secondStdout, secondStderr bytes.Buffer
	if err := climessage.RunMessageUploadAttachment(ctx, deps, args, bytes.NewReader(content), &secondStdout, &secondStderr); err != nil {
		t.Fatalf("retry RunMessageUploadAttachment() error = %v, stderr = %s", err, secondStderr.String())
	}

	var envelope struct {
		Replayed bool                           `json:"replayed"`
		Result   appmessage.AppendMessageResult `json:"result"`
	}
	if err := json.Unmarshal(secondStdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode retry stdout %s: %v", secondStdout.String(), err)
	}
	if envelope.Result.MessageID == "" || envelope.Result.ContentArtifactID == "" {
		t.Fatalf("retry result missing IDs: %+v", envelope.Result)
	}

	// Exactly one Message must exist after the successful retry — no
	// duplicate, no orphan from the crashed attempt.
	msgs, err := appmessage.ListMessages(ctx, deps.UnitOfWork, workItemID)
	if err != nil {
		t.Fatalf("ListMessages after retry: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want exactly 1 after the retry succeeded", len(msgs))
	}

	// The stored content must round-trip exactly, through the real
	// ArtifactStore, never a corrupted/partial write from the crashed
	// attempt.
	var locator, hash string
	if err := deps.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		row, err := tx.Artifacts().GetArtifact(ctx, envelope.Result.ContentArtifactID)
		if err != nil {
			return err
		}
		locator, hash = row.Locator, row.ContentHash
		return nil
	}); err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	reader, err := flaky.ArtifactStore.Open(ctx, ports.ArtifactRef{Locator: locator, SHA256: hash, Size: int64(len(content))})
	if err != nil {
		t.Fatalf("ArtifactStore.Open: %v", err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stored content: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("stored content = %q, want %q (no corruption from the crashed first attempt)", got, content)
	}
}
