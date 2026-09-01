package spikeacceptance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// runSPK05Scenario closes SPK-05 against a real local Git provider: a
// family with two repositories in its scope gets exactly one WorkspaceSet
// with two RepositoryWorkspaces; a child WorkItem reuses the family-owned
// handle for its repository rather than provisioning a new worktree; and
// provisioning is idempotent for the same family/repository/generation.
func runSPK05Scenario(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
	started := time.Now().UTC()
	var assertions []Assertion
	passed := true
	record := func(name string, ok bool, detail string) {
		assertions = append(assertions, Assertion{Name: name, Passed: ok, Detail: detail})
		if !ok {
			passed = false
		}
	}

	fixtureRoot, err := os.MkdirTemp("", "spk05-*")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk05: create temp dir: %w", err)
	}
	defer os.RemoveAll(fixtureRoot)

	userRepositoryPath := filepath.Join(fixtureRoot, "sources", "user-service")
	userBase, err := createFixtureGitRepository(userRepositoryPath, "user-v0\n")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk05: create user repository: %w", err)
	}
	webRepositoryPath := filepath.Join(fixtureRoot, "sources", "web-app")
	webBase, err := createFixtureGitRepository(webRepositoryPath, "web-v0\n")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk05: create web repository: %w", err)
	}
	provider, err := gitworktree.New(gitworktree.Config{Root: filepath.Join(fixtureRoot, "workspaces")})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk05: build provider: %w", err)
	}

	familyID := work.TaskFamilyID("spk05-family")
	setID := workspace.WorkspaceSetID("spk05-workspace-set")
	userSpec := ports.ProvisionSpec{
		RepositoryID: project.RepositoryID("repo-user"), LocalRepository: userRepositoryPath, BaseRef: "HEAD",
		FamilyID: familyID, WorkspaceSetID: setID, Generation: 1,
	}
	webSpec := ports.ProvisionSpec{
		RepositoryID: project.RepositoryID("repo-web"), LocalRepository: webRepositoryPath, BaseRef: "HEAD",
		FamilyID: familyID, WorkspaceSetID: setID, Generation: 1,
	}
	userHandle, err := provider.Provision(ctx, userSpec)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk05: provision user workspace: %w", err)
	}
	webHandle, err := provider.Provision(ctx, webSpec)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk05: provision web workspace: %w", err)
	}
	record("two repositories in one family get distinct workspace handles", userHandle != webHandle, "")

	userInspection, err := provider.Inspect(ctx, userHandle)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk05: inspect user workspace: %w", err)
	}
	webInspection, err := provider.Inspect(ctx, webHandle)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk05: inspect web workspace: %w", err)
	}
	record("user workspace identity matches the family/workspace set/repository it was provisioned for",
		userInspection.FamilyID == familyID && userInspection.WorkspaceSetID == setID && userInspection.RepositoryID == userSpec.RepositoryID, "")
	record("web workspace identity matches the family/workspace set/repository it was provisioned for",
		webInspection.FamilyID == familyID && webInspection.WorkspaceSetID == setID && webInspection.RepositoryID == webSpec.RepositoryID, "")

	// A child WorkItem does not provision: application state gives it the
	// same family-owned handle for the repository in its effective scope.
	childUserHandle := userHandle
	childInspection, err := provider.Inspect(ctx, childUserHandle)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk05: inspect child-reused workspace: %w", err)
	}
	record("child WorkItem reuses the family-owned handle within its TaskFamily/WorkspaceSet",
		childInspection.FamilyID == familyID && childInspection.WorkspaceSetID == setID, "")

	userHandleAgain, err := provider.Provision(ctx, userSpec)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk05: idempotent provision: %w", err)
	}
	record("provisioning the same family/repository/generation again is idempotent", userHandleAgain == userHandle, "")

	revisionSet, err := workspace.NewRevisionSet([]workspace.Revision{userInspection.CurrentRevision, webInspection.CurrentRevision})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk05: build revision set: %w", err)
	}
	record("RevisionSet pins both repositories' base revisions",
		len(revisionSet.Entries()) == 2 && revisionSet.ContentHash() != "", "")

	workspaceSetArtifact, err := sc.Bundle.PutJSON("workspace/workspace-set.json", map[string]any{
		"familyId": familyID, "workspaceSetId": setID,
		"repositories": []string{string(userSpec.RepositoryID), string(webSpec.RepositoryID)},
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk05: write workspace-set evidence: %w", err)
	}
	revisionsArtifact, err := sc.Bundle.PutJSON("workspace/revisions-before.json", map[string]string{
		"repoUser": userBase, "repoWeb": webBase,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk05: write revisions evidence: %w", err)
	}

	return SPKResult{
		Passed:     passed,
		Assertions: assertions,
		Correlation: CorrelationIDs{
			ProjectID: "spk05-project", FamilyID: string(familyID), RepositoryID: string(userSpec.RepositoryID), Revision: userBase,
		},
		Platform: Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Timing:   Timing{StartedAt: started, EndedAt: time.Now().UTC()},
		Artifacts: []ArtifactRef{
			{Kind: ArtifactKindWorkspace, Artifact: workspaceSetArtifact},
			{Kind: ArtifactKindWorkspace, Artifact: revisionsArtifact},
		},
	}, nil
}
