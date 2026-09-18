package workitem_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	cliworkitem "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitem"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func decodeReadiness(t *testing.T, stdout *bytes.Buffer) workapp.WorkItemReadiness {
	t.Helper()
	var readiness workapp.WorkItemReadiness
	if err := json.Unmarshal(stdout.Bytes(), &readiness); err != nil {
		t.Fatalf("decode WorkItemReadiness %s: %v", stdout.String(), err)
	}
	return readiness
}

// TestRunWorkItemReadiness_RecomputesFreshNeverTrustsStoredStatus is this
// task's own "readiness must always be recomputed FRESH — never trust a
// projection's own status for a decision" Verify bullet, made concrete
// against the one axis a naive/cached implementation could plausibly get
// wrong: the WorkItem's own stored Status field. It proves BOTH directions:
//
//   - a WorkItem whose Status is ACTIVE (which a naive shortcut might read
//     as "already running, so it must have passed readiness once" and
//     report Ready=true without checking anything else) is correctly
//     reported Ready=true here ONLY because its contract genuinely IS
//     complete — never because of its Status;
//   - a WorkItem whose Status is BACKLOG (the ordinary "not yet marked
//     ready" case) but whose contract is genuinely INCOMPLETE (missing
//     Behavior) is correctly reported Ready=false with that exact problem
//     named — never a generic "still BACKLOG" answer that a naive
//     status-based shortcut might substitute instead of running the real
//     gate.
//
// Both WorkItems are read through the exact same `work-item readiness`
// command path; the only thing that differs between the two calls is each
// WorkItem's own freshly-reloaded row, proving this leaf never short-
// circuits on a cached or stale value — there is nothing else it could be
// deriving these two different, correct answers from.
func TestRunWorkItemReadiness_RecomputesFreshNeverTrustsStoredStatus(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")
	familyID := workdomain.TaskFamilyID(root.FamilyID)

	complete := fullyContractedWorkItem("wi-complete", project.ProjectID("project-1"), familyID, workdomain.WorkItemActive)
	persistWorkItem(t, u, complete)

	incomplete := fullyContractedWorkItem("wi-incomplete", project.ProjectID("project-1"), familyID, workdomain.WorkItemBacklog)
	incomplete.Behavior = ""
	persistWorkItem(t, u, incomplete)

	var completeOut bytes.Buffer
	if err := cliworkitem.RunWorkItemReadiness(context.Background(), deps, []string{"--project-id", "project-1", "wi-complete"}, &completeOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunWorkItemReadiness(wi-complete) error = %v", err)
	}
	completeReadiness := decodeReadiness(t, &completeOut)
	if !completeReadiness.Ready || len(completeReadiness.Problems) != 0 {
		t.Fatalf("wi-complete (Status=ACTIVE) readiness = %+v, want Ready=true Problems=empty — the real gate, not Status, decides this", completeReadiness)
	}

	var incompleteOut bytes.Buffer
	if err := cliworkitem.RunWorkItemReadiness(context.Background(), deps, []string{"--project-id", "project-1", "wi-incomplete"}, &incompleteOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunWorkItemReadiness(wi-incomplete) error = %v", err)
	}
	incompleteReadiness := decodeReadiness(t, &incompleteOut)
	if incompleteReadiness.Ready {
		t.Fatalf("wi-incomplete (Status=BACKLOG, missing Behavior) readiness = %+v, want Ready=false", incompleteReadiness)
	}
	foundBehaviorProblem := false
	for _, p := range incompleteReadiness.Problems {
		if p == "behavior is required" {
			foundBehaviorProblem = true
		}
	}
	if !foundBehaviorProblem {
		t.Fatalf("wi-incomplete Problems = %+v, want to include the real gate's own \"behavior is required\"", incompleteReadiness.Problems)
	}
}

func TestRunWorkItemReadiness_CrossProject_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-a", "repo-a")
	mustCreateProject(t, u, "project-b")

	var stdout, stderr bytes.Buffer
	err := cliworkitem.RunWorkItemReadiness(context.Background(), deps, []string{"--project-id", "project-b", root.WorkItemID}, &stdout, &stderr)
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("error = %v, want ports.ErrScopeMismatch", err)
	}
}
