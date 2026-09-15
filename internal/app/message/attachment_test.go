package message_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	messagedomain "github.com/taQuangLing/agent-workflow/internal/domain/message"
)

// This file is V6-07A's own crash-safety test suite
// (docs/design/08-v6-api-projections.md V6-07A) — every scenario the
// task's own Verify checklist names, forced for real via the two test
// hooks below (countingUOW/flakyStore) rather than argued about in a code
// comment: crash after spool/blob-put/claim-record/DB-commit-before-ack,
// same-key and different-key concurrency (including a shared blob), tamper,
// oversize, a required-media-type check, and restart-and-orphan-cleanup via
// ResumeOrCleanExpiredAttachmentClaims.

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func attachmentCommand(idempotencyKey, requestHash string) ports.Command {
	return ports.Command{
		ID: "attach-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"),
		RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type:        appmessage.AttachmentCommandType, RequestHash: requestHash,
	}
}

var errSimulatedCrash = errors.New("simulated crash")

// countingUOW wraps a real ports.UnitOfWork and fails the Nth
// WithSerializedWrite call (1-indexed) with errSimulatedCrash, letting
// every WithReadOnly call and every OTHER WithSerializedWrite call through
// unchanged. This is this test file's own "test hook that lets you commit
// up to some point, then simulate a restart by running the resume path
// fresh": AppendConversationAttachment's own sequence issues exactly 4
// WithSerializedWrite calls on a fresh (non-resumed) path — (1) claim
// insert, (2) record-blob-ready, (3) the Artifact+Message+event+receipt
// transaction, (4) release/ack — so failAtWrite=N forces a crash
// immediately after the (N-1)th one durably committed.
type countingUOW struct {
	ports.UnitOfWork
	failAtWrite int
	writeCalls  int
}

func (u *countingUOW) WithSerializedWrite(ctx context.Context, fn func(ports.Tx) error) error {
	u.writeCalls++
	if u.failAtWrite != 0 && u.writeCalls == u.failAtWrite {
		return errSimulatedCrash
	}
	return u.UnitOfWork.WithSerializedWrite(ctx, fn)
}

var errSimulatedPutFailure = errors.New("simulated ArtifactStore.Put failure")

// flakyStore wraps a real ports.ArtifactStore and fails every Put call
// while failPut is true — this test file's own hook for forcing "the
// spool/blob-put step never completes" without a real process kill.
type flakyStore struct {
	ports.ArtifactStore
	failPut bool
}

func (s *flakyStore) Put(ctx context.Context, meta ports.ArtifactMetadata, body io.Reader) (ports.ArtifactRef, error) {
	if s.failPut {
		return ports.ArtifactRef{}, errSimulatedPutFailure
	}
	return s.ArtifactStore.Put(ctx, meta, body)
}

func getAttachmentClaim(t *testing.T, uow ports.UnitOfWork, uploadID string) (ports.AttachmentPrepareClaim, bool) {
	t.Helper()
	var claim ports.AttachmentPrepareClaim
	var found bool
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		c, err := tx.AttachmentClaims().GetAttachmentClaim(context.Background(), uploadID)
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		claim, found = c, true
		return nil
	}); err != nil {
		t.Fatalf("read attachment claim %s: %v", uploadID, err)
	}
	return claim, found
}

func baseAttachmentRequest(workItemID string, body []byte, contentType, digest string) appmessage.AppendConversationAttachmentRequest {
	return appmessage.AppendConversationAttachmentRequest{
		ProjectID: "project-1", WorkItemID: workItemID, Role: messagedomain.RoleUser,
		Body: bytes.NewReader(body), ContentType: contentType, Sensitivity: redact.Public, DeclaredSHA256: digest,
	}
}

