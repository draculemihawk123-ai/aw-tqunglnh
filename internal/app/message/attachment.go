// This file is V6-07A's own command (docs/design/08-v6-api-projections.md
// V6-07A) — AppendConversationAttachment, the binary-upload sibling of
// AppendMessage (commands.go) this package's own doc comment explicitly
// says AppendMessage does NOT solve: AppendMessage's content arrives as a
// single, already fully-buffered []byte, so composing
// internal/app/artifact.PrepareAttachment (Put+Verify, OUTSIDE any
// transaction) with ONE follow-up transaction is enough. A real file
// upload cannot assume that — its bytes are streamed, its declared digest
// must be verified against what was actually durably stored, and the
// whole operation must survive a crash at ANY point: mid-spool, mid-hash,
// after the blob is durably Put but before the claim/row/receipt ever
// commits, or after commit but before the caller's own HTTP ack arrives.
//
// # The composite canonical hash
//
// cmd.RequestHash (built by the HTTP layer via httpapi.SemanticHash,
// EXACTLY the way that function's own doc comment already anticipates:
// "an optional exact content digest ... e.g. a future attachment upload")
// is this command's own canonical/composite hash: sha256 over
// (commandType, scope, normalizedMetadataJSON, declaredSHA256,
// expectedVersion=0), NUL-separated. normalizedMetadataJSON canonicalizes
// exactly the "target" (WorkItemID/AttemptID) and "metadata"
// (Role/ContentType) and "retention/sensitivity" (Sensitivity;
// RetentionClass is always RetentionCanonicalContext for a conversation
// attachment, the identical fixed policy AppendMessage already applies to
// canonical Message content, ADR-017) components the spec names;
// declaredSHA256 is the fourth, "digest persisted bytes" component. This
// is what command receipts key their OWN conflict-detection on (a replay
// with the same Idempotency-Key but ANY different metadata/digest is
// ports.ErrReceiptConflict, exactly like AppendMessage already gets for
// free from the shared receipt mechanism) — nothing in THIS file computes
// it; it arrives already set on cmd.
//
// # Deterministic UploadID
//
// DeterministicAttachmentUploadID(cmd) derives a SEPARATE, narrower identifier: sha256
// over (Actor, Scope.Key(), IdempotencyKey, CommandType) alone — the exact
// same 4-tuple identity a command receipt is ALREADY keyed by
// (ports.ReceiptsRepository.Load's own signature), deliberately NOT
// including declaredSHA256/metadata. This is intentional, not an
// approximation of "the same identifying information the canonical hash
// uses": UploadID exists to fence the REAL-I/O portion of an upload — spool,
// hash, ArtifactStore.Put — against a genuine RETRY (same Idempotency-Key)
// arriving while, or after, a prior attempt already did that I/O. A genuine
// retry naturally resends the same Idempotency-Key (this codebase's own
// established "how a caller names its own retry" convention, prepareCreateCommand's
// own cmd.ID = commandType+"-"+idempotencyKey), so it always recomputes the
// identical UploadID and finds its own prior claim. Two INDEPENDENT callers
// that merely happen to upload byte-identical content to the identical
// target with identical metadata (different Idempotency-Key) correctly get
// TWO DIFFERENT UploadIDs — this is the Verify checklist's own
// "different-key concurrency ... including two that happen to share content
// bytes" scenario: each gets its own claim, its own Artifact row, its own
// Message row; only the underlying content-addressed blob is shared
// (ArtifactStore's own Put-level dedup, one layer below this command
// entirely). Including declaredSHA256 in UploadID would have made this
// scenario, and the "shared blob" scenario, indistinguishable from a true
// retry — exactly the collision this design avoids.
//
// This also explains the spec's own "mismatch" bullet ("same UploadID's
// claim exists, but this attempt's digest differs, is a real conflict, not
// silently overwritten"): since UploadID excludes the digest, a caller that
// resends the SAME Idempotency-Key with a DIFFERENT declared digest
// recomputes the SAME UploadID, finds the EXISTING claim (created=false),
// and this file's own claimOrResumeAttachmentUpload compares
// claim.DeclaredSHA256 against the new attempt's own declared digest BEFORE
// any real I/O — a mismatch there is ErrAttachmentUploadConflict, the
// upload-claim layer's own early, pre-I/O echo of what the LATE,
// post-Put/receipt-layer RequestHash conflict check would eventually have
// caught anyway (two concurrent requests, identical Idempotency-Key,
// different bodies, racing to be first — neither has a receipt yet, so only
// the claim layer can catch this in time).
//
// # Durable prepare claim and the two-phase sequence
//
// See internal/adapters/sqlite/migrations/0038_attachment_prepare_claims.sql
// and internal/app/ports/attachmentclaim.go for the claim row's own full
// shape/state-machine reasoning. The sequence AppendConversationAttachment
// follows:
//
//  1. Validate the request (cheap, no I/O).
//  2. Receipt precheck (uow.WithReadOnly, read-only, BEFORE any real I/O) —
//     mirrors AppendMessage's own loadOrValidateReceipt exactly. A replay
//     also opportunistically releases any now-stale claim for this
//     UploadID (a separate, explicit write step — never folded into the
//     read-only precheck itself), covering the "crash after DB commit but
//     before ack" Verify bullet on the VERY NEXT contact with this
//     UploadID, whether that is a genuine client retry or
//     ResumeOrCleanExpiredAttachmentClaims.
//  3. claimOrResumeAttachmentUpload (its own small uow.WithSerializedWrite):
//     idempotent-insert-or-find the claim; detect a digest mismatch before
//     any I/O; take over a stale SPOOLING claim's lease.
//  4. If the claim is already AttachmentClaimBlobReady: re-Verify the
//     already-durable blob (never trust a stale claim record blindly) and
//     skip straight to step 6 — the resume path.
//  5. Otherwise: appartifact.PrepareAttachment (Put+Verify, OUTSIDE any
//     transaction, bounded by io.LimitReader(Body, MaxAttachmentSize+1) —
//     the "bounded spool/hash step"), compare the ACTUAL resulting content
//     hash against the caller's OWN declared digest (tamper check), then
//     RecordAttachmentBlobReady (its own small, CAS-fenced
//     uow.WithSerializedWrite; a lost race against a concurrent identical
//     attempt is resolved by re-reading and adopting the winner's own
//     result, never treated as a hard failure).
//  6. ONE serialized-write transaction: re-check the receipt one more time
//     (closes the TOCTOU gap against a concurrent caller that finished
//     first while this one was doing the slow I/O above — the identical
//     two-phase discipline AppendMessage already uses), and if still
//     needed, atomically insert the Artifact metadata row (Attached, never
//     Orphan-then-promote: PrepareAttachment already returns an
//     Attached-shaped artifact.Artifact, exactly like AppendMessage's own
//     usage) + Message row + MessageAppended event + receipt.
//  7. ONLY once step 6 has committed: release (acknowledge) the claim, in
//     its own separate, subsequent uow.WithSerializedWrite call — a
//     best-effort step whose failure is safe and recoverable (the receipt
//     step 6 just recorded is now the durable, permanent proof of
//     completion; the claim row merely lingers until the next contact,
//     never causing a duplicate row or blocking anything else that does not
//     share its exact UploadID).
//
// No step ever puts real I/O (spool/hash/ArtifactStore.Put) inside a
// database transaction, and no raw ports.ArtifactRef.Locator is ever
// returned to a caller — AppendConversationAttachmentResult carries only
// ContentArtifactID, the identical bounded reference AppendMessageResult
// already established.
package message

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	appartifact "github.com/taQuangLing/agent-workflow/internal/app/artifact"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// MaxAttachmentSize bounds one attachment's own raw content — 25 MiB, a
// generous-but-finite ceiling distinct from (and much larger than)
// MaxContentSize's 1 MiB text-message bound, since an attachment is real
// file content, not typed chat text. This is a ROUTE-local bound, enforced
// here via io.LimitReader regardless of transport: internal/delivery/httpapi/message's
// own attachment route additionally wraps the request body in its own
// http.MaxBytesReader at this same size, but the shared server-wide
// httpapi.Server Config.MaxBodyBytes ceiling (cmd/aw/serve.go's own
// --max-body-bytes flag, defaulting to 1 MiB) is checked FIRST, by
// middleware, before any handler ever runs — exactly like
// internal/delivery/httpapi/message/envelope.go's own maxBodyBytes constant
// already documents for AppendMessage. An operator who wants attachments
// anywhere near this 25 MiB ceiling to actually work must raise
// --max-body-bytes accordingly; this is a deliberate operational choice,
// not a gap this task silently leaves unexplained.
const MaxAttachmentSize = 25 << 20

