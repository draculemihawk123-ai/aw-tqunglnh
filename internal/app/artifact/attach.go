// Package artifact is V5-01's application-layer half of the "attach"
// operation docs/design/07-v5-execution-evidence.md's own V5-01 names
// (mirroring the domain package it is built on, internal/domain/artifact —
// the same "app package named after the domain package it orchestrates"
// convention internal/app/work follows for internal/domain/work).
//
// PrepareAttachment is deliberately NOT a top-level ports.Command the way
// internal/app/work.CreateRootWorkItem is: it never opens its own
// ports.UnitOfWork transaction, because every real future caller
// (Message attachments V5-02, checkpoint diff capture V5-08A, command
// output V5-09, gate evidence V5-10, ...) needs to insert the resulting
// artifact.Artifact INSIDE a larger transaction alongside whatever else
// that caller writes — and a transaction can never nest inside another
// (internal/app/ports/unitofwork.go's own UnitOfWork doc comment). This
// package instead does only the half that MUST happen outside any
// transaction (docs/architecture/04-go-core-spec.md §11.1: "Không gọi...
// filesystem artifact store... trong transaction"): call
// ports.ArtifactStore.Put, then explicitly re-Verify the result, and only
// once both succeed, construct the artifact.Artifact ready for the
// caller's own tx.Artifacts().InsertArtifact call. This ordering — durable
// AND hash-verified before any database commit — is V5-01's own Done-when
// bar ("evidence bắt buộc không commit trước artifact durable/hash
// verified"), enforced here by sequencing rather than by owning both
// halves in one function.
package artifact

import (
	"context"
	"fmt"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// PrepareAttachmentRequest is what a caller supplies to PrepareAttachment.
type PrepareAttachmentRequest struct {
	ProjectID      string
	Body           io.Reader
	ContentType    string
	Sensitivity    redact.Sensitivity
	Redacted       bool
	RetentionClass artifact.RetentionClass
}

// PrepareAttachment stores req.Body durably in store (Put) and re-verifies
// its hash (Verify) — both OUTSIDE any database transaction — then returns
// an artifact.Artifact, AttachState Attached, ready for the caller's own
// tx.Artifacts().InsertArtifact call inside whatever ports.UnitOfWork
// transaction that caller is already composing. See this package's own
// doc comment for why this function itself never opens a transaction.
func PrepareAttachment(
	ctx context.Context,
	store ports.ArtifactStore,
	ids idsource.Source,
	clk clock.Clock,
	req PrepareAttachmentRequest,
) (artifact.Artifact, error) {
	ref, err := store.Put(ctx, ports.ArtifactMetadata{
		ContentType: req.ContentType,
		Sensitivity: req.Sensitivity,
		Redacted:    req.Redacted,
	}, req.Body)
	if err != nil {
		return artifact.Artifact{}, fmt.Errorf("artifact: put content: %w", err)
	}
	if err := store.Verify(ctx, ref); err != nil {
		return artifact.Artifact{}, fmt.Errorf("artifact: verify content before attach: %w", err)
	}

	now := clk.Now()
	return artifact.NewArtifact(
		artifact.ID(ids.NewID()),
		project.ProjectID(req.ProjectID),
		ref.Locator,
		ref.SHA256,
		ref.Size,
		ref.ContentType,
		ref.Sensitivity,
		ref.Redacted,
		req.RetentionClass,
		artifact.Attached,
		false,
		artifact.ComputeExpiresAt(req.RetentionClass, now),
		now,
		1,
	)
}
