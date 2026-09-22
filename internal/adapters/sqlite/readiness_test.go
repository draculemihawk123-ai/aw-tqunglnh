package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func openReadinessTestStore(t *testing.T, name string) *Store {
	t.Helper()
	store, err := Open(context.Background(), migratedDatabasePath(t, name))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func withReadinessTx(t *testing.T, store *Store, fn func(tx *sql.Tx) error) {
	t.Helper()
	if err := store.RunSerializedWrite(context.Background(), fn); err != nil {
		t.Fatalf("RunSerializedWrite: %v", err)
	}
}

// seedBaselineJob inserts a real durable_jobs row — readiness_baseline_attempts.job_id
// and readiness_environment_blockers.job_id are both real foreign keys
// against durable_jobs(id) (0014_readiness_evidence.sql), mirroring
// repository_probe_attempts.job_id's own identical FK; catalog_test.go's
// own seedProbeJob is the established precedent for seeding one directly
// via Store.EnqueueJob rather than routing through a full command.
func seedBaselineJob(t *testing.T, store *Store, projectID, repositoryWorkspaceID, jobID string) ports.DurableJob {
	t.Helper()
	job, err := store.EnqueueJob(context.Background(), ports.EnqueueJobRequest{
		ID: ports.JobID(jobID), ProjectID: project.ProjectID(projectID), Kind: "BASELINE_EVIDENCE",
		AggregateType: "RepositoryWorkspace", AggregateID: repositoryWorkspaceID, MaxClaims: 3, IdempotencyKey: jobID + "-key",
	})
	if err != nil {
		t.Fatalf("seedBaselineJob: %v", err)
	}
	return job
}

// seedReadyRepositoryWorkspace seeds a Project, Repository, TaskFamily,
// WorkItem, WorkspaceSet and one READY RepositoryWorkspace (generation 1)
// — the minimal real fixture readiness_baseline_attempts/
// readiness_environment_blockers' own foreign keys need. It never routes
// through internal/app/work.CreateRootWorkItem (that package is out of
// this task's own scope) — it builds each domain value directly with the
// same real constructors that command already composes, mirroring
// internal/app/workspaceprovision.Handler's own finishReady building a
// RepositoryWorkspace struct literal directly.
func seedReadyRepositoryWorkspace(t *testing.T, store *Store, projectID, repositoryID string) workspace.RepositoryWorkspace {
	t.Helper()
	ctx := context.Background()
	var rw workspace.RepositoryWorkspace
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		if _, err := (catalogRepository{tx: tx}).CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID}); err != nil {
			return err
		}
		if _, err := (catalogRepository{tx: tx}).RegisterRepository(ctx, ports.RegisterRepositoryRequest{
			ID: repositoryID, ProjectID: projectID, Name: "repo " + repositoryID,
			RemoteLocator: "/fixture/" + repositoryID, DefaultRef: "main",
		}); err != nil {
			return err
		}

		familyID := "family-" + repositoryID
		root, err := work.NewRootWorkItem(work.WorkItemID("work-"+repositoryID), project.ProjectID(projectID), work.TaskFamilyID(familyID), "root")
		if err != nil {
			return err
		}
		family, err := work.NewTaskFamily(work.TaskFamilyID(familyID), root)
		if err != nil {
			return err
		}
		if _, err := (workRepository{tx: tx}).CreateTaskFamily(ctx, family); err != nil {
			return err
		}
		if _, err := (workRepository{tx: tx}).CreateWorkItem(ctx, root); err != nil {
			return err
		}

		set, err := workspace.NewWorkspaceSet(workspace.WorkspaceSetID("set-"+repositoryID), family)
		if err != nil {
			return err
		}
		if _, err := (workRepository{tx: tx}).CreateWorkspaceSet(ctx, set); err != nil {
			return err
		}

		repo, err := (catalogRepository{tx: tx}).GetRepository(ctx, repositoryID)
		if err != nil {
			return err
		}
		built, err := workspace.NewRepositoryWorkspace(
			workspace.RepositoryWorkspaceID("rw-"+repositoryID), set, repo, 1,
			"ws_"+repositoryID, "", "deadbeef",
		)
		if err != nil {
			return err
		}
		built.State = workspace.RepositoryWorkspaceReady
		created, err := (workRepository{tx: tx}).CreateRepositoryWorkspace(ctx, built)
		if err != nil {
			return err
		}
		rw = created
		return nil
	})
	return rw
}

