package message

import (
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
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

// contextSnapshotDetail/contextSnapshotToDetail are now a thin alias onto
// internal/app/runtime's own exported ContextSnapshotDetail/
// ContextSnapshotToDetail (V6-07B, docs/design/08-v6-api-projections.md
// V6-07B): that package's own queries.go doc comment explains why the
// conversion was moved there — V6-07B's own broader, message-independent
// GetContextSnapshot route (internal/delivery/httpapi/evidence) needs the
// IDENTICAL reload/redaction-free reference-only shape this route already
// established, and docs/design/11-v6-00-ux-artifact.md's own Screen 12 row
// 4 says so explicitly ("authority dùng chung với Screen 11 hàng 5 cho chi
// tiết đầy đủ") — so both routes now share one conversion instead of
// maintaining two byte-for-byte-identical copies. The wire shape (JSON
// field names/omitempty) is UNCHANGED from V6-07's own original — this is
// a pure "move, don't change" refactor, verified by this package's own
// already-passing context_snapshot_test.go continuing to pass unmodified.
type contextSnapshotDetail = runtimeapp.ContextSnapshotDetail

func contextSnapshotToDetail(s contextsnapshot.Snapshot) contextSnapshotDetail {
	return runtimeapp.ContextSnapshotToDetail(s)
}
