package gitworktree

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

const (
	spk07HelperModeEnvironment = "AGENTKIT_SPK07_HELPER"
	spk07WriteFileEnvironment  = "AGENTKIT_SPK07_WRITE_FILE"
	spk07ReadFileEnvironment   = "AGENTKIT_SPK07_READ_FILE"
)

// TestSPK07ScopeEnforcementCatchesOutOfScopeWrite closes SPK-07 end to end:
// a real helper OS process writes into both a WRITE-scoped and a READ-only
// repository workspace of the same family; real Git diffs are captured for
// both via Provider.Diff; scopeguard.ValidateDiffs — invoked through the
// real worker.Finalizer, not a hand-rolled check — rejects the result
// because of the read-only repository's change, and the finalizer never
// reaches persistence, so that repository's current revision is never
// accepted anywhere.
//
// Alpha does not enforce OS-level mount isolation (docs/00-start-here.md
// item 10: the diff guard is not a sandbox); this test documents that
// limitation by having the helper process genuinely succeed at writing both
// files, and relying entirely on post-execution diff enforcement to catch
// it — not on the write being blocked.
func TestSPK07ScopeEnforcementCatchesOutOfScopeWrite(t *testing.T) {
	if os.Getenv(spk07HelperModeEnvironment) == "1" {
		runSPK07HelperProcess(t)
		return
	}

	fixtureRoot := t.TempDir()
	writeRepository, writeBase := createGitRepository(t, filepath.Join(fixtureRoot, "sources", "user-service"), "user-v0\n")
	readRepository, readBase := createGitRepository(t, filepath.Join(fixtureRoot, "sources", "web-app"), "web-v0\n")
	provider := newTestProvider(t, filepath.Join(fixtureRoot, "workspaces"))
	ctx := context.Background()

	familyID := work.TaskFamilyID("family-spk07")
	setID := workspace.WorkspaceSetID("workspace-set-spk07")
	writeSpec := ports.ProvisionSpec{
		RepositoryID: project.RepositoryID("repo-user"), LocalRepository: writeRepository, BaseRef: writeBase,
		FamilyID: familyID, WorkspaceSetID: setID, Generation: 1,
	}
	readSpec := ports.ProvisionSpec{
		RepositoryID: project.RepositoryID("repo-web"), LocalRepository: readRepository, BaseRef: readBase,
		FamilyID: familyID, WorkspaceSetID: setID, Generation: 1,
	}
	writeHandle, err := provider.Provision(ctx, writeSpec)
	if err != nil {
		t.Fatalf("provision write workspace: %v", err)
	}
	readHandle, err := provider.Provision(ctx, readSpec)
	if err != nil {
		t.Fatalf("provision read workspace: %v", err)
	}
	writePath, err := provider.workspacePath(writeHandle)
	if err != nil {
		t.Fatalf("resolve write workspace path: %v", err)
	}
	readPath, err := provider.workspacePath(readHandle)
	if err != nil {
		t.Fatalf("resolve read workspace path: %v", err)
	}

	writeBaseRevision := workspace.Revision{RepositoryID: writeSpec.RepositoryID, VCSObjectID: writeBase, WorkspaceGeneration: 1}
	readBaseRevision := workspace.Revision{RepositoryID: readSpec.RepositoryID, VCSObjectID: readBase, WorkspaceGeneration: 1}

	// A real helper OS process — not the test itself — tries to change both
	// repositories, exactly like a provider/agent process would.
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve current test executable: %v", err)
	}
	helper := exec.Command(executable, "-test.run=^TestSPK07ScopeEnforcementCatchesOutOfScopeWrite$", "-test.v", "-test.timeout=30s")
	helper.Env = append(os.Environ(),
		spk07HelperModeEnvironment+"=1",
		spk07WriteFileEnvironment+"="+filepath.Join(writePath, "service.txt"),
		spk07ReadFileEnvironment+"="+filepath.Join(readPath, "service.txt"),
	)
	if output, err := helper.CombinedOutput(); err != nil {
		t.Fatalf("scope-violating helper process failed: %v\n%s", err, output)
	}

	writeDiff, err := provider.Diff(ctx, writeHandle, writeBaseRevision)
	if err != nil {
		t.Fatalf("capture write-workspace diff: %v", err)
	}
	readDiff, err := provider.Diff(ctx, readHandle, readBaseRevision)
	if err != nil {
		t.Fatalf("capture read-workspace diff: %v", err)
	}
	if len(writeDiff.Files) != 1 || len(readDiff.Files) != 1 {
		t.Fatalf("expected the helper to touch exactly one file per repository: write=%#v read=%#v", writeDiff.Files, readDiff.Files)
	}
	// CurrentRevision tracks HEAD, not working-tree dirtiness: an uncommitted
	// write never moves it (see TestProviderIsolatesRootFamiliesAndCapturesDiff
	// for the same distinction). The change itself is evidenced by Files/Patch
	// above; committed-revision provenance is exercised by that existing test.

	scope, err := work.NewRepositoryScope(
		familyID, 1, writeSpec.RepositoryID, work.RepositoryWrite, nil,
		"spk07 fixture", "spk07-test", time.Date(2026, 8, 28, 17, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("build write scope: %v", err)
	}
	// repo-web deliberately has no scope entry at all: scopeguard.ValidateDiffs
	// only admits a change under an explicit WRITE grant, so "no grant" is
	// how a READ-only repository is represented here.

	if err := scopeguard.ValidateDiffs([]work.RepositoryScope{scope}, []ports.WorkspaceDiff{writeDiff, readDiff}); err == nil {
		t.Fatal("ValidateDiffs() accepted a diff outside the granted WRITE scope")
	} else if !errors.Is(err, scopeguard.ErrScopeViolation) || !strings.Contains(err.Error(), "repo-web") {
		t.Fatalf("ValidateDiffs() error = %v, want ErrScopeViolation naming repo-web", err)
	}

	// The same rejection happens through the real Finalizer, before
	// persistence is ever reached — the read-only repository's current
	// revision is never accepted as part of a completed run.
	persistence := &spk07RecordingPersistence{}
	finalizer, err := worker.NewFinalizer(persistence)
	if err != nil {
		t.Fatalf("build finalizer: %v", err)
	}
	_, err = finalizer.Finalize(ctx, worker.FinalizationInput{
		Finalization: ports.WorkerWorkflowRunFinalization{
			Transition: ports.WorkflowRunTransition{
				RunID:           "run-spk07",
				ExpectedState:   runtime.WorkflowRunRunning,
				ExpectedVersion: 2,
				NextState:       runtime.WorkflowRunSucceeded,
				SharedState:     json.RawMessage(`{}`),
				OccurredAt:      time.Date(2026, 8, 28, 17, 1, 0, 0, time.UTC),
			},
			JobLease:      ports.JobLease{JobID: "job-spk07", Owner: "worker-spk07", Token: 1},
			EventID:       "event-spk07",
			CorrelationID: "spk07",
		},
		EffectiveScopes: []work.RepositoryScope{scope},
		Diffs:           []ports.WorkspaceDiff{writeDiff, readDiff},
	})
	if !errors.Is(err, scopeguard.ErrScopeViolation) {
		t.Fatalf("Finalize() error = %v, want ErrScopeViolation", err)
	}
	if persistence.finalizationCalls != 0 {
		t.Fatalf("finalizer reached persistence despite a scope violation: calls=%d", persistence.finalizationCalls)
	}
}

