package message

import (
	"time"

	messagedomain "github.com/taQuangLing/agent-workflow/internal/domain/message"
)

// messageView is `aw message list`'s own wire shape for one
// messagedomain.Message — mirrors internal/delivery/httpapi/message/dto.go's
// own messageRefDTO field for field: a bounded canonical reference, never
// inlined content (this task's own "Không làm: no locator output" line,
// doc.go's own top comment). ContentArtifactID is a platform-minted ID, the
// exact same bounded reference HTTP already returns — never anything read
// from ports.ArtifactStore/ports.ArtifactRef.Locator, which this package
// never touches directly.
type messageView struct {
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

func newMessageView(m messagedomain.Message) messageView {
	v := messageView{
		MessageID: string(m.ID), ProjectID: string(m.ProjectID), WorkItemID: string(m.WorkItemID),
		Sequence: m.Sequence, Actor: m.Actor, Role: string(m.Role),
		ContentArtifactID: string(m.ContentArtifactID), CorrelationID: m.CorrelationID, CreatedAt: m.CreatedAt,
	}
	if m.AttemptID != nil {
		v.AttemptID = string(*m.AttemptID)
	}
	return v
}

// messageListView is `aw message list`'s own top-level response shape —
// an object wrapping "items" (rather than a bare top-level JSON array),
// mirroring internal/delivery/httpapi/message/dto.go's own
// listMessagesResponse doc comment reasoning, minus its own NextCursor: this
// leaf never paginates (list.go's own doc comment explains why).
type messageListView struct {
	Items []messageView `json:"items"`
}