// AttachmentCommandType is the one, fixed ports.Command.Type value every
// real AppendConversationAttachment caller uses — exported so
// internal/delivery/httpapi/message's own prepareAttachmentCommand builds
// cmd.Type from this SAME constant rather than a second, independently
// typed literal that could silently drift from it. Callers set cmd.Type
// themselves (mirroring how AppendMessage never validates cmd.Type either),
// but ResumeOrCleanExpiredAttachmentClaims below needs SOME fixed
// commandType string to pass to tx.Receipts().Load, since
// attachment_prepare_claims itself stores no CommandType column of its own
// (every row this table will ever hold belongs to this one command, so a
// separate stored column would be redundant with this constant).
const AttachmentCommandType = "AppendConversationAttachment"

// attachmentClaimLease is how long a SPOOLING claim's own ClaimedAt is
// trusted as "an attempt is still plausibly in flight" before a fresh
// attempt (a retry, or ResumeOrCleanExpiredAttachmentClaims) is allowed to
// take over its lease via TakeOverAttachmentClaim. Deliberately much
// shorter than artifactsweep's own 7-day orphanGrace (V5-14): that grace
// answers "how long before OWNED, CONFIRMED content is abandoned"; this one
// answers "how long before a single synchronous HTTP request's own claim
// owner is presumed dead", a fundamentally faster question.
const attachmentClaimLease = 2 * time.Minute

