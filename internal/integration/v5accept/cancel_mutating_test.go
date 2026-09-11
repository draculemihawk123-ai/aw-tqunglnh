// V5-15D — "Cancel during a mutating attempt" (docs/design/07-v5-
// execution-evidence.md V5-15's own "scope violation, checker write
// attempt, cancel giữa mutating attempt... xác minh workspace/revision
// không bị promote sau khi fence hoặc policy thắng" line; user's own
// binding 5-part PR split, full text in baocaov5checklist.md's own "V5-15"
// section and memory agent-kit-v5-15-acceptance-gate-contract.md).
//
// Proves V5-08C's own real cancellation-reconciliation path
// (agent_node_executor_cancellation.go's own handleMutatingCancellation,
// shared by CommandNodeExecutor per V5-09's own explicit reuse) really
// quarantines a repository workspace end to end when a genuinely mutating
// Attempt is cancelled mid-flight — and that the quarantined workspace can
// never be "promoted" afterward (this codebase's own concrete meaning of
// that word, confirmed by reading ports.WorkspaceLifecycle's own doc
// comment: ReleaseRepositoryWorkspace has "no path out of QUARANTINED...
// refused with ErrWorkspaceQuarantined", and RecreateRepositoryWorkspace —
// the only way past it — always produces a NEW generation, never reusing
// the quarantined one).
//
// Load-bearing real finding (confirmed by reading the code, not guessed):
// worker.ReconcileMutatingAttempt compares real Git HEAD SHAs
// (gitworktree.Provider.CaptureRevision), a value an UNCOMMITTED write
// never changes (WorkspaceDiff.Files sees uncommitted/untracked files via
// `git status`, but CurrentRevision is HEAD, entirely separate) — so this
// scenario's own real script must perform a REAL commit (moving HEAD) for
// the cancellation reconciliation to observe a real mutation at all; a
// merely-uncommitted write would reconcile CLEAN and never quarantine
// anything, silently defeating the scenario.
//
// Empirically confirmed while building this (not guessed): this test must
// NEVER poll the repository via a second, concurrent real `git` call
// (e.g. CaptureRevision) while the real script itself may still be running
// its own `git commit` — two real git processes racing the SAME real
// on-disk .git directory produced real, observed lock contention slow
// enough to starve this pool's own heartbeat past its LeaseTTL, reclaiming
// the still-in-flight job out from under itself. Waiting on a plain
// filesystem marker (os.Stat, the same reliable technique
// crash_recovery_test.go's own scenario already established) instead of a
// second concurrent git call avoids this entirely.
package v5accept

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// v5AcceptCancelMutatingScript is a real, cross-platform script that
// commits a real change for real (moving the repository's own real HEAD),
// writes committedMarkerPath (an absolute path OUTSIDE the repository,
// mirroring v5AcceptScripts' own doc comment for why: this test's own
// proof the real commit step finished, without ever needing a second,
// concurrent real git call against the same real working tree — see this
// file's own package doc comment), then sleeps far longer than this test
// needs to issue a real CancelRun against it. gitExecutable is the real,
// absolute path resolved once at test-generation time (exec.LookPath) —
// this real COMMAND node's own ProcessSpec.InheritedEnvironment is
// doc.EnvAllowlist, which this file's own CommandDocument never sets, so a
// bare `git`/`powershell` invocation would have no PATH to find it (the
// exact real gotcha crash_recovery_test.go's own doc comment already
// documents and fixes the identical way).
func v5AcceptCancelMutatingScript(gitExecutable, committedMarkerPath string) (key, script string) {
	if stdruntime.GOOS == "windows" {
		return "mutate-then-sleep.bat", "@echo off\r\n" +
			"echo mutated> mutated.txt\r\n" +
			"\"" + gitExecutable + "\" add -A\r\n" +
			"\"" + gitExecutable + "\" -c user.name=\"Agent Kit Test\" -c user.email=\"agent-kit@example.invalid\" commit -m \"v5d cancel-mid-mutation\"\r\n" +
			"echo committed> \"" + committedMarkerPath + "\"\r\n" +
			"\"C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe\" -NoProfile -Command \"Start-Sleep -Seconds 90\"\r\n" +
			"exit /b 0\r\n"
	}
	return "mutate-then-sleep.sh", "#!/bin/sh\n" +
		"echo mutated > mutated.txt\n" +
		"\"" + gitExecutable + "\" add -A\n" +
		"\"" + gitExecutable + "\" -c user.name=\"Agent Kit Test\" -c user.email=\"agent-kit@example.invalid\" commit -m \"v5d cancel-mid-mutation\"\n" +
		"echo committed > \"" + committedMarkerPath + "\"\n" +
		"sleep 90\n" +
		"exit 0\n"
}

func v5AcceptCancelMutatingDocument() workflow.WorkflowDocument {
	attemptPermissionRefs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v5d-cancel-attempt-policy-def", VersionID: "v5d-cancel-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v5d-cancel-permission-policy-def", VersionID: "v5d-cancel-permission-policy-v1"},
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{
				Key: "mutator", Type: workflow.NodeCommand, Outcomes: []string{"passed"},
				Command: &workflow.CommandNodeConfig{
					CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v5d-cancel-cmd-def", VersionID: "v5d-cancel-cmd-v1"},
					PolicyRefs: attemptPermissionRefs,
				},
			},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-mutator", From: "start", Outcome: "next", To: "mutator"},
			{Key: "mutator-end", From: "mutator", Outcome: "passed", To: "end"},
		},
	}
}

