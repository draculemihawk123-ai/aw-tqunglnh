// V9-06 — "Chạy lại WorkItem sau run không thành công"
// (docs/design/12-v9-harness-alignment.md V9-06; ADR-033 in
// docs/architecture/02-architecture-decisions.md; gap G6 in
// docs/harness-engineering/15-doi-chieu-v9.md).
//
// Before V9-06 a failed run left its WorkItem ACTIVE, which `run start` refuses
// (it needs READY): the only way to try again was to cancel the WorkItem and
// create a new one with a contract copied from the old, splitting one piece of
// work's history, messages and evidence over several WorkItems. The scenario
// here drives the whole loop on ONE WorkItem with the real thing end to end — a
// real COMMAND process that fails the first time it runs, real evaluator
// processes, a real gitworktree.Provider, a real sqlite store, a real worker
// pool:
//
//	run 1: COMMAND exits 1 -> the run FAILED, a RUN_FAILED blocker, the WorkItem BLOCKED
//	`run start` is refused (not READY); the blocker cannot be waived
//	resolve (RESOLVED) -> READY; the failed run's worktree changes are still there
//	run 2 (the same pinned workflow version): COMMAND exits 0 -> gate PASS -> VERIFYING
//	release + completion -> the WorkItem is DONE — the first run's FAILED
//	evidence does not count against it
//
// the messages written before and between the runs are in the second run's
// context; afterwards the task detail lists both runs, the run count is 2, and
// each run has its own timeline.
//
// The scripts have .bat twins built from batch builtins only.
package v5accept

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/kanban"
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	appwork "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	clirun "github.com/taQuangLing/agent-workflow/internal/delivery/cli/run"
	cliworkitem "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitem"
	cliworkitemblocker "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitemblocker"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	messagedomain "github.com/taQuangLing/agent-workflow/internal/domain/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

const (
	// v9rGateEvidenceKey is the one criterion of the gate.
	v9rGateEvidenceKey = "REPAIRED"
	// v9rFailureStderr is what the first run's command writes to stderr.
	v9rFailureStderr = "FAIL: TestAdd want 3 got 2"
	// v9rLeftover is the file the failed run leaves in the repository
	// worktree: ADR-033 decision 6, nothing resets it.
	v9rLeftover = "partial.txt"
)

// v9rMakerScript fails the first time it runs — leaving v9rLeftover in its
// working directory (the repository worktree) and a flag file outside every
// repository — and succeeds ever after, writing the output the gate looks for.
// Shell builtins only.
func v9rMakerScript(flagPath, outputPath string) (key, script string) {
	if stdruntime.GOOS == "windows" {
		return "rerun-maker.bat", "@echo off\r\nif exist \"" + flagPath + "\" (\r\n  echo marker> \"" + outputPath + "\"\r\n  exit /b 0\r\n)\r\n" +
			"echo run> \"" + flagPath + "\"\r\necho partial> " + v9rLeftover + "\r\necho " + v9rFailureStderr + " 1>&2\r\nexit /b 1\r\n"
	}
	return "rerun-maker.sh", "#!/bin/sh\nif [ -f \"" + flagPath + "\" ]; then\n  echo marker > \"" + outputPath + "\"\n  exit 0\nfi\n" +
		"echo run > \"" + flagPath + "\"\necho partial > " + v9rLeftover + "\necho '" + v9rFailureStderr + "' >&2\nexit 1\n"
}

// v9rGateScript reports the criterion PASS once the output exists.
func v9rGateScript(outputPath string) (key, script string) {
	pass := `{"` + v9rGateEvidenceKey + `":{"verdict":"PASS"}}`
	fail := `{"` + v9rGateEvidenceKey + `":{"verdict":"FAIL"}}`
	if stdruntime.GOOS == "windows" {
		return "rerun-gate.bat", "@echo off\r\nif exist \"" + outputPath + "\" (\r\n  echo " + pass + "\r\n) else (\r\n  echo " + fail + "\r\n)\r\nexit /b 0\r\n"
	}
	return "rerun-gate.sh", "#!/bin/sh\nif [ -f \"" + outputPath + "\" ]; then\n  echo '" + pass + "'\nelse\n  echo '" + fail + "'\nfi\nexit 0\n"
}