// attachmentStaleClaimGrace is ResumeOrCleanExpiredAttachmentClaims' own
// candidate-selection cutoff — deliberately longer than attachmentClaimLease
// itself (a claim only becomes a CLEANUP candidate once it is stale by a
// wide margin, not the instant a retry would already be entitled to take
// its lease over), so an in-flight retry that is ABOUT to take over a claim
// never races a concurrent sweep pass over the exact same row.
const attachmentStaleClaimGrace = 10 * attachmentClaimLease

var (
	// ErrAttachmentDigestRequired is returned when DeclaredSHA256 is empty.
	ErrAttachmentDigestRequired = errors.New("message: DeclaredSHA256 is required")
	// ErrAttachmentDigestMalformed is returned when DeclaredSHA256 is not
	// exactly 64 lowercase hex characters.
	ErrAttachmentDigestMalformed = errors.New("message: DeclaredSHA256 must be exactly 64 lowercase hex characters")
	// ErrAttachmentTooLarge is returned when the actual streamed content
	// exceeds MaxAttachmentSize.
	ErrAttachmentTooLarge = fmt.Errorf("message: attachment content exceeds max size of %d bytes", MaxAttachmentSize)
	// ErrAttachmentDigestMismatch is returned when the actual, durably
	// verified content hash does not match the caller's own DeclaredSHA256
	// — a tamper/corruption signal, never silently resolved. The bytes are
	// already durably stored at THEIR OWN real content address by the time
	// this is detected (ArtifactStore is content-addressed and immutable —
	// PrepareAttachment's own Put+Verify already ran) but no Artifact/
	// Message/claim row is ever created or advanced to reference them: the
	// same harmless "Put succeeded, nothing ever referenced it" situation
	// that has always been possible for any ArtifactStore.Put call whose
	// caller never proceeds to insert a row for it (V1-08's own
	// content-addressed design, not a new gap this command introduces).
	ErrAttachmentDigestMismatch = errors.New("message: attachment content does not match its declared SHA-256 digest")
	// ErrAttachmentUploadConflict is returned when a claim already exists
	// for this exact UploadID (same Actor/Scope/IdempotencyKey/CommandType)
	// but with a DIFFERENT DeclaredSHA256 than this attempt's own — the
	// Idempotency-Key was reused for a semantically different upload; see
	// this file's own top-of-file doc comment for the full reasoning.
	ErrAttachmentUploadConflict = errors.New("message: idempotency key already claims a different attachment upload (declared digest differs)")
)

// AppendConversationAttachmentRequest is what a caller supplies to
// AppendConversationAttachment.
type AppendConversationAttachmentRequest struct {
	ProjectID  string
	WorkItemID string
	AttemptID  string // empty = no attempt linkage, mirrors AppendMessageRequest.AttemptID
	Role       message.Role
	// Body is the raw attachment content, streamed — never assumed to be
	// small or already buffered.
	Body io.Reader
	// ContentType is the attachment's own real media type (e.g.
	// "image/png"), never a wrapper/container type.
	ContentType string
	Sensitivity redact.Sensitivity
	// DeclaredSHA256 is the caller-declared SHA-256 of the raw content
	// bytes, lowercase hex, no "sha256:" prefix — required. The actual,
	// durably verified content hash MUST match this before anything is
	// ever attached (ErrAttachmentDigestMismatch otherwise).
	DeclaredSHA256 string
}

