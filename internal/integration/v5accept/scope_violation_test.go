// V5-15D — "Scope violation" (docs/design/07-v5-execution-evidence.md
// V5-15's own "scope violation, checker write attempt, cancel giữa
// mutating attempt" line; user's own binding 5-part PR split, full text in
// baocaov5checklist.md's own "V5-15" section and memory
// agent-kit-v5-15-acceptance-gate-contract.md).
//
// Proves internal/app/scopeguard.ValidateDiffs really rejects a real,
// observed out-of-scope write end to end, not merely in its own unit test:
// a real COMMAND node's real script writes a real file OUTSIDE the
// WorkItem's own real, narrower-than-whole-repository WRITE grant
// (createChildWorkItemWithPathScopes, PathScopes: ["allowed"] — a real,
// literal-prefix grant, scopeguard's own isAllowed never a glob) — the
// real post-execution diff (a real `git status`) sees the real new file,
// and ValidateDiffs really rejects it as outside every granted PathScopes
// prefix. Never a "checker forced read-only" case (that is this file's own
// sibling, checker_write_test.go) — this WorkItem genuinely holds a real
// WRITE grant, just a narrower one than the script's own real write
// target.
package v5accept

import (
	"context"
	stdruntime "runtime"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// v5AcceptScopeViolationScript is a real, cross-platform script that
// writes a real file at the repository's own root — "leaked.txt", deliberately
// OUTSIDE the "allowed" PathScopes prefix this file's own scenario grants.
func v5AcceptScopeViolationScript() (key, script string) {
	if stdruntime.GOOS == "windows" {
		return "leak.bat", "@echo off\r\necho leaked> leaked.txt\r\nexit /b 0\r\n"
	}
	return "leak.sh", "#!/bin/sh\necho leaked > leaked.txt\nexit 0\n"
}

func v5AcceptScopeViolationDocument() workflow.WorkflowDocument {
	attemptPermissionRefs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v5d-scope-attempt-policy-def", VersionID: "v5d-scope-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v5d-scope-permission-policy-def", VersionID: "v5d-scope-permission-policy-v1"},
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{
				Key: "leaker", Type: workflow.NodeCommand, Outcomes: []string{"passed"},
				Command: &workflow.CommandNodeConfig{
					CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v5d-scope-cmd-def", VersionID: "v5d-scope-cmd-v1"},
					PolicyRefs: attemptPermissionRefs,
				},
			},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-leaker", From: "start", Outcome: "next", To: "leaker"},
			{Key: "leaker-end", From: "leaker", Outcome: "passed", To: "end"},
		},
	}
}

func TestV5AcceptScopeViolation_RealDiffRejectsOutOfScopeWrite(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	leakKey, leakScript := v5AcceptScopeViolationScript()
	skillDoc := v5AcceptTwoResourceSkillDocument(leakKey, leakScript, "unused.txt", "unused")
	publishSkillVersion(t, f.uow, "v5d-scope-skill-def", "v5d-scope-skill-v1", skillDoc)
	leakHash := resourceContentHash(t, "v5d-scope-skill-v1", leakKey, skillDoc)

	publishCommandVersion(t, f.uow, "v5d-scope-cmd-def", "v5d-scope-cmd-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "v5d-scope-skill-v1", ResourceKey: leakKey, ContentHash: leakHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: v5AcceptRepositoryID,
		Compatibility:       command.Compatibility{OS: []string{stdruntime.GOOS}},
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      60,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})

	publishPolicyVersion(t, f.uow, "v5d-scope-attempt-policy-def", "v5d-scope-attempt-policy-v1", v5AcceptAttemptPolicyDocument(60))
	publishPolicyVersion(t, f.uow, "v5d-scope-permission-policy-def", "v5d-scope-permission-policy-v1", v5AcceptDriftPermissionPolicyDocument())

	doc := v5AcceptScopeViolationDocument()
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v5d-scope-workflow-def", "v5d-scope-workflow-v1", doc, workflow.DependencyManifest{})

	router := &runtime.NodeExecutorRouter{Command: f.newCommandExecutor()}
	registry := f.registerHandlers(router, "v5dsv")
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	// A real, narrower-than-whole-repository WRITE grant: only "allowed"
	// (and anything under "allowed/") is in scope — the real script above
	// writes "leaked.txt" at the repository root, a real path this grant
	// never covers.
	root := f.createRootWorkItem(t, "scope-violation-root", workdomain.RepositoryWrite)
	child := f.createChildWorkItemWithPathScopes(t, root.WorkItemID, "scope-violation-child", workdomain.RepositoryWrite, []string{"allowed"})

	startCmd := testCmd("v5d-scope-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := started.RunID

	// v5AcceptAttemptPolicyDocument declares no RetryableErrorCodes, so a
	// real CodeScopeViolation failure is non-retryable — the SAME real
	// NON_RETRYABLE_FAILURE -> transitionRunToFailedTx path V5-15B's own
	// gate-FAIL case already exercises (completion.go, nothing new here):
	// the Run fails directly, never reaching VERIFYING.
	run := f.waitForRunState(t, runID, runtimedomain.WorkflowRunFailed)

	var workItem workdomain.WorkItem
	var attempts []runtimedomain.ExecutionAttempt
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		if workItem, err = tx.Work().GetWorkItem(ctx, child.WorkItemID); err != nil {
			return err
		}
		attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("read final state: %v", err)
	}
	if run.State != runtimedomain.WorkflowRunFailed {
		t.Fatalf("run.State = %s, want FAILED", run.State)
	}
	if workItem.Status == workdomain.WorkItemDone {
		t.Fatal("workItem.Status = DONE — a real scope violation must never reach DONE")
	}

	var failed *runtimedomain.ExecutionAttempt
	for i := range attempts {
		if attempts[i].State == runtimedomain.ExecutionAttemptFailed {
			failed = &attempts[i]
		}
	}
	if failed == nil {
		t.Fatalf("no ExecutionAttempt reached FAILED; attempts=%+v", attempts)
	}
	if failed.TerminationReason != runtimedomain.TerminationReasonScopeViolation {
		t.Fatalf("attempt.TerminationReason = %s, want %s", failed.TerminationReason, runtimedomain.TerminationReasonScopeViolation)
	}
	if failed.FailureCode != errorcode.CodeScopeViolation {
		t.Fatalf("attempt.FailureCode = %s, want %s", failed.FailureCode, errorcode.CodeScopeViolation)
	}
	// Exactly one real Attempt — CodeScopeViolation is genuinely
	// non-retryable (v5AcceptAttemptPolicyDocument declares no
	// RetryableErrorCodes), so this must never have retried.
	if len(attempts) != 1 {
		t.Fatalf("ExecutionAttempts for run %s = %d, want exactly 1 (non-retryable)", runID, len(attempts))
	}
}
