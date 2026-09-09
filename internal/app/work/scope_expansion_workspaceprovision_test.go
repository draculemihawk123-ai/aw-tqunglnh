package work_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// This file exercises ApproveScopeExpansion's own "READY reopen" fix (see
// that function's own doc comment in scope_expansion.go for the full
// reasoning) against the REAL, completely UNMODIFIED
// internal/app/workspaceprovision.Handler (V3-06) — never a re-implementation
// or a mock of it. It is the concrete proof that a post-hoc-added repository
// really does reach READY with a freshly-recomputed, EXPANDED base
// RevisionSet, and that this task's own change achieves that by reopening
// the WorkspaceSet from THIS package's side only, with zero changes to
// workspaceprovision itself.

// scriptedProvider is a scriptable ports.WorkspaceProvider test double keyed
// by RepositoryID, mirroring internal/app/workspaceprovision's own
// (unexported, different-package) stubProvider test double — duplicated
// locally rather than imported, since this file's own job is proving
// ApproveScopeExpansion and workspaceprovision.Handler compose correctly
// across the package boundary, not re-testing either package's own
// already-covered internals.
type scriptedProvider struct {
	revisions map[string]workspace.Revision // by RepositoryID
}

func (s *scriptedProvider) Provision(_ context.Context, spec ports.ProvisionSpec) (ports.WorkspaceHandle, error) {
	if _, ok := s.revisions[string(spec.RepositoryID)]; !ok {
		return ports.WorkspaceHandle{}, fmt.Errorf("scriptedProvider: no revision scripted for %s", spec.RepositoryID)
	}
	return ports.NewWorkspaceHandle("handle-" + string(spec.RepositoryID))
}

func (s *scriptedProvider) Inspect(context.Context, ports.WorkspaceHandle) (ports.WorkspaceInspection, error) {
	return ports.WorkspaceInspection{}, errors.New("scriptedProvider: Inspect must not be called by workspaceprovision.Handler")
}

func (s *scriptedProvider) CaptureRevision(_ context.Context, handle ports.WorkspaceHandle) (workspace.Revision, error) {
	repositoryID := strings.TrimPrefix(handle.String(), "handle-")
	rev, ok := s.revisions[repositoryID]
	if !ok {
		return workspace.Revision{}, fmt.Errorf("scriptedProvider: no revision scripted for handle %s", handle.String())
	}
	return rev, nil
}

func (s *scriptedProvider) Diff(context.Context, ports.WorkspaceHandle, workspace.Revision) (ports.WorkspaceDiff, error) {
	return ports.WorkspaceDiff{}, errors.New("scriptedProvider: Diff must not be called by workspaceprovision.Handler")
}

func (s *scriptedProvider) Release(context.Context, ports.WorkspaceHandle) error {
	return errors.New("scriptedProvider: Release must not be called by workspaceprovision.Handler")
}

func (s *scriptedProvider) WorkingDirectory(context.Context, ports.WorkspaceHandle) (string, error) {
	return "", errors.New("scriptedProvider: WorkingDirectory must not be called by workspaceprovision.Handler")
}

// provisionJobFor builds the exact ports.DurableJob payload shape
// ApproveScopeExpansion/CreateRootWorkItem both marshal for a
// WORKSPACE_PROVISION job, mirroring workspaceprovision's own
// (unexported) provisionJob test helper.
func provisionJobFor(id, projectID, familyID, workspaceSetID, repositoryID string) ports.DurableJob {
	payload, _ := json.Marshal(struct {
		WorkItemID     string `json:"workItemId"`
		ProjectID      string `json:"projectId"`
		FamilyID       string `json:"familyId"`
		WorkspaceSetID string `json:"workspaceSetId"`
		RepositoryID   string `json:"repositoryId"`
	}{ProjectID: projectID, FamilyID: familyID, WorkspaceSetID: workspaceSetID, RepositoryID: repositoryID})
	return ports.DurableJob{ID: ports.JobID(id), Kind: work.WorkspaceProvisionJobKind, Payload: payload}
}

