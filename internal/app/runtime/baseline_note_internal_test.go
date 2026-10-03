package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// V9-08 (HE-12-M03: "regression không được che bởi lỗi có sẵn") — what a maker
// sent back by a failing check is told about the repository's baseline, and
// that the note is a function of the snapshot's creation time.

func baselineNoteFixture(t *testing.T) (*fake.UnitOfWork, []workdomain.RepositoryScope) {
	t.Helper()
	ctx := context.Background()
	uow := fake.New()
	const projectID, repositoryID, familyID = "project-1", "repo-1", "family-1"
	var scopes []workdomain.RepositoryScope
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project"}); err != nil {
			return err
		}
		if _, err := tx.Catalog().RegisterRepository(ctx, ports.RegisterRepositoryRequest{
			ID: repositoryID, ProjectID: projectID, Name: "repo", RemoteLocator: "/fixture/repo", DefaultRef: "main",
		}); err != nil {
			return err
		}
		item, err := workdomain.NewRootWorkItem("work-1", projectID, familyID, "root")
		if err != nil {
			return err
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
		if _, err := tx.Work().CreateRepositoryWorkspace(ctx, rw); err != nil {
			return err
		}
		write, err := workdomain.NewRepositoryScope(familyID, 1, repositoryID, workdomain.RepositoryWrite, nil, "root", "operator-1", time.Now().UTC())
		if err != nil {
			return err
		}
		scopes = []workdomain.RepositoryScope{write}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return uow, scopes
}

func TestBaselineNoteAt(t *testing.T) {
	ctx := context.Background()
	uow, scopes := baselineNoteFixture(t)
	note := func(at time.Time) string {
		t.Helper()
		var out string
		if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			out, err = baselineNoteAt(ctx, tx, "family-1", scopes, at)
			return err
		}); err != nil {
			t.Fatalf("baselineNoteAt: %v", err)
		}
		return out
	}
	record := func(id string, outcome readiness.BaselineOutcome) {
		t.Helper()
		exit := 0
		if outcome == readiness.BaselineRed {
			exit = 1
		}
		if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			_, err := tx.Readiness().RecordBaselineAttempt(ctx, ports.RecordBaselineAttemptRequest{
				ID: id, ProjectID: "project-1", RepositoryWorkspaceID: "rw-1", RepositoryID: "repo-1", JobID: "job-" + id,
				Stage: readiness.StageVerification, Outcome: outcome, ExitCode: &exit, ProfileVersion: 1,
			})
			return err
		}); err != nil {
			t.Fatalf("record %s: %v", id, err)
		}
	}

	if got := note(time.Now().Add(time.Hour)); got != "" {
		t.Fatalf("no baseline attempt: note = %q, want none", got)
	}

	record("green", readiness.BaselineGreen)
	time.Sleep(5 * time.Millisecond)
	afterGreen := time.Now()
	if got := note(afterGreen); !strings.Contains(got, "repository repo-1: its baseline passed before this task started") || !strings.Contains(got, "comes from the changes made during this task") {
		t.Fatalf("green baseline: note = %q, want the regression wording", got)
	}

	time.Sleep(5 * time.Millisecond)
	record("red", readiness.BaselineRed)
	time.Sleep(5 * time.Millisecond)
	afterRed := time.Now()
	if got := note(afterRed); !strings.Contains(got, "had already failed before this task started (PRE_EXISTING_FAILURE, attempt red)") ||
		!strings.Contains(got, "not caused by this task") || strings.Contains(got, "accepted") {
		t.Fatalf("red baseline: note = %q, want the pre-existing wording without an acceptance", got)
	}
	// A snapshot created before the red attempt still renders the green wording:
	// the note is a function of the snapshot's creation time.
	if got := note(afterGreen); !strings.Contains(got, "its baseline passed") {
		t.Fatalf("note as of before the red attempt = %q, want the green wording (determinism per snapshot)", got)
	}

	time.Sleep(5 * time.Millisecond)
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Readiness().RecordBaselineException(ctx, ports.RecordBaselineExceptionRequest{
			ID: "exc-1", ProjectID: "project-1", BaselineAttemptID: "red", Reason: "known flaky suite",
			AcceptedBy: "operator-1", AcceptedAt: time.Now().UTC(),
		})
		return err
	}); err != nil {
		t.Fatalf("record exception: %v", err)
	}
	if got := note(time.Now().Add(time.Second)); !strings.Contains(got, "an operator accepted it (known flaky suite)") {
		t.Fatalf("accepted red baseline: note = %q, want the acceptance and its reason", got)
	}
	// An exception accepted after the snapshot was created is not in its note.
	if got := note(afterRed); strings.Contains(got, "accepted") {
		t.Fatalf("note as of before the acceptance = %q, want no acceptance in it", got)
	}
}

func TestBaselineNoteAt_ReadOnlyScopesAndNoWorkspaceAddNothing(t *testing.T) {
	ctx := context.Background()
	uow, _ := baselineNoteFixture(t)
	read, err := workdomain.NewRepositoryScope("family-1", 1, "repo-1", workdomain.RepositoryRead, nil, "r", "operator-1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	other, err := workdomain.NewRepositoryScope("family-1", 1, "repo-other", workdomain.RepositoryWrite, nil, "r", "operator-1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		got, err := baselineNoteAt(ctx, tx, "family-1", []workdomain.RepositoryScope{read, other}, time.Now().Add(time.Hour))
		if err != nil || got != "" {
			t.Fatalf("note for a read-only scope and a repository with no workspace = %q (%v), want none", got, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