// --- Profile ---

func TestReadinessRepository_SetGetReadinessProfile(t *testing.T) {
	store := openReadinessTestStore(t, "readiness-profile.db")
	ctx := context.Background()
	rw := seedReadyRepositoryWorkspace(t, store, "project-1", "repo-1")

	setup, err := readiness.NewCommandSpec("npm", []string{"ci"}, "", 60)
	if err != nil {
		t.Fatalf("NewCommandSpec setup: %v", err)
	}
	verification, err := readiness.NewCommandSpec("npm", []string{"test"}, "sub", 30)
	if err != nil {
		t.Fatalf("NewCommandSpec verification: %v", err)
	}
	profile, err := readiness.NewProfile(project.RepositoryID("repo-1"), &setup, verification)
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}

	var created readiness.Profile
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		var err error
		created, err = readinessRepository{tx: tx}.SetReadinessProfile(ctx, profile)
		return err
	})
	if created.Version != 1 {
		t.Fatalf("created version = %d, want 1", created.Version)
	}

	var loaded readiness.Profile
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = readinessRepository{tx: tx}.GetReadinessProfile(ctx, "repo-1")
		return err
	})
	if loaded.RepositoryID != "repo-1" || loaded.Version != 1 {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.Setup == nil || loaded.Setup.Executable != "npm" || len(loaded.Setup.Argv) != 1 || loaded.Setup.Argv[0] != "ci" {
		t.Fatalf("loaded setup = %+v", loaded.Setup)
	}
	if loaded.Verification.WorkingDirectory != "sub" || loaded.Verification.TimeoutSeconds != 30 {
		t.Fatalf("loaded verification = %+v", loaded.Verification)
	}

	// Upsert bumps version and replaces the recipe.
	verification2, err := readiness.NewCommandSpec("make", []string{"check"}, "", 45)
	if err != nil {
		t.Fatalf("NewCommandSpec verification2: %v", err)
	}
	profile2, err := readiness.NewProfile(project.RepositoryID("repo-1"), nil, verification2)
	if err != nil {
		t.Fatalf("NewProfile 2: %v", err)
	}
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		var err error
		created, err = readinessRepository{tx: tx}.SetReadinessProfile(ctx, profile2)
		return err
	})
	if created.Version != 2 {
		t.Fatalf("upserted version = %d, want 2", created.Version)
	}
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = readinessRepository{tx: tx}.GetReadinessProfile(ctx, "repo-1")
		return err
	})
	if loaded.Setup != nil {
		t.Fatalf("loaded.Setup = %+v, want nil after upsert dropped it", loaded.Setup)
	}
	if loaded.Verification.Executable != "make" {
		t.Fatalf("loaded.Verification = %+v, want the upserted recipe", loaded.Verification)
	}
	_ = rw
}

func TestReadinessRepository_SetReadinessProfile_UnknownRepository(t *testing.T) {
	store := openReadinessTestStore(t, "readiness-profile-unknown-repo.db")
	ctx := context.Background()
	verification, err := readiness.NewCommandSpec("npm", []string{"test"}, "", 30)
	if err != nil {
		t.Fatalf("NewCommandSpec: %v", err)
	}
	profile, err := readiness.NewProfile(project.RepositoryID("missing-repo"), nil, verification)
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}
	err = store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		_, err := readinessRepository{tx: tx}.SetReadinessProfile(ctx, profile)
		return err
	})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
	}
}

