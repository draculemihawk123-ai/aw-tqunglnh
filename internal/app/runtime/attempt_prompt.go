package runtime

// V12-26 (docs/design/15-v12-ui-operations.md, UX-12, ADR-059): the prompt an AGENT attempt received, for the node panel's Prompt tab.
// The instruction is rendered deterministically from the attempt's pinned context snapshot (the same renderAttemptInstruction the
// scheduler-side assembly uses), so it is read without storing anything; known secrets are redacted and the text is bounded. Who may
// read it: whoever may read the work item's evidence (V11-11, decision 3) — a project-scoped read.

import (
	"context"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
)

// MaxPromptBytes bounds the prompt a read returns.
const MaxPromptBytes = 512 << 10

// ErrAttemptHasNoPrompt: the attempt is not an AGENT attempt with a context snapshot (a COMMAND or gate attempt has no prompt).
var ErrAttemptHasNoPrompt = errors.New("runtime: this attempt has no prompt (it is not an agent attempt with a context snapshot)")

// AttemptPrompt is the instruction one attempt received.
type AttemptPrompt struct {
	AttemptID         string `json:"attemptId"`
	NodeRunID         string `json:"nodeRunId"`
	RunID             string `json:"runId"`
	NodeKey           string `json:"nodeKey"`
	ContextSnapshotID string `json:"contextSnapshotId"`
	// Prompt is the JSON document written to the provider's stdin, known secrets redacted.
	Prompt    string `json:"prompt"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated,omitempty"`
}

// ReadAttemptPrompt renders the prompt of attemptID in projectID.
func ReadAttemptPrompt(ctx context.Context, uow ports.UnitOfWork, store ports.ArtifactStore, matcher redact.Matcher, projectID, attemptID string) (AttemptPrompt, error) {
	out := AttemptPrompt{AttemptID: attemptID}
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		nodeRun, err := tx.Runtime().GetNodeRun(ctx, string(attempt.NodeRunID))
		if err != nil {
			return err
		}
		run, err := tx.Runtime().GetWorkflowRun(ctx, string(nodeRun.RunID))
		if err != nil {
			return err
		}
		if string(run.ProjectID) != projectID {
			return fmt.Errorf("%w: attempt %s", ports.ErrPersistenceNotFound, attemptID)
		}
		if attempt.ProviderKey == "" || attempt.ContextSnapshotID == nil {
			return ErrAttemptHasNoPrompt
		}
		out.NodeRunID, out.RunID, out.NodeKey, out.ContextSnapshotID = string(nodeRun.ID), string(run.ID), nodeRun.NodeKey, string(*attempt.ContextSnapshotID)
		return nil
	}); err != nil {
		return AttemptPrompt{}, err
	}
	_, content, err := renderAttemptInstruction(ctx, uow, store, AssembleAgentExecutionRequestRequest{RunID: out.RunID, NodeRunID: out.NodeRunID, AttemptID: attemptID})
	if err != nil {
		return AttemptPrompt{}, err
	}
	redacted, _ := matcher.Redact(content)
	out.Bytes = len(redacted)
	if len(redacted) > MaxPromptBytes {
		redacted, out.Truncated = redacted[:MaxPromptBytes], true
	}
	out.Prompt = string(redacted)
	return out, nil
}
