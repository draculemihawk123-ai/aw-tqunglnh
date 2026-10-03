package runtime_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V9-08 (HE-12-M03: "regression không được che bởi lỗi có sẵn") — the
// maker sent back by a failing check learns whether the repository's baseline
// was green before the task (the failure is the task's regression) or already
// red (the failure may not be the task's).

// recordLoopBaseline records a baseline attempt on the repository workspace of
// the loop's work item.
func recordLoopBaseline(t *testing.T, loop *makerCheckLoop, id string, outcome readiness.BaselineOutcome) {
	t.Helper()
	ctx := context.Background()
	if err := loop.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, loop.runID)
		if err != nil {
			return err
		}
		item, err := tx.Work().GetWorkItem(ctx, string(run.WorkItemID))
		if err != nil {
			return err
		}
		set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, string(item.FamilyID))
		if err != nil {
			return err
		}
		all, err := tx.Work().ListWorkspaceSetRepositoryWorkspaces(ctx, string(set.ID))
		if err != nil || len(all) != 1 {
			t.Fatalf("repository workspaces = %d (%v), want 1", len(all), err)
		}
		exit := 0
		if outcome == readiness.BaselineRed {
			exit = 1
		}
		_, err = tx.Readiness().RecordBaselineAttempt(ctx, ports.RecordBaselineAttemptRequest{
			ID: id, ProjectID: "project-1", RepositoryWorkspaceID: string(all[0].ID), RepositoryID: "repo-1", JobID: "job-" + id,
			Stage: readiness.StageVerification, Outcome: outcome, ExitCode: &exit, ProfileVersion: 1,
		})
		return err
	}); err != nil {
		t.Fatalf("record baseline %s: %v", id, err)
	}
}

// secondMakerCheckFailureBaseline runs maker -> failing check -> maker and
// returns the "baseline" text of the second maker's checkFailures entry.
func secondMakerCheckFailureBaseline(t *testing.T, loop *makerCheckLoop) (text string, present bool) {
	t.Helper()
	// The snapshot of the second maker is created later than the baseline
	// attempt; sqlite-free fake time has nanosecond resolution but be explicit.
	time.Sleep(5 * time.Millisecond)
	_, checkNodeRunID := loop.runMaker()
	_, finalized := loop.runCheck(checkNodeRunID, &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 1, TreeQuiesced: true},
		Stderr: "--- FAIL: TestAdd (0.00s)\n    add_test.go:12: want 3, got 2\nFAIL\n",
	})
	if !finalized.Advanced || finalized.AdvanceResult.NextNodeKey != "implement" {
		t.Fatalf("finalize check = %+v, want the failureOutcome edge back to implement", finalized)
	}
	secondNodeRunID := finalized.AdvanceResult.NextNodeRunID
	secondAttempt, _ := loop.start(secondNodeRunID)
	prompt := loop.prompt(secondNodeRunID, secondAttempt)
	var failures []struct {
		Baseline *string `json:"baseline"`
	}
	if err := json.Unmarshal(prompt["checkFailures"], &failures); err != nil || len(failures) != 1 {
		t.Fatalf("checkFailures = %s (%v), want exactly one", prompt["checkFailures"], err)
	}
	if failures[0].Baseline == nil {
		return "", false
	}
	return *failures[0].Baseline, true
}

func TestMakerAfterFailingCheck_BaselineWasGreen_FailureIsTheTasksRegression(t *testing.T) {
	loop := newMakerCheckLoop(t, workflow.NodeCommand)
	recordLoopBaseline(t, loop, "baseline-green", readiness.BaselineGreen)
	text, present := secondMakerCheckFailureBaseline(t, loop)
	if !present || !strings.Contains(text, "its baseline passed before this task started") || !strings.Contains(text, "comes from the changes made during this task") {
		t.Fatalf("baseline note = %q (present=%v), want the regression wording", text, present)
	}
}

func TestMakerAfterFailingCheck_BaselineWasRed_FailureMayNotBeTheTasks(t *testing.T) {
	loop := newMakerCheckLoop(t, workflow.NodeCommand)
	recordLoopBaseline(t, loop, "baseline-red", readiness.BaselineRed)
	text, present := secondMakerCheckFailureBaseline(t, loop)
	if !present || !strings.Contains(text, "had already failed before this task started (PRE_EXISTING_FAILURE, attempt baseline-red)") ||
		!strings.Contains(text, "is not caused by this task") {
		t.Fatalf("baseline note = %q (present=%v), want the pre-existing wording", text, present)
	}
}

func TestMakerAfterFailingCheck_NoBaseline_HasNoBaselineKey(t *testing.T) {
	loop := newMakerCheckLoop(t, workflow.NodeCommand)
	if text, present := secondMakerCheckFailureBaseline(t, loop); present {
		t.Fatalf("a repository with no baseline got a baseline note: %q", text)
	}
}
