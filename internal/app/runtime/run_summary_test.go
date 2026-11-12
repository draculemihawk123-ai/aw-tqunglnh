package runtime_test

import (
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
)

func node(key, state, outcome string) runtime.TimelineEntryView {
	return runtime.TimelineEntryView{Kind: runtime.TimelineEntryNodeRun, NodeRunID: "nr-" + key + "-" + state, NodeKey: key, NodeState: state, SelectedOutcome: outcome}
}

func attempt(n runtime.TimelineEntryView, number uint32, code, detail, provider string) runtime.TimelineEntryView {
	return runtime.TimelineEntryView{Kind: runtime.TimelineEntryExecutionAttempt, NodeRunID: n.NodeRunID, NodeKey: n.NodeKey,
		AttemptNumber: number, AttemptState: "FAILED", FailureCode: code, FailureDetail: detail, ProviderKey: provider}
}

// V12-11: one test per stop reason code, the P2-02 case (gate1's fourth attempt could not start, no evidence) first.
func TestDeriveStopReason(t *testing.T) {
	gate1 := node("gate1", "FAILED", "")
	build := node("build", "FAILED", "")
	reject := node("reject", "FAILED", "")
	cases := []struct {
		name     string
		state    string
		entries  []runtime.TimelineEntryView
		blockers []runtime.BlockerDiagnostic
		code     string
		nodeKey  string
		contains string
	}{
		{"command not started (P2-02)", "FAILED",
			[]runtime.TimelineEntryView{node("size", "SUCCEEDED", "large"), gate1, attempt(gate1, 4, "PROVIDER_UNAVAILABLE", "", "")},
			nil, runtime.StopCommandNotStarted, "gate1", "could not be started (attempt 4)"},
		{"command not started, Windows exit code", "FAILED",
			[]runtime.TimelineEntryView{gate1, attempt(gate1, 1, "PROVIDER_UNAVAILABLE", "the process exited 3221225794 (0xC0000142) before it ran: STATUS_DLL_INIT_FAILED", "")},
			nil, runtime.StopCommandNotStarted, "gate1", "0xC0000142"},
		{"scope violation (P2-01)", "FAILED",
			[]runtime.TimelineEntryView{build, attempt(build, 1, "SCOPE_VIOLATION", "backend/application.yml", "claude")},
			nil, runtime.StopScopeViolation, "build", "backend/application.yml"},
		{"provider quota (FAILED before V12-03)", "FAILED",
			[]runtime.TimelineEntryView{build, attempt(build, 1, "PROVIDER_QUOTA_EXCEEDED", "You've hit your limit", "claude")},
			nil, runtime.StopProviderQuotaExceeded, "build", "usage limit"},
		{"cycle escalated", "FAILED",
			[]runtime.TimelineEntryView{node("simplify", "SKIPPED", "escalated"), reject},
			nil, runtime.StopCycleEscalated, "simplify", "ran out of rounds"},
		{"approval rejected", "FAILED",
			[]runtime.TimelineEntryView{node("gate2", "SUCCEEDED", "rejected"), reject},
			nil, runtime.StopApprovalRejected, "gate2", "rejected"},
		{"definition defect", "FAILED", []runtime.TimelineEntryView{node("build", "SUCCEEDED", "done")},
			[]runtime.BlockerDiagnostic{{Type: "RUN_FAILED", State: "OPEN", Reason: "run failed: WORKFLOW_DEFINITION_DEFECT: node simplify"}},
			runtime.StopWorkflowDefinitionDefect, "", "cannot route"},
		{"adapter build drift blocks a live run", "RUNNING", []runtime.TimelineEntryView{node("build", "BLOCKED", "")},
			[]runtime.BlockerDiagnostic{{Type: "ADAPTER_BUILD_DRIFT", State: "OPEN", SourceNodeRunID: "nr-build-BLOCKED", Reason: "adapter build drift: observed build b2"}},
			"ADAPTER_BUILD_DRIFT", "build", "blocked (ADAPTER_BUILD_DRIFT)"},
		{"approval pending", "WAITING", []runtime.TimelineEntryView{node("gate2", "WAITING", "")}, nil, runtime.StopApprovalPending, "gate2", "waiting for a decision"},
		{"cancelled", "CANCELLED", nil, nil, runtime.StopRunCancelled, "", "cancelled"},
		{"other failure", "FAILED", []runtime.TimelineEntryView{build, attempt(build, 2, "EXECUTION_FAILED", "", "claude")}, nil, runtime.StopAttemptFailed, "build", "EXECUTION_FAILED, attempt 2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runtime.DeriveStopReason(c.state, c.entries, c.blockers)
			if got == nil {
				t.Fatalf("stop reason = nil, want %s", c.code)
			}
			if got.Code != c.code || got.NodeKey != c.nodeKey || !strings.Contains(got.Message, c.contains) {
				t.Fatalf("stop reason = %+v, want code %s node %q and a message containing %q", got, c.code, c.nodeKey, c.contains)
			}
		})
	}
	if got := runtime.DeriveStopReason("SUCCEEDED", []runtime.TimelineEntryView{node("end", "SUCCEEDED", "")}, nil); got != nil {
		t.Fatalf("a succeeded run has no stop reason, got %+v", got)
	}
	if got := runtime.DeriveStopReason("RUNNING", []runtime.TimelineEntryView{node("build", "RUNNING", "")}, nil); got != nil {
		t.Fatalf("a run moving on its own has no stop reason, got %+v", got)
	}
}

