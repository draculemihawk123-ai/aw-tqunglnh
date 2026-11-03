// V12-11 (docs/design/15-v12-ui-operations.md, ADR-051): the two answers the run graph banner and history panel need and that
// were only reachable through the CLI — what each activation produced, in one line (resultSummary), and why a run stopped,
// typed and in words (stopReason).
package runtime

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// maxSummaryRunes bounds a result summary.
const maxSummaryRunes = 200

// commandSummaryLine is the kit's convention for a command's summary line: "<command>: <what happened>" (diff-size: large
// diff: …, expect-fail: backend: the tests are RED as expected …).
var commandSummaryLine = regexp.MustCompile(`^[a-z][a-z0-9-]*: \S`)

// ResultSummary is the one-line result of a node run, from its detail (already redacted): an AGENT's first line of its final
// report, a COMMAND's summary line by the kit's convention (else its last non-empty output line), an APPROVAL's decision, a
// MACHINE_GATE's verdict count, and for a failed run of any node the failure in words.
func ResultSummary(detail NodeRunDetail) string {
	if detail.Failure != nil && (detail.State == string(runtimedomain.NodeRunFailed) || detail.State == string(runtimedomain.NodeRunBlocked)) {
		text := detail.Failure.Title
		if detail.Failure.Detail != "" {
			text += ": " + detail.Failure.Detail
		}
		return bound(text)
	}
	switch detail.Result.Kind {
	case "AGENT":
		if detail.Result.FinalMessage != nil {
			if line := firstMeaningfulLine(detail.Result.FinalMessage.Text); line != "" {
				return bound(line)
			}
		}
		if n := len(detail.Result.ChangedFiles); n > 0 {
			return fmt.Sprintf("changed %d file%s", n, plural(n))
		}
	case "COMMAND":
		if c := detail.Result.Command; c != nil {
			if line := commandSummary(c.Stdout + "\n" + c.Stderr); line != "" {
				return bound(line)
			}
			return fmt.Sprintf("exit code %d", c.ExitCode)
		}
	case "MACHINE_GATE":
		pass := 0
		for _, c := range detail.Result.Criteria {
			if c.Verdict == "PASS" {
				pass++
			}
		}
		if n := len(detail.Result.Criteria); n > 0 {
			return fmt.Sprintf("%d of %d criteria passed", pass, n)
		}
	case "APPROVAL":
		if a := detail.Approval; a != nil && a.ResolvedOutcome != "" {
			text := a.ResolvedOutcome
			if a.ResolvedBy != "" {
				text += " by " + a.ResolvedBy
			}
			return bound(text)
		}
		return bound(detail.Result.Note)
	}
	if detail.Result.Note != "" {
		return bound(detail.Result.Note)
	}
	return ""
}

func firstMeaningfulLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#*->"))
		if line != "" && !strings.HasPrefix(line, "<agentkit-outcome") && line != "```" {
			return line
		}
	}
	return ""
}

func commandSummary(output string) string {
	lines := strings.Split(output, "\n")
	last := ""
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if last == "" {
			last = line
		}
		if commandSummaryLine.MatchString(line) {
			return line
		}
	}
	return last
}

func bound(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > maxSummaryRunes {
		return string(runes[:maxSummaryRunes]) + "…"
	}
	return text
}

// SummarizeRunActivations fills ResultSummary on every activation of graph that has run (a pending one has nothing to say).
// It reads each node run's detail, as getNodeRunDetail does; a node run whose detail cannot be read keeps an empty summary.
func SummarizeRunActivations(ctx context.Context, uow ports.UnitOfWork, store ports.ArtifactStore, matcher redact.Matcher, graph *RunGraph) {
	for i := range graph.Activations {
		a := &graph.Activations[i]
		switch runtimedomain.NodeRunState(a.State) {
		case runtimedomain.NodeRunPending, runtimedomain.NodeRunReady, runtimedomain.NodeRunQueued:
			continue
		}
		detail, err := GetNodeRunDetail(ctx, uow, store, matcher, a.NodeRunID)
		if err != nil {
			continue
		}
		a.ResultSummary = ResultSummary(detail)
	}
}

