// Package message is the durable, append-only task-chat authority
// (docs/design/07-v5-execution-evidence.md V5-02; go-core-spec §4.6's
// "ConversationMessage"; HE-02-M05, HE-05-M07): a CLI provider's own
// session transcript is never the canonical record of what was said in a
// task — this package's Message row is. "Conversation" is not a separate
// aggregate anywhere in this codebase's design (go-core-spec §4.6 sketches
// only ConversationMessage, no header row) — it is simply every Message
// sharing one WorkItemID, ordered by Sequence.
package message

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// ID is a platform-minted identity for one Message row.
type ID string

// Role is who/what produced a Message's content. There is no existing
// precedent for this enum anywhere in the docs (checked: go-core-spec §4.6
// names the field but not its values) — these four values are this
// package's own reasonable reading of "task chat."
type Role string

const (
	RoleUser      Role = "USER"
	RoleAssistant Role = "ASSISTANT"
	RoleSystem    Role = "SYSTEM"
	RoleTool      Role = "TOOL"
)

// Valid reports whether r is one of this package's own known roles.
func (r Role) Valid() bool {
	switch r {
	case RoleUser, RoleAssistant, RoleSystem, RoleTool:
		return true
	default:
		return false
	}
}

// Message is one append-only turn in a WorkItem's task chat. Content is
// never inlined here — ContentArtifactID always references a durable,
// hash-verified V5-01 Artifact row (HE-11-S06: "DB chỉ giữ metadata...
// ArtifactStore chỉ giữ content"). AttemptID is nil for a message with no
// execution context yet (e.g. a human's initial task description before
// any Run starts).
type Message struct {
	ID                ID
	ProjectID         project.ProjectID
	WorkItemID        work.WorkItemID
	AttemptID         *runtime.ExecutionAttemptID
	Sequence          uint64
	Actor             string
	Role              Role
	ContentArtifactID artifact.ID
	CorrelationID     string
	CreatedAt         time.Time
}

// NewMessage validates and constructs a Message. sequence must already be
// resolved by the caller (the persistence layer's own MAX(sequence)+1
// scoped to workItemID, computed inside the same transaction as the
// insert — see ports.MessageRepository.AppendMessage's own doc comment);
// this constructor only checks it is positive, it never allocates it.
func NewMessage(
	id ID,
	projectID project.ProjectID,
	workItemID work.WorkItemID,
	attemptID *runtime.ExecutionAttemptID,
	sequence uint64,
	actor string,
	role Role,
	contentArtifactID artifact.ID,
	correlationID string,
	createdAt time.Time,
) (Message, error) {
	if strings.TrimSpace(string(id)) == "" {
		return Message{}, errors.New("message: ID is required")
	}
	if strings.TrimSpace(string(projectID)) == "" {
		return Message{}, errors.New("message: ProjectID is required")
	}
	if strings.TrimSpace(string(workItemID)) == "" {
		return Message{}, errors.New("message: WorkItemID is required")
	}
	if attemptID != nil && strings.TrimSpace(string(*attemptID)) == "" {
		return Message{}, errors.New("message: AttemptID must not be blank when provided")
	}
	if sequence == 0 {
		return Message{}, errors.New("message: Sequence must be positive")
	}
	if strings.TrimSpace(actor) == "" {
		return Message{}, errors.New("message: Actor is required")
	}
	if !role.Valid() {
		return Message{}, fmt.Errorf("message: unknown Role %q", role)
	}
	if strings.TrimSpace(string(contentArtifactID)) == "" {
		return Message{}, errors.New("message: ContentArtifactID is required")
	}
	if createdAt.IsZero() {
		return Message{}, errors.New("message: CreatedAt is required")
	}

	var attemptIDCopy *runtime.ExecutionAttemptID
	if attemptID != nil {
		copyVal := *attemptID
		attemptIDCopy = &copyVal
	}
	return Message{
		ID: id, ProjectID: projectID, WorkItemID: workItemID, AttemptID: attemptIDCopy,
		Sequence: sequence, Actor: actor, Role: role, ContentArtifactID: contentArtifactID,
		CorrelationID: correlationID, CreatedAt: createdAt.UTC(),
	}, nil
}