func TestAppendConversationAttachment_HappyPath_ProducesRetrievableArtifactAndMessage(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	content := []byte("hello attachment bytes")
	digest := sha256Hex(content)

	result, err := appmessage.AppendConversationAttachment(ctx, uow, store, ids, clk, attachmentCommand("idem-att-1", "hash-att-1"),
		baseAttachmentRequest(workItemID, content, "image/png", digest))
	if err != nil {
		t.Fatalf("AppendConversationAttachment: %v", err)
	}
	if result.MessageID == "" || result.ContentArtifactID == "" {
		t.Fatalf("result missing IDs: %+v", result)
	}

	msgs, err := appmessage.ListMessages(ctx, uow, workItemID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}

	var a struct {
		Locator, ContentHash, MediaType string
		Attached                        bool
	}
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		row, err := tx.Artifacts().GetArtifact(ctx, result.ContentArtifactID)
		if err != nil {
			return err
		}
		a.Locator, a.ContentHash, a.MediaType = row.Locator, row.ContentHash, row.MediaType
		a.Attached = row.AttachState == "ATTACHED"
		return nil
	}); err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	if !a.Attached {
		t.Fatalf("expected the artifact to be Attached, not Orphan")
	}
	if a.ContentHash != "sha256:"+digest {
		t.Fatalf("ContentHash = %s, want sha256:%s", a.ContentHash, digest)
	}
	if a.MediaType != "image/png" {
		t.Fatalf("MediaType = %s, want image/png", a.MediaType)
	}

	reader, err := store.Open(ctx, ports.ArtifactRef{Locator: a.Locator, SHA256: a.ContentHash, Size: int64(len(content))})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer reader.Close()
	got := make([]byte, len(content))
	if _, err := reader.Read(got); err != nil {
		t.Fatalf("read stored content: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("stored content = %q, want %q", got, content)
	}

	// The claim is released once the command has fully committed.
	uploadID := appmessage.DeterministicAttachmentUploadID(attachmentCommand("idem-att-1", "hash-att-1"))
	if _, found := getAttachmentClaim(t, uow, uploadID); found {
		t.Fatalf("expected the claim to be released after a successful happy path")
	}
}

func TestAppendConversationAttachment_Replay_SameCommand_ReturnsIdenticalResultNoDuplicateRow(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	content := []byte("replay me exactly")
	digest := sha256Hex(content)
	cmd := attachmentCommand("idem-replay-1", "hash-replay-1")

	first, err := appmessage.AppendConversationAttachment(ctx, uow, store, ids, clk, cmd, baseAttachmentRequest(workItemID, content, "text/plain", digest))
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := appmessage.AppendConversationAttachment(ctx, uow, store, ids, clk, cmd, baseAttachmentRequest(workItemID, content, "text/plain", digest))
	if err != nil {
		t.Fatalf("replay call: %v", err)
	}
	if first != second {
		t.Fatalf("replay result %+v != first result %+v", second, first)
	}
	msgs, _ := appmessage.ListMessages(ctx, uow, workItemID)
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
}

func TestAppendConversationAttachment_TamperedContent_DigestMismatch_NoRowsCreated_StaysResumable(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	content := []byte("actual bytes that will really be sent")
	wrongDigest := sha256Hex([]byte("declared digest for entirely different bytes"))
	cmd := attachmentCommand("idem-tamper-1", "hash-tamper-1")

	_, err := appmessage.AppendConversationAttachment(ctx, uow, store, ids, clk, cmd, baseAttachmentRequest(workItemID, content, "text/plain", wrongDigest))
	if !errors.Is(err, appmessage.ErrAttachmentDigestMismatch) {
		t.Fatalf("err = %v, want ErrAttachmentDigestMismatch", err)
	}
	msgs, _ := appmessage.ListMessages(ctx, uow, workItemID)
	if len(msgs) != 0 {
		t.Fatalf("expected no Message row, got %d", len(msgs))
	}

	uploadID := appmessage.DeterministicAttachmentUploadID(cmd)
	claim, found := getAttachmentClaim(t, uow, uploadID)
	if !found || claim.State != ports.AttachmentClaimSpooling {
		t.Fatalf("claim = %+v found=%v, want a still-SPOOLING claim (resumable, not abandoned mid-air)", claim, found)
	}

	// A retry with the identical (still wrong) declared digest fails
	// identically and idempotently — never silently "fixes itself".
	_, err = appmessage.AppendConversationAttachment(ctx, uow, store, ids, clk, cmd, baseAttachmentRequest(workItemID, content, "text/plain", wrongDigest))
	if !errors.Is(err, appmessage.ErrAttachmentDigestMismatch) {
		t.Fatalf("retry err = %v, want ErrAttachmentDigestMismatch again", err)
	}
}