// AppendConversationAttachment durably attaches req.Body as a NEW Message
// (an attachment IS a Message whose content artifact happens to be binary
// — see internal/domain/message.Message's own single ContentArtifactID
// field: there is no separate attachment-linkage table in this codebase's
// domain model, so re-using the exact same append-only Message row
// AppendMessage already writes through, rather than inventing a second
// concept, is this task's own considered scope decision) with exact replay
// semantics and a durable owner at every crash point — see this file's own
// top-of-file doc comment for the full two-phase sequence.
func AppendConversationAttachment(
	ctx context.Context,
	uow ports.UnitOfWork,
	store ports.ArtifactStore,
	ids idsource.Source,
	clk clock.Clock,
	cmd ports.Command,
	req AppendConversationAttachmentRequest,
) (AppendMessageResult, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return AppendMessageResult{}, errors.New("message: ProjectID is required")
	}
	if strings.TrimSpace(req.WorkItemID) == "" {
		return AppendMessageResult{}, errors.New("message: WorkItemID is required")
	}
	if !req.Role.Valid() {
		return AppendMessageResult{}, fmt.Errorf("message: unknown Role %q", req.Role)
	}
	if req.Body == nil {
		return AppendMessageResult{}, errors.New("message: Body is required")
	}
	if strings.TrimSpace(req.ContentType) == "" {
		return AppendMessageResult{}, errors.New("message: ContentType is required")
	}
	if req.Sensitivity < redact.Public || req.Sensitivity > redact.Secret {
		return AppendMessageResult{}, fmt.Errorf("message: unknown Sensitivity %d", req.Sensitivity)
	}
	declaredSHA256 := strings.ToLower(strings.TrimSpace(req.DeclaredSHA256))
	if declaredSHA256 == "" {
		return AppendMessageResult{}, ErrAttachmentDigestRequired
	}
	if !isHexSHA256(declaredSHA256) {
		return AppendMessageResult{}, ErrAttachmentDigestMalformed
	}

	uploadID := DeterministicAttachmentUploadID(cmd)

	// Receipt precheck BEFORE any real I/O — mirrors AppendMessage's own
	// loadOrValidateReceipt exactly (see that function's own doc comment,
	// commands.go). A replay releases the (now provably stale) claim as a
	// courtesy before returning — see this file's own top-of-file doc
	// comment, step 2.
	if preResult, replayed, err := loadOrValidateReceipt(ctx, uow, cmd); err != nil {
		return AppendMessageResult{}, err
	} else if replayed {
		releaseAttachmentClaimBestEffort(ctx, uow, uploadID)
		return preResult, nil
	}

	now := clk.Now()
	claim, err := claimOrResumeAttachmentUpload(ctx, uow, attachmentClaimTarget{
		UploadID: uploadID, ProjectID: req.ProjectID, WorkItemID: req.WorkItemID, AttemptID: req.AttemptID,
		Actor: cmd.Actor, IdempotencyKey: cmd.IdempotencyKey, Role: string(req.Role), ContentType: req.ContentType,
		Sensitivity: sensitivityToWire(req.Sensitivity), RetentionClass: string(artifact.RetentionCanonicalContext),
		DeclaredSHA256: declaredSHA256, ClaimOwner: ids.NewID(), Now: now,
	})
	if err != nil {
		return AppendMessageResult{}, err
	}

	var prepared artifact.Artifact
	if claim.State == ports.AttachmentClaimBlobReady {
		// Resume: the blob is already durably Put+Verified by a prior
		// attempt for this EXACT UploadID+DeclaredSHA256 — never re-spool/
		// re-hash/re-Put. Still re-Verify (never trust a stale claim record
		// blindly): a Verify failure here means the content-addressed store
		// itself lost or corrupted the bytes since, a real integrity
		// problem this command surfaces rather than silently working around.
		ref := ports.ArtifactRef{
			Locator: claim.Locator, SHA256: claim.ActualSHA256, Size: claim.Size,
			ContentType: req.ContentType, Sensitivity: req.Sensitivity, Redacted: false,
		}
		if err := store.Verify(ctx, ref); err != nil {
			return AppendMessageResult{}, fmt.Errorf("message: verify resumed attachment content: %w", err)
		}
		prepared, err = artifact.NewArtifact(
			artifact.ID(ids.NewID()), project.ProjectID(req.ProjectID), ref.Locator, ref.SHA256, ref.Size, req.ContentType,
			req.Sensitivity, false, artifact.RetentionCanonicalContext, artifact.Attached, false,
			artifact.ComputeExpiresAt(artifact.RetentionCanonicalContext, now), now, 1,
		)
		if err != nil {
			return AppendMessageResult{}, err
		}
	} else {
		// Bounded spool/hash step: Put+Verify (OUTSIDE any transaction,
		// exactly like PrepareAttachment's own doc comment requires) against
		// a size-limited reader so an oversize upload can never be streamed
		// unboundedly even before this route's own transport-level limits
		// apply.
		limited := io.LimitReader(req.Body, MaxAttachmentSize+1)
		prepared, err = appartifact.PrepareAttachment(ctx, store, ids, clk, appartifact.PrepareAttachmentRequest{
			ProjectID: req.ProjectID, Body: limited, ContentType: req.ContentType, Sensitivity: req.Sensitivity,
			Redacted: false, RetentionClass: artifact.RetentionCanonicalContext,
		})
		if err != nil {
			return AppendMessageResult{}, fmt.Errorf("message: prepare attachment content: %w", err)
		}
		if prepared.Size > MaxAttachmentSize {
			return AppendMessageResult{}, ErrAttachmentTooLarge
		}
		if !strings.EqualFold(prepared.ContentHash, "sha256:"+declaredSHA256) {
			return AppendMessageResult{}, ErrAttachmentDigestMismatch
		}
		claim, err = recordAttachmentBlobReadyWithRetry(ctx, uow, claim, prepared, now)
		if err != nil {
			return AppendMessageResult{}, err
		}
	}

	var result AppendMessageResult
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if replayedResult, found, err := loadOrValidateReceiptTx(ctx, tx, cmd); err != nil {
			return err
		} else if found {
			result = replayedResult
			return nil
		}

		if _, err := tx.Artifacts().InsertArtifact(ctx, prepared); err != nil {
			return err
		}

		messageID := ids.NewID()
		m, err := tx.Messages().AppendMessage(ctx, ports.AppendMessageRequest{
			ID: messageID, ProjectID: req.ProjectID, WorkItemID: req.WorkItemID, AttemptID: req.AttemptID,
			Actor: cmd.Actor, Role: req.Role, ContentArtifactID: string(prepared.ID),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		})
		if err != nil {
			return err
		}

		eventPayload, err := json.Marshal(messageAppendedEventPayload{
			WorkItemID: req.WorkItemID, Role: string(m.Role),
			Sequence: m.Sequence, ContentArtifactID: string(m.ContentArtifactID),
		})
		if err != nil {
			return fmt.Errorf("marshal MessageAppended payload: %w", err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-appended", ProjectID: req.ProjectID,
			AggregateType: "Message", AggregateID: messageID, Sequence: 1,
			EventType: MessageAppendedEventType, SchemaVersion: MessageAppendedSchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = AppendMessageResult{
			MessageID: string(m.ID), ProjectID: req.ProjectID, WorkItemID: req.WorkItemID,
			Sequence: m.Sequence, ContentArtifactID: string(m.ContentArtifactID),
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal receipt result: %w", err)
		}
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
			CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON),
			CreatedAt: cmd.RequestedAt,
		})
	})
	if err != nil {
		return AppendMessageResult{}, err
	}

	// Only after commit: acknowledge/release the claim — see this file's
	// own top-of-file doc comment, step 7, for why a failure here is safe.
	releaseAttachmentClaimBestEffort(ctx, uow, uploadID)
	return result, nil
}