// runSPK07HelperProcess plays a real agent/provider process attempting to
// touch both repositories — exactly the behavior scope enforcement must
// catch after the fact, since Alpha has no OS-level mount isolation to
// prevent it up front.
func runSPK07HelperProcess(t *testing.T) {
	t.Helper()
	writeFile := os.Getenv(spk07WriteFileEnvironment)
	readFile := os.Getenv(spk07ReadFileEnvironment)
	if writeFile == "" || readFile == "" {
		t.Fatal("spk07 helper requires both write and read target files")
	}
	if err := os.WriteFile(writeFile, []byte("changed-in-scope\n"), 0o600); err != nil {
		t.Fatalf("helper write in-scope file: %v", err)
	}
	if err := os.WriteFile(readFile, []byte("changed-out-of-scope\n"), 0o600); err != nil {
		t.Fatalf("helper write out-of-scope file: %v", err)
	}
}

// spk07RecordingPersistence is a minimal ports.WorkflowPersistence fake: the
// SQLite-backed CAS/fencing behavior behind FinalizeWorkflowRun is already
// proven by the crash/recovery tests elsewhere in this repository. What
// this test needs to prove is that the real Finalizer never calls it at all
// when scopeguard rejects the diff.
type spk07RecordingPersistence struct {
	finalizationCalls int
}

func (*spk07RecordingPersistence) PublishWorkflowVersion(context.Context, workflow.WorkflowDefinition, workflow.WorkflowVersion) (workflow.WorkflowVersion, error) {
	return workflow.WorkflowVersion{}, errors.New("not used")
}

func (*spk07RecordingPersistence) LoadWorkflowVersion(context.Context, workflow.WorkflowVersionID) (workflow.WorkflowVersion, error) {
	return workflow.WorkflowVersion{}, errors.New("not used")
}

func (*spk07RecordingPersistence) StartWorkflowRun(context.Context, runtime.WorkflowRun) error {
	return errors.New("not used")
}

func (*spk07RecordingPersistence) LoadWorkflowRun(context.Context, runtime.WorkflowRunID) (runtime.WorkflowRun, error) {
	return runtime.WorkflowRun{}, errors.New("not used")
}

func (*spk07RecordingPersistence) CompareAndSwapWorkflowRun(context.Context, ports.WorkflowRunTransition) (runtime.WorkflowRun, error) {
	return runtime.WorkflowRun{}, errors.New("not used")
}

func (*spk07RecordingPersistence) StoreContextSnapshot(context.Context, project.ProjectID, runtime.ContextSnapshot) (runtime.ContextSnapshot, error) {
	return runtime.ContextSnapshot{}, errors.New("not used")
}

func (*spk07RecordingPersistence) LoadContextSnapshot(context.Context, runtime.ContextSnapshotID) (runtime.ContextSnapshot, error) {
	return runtime.ContextSnapshot{}, errors.New("not used")
}

func (*spk07RecordingPersistence) StoreCheckpoint(context.Context, runtime.Checkpoint) (runtime.Checkpoint, error) {
	return runtime.Checkpoint{}, errors.New("not used")
}

func (*spk07RecordingPersistence) LoadLatestCheckpoint(context.Context, runtime.ExecutionAttemptID) (runtime.Checkpoint, error) {
	return runtime.Checkpoint{}, errors.New("not used")
}

func (p *spk07RecordingPersistence) FinalizeWorkflowRun(context.Context, ports.WorkerWorkflowRunFinalization) (runtime.WorkflowRun, error) {
	p.finalizationCalls++
	return runtime.WorkflowRun{}, nil
}
