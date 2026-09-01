package spikeacceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// runSPK07Scenario closes SPK-07: an attempt scoped to WRITE user-service
// (and nothing in web-app) has a real, separate helper process — not the
// scenario's own goroutine — write into both repositories, exactly like a
// real agent/provider process would. Alpha has no OS-level mount isolation
// (docs/00-start-here.md item 10), so the helper genuinely succeeds at
// writing both files; post-execution diff enforcement, invoked through the
// real worker.Finalizer (not a hand-rolled check), must catch the
// out-of-scope write and never let the finalizer reach persistence.
func runSPK07Scenario(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
	started := time.Now().UTC()
	if sc.Binaries.SpikeHelper == "" {
		return SPKResult{}, fmt.Errorf("spk07: ScenarioBinaries.SpikeHelper is required")
	}
	var assertions []Assertion
	passed := true
	record := func(name string, ok bool, detail string) {
		assertions = append(assertions, Assertion{Name: name, Passed: ok, Detail: detail})
		if !ok {
			passed = false
		}
	}

	fixtureRoot, err := os.MkdirTemp("", "spk07-*")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk07: create temp dir: %w", err)
	}
	defer os.RemoveAll(fixtureRoot)

	writeRepositoryPath := filepath.Join(fixtureRoot, "sources", "user-service")
	writeBase, err := createFixtureGitRepository(writeRepositoryPath, "user-v0\n")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk07: create write-scoped repository: %w", err)
	}
	readRepositoryPath := filepath.Join(fixtureRoot, "sources", "web-app")
	readBase, err := createFixtureGitRepository(readRepositoryPath, "web-v0\n")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk07: create read-only repository: %w", err)
	}
	provider, err := gitworktree.New(gitworktree.Config{Root: filepath.Join(fixtureRoot, "workspaces")})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk07: build provider: %w", err)
	}

	familyID := work.TaskFamilyID("spk07-family")
	setID := workspace.WorkspaceSetID("spk07-workspace-set")
	writeSpec := ports.ProvisionSpec{
		RepositoryID: project.RepositoryID("repo-user"), LocalRepository: writeRepositoryPath, BaseRef: writeBase,
		FamilyID: familyID, WorkspaceSetID: setID, Generation: 1,
	}
	readSpec := ports.ProvisionSpec{
		RepositoryID: project.RepositoryID("repo-web"), LocalRepository: readRepositoryPath, BaseRef: readBase,
		FamilyID: familyID, WorkspaceSetID: setID, Generation: 1,
	}
	writeHandle, err := provider.Provision(ctx, writeSpec)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk07: provision write workspace: %w", err)
	}
	readHandle, err := provider.Provision(ctx, readSpec)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk07: provision read workspace: %w", err)
	}
	writeDirectory, err := provider.WorkingDirectory(ctx, writeHandle)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk07: resolve write working directory: %w", err)
	}
	readDirectory, err := provider.WorkingDirectory(ctx, readHandle)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk07: resolve read working directory: %w", err)
	}

	helper := exec.Command(sc.Binaries.SpikeHelper,
		filepath.Join(writeDirectory, "service.txt")+"=changed-in-scope\n",
		filepath.Join(readDirectory, "service.txt")+"=changed-out-of-scope\n",
	)
	if output, err := helper.CombinedOutput(); err != nil {
		return SPKResult{}, fmt.Errorf("spk07: spike-helper write: %w: %s", err, output)
	}

	writeBaseRevision := workspace.Revision{RepositoryID: writeSpec.RepositoryID, VCSObjectID: writeBase, WorkspaceGeneration: 1}
	readBaseRevision := workspace.Revision{RepositoryID: readSpec.RepositoryID, VCSObjectID: readBase, WorkspaceGeneration: 1}
	writeDiff, err := provider.Diff(ctx, writeHandle, writeBaseRevision)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk07: diff write workspace: %w", err)
	}
	readDiff, err := provider.Diff(ctx, readHandle, readBaseRevision)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk07: diff read workspace: %w", err)
	}
	record("the helper process genuinely wrote into both repositories",
		len(writeDiff.Files) == 1 && len(readDiff.Files) == 1, fmt.Sprintf("write=%d read=%d", len(writeDiff.Files), len(readDiff.Files)))

	scope, err := work.NewRepositoryScope(
		familyID, 1, writeSpec.RepositoryID, work.RepositoryWrite, nil,
		"spk07 fixture", "spk07-scenario", started,
	)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk07: build write scope: %w", err)
	}

	validateErr := scopeguard.ValidateDiffs([]work.RepositoryScope{scope}, []ports.WorkspaceDiff{writeDiff, readDiff})
	record("post-execution diff enforcement rejects the out-of-scope write",
		errors.Is(validateErr, scopeguard.ErrScopeViolation), fmt.Sprintf("error=%v", validateErr))

	persistence := &spk07RecordingPersistence{}
	finalizer, err := worker.NewFinalizer(persistence)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk07: build finalizer: %w", err)
	}
	_, finalizeErr := finalizer.Finalize(ctx, worker.FinalizationInput{
		Finalization: ports.WorkerWorkflowRunFinalization{
			Transition: ports.WorkflowRunTransition{
				RunID: "spk07-run", ExpectedState: domainruntime.WorkflowRunRunning, ExpectedVersion: 1,
				NextState: domainruntime.WorkflowRunSucceeded, SharedState: json.RawMessage(`{}`),
				OccurredAt: started,
			},
			JobLease: ports.JobLease{JobID: "spk07-job", Owner: "spk07-worker", Token: 1},
			EventID:  "spk07-event", CorrelationID: "spk07",
		},
		EffectiveScopes: []work.RepositoryScope{scope},
		Diffs:           []ports.WorkspaceDiff{writeDiff, readDiff},
	})
	record("the real Finalizer rejects the run and never reaches persistence",
		errors.Is(finalizeErr, scopeguard.ErrScopeViolation) && persistence.finalizationCalls == 0,
		fmt.Sprintf("error=%v calls=%d", finalizeErr, persistence.finalizationCalls))

	workspaceSetArtifact, err := sc.Bundle.PutJSON("workspace/workspace-set.json", map[string]any{
		"familyId": familyID, "writeRepository": writeSpec.RepositoryID, "readRepository": readSpec.RepositoryID,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk07: write workspace-set evidence: %w", err)
	}
	diffArtifact, err := sc.Bundle.Put("workspace/diffs/repo-web.patch", readDiff.Patch)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk07: write diff evidence: %w", err)
	}

	return SPKResult{
		Passed:     passed,
		Assertions: assertions,
		Correlation: CorrelationIDs{
			ProjectID: "spk07-project", FamilyID: string(familyID), RepositoryID: string(readSpec.RepositoryID), Revision: readBase,
		},
		Platform: Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Timing:   Timing{StartedAt: started, EndedAt: time.Now().UTC()},
		Artifacts: []ArtifactRef{
			{Kind: ArtifactKindWorkspace, Artifact: workspaceSetArtifact},
			{Kind: ArtifactKindWorkspace, Artifact: diffArtifact},
		},
	}, nil
}

