package message

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// ResolveMessageContent authorizes and resolves the real ports.ArtifactRef
// for messageID's own ContentArtifactID — the public application operation
// ADR-028 wants behind GET .../messages/{messageId}/content
// (internal/delivery/httpapi/message/content.go's own getMessageContent),
// a real, previously-missing route: no route anywhere in this codebase
// could otherwise ever read a Message's own actual text (see that handler's
// own doc comment for the full reasoning — evidence.getArtifactContent only
// ever authorizes against an Evidence row's own ArtifactReferences, which a
// chat Message can never satisfy).
//
// The caller (handleGetMessageContent) has already reloaded and
// scope-checked workItemID via workapp.GetWorkItem before ever calling this
// function — the identical "reload the route's own primary target first"
// discipline every other route in this package follows — so this function
// only needs to reload messageID itself and cross-check it against BOTH
// projectID and workItemID (never trusted from the path alone), then the
// Artifact it names, defense-in-depth re-checked against projectID too —
// mirroring internal/app/runtime.ResolveEvidenceArtifactContent's own
// identical two-step reload/cross-check shape.
func ResolveMessageContent(ctx context.Context, uow ports.UnitOfWork, projectID, workItemID, messageID string) (ports.ArtifactRef, error) {
	var ref ports.ArtifactRef
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		m, err := tx.Messages().GetMessage(ctx, messageID)
		if err != nil {
			return err
		}
		if string(m.ProjectID) != projectID || string(m.WorkItemID) != workItemID {
			return fmt.Errorf("message: %w: message %s belongs to another work item/project", ports.ErrScopeMismatch, messageID)
		}
		a, err := tx.Artifacts().GetArtifact(ctx, string(m.ContentArtifactID))
		if err != nil {
			return err
		}
		if string(a.ProjectID) != projectID {
			return fmt.Errorf("message: %w: artifact %s belongs to another project", ports.ErrScopeMismatch, a.ID)
		}
		ref = ports.ArtifactRef{
			Locator: a.Locator, SHA256: a.ContentHash, Size: a.Size,
			ContentType: a.MediaType, Sensitivity: a.Sensitivity, Redacted: a.Redacted,
		}
		return nil
	})
	return ref, err
}
