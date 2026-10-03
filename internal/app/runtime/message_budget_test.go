package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	domainmessage "github.com/taQuangLing/agent-workflow/internal/domain/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V9-07 (gap G7, docs/design/12-v9-harness-alignment.md) — the prompt of a
// rework loop stops growing once the context policy declares a `messages`
// budget. Each round of the loop appends one large failure log to the
// WorkItem's chat and schedules the next activation of the maker node, which is
// what the engine does after a rework outcome; the request is then assembled
// the way the worker assembles it.

const (
	messageBudgetRounds   = 10
	messageBudgetLogBytes = 8 << 10
	messageBudgetMaxBytes = 20 << 10
)

// failureLog is one round's large log; the round number is in it so a prompt
// can be asked whether it carries that round.
func failureLog(round int) string {
	return fmt.Sprintf("ROUND-%02d-FAILURE-LOG\n%s", round, strings.Repeat("x", messageBudgetLogBytes))
}

type budgetPrompt struct {
	Messages []struct {
		MessageID string `json:"messageId"`
		Content   string `json:"content"`
	} `json:"messages"`
	OmittedMessages []struct {
		MessageID string `json:"messageId"`
		Sequence  uint64 `json:"sequence"`
		Actor     string `json:"actor"`
		Role      string `json:"role"`
		CreatedAt string `json:"createdAt"`
		Reason    string `json:"reason"`
	} `json:"omittedMessages"`
}

// runReworkRounds drives messageBudgetRounds rounds after the fixture's own
// first activation and returns every round's assembled request and attempt id.
func runReworkRounds(t *testing.T, messageBudget *policy.MessageBudget) (uow *fake.UnitOfWork, store ports.ArtifactStore, requests []ports.AgentExecutionRequest, attemptIDs []string) {
	t.Helper()
	ctx := context.Background()
	uow, ids, store, runID, nodeRunID, attemptID := assembleRequestFixtureWithMessageBudget(t, workflow.AgentRoleMaker, validAgentProfileDocument(), fake.NewRuntimeExecutionConfigProvider(), attemptPolicyDocument(600), messageBudget)
	run, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	requests = append(requests, assembleFor(t, uow, store, runID, nodeRunID, attemptID))
	attemptIDs = append(attemptIDs, attemptID)

	for round := 1; round <= messageBudgetRounds; round++ {
		cmd := testCommand(fmt.Sprintf("idem-log-%d", round), fmt.Sprintf("hash-log-%d", round), ports.ProjectScope("project-1"), "AppendMessage")
		if _, err := message.AppendMessage(ctx, uow, store, ids, clock.System{}, cmd, message.AppendMessageRequest{
			ProjectID: "project-1", WorkItemID: string(run.WorkItemID), Role: domainmessage.RoleTool,
			Content: []byte(failureLog(round)), ContentType: "text/plain", Sensitivity: redact.Public,
		}); err != nil {
			t.Fatalf("round %d: AppendMessage: %v", round, err)
		}
		next := runtimedomain.NodeRunID(fmt.Sprintf("noderun-rework-%d", round))
		if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			nodeRun, err := runtimedomain.NewNodeRun(next, run.ID, "implement", uint64(round)+10, uint32(round), nil, fmt.Sprintf("input-hash-%d", round), "")
			if err != nil {
				return err
			}
			_, err = tx.Runtime().CreateNodeRun(ctx, nodeRun)
			return err
		}); err != nil {
			t.Fatalf("round %d: create the rework activation: %v", round, err)
		}
		result, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
			RunID: runID, NodeRunID: string(next), CorrelationID: fmt.Sprintf("corr-rework-%d", round),
		})
		if err != nil {
			t.Fatalf("round %d: ScheduleExecutableNodeRun: %v", round, err)
		}
		requests = append(requests, assembleFor(t, uow, store, runID, string(next), result.AttemptID))
		attemptIDs = append(attemptIDs, result.AttemptID)
	}
	return uow, store, requests, attemptIDs
}

func decodeBudgetPrompt(t *testing.T, req ports.AgentExecutionRequest) budgetPrompt {
	t.Helper()
	var prompt budgetPrompt
	if err := json.Unmarshal([]byte(req.Prompt), &prompt); err != nil {
		t.Fatalf("decode the prompt: %v", err)
	}
	return prompt
}

