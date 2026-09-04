package runtime

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// DecisionArtifactID identifies one immutable DecisionArtifact row.
type DecisionArtifactID string

// DecisionArtifact is the durable, versioned record HE-03-M08 requires for
// every consequential decision ("quyết định hệ trọng và lý do MUST được lưu
// trong decision artifact có version, không chỉ trong transcript"):
// completion decisions, scope-waive approvals, recovery actions. Kind is
// deliberately an open, repository-validated string rather than a closed Go
// enum here — each later task that produces a new kind of decision (V4-09
// approval, V4-12A scope waive, V4-13 recovery, V5-11 completion) owns
// naming its own Kind value, the same way agent_events.Kind stays open for
// the same reason. Input/Result carry whatever fields that Kind needs;
// DecisionArtifact itself never interprets them.
//
// This table exists now (V4-01), ahead of any of those callers, because
// ADR-020/ADR-021's own recovery and completion semantics require it to
// already exist ("decision_artifacts phải tồn tại trước recovery/completion,
// không đợi V5") — it is immutable and append-only from its very first row.
type DecisionArtifact struct {
	ID            DecisionArtifactID
	ProjectID     project.ProjectID
	Kind          string
	PolicyVersion string
	Input         json.RawMessage
	Result        json.RawMessage
	CreatedAt     time.Time
}

// NewDecisionArtifact validates and builds one immutable decision record.
func NewDecisionArtifact(
	id DecisionArtifactID,
	projectID project.ProjectID,
	kind string,
	policyVersion string,
	input json.RawMessage,
	result json.RawMessage,
	createdAt time.Time,
) (DecisionArtifact, error) {
	kind = strings.TrimSpace(kind)
	policyVersion = strings.TrimSpace(policyVersion)
	if id == "" || projectID == "" {
		return DecisionArtifact{}, errors.New("decision artifact identities are required")
	}
	if kind == "" || policyVersion == "" {
		return DecisionArtifact{}, errors.New("decision artifact kind and policy version are required")
	}
	if !json.Valid(input) {
		return DecisionArtifact{}, errors.New("decision artifact input must be valid JSON")
	}
	if !json.Valid(result) {
		return DecisionArtifact{}, errors.New("decision artifact result must be valid JSON")
	}
	if createdAt.IsZero() {
		return DecisionArtifact{}, errors.New("decision artifact created timestamp is required")
	}
	return DecisionArtifact{
		ID:            id,
		ProjectID:     projectID,
		Kind:          kind,
		PolicyVersion: policyVersion,
		Input:         append(json.RawMessage(nil), input...),
		Result:        append(json.RawMessage(nil), result...),
		CreatedAt:     createdAt.UTC(),
	}, nil
}