// DeterministicAttachmentUploadID derives V6-07A's own durable prepare-claim key from
// EXACTLY the 4-tuple identity a command receipt is already keyed by
// (Actor, Scope, IdempotencyKey, CommandType) — see this file's own
// top-of-file doc comment for why this, and not a hash that also folds in
// the declared digest/metadata, is the correct derivation. Mirrors this
// codebase's own established deterministic-ID convention
// (internal/app/runtime/completion_policy.go's
// deterministicCompletionDecisionID and siblings): sha256 over
// NUL-separated parts, hex-encoded, truncated to 16 bytes, human-readable
// prefix.
func DeterministicAttachmentUploadID(cmd ports.Command) string {
	sum := sha256.Sum256([]byte(
		"attachment-upload" + "\x00" + cmd.Actor + "\x00" + cmd.Scope.Key() + "\x00" + cmd.IdempotencyKey + "\x00" + cmd.Type,
	))
	return "attachment-upload-" + hex.EncodeToString(sum[:16])
}

// attachmentClaimTarget bundles claimOrResumeAttachmentUpload's own inputs.
type attachmentClaimTarget struct {
	UploadID, ProjectID, WorkItemID, AttemptID     string
	Actor, IdempotencyKey                          string
	Role, ContentType, Sensitivity, RetentionClass string
	DeclaredSHA256, ClaimOwner                     string
	Now                                            time.Time
}

// claimOrResumeAttachmentUpload is this command's own step 3 (see this
// file's own top-of-file doc comment): idempotent-insert-or-find the claim
// row for target.UploadID, inside its own small serialized-write
// transaction (never the same transaction as the later Artifact/Message/
// event/receipt insert — this one commits and is durably visible on its
// own, BEFORE any real I/O ever starts).
func claimOrResumeAttachmentUpload(ctx context.Context, uow ports.UnitOfWork, target attachmentClaimTarget) (ports.AttachmentPrepareClaim, error) {
	var result ports.AttachmentPrepareClaim
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		claim, created, err := tx.AttachmentClaims().ClaimAttachmentUpload(ctx, ports.AttachmentPrepareClaim{
			UploadID: target.UploadID, ProjectID: target.ProjectID, WorkItemID: target.WorkItemID, AttemptID: target.AttemptID,
			Actor: target.Actor, IdempotencyKey: target.IdempotencyKey, Role: target.Role, ContentType: target.ContentType,
			Sensitivity: target.Sensitivity, RetentionClass: target.RetentionClass, DeclaredSHA256: target.DeclaredSHA256,
			ClaimOwner: target.ClaimOwner, ClaimedAt: target.Now,
		})
		if err != nil {
			return err
		}
		if created {
			result = claim
			return nil
		}
		// A claim already exists for this UploadID (same Actor/Scope/
		// IdempotencyKey/CommandType). Same declared digest: this is either
		// a genuine retry resuming its own prior attempt, or a concurrent
		// duplicate of it — both safe to proceed against (see below).
		// Different declared digest: the Idempotency-Key was reused for a
		// semantically different upload — a real conflict, never silently
		// overwritten.
		if claim.DeclaredSHA256 != target.DeclaredSHA256 {
			return ErrAttachmentUploadConflict
		}
		if claim.State == ports.AttachmentClaimBlobReady {
			result = claim
			return nil
		}
		// SPOOLING: take over a STALE claim's lease so a crashed prior
		// attempt's own claim can be resumed by a fresh one; a claim that is
		// still fresh (plausibly a genuinely concurrent in-flight duplicate
		// attempt, not a crash) is left exactly as-is and simply reused —
		// both racers verified the identical DeclaredSHA256 above, so
		// proceeding redundantly is always safe, arbitrated later by
		// RecordAttachmentBlobReady's own CAS (recordAttachmentBlobReadyWithRetry).
		if target.Now.Sub(claim.ClaimedAt) > attachmentClaimLease {
			taken, err := tx.AttachmentClaims().TakeOverAttachmentClaim(ctx, ports.TakeOverAttachmentClaimRequest{
				UploadID: target.UploadID, ExpectedVersion: claim.Version,
				NewClaimOwner: target.ClaimOwner, ClaimedAt: target.Now,
			})
			if err != nil {
				return err
			}
			result = taken
			return nil
		}
		result = claim
		return nil
	})
	return result, err
}

