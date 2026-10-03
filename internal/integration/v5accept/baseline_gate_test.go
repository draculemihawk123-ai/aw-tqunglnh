// V9-08 — "Readiness profile và baseline cho người vận hành"
// (docs/design/12-v9-harness-alignment.md V9-08; gap G8 in
// docs/harness-engineering/15-doi-chieu-v9.md).
//
// Before V9-08 a repository's readiness profile could only be set by code, no
// baseline ever started on its own and nothing kept an agent from writing to a
// repository whose tests were already red. The scenarios here drive the real
// thing: a real git repository and worktree, a real sqlite store, a real worker
// pool running the BASELINE_EVIDENCE handler, and the verification command run
// as a REAL process (its exit code is the only thing that decides red or green).
//
//   - a repository whose verification command fails: the baseline is recorded
//     as FAIL (PRE_EXISTING_FAILURE), the work item that may write to it cannot
//     become READY, and the refusal names what to do;
//   - the operator accepts that failure with a reason: the same work item
//     becomes READY, the failed attempt is still on the record and the
//     exception carries who and why;
//   - a repository whose verification command passes: the baseline is PASS and
//     the work item becomes READY with no exception;
//   - changing the profile starts a fresh baseline, and until it has run the
//     work item is held again.
package v5accept

import (
	"context"
	"errors"
	"os"
	stdruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/readinesscheck"
	appwork "github.com/taQuangLing/agent-workflow/internal/app/work"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// exitCommand is a real command that exits with code, on every platform the
// suite runs on. An absolute path: the readiness recipe runs with only the
// environment the handler allows.
func exitCommand(code string) readinesscheck.CommandInput {
	if stdruntime.GOOS == "windows" {
		shell := os.Getenv("ComSpec")
		if shell == "" {
			shell = `C:\Windows\System32\cmd.exe`
		}
		return readinesscheck.CommandInput{Executable: shell, Argv: []string{"/c", "exit " + code}, TimeoutSeconds: 60}
	}
	return readinesscheck.CommandInput{Executable: "/bin/sh", Argv: []string{"-c", "exit " + code}, TimeoutSeconds: 60}
}

// baselineGateScenario is one fixture with a running pool that handles
// provisioning and BASELINE_EVIDENCE jobs, and one BACKLOG root work item
// (complete contract, WRITE scope) whose workspace is provisioned.
type baselineGateScenario struct {
	f      *v5AcceptFixture
	itemID string
}

func newBaselineGateScenario(t *testing.T) *baselineGateScenario {
	t.Helper()
	f := newV5AcceptFixture(t)
	registry := f.registerHandlers(f.newCommandExecutor(), "bg")
	registry.Register(readinesscheck.BaselineEvidenceJobKind, readinesscheck.New(f.uow, f.ids, f.supervisor, f.provider))
	_, stop := f.startPool(t, registry)
	t.Cleanup(stop)

	root, err := appwork.CreateRootWorkItem(context.Background(), f.uow, f.ids,
		testCmd("v9h-root", ports.ProjectScope(v5AcceptProjectID), "CreateRootWorkItem"), appwork.CreateRootWorkItemRequest{
			ProjectID: v5AcceptProjectID, Title: "Add the feature",
			InitialScope: []appwork.ScopeGrantRequest{{RepositoryID: v5AcceptRepositoryID, Access: string(workdomain.RepositoryWrite), Reason: "v9-08 acceptance"}},
			Contract: &appwork.WorkItemContractRequest{
				SchemaVersion: 1, Behavior: "The feature works end to end.",
				AcceptanceCriteria: []appwork.AcceptanceCriterionRequest{{Description: "it works", VerificationRef: "go test ./..."}},
				VerificationSpec:   "run the suite", RiskLevel: "MEDIUM",
			},
		})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	for _, provisioned := range root.ProvisionedRepositories {
		f.waitForJobState(t, provisioned.ProvisionJobID, ports.JobSucceeded)
	}
	return &baselineGateScenario{f: f, itemID: root.WorkItemID}
}

func (s *baselineGateScenario) setProfile(t *testing.T, key string, verification readinesscheck.CommandInput) readinesscheck.SetRepositoryReadinessProfileResult {
	t.Helper()
	result, err := readinesscheck.SetRepositoryReadinessProfile(context.Background(), s.f.uow, s.f.ids,
		testCmd(key, ports.ProjectScope(v5AcceptProjectID), "SetRepositoryReadinessProfile"),
		readinesscheck.SetRepositoryReadinessProfileRequest{ProjectID: v5AcceptProjectID, RepositoryID: v5AcceptRepositoryID, Verification: verification})
	if err != nil {
		t.Fatalf("SetRepositoryReadinessProfile(%s): %v", key, err)
	}
	return result
}

// waitForBaseline polls the operator view until the workspace's baseline
// reaches want.
func (s *baselineGateScenario) waitForBaseline(t *testing.T, want readinesscheck.BaselineState) readinesscheck.WorkspaceBaselineView {
	t.Helper()
	deadline := time.Now().Add(pollDeadline)
	var last readinesscheck.RepositoryReadinessView
	for time.Now().Before(deadline) {
		view, err := readinesscheck.GetRepositoryReadiness(context.Background(), s.f.uow, v5AcceptProjectID, v5AcceptRepositoryID)
		if err != nil {
			t.Fatalf("GetRepositoryReadiness: %v", err)
		}
		last = view
		if len(view.Workspaces) == 1 && view.Workspaces[0].BaselineState == string(want) {
			return view.Workspaces[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the baseline did not reach %s within the deadline; last view = %+v", want, last)
	return readinesscheck.WorkspaceBaselineView{}
}

func (s *baselineGateScenario) readinessProblems(t *testing.T) []string {
	t.Helper()
	explained, err := appwork.ExplainWorkItemReadiness(context.Background(), s.f.uow, ports.ProjectScope(v5AcceptProjectID), s.itemID)
	if err != nil {
		t.Fatalf("ExplainWorkItemReadiness: %v", err)
	}
	if explained.Ready {
		return nil
	}
	return explained.Problems
}

func (s *baselineGateScenario) markReady(t *testing.T, key string) error {
	t.Helper()
	_, err := appwork.MarkWorkItemReady(context.Background(), s.f.uow, testCmd(key, ports.ProjectScope(v5AcceptProjectID), "MarkWorkItemReady"),
		appwork.MarkWorkItemReadyRequest{WorkItemID: s.itemID})
	return err
}

func TestV9AcceptBaselineGate_RedRepositoryDoesNotAdmitAWriterUntilTheOperatorAcceptsIt(t *testing.T) {
	s := newBaselineGateScenario(t)
	set := s.setProfile(t, "v9h-profile-red", exitCommand("3"))
	if set.ProfileVersion != 1 || set.BaselineJobsEnqueued != 1 {
		t.Fatalf("set profile = %+v, want version 1 with the baseline of the READY workspace started", set)
	}

	failed := s.waitForBaseline(t, readinesscheck.BaselineFail)
	if failed.AdmitsWriters || failed.Attempt == nil || failed.Attempt.Outcome != "RED" || failed.Attempt.FailureKind != "PRE_EXISTING_FAILURE" ||
		failed.Attempt.ExitCode == nil || *failed.Attempt.ExitCode != 3 {
		t.Fatalf("baseline = %+v, want FAIL: RED, PRE_EXISTING_FAILURE, the real exit code 3, writers blocked", failed)
	}

	problems := s.readinessProblems(t)
	if len(problems) != 1 || !strings.Contains(problems[0], "baseline FAILED (PRE_EXISTING_FAILURE, attempt "+failed.Attempt.AttemptID+")") ||
		!strings.Contains(problems[0], "aw repository readiness accept-exception") {
		t.Fatalf("readiness problems = %v, want the failed baseline and the way out", problems)
	}
	var readinessErr *workdomain.ReadinessError
	if err := s.markReady(t, "v9h-ready-blocked"); !errors.As(err, &readinessErr) {
		t.Fatalf("MarkWorkItemReady on a red baseline = %v, want a ReadinessError", err)
	}

	// The operator accepts the failure, with a reason.
	accepted, err := readinesscheck.AcceptBaselineException(context.Background(), s.f.uow, s.f.ids,
		testCmd("v9h-accept", ports.ProjectScope(v5AcceptProjectID), "AcceptBaselineException"),
		readinesscheck.AcceptBaselineExceptionRequest{
			ProjectID: v5AcceptProjectID, RepositoryID: v5AcceptRepositoryID, BaselineAttemptID: failed.Attempt.AttemptID, Reason: "legacy suite, tracked as TODO-7",
		})
	if err != nil || accepted.AcceptedBy != "operator-1" {
		t.Fatalf("AcceptBaselineException = %+v (%v), want it attributed to operator-1", accepted, err)
	}
	if err := s.markReady(t, "v9h-ready-accepted"); err != nil {
		t.Fatalf("MarkWorkItemReady after the operator accepted the failure: %v", err)
	}

	after := s.waitForBaseline(t, readinesscheck.BaselineExceptionAccepted)
	if !after.AdmitsWriters || after.Attempt == nil || after.Attempt.Outcome != "RED" || after.Exception == nil ||
		after.Exception.Reason != "legacy suite, tracked as TODO-7" || after.Exception.AcceptedBy != "operator-1" {
		t.Fatalf("baseline after the exception = %+v, want the RED attempt still on record next to the attributed exception", after)
	}
}

func TestV9AcceptBaselineGate_GreenRepositoryRunsNormally_AndAChangedProfileHoldsItAgain(t *testing.T) {
	s := newBaselineGateScenario(t)
	s.setProfile(t, "v9h-profile-green", exitCommand("0"))

	passed := s.waitForBaseline(t, readinesscheck.BaselinePass)
	if !passed.AdmitsWriters || passed.Attempt == nil || passed.Attempt.Outcome != "GREEN" || passed.Attempt.FailureKind != "" {
		t.Fatalf("baseline = %+v, want PASS: GREEN, no failure kind, writers admitted", passed)
	}
	if problems := s.readinessProblems(t); len(problems) != 0 {
		t.Fatalf("readiness problems on a green baseline = %v, want none", problems)
	}

	// Changing the profile makes the green result stale; the new baseline runs
	// on its own and, here, fails — a repository can go red between two profiles.
	changed := s.setProfile(t, "v9h-profile-changed", exitCommand("1"))
	if changed.ProfileVersion != 2 || changed.BaselineJobsEnqueued != 1 {
		t.Fatalf("changed profile = %+v, want version 2 with a fresh baseline started", changed)
	}
	failed := s.waitForBaseline(t, readinesscheck.BaselineFail)
	if failed.Attempt == nil || failed.Attempt.ProfileVersion != 2 {
		t.Fatalf("baseline after the change = %+v, want an attempt of profile version 2", failed)
	}
	if err := s.markReady(t, "v9h-ready-after-change"); err == nil {
		t.Fatal("MarkWorkItemReady succeeded on the strength of a baseline that ran for the previous profile")
	}

	// Fix the repository (here: the profile again) and verify: PASS, READY.
	s.setProfile(t, "v9h-profile-fixed", exitCommand("0"))
	s.waitForBaseline(t, readinesscheck.BaselinePass)
	if err := s.markReady(t, "v9h-ready-fixed"); err != nil {
		t.Fatalf("MarkWorkItemReady after the baseline passed: %v", err)
	}
}

func TestV9AcceptBaselineGate_VerifyRunsTheBaselineAgain(t *testing.T) {
	s := newBaselineGateScenario(t)
	s.setProfile(t, "v9h-profile", exitCommand("0"))
	s.waitForBaseline(t, readinesscheck.BaselinePass)

	result, err := readinesscheck.RequestBaselineCheck(context.Background(), s.f.uow, s.f.ids,
		testCmd("v9h-verify", ports.ProjectScope(v5AcceptProjectID), "RequestBaselineCheck"),
		readinesscheck.RequestBaselineCheckRequest{ProjectID: v5AcceptProjectID, RepositoryID: v5AcceptRepositoryID})
	if err != nil || result.BaselineJobsEnqueued != 1 {
		t.Fatalf("RequestBaselineCheck = %+v (%v), want one baseline job", result, err)
	}
	deadline := time.Now().Add(pollDeadline)
	for time.Now().Before(deadline) {
		var attempts []ports.BaselineAttempt
		if err := s.f.uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
			all, err := tx.Work().ListRepositoryWorkspacesForRepository(context.Background(), v5AcceptRepositoryID)
			if err != nil || len(all) != 1 {
				return errors.New("expected exactly one repository workspace")
			}
			attempts, err = tx.Readiness().ListBaselineAttempts(context.Background(), string(all[0].ID))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if len(attempts) == 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("verify did not append a second baseline attempt within the deadline")
}
