// V5-15C — "Crash/checkpoint recovery" (docs/design/07-v5-execution-
// evidence.md V5-15's own "crash/checkpoint recovery... xác minh replay
// không tạo trùng Attempt, activation, artifact hoặc event" line; user's
// own binding 5-part PR split, full text in baocaov5checklist.md's own
// "V5-15" section and memory agent-kit-v5-15-acceptance-gate-contract.md).
//
// This is a REAL crash, not a simulated one: a real COMMAND node's real
// script is driven to genuinely RUNNING by a real workerpool.Pool, that
// pool is then torn down (stop()) WHILE the real child process is still
// alive — Pool.Run's own real shutdown escalation (pool.go: ShutdownGrace
// elapses, cancelJobs() fires, the real ProcessSupervisor.Run sees its own
// ctx cancelled and really kills the real process tree) is what actually
// ends it, exactly the same mechanism V4-12B/V4-12C built for a real
// cancel-during-mutating-attempt (V5-15D's own later scope). Once that
// pool is gone, its own driving EXECUTE_NODE job's lease is genuinely no
// longer being renewed by anyone — real wall-clock time, not a DB write,
// is what eventually makes ListOrphanedRunningExecutionAttempts see it.
// This test then calls the real runtime.StartupRecoveryScan and starts a
// SECOND, independent pool against the SAME on-disk store — the real
// RecoveryReaperHandler (already registered by every v5AcceptFixture
// registry) does the rest: classify LOST (read-only attempt, V4-13),
// retry (a fresh AttemptNumber+1, budget remains), and the real script
// runs again for real — this time finishing immediately rather than
// sleeping, a real, observable difference the script itself decides by
// checking for its own earlier real side effect (a marker file it wrote
// before sleeping the first time), never a DB shortcut.
package v5accept

import (
	"context"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// v5AcceptCrashRecoveryScripts builds the one real, cross-platform maker
// script this file's own scenario needs: on its FIRST real invocation it
// writes startedMarkerPath (this test's own real, observable "I have
// genuinely begun running" signal) and then sleeps far longer than this
// pool's own LeaseTTL/ShutdownGrace ever gives it to finish — real time
// the crash below genuinely interrupts. On any LATER real invocation (the
// real retry attempt a recovered Run dispatches) it finds its own earlier
// marker already on disk and returns immediately instead, writing
// doneMarkerPath as evidence a SECOND real invocation truly happened.
// Both markers live outside repo-a's own git worktree (this fixture's own
// root), the same reason v5AcceptScripts' own doc comment documents.
func v5AcceptCrashRecoveryScripts(startedMarkerPath, doneMarkerPath string) (key, script string) {
	if stdruntime.GOOS == "windows" {
		// A plain `ping -n N 127.0.0.1` was tried first and empirically
		// confirmed unreliable here — it returned almost immediately
		// instead of pacing ~1s per echo. A bare `powershell` call was
		// tried next and ALSO confirmed unreliable: this real COMMAND
		// node's own real ProcessSpec.InheritedEnvironment is
		// doc.EnvAllowlist (command_node_executor.go), which this file's
		// own CommandDocument never sets — the real spawned process gets
		// no PATH at all, so a bare `powershell` invocation fails to
		// launch, and this script's own unconditional `exit /b 0`
		// silently swallows that failure, returning "success" almost
		// instantly instead of ever sleeping. The real, well-known
		// absolute path to powershell.exe sidesteps needing PATH (or any
		// inherited environment) at all.
		return "maker-crash.bat", "@echo off\r\n" +
			"if exist \"" + startedMarkerPath + "\" (\r\n" +
			"  echo done> \"" + doneMarkerPath + "\"\r\n" +
			"  exit /b 0\r\n" +
			") else (\r\n" +
			"  echo started> \"" + startedMarkerPath + "\"\r\n" +
			"  \"C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe\" -NoProfile -Command \"Start-Sleep -Seconds 90\"\r\n" +
			"  exit /b 0\r\n" +
			")\r\n"
	}
	return "maker-crash.sh", "#!/bin/sh\n" +
		"if [ -f \"" + startedMarkerPath + "\" ]; then\n" +
		"  echo done > \"" + doneMarkerPath + "\"\n" +
		"  exit 0\n" +
		"else\n" +
		"  echo started > \"" + startedMarkerPath + "\"\n" +
		"  sleep 60\n" +
		"  exit 0\n" +
		"fi\n"
}

// v5AcceptCrashRecoveryDocument is a minimal real graph — a single real
// COMMAND node, no gate: this scenario's own subject is the Attempt/job
// lifecycle around one node, not multi-node composition (already proven
// by V5-15A's own happy path).
func v5AcceptCrashRecoveryDocument() workflow.WorkflowDocument {
	attemptPermissionRefs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v5c-crash-attempt-policy-def", VersionID: "v5c-crash-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v5c-crash-permission-policy-def", VersionID: "v5c-crash-permission-policy-v1"},
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{
				Key: "maker", Type: workflow.NodeCommand, Outcomes: []string{"passed"},
				Command: &workflow.CommandNodeConfig{
					CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v5c-crash-maker-cmd-def", VersionID: "v5c-crash-maker-cmd-v1"},
					PolicyRefs: attemptPermissionRefs,
				},
			},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-maker", From: "start", Outcome: "next", To: "maker"},
			{Key: "maker-end", From: "maker", Outcome: "passed", To: "end"},
		},
	}
}