// recordAttachmentBlobReadyWithRetry is this command's own step 5 CAS (see
// this file's own top-of-file doc comment): promote claim to
// AttachmentClaimBlobReady now that Put+Verify have both succeeded. A lost
// race against a concurrent, identical attempt (ports.ErrOptimisticConflict)
// is resolved by re-reading the claim and adopting the winner's own result,
// never treated as a hard failure — both racers verified the SAME
// DeclaredSHA256 before ever reaching this call, so the winner's own
// ActualSHA256 is expected to equal prepared.ContentHash exactly; anything
// else is a genuine internal-consistency error, surfaced rather than
// papered over.
//
// A THIRD outcome, beyond "I won" and "I lost but the claim is still here":
// the winner may have already run its ENTIRE remaining sequence — its own
// RecordAttachmentBlobReady, its own final Artifact+Message+event+receipt
// transaction (step 6), AND its own post-commit releaseAttachmentClaimBestEffort
// (step 7) — as three separate, already-committed transactions, all before
// this goroutine's own CAS attempt (or its post-conflict re-read) ever runs.
// In that case the claim row is simply GONE: either RecordAttachmentBlobReady
// itself returns ports.ErrPersistenceNotFound directly (the row vanished
// between this goroutine's own claimOrResumeAttachmentUpload read and this
// CAS attempt), or the CAS loses with ErrOptimisticConflict and the
// following re-read (GetAttachmentClaim) returns ErrPersistenceNotFound (the
// row vanished between the failed CAS and the re-read). Both are the SAME
// situation, not an error: releaseAttachmentClaimBestEffort is only ever
// called AFTER the owning transaction that writes the receipt has already
// committed (step 7's own doc comment), so "the claim is gone" always
// implies "a receipt for this exact command already exists". This function
// returns the zero AttachmentPrepareClaim with a nil error in that case —
// safe because its own caller (AppendConversationAttachment) never reads
// the claim this function returns; it only proceeds straight to the final
// WithSerializedWrite transaction, which re-checks the receipt FIRST
// (loadOrValidateReceiptTx) before touching anything else and will
// correctly replay the winner's own result instead of attempting a second
// InsertArtifact/AppendMessage. (If this reasoning ever turned out to be
// wrong for some OTHER, not-yet-existing caller of ReleaseAttachmentClaim
// that releases a claim with no receipt, the outcome would still be safe,
// merely redundant: the final transaction's own receipt PRIMARY KEY
// (actor, scope_key, idempotency_key, command_type) is command_receipts'
// own ultimate, always-enforced idempotency backstop — the claim is only
// ever a fencing optimization for the real-I/O phase, never itself the
// source of truth for "has this command already completed".)
func recordAttachmentBlobReadyWithRetry(ctx context.Context, uow ports.UnitOfWork, claim ports.AttachmentPrepareClaim, prepared artifact.Artifact, now time.Time) (ports.AttachmentPrepareClaim, error) {
	var result ports.AttachmentPrepareClaim
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		updated, err := tx.AttachmentClaims().RecordAttachmentBlobReady(ctx, ports.RecordAttachmentBlobReadyRequest{
			UploadID: claim.UploadID, ExpectedVersion: claim.Version,
			Locator: prepared.Locator, ActualSHA256: prepared.ContentHash, Size: prepared.Size, UpdatedAt: now,
		})
		if err == nil {
			result = updated
			return nil
		}
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			// The claim is already gone — the winner finished its entire
			// sequence, receipt included, before this CAS ever ran. See
			// this function's own doc comment above.
			return nil
		}
		if !errors.Is(err, ports.ErrOptimisticConflict) {
			return err
		}
		fresh, getErr := tx.AttachmentClaims().GetAttachmentClaim(ctx, claim.UploadID)
		if errors.Is(getErr, ports.ErrPersistenceNotFound) {
			// Same situation, just discovered one step later: the claim
			// was released between this goroutine's own lost CAS and this
			// re-read. See this function's own doc comment above.
			return nil
		}
		if getErr != nil {
			return getErr
		}
		if fresh.State != ports.AttachmentClaimBlobReady || fresh.ActualSHA256 != prepared.ContentHash {
			return fmt.Errorf("message: attachment claim %s resolved unexpectedly after a lost CAS race: %w", claim.UploadID, err)
		}
		result = fresh
		return nil
	})
	return result, err
}

