package runtime_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/readinesscheck"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
)

// V9-08 (gap G8) — a run only starts for a work item that may write to a
// repository when that repository's baseline admits a writer. MarkWorkItemReady
// checks it too (internal/app/work), but a profile can change after a work item
// became READY, so StartWorkflowRun asks again at the moment a writer would
// actually start.

func TestStartWorkflowRun_BaselineGate(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := readyFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")

	var rwID string
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		all, err := tx.Work().ListWorkspaceSetRepositoryWorkspaces(ctx, root.WorkspaceSetID)
		if err == nil && len(all) == 1 {
			rwID = string(all[0].ID)
		}
		return err
	}); err != nil || rwID == "" {
		t.Fatalf("find the provisioned repository workspace: %v (%q)", err, rwID)
	}

	// The profile is declared AFTER the work item became READY.
	if _, err := readinesscheck.SetRepositoryReadinessProfile(ctx, uow, ids,
		testCommand("idem-profile", "hash-profile", ports.ProjectScope("project-1"), "SetRepositoryReadinessProfile"),
		readinesscheck.SetRepositoryReadinessProfileRequest{
			ProjectID: "project-1", RepositoryID: "repo-1",
			Verification: readinesscheck.CommandInput{Executable: "npm", Argv: []string{"test"}, TimeoutSeconds: 60},
		}); err != nil {
		t.Fatalf("SetRepositoryReadinessProfile: %v", err)
	}

	start := func(key string) error {
		_, err := runtime.StartWorkflowRun(ctx, uow, ids, testCommand(key, "hash-"+key, ports.ProjectScope("project-1"), "StartWorkflowRun"),
			runtime.StartWorkflowRunRequest{ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID())})
		return err
	}
	record := func(id string, outcome readiness.BaselineOutcome) {
		t.Helper()
		exit := 0
		if outcome == readiness.BaselineRed {
			exit = 1
		}
		if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			_, err := tx.Readiness().RecordBaselineAttempt(ctx, ports.RecordBaselineAttemptRequest{
				ID: id, ProjectID: "project-1", RepositoryWorkspaceID: rwID, RepositoryID: "repo-1", JobID: "job-" + id,
				Stage: readiness.StageVerification, Outcome: outcome, ExitCode: &exit, ProfileVersion: 1,
			})
			return err
		}); err != nil {
			t.Fatalf("record %s: %v", id, err)
		}
	}

	if err := start("start-pending"); !errors.Is(err, runtime.ErrBaselineNotAdmitted) || !strings.Contains(err.Error(), "baseline pending") {
		t.Fatalf("starting before the baseline ran = %v, want ErrBaselineNotAdmitted (baseline pending)", err)
	}
	record("attempt-red", readiness.BaselineRed)
	if err := start("start-red"); !errors.Is(err, runtime.ErrBaselineNotAdmitted) || !strings.Contains(err.Error(), "baseline FAILED") {
		t.Fatalf("starting on a red baseline = %v, want ErrBaselineNotAdmitted (baseline FAILED)", err)
	}
	// The refusal changed nothing: the work item is still READY.
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(ctx, root.WorkItemID)
		if err != nil || string(item.Status) != "READY" {
			t.Fatalf("work item after refused starts = %v (%v), want READY", item.Status, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := readinesscheck.AcceptBaselineException(ctx, uow, ids,
		testCommand("idem-accept", "hash-accept", ports.ProjectScope("project-1"), "AcceptBaselineException"),
		readinesscheck.AcceptBaselineExceptionRequest{ProjectID: "project-1", RepositoryID: "repo-1", BaselineAttemptID: "attempt-red", Reason: "known flaky suite"}); err != nil {
		t.Fatalf("AcceptBaselineException: %v", err)
	}
	if err := start("start-accepted"); err != nil {
		t.Fatalf("starting after the operator accepted the failure: %v", err)
	}
}

func TestStartWorkflowRun_NoReadinessProfile_IsNotGated(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := readyFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")
	if _, err := runtime.StartWorkflowRun(ctx, uow, ids, testCommand("idem-start", "hash-start", ports.ProjectScope("project-1"), "StartWorkflowRun"),
		runtime.StartWorkflowRunRequest{ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID())}); err != nil {
		t.Fatalf("a repository without a readiness profile gated the run: %v", err)
	}
}
