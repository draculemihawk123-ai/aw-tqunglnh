package workspacereconcile_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
)

// V11-01: the preflight `aw run start` runs — an early, explained answer in
// place of a late, opaque machine-gate failure.

func TestCheckFamilyWorkspaceHeads_PassesWhenHeadIsTheRecordedRevision(t *testing.T) {
	f := newRealFixture(t, "preflight-clean.db")
	if err := workspacereconcile.CheckFamilyWorkspaceHeads(context.Background(), f.uow, f.provider, f.workItemID); err != nil {
		t.Fatalf("CheckFamilyWorkspaceHeads on an untouched worktree: %v", err)
	}
}

func TestCheckFamilyWorkspaceHeads_RejectsAnOutsideCommitAndNamesTheCommand(t *testing.T) {
	f := newRealFixture(t, "preflight-drift.db")
	f.commitOutsideAw(t, "a.txt", "by hand")

	err := workspacereconcile.CheckFamilyWorkspaceHeads(context.Background(), f.uow, f.provider, f.workItemID)
	if !errors.Is(err, workspacereconcile.ErrWorkspaceHeadDrift) {
		t.Fatalf("err = %v, want ErrWorkspaceHeadDrift", err)
	}
	var drift *workspacereconcile.WorkspaceHeadDriftError
	if !errors.As(err, &drift) || len(drift.Drifts) != 1 || !drift.Drifts[0].FastForward || drift.Drifts[0].TotalCommits != 1 {
		t.Fatalf("drift = %+v, want one fast-forward repository with one commit", drift)
	}
	for _, want := range []string{"WORKSPACE_HEAD_DRIFT", f.repositoryID, "1 commit(s) made outside aw", "aw repository-workspace adopt-head " + string(f.rw.ID), "--project-id project-1"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("message %q does not contain %q", err.Error(), want)
		}
	}
}

func TestCheckFamilyWorkspaceHeads_PassesOnceTheHeadIsAdopted(t *testing.T) {
	f := newRealFixture(t, "preflight-adopted.db")
	f.commitOutsideAw(t, "a.txt", "by hand")
	if _, err := f.adopt(t, "preflight-adopt", f.rw.Version); err != nil {
		t.Fatalf("AdoptWorkspaceHead: %v", err)
	}
	if err := workspacereconcile.CheckFamilyWorkspaceHeads(context.Background(), f.uow, f.provider, f.workItemID); err != nil {
		t.Fatalf("CheckFamilyWorkspaceHeads after adopt-head: %v", err)
	}
}

func TestCheckFamilyWorkspaceHeads_AfterAResetDoesNotOfferAdoption(t *testing.T) {
	f := newRealFixture(t, "preflight-reset.db")
	f.commitInFamilyWorkspace(t, "family.txt")
	runReconcileFixtureGit(t, f.workingDirectory(t), "reset", "-q", "--hard", f.rw.BaseRevision)

	err := workspacereconcile.CheckFamilyWorkspaceHeads(context.Background(), f.uow, f.provider, f.workItemID)
	if !errors.Is(err, workspacereconcile.ErrWorkspaceHeadDrift) {
		t.Fatalf("err = %v, want ErrWorkspaceHeadDrift", err)
	}
	if strings.Contains(err.Error(), "adopt-head ") || !strings.Contains(err.Error(), "reset or rebase") {
		t.Fatalf("message %q must explain the reset instead of offering adoption", err.Error())
	}
}

func TestCheckFamilyWorkspaceHeads_NilAuditorAndUnknownWorkItemAreNotErrors(t *testing.T) {
	f := newRealFixture(t, "preflight-skip.db")
	f.commitOutsideAw(t, "a.txt", "by hand")
	if err := workspacereconcile.CheckFamilyWorkspaceHeads(context.Background(), f.uow, nil, f.workItemID); err != nil {
		t.Fatalf("nil auditor: %v", err)
	}
	if err := workspacereconcile.CheckFamilyWorkspaceHeads(context.Background(), f.uow, f.provider, "no-such-work-item"); err != nil {
		t.Fatalf("unknown work item is StartWorkflowRun's error to report: %v", err)
	}
}
