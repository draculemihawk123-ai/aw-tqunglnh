package ports

import (
	"context"
	"errors"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/message"
)

// ErrCrossWorkItemReference is returned when req.AttemptID names a real
// ExecutionAttempt, but that Attempt's own NodeRun/WorkflowRun resolves to a
// WorkItem other than req.WorkItemID (audit finding, 2026-09-08: the
// original V5-02 check only verified the Attempt ID existed at all, never
// that it actually belonged to the Message's own claimed WorkItem — a
// caller could otherwise link a Message to an Attempt from a completely
// different WorkItem, or a different Project entirely). Deliberately a
// distinct sentinel from ErrCrossProjectReference (catalog.go): a mismatch
// here can occur even within the same Project (two WorkItems, one Project),
// which ErrCrossProjectReference's own name would not describe correctly.
var ErrCrossWorkItemReference = errors.New("ports: message's own AttemptID belongs to a different WorkItem (or Project) than the message claims")

// MessageRepository is V5-02's Tx accessor for the durable, append-only
// task-chat Message row (docs/design/07-v5-execution-evidence.md V5-02;
// HE-02-M05: "dữ liệu phải sống qua attempt/session MUST thuộc
// platform-owned state hoặc artifact store, không chỉ nằm trong provider
// transcript"; HE-05-M07: "raw transcript/log MUST có redaction, size và
// retention policy"). It is deliberately its own accessor: no other Tx
// concern owns task-chat history, and a future caller composing a message
// append alongside other writes (e.g. linking it to a newly-created
// Attempt) reaches it the same way every other concern is reached, inside
// its own transaction.
type MessageRepository interface {
	// AppendMessage resolves req's next Sequence — MAX(sequence)+1 scoped
	// to req.WorkItemID, computed and used inside THIS SAME transaction,
	// the identical pattern internal/adapters/sqlite/node_dispatch.go and
	// attempt_store.go already use for domain_events.sequence — verifies
	// req.WorkItemID names a WorkItem that exists and, when req.AttemptID
	// is non-empty, that it names an ExecutionAttempt that exists, then
	// inserts the row. Idempotent by ID: a duplicate insert of the
	// identical ID returns the already-stored row (with its ORIGINALLY
	// resolved Sequence) rather than erroring or resolving a second
	// Sequence value for it — the same discipline
	// createWorkItemBlockerTx/RecordRunCancellationIntent already
	// establish elsewhere in this codebase.
	//
	// It is the caller's own responsibility to have already durably
	// attached req.ContentArtifactID (via internal/app/artifact.PrepareAttachment
	// + this same transaction's own tx.Artifacts().InsertArtifact) before
	// calling this — AppendMessage itself only verifies the FK, it never
	// touches ArtifactStore.
	AppendMessage(ctx context.Context, req AppendMessageRequest) (message.Message, error)
	// GetMessage returns the Message with the given ID, or
	// ErrPersistenceNotFound.
	GetMessage(ctx context.Context, id string) (message.Message, error)
	// ListMessagesForWorkItem returns every Message for workItemID,
	// ordered by Sequence — the full, canonical task chat for that
	// WorkItem (this task's own Done-when bar: this is what a UI/API
	// reload reads, never a provider's own session transcript).
	ListMessagesForWorkItem(ctx context.Context, workItemID string) ([]message.Message, error)
}

// AppendMessageRequest is the request for MessageRepository.AppendMessage.
// It deliberately carries no Sequence field — see AppendMessage's own doc
// comment for why that value is always resolved inside the repository,
// never supplied by a caller.
type AppendMessageRequest struct {
	ID                string
	ProjectID         string
	WorkItemID        string
	AttemptID         string // empty = no attempt linkage
	Actor             string
	Role              message.Role
	ContentArtifactID string
	CorrelationID     string
	CreatedAt         time.Time
}
