package fake

import (
	"context"
	"fmt"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// MessageRepository is an in-memory ports.MessageRepository — V5-02 gives
// this concern real behavior from the start (the same treatment
// AdapterBuildRepository/ArtifactRepository above already received), so an
// application-layer test of the append/list flow never needs sqlite.
// work/runtime are populated now, mirroring ArtifactRepository's own
// catalog field: AppendMessage's own "WorkItemID/AttemptID must name a row
// that exists" checks need to read WorkRepository/RuntimeRepository's own
// records, and this fake has no other way to reach a sibling repository's
// data.
type MessageRepository struct {
	work    *WorkRepository
	runtime *RuntimeRepository

	messages    map[string]message.Message // by ID
	maxSequence map[string]uint64          // by WorkItemID
}

var _ ports.MessageRepository = (*MessageRepository)(nil)

func (m *MessageRepository) cloneWith(work *WorkRepository, runtime *RuntimeRepository) *MessageRepository {
	messages := make(map[string]message.Message, len(m.messages))
	for k, v := range m.messages {
		messages[k] = v
	}
	maxSequence := make(map[string]uint64, len(m.maxSequence))
	for k, v := range m.maxSequence {
		maxSequence[k] = v
	}
	return &MessageRepository{work: work, runtime: runtime, messages: messages, maxSequence: maxSequence}
}

// AppendMessage mirrors sqlite's appendMessageTx: WorkItemID must name a
// WorkItem this fake's own WorkRepository already has, AttemptID (when
// non-empty) must name an ExecutionAttempt this fake's own
// RuntimeRepository already has, Sequence is resolved as this fake's own
// running max+1 per WorkItemID, and a duplicate ID returns the
// already-stored row rather than resolving/persisting a second Sequence.
func (m *MessageRepository) AppendMessage(_ context.Context, req ports.AppendMessageRequest) (message.Message, error) {
	if existing, ok := m.messages[req.ID]; ok {
		return existing, nil
	}
	workItem, ok := m.work.workItems[req.WorkItemID]
	if !ok {
		return message.Message{}, fmt.Errorf("fake: %w: work item %s", ports.ErrPersistenceNotFound, req.WorkItemID)
	}
	// The WorkItem's own STORED project is always what is checked against
	// req.ProjectID, never trusted from the caller — mirrors sqlite's own
	// appendMessageTx and CatalogRepository's own cross-project checks.
	if string(workItem.ProjectID) != req.ProjectID {
		return message.Message{}, fmt.Errorf("fake: %w: work item %s belongs to project %s, not %s",
			ports.ErrCrossProjectReference, req.WorkItemID, workItem.ProjectID, req.ProjectID)
	}
	var attemptID *runtime.ExecutionAttemptID
	if req.AttemptID != "" {
		if _, ok := m.runtime.attempts[req.AttemptID]; !ok {
			return message.Message{}, fmt.Errorf("fake: %w: execution attempt %s", ports.ErrPersistenceNotFound, req.AttemptID)
		}
		id := runtime.ExecutionAttemptID(req.AttemptID)
		attemptID = &id
	}

	nextSequence := m.maxSequence[req.WorkItemID] + 1
	msg, err := message.NewMessage(
		message.ID(req.ID), project.ProjectID(req.ProjectID), work.WorkItemID(req.WorkItemID), attemptID,
		nextSequence, req.Actor, req.Role, artifact.ID(req.ContentArtifactID), req.CorrelationID, req.CreatedAt,
	)
	if err != nil {
		return message.Message{}, err
	}
	if m.messages == nil {
		m.messages = map[string]message.Message{}
	}
	if m.maxSequence == nil {
		m.maxSequence = map[string]uint64{}
	}
	m.messages[req.ID] = msg
	m.maxSequence[req.WorkItemID] = nextSequence
	return msg, nil
}

func (m *MessageRepository) GetMessage(_ context.Context, id string) (message.Message, error) {
	msg, ok := m.messages[id]
	if !ok {
		return message.Message{}, fmt.Errorf("fake: %w: message %s", ports.ErrPersistenceNotFound, id)
	}
	return msg, nil
}

// ListMessagesForWorkItem mirrors sqlite's own query, ordered by Sequence
// for a deterministic result either implementation gives.
func (m *MessageRepository) ListMessagesForWorkItem(_ context.Context, workItemID string) ([]message.Message, error) {
	var result []message.Message
	for _, msg := range m.messages {
		if string(msg.WorkItemID) == workItemID {
			result = append(result, msg)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Sequence < result[j].Sequence })
	return result, nil
}
