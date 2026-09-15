package ports

import (
	"context"
	"time"
)

// AttachmentPrepareClaimState is the closed two-state machine an
// AttachmentPrepareClaim moves through (V6-07A,
// docs/design/08-v6-api-projections.md V6-07A) — see
// internal/adapters/sqlite/migrations/0038_attachment_prepare_claims.sql's
// own doc comment for the full reasoning. There is no terminal "DONE"
// state: a completed claim is deleted (ReleaseAttachmentClaim), not
// transitioned into a third state.
type AttachmentPrepareClaimState string

const (
	// AttachmentClaimSpooling is a claim's own starting state: the logical
	// upload (target+metadata+declared digest) is durably claimed, but
	// ports.ArtifactStore.Put/Verify has not yet succeeded for it.
	AttachmentClaimSpooling AttachmentPrepareClaimState = "SPOOLING"
	// AttachmentClaimBlobReady is set once Put+Verify have succeeded for
	// this claim's own DeclaredSHA256 — Locator/ActualSHA256/Size are
	// populated, and a resumed retry can skip straight to the final
	// serialized-write transaction without re-spooling/re-hashing/re-Put.
	AttachmentClaimBlobReady AttachmentPrepareClaimState = "BLOB_READY"
)

// AttachmentPrepareClaim is one durable prepare-claim row (V6-07A) — see
// migration 0038's own doc comment for the full field-by-field reasoning.
type AttachmentPrepareClaim struct {
	UploadID       string
	ProjectID      string
	WorkItemID     string
	AttemptID      string // empty = no attempt linkage, mirrors message.AppendMessageRequest.AttemptID
	Actor          string
	IdempotencyKey string
	Role           string
	ContentType    string
	Sensitivity    string // wire-form redact.Sensitivity name (PUBLIC/SENSITIVE/SECRET), matching artifacts.sensitivity's own DB encoding
	RetentionClass string
	DeclaredSHA256 string
	State          AttachmentPrepareClaimState
	// Locator/ActualSHA256/Size are populated together, exactly when State
	// is AttachmentClaimBlobReady (never independently) — see migration
	// 0038's own CHECK constraint enforcing this pairing at the storage
	// layer too.
	Locator      string
	ActualSHA256 string
	Size         int64
	ClaimOwner   string
	ClaimedAt    time.Time
	UpdatedAt    time.Time
	Version      uint64
}

// RecordAttachmentBlobReadyRequest is the CAS request for
// AttachmentClaimRepository.RecordAttachmentBlobReady.
type RecordAttachmentBlobReadyRequest struct {
	UploadID        string
	ExpectedVersion uint64
	Locator         string
	ActualSHA256    string
	Size            int64
	UpdatedAt       time.Time
}

// TakeOverAttachmentClaimRequest is the CAS request for
// AttachmentClaimRepository.TakeOverAttachmentClaim.
type TakeOverAttachmentClaimRequest struct {
	UploadID        string
	ExpectedVersion uint64
	NewClaimOwner   string
	ClaimedAt       time.Time
}

// AttachmentClaimRepository is V6-07A's Tx accessor for the durable
// attachment prepare-claim row (docs/design/08-v6-api-projections.md
// V6-07A) — the upload-side sibling of ArtifactRepository's own
// ClaimArtifactLocatorForPurge/ReleaseArtifactLocatorClaim (V5-14), see
// this task's own migration 0038 for the full architectural parallel.
type AttachmentClaimRepository interface {
	// ClaimAttachmentUpload atomically inserts a new claim row (State
	// AttachmentClaimSpooling) if claim.UploadID has no existing row —
	// returns (claim, created=true, nil). If a row already exists for
	// claim.UploadID, this is NEVER an error (mirroring InsertArtifact's
	// own "duplicate insert of the identical ID returns the already-stored
	// row" discipline): it returns (existingRow, created=false, nil) so the
	// caller can inspect the existing row's own DeclaredSHA256/State/
	// ClaimedAt and decide resume vs conflict vs take-over for itself —
	// this method never makes that judgment call on its own.
	ClaimAttachmentUpload(ctx context.Context, claim AttachmentPrepareClaim) (result AttachmentPrepareClaim, created bool, err error)
	// GetAttachmentClaim returns the claim for uploadID, or
	// ErrPersistenceNotFound.
	GetAttachmentClaim(ctx context.Context, uploadID string) (AttachmentPrepareClaim, error)
	// RecordAttachmentBlobReady is the fenced CAS that promotes a claim
	// AttachmentClaimSpooling -> AttachmentClaimBlobReady once
	// ports.ArtifactStore.Put+Verify have both succeeded (OUTSIDE any
	// transaction, exactly like internal/app/artifact.PrepareAttachment's
	// own Put+Verify sequence) — see migration 0038's own CHECK constraint
	// for why Locator/ActualSHA256/Size are always set together with the
	// state. ExpectedVersion mismatch (including a row that is no longer
	// AttachmentClaimSpooling) is ErrOptimisticConflict — a caller that
	// loses this race re-reads (GetAttachmentClaim) and adopts the winner's
	// now-current BLOB_READY state instead of treating the loss as a hard
	// failure, since both racers verified the identical DeclaredSHA256
	// before ever reaching this call. ErrPersistenceNotFound for an
	// unknown UploadID.
	RecordAttachmentBlobReady(ctx context.Context, req RecordAttachmentBlobReadyRequest) (AttachmentPrepareClaim, error)
	// TakeOverAttachmentClaim is the fenced CAS that lets a fresh attempt
	// adopt ownership of an existing, STALE claim (claimed_at old enough
	// that its prior owner is presumed dead) without disturbing its own
	// State/Locator/ActualSHA256/Size — only ClaimOwner/ClaimedAt/Version
	// change. A caller decides staleness for itself (by comparing the
	// claim's own ClaimedAt against a grace duration) before ever calling
	// this; this method itself performs no staleness check, only the CAS.
	// ExpectedVersion mismatch is ErrOptimisticConflict (someone else
	// already took over, or already released/advanced it).
	// ErrPersistenceNotFound for an unknown UploadID.
	TakeOverAttachmentClaim(ctx context.Context, req TakeOverAttachmentClaimRequest) (AttachmentPrepareClaim, error)
	// ReleaseAttachmentClaim removes uploadID's own claim row — called once
	// the owning AppendConversationAttachment call's own serialized-write
	// transaction has committed (Artifact metadata + Message ref + event +
	// receipt, atomically), or once ResumeOrCleanExpiredAttachmentClaims has
	// confirmed (via the receipt, or via ListArtifactsByLocator, or via a
	// real purge) that this claim no longer needs to exist. Idempotent: a
	// UploadID with no open claim is a no-op, never an error — the same
	// "already resolved" discipline ReleaseArtifactLocatorClaim already
	// establishes.
	ReleaseAttachmentClaim(ctx context.Context, uploadID string) error
	// ListStaleAttachmentClaims returns every claim whose ClaimedAt is
	// <= olderThan, oldest-ClaimedAt-first — the candidate set
	// ResumeOrCleanExpiredAttachmentClaims resolves (see that function's own
	// doc comment, internal/app/message/attachment.go).
	ListStaleAttachmentClaims(ctx context.Context, olderThan time.Time) ([]AttachmentPrepareClaim, error)
}
