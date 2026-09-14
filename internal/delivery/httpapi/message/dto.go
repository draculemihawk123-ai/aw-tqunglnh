package message

import (
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	messagedomain "github.com/taQuangLing/agent-workflow/internal/domain/message"
)

// --- Sensitivity wire vocabulary ---

// sensitivityWire is this package's own wire vocabulary for
// redact.Sensitivity: a caller declares a message's own structural
// sensitivity by name, never by redact.Sensitivity's internal int
// encoding, which is an internal/app/redact implementation detail, not a
// stable wire contract.
type sensitivityWire string

const (
	sensitivityPublic    sensitivityWire = "PUBLIC"
	sensitivitySensitive sensitivityWire = "SENSITIVE"
	sensitivitySecret    sensitivityWire = "SECRET"
)

// parseSensitivity maps a wire sensitivityWire value to its
// redact.Sensitivity. An empty value defaults to PUBLIC (redact.Public's
// own zero value) — the same "zero value is safe" default
// appmessage.AppendMessageRequest.Matcher already follows (see
// dependencies.go's own Matcher doc comment).
func parseSensitivity(raw string) (redact.Sensitivity, error) {
	switch sensitivityWire(raw) {
	case "", sensitivityPublic:
		return redact.Public, nil
	case sensitivitySensitive:
		return redact.Sensitive, nil
	case sensitivitySecret:
		return redact.Secret, nil
	default:
		return 0, fmt.Errorf("unknown sensitivity %q (want PUBLIC, SENSITIVE or SECRET)", raw)
	}
}

// --- Message reference (append/list) ---

// messageRefDTO is one Message's own bounded canonical reference — never
// inlined content (this task's own "Không làm: KHÔNG raw provider
// transcript" plus HE-11-S06's "DB chỉ giữ metadata... ArtifactStore chỉ
// giữ content"): a caller that needs the actual bytes fetches them by
// ContentArtifactID through a future V6-07B GetArtifactContent route, never
// from this response.
type messageRefDTO struct {
	MessageID         string    `json:"messageId"`
	ProjectID         string    `json:"projectId"`
	WorkItemID        string    `json:"workItemId"`
	AttemptID         string    `json:"attemptId,omitempty"`
	Sequence          uint64    `json:"sequence"`
	Actor             string    `json:"actor"`
	Role              string    `json:"role"`
	ContentArtifactID string    `json:"contentArtifactId"`
	CorrelationID     string    `json:"correlationId,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
}

func messageToRefDTO(m messagedomain.Message) messageRefDTO {
	dto := messageRefDTO{
		MessageID: string(m.ID), ProjectID: string(m.ProjectID), WorkItemID: string(m.WorkItemID),
		Sequence: m.Sequence, Actor: m.Actor, Role: string(m.Role),
		ContentArtifactID: string(m.ContentArtifactID), CorrelationID: m.CorrelationID, CreatedAt: m.CreatedAt,
	}
	if m.AttemptID != nil {
		dto.AttemptID = string(*m.AttemptID)
	}
	return dto
}

// listMessagesResponse wraps a messageRefDTO page in an object (rather
// than a bare top-level JSON array) so NextCursor can sit beside "items"
// without a breaking wire-shape change — mirrors
// internal/delivery/httpapi/workitem's own workItemListResponse doc
// comment reasoning, extended here with the real opaque cursor that
// package's own lists deliberately deferred (this task's own "Phạm vi"
// line asks for pagination; V6-04's did not).
type listMessagesResponse struct {
	Items      []messageRefDTO `json:"items"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

// listQueryFingerprint is the fixed shape httpapi.Fingerprint hashes for
// this route's own cursor.CursorState.QueryFingerprint. Unlike a projected
// list (V6-08 onward, which accepts a real filter/sort vocabulary), this
// route accepts no filter/sort parameter — WorkItemID alone is "the query"
// a cursor can ever be resumed against.
type listQueryFingerprint struct {
	WorkItemID string `json:"workItemId"`
}

// --- ContextSnapshot bounded detail ---

// messageRefView/resourceRefView/evidenceRefView/revisionView mirror
// contextsnapshot.MessageRef/ResourceRef/EvidenceRef/workspace.Revision's
// own fields exactly, with json tags (the domain types carry none) — every
// field here is itself a reference (an ID or a content hash), never
// inlined resource/message content, the identical "reference, not content"
// discipline messageRefDTO above already follows.
type messageRefView struct {
	MessageID string `json:"messageId"`
}

type resourceRefView struct {
	OwnerVersionID string `json:"ownerVersionId,omitempty"`
	ResourceKey    string `json:"resourceKey"`
	ContentHash    string `json:"contentHash"`
}

type evidenceRefView struct {
	EvidenceID string `json:"evidenceId"`
}

type revisionView struct {
	RepositoryID        string `json:"repositoryId"`
	VCSObjectID         string `json:"vcsObjectId"`
	WorkspaceGeneration uint64 `json:"workspaceGeneration"`
}

// contextSnapshotDetail is the bounded, read-only view this package's own
// getMessageContextSnapshot route exposes for a message's own bound V5-04
// ContextSnapshot (internal/app/ports/contextsnapshot.go) — deliberately
// NOT the legacy internal/domain/runtime.ContextSnapshot (see that port's
// own doc comment for why the two are kept apart).
type contextSnapshotDetail struct {
	SnapshotID      string            `json:"snapshotId"`
	ProjectID       string            `json:"projectId"`
	WorkItemID      string            `json:"workItemId"`
	AttemptID       string            `json:"attemptId"`
	MessageRefs     []messageRefView  `json:"messageRefs,omitempty"`
	ResourceRefs    []resourceRefView `json:"resourceRefs,omitempty"`
	EvidenceRefs    []evidenceRefView `json:"evidenceRefs,omitempty"`
	Revisions       []revisionView    `json:"revisions,omitempty"`
	RevisionSetHash string            `json:"revisionSetHash"`
	ManifestHash    string            `json:"manifestHash"`
	CreatedAt       time.Time         `json:"createdAt"`
}

func contextSnapshotToDetail(s contextsnapshot.Snapshot) contextSnapshotDetail {
	detail := contextSnapshotDetail{
		SnapshotID: string(s.ID), ProjectID: string(s.ProjectID), WorkItemID: string(s.WorkItemID),
		AttemptID: string(s.AttemptID), ManifestHash: s.ManifestHash, RevisionSetHash: s.Revisions.ContentHash(),
		CreatedAt: s.CreatedAt,
	}
	for _, ref := range s.MessageRefs {
		detail.MessageRefs = append(detail.MessageRefs, messageRefView{MessageID: ref.MessageID})
	}
	for _, ref := range s.ResourceRefs {
		detail.ResourceRefs = append(detail.ResourceRefs, resourceRefView{
			OwnerVersionID: ref.OwnerVersionID, ResourceKey: ref.ResourceKey, ContentHash: ref.ContentHash,
		})
	}
	for _, ref := range s.EvidenceRefs {
		detail.EvidenceRefs = append(detail.EvidenceRefs, evidenceRefView{EvidenceID: ref.EvidenceID})
	}
	for _, rev := range s.Revisions.Entries() {
		detail.Revisions = append(detail.Revisions, revisionView{
			RepositoryID: string(rev.RepositoryID), VCSObjectID: rev.VCSObjectID, WorkspaceGeneration: rev.WorkspaceGeneration,
		})
	}
	return detail
}