func TestReadinessRepository_GetReadinessProfile_NotFound(t *testing.T) {
	store := openReadinessTestStore(t, "readiness-profile-notfound.db")
	ctx := context.Background()
	seedReadyRepositoryWorkspace(t, store, "project-1", "repo-1")
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		_, err := readinessRepository{tx: tx}.GetReadinessProfile(ctx, "repo-1")
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

// --- BaselineAttempt ---

func TestReadinessRepository_RecordAndListBaselineAttempts(t *testing.T) {
	store := openReadinessTestStore(t, "readiness-attempts.db")
	ctx := context.Background()
	rw := seedReadyRepositoryWorkspace(t, store, "project-1", "repo-1")

	seedBaselineJob(t, store, "project-1", string(rw.ID), "job-1")
	seedBaselineJob(t, store, "project-1", string(rw.ID), "job-2")
	seedBaselineJob(t, store, "project-1", string(rw.ID), "job-3")

	greenExit := 0
	redExit := 1
	errCode := "UNAVAILABLE"
	errMessage := "executable not found"

	requests := []ports.RecordBaselineAttemptRequest{
		{
			ID: "attempt-1", ProjectID: "project-1", RepositoryWorkspaceID: string(rw.ID), RepositoryID: "repo-1",
			JobID: "job-1", Stage: readiness.StageVerification, Outcome: readiness.BaselineGreen,
			ExitCode: &greenExit, DurationMS: 120, StdoutExcerpt: "ok", StderrExcerpt: "",
		},
		{
			ID: "attempt-2", ProjectID: "project-1", RepositoryWorkspaceID: string(rw.ID), RepositoryID: "repo-1",
			JobID: "job-2", Stage: readiness.StageVerification, Outcome: readiness.BaselineRed,
			ExitCode: &redExit, DurationMS: 80, StdoutExcerpt: "", StderrExcerpt: "assertion failed",
		},
		{
			ID: "attempt-3", ProjectID: "project-1", RepositoryWorkspaceID: string(rw.ID), RepositoryID: "repo-1",
			JobID: "job-3", Stage: readiness.StageSetup, Outcome: readiness.BaselineEnvironmentError,
			DurationMS: 5, ErrorCode: &errCode, ErrorMessage: &errMessage,
		},
	}
	for _, req := range requests {
		withReadinessTx(t, store, func(tx *sql.Tx) error {
			_, err := readinessRepository{tx: tx}.RecordBaselineAttempt(ctx, req)
			return err
		})
	}

	var byJob ports.BaselineAttempt
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		var err error
		byJob, err = readinessRepository{tx: tx}.GetBaselineAttemptByJobID(ctx, "job-1")
		return err
	})
	if byJob.Outcome != readiness.BaselineGreen || byJob.ExitCode == nil || *byJob.ExitCode != 0 {
		t.Fatalf("byJob = %+v", byJob)
	}

	var list []ports.BaselineAttempt
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		var err error
		list, err = readinessRepository{tx: tx}.ListBaselineAttempts(ctx, string(rw.ID))
		return err
	})
	if len(list) != 3 {
		t.Fatalf("len(list) = %d, want 3", len(list))
	}
	if list[0].JobID != "job-1" || list[1].JobID != "job-2" || list[2].JobID != "job-3" {
		t.Fatalf("list order = %v, want job-1,job-2,job-3", []string{list[0].JobID, list[1].JobID, list[2].JobID})
	}
	if list[1].Outcome != readiness.BaselineRed || list[1].ExitCode == nil || *list[1].ExitCode != 1 {
		t.Fatalf("list[1] (RED) = %+v", list[1])
	}
	if list[2].Outcome != readiness.BaselineEnvironmentError || list[2].ExitCode != nil {
		t.Fatalf("list[2] (ENVIRONMENT_ERROR) = %+v, want nil ExitCode", list[2])
	}
	if list[2].ErrorCode == nil || *list[2].ErrorCode != "UNAVAILABLE" || list[2].ErrorMessage == nil {
		t.Fatalf("list[2] error fields = %+v", list[2])
	}
}

func TestReadinessRepository_RecordBaselineAttempt_DuplicateJobID(t *testing.T) {
	store := openReadinessTestStore(t, "readiness-attempts-dup.db")
	ctx := context.Background()
	rw := seedReadyRepositoryWorkspace(t, store, "project-1", "repo-1")
	seedBaselineJob(t, store, "project-1", string(rw.ID), "job-1")

	req := ports.RecordBaselineAttemptRequest{
		ID: "attempt-1", ProjectID: "project-1", RepositoryWorkspaceID: string(rw.ID), RepositoryID: "repo-1",
		JobID: "job-1", Stage: readiness.StageVerification, Outcome: readiness.BaselineGreen, DurationMS: 1,
	}
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		_, err := readinessRepository{tx: tx}.RecordBaselineAttempt(ctx, req)
		return err
	})

	req.ID = "attempt-2"
	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		_, err := readinessRepository{tx: tx}.RecordBaselineAttempt(ctx, req)
		return err
	})
	if err == nil {
		t.Fatal("duplicate job id RecordBaselineAttempt succeeded, want a unique constraint error")
	}
}

