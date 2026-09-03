package readinesscheck_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/readinesscheck"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// This file exercises the real stack end to end -- real sqlite persistence
// and real subprocess execution via internal/adapters/process.Supervisor
// (never mocked) -- for this task's own explicit Verify line: "baseline
// green/red/command-error fixtures". handler_test.go (fake-based) already
// covers the handler's own business logic and control flow in isolation;
// these three tests instead prove the identical GREEN/RED/ENVIRONMENT_ERROR
// classification survives a real ports.ProcessSupervisor.Run call and a
// real sqlite transaction. The fixture "process" is this test binary
// itself (TestReadinessHelper below), the exact same os.Args[0]-as-helper
// technique internal/adapters/process/supervisor_test.go already
// establishes -- cross-platform by construction, no bash/cmd dependency.

func openReadinessCheckTestStore(t *testing.T, name string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// seedRealBaselineJob inserts a real durable_jobs row via the same
// EnqueueBaselineEvidenceJob composition a real trigger caller would use
// (this package's own doc comment) — readiness_baseline_attempts.job_id
// and readiness_environment_blockers.job_id are both real foreign keys
// against durable_jobs(id) (0014_readiness_evidence.sql), so Handle must
// always be given a job that genuinely exists there, exactly as a real
// workerpool.Pool claim would.
func seedRealBaselineJob(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID, workspaceSetID, repositoryWorkspaceID string) ports.DurableJob {
	t.Helper()
	var job ports.DurableJob
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		var err error
		job, err = readinesscheck.EnqueueBaselineEvidenceJob(context.Background(), tx, ids, readinesscheck.EnqueueBaselineEvidenceJobRequest{
			ProjectID: projectID, RepositoryID: repositoryID, WorkspaceSetID: workspaceSetID,
			RepositoryWorkspaceID: repositoryWorkspaceID, AvailableAt: time.Now().UTC(),
		})
		return err
	})
	if err != nil {
		t.Fatalf("seedRealBaselineJob: %v", err)
	}
	return job
}

// realFixtureWorkspaces is a fixed ports.WorkspaceDirectoryResolver that
// returns a real, real temp directory — this task's own scope is proving
// real subprocess execution and real sqlite persistence, not re-proving
// real Git worktree provisioning (already V3-06's own, separately tested
// concern; internal/adapters/gitworktree.Provider satisfies this same
// interface for a real composition root, see that package's own
// WorkspaceDirectoryResolver assertion).
type realFixtureWorkspaces struct{ directory string }

func (r realFixtureWorkspaces) WorkingDirectory(context.Context, ports.WorkspaceHandle) (string, error) {
	return r.directory, nil
}

var _ ports.WorkspaceDirectoryResolver = realFixtureWorkspaces{}

// seedSQLiteReadyRepositoryWorkspace mirrors handler_test.go's own
// mustSeedReadyRepositoryWorkspace, against a real sqlite-backed
// ports.UnitOfWork instead of fake.UnitOfWork.
func seedSQLiteReadyRepositoryWorkspace(t *testing.T, uow ports.UnitOfWork, projectID, repositoryID string) workspace.RepositoryWorkspace {
	t.Helper()
	ctx := context.Background()
	var rw workspace.RepositoryWorkspace
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID}); err != nil {
			return err
		}
		if _, err := tx.Catalog().RegisterRepository(ctx, ports.RegisterRepositoryRequest{
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
		if _, err := tx.Work().CreateTaskFamily(ctx, family); err != nil {
			return err
		}
		if _, err := tx.Work().CreateWorkItem(ctx, root); err != nil {
			return err
		}
		set, err := workspace.NewWorkspaceSet(workspace.WorkspaceSetID("set-"+repositoryID), family)
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
		built, err := workspace.NewRepositoryWorkspace(
			workspace.RepositoryWorkspaceID("rw-"+repositoryID), set, repo, 1, "ws_"+repositoryID, "", "deadbeef",
		)
		if err != nil {
			return err
		}
		built.State = workspace.RepositoryWorkspaceReady
		created, err := tx.Work().CreateRepositoryWorkspace(ctx, built)
		if err != nil {
			return err
		}
		rw = created
		return nil
	})
	if err != nil {
		t.Fatalf("seed ready repository workspace: %v", err)
	}
	return rw
}

func mustSetSQLiteProfile(t *testing.T, uow ports.UnitOfWork, repositoryID string, setup *readiness.CommandSpec, verification readiness.CommandSpec) {
	t.Helper()
	profile, err := readiness.NewProfile(project.RepositoryID(repositoryID), setup, verification)
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}
	if _, err := readinesscheck.SetReadinessProfile(context.Background(), uow, profile); err != nil {
		t.Fatalf("SetReadinessProfile: %v", err)
	}
}