func TestAppendConversationAttachment_Oversize_Rejected(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	content := bytes.Repeat([]byte("x"), appmessage.MaxAttachmentSize+1024)
	digest := sha256Hex(content)
	cmd := attachmentCommand("idem-oversize-1", "hash-oversize-1")

	_, err := appmessage.AppendConversationAttachment(ctx, uow, store, ids, clk, cmd, baseAttachmentRequest(workItemID, content, "application/octet-stream", digest))
	if !errors.Is(err, appmessage.ErrAttachmentTooLarge) {
		t.Fatalf("err = %v, want ErrAttachmentTooLarge", err)
	}
	msgs, _ := appmessage.ListMessages(ctx, uow, workItemID)
	if len(msgs) != 0 {
		t.Fatalf("expected no Message row, got %d", len(msgs))
	}
}

func TestAppendConversationAttachment_MissingContentType_Rejected(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	content := []byte("bytes")
	digest := sha256Hex(content)

	_, err := appmessage.AppendConversationAttachment(ctx, uow, store, ids, clk, attachmentCommand("idem-mt-1", "hash-mt-1"),
		baseAttachmentRequest(workItemID, content, "", digest))
	if err == nil || !strings.Contains(err.Error(), "ContentType") {
		t.Fatalf("err = %v, want a ContentType validation error", err)
	}
}

func TestAppendConversationAttachment_DigestRequiredAndMalformed(t *testing.T) {
	uow, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())

	_, err := appmessage.AppendConversationAttachment(ctx, uow, store, ids, clk, attachmentCommand("idem-nodigest", "h1"),
		baseAttachmentRequest(workItemID, []byte("x"), "text/plain", ""))
	if !errors.Is(err, appmessage.ErrAttachmentDigestRequired) {
		t.Fatalf("err = %v, want ErrAttachmentDigestRequired", err)
	}

	_, err = appmessage.AppendConversationAttachment(ctx, uow, store, ids, clk, attachmentCommand("idem-baddigest", "h2"),
		baseAttachmentRequest(workItemID, []byte("x"), "text/plain", "not-a-valid-hex-digest"))
	if !errors.Is(err, appmessage.ErrAttachmentDigestMalformed) {
		t.Fatalf("err = %v, want ErrAttachmentDigestMalformed", err)
	}
}

// TestAppendConversationAttachment_CrashAfterClaimRecord_BeforePut_RetrySucceeds forces
// the Verify checklist's own "crash after claim-record" (and, since
// ArtifactStore.Put is itself atomic, this is indistinguishable from
// "crash after spool" from any external observer — see attachment.go's own
// top-of-file doc comment) point: the claim commits durably, then Put
// itself never completes. A subsequent retry (same command) must resume
// cleanly to exactly one committed Message.
func TestAppendConversationAttachment_CrashAfterClaimRecord_BeforePut_RetrySucceeds(t *testing.T) {
	uow, realStore, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	content := []byte("resumable after a put failure")
	digest := sha256Hex(content)
	cmd := attachmentCommand("idem-putfail-1", "hash-putfail-1")

	flaky := &flakyStore{ArtifactStore: realStore, failPut: true}
	_, err := appmessage.AppendConversationAttachment(ctx, uow, flaky, ids, clk, cmd, baseAttachmentRequest(workItemID, content, "text/plain", digest))
	if !errors.Is(err, errSimulatedPutFailure) {
		t.Fatalf("setup err = %v, want errSimulatedPutFailure", err)
	}

	uploadID := appmessage.DeterministicAttachmentUploadID(cmd)
	claim, found := getAttachmentClaim(t, uow, uploadID)
	if !found || claim.State != ports.AttachmentClaimSpooling {
		t.Fatalf("claim = %+v found=%v, want a durable SPOOLING claim", claim, found)
	}
	msgs, _ := appmessage.ListMessages(ctx, uow, workItemID)
	if len(msgs) != 0 {
		t.Fatalf("expected no Message row yet, got %d", len(msgs))
	}

	// "Restart": retry against the SAME durable state with a working store.
	flaky.failPut = false
	result, err := appmessage.AppendConversationAttachment(ctx, uow, flaky, ids, clk, cmd, baseAttachmentRequest(workItemID, content, "text/plain", digest))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if result.MessageID == "" {
		t.Fatalf("expected a real result on retry")
	}
	msgs, _ = appmessage.ListMessages(ctx, uow, workItemID)
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) after retry = %d, want 1 (no duplicate)", len(msgs))
	}
	if _, found := getAttachmentClaim(t, uow, uploadID); found {
		t.Fatalf("expected the claim to be released after the successful retry")
	}
}

