package spikeacceptance

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// runSPK06Scenario closes SPK-06 against a real local Git provider: two root
// TaskFamilies provisioning the same repository get two distinct worktrees
// on distinct branches; a real, uncommitted change written into one
// family's worktree (via spike-helper, a genuinely separate process — the
// same isolation boundary a real agent process would be spawned inside) is
// invisible in Inspect/Diff for the other family; and the base repository
// itself is never left dirty by either.
func runSPK06Scenario(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
	started := time.Now().UTC()
	if sc.Binaries.SpikeHelper == "" {
		return SPKResult{}, fmt.Errorf("spk06: ScenarioBinaries.SpikeHelper is required")
	}
	var assertions []Assertion
	passed := true
	record := func(name string, ok bool, detail string) {
		assertions = append(assertions, Assertion{Name: name, Passed: ok, Detail: detail})
		if !ok {
			passed = false
		}
	}

	fixtureRoot, err := os.MkdirTemp("", "spk06-*")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk06: create temp dir: %w", err)
	}
	defer os.RemoveAll(fixtureRoot)

	repositoryPath := filepath.Join(fixtureRoot, "sources", "shared-service")
	baseRevision, err := createFixtureGitRepository(repositoryPath, "base\n")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk06: create shared repository: %w", err)
	}
	provider, err := gitworktree.New(gitworktree.Config{Root: filepath.Join(fixtureRoot, "workspaces")})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk06: build provider: %w", err)
	}

	firstSpec := ports.ProvisionSpec{
		RepositoryID: project.RepositoryID("repo-shared"), LocalRepository: repositoryPath, BaseRef: baseRevision,
		FamilyID: work.TaskFamilyID("spk06-family-one"), WorkspaceSetID: workspace.WorkspaceSetID("spk06-set-one"), Generation: 1,
	}
	secondSpec := firstSpec
	secondSpec.FamilyID = work.TaskFamilyID("spk06-family-two")
	secondSpec.WorkspaceSetID = workspace.WorkspaceSetID("spk06-set-two")

	firstHandle, err := provider.Provision(ctx, firstSpec)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk06: provision family one: %w", err)
	}
	secondHandle, err := provider.Provision(ctx, secondSpec)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk06: provision family two: %w", err)
	}
	record("two root families provisioning the same repository get distinct handles", firstHandle != secondHandle, "")

	firstWorkingDirectory, err := provider.WorkingDirectory(ctx, firstHandle)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk06: resolve family one working directory: %w", err)
	}

	// A real, separate process — not the scenario's own goroutine — writes
	// the change, exactly like a real agent process spawned in this
	// worktree would.
	helper := exec.Command(sc.Binaries.SpikeHelper, filepath.Join(firstWorkingDirectory, "service.txt")+"=changed-only-by-family-one\n")
	if output, err := helper.CombinedOutput(); err != nil {
		return SPKResult{}, fmt.Errorf("spk06: spike-helper write: %w: %s", err, output)
	}

	firstInspection, err := provider.Inspect(ctx, firstHandle)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk06: inspect family one: %w", err)
	}
	secondInspection, err := provider.Inspect(ctx, secondHandle)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk06: inspect family two: %w", err)
	}
	record("family one's change is observed as dirty", firstInspection.Dirty, "")
	record("family one's change does not leak into family two's worktree", !secondInspection.Dirty, "")

	secondWorkingDirectory, err := provider.WorkingDirectory(ctx, secondHandle)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk06: resolve family two working directory: %w", err)
	}
	secondContent, err := os.ReadFile(filepath.Join(secondWorkingDirectory, "service.txt"))
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk06: read family two fixture file: %w", err)
	}
	record("family two's file content is untouched", string(secondContent) == "base\n", string(secondContent))

	base := workspace.Revision{RepositoryID: firstSpec.RepositoryID, VCSObjectID: baseRevision, WorkspaceGeneration: firstSpec.Generation}
	firstDiff, err := provider.Diff(ctx, firstHandle, base)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk06: diff family one: %w", err)
	}
	record("family one's diff shows exactly the one changed file",
		len(firstDiff.Files) == 1 && firstDiff.Files[0].Path == "service.txt", fmt.Sprintf("%#v", firstDiff.Files))

	sourceDirty, err := isRepositoryDirty(repositoryPath)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk06: check base repository cleanliness: %w", err)
	}
	record("base repository is never left dirty by either family's worktree", !sourceDirty, "")

	workspaceSetArtifact, err := sc.Bundle.PutJSON("workspace/workspace-set.json", map[string]any{
		"familyOne": firstSpec.FamilyID, "familyTwo": secondSpec.FamilyID,
		"repositoryId": firstSpec.RepositoryID, "handlesDiffer": firstHandle != secondHandle,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk06: write workspace-set evidence: %w", err)
	}
	diffArtifact, err := sc.Bundle.Put("workspace/diffs/repo-shared.patch", firstDiff.Patch)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk06: write diff evidence: %w", err)
	}

	return SPKResult{
		Passed:     passed,
		Assertions: assertions,
		Correlation: CorrelationIDs{
			ProjectID: "spk06-project", FamilyID: string(firstSpec.FamilyID), RepositoryID: string(firstSpec.RepositoryID), Revision: baseRevision,
		},
		Platform: Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Timing:   Timing{StartedAt: started, EndedAt: time.Now().UTC()},
		Artifacts: []ArtifactRef{
			{Kind: ArtifactKindWorkspace, Artifact: workspaceSetArtifact},
			{Kind: ArtifactKindWorkspace, Artifact: diffArtifact},
		},
	}, nil
}

func isRepositoryDirty(repositoryPath string) (bool, error) {
	command := exec.Command("git", "-C", repositoryPath, "status", "--porcelain")
	output, err := command.Output()
	if err != nil {
		return false, err
	}
	return len(output) != 0, nil
}