// Stop reason codes (V12-11). The set is open in the sense that a blocker type is used as its own code.
const (
	StopCommandNotStarted        = "COMMAND_NOT_STARTED"
	StopScopeViolation           = "SCOPE_VIOLATION"
	StopProviderQuotaExceeded    = "PROVIDER_QUOTA_EXCEEDED"
	StopCycleEscalated           = "CYCLE_ESCALATED"
	StopApprovalRejected         = "APPROVAL_REJECTED"
	StopApprovalPending          = "APPROVAL_PENDING"
	StopWorkflowDefinitionDefect = "WORKFLOW_DEFINITION_DEFECT"
	StopAttemptFailed            = "ATTEMPT_FAILED"
	StopRunCancelled             = "RUN_CANCELLED"
	StopRunFailed                = "RUN_FAILED"
)

// StopReason is why a run stopped, or is waiting: a typed code, the node it concerns and a sentence in English.
type StopReason struct {
	Code      string `json:"code"`
	NodeKey   string `json:"nodeKey,omitempty"`
	NodeRunID string `json:"nodeRunId,omitempty"`
	Message   string `json:"message"`
}

// exitOutcomes are the outcomes that leave the normal flow (the same list web/src/graph/layoutRunGraph.ts documents).
var exitOutcomes = map[string]bool{"needs_info": true, "escalated": true, "rejected": true, "abandon": true}

// DeriveStopReason decides the stop reason from the run's state, its timeline (in activation order) and its blockers. nil for a
// run that is moving on its own (running with nothing blocked or pending) or that succeeded.
func DeriveStopReason(runState string, entries []TimelineEntryView, blockers []BlockerDiagnostic) *StopReason {
	nodeKeyOf := map[string]string{}
	for _, e := range entries {
		nodeKeyOf[e.NodeRunID] = e.NodeKey
	}
	// A node that waits for something: an open blocker naming it.
	for _, b := range blockers {
		if b.State != string(workdomain.BlockerOpen) || b.SourceNodeRunID == "" {
			continue
		}
		if b.Type == string(workdomain.BlockerRunFailed) || b.Type == string(workdomain.BlockerRunCancelled) {
			continue
		}
		key := nodeKeyOf[b.SourceNodeRunID]
		return &StopReason{Code: b.Type, NodeKey: key, NodeRunID: b.SourceNodeRunID,
			Message: fmt.Sprintf("%s is blocked (%s): %s", orNode(key), b.Type, bound(b.Reason))}
	}
	switch runState {
	case string(runtimedomain.WorkflowRunSucceeded):
		return nil
	case string(runtimedomain.WorkflowRunCancelled), string(runtimedomain.WorkflowRunCancelling):
		return &StopReason{Code: StopRunCancelled, Message: "The run was cancelled."}
	case string(runtimedomain.WorkflowRunFailed):
		return failedStopReason(entries, blockers)
	}
	// Running: a waiting approval is the one thing worth saying.
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Kind == TimelineEntryNodeRun && e.NodeState == string(runtimedomain.NodeRunWaiting) {
			return &StopReason{Code: StopApprovalPending, NodeKey: e.NodeKey, NodeRunID: e.NodeRunID,
				Message: fmt.Sprintf("%s is waiting for a decision.", e.NodeKey)}
		}
	}
	return nil
}