func TestResultSummary(t *testing.T) {
	cases := []struct {
		name   string
		detail runtime.NodeRunDetail
		want   string
	}{
		{"agent: first line of the report", runtime.NodeRunDetail{State: "SUCCEEDED", Result: runtime.NodeResultView{Kind: "AGENT",
			FinalMessage: &runtime.NodeTextView{Text: "\n## Fixed the reopen bug in IssueService\nDetails…\n<agentkit-outcome>{}</agentkit-outcome>"}}},
			"Fixed the reopen bug in IssueService"},
		{"command: the kit's summary line", runtime.NodeRunDetail{State: "SUCCEEDED", Result: runtime.NodeResultView{Kind: "COMMAND",
			Command: &runtime.NodeCommandResult{Stdout: "diff-size: component backend/: 900 lines, 10 files (large)\n", Stderr: "diff-size: large diff: backend/: 900 changed lines (threshold 800);\nWHY: …\nFIX: …\n"}}},
			"diff-size: large diff: backend/: 900 changed lines (threshold 800);"},
		{"command: else the last line", runtime.NodeRunDetail{State: "SUCCEEDED", Result: runtime.NodeResultView{Kind: "COMMAND",
			Command: &runtime.NodeCommandResult{Stdout: "BUILD SUCCESS\nTotal time 3s\n"}}}, "Total time 3s"},
		{"approval: decision and actor", runtime.NodeRunDetail{State: "SUCCEEDED", Result: runtime.NodeResultView{Kind: "APPROVAL"},
			Approval: &runtime.ApprovalRequestView{ResolvedOutcome: "approved", ResolvedBy: "owner"}}, "approved by owner"},
		{"failed: the failure in words", runtime.NodeRunDetail{State: "FAILED", Failure: &runtime.ExplainedFailure{Title: "The attempt ended with an error", Detail: "exit 1"}},
			"The attempt ended with an error: exit 1"},
	}
	for _, c := range cases {
		if got := runtime.ResultSummary(c.detail); got != c.want {
			t.Errorf("%s: ResultSummary = %q, want %q", c.name, got, c.want)
		}
	}
	long := runtime.ResultSummary(runtime.NodeRunDetail{Result: runtime.NodeResultView{Kind: "AGENT", FinalMessage: &runtime.NodeTextView{Text: strings.Repeat("ă", 500)}}})
	if n := len([]rune(long)); n != 201 {
		t.Fatalf("summary is %d runes, want 200 and an ellipsis", n)
	}
}