// spk07RecordingPersistence is a minimal ports.WorkflowPersistence fake: the
// SQLite-backed CAS/fencing behavior behind FinalizeWorkflowRun is proven
// elsewhere (SPK-09/SPK-10 scenarios). What this scenario needs to prove is
// that the real Finalizer never calls persistence at all when scopeguard
// rejects the diff.
type spk07RecordingPersistence struct {
	finalizationCalls int
}

func (*spk07RecordingPersistence) PublishWorkflowVersion(context.Context, workflow.WorkflowDefinition, workflow.WorkflowVersion) (workflow.WorkflowVersion, error) {
	return workflow.WorkflowVersion{}, errors.New("not used")
}

func (*spk07RecordingPersistence) LoadWorkflowVersion(context.Context, workflow.WorkflowVersionID) (workflow.WorkflowVersion, error) {
	return workflow.WorkflowVersion{}, errors.New("not used")
}

func (*spk07RecordingPersistence) StartWorkflowRun(context.Context, domainruntime.WorkflowRun) error {
	return errors.New("not used")
}

func (*spk07RecordingPersistence) LoadWorkflowRun(context.Context, domainruntime.WorkflowRunID) (domainruntime.WorkflowRun, error) {
	return domainruntime.WorkflowRun{}, errors.New("not used")
}

func (*spk07RecordingPersistence) CompareAndSwapWorkflowRun(context.Context, ports.WorkflowRunTransition) (domainruntime.WorkflowRun, error) {
	return domainruntime.WorkflowRun{}, errors.New("not used")
}

func (*spk07RecordingPersistence) StoreContextSnapshot(context.Context, project.ProjectID, domainruntime.ContextSnapshot) (domainruntime.ContextSnapshot, error) {
	return domainruntime.ContextSnapshot{}, errors.New("not used")
}

func (*spk07RecordingPersistence) LoadContextSnapshot(context.Context, domainruntime.ContextSnapshotID) (domainruntime.ContextSnapshot, error) {
	return domainruntime.ContextSnapshot{}, errors.New("not used")
}

func (*spk07RecordingPersistence) StoreCheckpoint(context.Context, domainruntime.Checkpoint) (domainruntime.Checkpoint, error) {
	return domainruntime.Checkpoint{}, errors.New("not used")
}

func (*spk07RecordingPersistence) LoadLatestCheckpoint(context.Context, domainruntime.ExecutionAttemptID) (domainruntime.Checkpoint, error) {
	return domainruntime.Checkpoint{}, errors.New("not used")
}

func (p *spk07RecordingPersistence) FinalizeWorkflowRun(context.Context, ports.WorkerWorkflowRunFinalization) (domainruntime.WorkflowRun, error) {
	p.finalizationCalls++
	return domainruntime.WorkflowRun{}, nil
}

var _ ports.WorkflowPersistence = (*spk07RecordingPersistence)(nil)