// TestApproveScopeExpansion_ReopensReadyWorkspaceSet_NewRepositoryReachesReadyWithExpandedRevisionSet
// is this task's own most important integration proof. Without
// ApproveScopeExpansion's own reopen step, this test would fail: the
// WorkspaceSet would stay stuck at READY, forever holding only repo-1's own
// original single-repository base RevisionSet, even after repo-2's own
// WORKSPACE_PROVISION job completed successfully — exactly the "hard no path
// back once terminal" gap this task's own investigation traced through
// workspaceprovision.Handler's own aggregateWorkspaceSet.
func TestApproveScopeExpansion_ReopensReadyWorkspaceSet_NewRepositoryReachesReadyWithExpandedRevisionSet(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-1")

	provider := &scriptedProvider{revisions: map[string]workspace.Revision{
		"repo-1": {RepositoryID: "repo-1", VCSObjectID: "1111111111111111111111111111111111111111", WorkspaceGeneration: 1},
	}}
	handler := workspaceprovision.New(uow, ids, provider)

	rootCmd := testCommand("idem-root-1", "hash-root-1", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	root, err := work.CreateRootWorkItem(ctx, uow, ids, rootCmd, baseRequest("project-1", grant("repo-1")))
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}

	// Drive repo-1's own WORKSPACE_PROVISION job to completion — the
	// WorkspaceSet reaches READY with a single-repository base RevisionSet,
	// the "already READY before any expansion" starting condition this
	// test's own scenario needs.
	if err := handler.Handle(ctx, provisionJobFor(root.ProvisionedRepositories[0].ProvisionJobID, "project-1", root.FamilyID, root.WorkspaceSetID, "repo-1")); err != nil {
		t.Fatalf("Handle(repo-1): %v", err)
	}
	setBefore, err := uow.Snapshot.Work().GetWorkspaceSetByFamilyID(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID (before expansion): %v", err)
	}
	if setBefore.State != workspace.WorkspaceSetReady {
		t.Fatalf("WorkspaceSet.State (before expansion) = %q, want READY", setBefore.State)
	}
	if setBefore.BaseRevisionSet == nil {
		t.Fatal("BaseRevisionSet (before expansion) is nil, want the single-repository base revision set")
	}
	if _, ok := setBefore.BaseRevisionSet.RevisionFor("repo-2"); ok {
		t.Fatal("BaseRevisionSet already covers repo-2 before it was ever granted")
	}

	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")
	provider.revisions["repo-2"] = workspace.Revision{RepositoryID: "repo-2", VCSObjectID: "2222222222222222222222222222222222222222", WorkspaceGeneration: 1}

	reqCmd := testCommand("idem-request-1", "hash-request-1", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, reqCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2 too",
		RequestedGrants: []work.ScopeGrantRequest{
			{RepositoryID: "repo-2", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"services/repo-2"}, Reason: "expand"},
		},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}

	approveCmd := testCommand("idem-approve-1", "hash-approve-1", ports.ProjectScope("project-1"), "ApproveScopeExpansion")
	approveResult, err := work.ApproveScopeExpansion(ctx, uow, ids, approveCmd, work.ApproveScopeExpansionRequest{RequestID: reqResult.RequestID})
	if err != nil {
		t.Fatalf("ApproveScopeExpansion: %v", err)
	}
	if approveResult.NewScopeVersion != 2 {
		t.Fatalf("NewScopeVersion = %d, want 2", approveResult.NewScopeVersion)
	}
	if len(approveResult.ProvisionedRepositories) != 1 || approveResult.ProvisionedRepositories[0].RepositoryID != "repo-2" {
		t.Fatalf("ProvisionedRepositories = %+v, want exactly one entry for repo-2", approveResult.ProvisionedRepositories)
	}

	// The reopen itself: the set must have already left READY the instant
	// approval committed, even though repo-2's own job has not run yet.
	setAfterApproval, err := uow.Snapshot.Work().GetWorkspaceSetByFamilyID(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID (after approval): %v", err)
	}
	if setAfterApproval.State != workspace.WorkspaceSetProvisioning {
		t.Fatalf("WorkspaceSet.State (after approval, before repo-2's own job runs) = %q, want PROVISIONING (the reopen)", setAfterApproval.State)
	}

	if err := handler.Handle(ctx, provisionJobFor(approveResult.ProvisionedRepositories[0].ProvisionJobID, "project-1", root.FamilyID, root.WorkspaceSetID, "repo-2")); err != nil {
		t.Fatalf("Handle(repo-2): %v", err)
	}

	setFinal, err := uow.Snapshot.Work().GetWorkspaceSetByFamilyID(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID (final): %v", err)
	}
	if setFinal.State != workspace.WorkspaceSetReady {
		t.Fatalf("WorkspaceSet.State (final) = %q, want READY again", setFinal.State)
	}
	if setFinal.BaseRevisionSet == nil {
		t.Fatal("BaseRevisionSet (final) is nil, want a freshly computed base revision set covering both repositories")
	}
	rev1, ok1 := setFinal.BaseRevisionSet.RevisionFor("repo-1")
	rev2, ok2 := setFinal.BaseRevisionSet.RevisionFor("repo-2")
	if !ok1 || !ok2 || rev1.VCSObjectID != "1111111111111111111111111111111111111111" || rev2.VCSObjectID != "2222222222222222222222222222222222222222" {
		t.Fatalf("final BaseRevisionSet = %+v, want entries for both repo-1 and repo-2", setFinal.BaseRevisionSet.Entries())
	}

	// A same-repository access upgrade (no new repository) must NOT reopen
	// an already-READY set — see ApproveScopeExpansion's own "newly-added
	// vs. upgraded" paragraph.
	upgradeReqCmd := testCommand("idem-request-2", "hash-request-2", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	upgradeReq, err := work.RequestScopeExpansion(ctx, uow, ids, upgradeReqCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "widen repo-1 path",
		RequestedGrants: []work.ScopeGrantRequest{
			{RepositoryID: "repo-1", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"services/repo-1/extra"}, Reason: "widen"},
		},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion (upgrade): %v", err)
	}
	upgradeApproveCmd := testCommand("idem-approve-2", "hash-approve-2", ports.ProjectScope("project-1"), "ApproveScopeExpansion")
	upgradeResult, err := work.ApproveScopeExpansion(ctx, uow, ids, upgradeApproveCmd, work.ApproveScopeExpansionRequest{RequestID: upgradeReq.RequestID})
	if err != nil {
		t.Fatalf("ApproveScopeExpansion (upgrade): %v", err)
	}
	if len(upgradeResult.ProvisionedRepositories) != 0 {
		t.Fatalf("ProvisionedRepositories (upgrade) = %+v, want none (repo-1 already had a grant)", upgradeResult.ProvisionedRepositories)
	}
	setAfterUpgrade, err := uow.Snapshot.Work().GetWorkspaceSetByFamilyID(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID (after upgrade): %v", err)
	}
	if setAfterUpgrade.State != workspace.WorkspaceSetReady {
		t.Fatalf("WorkspaceSet.State (after a pure access/path upgrade) = %q, want unchanged READY", setAfterUpgrade.State)
	}
}
