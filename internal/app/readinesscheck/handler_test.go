package readinesscheck_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/readinesscheck"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// stubProcesses is a scriptable ports.ProcessSupervisor test double —
// mirroring internal/app/repositoryprobe's own stubProber and
// internal/app/workspaceprovision's own stubProvider: no real subprocess
// I/O, just fixed outcomes and call counters keyed by the process ID's own
// suffix ("-setup"/"-verification"), so a test can assert exactly which
// command ran and how many times.
type stubProcesses struct {
	results map[string]ports.ProcessResult
	errs    map[string]error
	calls   []ports.ProcessID
}

func (s *stubProcesses) Run(_ context.Context, spec ports.ProcessSpec, stdout, stderr io.Writer) (ports.ProcessResult, error) {
	s.calls = append(s.calls, spec.ID)
	if stdout != nil {
		_, _ = stdout.Write([]byte("stdout for " + string(spec.ID)))
	}
	if stderr != nil {
		_, _ = stderr.Write([]byte("stderr for " + string(spec.ID)))
	}
	key := string(spec.ID)
	if err, ok := s.errs[key]; ok {
		return ports.ProcessResult{ID: spec.ID, ExitCode: -1}, err
	}
	if result, ok := s.results[key]; ok {
		result.ID = spec.ID
		return result, nil
	}
	return ports.ProcessResult{ID: spec.ID, ExitCode: 0}, nil
}

func (s *stubProcesses) Cancel(context.Context, ports.ProcessID) error {
	return errors.New("stub: Cancel must not be called by this handler")
}

var _ ports.ProcessSupervisor = (*stubProcesses)(nil)

// stubWorkspaces is a fixed ports.WorkspaceDirectoryResolver test double.
type stubWorkspaces struct {
	directory string
	err       error
}

func (s *stubWorkspaces) WorkingDirectory(context.Context, ports.WorkspaceHandle) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.directory, nil
}

var _ ports.WorkspaceDirectoryResolver = (*stubWorkspaces)(nil)

func baselineJob(id, projectID, repositoryID, workspaceSetID, repositoryWorkspaceID string) ports.DurableJob {
	payload, _ := json.Marshal(struct {
		ProjectID             string `json:"projectId"`
		RepositoryID          string `json:"repositoryId"`
		WorkspaceSetID        string `json:"workspaceSetId"`
		RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	}{ProjectID: projectID, RepositoryID: repositoryID, WorkspaceSetID: workspaceSetID, RepositoryWorkspaceID: repositoryWorkspaceID})
	return ports.DurableJob{ID: ports.JobID(id), Kind: readinesscheck.BaselineEvidenceJobKind, Payload: payload}
}

// mustSeedReadyRepositoryWorkspace seeds a Project/Repository/TaskFamily/
// WorkItem/WorkspaceSet/RepositoryWorkspace (READY, generation 1) directly
// against the fake in-memory UnitOfWork — mirroring
// internal/app/workspaceprovision's own handler_test.go fixture-building
// style, without routing through internal/app/work (out of this task's
// own scope to touch).
func mustSeedReadyRepositoryWorkspace(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, projectID, repositoryID string) workspace.RepositoryWorkspace {
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

func mustSetProfile(t *testing.T, uow *fake.UnitOfWork, repositoryID string, setup *readiness.CommandSpec, verification readiness.CommandSpec) {
	t.Helper()
	profile, err := readiness.NewProfile(project.RepositoryID(repositoryID), setup, verification)
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}
	if _, err := readinesscheck.SetReadinessProfile(context.Background(), uow, profile); err != nil {
		t.Fatalf("SetReadinessProfile: %v", err)
	}
}

func mustVerificationSpec(t *testing.T) readiness.CommandSpec {
	t.Helper()
	spec, err := readiness.NewCommandSpec("npm", []string{"test"}, "", 30)
	if err != nil {
		t.Fatalf("NewCommandSpec: %v", err)
	}
	return spec
}