// helperCommandSpec builds a readiness.CommandSpec that re-invokes this
// test binary itself in "TestReadinessHelper" mode, exactly like
// internal/adapters/process/supervisor_test.go's own helperSpec.
func helperCommandSpec(t *testing.T, mode string) readiness.CommandSpec {
	t.Helper()
	spec, err := readiness.NewCommandSpec(os.Args[0], []string{"-test.run=TestReadinessHelper", "--", mode}, "", 10)
	if err != nil {
		t.Fatalf("NewCommandSpec: %v", err)
	}
	return spec
}

func TestHandler_Handle_RealSubprocess_GreenBaseline(t *testing.T) {
	store := openReadinessCheckTestStore(t, "readinesscheck-green.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	rw := seedSQLiteReadyRepositoryWorkspace(t, uow, "project-1", "repo-1")
	mustSetSQLiteProfile(t, uow, "repo-1", nil, helperCommandSpec(t, "pass"))
	job := seedRealBaselineJob(t, uow, ids, "project-1", "repo-1", "set-repo-1", string(rw.ID))

	handler := readinesscheck.New(uow, ids, process.NewSupervisor(), realFixtureWorkspaces{directory: t.TempDir()})
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	attempts, err := readinesscheck.ListBaselineAttempts(context.Background(), uow, string(rw.ID))
	if err != nil {
		t.Fatalf("ListBaselineAttempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Outcome != readiness.BaselineGreen {
		t.Fatalf("attempts = %+v, want exactly one real GREEN attempt", attempts)
	}
	if attempts[0].ExitCode == nil || *attempts[0].ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", attempts[0].ExitCode)
	}
	if attempts[0].StdoutExcerpt == "" {
		t.Fatal("stdout excerpt is empty, want the real helper's own captured output")
	}
}

func TestHandler_Handle_RealSubprocess_RedBaseline(t *testing.T) {
	store := openReadinessCheckTestStore(t, "readinesscheck-red.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	rw := seedSQLiteReadyRepositoryWorkspace(t, uow, "project-1", "repo-1")
	mustSetSQLiteProfile(t, uow, "repo-1", nil, helperCommandSpec(t, "fail"))
	job := seedRealBaselineJob(t, uow, ids, "project-1", "repo-1", "set-repo-1", string(rw.ID))

	handler := readinesscheck.New(uow, ids, process.NewSupervisor(), realFixtureWorkspaces{directory: t.TempDir()})
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	attempts, err := readinesscheck.ListBaselineAttempts(context.Background(), uow, string(rw.ID))
	if err != nil {
		t.Fatalf("ListBaselineAttempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Outcome != readiness.BaselineRed {
		t.Fatalf("attempts = %+v, want exactly one real RED attempt (pinned baseline debt, never a fake PASS)", attempts)
	}
	if attempts[0].ExitCode == nil || *attempts[0].ExitCode != 7 {
		t.Fatalf("exit code = %v, want 7 (TestReadinessHelper's own \"fail\" mode)", attempts[0].ExitCode)
	}
	if attempts[0].StderrExcerpt == "" {
		t.Fatal("stderr excerpt is empty, want the real helper's own captured diagnostic output")
	}

	if _, err := readinesscheck.GetOpenEnvironmentBlocker(context.Background(), uow, string(rw.ID)); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetOpenEnvironmentBlocker err = %v, want ErrPersistenceNotFound (a real RED baseline never opens a blocker)", err)
	}
}

// TestHandler_Handle_RealSubprocess_CommandError proves the genuine
// "command couldn't even run" case: a verification executable that does
// not exist at all, no helper trick needed — os/exec's own real Start()
// failure is exactly what ENVIRONMENT_ERROR classifies.
func TestHandler_Handle_RealSubprocess_CommandError(t *testing.T) {
	store := openReadinessCheckTestStore(t, "readinesscheck-command-error.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	rw := seedSQLiteReadyRepositoryWorkspace(t, uow, "project-1", "repo-1")

	missingExecutable := filepath.Join(t.TempDir(), "does-not-exist-binary")
	verification, err := readiness.NewCommandSpec(missingExecutable, nil, "", 10)
	if err != nil {
		t.Fatalf("NewCommandSpec: %v", err)
	}
	mustSetSQLiteProfile(t, uow, "repo-1", nil, verification)
	job := seedRealBaselineJob(t, uow, ids, "project-1", "repo-1", "set-repo-1", string(rw.ID))

	handler := readinesscheck.New(uow, ids, process.NewSupervisor(), realFixtureWorkspaces{directory: t.TempDir()})
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	attempts, err := readinesscheck.ListBaselineAttempts(context.Background(), uow, string(rw.ID))
	if err != nil {
		t.Fatalf("ListBaselineAttempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Outcome != readiness.BaselineEnvironmentError {
		t.Fatalf("attempts = %+v, want exactly one real ENVIRONMENT_ERROR attempt", attempts)
	}
	if attempts[0].ExitCode != nil {
		t.Fatalf("environment-error attempt has ExitCode = %v, want nil (the command never ran)", attempts[0].ExitCode)
	}
	if attempts[0].ErrorCode == nil || *attempts[0].ErrorCode != "UNAVAILABLE" {
		t.Fatalf("error code = %v, want UNAVAILABLE", attempts[0].ErrorCode)
	}
	if attempts[0].ErrorMessage == nil || *attempts[0].ErrorMessage == "" {
		t.Fatal("error message is empty, want the real os/exec Start() failure reason")
	}

	blocker, err := readinesscheck.GetOpenEnvironmentBlocker(context.Background(), uow, string(rw.ID))
	if err != nil {
		t.Fatalf("GetOpenEnvironmentBlocker: %v", err)
	}
	if blocker.Status != readiness.BlockerOpen {
		t.Fatalf("blocker = %+v, want OPEN", blocker)
	}
}

// TestHandler_Handle_RealSubprocess_SetupThenVerification proves a real,
// full two-stage recipe (setup succeeds, verification then runs and also
// succeeds) end to end against real sqlite and real subprocesses.
func TestHandler_Handle_RealSubprocess_SetupThenVerification(t *testing.T) {
	store := openReadinessCheckTestStore(t, "readinesscheck-setup-then-verify.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	rw := seedSQLiteReadyRepositoryWorkspace(t, uow, "project-1", "repo-1")
	setup := helperCommandSpec(t, "pass")
	mustSetSQLiteProfile(t, uow, "repo-1", &setup, helperCommandSpec(t, "pass"))
	job := seedRealBaselineJob(t, uow, ids, "project-1", "repo-1", "set-repo-1", string(rw.ID))

	handler := readinesscheck.New(uow, ids, process.NewSupervisor(), realFixtureWorkspaces{directory: t.TempDir()})
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	attempts, err := readinesscheck.ListBaselineAttempts(context.Background(), uow, string(rw.ID))
	if err != nil {
		t.Fatalf("ListBaselineAttempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Stage != readiness.StageVerification || attempts[0].Outcome != readiness.BaselineGreen {
		t.Fatalf("attempts = %+v, want exactly one GREEN VERIFICATION attempt (setup succeeded silently)", attempts)
	}
}

// TestReadinessHelper is not a real test: it is the fixture "process"
// TestHandler_Handle_RealSubprocess_* re-invokes via os.Args[0] as its own
// readiness recipe executable — the identical os.Args[0]-as-helper
// technique internal/adapters/process/supervisor_test.go's own
// TestProcessHelper already establishes for this codebase.
//
// Unlike TestProcessHelper (which gates on an env var explicitly injected
// via ports.ProcessSpec.Environment), this helper gates purely on argv:
// readiness.CommandSpec deliberately carries no Environment field of its
// own (this task's own scope never cited a need for a readiness recipe to
// carry bespoke environment overrides beyond what a caller's real
// toolchain already provides via PATH — see CommandSpec's own doc
// comment), so Handler's own runCommand never has an explicit env override
// to inject a test-only activation flag through. That is safe here
// specifically because a normal `go test` run of this package (with no
// `-run` filter, or with a filter that is not exactly this function name)
// invokes this function with os.Args holding the real test binary's own
// flags — never a bare `--` separator — so argumentsAfterSeparator returns
// nil and this function is a true no-op. Only Supervisor.Run's own
// re-invocation (Argv: {"-test.run=TestReadinessHelper", "--", mode}, from
// helperCommandSpec below) ever supplies a `--` separator, so this stays
// inert under every other invocation shape, including a developer running
// `go test -run TestReadinessHelper` directly by hand.
func TestReadinessHelper(t *testing.T) {
	arguments := argumentsAfterSeparator(os.Args)
	if len(arguments) == 0 {
		return
	}
	switch arguments[0] {
	case "pass":
		_, _ = os.Stdout.WriteString("readiness helper: pass\n")
		os.Exit(0)
	case "fail":
		_, _ = os.Stderr.WriteString("readiness helper: fail\n")
		os.Exit(7)
	default:
		os.Exit(3)
	}
}

func argumentsAfterSeparator(arguments []string) []string {
	for index, argument := range arguments {
		if argument == "--" {
			return arguments[index+1:]
		}
	}
	return nil
}
