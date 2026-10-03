package readinesscheck_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/readinesscheck"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// V9-08 — the operator surface over the readiness profile and baseline: the
// commands CLI, HTTP and the UI call, and the baseline state they show.

func operatorCommand(projectID, commandType, key string) ports.Command {
	return ports.Command{
		ID: commandType + "-" + key, IdempotencyKey: key, Actor: "operator-1", CorrelationID: key,
		Scope: ports.ProjectScope(projectID), Type: commandType, RequestHash: "hash-" + key,
		RequestedAt: time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC),
	}
}

var verificationInput = readinesscheck.CommandInput{Executable: "npm", Argv: []string{"test"}, TimeoutSeconds: 300}

func setProfile(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, key string) readinesscheck.SetRepositoryReadinessProfileResult {
	t.Helper()
	result, err := readinesscheck.SetRepositoryReadinessProfile(context.Background(), uow, ids, operatorCommand("project-1", "SetRepositoryReadinessProfile", key),
		readinesscheck.SetRepositoryReadinessProfileRequest{ProjectID: "project-1", RepositoryID: "repo-1", Verification: verificationInput})
	if err != nil {
		t.Fatalf("SetRepositoryReadinessProfile(%s): %v", key, err)
	}
	return result
}

// recordAttempt writes a baseline attempt for rw directly, as the handler would.
func recordAttempt(t *testing.T, uow *fake.UnitOfWork, id, rwID string, profileVersion uint64, outcome readiness.BaselineOutcome) {
	t.Helper()
	exit := 0
	if outcome == readiness.BaselineRed {
		exit = 1
	}
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Readiness().RecordBaselineAttempt(context.Background(), ports.RecordBaselineAttemptRequest{
			ID: id, ProjectID: "project-1", RepositoryWorkspaceID: rwID, RepositoryID: "repo-1", JobID: "job-" + id,
			Stage: readiness.StageVerification, Outcome: outcome, ExitCode: &exit, ProfileVersion: profileVersion,
		})
		return err
	}); err != nil {
		t.Fatalf("record baseline attempt %s: %v", id, err)
	}
}

func readinessOf(t *testing.T, uow *fake.UnitOfWork) readinesscheck.RepositoryReadinessView {
	t.Helper()
	view, err := readinesscheck.GetRepositoryReadiness(context.Background(), uow, "project-1", "repo-1")
	if err != nil {
		t.Fatalf("GetRepositoryReadiness: %v", err)
	}
	return view
}

func hasBaselineJob(t *testing.T, uow *fake.UnitOfWork, rwID string) bool {
	t.Helper()
	var active bool
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		active, err = tx.Jobs().HasActiveJobForAggregateIDs(context.Background(), []string{rwID})
		return err
	}); err != nil {
		t.Fatalf("HasActiveJobForAggregateIDs: %v", err)
	}
	return active
}

func TestSetRepositoryReadinessProfile_StartsABaselineOnEveryReadyWorkspaceAndBumpsTheVersion(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	rw := mustSeedReadyRepositoryWorkspace(t, uow, ids, "project-1", "repo-1")
	if hasBaselineJob(t, uow, string(rw.ID)) {
		t.Fatal("a baseline job exists before any profile was set")
	}

	first := setProfile(t, uow, ids, "set-1")
	if first.ProfileVersion != 1 || first.BaselineJobsEnqueued != 1 {
		t.Fatalf("first result = %+v, want version 1 and one baseline job", first)
	}
	if !hasBaselineJob(t, uow, string(rw.ID)) {
		t.Fatal("setting the profile enqueued no baseline job for the READY workspace")
	}
	second := setProfile(t, uow, ids, "set-2")
	if second.ProfileVersion != 2 || second.BaselineJobsEnqueued != 1 {
		t.Fatalf("a changed profile must start a fresh baseline: %+v", second)
	}
}

