package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/message"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// ActivityPoster (V11-11, ADR-042) writes the engine's milestones into a work item's task chat: a node started, what the agent said last,
// how the node ended (with the plain-words reason when it failed), an approval waiting for a decision. They are display-only messages
// (message.Message.DisplayOnly): the context scheduler leaves them out of every prompt, so enabling this changes nothing an agent sees.
// The per-tool stream is NOT poured into the chat (too long); the chat message names the node and the Graph tab shows the stream.
//
// Every method is best effort and idempotent by a key derived from the attempt or node run: an error here is swallowed, because a missing
// chat line must never fail or delay a node. Writing happens after the engine's own transaction, never inside one.
type ActivityPoster struct {
	uow     ports.UnitOfWork
	store   ports.ArtifactStore
	ids     idsource.Source
	clk     clock.Clock
	matcher redact.Matcher
}

// NewActivityPoster returns a poster over the given collaborators. A nil poster is valid wherever one is accepted and posts nothing.
func NewActivityPoster(uow ports.UnitOfWork, store ports.ArtifactStore, ids idsource.Source, clk clock.Clock, matcher redact.Matcher) *ActivityPoster {
	return &ActivityPoster{uow: uow, store: store, ids: ids, clk: clk, matcher: matcher}
}

func (p *ActivityPoster) post(ctx context.Context, req appmessage.ActivityRequest) {
	req.Matcher = p.matcher
	_, _ = appmessage.AppendActivity(ctx, p.uow, p.store, p.ids, p.clk, req)
}

type nodeIdentity struct {
	projectID, workItemID, nodeKey, nodeType string
	iteration                                 uint32
	attemptNumber                             uint32
	attemptState                              runtimedomain.ExecutionAttemptState
	profileKey                                string
}

// identify loads who and what a node run is, for the messages about it.
func (p *ActivityPoster) identify(ctx context.Context, nodeRunID, attemptID string) (nodeIdentity, bool) {
	var id nodeIdentity
	err := p.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		nodeRun, err := tx.Runtime().GetNodeRun(ctx, nodeRunID)
		if err != nil {
			return err
		}
		run, err := tx.Runtime().GetWorkflowRun(ctx, string(nodeRun.RunID))
		if err != nil {
			return err
		}
		version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID))
		if err != nil {
			return err
		}
		id = nodeIdentity{projectID: string(run.ProjectID), workItemID: string(run.WorkItemID), nodeKey: nodeRun.NodeKey, iteration: nodeRun.Iteration}
		for _, node := range version.Document().Nodes {
			if node.Key == nodeRun.NodeKey {
				id.nodeType = string(node.Type)
			}
		}
		if attemptID != "" {
			attempt, err := tx.Runtime().GetExecutionAttempt(ctx, attemptID)
			if err != nil {
				return err
			}
			id.attemptNumber, id.attemptState, id.profileKey = attempt.AttemptNumber, attempt.State, attempt.ProviderKey
		}
		return nil
	})
	return id, err == nil
}

func (p *ActivityPoster) actor(id nodeIdentity) string {
	if id.nodeType == string(workflow.NodeAgent) && id.profileKey != "" {
		return id.profileKey
	}
	return "aw"
}

// NodeStarted posts "<node> started" when an attempt begins to run.
func (p *ActivityPoster) NodeStarted(ctx context.Context, payload ExecuteNodeJobPayload) {
	if p == nil {
		return
	}
	id, ok := p.identify(ctx, payload.NodeRunID, payload.AttemptID)
	if !ok {
		return
	}
	text := fmt.Sprintf("%s started (iteration %d, attempt %d).", id.nodeKey, id.iteration, id.attemptNumber)
	p.post(ctx, appmessage.ActivityRequest{
		ProjectID: id.projectID, WorkItemID: id.workItemID, NodeRunID: payload.NodeRunID, AttemptID: payload.AttemptID,
		Key: "start:" + payload.AttemptID, Role: message.RoleSystem, Actor: "aw", Text: text,
	})
}