// createPinnedChildWorkItem is createChildWorkItem with a contract pinning
// workflowVersionID, the way an operator creates a WorkItem that must keep
// running one workflow version (ADR-033 decision 4); then READY as every
// fixture WorkItem is.
func (f *v5AcceptFixture) createPinnedChildWorkItem(t *testing.T, parentWorkItemID, title, workflowVersionID string) appwork.CreateChildWorkItemResult {
	t.Helper()
	ctx := context.Background()
	result, err := appwork.CreateChildWorkItem(ctx, f.uow, f.ids, testCmd("v5a-child-"+title, ports.ProjectScope(v5AcceptProjectID), "CreateChildWorkItem"), appwork.CreateChildWorkItemRequest{
		ParentWorkItemID: parentWorkItemID, Title: title, ParentJoinPolicy: "v5-accept-child",
		EffectiveScope: []appwork.ScopeGrantRequest{{
			RepositoryID: v5AcceptRepositoryID, Access: string(workdomain.RepositoryWrite), Reason: "v9-06 acceptance",
		}},
		Contract: &appwork.WorkItemContractRequest{SchemaVersion: 1, WorkflowVersionID: workflowVersionID},
	})
	if err != nil {
		t.Fatalf("CreateChildWorkItem(%s): %v", title, err)
	}
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: result.WorkItemID, ExpectedStatus: workdomain.WorkItemBacklog, ExpectedVersion: 1,
			NextStatus: workdomain.WorkItemReady,
		})
		return err
	}); err != nil {
		t.Fatalf("force work item %s READY: %v", result.WorkItemID, err)
	}
	return result
}

func (f *v5AcceptFixture) workItemOf(t *testing.T, workItemID string) workdomain.WorkItem {
	t.Helper()
	ctx := context.Background()
	var item workdomain.WorkItem
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		item, err = tx.Work().GetWorkItem(ctx, workItemID)
		return err
	}); err != nil {
		t.Fatalf("GetWorkItem(%s): %v", workItemID, err)
	}
	return item
}

func (f *v5AcceptFixture) blockersOf(t *testing.T, workItemID string) []workdomain.WorkItemBlocker {
	t.Helper()
	ctx := context.Background()
	var blockers []workdomain.WorkItemBlocker
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		blockers, err = tx.Work().ListWorkItemBlockersForWorkItem(ctx, workItemID)
		return err
	}); err != nil {
		t.Fatalf("ListWorkItemBlockersForWorkItem(%s): %v", workItemID, err)
	}
	return blockers
}

func (f *v5AcceptFixture) startRun(workItemID, workflowVersionID, key string) (runtime.StartWorkflowRunResult, error) {
	return runtime.StartWorkflowRun(context.Background(), f.uow, f.ids, testCmd(key, ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun"), runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: workItemID, WorkflowVersionID: workflowVersionID,
	})
}

func (f *v5AcceptFixture) run(t *testing.T, runID string) runtimedomain.WorkflowRun {
	t.Helper()
	ctx := context.Background()
	var run runtimedomain.WorkflowRun
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		run, err = tx.Runtime().GetWorkflowRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("GetWorkflowRun(%s): %v", runID, err)
	}
	return run
}

// runEvidence is every Evidence row of every attempt of runID.
func (f *v5AcceptFixture) runEvidence(t *testing.T, runID string) []runtimedomain.Evidence {
	t.Helper()
	ctx := context.Background()
	var rows []runtimedomain.Evidence
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		if err != nil {
			return err
		}
		for _, a := range attempts {
			evidence, err := tx.Runtime().ListEvidenceForAttempt(ctx, string(a.ID))
			if err != nil {
				return err
			}
			rows = append(rows, evidence...)
		}
		return nil
	}); err != nil {
		t.Fatalf("read evidence of run %s: %v", runID, err)
	}
	return rows
}

// appendMessage appends one USER message to workItemID through the real
// command and returns its id.
func (f *v5AcceptFixture) appendMessage(t *testing.T, workItemID, key, text string) string {
	t.Helper()
	result, err := appmessage.AppendMessage(
		context.Background(), f.uow, f.artifacts, f.ids, clock.System{},
		testCmd(key, ports.ProjectScope(v5AcceptProjectID), "AppendMessage"),
		appmessage.AppendMessageRequest{
			ProjectID: v5AcceptProjectID, WorkItemID: workItemID, Role: messagedomain.RoleUser,
			Content: []byte(text), ContentType: "text/plain",
		},
	)
	if err != nil {
		t.Fatalf("AppendMessage(%s): %v", key, err)
	}
	return result.MessageID
}