// releaseAttachmentClaimBestEffort releases uploadID's own claim row in its
// own, separate serialized-write transaction — called only AFTER the
// owning command's own Artifact+Message+event+receipt transaction has
// already committed (or, on the replay path, after a receipt has already
// been confirmed to exist). Its error is deliberately swallowed: by the
// time this is ever called, the command has already, durably, succeeded
// (the receipt is the permanent proof) — a failure here only means the
// claim row lingers a little longer, safely resolved on the next contact
// (a further retry's own replay path above, or
// ResumeOrCleanExpiredAttachmentClaims), never a lost result and never a
// duplicate row.
func releaseAttachmentClaimBestEffort(ctx context.Context, uow ports.UnitOfWork, uploadID string) {
	_ = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return tx.AttachmentClaims().ReleaseAttachmentClaim(ctx, uploadID)
	})
}

// sensitivityToWire mirrors sqlite's own artifacts.sensitivity encoding
// (PUBLIC/SENSITIVE/SECRET) — the claim row's own Sensitivity column uses
// the identical wire vocabulary so a later read (e.g.
// ResumeOrCleanExpiredAttachmentClaims, or a human inspecting the row) never
// has to learn a second encoding for the same redact.Sensitivity value.
func sensitivityToWire(s redact.Sensitivity) string {
	switch s {
	case redact.Public:
		return "PUBLIC"
	case redact.Sensitive:
		return "SENSITIVE"
	case redact.Secret:
		return "SECRET"
	default:
		return "PUBLIC"
	}
}

// isHexSHA256 reports whether s is exactly 64 lowercase hex characters.
func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// AttachmentClaimSweepReport is ResumeOrCleanExpiredAttachmentClaims' own
// summary of one pass.
type AttachmentClaimSweepReport struct {
	// Released counts every stale claim this pass removed (committed,
	// still-referenced-elsewhere, or genuinely orphaned — every resolution
	// this function reaches ends in a release).
	Released int
	// Purged counts the subset of Released where this pass additionally
	// performed a real ports.ArtifactStore.Delete (a genuinely orphaned
	// blob no Artifact row anywhere references).
	Purged int
}

// ResumeOrCleanExpiredAttachmentClaims is V6-07A's own restart-and-orphan-
// cleanup pass (this task's own Verify checklist: "a claim abandoned by a
// crashed process gets safely resumed OR cleaned on the next relevant
// trigger, never left permanently dangling AND never double-processed").
//
// This is a real, callable, independently-tested function — NOT wired into
// a self-rescheduling durable CONTROL job the way V5-14's own artifactsweep
// package is (see that package's own StartupArtifactSweep/Handler):
// confirmed by grepping cmd/, neither StartupArtifactSweep nor
// StartupRecoveryScan (V4-13's own identical-shape sibling) is actually
// called from cmd/aw/serve.go or anywhere else in this codebase today — the
// durable-job/worker-pool composition root that would run such a
// self-rescheduling CONTROL job does not exist yet for ANY sweep in this
// codebase, V5-14's own real one included. Given that, inventing a NEW
// durable_jobs job_class (its own CHECK-constraint-rebuild migration,
// mirroring 0025/0034) for THIS task alone, only to leave it equally
// unwired, would add real schema/migration surface with no more actual
// runtime effect than what this plain function already provides. This
// mirrors the spec's own explicit allowance ("at minimum a resumable
// recovery path") — see baocaov6checklist.md's own V6-07A section for the
// full reasoning and the follow-up this scope decision implies (wiring a
// real self-rescheduling job once this codebase gains a durable-job worker
// composition root at all, for every sweep, not just this one).
//
// It never blindly deletes/abandons a claim — every resolution below
// re-checks, in this exact order: (1) the receipt (has the owning command
// actually completed since. tx.Receipts().Load by the claim's own stored
// Actor/IdempotencyKey/AttachmentCommandType — reading only, never forging
// a write on a caller's behalf); (2) for a BLOB_READY claim with no
// receipt, the Artifact's own real refs (tx.Artifacts().ListArtifactsByLocator
// — a DIFFERENT logical upload may legitimately already share this exact
// content-addressed Locator, V5-14's own "content_hash is deliberately NOT
// unique" invariant); (3) only once both come back empty does it reuse
// V5-14's own reserve/delete/finalize purge protocol
// (ClaimArtifactLocatorForPurge -> ports.ArtifactStore.Delete ->
// ReleaseArtifactLocatorClaim) to actually free the orphaned bytes — the
// "shared content hash" re-check the spec names.
func ResumeOrCleanExpiredAttachmentClaims(ctx context.Context, uow ports.UnitOfWork, store ports.ArtifactStore, clk clock.Clock) (AttachmentClaimSweepReport, error) {
	olderThan := clk.Now().Add(-attachmentStaleClaimGrace)

	var claims []ports.AttachmentPrepareClaim
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		claims, err = tx.AttachmentClaims().ListStaleAttachmentClaims(ctx, olderThan)
		return err
	}); err != nil {
		return AttachmentClaimSweepReport{}, fmt.Errorf("message: list stale attachment claims: %w", err)
	}

	var report AttachmentClaimSweepReport
	for _, claim := range claims {
		purged, err := resolveExpiredAttachmentClaim(ctx, uow, store, claim)
		if err != nil {
			return report, err
		}
		report.Released++
		if purged {
			report.Purged++
		}
	}
	return report, nil
}