func TestSetRepositoryReadinessProfile_ReplayDoesNotEnqueueAgainOrBumpTheVersion(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustSeedReadyRepositoryWorkspace(t, uow, ids, "project-1", "repo-1")
	first := setProfile(t, uow, ids, "set-1")
	replayed := setProfile(t, uow, ids, "set-1")
	if replayed != first {
		t.Fatalf("replay = %+v, want the first result %+v", replayed, first)
	}
	profile, err := readinesscheck.GetReadinessProfile(context.Background(), uow, "repo-1")
	if err != nil || profile.Version != 1 {
		t.Fatalf("profile after a replay = %+v (%v), want version 1", profile, err)
	}
}

func TestSetRepositoryReadinessProfile_RejectsInvalidCommandsAndForeignRepositories(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustSeedReadyRepositoryWorkspace(t, uow, ids, "project-1", "repo-1")

	_, err := readinesscheck.SetRepositoryReadinessProfile(context.Background(), uow, ids, operatorCommand("project-1", "SetRepositoryReadinessProfile", "bad"),
		readinesscheck.SetRepositoryReadinessProfileRequest{ProjectID: "project-1", RepositoryID: "repo-1", Verification: readinesscheck.CommandInput{Executable: "npm"}})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("a verification command without a timeout was accepted or mis-reported: %v", err)
	}
	_, err = readinesscheck.SetRepositoryReadinessProfile(context.Background(), uow, ids, operatorCommand("project-2", "SetRepositoryReadinessProfile", "foreign"),
		readinesscheck.SetRepositoryReadinessProfileRequest{ProjectID: "project-2", RepositoryID: "repo-1", Verification: verificationInput})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("setting the profile of another project's repository = %v, want not found", err)
	}
}

func TestEvaluateBaseline_PendingThenPassThenFailThenExceptionAccepted(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	rw := mustSeedReadyRepositoryWorkspace(t, uow, ids, "project-1", "repo-1")
	stateOf := func() readinesscheck.WorkspaceBaselineView {
		t.Helper()
		view := readinessOf(t, uow)
		if len(view.Workspaces) != 1 {
			t.Fatalf("workspaces = %+v, want exactly the seeded one", view.Workspaces)
		}
		return view.Workspaces[0]
	}

	if got := stateOf(); got.BaselineState != string(readinesscheck.BaselineNotRequired) || !got.AdmitsWriters {
		t.Fatalf("no profile: %+v, want NOT_REQUIRED admitting writers", got)
	}

	setProfile(t, uow, ids, "set-1")
	if got := stateOf(); got.BaselineState != string(readinesscheck.BaselinePending) || got.AdmitsWriters {
		t.Fatalf("profile set, baseline not run: %+v, want PENDING blocking writers", got)
	}

	recordAttempt(t, uow, "attempt-1", string(rw.ID), 1, readiness.BaselineGreen)
	if got := stateOf(); got.BaselineState != string(readinesscheck.BaselinePass) || !got.AdmitsWriters {
		t.Fatalf("green baseline: %+v, want PASS admitting writers", got)
	}

	// A changed profile makes the green attempt stale: the baseline vouches for
	// the profile it ran, not for its successor.
	setProfile(t, uow, ids, "set-2")
	if got := stateOf(); got.BaselineState != string(readinesscheck.BaselinePending) || got.AdmitsWriters {
		t.Fatalf("profile changed after a green baseline: %+v, want PENDING", got)
	}

	recordAttempt(t, uow, "attempt-2", string(rw.ID), 2, readiness.BaselineRed)
	failed := stateOf()
	if failed.BaselineState != string(readinesscheck.BaselineFail) || failed.AdmitsWriters || failed.Attempt == nil || failed.Attempt.FailureKind != string(readiness.FailurePreExisting) {
		t.Fatalf("red baseline: %+v, want FAIL, PRE_EXISTING_FAILURE, writers blocked", failed)
	}

	if _, err := readinesscheck.AcceptBaselineException(context.Background(), uow, ids, operatorCommand("project-1", "AcceptBaselineException", "accept-1"),
		readinesscheck.AcceptBaselineExceptionRequest{ProjectID: "project-1", RepositoryID: "repo-1", BaselineAttemptID: "attempt-2", Reason: "known flaky suite"}); err != nil {
		t.Fatalf("AcceptBaselineException: %v", err)
	}
	accepted := stateOf()
	if accepted.BaselineState != string(readinesscheck.BaselineExceptionAccepted) || !accepted.AdmitsWriters || accepted.Exception == nil ||
		accepted.Exception.AcceptedBy != "operator-1" || accepted.Exception.Reason != "known flaky suite" {
		t.Fatalf("after the exception: %+v, want EXCEPTION_ACCEPTED by operator-1 admitting writers", accepted)
	}
	if accepted.Attempt == nil || accepted.Attempt.Outcome != string(readiness.BaselineRed) {
		t.Fatalf("the failed attempt must stay on the record after the exception: %+v", accepted.Attempt)
	}

	// The exception covers attempt-2 only: the next failing attempt is a new
	// failure and needs its own acceptance.
	recordAttempt(t, uow, "attempt-3", string(rw.ID), 2, readiness.BaselineEnvironmentError)
	if got := stateOf(); got.BaselineState != string(readinesscheck.BaselineFail) || got.Attempt.FailureKind != string(readiness.FailureEnvironment) {
		t.Fatalf("a later failing attempt: %+v, want FAIL (ENVIRONMENT_ERROR)", got)
	}
}