// attemptOfNode is the (only) ExecutionAttempt of nodeKey in runID.
func (f *v5AcceptFixture) attemptOfNode(t *testing.T, runID, nodeKey string) runtimedomain.ExecutionAttempt {
	t.Helper()
	history := readV9fHistory(t, f, runID)
	nodeRuns := history.byKey(nodeKey)
	if len(nodeRuns) != 1 {
		t.Fatalf("run %s has %d activations of %s, want 1", runID, len(nodeRuns), nodeKey)
	}
	return history.onlyAttempt(t, nodeRuns[0])
}

// snapshotMessageIDs are the message ids in attemptID's context snapshot.
func (f *v5AcceptFixture) snapshotMessageIDs(t *testing.T, attemptID string) []string {
	t.Helper()
	ctx := context.Background()
	var ids []string
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		snapshot, err := tx.ContextSnapshots().GetSnapshotByAttemptID(ctx, attemptID)
		if err != nil {
			return err
		}
		for _, ref := range snapshot.MessageRefs {
			ids = append(ids, ref.MessageID)
		}
		return nil
	}); err != nil {
		t.Fatalf("read the context snapshot of attempt %s: %v", attemptID, err)
	}
	return ids
}

func containsAll(have []string, want ...string) bool {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func (f *v5AcceptFixture) domainEventCount(t *testing.T, eventType string) int {
	t.Helper()
	events, err := f.store.ListDomainEventsForProject(context.Background(), v5AcceptProjectID)
	if err != nil {
		t.Fatalf("ListDomainEventsForProject: %v", err)
	}
	count := 0
	for _, e := range events {
		if e.EventType == eventType {
			count++
		}
	}
	return count
}

// TestV9AcceptRerunWorkItem_FailResolveStartDoneOnTheSameWorkItem is the V9-06
// "Verify": fail -> resolve -> run start -> DONE on the same WorkItem, both runs
// visible, the run count 2.
func TestV9AcceptRerunWorkItem_FailResolveStartDoneOnTheSameWorkItem(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	// -- definitions: a COMMAND that fails once then passes, a gate, a
	// completion policy requiring both a passing COMMAND_EXECUTION and the
	// gate's criterion --
	flagPath := filepath.Join(f.fixtureRoot, "v9r-failed-once.flag")
	outputPath := filepath.Join(f.fixtureRoot, "v9r-output.txt")
	makerKey, makerScript := v9rMakerScript(flagPath, outputPath)
	gateKey, gateScript := v9rGateScript(outputPath)
	skillDoc := v5AcceptTwoResourceSkillDocument(makerKey, makerScript, gateKey, gateScript)
	publishSkillVersion(t, f.uow, "v9r-skill-def", "v9r-skill-v1", skillDoc)
	osCompat := command.Compatibility{OS: []string{stdruntime.GOOS}}
	commandDoc := func(key string) command.CommandDocument {
		return command.CommandDocument{
			Executable:          command.ExecutableRef{OwnerVersionID: "v9r-skill-v1", ResourceKey: key, ContentHash: resourceContentHash(t, "v9r-skill-v1", key, skillDoc)},
			Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
			CwdRepositoryTarget: v5AcceptRepositoryID,
			Compatibility:       osCompat,
			NetworkAccess:       command.NetworkAccessNone,
			TimeoutSeconds:      120,
			Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
		}
	}
	publishCommandVersion(t, f.uow, "v9r-maker-cmd-def", "v9r-maker-cmd-v1", commandDoc(makerKey))
	publishCommandVersion(t, f.uow, "v9r-gate-cmd-def", "v9r-gate-cmd-v1", commandDoc(gateKey))
	publishGateVersion(t, f.uow, "v9r-gate-def", "v9r-gate-v1", gate.GateDocument{
		CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v9r-gate-cmd-def", VersionID: "v9r-gate-cmd-v1"},
		Criteria:   []gate.Criterion{{Name: "repaired", EvidenceKey: v9rGateEvidenceKey}},
	})
	publishPolicyVersion(t, f.uow, "v9r-attempt-policy-def", "v9r-attempt-policy-v1", v5AcceptAttemptPolicyDocument(120))
	publishPolicyVersion(t, f.uow, "v9r-permission-policy-def", "v9r-permission-policy-v1", v5AcceptPermissionPolicyDocument())
	completionFields := publishPolicyVersion(t, f.uow, "v9r-completion-policy-def", "v9r-completion-policy-v1", policy.PolicyDocument{
		Category:   policy.CategoryCompletion,
		Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{runtimedomain.EvidenceKindCommandExecution, v9rGateEvidenceKey}},
	})
	refs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v9r-attempt-policy-def", VersionID: "v9r-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v9r-permission-policy-def", VersionID: "v9r-permission-policy-v1"},
	}
	doc := workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "test_a", Type: workflow.NodeCommand, Outcomes: []string{"passed"}, Command: &workflow.CommandNodeConfig{
				CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v9r-maker-cmd-def", VersionID: "v9r-maker-cmd-v1"},
				PolicyRefs: refs,
			}},
			{Key: "gate_b", Type: workflow.NodeMachineGate, Outcomes: []string{"passed"}, MachineGate: &workflow.MachineGateNodeConfig{
				GateRef:    definition.DependencyPin{Kind: definition.KindGate, DefinitionID: "v9r-gate-def", VersionID: "v9r-gate-v1"},
				PolicyRefs: refs,
			}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-test_a", From: "start", Outcome: "next", To: "test_a"},
			{Key: "test_a-gate_b", From: "test_a", Outcome: "passed", To: "gate_b"},
			{Key: "gate_b-end", From: "gate_b", Outcome: "passed", To: "end"},
		},
		CompletionPolicyRef: &definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: "v9r-completion-policy-def", VersionID: "v9r-completion-policy-v1"},
	}
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v9r-workflow-def", "v9r-workflow-v1", doc, workflow.DependencyManifest{Pins: []workflow.DependencyPin{
		{Kind: string(definition.KindPolicy), Key: "v9r-completion-policy-def", Version: "v9r-completion-policy-v1", Hash: completionFields.CompiledHash()},
	}})
	versionID := string(version.ID())

	router := &runtime.NodeExecutorRouter{Command: f.newCommandExecutor(), Gate: f.newGateExecutor()}
	registry := f.registerHandlers(router, "v9rh")
	// A generous lease: nothing here is about lease expiry.
	_, stopPool := f.startPoolWithConfig(t, registry, workerpool.Config{
		Concurrency: 1, Owner: "v9r-accept", LeaseTTL: 60 * time.Second,
		HeartbeatEvery: time.Second, PollInterval: 10 * time.Millisecond,
		ShutdownGrace: 5 * time.Second, RecoveryInterval: time.Second,
	})
	defer stopPool()

	root := f.createRootWorkItem(t, "v9r-root", workdomain.RepositoryWrite)
	child := f.createPinnedChildWorkItem(t, root.WorkItemID, "v9r-child", versionID)
	baseHandle := f.repositoryWorkspaceHandle(t, root.WorkspaceSetID, v5AcceptRepositoryID, 1)
	baseRevision, err := f.provider.CaptureRevision(ctx, baseHandle)
	if err != nil {
		t.Fatalf("CaptureRevision (base): %v", err)
	}
	workDir, err := f.provider.WorkingDirectory(ctx, baseHandle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}

	// A message before the first run: it belongs to the WorkItem, so it must
	// reach the second run's context too (ADR-033 decision 5).
	beforeFirst := f.appendMessage(t, child.WorkItemID, "v9r-msg-1", "please fix TestAdd")

	// -- run 1: the COMMAND exits 1; the run fails; the WorkItem is BLOCKED by a
	// RUN_FAILED blocker instead of staying ACTIVE --
	first, err := f.startRun(child.WorkItemID, versionID, "v9r-start-1")
	if err != nil {
		t.Fatalf("StartWorkflowRun (run 1): %v", err)
	}
	f.waitForRunState(t, first.RunID, runtimedomain.WorkflowRunFailed)

	item := f.workItemOf(t, child.WorkItemID)
	if item.Status != workdomain.WorkItemBlocked {
		t.Fatalf("work item status after the failed run = %s, want BLOCKED (it stayed ACTIVE before V9-06)", item.Status)
	}
	blockers := f.blockersOf(t, child.WorkItemID)
	if len(blockers) != 1 || blockers[0].Type != workdomain.BlockerRunFailed || blockers[0].State != workdomain.BlockerOpen ||
		blockers[0].SourceRunID != first.RunID || string(blockers[0].ID) != first.RunID+"-run-failed-blocker" {
		t.Fatalf("blockers = %+v, want exactly one OPEN RUN_FAILED %s-run-failed-blocker sourced from run 1", blockers, first.RunID)
	}
	if got := f.domainEventCount(t, runtime.WorkItemBlockedEventType); got != 1 {
		t.Fatalf("%s events = %d, want exactly 1", runtime.WorkItemBlockedEventType, got)
	}

	// The evidence of the failed run is there (G2) and stays on the WorkItem.
	firstEvidence := f.runEvidence(t, first.RunID)
	if len(firstEvidence) != 1 || firstEvidence[0].Kind != runtimedomain.EvidenceKindCommandExecution || firstEvidence[0].Verdict != runtimedomain.EvidenceVerdictFailed {
		t.Fatalf("evidence of run 1 = %+v, want one COMMAND_EXECUTION / FAILED row", firstEvidence)
	}
	if !strings.Contains(f.readArtifactText(t, firstEvidence[0].ArtifactReferences[0]), v9rFailureStderr) {
		t.Fatalf("the failed run's evidence artifact lacks the stderr %q", v9rFailureStderr)
	}

	// The operator writes down what they found, before resolving.
	afterFirst := f.appendMessage(t, child.WorkItemID, "v9r-msg-2", "the first run left partial.txt behind; keeping it")

	// What the operator types from here on goes through the CLI leaves
	// (`aw run start`, `aw blocker resolve`, `aw work-item detail`).
	runDeps := clirun.Dependencies{UOW: f.uow, IDs: f.ids}
	blockerDeps := cliworkitemblocker.Dependencies{UoW: f.uow, IDs: f.ids}
	cliStart := func(key string) (clirun.StartResult, error) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		args := []string{"--workflow-version-id", versionID, "--idempotency-key", key, child.WorkItemID}
		if err := clirun.Start(ctx, runDeps, args, &stdout, &stderr); err != nil {
			return clirun.StartResult{}, err
		}
		var envelope struct {
			Result clirun.StartResult `json:"result"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			t.Fatalf("decode `run start` output %q: %v", stdout.String(), err)
		}
		return envelope.Result, nil
	}
	cliResolve := func(mode string, extra ...string) (cliworkitemblocker.ResolveResult, error) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		args := append([]string{"--mode", mode, "--reason", "reviewed the failed run"}, extra...)
		args = append(args, string(blockers[0].ID))
		if err := cliworkitemblocker.Resolve(ctx, blockerDeps, args, &stdout, &stderr); err != nil {
			return cliworkitemblocker.ResolveResult{}, err
		}
		var result cliworkitemblocker.ResolveResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("decode `blocker resolve` output %q: %v", stdout.String(), err)
		}
		return result, nil
	}

	// `run start` is refused while the blocker is open; the blocker cannot be waived.
	if _, err := cliStart("v9r-start-blocked"); !errors.Is(err, runtime.ErrWorkItemNotReady) {
		t.Fatalf("`run start` while the RUN_FAILED blocker is open = %v, want ErrWorkItemNotReady", err)
	}
	if _, err := cliResolve("WAIVED", "--policy-grant-ref", "grant-1"); !errors.Is(err, runtime.ErrBlockerNotWaivable) {
		t.Fatalf("`blocker resolve --mode WAIVED` = %v, want ErrBlockerNotWaivable", err)
	}
	if item := f.workItemOf(t, child.WorkItemID); item.Status != workdomain.WorkItemBlocked {
		t.Fatalf("work item status after the refused start and waive = %s, want still BLOCKED", item.Status)
	}

	// The worktree keeps what the failed run left (ADR-033 decision 6).
	if _, err := os.Stat(filepath.Join(workDir, v9rLeftover)); err != nil {
		t.Fatalf("the failed run's change %s is gone from the worktree: %v", v9rLeftover, err)
	}

	// -- resolve -> READY --
	resolved, err := cliResolve("RESOLVED")
	if err != nil {
		t.Fatalf("`blocker resolve --mode RESOLVED`: %v", err)
	}
	if resolved.State != "RESOLVED" || !resolved.WorkItemUnblocked || resolved.WorkItemStatus != string(workdomain.WorkItemReady) {
		t.Fatalf("resolve result = %+v, want RESOLVED / unblocked / READY", resolved)
	}
	if _, err := os.Stat(filepath.Join(workDir, v9rLeftover)); err != nil {
		t.Fatalf("resolving reset the worktree: %s is gone: %v", v9rLeftover, err)
	}

	// -- run 2 on the SAME WorkItem, the SAME pinned version: the COMMAND passes --
	second, err := cliStart("v9r-start-2")
	if err != nil {
		t.Fatalf("`run start` (run 2): %v", err)
	}
	if second.RunID == "" || second.RunID == first.RunID || second.WorkItemID != child.WorkItemID {
		t.Fatalf("run 2 = %+v, want a new run of work item %s", second, child.WorkItemID)
	}
	// The same command re-issued (a retry after a crash) replays instead of
	// starting a third run.
	if replayed, err := cliStart("v9r-start-2"); err != nil || replayed.RunID != second.RunID {
		t.Fatalf("`run start` replay = %+v, %v, want the same run %s", replayed, err, second.RunID)
	}
	run := f.waitForRunState(t, second.RunID, runtimedomain.WorkflowRunVerifying)

	// Both messages — written before and between the runs — are in the second
	// run's context, next to nothing of the first run's evidence being counted.
	secondAttempt := f.attemptOfNode(t, second.RunID, "test_a")
	if refs := f.snapshotMessageIDs(t, string(secondAttempt.ID)); !containsAll(refs, beforeFirst, afterFirst) {
		t.Fatalf("message refs of run 2's context snapshot = %v, want both %s and %s", refs, beforeFirst, afterFirst)
	}

	// -- release (a real local commit of whatever the worktree holds) and
	// completion: PASS although run 1's evidence is FAILED --
	resultRevision, err := f.provider.CreateLocalCommit(ctx, ports.CreateLocalCommitRequest{
		Handle: baseHandle, Message: "v9-06 rerun", AuthorName: "Agent Kit Test", AuthorEmail: "agent-kit@example.invalid",
	})
	if err != nil {
		t.Fatalf("CreateLocalCommit: %v", err)
	}
	releaseResult, err := appwork.CreateReleaseSet(ctx, f.uow, f.ids, testCmd("v9r-release-create", ports.ProjectScope(v5AcceptProjectID), "CreateReleaseSet"), appwork.CreateReleaseSetRequest{
		ProjectID: v5AcceptProjectID, FamilyID: root.FamilyID,
		Repositories: []appwork.RepositoryReleaseRequest{{
			RepositoryID: v5AcceptRepositoryID, BaseVCSObjectID: baseRevision.VCSObjectID, ResultVCSObjectID: resultRevision.VCSObjectID,
			Verdict: string(gate.VerdictPass),
		}},
	})
	if err != nil {
		t.Fatalf("CreateReleaseSet: %v", err)
	}
	sealCmd := testCmd("v9r-release-seal", ports.ProjectScope(v5AcceptProjectID), "SealReleaseSet")
	sealCmd.ExpectedVersion = releaseResult.Version
	if _, err := appwork.SealReleaseSet(ctx, f.uow, sealCmd, appwork.SealReleaseSetRequest{ReleaseSetID: releaseResult.ReleaseSetID}); err != nil {
		t.Fatalf("SealReleaseSet: %v", err)
	}
	evalCmd := testCmd("v9r-eval", ports.ProjectScope(v5AcceptProjectID), "EvaluateCompletionCandidate")
	evalCmd.ExpectedVersion = run.Version
	decision, err := runtime.EvaluateCompletionCandidate(ctx, f.uow, f.ids, clock.System{}, evalCmd, runtime.EvaluateCompletionCandidateRequest{RunID: second.RunID})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	if decision.Outcome != runtime.CompletionOutcomePass {
		t.Fatalf("completion of run 2 = %+v, want PASS — run 1's FAILED evidence must not count against it", decision)
	}

	// -- DONE on the same WorkItem; one history: two runs, run count 2 --
	if item := f.workItemOf(t, child.WorkItemID); item.Status != workdomain.WorkItemDone {
		t.Fatalf("work item status = %s, want DONE", item.Status)
	}
	if got := f.run(t, first.RunID).State; got != runtimedomain.WorkflowRunFailed {
		t.Fatalf("run 1 state = %s, want it left FAILED", got)
	}
	if got := f.run(t, second.RunID).State; got != runtimedomain.WorkflowRunSucceeded {
		t.Fatalf("run 2 state = %s, want SUCCEEDED", got)
	}
	for _, id := range []string{first.RunID, second.RunID} {
		if got := f.run(t, id).WorkflowVersionID; got != version.ID() {
			t.Fatalf("run %s pins workflow version %s, want the WorkItem's pinned %s", id, got, version.ID())
		}
	}

	// The projection (the live consumer, run here by hand) counts both runs.
	if outcome, err := projection.ApplyBatch(ctx, f.uow, projection.NewCatalog(), projection.ApplyBatchRequest{
		ProjectID: v5AcceptProjectID, ProjectionName: projection.ProjectionName, Owner: "v9r", TTL: time.Minute, BatchSize: 100000,
		Now: time.Now().UTC(), IDs: idsource.Random{},
	}); err != nil || outcome.Poisoned {
		t.Fatalf("ApplyBatch = %+v, %v, want applied without poison", outcome, err)
	}
	var detailOut, detailErr bytes.Buffer
	if err := cliworkitem.RunWorkItemDetail(ctx, cliworkitem.Dependencies{UoW: f.uow, IDs: f.ids},
		[]string{"--project-id", v5AcceptProjectID, child.WorkItemID}, &detailOut, &detailErr); err != nil {
		t.Fatalf("`work-item detail`: %v (stderr %s)", err, detailErr.String())
	}
	var detail kanban.Detail
	if err := json.Unmarshal(detailOut.Bytes(), &detail); err != nil {
		t.Fatalf("decode `work-item detail` output %q: %v", detailOut.String(), err)
	}
	if detail.Card.RunCount != 2 {
		t.Fatalf("task detail card runCount = %d, want 2", detail.Card.RunCount)
	}
	if len(detail.Runs) != 2 || detail.Runs[0].RunID != first.RunID || detail.Runs[0].RunNumber != 1 || detail.Runs[0].State != "FAILED" ||
		detail.Runs[1].RunID != second.RunID || detail.Runs[1].RunNumber != 2 || detail.Runs[1].State != "SUCCEEDED" {
		t.Fatalf("task detail runs = %+v, want [run 1 FAILED, run 2 SUCCEEDED]", detail.Runs)
	}
	page, err := kanban.ListWorkItemKanban(ctx, f.uow, kanban.ListRequest{ProjectID: v5AcceptProjectID})
	if err != nil {
		t.Fatalf("ListWorkItemKanban: %v", err)
	}
	var card *kanban.Card
	for i := range page.Items {
		if page.Items[i].WorkItemID == child.WorkItemID {
			card = &page.Items[i]
		}
	}
	if card == nil || card.RunCount != 2 {
		t.Fatalf("board card = %+v, want runCount 2", card)
	}

	// Each run has its own timeline; the first shows the failed command, the
	// second the whole path to END.
	timeline := func(runID string) map[string]string {
		t.Helper()
		tl, err := runtime.GetRunTimeline(ctx, f.uow, redact.NewMatcher(), runID)
		if err != nil {
			t.Fatalf("GetRunTimeline(%s): %v", runID, err)
		}
		states := map[string]string{}
		for _, e := range tl.Entries {
			if e.Kind == runtime.TimelineEntryNodeRun {
				states[e.NodeKey] = e.NodeState
			}
		}
		return states
	}
	if states := timeline(first.RunID); states["test_a"] != string(runtimedomain.NodeRunFailed) || states["gate_b"] != "" {
		t.Fatalf("timeline of run 1 = %v, want test_a FAILED and no gate_b", states)
	}
	if states := timeline(second.RunID); states["test_a"] != string(runtimedomain.NodeRunSucceeded) ||
		states["gate_b"] != string(runtimedomain.NodeRunSucceeded) || states["end"] != string(runtimedomain.NodeRunSucceeded) {
		t.Fatalf("timeline of run 2 = %v, want test_a, gate_b and end SUCCEEDED", states)
	}

	// Nothing of this is lost across a restart.
	stopPool()
	f.restart(t)
	if item := f.workItemOf(t, child.WorkItemID); item.Status != workdomain.WorkItemDone {
		t.Fatalf("work item status after a restart = %s, want DONE", item.Status)
	}
	if got := len(f.blockersOf(t, child.WorkItemID)); got != 1 {
		t.Fatalf("blockers after a restart = %d, want 1", got)
	}
}