// resolveExpiredAttachmentClaim resolves ONE stale claim per
// ResumeOrCleanExpiredAttachmentClaims' own doc comment above.
func resolveExpiredAttachmentClaim(ctx context.Context, uow ports.UnitOfWork, store ports.ArtifactStore, claim ports.AttachmentPrepareClaim) (purged bool, err error) {
	scope := ports.ProjectScope(claim.ProjectID)

	var receiptFound bool
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		_, found, loadErr := tx.Receipts().Load(ctx, claim.Actor, scope, claim.IdempotencyKey, AttachmentCommandType)
		receiptFound = found
		return loadErr
	}); err != nil {
		return false, fmt.Errorf("message: recheck receipt for attachment claim %s: %w", claim.UploadID, err)
	}
	if receiptFound {
		// The owning command already completed and committed — this claim
		// is exactly the "crash after DB commit but before ack" case,
		// resolved here instead of by a retry. The receipt is the
		// permanent, durable proof; releasing the claim is always safe.
		releaseAttachmentClaimBestEffort(ctx, uow, claim.UploadID)
		return false, nil
	}

	if claim.State != ports.AttachmentClaimBlobReady {
		// SPOOLING with no receipt: no Artifact/Message row was ever
		// created for this claim, and no blob was ever durably Put under
		// it either (or if one was, it is unreferenced by any row anywhere
		// — the same harmless "Put succeeded, nothing ever referenced it"
		// situation ErrAttachmentDigestMismatch's own doc comment already
		// describes) — nothing to purge, just release the row.
		releaseAttachmentClaimBestEffort(ctx, uow, claim.UploadID)
		return false, nil
	}

	var stillReferenced bool
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		rows, listErr := tx.Artifacts().ListArtifactsByLocator(ctx, claim.Locator)
		stillReferenced = len(rows) > 0
		return listErr
	}); err != nil {
		return false, fmt.Errorf("message: recheck artifact refs for attachment claim %s: %w", claim.UploadID, err)
	}
	if stillReferenced {
		// A DIFFERENT logical upload (a different UploadID/claim, already
		// completed) already owns an Artifact row at this exact
		// content-addressed Locator — the blob is legitimately alive; only
		// THIS claim's own now-pointless row is released.
		releaseAttachmentClaimBestEffort(ctx, uow, claim.UploadID)
		return false, nil
	}

	// No receipt, BLOB_READY, and genuinely no Artifact row anywhere
	// references this Locator: this Put'd blob is provably abandoned. Reuse
	// V5-14's own reserve/delete/finalize purge protocol exactly
	// (internal/app/artifactsweep.purgeLocatorGroup's identical shape),
	// scoped to this one Locator — ClaimArtifactLocatorForPurge returning
	// ErrPersistenceAlreadyExists here is treated the same way that
	// package's own doc comment treats it for its own singleton job: this
	// sweep pass processes one claim (and therefore one purge) at a time,
	// so a pre-existing claim on this exact Locator can only be this same
	// pass's own earlier, crashed attempt — safe to resume, never a
	// foreign conflict to abort on.
	ref := ports.ArtifactRef{Locator: claim.Locator, SHA256: claim.ActualSHA256, Size: claim.Size}
	claimErr := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return tx.Artifacts().ClaimArtifactLocatorForPurge(ctx, claim.Locator, "attachment-claim-sweep:"+claim.UploadID, time.Now().UTC())
	})
	if claimErr != nil && !errors.Is(claimErr, ports.ErrPersistenceAlreadyExists) {
		return false, fmt.Errorf("message: claim locator %s for purge: %w", claim.Locator, claimErr)
	}
	if err := store.Delete(ctx, ref); err != nil {
		return false, fmt.Errorf("message: delete orphaned attachment content at locator %s: %w", claim.Locator, err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return tx.Artifacts().ReleaseArtifactLocatorClaim(ctx, claim.Locator)
	}); err != nil {
		return false, fmt.Errorf("message: release locator %s purge claim: %w", claim.Locator, err)
	}
	releaseAttachmentClaimBestEffort(ctx, uow, claim.UploadID)
	return true, nil
}
