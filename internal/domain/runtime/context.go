package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

type ContextRole string

const (
	ContextRoleSystem    ContextRole = "SYSTEM"
	ContextRoleUser      ContextRole = "USER"
	ContextRoleAssistant ContextRole = "ASSISTANT"
	ContextRoleTool      ContextRole = "TOOL"
)

type ContextMessage struct {
	Role          ContextRole `json:"role"`
	Content       string      `json:"content"`
	CorrelationID string      `json:"correlationId,omitempty"`
}

type ContextResource struct {
	Kind        string `json:"kind"`
	Reference   string `json:"reference"`
	ContentHash string `json:"contentHash"`
}

type ContextSnapshot struct {
	id          ContextSnapshotID
	attemptID   ExecutionAttemptID
	messages    []ContextMessage
	resources   []ContextResource
	revisions   workspace.RevisionSet
	canonical   []byte
	contentHash string
	createdAt   time.Time
}

type ContextSnapshotInput struct {
	ID        ContextSnapshotID
	AttemptID ExecutionAttemptID
	Messages  []ContextMessage
	Resources []ContextResource
	Revisions workspace.RevisionSet
	CreatedAt time.Time
}

// NewContextSnapshot creates the only provider-independent resume payload.
// Provider sessions are intentionally excluded: every resume starts a fresh
// agent execution from this canonical platform-owned snapshot.
func NewContextSnapshot(input ContextSnapshotInput) (ContextSnapshot, error) {
	if input.ID == "" || input.AttemptID == "" || input.CreatedAt.IsZero() {
		return ContextSnapshot{}, errors.New("context snapshot id, attempt id and timestamp are required")
	}
	messages, err := normalizeMessages(input.Messages)
	if err != nil {
		return ContextSnapshot{}, err
	}
	resources, err := normalizeResources(input.Resources)
	if err != nil {
		return ContextSnapshot{}, err
	}
	revisions, err := workspace.NewRevisionSet(input.Revisions.Entries())
	if err != nil {
		return ContextSnapshot{}, err
	}
	canonical, err := json.Marshal(struct {
		Messages  []ContextMessage     `json:"messages"`
		Resources []ContextResource    `json:"resources"`
		Revisions []workspace.Revision `json:"revisions"`
	}{
		Messages:  messages,
		Resources: resources,
		Revisions: revisions.Entries(),
	})
	if err != nil {
		return ContextSnapshot{}, fmt.Errorf("encode canonical context snapshot: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return ContextSnapshot{
		id:          input.ID,
		attemptID:   input.AttemptID,
		messages:    messages,
		resources:   resources,
		revisions:   revisions,
		canonical:   canonical,
		contentHash: "sha256:" + hex.EncodeToString(digest[:]),
		createdAt:   input.CreatedAt.UTC(),
	}, nil
}

func (s ContextSnapshot) ID() ContextSnapshotID         { return s.id }
func (s ContextSnapshot) AttemptID() ExecutionAttemptID { return s.attemptID }
func (s ContextSnapshot) ContentHash() string           { return s.contentHash }
func (s ContextSnapshot) CreatedAt() time.Time          { return s.createdAt }
func (s ContextSnapshot) CanonicalContent() []byte      { return append([]byte(nil), s.canonical...) }
func (s ContextSnapshot) Messages() []ContextMessage {
	return append([]ContextMessage(nil), s.messages...)
}
func (s ContextSnapshot) Resources() []ContextResource {
	return append([]ContextResource(nil), s.resources...)
}
func (s ContextSnapshot) Revisions() workspace.RevisionSet { return s.revisions }

func normalizeMessages(messages []ContextMessage) ([]ContextMessage, error) {
	if len(messages) == 0 {
		return nil, errors.New("context snapshot must contain at least one message")
	}
	result := append([]ContextMessage(nil), messages...)
	for index := range result {
		result[index].Content = strings.TrimSpace(result[index].Content)
		result[index].CorrelationID = strings.TrimSpace(result[index].CorrelationID)
		switch result[index].Role {
		case ContextRoleSystem, ContextRoleUser, ContextRoleAssistant, ContextRoleTool:
		default:
			return nil, fmt.Errorf("context message %d has unsupported role %q", index, result[index].Role)
		}
		if result[index].Content == "" {
			return nil, fmt.Errorf("context message %d content is required", index)
		}
	}
	return result, nil
}

func normalizeResources(resources []ContextResource) ([]ContextResource, error) {
	result := append([]ContextResource(nil), resources...)
	for index := range result {
		result[index].Kind = strings.TrimSpace(result[index].Kind)
		result[index].Reference = strings.TrimSpace(result[index].Reference)
		result[index].ContentHash = strings.TrimSpace(result[index].ContentHash)
		if result[index].Kind == "" || result[index].Reference == "" || result[index].ContentHash == "" {
			return nil, fmt.Errorf("context resource %d kind, reference and content hash are required", index)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Kind != result[right].Kind {
			return result[left].Kind < result[right].Kind
		}
		return result[left].Reference < result[right].Reference
	})
	for index := 1; index < len(result); index++ {
		if result[index-1].Kind == result[index].Kind && result[index-1].Reference == result[index].Reference {
			return nil, fmt.Errorf("duplicate context resource %s/%s", result[index].Kind, result[index].Reference)
		}
	}
	return result, nil
}
