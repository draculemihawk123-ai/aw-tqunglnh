// V5-15C — "Isolation unavailable" (docs/design/07-v5-execution-evidence.md
// V5-15's own "provider loss, adapter drift, isolation unavailable" line;
// user's own binding 5-part PR split, full text in baocaov5checklist.md's
// own "V5-15" section and memory
// agent-kit-v5-15-acceptance-gate-contract.md).
//
// Proves a real production rejection, not a simulated one: every earlier
// V5-15 scenario wires the permissive fake.IsolationEnforcementChecker{}
// (Err: nil) so its own real node executor can actually run — this file is
// the first to wire the REAL, non-fake process.IsolationChecker{}
// (internal/adapters/process/isolation.go), which is a static, I/O-free
// fact about this codebase's own Supervisor: it structurally always
// returns ErrIsolationEnforcementUnavailable for
// policy.IsolationTierEnforcedIsolated (no real OS-level sandbox exists
// today, ADR-013/ADR-023's own "không bao giờ auto-downgrade"). A real
// COMMAND node pinned to that tier (v5AcceptPermissionPolicyDocument
// already requests it — every other scenario in this package just happens
// to run under a permissive fake) therefore fails real admission
// (admission.go's own runAdmissionProbePhase/evaluateAdmission) BEFORE
// ProcessSupervisor.Run is ever called — proven here not by asserting
// error strings alone, but by confirming the real maker script's own
// marker file was never written to disk: the real process genuinely never
// spawned.
package v5accept

import (
	"context"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"testing"

	processadapter "github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// v5AcceptIsolationUnavailableDocument is a minimal real graph — a single
// real COMMAND node, no gate, no completion policy needed: admission never
// even reaches the point where a completion policy would matter.
func v5AcceptIsolationUnavailableDocument() workflow.WorkflowDocument {
	attemptPermissionRefs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v5c-iso-attempt-policy-def", VersionID: "v5c-iso-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v5c-iso-permission-policy-def", VersionID: "v5c-iso-permission-policy-v1"},
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{
				Key: "test_a", Type: workflow.NodeCommand, Outcomes: []string{"passed"},
				Command: &workflow.CommandNodeConfig{
					CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v5c-iso-maker-cmd-def", VersionID: "v5c-iso-maker-cmd-v1"},
					PolicyRefs: attemptPermissionRefs,
				},
			},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-test_a", From: "start", Outcome: "next", To: "test_a"},
			{Key: "test_a-end", From: "test_a", Outcome: "passed", To: "end"},
		},
	}
}

func TestV5AcceptIsolationUnavailable_RealAdmissionRejectsBeforeSpawn(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	// The marker path a real maker script would write to, if it ever ran —
	// this test's own proof that admission genuinely rejected the Attempt
	// BEFORE any real process spawn, not merely that some later check
	// happened to also fail.
	markerPath := filepath.Join(f.fixtureRoot, "v5c-iso-marker.txt")
	makerKey, makerScript, _, _ := v5AcceptScripts(markerPath)
	skillDoc := v5AcceptTwoResourceSkillDocument(makerKey, makerScript, "unused.txt", "unused")
	publishSkillVersion(t, f.uow, "v5c-iso-skill-def", "v5c-iso-skill-v1", skillDoc)
	makerHash := resourceContentHash(t, "v5c-iso-skill-v1", makerKey, skillDoc)

	publishCommandVersion(t, f.uow, "v5c-iso-maker-cmd-def", "v5c-iso-maker-cmd-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "v5c-iso-skill-v1", ResourceKey: makerKey, ContentHash: makerHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: v5AcceptRepositoryID,
		Compatibility:       command.Compatibility{OS: []string{stdruntime.GOOS}},
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      60,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})

	publishPolicyVersion(t, f.uow, "v5c-iso-attempt-policy-def", "v5c-iso-attempt-policy-v1", v5AcceptAttemptPolicyDocument(60))
	// v5AcceptPermissionPolicyDocument already pins
	// IsolationTier: policy.IsolationTierEnforcedIsolated — the exact tier
	// the real process.IsolationChecker{} below can never honestly satisfy.
	publishPolicyVersion(t, f.uow, "v5c-iso-permission-policy-def", "v5c-iso-permission-policy-v1", v5AcceptPermissionPolicyDocument())

	doc := v5AcceptIsolationUnavailableDocument()
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v5c-iso-workflow-def", "v5c-iso-workflow-v1", doc, workflow.DependencyManifest{})

	router := &runtime.NodeExecutorRouter{Command: f.newCommandExecutor()}
	registry := f.registerHandlersWithIsolation(router, "v5ciso", agentregistry.Empty(), processadapter.NewIsolationChecker())
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	root := f.createRootWorkItem(t, "iso-unavailable-root", workdomain.RepositoryWrite)
	child := f.createChildWorkItem(t, root.WorkItemID, "iso-unavailable-child", workdomain.RepositoryWrite)

	startCmd := testCmd("v5c-iso-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := started.RunID

	nodeRun := f.waitForNodeRunState(t, runID, "test_a", runtimedomain.NodeRunBlocked)

	var attempts []runtimedomain.ExecutionAttempt
	var blockers []workdomain.WorkItemBlocker
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		if err != nil {
			return err
		}
		blockers, err = tx.Work().ListWorkItemBlockersForWorkItem(ctx, child.WorkItemID)
		return err
	}); err != nil {
		t.Fatalf("read admission-blocked state: %v", err)
	}

	var blockedAttempt *runtimedomain.ExecutionAttempt
	for i := range attempts {
		if attempts[i].NodeRunID == nodeRun.ID {
			blockedAttempt = &attempts[i]
		}
	}
	if blockedAttempt == nil {
		t.Fatalf("no ExecutionAttempt found for node run %s", nodeRun.ID)
	}
	if blockedAttempt.State != runtimedomain.ExecutionAttemptBlocked {
		t.Fatalf("attempt.State = %s, want BLOCKED", blockedAttempt.State)
	}
	if blockedAttempt.TerminationReason != runtimedomain.TerminationReasonIsolationEnforcementUnavailable {
		t.Fatalf("attempt.TerminationReason = %s, want %s", blockedAttempt.TerminationReason, runtimedomain.TerminationReasonIsolationEnforcementUnavailable)
	}

	foundBlocker := false
	for _, blocker := range blockers {
		if blocker.Type == workdomain.BlockerIsolationEnforcementUnavailable {
			foundBlocker = true
			if blocker.State != workdomain.BlockerOpen {
				t.Fatalf("blocker.State = %s, want OPEN", blocker.State)
			}
			if blocker.SourceAttemptID != string(blockedAttempt.ID) {
				t.Fatalf("blocker.SourceAttemptID = %s, want %s", blocker.SourceAttemptID, blockedAttempt.ID)
			}
		}
	}
	if !foundBlocker {
		t.Fatalf("no WorkItemBlocker of type %s found for work item %s; blockers=%+v", workdomain.BlockerIsolationEnforcementUnavailable, child.WorkItemID, blockers)
	}

	// The real proof admission rejected this BEFORE any spawn: the real
	// maker script's own marker file was never written, because
	// ProcessSupervisor.Run was never called at all.
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("marker file %s exists (err=%v) — the real maker process was spawned despite isolation enforcement being unavailable", markerPath, err)
	}
}