func TestHandler_Handle_GreenBaseline(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	rw := mustSeedReadyRepositoryWorkspace(t, uow, ids, "project-1", "repo-1")
	mustSetProfile(t, uow, "repo-1", nil, mustVerificationSpec(t))

	processes := &stubProcesses{results: map[string]ports.ProcessResult{
		"job-1-verification": {ExitCode: 0},
	}}
	workspaces := &stubWorkspaces{directory: "/fixture/workspace"}
	handler := readinesscheck.New(uow, ids, processes, workspaces)

	if err := handler.Handle(context.Background(), baselineJob("job-1", "project-1", "repo-1", "set-repo-1", string(rw.ID))); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	attempts, err := readinesscheck.ListBaselineAttempts(context.Background(), uow, string(rw.ID))
	if err != nil {
		t.Fatalf("ListBaselineAttempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("len(attempts) = %d, want 1", len(attempts))
	}
	if attempts[0].Outcome != readiness.BaselineGreen || attempts[0].Stage != readiness.StageVerification {
		t.Fatalf("attempt = %+v", attempts[0])
	}
	if attempts[0].ExitCode == nil || *attempts[0].ExitCode != 0 {
		t.Fatalf("attempt exit code = %v, want 0", attempts[0].ExitCode)
	}
	if attempts[0].ErrorCode != nil {
		t.Fatalf("green attempt has ErrorCode = %v, want nil", attempts[0].ErrorCode)
	}

	if _, err := readinesscheck.GetOpenEnvironmentBlocker(context.Background(), uow, string(rw.ID)); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetOpenEnvironmentBlocker err = %v, want ErrPersistenceNotFound (green baseline opens no blocker)", err)
	}
}

func TestHandler_Handle_RedBaseline(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	rw := mustSeedReadyRepositoryWorkspace(t, uow, ids, "project-1", "repo-1")
	mustSetProfile(t, uow, "repo-1", nil, mustVerificationSpec(t))

	processes := &stubProcesses{results: map[string]ports.ProcessResult{
		"job-1-verification": {ExitCode: 1},
	}}
	workspaces := &stubWorkspaces{directory: "/fixture/workspace"}
	handler := readinesscheck.New(uow, ids, processes, workspaces)

	if err := handler.Handle(context.Background(), baselineJob("job-1", "project-1", "repo-1", "set-repo-1", string(rw.ID))); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	attempts, err := readinesscheck.ListBaselineAttempts(context.Background(), uow, string(rw.ID))
	if err != nil {
		t.Fatalf("ListBaselineAttempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Outcome != readiness.BaselineRed {
		t.Fatalf("attempts = %+v, want exactly one RED attempt", attempts)
	}
	if attempts[0].ExitCode == nil || *attempts[0].ExitCode != 1 {
		t.Fatalf("attempt exit code = %v, want 1", attempts[0].ExitCode)
	}

	// RED is a pinned baseline debt, never a fake PASS — but it is also
	// never an environment fault: no blocker opens for it.
	if _, err := readinesscheck.GetOpenEnvironmentBlocker(context.Background(), uow, string(rw.ID)); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetOpenEnvironmentBlocker err = %v, want ErrPersistenceNotFound (red baseline is debt, not a blocker)", err)
	}
}

