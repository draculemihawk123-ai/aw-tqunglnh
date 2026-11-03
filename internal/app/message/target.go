package message

import (
	"context"
	"errors"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/message"
)

// DeliveredMessageIDs returns the ids of the work item's messages some attempt's context snapshot already carries (in its prompt
// or, past the messages budget, as a reference): V12-18 (ADR-053) reads it to deliver a NextAttemptOnly message once. It walks
// every attempt of every run of the work item, so a caller asks only when such a message exists (see HasNextAttemptOnly).
func DeliveredMessageIDs(ctx context.Context, tx ports.Tx, workItemID string) (map[string]bool, error) {
	delivered := map[string]bool{}
	runs, err := tx.Runtime().ListWorkflowRunsForWorkItem(ctx, workItemID)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, string(run.ID))
		if err != nil {
			return nil, err
		}
		for _, attempt := range attempts {
			snapshot, err := tx.ContextSnapshots().GetSnapshotByAttemptID(ctx, string(attempt.ID))
			if errors.Is(err, ports.ErrPersistenceNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for _, ref := range snapshot.MessageRefs {
				delivered[ref.MessageID] = true
			}
			for _, ref := range snapshot.OmittedMessageRefs {
				delivered[ref.MessageID] = true
			}
		}
	}
	return delivered, nil
}

// HasNextAttemptOnly reports whether any of messages is a NextAttemptOnly one.
func HasNextAttemptOnly(messages []message.Message) bool {
	for _, m := range messages {
		if m.NextAttemptOnly {
			return true
		}
	}
	return false
}

// ForAttempt keeps the messages an attempt of nodeKey gets (V12-18): not display-only, aimed at nodeKey or at every node, and,
// for a NextAttemptOnly message, not yet delivered to an earlier attempt. delivered may be nil when no message is NextAttemptOnly.
func ForAttempt(messages []message.Message, nodeKey string, delivered map[string]bool) []message.Message {
	out := make([]message.Message, 0, len(messages))
	for _, m := range messages {
		if m.DisplayOnly || !m.ReachesNode(nodeKey) {
			continue
		}
		if m.NextAttemptOnly && delivered[string(m.ID)] {
			continue
		}
		out = append(out, m)
	}
	return out
}