func TestReadinessRepository_GetBaselineAttemptByJobID_NotFound(t *testing.T) {
	store := openReadinessTestStore(t, "readiness-attempts-notfound.db")
	ctx := context.Background()
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		_, err := readinessRepository{tx: tx}.GetBaselineAttemptByJobID(ctx, "missing-job")
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

// --- EnvironmentBlocker ---

func TestReadinessRepository_OpenResolveEnvironmentBlocker(t *testing.T) {
	store := openReadinessTestStore(t, "readiness-blocker.db")
	ctx := context.Background()
	rw := seedReadyRepositoryWorkspace(t, store, "project-1", "repo-1")
	seedBaselineJob(t, store, "project-1", string(rw.ID), "job-1")
	seedBaselineJob(t, store, "project-1", string(rw.ID), "job-2")
	seedBaselineJob(t, store, "project-1", string(rw.ID), "job-3")

	var opened ports.EnvironmentBlocker
	var alreadyOpen bool
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		var err error
		opened, alreadyOpen, err = readinessRepository{tx: tx}.OpenEnvironmentBlocker(ctx, ports.OpenEnvironmentBlockerRequest{
			ID: "blocker-1", ProjectID: "project-1", RepositoryWorkspaceID: string(rw.ID), RepositoryID: "repo-1",
			JobID: "job-1", Reason: "executable not found",
		})
		return err
	})
	if alreadyOpen {
		t.Fatal("first OpenEnvironmentBlocker reported alreadyOpen = true")
	}
	if opened.Status != readiness.BlockerOpen || opened.Type != readiness.EnvironmentBlockerType {
		t.Fatalf("opened = %+v", opened)
	}

	// A second open call for the same workspace is idempotent: it returns
	// the existing OPEN row instead of violating the partial unique index.
	var second ports.EnvironmentBlocker
	var secondAlreadyOpen bool
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		var err error
		second, secondAlreadyOpen, err = readinessRepository{tx: tx}.OpenEnvironmentBlocker(ctx, ports.OpenEnvironmentBlockerRequest{
			ID: "blocker-2", ProjectID: "project-1", RepositoryWorkspaceID: string(rw.ID), RepositoryID: "repo-1",
			JobID: "job-2", Reason: "still broken",
		})
		return err
	})
	if !secondAlreadyOpen || second.ID != opened.ID {
		t.Fatalf("second open = %+v (alreadyOpen=%v), want the original blocker-1 row", second, secondAlreadyOpen)
	}

	var fetched ports.EnvironmentBlocker
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		var err error
		fetched, err = readinessRepository{tx: tx}.GetOpenEnvironmentBlocker(ctx, string(rw.ID))
		return err
	})
	if fetched.ID != opened.ID {
		t.Fatalf("fetched = %+v, want blocker-1", fetched)
	}

	withReadinessTx(t, store, func(tx *sql.Tx) error {
		return readinessRepository{tx: tx}.ResolveOpenEnvironmentBlocker(ctx, string(rw.ID), fetched.CreatedAt)
	})
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		_, err := readinessRepository{tx: tx}.GetOpenEnvironmentBlocker(ctx, string(rw.ID))
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err after resolve = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})

	// Resolving twice is a safe no-op.
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		return readinessRepository{tx: tx}.ResolveOpenEnvironmentBlocker(ctx, string(rw.ID), fetched.CreatedAt)
	})

	// A fresh blocker can open again after the old one resolved (the
	// partial unique index only constrains OPEN rows).
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		reopened, alreadyOpen, err := readinessRepository{tx: tx}.OpenEnvironmentBlocker(ctx, ports.OpenEnvironmentBlockerRequest{
			ID: "blocker-3", ProjectID: "project-1", RepositoryWorkspaceID: string(rw.ID), RepositoryID: "repo-1",
			JobID: "job-3", Reason: "broken again",
		})
		if err != nil {
			return err
		}
		if alreadyOpen || reopened.ID != "blocker-3" {
			t.Fatalf("reopened = %+v (alreadyOpen=%v), want a brand new OPEN row", reopened, alreadyOpen)
		}
		return nil
	})
}

func TestReadinessRepository_GetOpenEnvironmentBlocker_NotFound(t *testing.T) {
	store := openReadinessTestStore(t, "readiness-blocker-notfound.db")
	ctx := context.Background()
	rw := seedReadyRepositoryWorkspace(t, store, "project-1", "repo-1")
	withReadinessTx(t, store, func(tx *sql.Tx) error {
		_, err := readinessRepository{tx: tx}.GetOpenEnvironmentBlocker(ctx, string(rw.ID))
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}