func TestHandler_Handle_EnvironmentErrorOpensBlocker(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	rw := mustSeedReadyRepositoryWorkspace(t, uow, ids, "project-1", "repo-1")
	mustSetProfile(t, uow, "repo-1", nil, mustVerificationSpec(t))

	processes := &stubProcesses{errs: map[string]error{
		"job-1-verification": errors.New("start executable \"npm\": exec: \"npm\": executable file not found in $PATH"),
	}}
	workspaces := &stubWorkspaces{directory: "/fixture/workspace"}
	handler := readinesscheck.New(uow, ids, processes, workspaces)

	if err := handler.Handle(context.Background(), baselineJob("job-1", "project-1", "repo-1", "set-repo-1", string(rw.ID))); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	attempts, err := readinesscheck.ListBaselineAttempts(context.Background(), uow, string(rw.ID))
	if err != nil {
		t.Fatalf("ListBaselineAttempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Outcome != readiness.BaselineEnvironmentError {
		t.Fatalf("attempts = %+v, want exactly one ENVIRONMENT_ERROR attempt", attempts)
	}
	if attempts[0].ExitCode != nil {
		t.Fatalf("environment-error attempt has ExitCode = %v, want nil", attempts[0].ExitCode)
	}
	if attempts[0].ErrorCode == nil || attempts[0].ErrorMessage == nil {
		t.Fatalf("environment-error attempt missing ErrorCode/ErrorMessage: %+v", attempts[0])
	}

	blocker, err := readinesscheck.GetOpenEnvironmentBlocker(context.Background(), uow, string(rw.ID))
	if err != nil {
		t.Fatalf("GetOpenEnvironmentBlocker: %v", err)
	}
	if blocker.Status != readiness.BlockerOpen || blocker.Type != readiness.EnvironmentBlockerType {
		t.Fatalf("blocker = %+v", blocker)
	}
}

// TestHandler_Handle_SetupFailureSkipsVerification proves runProfile never
// runs Verification once Setup itself did not produce a GREEN outcome —
// there is nothing valid to verify against a repository whose own setup
// step did not succeed.
func TestHandler_Handle_SetupFailureSkipsVerification(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	rw := mustSeedReadyRepositoryWorkspace(t, uow, ids, "project-1", "repo-1")
	setup, err := readiness.NewCommandSpec("npm", []string{"ci"}, "", 60)
	if err != nil {
		t.Fatalf("NewCommandSpec: %v", err)
	}
	mustSetProfile(t, uow, "repo-1", &setup, mustVerificationSpec(t))

	processes := &stubProcesses{results: map[string]ports.ProcessResult{
		"job-1-setup": {ExitCode: 1},
	}}
	workspaces := &stubWorkspaces{directory: "/fixture/workspace"}
	handler := readinesscheck.New(uow, ids, processes, workspaces)

	if err := handler.Handle(context.Background(), baselineJob("job-1", "project-1", "repo-1", "set-repo-1", string(rw.ID))); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(processes.calls) != 1 || processes.calls[0] != "job-1-setup" {
		t.Fatalf("process calls = %v, want exactly [job-1-setup] (verification must never run)", processes.calls)
	}
	attempts, err := readinesscheck.ListBaselineAttempts(context.Background(), uow, string(rw.ID))
	if err != nil {
		t.Fatalf("ListBaselineAttempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Stage != readiness.StageSetup || attempts[0].Outcome != readiness.BaselineRed {
		t.Fatalf("attempts = %+v, want exactly one RED SETUP attempt", attempts)
	}
}

// TestHandler_Handle_NoProfileIsANoOp proves HE-06-M03's "lưu output thật"
// bar is never faked: absent a configured Profile, no evidence is written
// at all, never a silently-invented GREEN.
func TestHandler_Handle_NoProfileIsANoOp(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	rw := mustSeedReadyRepositoryWorkspace(t, uow, ids, "project-1", "repo-1")

	processes := &stubProcesses{}
	workspaces := &stubWorkspaces{directory: "/fixture/workspace"}
	handler := readinesscheck.New(uow, ids, processes, workspaces)

	if err := handler.Handle(context.Background(), baselineJob("job-1", "project-1", "repo-1", "set-repo-1", string(rw.ID))); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(processes.calls) != 0 {
		t.Fatalf("process calls = %v, want none (no profile configured)", processes.calls)
	}
	attempts, err := readinesscheck.ListBaselineAttempts(context.Background(), uow, string(rw.ID))
	if err != nil {
		t.Fatalf("ListBaselineAttempts: %v", err)
	}
	if len(attempts) != 0 {
		t.Fatalf("attempts = %+v, want none", attempts)
	}
}

// TestHandler_Handle_IdempotentReclaim proves a redelivered job (the same
// JobID claimed twice, e.g. after a crash between commit and CompleteJob)
// never re-runs the real subprocess or records a second attempt.
func TestHandler_Handle_IdempotentReclaim(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	rw := mustSeedReadyRepositoryWorkspace(t, uow, ids, "project-1", "repo-1")
	mustSetProfile(t, uow, "repo-1", nil, mustVerificationSpec(t))

	processes := &stubProcesses{results: map[string]ports.ProcessResult{
		"job-1-verification": {ExitCode: 0},
	}}
	workspaces := &stubWorkspaces{directory: "/fixture/workspace"}
	handler := readinesscheck.New(uow, ids, processes, workspaces)

	job := baselineJob("job-1", "project-1", "repo-1", "set-repo-1", string(rw.ID))
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("second Handle (reclaim): %v", err)
	}
	if len(processes.calls) != 1 {
		t.Fatalf("process calls = %v, want exactly one (idempotent reclaim must never re-run)", processes.calls)
	}
	attempts, err := readinesscheck.ListBaselineAttempts(context.Background(), uow, string(rw.ID))
	if err != nil {
		t.Fatalf("ListBaselineAttempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %+v, want exactly one row", attempts)
	}
}

func TestHandler_Handle_MissingPayloadFields(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	handler := readinesscheck.New(uow, ids, &stubProcesses{}, &stubWorkspaces{})
	job := ports.DurableJob{ID: "job-bad", Kind: readinesscheck.BaselineEvidenceJobKind, Payload: []byte(`{"projectId":"project-1"}`)}
	if err := handler.Handle(context.Background(), job); err == nil {
		t.Fatal("Handle with missing payload fields succeeded, want error")
	}
}

func TestHandler_Handle_MalformedPayload(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	handler := readinesscheck.New(uow, ids, &stubProcesses{}, &stubWorkspaces{})
	job := ports.DurableJob{ID: "job-bad", Kind: readinesscheck.BaselineEvidenceJobKind, Payload: []byte(`{not-json`)}
	if err := handler.Handle(context.Background(), job); err == nil {
		t.Fatal("Handle with malformed payload succeeded, want error")
	}
}

// TestEnqueueBaselineEvidenceJob proves EnqueueBaselineEvidenceJob composes
// inside the caller's own already-open ports.Tx and is idempotent by
// RepositoryWorkspaceID.
func TestEnqueueBaselineEvidenceJob(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	ctx := context.Background()

	req := readinesscheck.EnqueueBaselineEvidenceJobRequest{
		ProjectID: "project-1", RepositoryID: "repo-1", WorkspaceSetID: "set-1",
		RepositoryWorkspaceID: "rw-1", AvailableAt: time.Now().UTC(),
	}
	var first, second ports.DurableJob
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		var err error
		first, err = readinesscheck.EnqueueBaselineEvidenceJob(ctx, tx, ids, req)
		return err
	})
	if err != nil {
		t.Fatalf("first EnqueueBaselineEvidenceJob: %v", err)
	}
	if first.Kind != readinesscheck.BaselineEvidenceJobKind {
		t.Fatalf("job kind = %q, want %q", first.Kind, readinesscheck.BaselineEvidenceJobKind)
	}

	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		var err error
		second, err = readinesscheck.EnqueueBaselineEvidenceJob(ctx, tx, ids, req)
		return err
	})
	if err == nil {
		t.Fatalf("second EnqueueBaselineEvidenceJob for the same RepositoryWorkspaceID succeeded (job %+v), want a duplicate idempotency key error", second)
	}
}
