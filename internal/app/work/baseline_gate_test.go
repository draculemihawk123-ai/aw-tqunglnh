package work_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/readinesscheck"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// V9-08 (gap G8) — "WorkItem có quyền WRITE chỉ READY khi baseline PASS, hoặc
// người vận hành chấp nhận ngoại lệ có audit". The same readiness question is
// asked by ExplainWorkItemReadiness (read-only) and MarkWorkItemReady (the
// BACKLOG -> READY command), so the tests check both give the same answer.

type baselineGateFixture struct {
	uow  *fake.UnitOfWork
	ids  idsource.Source
	rwID string
}

// newBaselineGateFixture seeds a project with one repository, a complete
// (ready-eligible) root work item that may WRITE to it, and a READY workspace.
func newBaselineGateFixture(t *testing.T, access workdomain.RepositoryAccess) baselineGateFixture {
	t.Helper()
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	const projectID, repositoryID, familyID, itemID = "project-1", "repo-1", "family-1", "work-1"
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project"}); err != nil {
			return err
		}
		if _, err := tx.Catalog().RegisterRepository(ctx, ports.RegisterRepositoryRequest{
			ID: repositoryID, ProjectID: projectID, Name: "repo", RemoteLocator: "/fixture/repo", DefaultRef: "main",
		}); err != nil {
			return err
		}
		item := workdomain.WorkItem{
			ID: itemID, ProjectID: projectID, Kind: workdomain.WorkItemRoot, FamilyID: familyID, SchemaVersion: 1, Title: "Ship it",
			Behavior:           "Users can do the thing",
			AcceptanceCriteria: []workdomain.AcceptanceCriterion{{Description: "works", VerificationRef: "go test ./..."}},
			VerificationSpec:   "run the suite", RiskLevel: workdomain.RiskLevel("MEDIUM"), Status: workdomain.WorkItemBacklog, Version: 1,
		}
		family, err := workdomain.NewTaskFamily(familyID, item)
		if err != nil {
			return err
		}
		if _, err := tx.Work().CreateTaskFamily(ctx, family); err != nil {
			return err
		}
		if _, err := tx.Work().CreateWorkItem(ctx, item); err != nil {
			return err
		}
		scope, err := workdomain.NewRepositoryScope(familyID, 1, repositoryID, access, nil, "root task", "operator-1", time.Now().UTC())
		if err != nil {
			return err
		}
		if _, err := tx.Work().AddRepositoryScope(ctx, scope); err != nil {
			return err
		}
		if _, err := tx.Work().AddEffectiveScope(ctx, itemID, scope); err != nil {
			return err
		}
		set, err := workspace.NewWorkspaceSet("set-1", family)
		if err != nil {
			return err
		}
		if _, err := tx.Work().CreateWorkspaceSet(ctx, set); err != nil {
			return err
		}
		repo, err := tx.Catalog().GetRepository(ctx, repositoryID)
		if err != nil {
			return err
		}
		rw, err := workspace.NewRepositoryWorkspace("rw-1", set, repo, 1, "ws_repo", "", "deadbeef")
		if err != nil {
			return err
		}
		rw.State = workspace.RepositoryWorkspaceReady
		_, err = tx.Work().CreateRepositoryWorkspace(ctx, rw)
		return err
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return baselineGateFixture{uow: uow, ids: ids, rwID: "rw-1"}
}

func (f baselineGateFixture) setProfile(t *testing.T, key string) {
	t.Helper()
	cmd := ports.Command{
		ID: "set-" + key, IdempotencyKey: key, Actor: "operator-1", Scope: ports.ProjectScope("project-1"),
		Type: "SetRepositoryReadinessProfile", RequestHash: "hash-" + key, RequestedAt: time.Now().UTC(),
	}
	if _, err := readinesscheck.SetRepositoryReadinessProfile(context.Background(), f.uow, f.ids, cmd, readinesscheck.SetRepositoryReadinessProfileRequest{
		ProjectID: "project-1", RepositoryID: "repo-1",
		Verification: readinesscheck.CommandInput{Executable: "npm", Argv: []string{"test"}, TimeoutSeconds: 60},
	}); err != nil {
		t.Fatalf("set profile: %v", err)
	}
}

func (f baselineGateFixture) recordAttempt(t *testing.T, id string, version uint64, outcome readiness.BaselineOutcome) {
	t.Helper()
	exit := 0
	if outcome == readiness.BaselineRed {
		exit = 1
	}
	if err := f.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Readiness().RecordBaselineAttempt(context.Background(), ports.RecordBaselineAttemptRequest{
			ID: id, ProjectID: "project-1", RepositoryWorkspaceID: f.rwID, RepositoryID: "repo-1", JobID: "job-" + id,
			Stage: readiness.StageVerification, Outcome: outcome, ExitCode: &exit, ProfileVersion: version,
		})
		return err
	}); err != nil {
		t.Fatalf("record attempt: %v", err)
	}
}