// TestAppendConversationAttachment_CrashAfterBlobPut_BeforeClaimRecorded_RetrySucceeds
// forces the Verify checklist's own "crash after blob-put" point: the
// content-addressed bytes are ALREADY durably stored, but the claim never
// advances to BLOB_READY and no Artifact/Message row is ever created. A
// retry must re-Put the identical bytes (a safe, content-addressed no-op)
// and complete to exactly one committed Message.
func TestAppendConversationAttachment_CrashAfterBlobPut_BeforeClaimRecorded_RetrySucceeds(t *testing.T) {
	baseUOW, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	content := []byte("crash right after the blob is durably put")
	digest := sha256Hex(content)
	cmd := attachmentCommand("idem-blobput-1", "hash-blobput-1")

	crashing := &countingUOW{UnitOfWork: baseUOW, failAtWrite: 2}
	_, err := appmessage.AppendConversationAttachment(ctx, crashing, store, ids, clk, cmd, baseAttachmentRequest(workItemID, content, "text/plain", digest))
	if !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("setup err = %v, want errSimulatedCrash", err)
	}

	uploadID := appmessage.DeterministicAttachmentUploadID(cmd)
	claim, found := getAttachmentClaim(t, baseUOW, uploadID)
	if !found || claim.State != ports.AttachmentClaimSpooling {
		t.Fatalf("claim = %+v found=%v, want SPOOLING (blob-ready was never recorded)", claim, found)
	}
	msgs, _ := appmessage.ListMessages(ctx, baseUOW, workItemID)
	if len(msgs) != 0 {
		t.Fatalf("expected no Message row yet, got %d", len(msgs))
	}

	result, err := appmessage.AppendConversationAttachment(ctx, baseUOW, store, ids, clk, cmd, baseAttachmentRequest(workItemID, content, "text/plain", digest))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if result.MessageID == "" {
		t.Fatalf("expected a real result on retry")
	}
	msgs, _ = appmessage.ListMessages(ctx, baseUOW, workItemID)
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) after retry = %d, want 1 (no duplicate)", len(msgs))
	}
}