func TestReworkLoop_MessageBudget_BoundsThePromptAndKeepsTheNewestMessage(t *testing.T) {
	uow, store, requests, attemptIDs := runReworkRounds(t, &policy.MessageBudget{MaxBytes: messageBudgetMaxBytes, KeepLatest: 1})

	// The size no prompt may exceed: the message budget plus the fixed parts of
	// the document and the one skill resource, with room to spare — and far
	// below what eleven messages of 8 KiB give.
	const promptCeiling = messageBudgetMaxBytes + 4<<10
	for round, req := range requests {
		if len(req.Prompt) > promptCeiling {
			t.Fatalf("round %d: prompt is %d bytes, over the ceiling %d: the message budget does not bound it", round, len(req.Prompt), promptCeiling)
		}
		prompt := decodeBudgetPrompt(t, req)
		if round > 0 {
			latest := fmt.Sprintf("ROUND-%02d-FAILURE-LOG", round)
			if len(prompt.Messages) == 0 || !strings.HasPrefix(prompt.Messages[len(prompt.Messages)-1].Content, latest) {
				t.Fatalf("round %d: the newest message (%s) is not the last message of the prompt: %+v", round, latest, prompt.Messages)
			}
		}
		requireArtifactIsPrompt(t, store, req)
	}

	// The last round: the audit shows what was left out, and every message is
	// either in the prompt in full or a reference.
	lastRequest := requests[len(requests)-1]
	last := decodeBudgetPrompt(t, lastRequest)
	snapshot := snapshotOfAttempt(t, uow, attemptIDs[len(attemptIDs)-1])
	totalMessages := 1 + messageBudgetRounds
	if len(snapshot.MessageRefs)+len(snapshot.OmittedMessageRefs) != totalMessages {
		t.Fatalf("snapshot accounts for %d included + %d omitted messages, want %d in all", len(snapshot.MessageRefs), len(snapshot.OmittedMessageRefs), totalMessages)
	}
	if len(snapshot.OmittedMessageRefs) == 0 {
		t.Fatal("ten 8 KiB logs against a 20 KiB budget omitted nothing")
	}
	for _, omitted := range snapshot.OmittedMessageRefs {
		if omitted.Reason != contextsnapshot.OmittedMessageBudgetExceeded {
			t.Fatalf("omitted message %s has reason %q", omitted.MessageID, omitted.Reason)
		}
	}
	if len(last.OmittedMessages) != len(snapshot.OmittedMessageRefs) || len(last.Messages) != len(snapshot.MessageRefs) {
		t.Fatalf("prompt shows %d messages and %d references, snapshot has %d and %d", len(last.Messages), len(last.OmittedMessages), len(snapshot.MessageRefs), len(snapshot.OmittedMessageRefs))
	}
	for i, ref := range last.OmittedMessages {
		if ref.MessageID != snapshot.OmittedMessageRefs[i].MessageID || ref.Reason != "BUDGET_EXCEEDED" || ref.Actor == "" || ref.CreatedAt == "" || ref.Role == "" || ref.Sequence == 0 {
			t.Fatalf("omitted reference %d = %+v, want id %s with actor, role, time, sequence and the reason", i, ref, snapshot.OmittedMessageRefs[i].MessageID)
		}
	}
	for _, m := range last.Messages {
		for _, ref := range last.OmittedMessages {
			if m.MessageID == ref.MessageID {
				t.Fatalf("message %s is both in the prompt and a reference", m.MessageID)
			}
		}
	}
	// A reference carries no content.
	if strings.Contains(lastRequest.Prompt, "ROUND-01-FAILURE-LOG") {
		t.Fatal("the first round's log is still in the last prompt although the budget omitted it")
	}
}

// The control for the test above: the same rounds under a policy without a
// `messages` block keep every message, so the prompt grows with every round —
// the behavior before V9-07, which a policy that declares nothing must keep.
func TestReworkLoop_NoMessageBudget_KeepsEveryMessage(t *testing.T) {
	uow, _, requests, attemptIDs := runReworkRounds(t, nil)

	first, last := len(requests[0].Prompt), len(requests[len(requests)-1].Prompt)
	if last < first+messageBudgetRounds*messageBudgetLogBytes {
		t.Fatalf("the prompt grew from %d to %d bytes over %d rounds of %d-byte logs, want it to carry them all", first, last, messageBudgetRounds, messageBudgetLogBytes)
	}
	snapshot := snapshotOfAttempt(t, uow, attemptIDs[len(attemptIDs)-1])
	if len(snapshot.OmittedMessageRefs) != 0 || len(snapshot.MessageRefs) != 1+messageBudgetRounds {
		t.Fatalf("snapshot = %d included, %d omitted, want all %d included", len(snapshot.MessageRefs), len(snapshot.OmittedMessageRefs), 1+messageBudgetRounds)
	}
	if strings.Contains(requests[len(requests)-1].Prompt, `"omittedMessages"`) {
		t.Fatal("a policy without a messages block rendered an omittedMessages section")
	}
}