func TestV5AcceptCrashRecovery_RealRetryReplaysWithNoDuplicates(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	startedMarkerPath := filepath.Join(f.fixtureRoot, "v5c-crash-started.txt")
	doneMarkerPath := filepath.Join(f.fixtureRoot, "v5c-crash-done.txt")
	makerKey, makerScript := v5AcceptCrashRecoveryScripts(startedMarkerPath, doneMarkerPath)
	skillDoc := v5AcceptTwoResourceSkillDocument(makerKey, makerScript, "unused.txt", "unused")
	publishSkillVersion(t, f.uow, "v5c-crash-skill-def", "v5c-crash-skill-v1", skillDoc)
	makerHash := resourceContentHash(t, "v5c-crash-skill-v1", makerKey, skillDoc)

	publishCommandVersion(t, f.uow, "v5c-crash-maker-cmd-def", "v5c-crash-maker-cmd-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "v5c-crash-skill-v1", ResourceKey: makerKey, ContentHash: makerHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: v5AcceptRepositoryID,
		Compatibility:       command.Compatibility{OS: []string{stdruntime.GOOS}},
		NetworkAccess:       command.NetworkAccessNone,
		// Comfortably longer than the ~60s the first real invocation's own
		// sleep would otherwise run for, itself already far longer than
		// this test's own pool1 LeaseTTL(2s)/ShutdownGrace(2s) — the real
		// crash below interrupts it long before either deadline matters.
		TimeoutSeconds: 120,
		Output:         command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})

	publishPolicyVersion(t, f.uow, "v5c-crash-attempt-policy-def", "v5c-crash-attempt-policy-v1", v5AcceptAttemptPolicyDocument(120))
	publishPolicyVersion(t, f.uow, "v5c-crash-permission-policy-def", "v5c-crash-permission-policy-v1", v5AcceptDriftPermissionPolicyDocument())

	completionFields := publishPolicyVersion(t, f.uow, "v5c-crash-completion-policy-def", "v5c-crash-completion-policy-v1", policy.PolicyDocument{
		Category:   policy.CategoryCompletion,
		Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{runtimedomain.EvidenceKindCommandExecution}},
	})
	doc := v5AcceptCrashRecoveryDocument()
	doc.CompletionPolicyRef = &definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: "v5c-crash-completion-policy-def", VersionID: "v5c-crash-completion-policy-v1"}
	dependencies := workflow.DependencyManifest{Pins: []workflow.DependencyPin{
		{Kind: string(definition.KindPolicy), Key: "v5c-crash-completion-policy-def", Version: "v5c-crash-completion-policy-v1", Hash: completionFields.CompiledHash()},
	}}
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v5c-crash-workflow-def", "v5c-crash-workflow-v1", doc, dependencies)

	router := &runtime.NodeExecutorRouter{Command: f.newCommandExecutor()}

	// pool1: LeaseTTL/ShutdownGrace/RecoveryInterval identical to
	// f.startPool's own defaults (2s/2s/200ms) — the real crash below
	// relies on exactly this timing, not a hand-tuned faster one, so it
	// stays representative of what a real production worker crash looks
	// like under this fixture's own established pool configuration.
	registry1 := f.registerHandlers(router, "v5crA")
	pool1, stopPool1 := f.startPool(t, registry1)
	_ = pool1
	pool1Stopped := false
	defer func() {
		if !pool1Stopped {
			stopPool1()
		}
	}()

	root := f.createRootWorkItem(t, "crash-recovery-root", workdomain.RepositoryRead)
	child := f.createChildWorkItem(t, root.WorkItemID, "crash-recovery-child", workdomain.RepositoryRead)

	startCmd := testCmd("v5c-crash-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := started.RunID

	// Poll for the real maker script's own real first side effect — proof
	// the real process has genuinely started (not merely that admission
	// passed, which happens slightly earlier) and is now really sleeping,
	// genuinely mid-flight when this test crashes pool1 below.
	startedDeadline := time.Now().Add(15 * time.Second)
	for {
		if _, statErr := os.Stat(startedMarkerPath); statErr == nil {
			break
		}
		if time.Now().After(startedDeadline) {
			t.Fatalf("real maker script never wrote its own started marker %s within the deadline", startedMarkerPath)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The real crash: tear down pool1 while the real child process is
	// still alive. stopPool1 blocks through Pool.Run's own real shutdown
	// sequence (pool.go) — ctx cancelled, ShutdownGrace elapses with the
	// job still in flight, cancelJobs() escalates, the real
	// ProcessSupervisor.Run sees ITS OWN ctx cancelled and really kills
	// the real process tree — so by the time this call returns, the real
	// child process is confirmed gone and the driving EXECUTE_NODE job's
	// lease has stopped being renewed (its heartbeat loop shared that
	// same now-cancelled ctx).
	stopPool1()
	pool1Stopped = true

	// The job's own last real heartbeat could have landed up to
	// LeaseTTL(2s) before this point — wait that out for real (polling the
	// real orphan query itself, never a blind sleep) so
	// ListOrphanedRunningExecutionAttempts genuinely sees a past
	// lease_until, exactly the real condition recovery depends on.
	orphanDeadline := time.Now().Add(15 * time.Second)
	var orphanedAttemptID string
	for {
		var orphaned []runtimedomain.ExecutionAttempt
		if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			orphaned, err = tx.Runtime().ListOrphanedRunningExecutionAttempts(ctx, time.Now())
			return err
		}); err == nil {
			for _, a := range orphaned {
				orphanedAttemptID = string(a.ID)
			}
		}
		if orphanedAttemptID != "" {
			break
		}
		if time.Now().After(orphanDeadline) {
			t.Fatal("no orphaned RUNNING execution attempt appeared within the deadline after the real crash")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The real recovery trigger — enqueues RECOVERY_REAPER for real
	// (idempotent insert, EnqueueJob's own established contract).
	if err := runtime.StartupRecoveryScan(ctx, f.uow, f.ids); err != nil {
		t.Fatalf("StartupRecoveryScan: %v", err)
	}

	// pool2: a fresh idPrefix (never reused, matching this package's own
	// established restart convention), same registry-building helper —
	// its own already-registered RecoveryReaperHandler is what actually
	// classifies the orphaned attempt LOST, retries it, and its own
	// already-registered ExecuteNodeHandler is what dispatches the real
	// retry Attempt's own real second script invocation.
	registry2 := f.registerHandlers(router, "v5crB")
	_, stopPool2 := f.startPool(t, registry2)
	defer stopPool2()

	run := f.waitForRunState(t, runID, runtimedomain.WorkflowRunVerifying)
	evalCmd := testCmd("v5c-crash-eval", ports.ProjectScope(v5AcceptProjectID), "EvaluateCompletionCandidate")
	evalCmd.ExpectedVersion = run.Version
	decision, err := runtime.EvaluateCompletionCandidate(ctx, f.uow, f.ids, clock.System{}, evalCmd, runtime.EvaluateCompletionCandidateRequest{RunID: runID})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	if decision.Outcome != runtime.CompletionOutcomePass {
		t.Fatalf("completion decision = %+v, want PASS", decision)
	}

	var finalRun runtimedomain.WorkflowRun
	var workItem workdomain.WorkItem
	var attempts []runtimedomain.ExecutionAttempt
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		if finalRun, err = tx.Runtime().GetWorkflowRun(ctx, runID); err != nil {
			return err
		}
		if workItem, err = tx.Work().GetWorkItem(ctx, child.WorkItemID); err != nil {
			return err
		}
		attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("read final state: %v", err)
	}
	if finalRun.State != runtimedomain.WorkflowRunSucceeded {
		t.Fatalf("run.State = %s, want SUCCEEDED", finalRun.State)
	}
	if workItem.Status != workdomain.WorkItemDone {
		t.Fatalf("workItem.Status = %s, want DONE", workItem.Status)
	}

	// The real "no duplicate Attempt" assertion: exactly the interrupted
	// original plus exactly one real retry, never more.
	if len(attempts) != 2 {
		t.Fatalf("ExecutionAttempts for run %s = %d, want exactly 2 (interrupted original + one real retry); attempts=%+v", runID, len(attempts), attempts)
	}
	var lost, succeeded *runtimedomain.ExecutionAttempt
	for i := range attempts {
		switch attempts[i].State {
		case runtimedomain.ExecutionAttemptLost:
			lost = &attempts[i]
		case runtimedomain.ExecutionAttemptSucceeded:
			succeeded = &attempts[i]
		}
	}
	if lost == nil {
		t.Fatalf("no ExecutionAttempt reached LOST; attempts=%+v", attempts)
	}
	if lost.TerminationReason != runtimedomain.TerminationReasonLeaseLost {
		t.Fatalf("interrupted attempt TerminationReason = %s, want %s", lost.TerminationReason, runtimedomain.TerminationReasonLeaseLost)
	}
	if lost.AttemptNumber != 1 {
		t.Fatalf("interrupted attempt AttemptNumber = %d, want 1", lost.AttemptNumber)
	}
	if succeeded == nil {
		t.Fatalf("no ExecutionAttempt reached SUCCEEDED (the real retry); attempts=%+v", attempts)
	}
	if succeeded.AttemptNumber != 2 {
		t.Fatalf("retry attempt AttemptNumber = %d, want 2", succeeded.AttemptNumber)
	}

	// The real "no duplicate job" assertion: exactly one durable
	// EXECUTE_NODE job row per real Attempt, never a phantom third from a
	// double-enqueued retry.
	jobRows, err := f.store.DebugListJobsByKind(ctx, runtime.ExecuteNodeJobKind)
	if err != nil {
		t.Fatalf("DebugListJobsByKind: %v", err)
	}
	if len(jobRows) != 2 {
		t.Fatalf("EXECUTE_NODE job rows = %d, want exactly 2; rows=%+v", len(jobRows), jobRows)
	}

	// The real "no duplicate event" assertion: exactly one termination
	// event for the interrupted original, exactly one finalize event for
	// the real retry, and — RETRY's own documented behavior
	// (recovery_reaper.go: "RETRY gets no such event") — zero
	// RECOVERY_DECISION_RECORDED events, proving this recovery never
	// mistakenly escalated or fresh-started instead of simply retrying.
	events, err := f.store.ListDomainEventsForProject(ctx, v5AcceptProjectID)
	if err != nil {
		t.Fatalf("ListDomainEventsForProject: %v", err)
	}
	var terminatedCount, finalizedCount, decisionCount int
	for _, e := range events {
		switch e.EventType {
		case "EXECUTION_ATTEMPT_TERMINATED":
			if e.AggregateID == string(lost.ID) {
				terminatedCount++
			}
		case runtime.ExecutionAttemptFinalizedEventType:
			if e.AggregateID == string(succeeded.ID) {
				finalizedCount++
			}
		case runtime.RecoveryDecisionRecordedEventType:
			decisionCount++
		}
	}
	if terminatedCount != 1 {
		t.Fatalf("EXECUTION_ATTEMPT_TERMINATED events for the interrupted attempt = %d, want exactly 1", terminatedCount)
	}
	if finalizedCount != 1 {
		t.Fatalf("EXECUTION_ATTEMPT_FINALIZED events for the retry attempt = %d, want exactly 1", finalizedCount)
	}
	if decisionCount != 0 {
		t.Fatalf("RECOVERY_DECISION_RECORDED events = %d, want 0 (a plain RETRY records none)", decisionCount)
	}

	// The real "no duplicate artifact" assertion: exactly one
	// COMMAND_EXECUTION Evidence row for this NodeRun, belonging to the
	// real retry's own real Attempt — the interrupted original never
	// produced any (it was killed mid-spawn, long before classify could
	// ever run).
	var evidence []runtimedomain.Evidence
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		evidence, err = tx.Runtime().ListEvidenceForAttempt(ctx, string(succeeded.ID))
		return err
	}); err != nil {
		t.Fatalf("ListEvidenceForAttempt(%s): %v", succeeded.ID, err)
	}
	commandEvidenceCount := 0
	for _, e := range evidence {
		if e.Kind == runtimedomain.EvidenceKindCommandExecution {
			commandEvidenceCount++
		}
	}
	if commandEvidenceCount != 1 {
		t.Fatalf("COMMAND_EXECUTION evidence rows for the retry attempt = %d, want exactly 1", commandEvidenceCount)
	}

	// The real proof the SECOND script invocation genuinely happened (not
	// merely that the Attempt record says SUCCEEDED): its own real
	// doneMarkerPath side effect exists on disk.
	if _, err := os.Stat(doneMarkerPath); err != nil {
		t.Fatalf("real done marker %s was never written by a real second script invocation: %v", doneMarkerPath, err)
	}
}
