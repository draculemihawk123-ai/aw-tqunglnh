package readinesscheck_test

import (
	"context"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/readinesscheck"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
)

// V9-08 against real sqlite: the operator commands on the real tables
// (migration 0047), and the state they produce read back through the real
// repository.

func TestOperatorCommands_RealSQLite_ProfileVersionExceptionAndAudit(t *testing.T) {
	ctx := context.Background()
	store := openReadinessCheckTestStore(t, "operator-sqlite.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	rw := seedSQLiteReadyRepositoryWorkspace(t, uow, "project-1", "repo-1")

	cmd := func(commandType, key string) ports.Command {
		return operatorCommand("project-1", commandType, key)
	}
	profile, err := readinesscheck.SetRepositoryReadinessProfile(ctx, uow, ids, cmd("SetRepositoryReadinessProfile", "set-1"),
		readinesscheck.SetRepositoryReadinessProfileRequest{
			ProjectID: "project-1", RepositoryID: "repo-1",
			Setup:        &readinesscheck.CommandInput{Executable: "npm", Argv: []string{"ci"}, TimeoutSeconds: 600},
			Verification: verificationInput,
		})
	if err != nil || profile.ProfileVersion != 1 || profile.BaselineJobsEnqueued != 1 {
		t.Fatalf("SetRepositoryReadinessProfile = %+v (%v), want version 1 and the baseline of the READY workspace started", profile, err)
	}

	// A baseline attempt of that profile version, recorded the way the handler does.
	job := seedRealBaselineJob(t, uow, ids, "project-1", "repo-1", string(rw.WorkspaceSetID), string(rw.ID))
	exit := 1
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Readiness().RecordBaselineAttempt(ctx, ports.RecordBaselineAttemptRequest{
			ID: "attempt-red", ProjectID: "project-1", RepositoryWorkspaceID: string(rw.ID), RepositoryID: "repo-1", JobID: string(job.ID),
			Stage: readiness.StageVerification, Outcome: readiness.BaselineRed, ExitCode: &exit, ProfileVersion: 1,
			StderrExcerpt: "2 tests failed",
		})
		return err
	}); err != nil {
		t.Fatalf("record attempt: %v", err)
	}

	view, err := readinesscheck.GetRepositoryReadiness(ctx, uow, "project-1", "repo-1")
	if err != nil || view.Profile == nil || view.Profile.Setup == nil || view.Profile.Setup.Executable != "npm" || len(view.Workspaces) != 1 {
		t.Fatalf("view = %+v (%v), want the profile with its setup command and one workspace", view, err)
	}
	if got := view.Workspaces[0]; got.BaselineState != string(readinesscheck.BaselineFail) || got.Attempt == nil ||
		got.Attempt.FailureKind != "PRE_EXISTING_FAILURE" || got.Attempt.ProfileVersion != 1 || got.Attempt.StderrExcerpt != "2 tests failed" {
		t.Fatalf("workspace = %+v, want FAIL with the classified attempt of profile version 1", got)
	}

	accepted, err := readinesscheck.AcceptBaselineException(ctx, uow, ids, cmd("AcceptBaselineException", "accept-1"),
		readinesscheck.AcceptBaselineExceptionRequest{ProjectID: "project-1", RepositoryID: "repo-1", BaselineAttemptID: "attempt-red", Reason: "  legacy suite  "})
	if err != nil || accepted.AcceptedBy != "operator-1" {
		t.Fatalf("AcceptBaselineException = %+v (%v)", accepted, err)
	}
	// The same exception under another key is the same fact, not a second row.
	again, err := readinesscheck.AcceptBaselineException(ctx, uow, ids, cmd("AcceptBaselineException", "accept-2"),
		readinesscheck.AcceptBaselineExceptionRequest{ProjectID: "project-1", RepositoryID: "repo-1", BaselineAttemptID: "attempt-red", Reason: "second try"})
	if err != nil || again.ExceptionID != accepted.ExceptionID {
		t.Fatalf("second acceptance = %+v (%v), want the stored exception %s", again, err, accepted.ExceptionID)
	}
	view, err = readinesscheck.GetRepositoryReadiness(ctx, uow, "project-1", "repo-1")
	if err != nil {
		t.Fatal(err)
	}
	got := view.Workspaces[0]
	if got.BaselineState != string(readinesscheck.BaselineExceptionAccepted) || got.Exception == nil ||
		got.Exception.Reason != "legacy suite" || got.Exception.AcceptedBy != "operator-1" || got.Exception.AcceptedAt.IsZero() {
		t.Fatalf("workspace after the exception = %+v, want EXCEPTION_ACCEPTED carrying who, when and the trimmed reason", got)
	}

	// Changing the profile bumps the version and makes the accepted attempt stale.
	changed, err := readinesscheck.SetRepositoryReadinessProfile(ctx, uow, ids, cmd("SetRepositoryReadinessProfile", "set-2"),
		readinesscheck.SetRepositoryReadinessProfileRequest{ProjectID: "project-1", RepositoryID: "repo-1", Verification: verificationInput})
	if err != nil || changed.ProfileVersion != 2 {
		t.Fatalf("changed profile = %+v (%v), want version 2", changed, err)
	}
	view, _ = readinesscheck.GetRepositoryReadiness(ctx, uow, "project-1", "repo-1")
	if view.Workspaces[0].BaselineState != string(readinesscheck.BaselinePending) || view.Profile.Setup != nil {
		t.Fatalf("after the change: %+v, want PENDING and the setup command removed", view)
	}
	if _, err := readinesscheck.AcceptBaselineException(ctx, uow, ids, cmd("AcceptBaselineException", "accept-3"),
		readinesscheck.AcceptBaselineExceptionRequest{ProjectID: "project-1", RepositoryID: "repo-1", BaselineAttemptID: "attempt-red", Reason: "x"}); !errors.Is(err, readinesscheck.ErrBaselineAttemptNotAcceptable) {
		// The earlier acceptance replays by attempt, but a NEW command on a stale attempt is refused.
		t.Fatalf("accepting a stale attempt = %v, want ErrBaselineAttemptNotAcceptable", err)
	}
}

func TestOperatorCommands_RealSQLite_ReplayAndForeignProject(t *testing.T) {
	ctx := context.Background()
	store := openReadinessCheckTestStore(t, "operator-sqlite-replay.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	seedSQLiteReadyRepositoryWorkspace(t, uow, "project-1", "repo-1")

	request := readinesscheck.SetRepositoryReadinessProfileRequest{ProjectID: "project-1", RepositoryID: "repo-1", Verification: verificationInput}
	set := operatorCommand("project-1", "SetRepositoryReadinessProfile", "set-1")
	first, err := readinesscheck.SetRepositoryReadinessProfile(ctx, uow, ids, set, request)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := readinesscheck.SetRepositoryReadinessProfile(ctx, uow, ids, set, request)
	if err != nil || replayed != first {
		t.Fatalf("replay = %+v (%v), want %+v", replayed, err, first)
	}
	conflicting := set
	conflicting.RequestHash = "another-hash"
	if _, err := readinesscheck.SetRepositoryReadinessProfile(ctx, uow, ids, conflicting, request); !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("same key, different request = %v, want ErrReceiptConflict", err)
	}
	if _, err := readinesscheck.GetRepositoryReadiness(ctx, uow, "project-2", "repo-1"); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("reading another project's repository = %v, want not found", err)
	}
}