func TestV5AcceptCancelDuringMutatingAttempt_RealQuarantineNeverPromotes(t *testing.T) {
	gitExecutable, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required for this end-to-end test: %v", err)
	}

	f := newV5AcceptFixture(t)
	ctx := context.Background()

	committedMarkerPath := filepath.Join(f.fixtureRoot, "v5d-cancel-committed.txt")
	mutateKey, mutateScript := v5AcceptCancelMutatingScript(gitExecutable, committedMarkerPath)
	skillDoc := v5AcceptTwoResourceSkillDocument(mutateKey, mutateScript, "unused.txt", "unused")
	publishSkillVersion(t, f.uow, "v5d-cancel-skill-def", "v5d-cancel-skill-v1", skillDoc)
	mutateHash := resourceContentHash(t, "v5d-cancel-skill-v1", mutateKey, skillDoc)

	publishCommandVersion(t, f.uow, "v5d-cancel-cmd-def", "v5d-cancel-cmd-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "v5d-cancel-skill-v1", ResourceKey: mutateKey, ContentHash: mutateHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: v5AcceptRepositoryID,
		Compatibility:       command.Compatibility{OS: []string{stdruntime.GOOS}},
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      120,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})

	publishPolicyVersion(t, f.uow, "v5d-cancel-attempt-policy-def", "v5d-cancel-attempt-policy-v1", v5AcceptAttemptPolicyDocument(120))
	publishPolicyVersion(t, f.uow, "v5d-cancel-permission-policy-def", "v5d-cancel-permission-policy-v1", v5AcceptDriftPermissionPolicyDocument())

	doc := v5AcceptCancelMutatingDocument()
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v5d-cancel-workflow-def", "v5d-cancel-workflow-v1", doc, workflow.DependencyManifest{})

	router := &runtime.NodeExecutorRouter{Command: f.newCommandExecutor()}
	registry := f.registerHandlers(router, "v5dcm")
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	// A real, unrestricted WRITE grant — this scenario's own point is a
	// real, genuinely mutating attempt, not a scope-restriction question
	// (that is this file's own sibling, scope_violation_test.go).
	root := f.createRootWorkItem(t, "cancel-mutating-root", workdomain.RepositoryWrite)
	child := f.createChildWorkItem(t, root.WorkItemID, "cancel-mutating-child", workdomain.RepositoryWrite)

	startCmd := testCmd("v5d-cancel-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := started.RunID

	// Poll the real script's own real marker file — never a second,
	// concurrent real git call against the same real working tree the
	// script's own `git commit` may still be mid-flight against (see this
	// file's own package doc comment for the real contention that caused).
	committedDeadline := time.Now().Add(30 * time.Second)
	for {
		if _, statErr := os.Stat(committedMarkerPath); statErr == nil {
			break
		}
		if time.Now().After(committedDeadline) {
			t.Fatalf("real script never wrote its own committed marker %s within the deadline", committedMarkerPath)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The real cancel — a genuine RunCancellationIntent through the real
	// production command, never a hand-crafted row.
	if _, err := runtime.CancelRun(ctx, f.uow, f.ids, runtime.CancelRunRequest{
		RunID: runID, Actor: "operator-1", Reason: "v5-15d cancel during mutating attempt",
	}); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	// execute.go's own real cancellationPollInterval is 500ms — the
	// running job's own executor notices the durable cancellation intent,
	// cancels its own execCtx, and the real ProcessSupervisor.Run kills
	// the real still-sleeping process tree, exactly the same real
	// escalation mechanism crash_recovery_test.go's own scenario already
	// proved.
	run := f.waitForRunState(t, runID, runtimedomain.WorkflowRunCancelled)
	if run.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run.State = %s, want CANCELLED", run.State)
	}

	var attempts []runtimedomain.ExecutionAttempt
	var repoWorkspace workspace.RepositoryWorkspace
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		if err != nil {
			return err
		}
		repoWorkspace, err = tx.Work().GetRepositoryWorkspace(ctx, root.WorkspaceSetID, v5AcceptRepositoryID, 1)
		return err
	}); err != nil {
		t.Fatalf("read final state: %v", err)
	}

	var indeterminate *runtimedomain.ExecutionAttempt
	for i := range attempts {
		if attempts[i].State == runtimedomain.ExecutionAttemptIndeterminate {
			indeterminate = &attempts[i]
		}
	}
	if indeterminate == nil {
		t.Fatalf("no ExecutionAttempt reached INDETERMINATE; attempts=%+v", attempts)
	}
	if indeterminate.TerminationReason != runtimedomain.TerminationReasonOwnershipLostMutating {
		t.Fatalf("attempt.TerminationReason = %s, want %s", indeterminate.TerminationReason, runtimedomain.TerminationReasonOwnershipLostMutating)
	}

	// The real "never promoted" assertion: the repository workspace this
	// mutating attempt held really ended up QUARANTINED — this codebase's
	// own permanent, one-way state for exactly this situation.
	if repoWorkspace.State != workspace.RepositoryWorkspaceQuarantined {
		t.Fatalf("repositoryWorkspace.State = %s, want QUARANTINED", repoWorkspace.State)
	}

	// And really can never be released (promoted) afterward — a real call
	// against the real, still-current version, real production port.
	releaseErr := f.store.ReleaseRepositoryWorkspace(ctx, ports.ReleaseRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: repoWorkspace.ID, ExpectedVersion: repoWorkspace.Version,
		EventID: "v5d-cancel-release-attempt", OccurredAt: time.Now().UTC(),
	})
	if releaseErr == nil {
		t.Fatal("ReleaseRepositoryWorkspace unexpectedly succeeded on a quarantined workspace — it must never be promotable")
	}
	if !errors.Is(releaseErr, ports.ErrWorkspaceQuarantined) {
		t.Fatalf("ReleaseRepositoryWorkspace returned %v, want ports.ErrWorkspaceQuarantined", releaseErr)
	}
}