// NodeFinished posts how an attempt ended: for an agent its last words (the engine keeps them even when the attempt failed), then one
// line with the outcome, the files changed and the cost, or the reason it failed and what to do. It posts nothing for an attempt that is
// not in a terminal state (an execution left for crash recovery).
func (p *ActivityPoster) NodeFinished(ctx context.Context, payload ExecuteNodeJobPayload) {
	if p == nil {
		return
	}
	id, ok := p.identify(ctx, payload.NodeRunID, payload.AttemptID)
	if !ok || !attemptTerminal(id.attemptState) {
		return
	}
	base := appmessage.ActivityRequest{ProjectID: id.projectID, WorkItemID: id.workItemID, NodeRunID: payload.NodeRunID, AttemptID: payload.AttemptID}

	if id.nodeType == string(workflow.NodeAgent) {
		if words, err := finalAssistantMessage(ctx, p.uow, payload.AttemptID); err == nil && words != "" {
			say := base
			say.Key, say.Role, say.Actor = "say:"+payload.AttemptID, message.RoleAssistant, p.actor(id)
			say.Text = words
			if id.attemptState != runtimedomain.ExecutionAttemptSucceeded {
				say.Text = "(last words before the attempt stopped)\n" + words
			}
			p.post(ctx, say)
		}
	}

	detail, err := GetNodeRunDetail(ctx, p.uow, p.store, p.matcher, payload.NodeRunID)
	line := base
	line.Key, line.Role, line.Actor = "end:"+payload.AttemptID, message.RoleSystem, "aw"
	switch id.attemptState {
	case runtimedomain.ExecutionAttemptSucceeded:
		parts := []string{fmt.Sprintf("%s finished", id.nodeKey)}
		if err == nil {
			if detail.Result.SelectedOutcome != "" {
				parts = append(parts, "outcome "+detail.Result.SelectedOutcome)
			}
			if n := len(detail.Result.ChangedFiles); n > 0 {
				parts = append(parts, fmt.Sprintf("%d file%s changed", n, plural(n)))
			}
			if detail.Result.Usage != nil && detail.Result.Usage.CostUSD > 0 {
				parts = append(parts, fmt.Sprintf("cost $%.4f", detail.Result.Usage.CostUSD))
			}
		}
		line.Text = strings.Join(parts, ", ") + "."
	case runtimedomain.ExecutionAttemptCancelled:
		line.Text = fmt.Sprintf("%s was cancelled.", id.nodeKey)
	case runtimedomain.ExecutionAttemptBlocked:
		line.Text = fmt.Sprintf("%s is blocked: it asked for a scope expansion that needs a decision.", id.nodeKey)
	default:
		line.Text = fmt.Sprintf("%s failed.", id.nodeKey)
		if err == nil && detail.Failure != nil {
			line.Text = fmt.Sprintf("%s failed: %s. %s", id.nodeKey, detail.Failure.Title, detail.Failure.Suggestion)
			if detail.Failure.Detail != "" {
				line.Text += " (" + detail.Failure.Detail + ")"
			}
		}
	}
	p.post(ctx, line)
}

// NodeReached posts that an APPROVAL node now waits for a decision. Other kinds of node post nothing here.
func (p *ActivityPoster) NodeReached(ctx context.Context, nodeRunID string) {
	if p == nil || nodeRunID == "" {
		return
	}
	id, ok := p.identify(ctx, nodeRunID, "")
	if !ok || id.nodeType != string(workflow.NodeApproval) {
		return
	}
	p.post(ctx, appmessage.ActivityRequest{
		ProjectID: id.projectID, WorkItemID: id.workItemID, NodeRunID: nodeRunID, Key: "approval:" + nodeRunID,
		Role: message.RoleSystem, Actor: "aw", Text: fmt.Sprintf("%s is waiting for your approval. Open the Graph tab to decide.", id.nodeKey),
	})
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