func failedStopReason(entries []TimelineEntryView, blockers []BlockerDiagnostic) *StopReason {
	var nodes []TimelineEntryView
	lastAttempt := map[string]TimelineEntryView{}
	for _, e := range entries {
		if e.Kind == TimelineEntryNodeRun {
			nodes = append(nodes, e)
		} else if e.Kind == TimelineEntryExecutionAttempt {
			lastAttempt[e.NodeRunID] = e
		}
	}
	for _, b := range blockers {
		if b.Type == string(workdomain.BlockerRunFailed) && strings.Contains(b.Reason, StopWorkflowDefinitionDefect) {
			return &StopReason{Code: StopWorkflowDefinitionDefect, Message: "The workflow definition cannot route this run: " + bound(b.Reason)}
		}
	}
	for i := len(nodes) - 1; i >= 0; i-- {
		n := nodes[i]
		if n.NodeState != string(runtimedomain.NodeRunFailed) {
			continue
		}
		// Reached through an exit outcome (a reject node): the reason is the node that took that exit.
		if i > 0 && exitOutcomes[nodes[i-1].SelectedOutcome] {
			prev := nodes[i-1]
			switch {
			case prev.NodeState == string(runtimedomain.NodeRunSkipped):
				return &StopReason{Code: StopCycleEscalated, NodeKey: prev.NodeKey, NodeRunID: prev.NodeRunID,
					Message: fmt.Sprintf("%s ran out of rounds (its cycle budget) and the run was stopped.", prev.NodeKey)}
			case prev.SelectedOutcome == "rejected":
				return &StopReason{Code: StopApprovalRejected, NodeKey: prev.NodeKey, NodeRunID: prev.NodeRunID,
					Message: fmt.Sprintf("%s was rejected and the run was stopped.", prev.NodeKey)}
			default:
				return &StopReason{Code: StopCycleEscalated, NodeKey: prev.NodeKey, NodeRunID: prev.NodeRunID,
					Message: fmt.Sprintf("%s ended with %q and the run was stopped.", prev.NodeKey, prev.SelectedOutcome)}
			}
		}
		a, ok := lastAttempt[n.NodeRunID]
		if !ok {
			return &StopReason{Code: StopAttemptFailed, NodeKey: n.NodeKey, NodeRunID: n.NodeRunID, Message: fmt.Sprintf("%s failed.", n.NodeKey)}
		}
		detail := ""
		if a.FailureDetail != "" {
			detail = ": " + bound(a.FailureDetail)
		}
		switch {
		case a.FailureCode == StopScopeViolation:
			return &StopReason{Code: StopScopeViolation, NodeKey: n.NodeKey, NodeRunID: n.NodeRunID,
				Message: fmt.Sprintf("%s changed files outside the work item's scope%s.", n.NodeKey, detail)}
		case a.FailureCode == StopProviderQuotaExceeded:
			return &StopReason{Code: StopProviderQuotaExceeded, NodeKey: n.NodeKey, NodeRunID: n.NodeRunID,
				Message: fmt.Sprintf("%s stopped: the provider's usage limit was reached%s.", n.NodeKey, detail)}
		case a.ProviderKey == "" && (strings.Contains(a.FailureDetail, "could not be started") || strings.Contains(a.FailureDetail, "before it ran") ||
			(a.FailureCode == "PROVIDER_UNAVAILABLE" && a.FailureDetail == "")):
			return &StopReason{Code: StopCommandNotStarted, NodeKey: n.NodeKey, NodeRunID: n.NodeRunID,
				Message: fmt.Sprintf("The %s command could not be started (attempt %d)%s; no evidence was recorded.", n.NodeKey, a.AttemptNumber, detail)}
		default:
			code := a.FailureCode
			if code == "" {
				code = a.TerminationReason
			}
			return &StopReason{Code: StopAttemptFailed, NodeKey: n.NodeKey, NodeRunID: n.NodeRunID,
				Message: fmt.Sprintf("%s failed (%s, attempt %d)%s.", n.NodeKey, code, a.AttemptNumber, detail)}
		}
	}
	return &StopReason{Code: StopRunFailed, Message: "The run failed."}
}

func orNode(key string) string {
	if key == "" {
		return "A node"
	}
	return key
}