func (f baselineGateFixture) acceptException(t *testing.T, attemptID string) {
	t.Helper()
	cmd := ports.Command{
		ID: "accept-" + attemptID, IdempotencyKey: "accept-" + attemptID, Actor: "operator-1", Scope: ports.ProjectScope("project-1"),
		Type: "AcceptBaselineException", RequestHash: "hash-accept-" + attemptID, RequestedAt: time.Now().UTC(),
	}
	if _, err := readinesscheck.AcceptBaselineException(context.Background(), f.uow, f.ids, cmd, readinesscheck.AcceptBaselineExceptionRequest{
		ProjectID: "project-1", RepositoryID: "repo-1", BaselineAttemptID: attemptID, Reason: "known flaky suite",
	}); err != nil {
		t.Fatalf("accept exception: %v", err)
	}
}

// markReady runs MarkWorkItemReady and returns the readiness problems it
// rejected with (nil when it succeeded) alongside what ExplainWorkItemReadiness
// says for the same work item, which must agree.
func (f baselineGateFixture) markReady(t *testing.T, key string) (problems []string, ready bool) {
	t.Helper()
	ctx := context.Background()
	explained, err := work.ExplainWorkItemReadiness(ctx, f.uow, ports.ProjectScope("project-1"), "work-1")
	if err != nil {
		t.Fatalf("ExplainWorkItemReadiness: %v", err)
	}
	_, err = work.MarkWorkItemReady(ctx, f.uow, markReadyCmd(key, "hash-"+key, "project-1"), work.MarkWorkItemReadyRequest{WorkItemID: "work-1"})
	var readinessErr *workdomain.ReadinessError
	switch {
	case err == nil:
		if !explained.Ready {
			t.Fatalf("MarkWorkItemReady succeeded but ExplainWorkItemReadiness said not ready: %v", explained.Problems)
		}
		return nil, true
	case errors.As(err, &readinessErr):
		if explained.Ready || !reflect.DeepEqual(explained.Problems, readinessErr.Problems) {
			t.Fatalf("Explain (%v ready=%v) and MarkWorkItemReady (%v) disagree", explained.Problems, explained.Ready, readinessErr.Problems)
		}
		return readinessErr.Problems, false
	default:
		t.Fatalf("MarkWorkItemReady: %v", err)
		return nil, false
	}
}

func TestMarkWorkItemReady_NoReadinessProfile_BehavesAsBefore(t *testing.T) {
	f := newBaselineGateFixture(t, workdomain.RepositoryWrite)
	if problems, ready := f.markReady(t, "k1"); !ready {
		t.Fatalf("a repository without a readiness profile gated a writer: %v", problems)
	}
}

func TestMarkWorkItemReady_WriterIsHeldUntilTheBaselinePasses(t *testing.T) {
	f := newBaselineGateFixture(t, workdomain.RepositoryWrite)
	f.setProfile(t, "set-1")

	problems, ready := f.markReady(t, "k1")
	if ready || len(problems) != 1 || !strings.Contains(problems[0], "baseline pending") {
		t.Fatalf("before the baseline ran: ready=%v problems=%v, want one 'baseline pending' problem", ready, problems)
	}

	f.recordAttempt(t, "attempt-1", 1, readiness.BaselineGreen)
	if problems, ready := f.markReady(t, "k2"); !ready {
		t.Fatalf("after a passing baseline: not ready: %v", problems)
	}
}

func TestMarkWorkItemReady_FailedBaselineBlocksTheWriterUntilAnOperatorAcceptsIt(t *testing.T) {
	f := newBaselineGateFixture(t, workdomain.RepositoryWrite)
	f.setProfile(t, "set-1")
	f.recordAttempt(t, "attempt-red", 1, readiness.BaselineRed)

	problems, ready := f.markReady(t, "k1")
	if ready || len(problems) != 1 || !strings.Contains(problems[0], "baseline FAILED (PRE_EXISTING_FAILURE, attempt attempt-red)") ||
		!strings.Contains(problems[0], "accept-exception") {
		t.Fatalf("red baseline: ready=%v problems=%v, want a FAILED (PRE_EXISTING_FAILURE) problem that names the way out", ready, problems)
	}

	f.acceptException(t, "attempt-red")
	if problems, ready := f.markReady(t, "k2"); !ready {
		t.Fatalf("after the operator accepted the failure: not ready: %v", problems)
	}
}

func TestMarkWorkItemReady_AChangedProfileHoldsTheWriterAgain(t *testing.T) {
	f := newBaselineGateFixture(t, workdomain.RepositoryWrite)
	f.setProfile(t, "set-1")
	f.recordAttempt(t, "attempt-1", 1, readiness.BaselineGreen)
	f.setProfile(t, "set-2")
	if problems, ready := f.markReady(t, "k1"); ready || len(problems) != 1 || !strings.Contains(problems[0], "profile version 2") {
		t.Fatalf("after the profile changed: ready=%v problems=%v, want pending for profile version 2", ready, problems)
	}
}

func TestMarkWorkItemReady_ReadOnlyAccessIsNeverGatedByTheBaseline(t *testing.T) {
	f := newBaselineGateFixture(t, workdomain.RepositoryRead)
	f.setProfile(t, "set-1")
	f.recordAttempt(t, "attempt-red", 1, readiness.BaselineRed)
	if problems, ready := f.markReady(t, "k1"); !ready {
		t.Fatalf("a read-only work item was gated by a failed baseline: %v", problems)
	}
}