// TestAppendConversationAttachment_CrashAfterDBCommit_BeforeAck_ReplayReleasesClaim
// forces the Verify checklist's own "crash after DB commit but before ack"
// point: the Artifact+Message+event+receipt transaction fully commits, but
// the claim's own release/ack step never runs. The command itself must
// still report success (release failure is swallowed by design — the
// receipt is already the durable, permanent proof); the NEXT contact (a
// genuine retry) must replay the identical result and release the claim.
func TestAppendConversationAttachment_CrashAfterDBCommit_BeforeAck_ReplayReleasesClaim(t *testing.T) {
	baseUOW, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	content := []byte("commits fully but the ack/release step never runs")
	digest := sha256Hex(content)
	cmd := attachmentCommand("idem-ack-1", "hash-ack-1")

	crashing := &countingUOW{UnitOfWork: baseUOW, failAtWrite: 4}
	result, err := appmessage.AppendConversationAttachment(ctx, crashing, store, ids, clk, cmd, baseAttachmentRequest(workItemID, content, "text/plain", digest))
	if err != nil {
		t.Fatalf("expected the command to still report success (release failure is swallowed), got %v", err)
	}
	if result.MessageID == "" {
		t.Fatalf("expected a real, committed result")
	}

	uploadID := appmessage.DeterministicAttachmentUploadID(cmd)
	if _, found := getAttachmentClaim(t, baseUOW, uploadID); !found {
		t.Fatalf("expected the claim row to still exist (ack/release never ran)")
	}
	msgs, _ := appmessage.ListMessages(ctx, baseUOW, workItemID)
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1 (already committed)", len(msgs))
	}

	replay, err := appmessage.AppendConversationAttachment(ctx, baseUOW, store, ids, clk, cmd, baseAttachmentRequest(workItemID, content, "text/plain", digest))
	if err != nil {
		t.Fatalf("replay retry: %v", err)
	}
	if replay != result {
		t.Fatalf("replay result %+v != original result %+v", replay, result)
	}
	if _, found := getAttachmentClaim(t, baseUOW, uploadID); found {
		t.Fatalf("expected the claim to be released after the replay retry")
	}
	msgs, _ = appmessage.ListMessages(ctx, baseUOW, workItemID)
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) after replay = %d, want 1 (never a duplicate)", len(msgs))
	}
}

// Genuine cross-goroutine concurrency tests (same-key and different-key
// racing, including the "shared blob" scenario) live in
// attachment_sqlite_test.go, against the REAL sqlite adapter — fake.UnitOfWork
// (internal/app/ports/fake) is a single-call-at-a-time test double (its own
// ErrNestedTransaction guard rejects a second WithSerializedWrite/WithReadOnly
// call that overlaps an in-flight one, even from a different goroutine), so
// it cannot exercise a real race the way sqlite's own BEGIN IMMEDIATE
// (which queues concurrent writers rather than rejecting them) does —
// mirroring how this codebase's own other genuine-concurrency tests
// (e.g. internal/adapters/sqlite/scheduling_test.go's own
// TestWriteLeaseRaceHasOneWinnerForSameRepository) are always written
// against the real adapter, never the fake.

// TestAppendConversationAttachment_SameIdempotencyKeyDifferentDigest_Conflict
// proves the "mismatch" bullet in attachment.go's own top-of-file doc
// comment: a claim already exists for this exact UploadID (same
// Actor/Scope/IdempotencyKey/CommandType) — created by a FIRST attempt that
// never got as far as recording a receipt (simulated via a Put failure,
// exactly like the crash-after-claim-record test above) — and a SECOND
// attempt reusing the identical Idempotency-Key but a DIFFERENT declared
// digest must be rejected as a real conflict, never silently resolved
// either way.
func TestAppendConversationAttachment_SameIdempotencyKeyDifferentDigest_Conflict(t *testing.T) {
	uow, realStore, ids, workItemID := setupFixture(t)
	clk := clock.NewFixed(time.Now())
	contentA := []byte("content A, the first declared upload")
	contentB := []byte("content B, a different upload reusing the same key")
	cmdA := attachmentCommand("idem-conflict-1", "hash-conflict-A")
	cmdB := attachmentCommand("idem-conflict-1", "hash-conflict-B")

	flaky := &flakyStore{ArtifactStore: realStore, failPut: true}
	_, err := appmessage.AppendConversationAttachment(context.Background(), uow, flaky, ids, clk, cmdA,
		baseAttachmentRequest(workItemID, contentA, "text/plain", sha256Hex(contentA)))
	if !errors.Is(err, errSimulatedPutFailure) {
		t.Fatalf("setup err = %v, want errSimulatedPutFailure", err)
	}

	_, err = appmessage.AppendConversationAttachment(context.Background(), uow, flaky, ids, clk, cmdB,
		baseAttachmentRequest(workItemID, contentB, "text/plain", sha256Hex(contentB)))
	if !errors.Is(err, appmessage.ErrAttachmentUploadConflict) {
		t.Fatalf("err = %v, want ErrAttachmentUploadConflict", err)
	}
}