func TestAcceptBaselineException_Preconditions(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	rw := mustSeedReadyRepositoryWorkspace(t, uow, ids, "project-1", "repo-1")
	setProfile(t, uow, ids, "set-1")
	recordAttempt(t, uow, "green", string(rw.ID), 1, readiness.BaselineGreen)
	recordAttempt(t, uow, "red-v1", string(rw.ID), 1, readiness.BaselineRed)
	setProfile(t, uow, ids, "set-2")

	accept := func(key, attempt, reason string) error {
		_, err := readinesscheck.AcceptBaselineException(context.Background(), uow, ids, operatorCommand("project-1", "AcceptBaselineException", key),
			readinesscheck.AcceptBaselineExceptionRequest{ProjectID: "project-1", RepositoryID: "repo-1", BaselineAttemptID: attempt, Reason: reason})
		return err
	}
	if err := accept("a1", "green", "because"); !errors.Is(err, readinesscheck.ErrBaselineAttemptNotAcceptable) {
		t.Fatalf("accepting a passing attempt = %v, want ErrBaselineAttemptNotAcceptable", err)
	}
	if err := accept("a2", "red-v1", "because"); !errors.Is(err, readinesscheck.ErrBaselineAttemptNotAcceptable) {
		t.Fatalf("accepting an attempt of a superseded profile version = %v, want ErrBaselineAttemptNotAcceptable", err)
	}
	if err := accept("a3", "no-such-attempt", "because"); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("accepting an unknown attempt = %v, want not found", err)
	}
	recordAttempt(t, uow, "red-v2", string(rw.ID), 2, readiness.BaselineRed)
	if err := accept("a4", "red-v2", "   "); err == nil {
		t.Fatal("an exception without a reason was accepted")
	}
	if err := accept("a5", "red-v2", "tracked in TODO-1"); err != nil {
		t.Fatalf("accepting the current failed attempt: %v", err)
	}
	// Accepting again with another key is the same fact, not a second record.
	if err := accept("a6", "red-v2", "tracked in TODO-1"); err != nil {
		t.Fatalf("accepting an already accepted attempt: %v", err)
	}
	// A repository of another project cannot be reached by naming its attempt.
	_, err := readinesscheck.AcceptBaselineException(context.Background(), uow, ids, operatorCommand("project-2", "AcceptBaselineException", "a7"),
		readinesscheck.AcceptBaselineExceptionRequest{ProjectID: "project-2", RepositoryID: "repo-1", BaselineAttemptID: "red-v2", Reason: "x"})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("accepting through another project = %v, want not found", err)
	}
}

func TestRequestBaselineCheck_NeedsAProfileAndStartsAFreshJobPerRequest(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	rw := mustSeedReadyRepositoryWorkspace(t, uow, ids, "project-1", "repo-1")
	verify := func(key string) (readinesscheck.RequestBaselineCheckResult, error) {
		return readinesscheck.RequestBaselineCheck(context.Background(), uow, ids, operatorCommand("project-1", "RequestBaselineCheck", key),
			readinesscheck.RequestBaselineCheckRequest{ProjectID: "project-1", RepositoryID: "repo-1"})
	}
	if _, err := verify("v0"); !errors.Is(err, readinesscheck.ErrNoReadinessProfile) {
		t.Fatalf("verify without a profile = %v, want ErrNoReadinessProfile", err)
	}
	setProfile(t, uow, ids, "set-1")
	result, err := verify("v1")
	if err != nil || result.BaselineJobsEnqueued != 1 || result.ProfileVersion != 1 {
		t.Fatalf("verify = %+v (%v), want one job for profile version 1", result, err)
	}
	// The same key replays; a new key is a new request that must not collide with
	// the job the profile change already enqueued.
	if replay, err := verify("v1"); err != nil || replay != result {
		t.Fatalf("replayed verify = %+v (%v), want %+v", replay, err, result)
	}
	if again, err := verify("v2"); err != nil || again.BaselineJobsEnqueued != 1 {
		t.Fatalf("a second verify = %+v (%v), want a fresh job", again, err)
	}
	_ = rw
}

func TestEnqueueBaselineForNewWorkspace_OnlyWhenTheRepositoryHasAProfile(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	rw := mustSeedReadyRepositoryWorkspace(t, uow, ids, "project-1", "repo-1")
	enqueue := func() {
		t.Helper()
		if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
			return readinesscheck.EnqueueBaselineForNewWorkspace(context.Background(), tx, ids, "project-1", rw, time.Now().UTC())
		}); err != nil {
			t.Fatalf("EnqueueBaselineForNewWorkspace: %v", err)
		}
	}
	enqueue()
	if hasBaselineJob(t, uow, string(rw.ID)) {
		t.Fatal("a repository without a profile got a baseline job")
	}
	mustSetProfile(t, uow, "repo-1", nil, mustVerificationSpec(t))
	enqueue()
	if !hasBaselineJob(t, uow, string(rw.ID)) {
		t.Fatal("a new workspace of a profiled repository got no baseline job")
	}
}

func TestWriterAdmissionProblems_OnlyWriteScopesOfProfiledRepositoriesCount(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	rw := mustSeedReadyRepositoryWorkspace(t, uow, ids, "project-1", "repo-1")
	scope := func(access work.RepositoryAccess) []work.RepositoryScope {
		s, err := work.NewRepositoryScope("family-repo-1", 1, "repo-1", access, nil, "test", "operator-1", time.Now().UTC())
		if err != nil {
			t.Fatalf("NewRepositoryScope: %v", err)
		}
		return []work.RepositoryScope{s}
	}
	problems := func(scopes []work.RepositoryScope) []string {
		t.Helper()
		var out []string
		if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
			var err error
			out, err = readinesscheck.WriterAdmissionProblems(context.Background(), tx, "family-repo-1", scopes)
			return err
		}); err != nil {
			t.Fatalf("WriterAdmissionProblems: %v", err)
		}
		return out
	}

	if got := problems(scope(work.RepositoryWrite)); len(got) != 0 {
		t.Fatalf("no profile: %v, want no problem", got)
	}
	setProfile(t, uow, ids, "set-1")
	if got := problems(scope(work.RepositoryRead)); len(got) != 0 {
		t.Fatalf("read-only access to a profiled repository: %v, want no problem", got)
	}
	if got := problems(scope(work.RepositoryWrite)); len(got) != 1 || !strings.Contains(got[0], "baseline pending") {
		t.Fatalf("profile with no baseline yet: %v, want one 'baseline pending' problem", got)
	}
	recordAttempt(t, uow, "attempt-1", string(rw.ID), 1, readiness.BaselineRed)
	if got := problems(scope(work.RepositoryWrite)); len(got) != 1 || !strings.Contains(got[0], "baseline FAILED") || !strings.Contains(got[0], "PRE_EXISTING_FAILURE") {
		t.Fatalf("red baseline: %v, want one 'baseline FAILED (PRE_EXISTING_FAILURE)' problem", got)
	}
	recordAttempt(t, uow, "attempt-2", string(rw.ID), 1, readiness.BaselineGreen)
	if got := problems(scope(work.RepositoryWrite)); len(got) != 0 {
		t.Fatalf("a later green baseline: %v, want no problem", got)
	}
}