func TestAppendConversationAttachment_StaleSpoolingClaim_SurvivesPastItsOwnLease_StillResumable(t *testing.T) {
	uow, realStore, ids, workItemID := setupFixture(t)
	clk := clock.NewFixed(time.Now())
	content := []byte("stale spooling claim, resumed by a later attempt")
	digest := sha256Hex(content)
	cmd := attachmentCommand("idem-stale-spool-1", "hash-stale-spool-1")

	flaky := &flakyStore{ArtifactStore: realStore, failPut: true}
	if _, err := appmessage.AppendConversationAttachment(context.Background(), uow, flaky, ids, clk, cmd,
		baseAttachmentRequest(workItemID, content, "text/plain", digest)); !errors.Is(err, errSimulatedPutFailure) {
		t.Fatalf("setup err = %v, want errSimulatedPutFailure", err)
	}

	clk.Advance(3 * time.Minute) // past attachmentClaimLease (2 minutes)
	flaky.failPut = false
	if _, err := appmessage.AppendConversationAttachment(context.Background(), uow, flaky, ids, clk, cmd,
		baseAttachmentRequest(workItemID, content, "text/plain", digest)); err != nil {
		t.Fatalf("retry after lease expiry: %v", err)
	}

	msgs, _ := appmessage.ListMessages(context.Background(), uow, workItemID)
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
}

// --- ResumeOrCleanExpiredAttachmentClaims: restart-and-orphan-cleanup ---

func TestResumeOrCleanExpiredAttachmentClaims_AbandonedBlobReadyClaim_PurgesBlobAndReleasesClaim(t *testing.T) {
	baseUOW, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	content := []byte("abandoned upload, nobody ever retries")
	digest := sha256Hex(content)
	cmd := attachmentCommand("idem-orphan-1", "hash-orphan-1")

	crashing := &countingUOW{UnitOfWork: baseUOW, failAtWrite: 3}
	if _, err := appmessage.AppendConversationAttachment(ctx, crashing, store, ids, clk, cmd,
		baseAttachmentRequest(workItemID, content, "text/plain", digest)); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("setup err = %v, want errSimulatedCrash", err)
	}

	uploadID := appmessage.DeterministicAttachmentUploadID(cmd)
	claim, found := getAttachmentClaim(t, baseUOW, uploadID)
	if !found || claim.State != ports.AttachmentClaimBlobReady {
		t.Fatalf("setup: claim = %+v found=%v, want BLOB_READY", claim, found)
	}
	ref := ports.ArtifactRef{Locator: claim.Locator, SHA256: claim.ActualSHA256, Size: claim.Size}
	if err := store.Verify(ctx, ref); err != nil {
		t.Fatalf("setup: expected the blob to actually be durably stored: %v", err)
	}

	clk.Advance(30 * time.Minute) // past attachmentStaleClaimGrace (20 minutes)
	report, err := appmessage.ResumeOrCleanExpiredAttachmentClaims(ctx, baseUOW, store, clk)
	if err != nil {
		t.Fatalf("ResumeOrCleanExpiredAttachmentClaims: %v", err)
	}
	if report.Released != 1 || report.Purged != 1 {
		t.Fatalf("report = %+v, want Released=1 Purged=1", report)
	}

	if _, found := getAttachmentClaim(t, baseUOW, uploadID); found {
		t.Fatalf("expected the claim to be released")
	}
	if err := store.Verify(ctx, ref); err == nil {
		t.Fatalf("expected the orphaned blob to actually be deleted")
	}
	msgs, _ := appmessage.ListMessages(ctx, baseUOW, workItemID)
	if len(msgs) != 0 {
		t.Fatalf("expected no Message row was ever created, got %d", len(msgs))
	}
}

func TestResumeOrCleanExpiredAttachmentClaims_CompletedClaim_ReleasesWithoutTouchingArtifact(t *testing.T) {
	baseUOW, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	content := []byte("completed but nobody ever acked")
	digest := sha256Hex(content)
	cmd := attachmentCommand("idem-sweep-committed-1", "hash-sweep-committed-1")

	crashing := &countingUOW{UnitOfWork: baseUOW, failAtWrite: 4}
	if _, err := appmessage.AppendConversationAttachment(ctx, crashing, store, ids, clk, cmd,
		baseAttachmentRequest(workItemID, content, "text/plain", digest)); err != nil {
		t.Fatalf("setup: %v", err)
	}

	uploadID := appmessage.DeterministicAttachmentUploadID(cmd)
	claim, found := getAttachmentClaim(t, baseUOW, uploadID)
	if !found {
		t.Fatalf("setup: expected the claim to still exist")
	}
	ref := ports.ArtifactRef{Locator: claim.Locator, SHA256: claim.ActualSHA256, Size: claim.Size}

	clk.Advance(30 * time.Minute)
	report, err := appmessage.ResumeOrCleanExpiredAttachmentClaims(ctx, baseUOW, store, clk)
	if err != nil {
		t.Fatalf("ResumeOrCleanExpiredAttachmentClaims: %v", err)
	}
	if report.Released != 1 || report.Purged != 0 {
		t.Fatalf("report = %+v, want Released=1 Purged=0 (already committed, must never purge)", report)
	}
	if _, found := getAttachmentClaim(t, baseUOW, uploadID); found {
		t.Fatalf("expected the claim to be released")
	}
	if err := store.Verify(ctx, ref); err != nil {
		t.Fatalf("expected the committed artifact content to remain intact: %v", err)
	}
	msgs, _ := appmessage.ListMessages(ctx, baseUOW, workItemID)
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
}

func TestResumeOrCleanExpiredAttachmentClaims_SharedLocatorStillReferencedElsewhere_ReleasesWithoutPurging(t *testing.T) {
	baseUOW, store, ids, workItemID := setupFixture(t)
	ctx := context.Background()
	clk := clock.NewFixed(time.Now())
	content := []byte("shared bytes: one completes, the other is abandoned")
	digest := sha256Hex(content)

	cmd1 := attachmentCommand("idem-shared-done", "hash-shared-done")
	if _, err := appmessage.AppendConversationAttachment(ctx, baseUOW, store, ids, clk, cmd1,
		baseAttachmentRequest(workItemID, content, "text/plain", digest)); err != nil {
		t.Fatalf("upload 1: %v", err)
	}

	cmd2 := attachmentCommand("idem-shared-abandoned", "hash-shared-abandoned")
	crashing := &countingUOW{UnitOfWork: baseUOW, failAtWrite: 3}
	if _, err := appmessage.AppendConversationAttachment(ctx, crashing, store, ids, clk, cmd2,
		baseAttachmentRequest(workItemID, content, "text/plain", digest)); !errors.Is(err, errSimulatedCrash) {
		t.Fatalf("upload 2 setup err = %v, want errSimulatedCrash", err)
	}

	uploadID2 := appmessage.DeterministicAttachmentUploadID(cmd2)
	claim2, found := getAttachmentClaim(t, baseUOW, uploadID2)
	if !found || claim2.State != ports.AttachmentClaimBlobReady {
		t.Fatalf("setup: upload 2 claim = %+v found=%v, want BLOB_READY", claim2, found)
	}
	ref := ports.ArtifactRef{Locator: claim2.Locator, SHA256: claim2.ActualSHA256, Size: claim2.Size}

	clk.Advance(30 * time.Minute)
	report, err := appmessage.ResumeOrCleanExpiredAttachmentClaims(ctx, baseUOW, store, clk)
	if err != nil {
		t.Fatalf("ResumeOrCleanExpiredAttachmentClaims: %v", err)
	}
	if report.Released != 1 || report.Purged != 0 {
		t.Fatalf("report = %+v, want Released=1 Purged=0 (locator still referenced by upload 1's own committed Artifact row)", report)
	}
	if _, found := getAttachmentClaim(t, baseUOW, uploadID2); found {
		t.Fatalf("expected upload 2's claim to be released")
	}
	if err := store.Verify(ctx, ref); err != nil {
		t.Fatalf("expected the shared content to remain intact (upload 1 still needs it): %v", err)
	}
	msgs, _ := appmessage.ListMessages(ctx, baseUOW, workItemID)
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1 (only upload 1 ever committed)", len(msgs))
	}
}
