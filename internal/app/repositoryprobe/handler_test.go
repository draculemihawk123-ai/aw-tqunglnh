package repositoryprobe_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/repositoryprobe"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// stubProber is a scriptable ports.RepositoryProber test double: no real
// git/filesystem I/O, just a fixed Evidence/error pair and a call
// counter, so a handler test can assert exactly when (and how many times)
// the probe itself actually ran.
type stubProber struct {
	evidence ports.RepositoryProbeEvidence
	err      error
	calls    int
}

func (s *stubProber) Probe(context.Context, string, string) (ports.RepositoryProbeEvidence, error) {
	s.calls++
	return s.evidence, s.err
}

func mustSeedRegisteringRepository(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, projectID, repositoryID string) string {
	t.Helper()
	ctx := context.Background()
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID})
		return err
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	cmd := ports.Command{
		ID: "cmd-register-" + repositoryID, IdempotencyKey: "idem-register-" + repositoryID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), Type: "RegisterRepository",
		RequestHash: "hash-register-" + repositoryID,
	}
	result, err := catalog.RegisterRepository(ctx, uow, ids, cmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: "svc",
		RemoteLocator: "/fixture/local/path", DefaultRef: "main",
	})
	if err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}
	return result.ProbeJobID
}

func probeJob(id, repositoryID, projectID string) ports.DurableJob {
	payload, _ := json.Marshal(struct {
		RepositoryID string `json:"repositoryId"`
		ProjectID    string `json:"projectId"`
	}{RepositoryID: repositoryID, ProjectID: projectID})
	return ports.DurableJob{ID: ports.JobID(id), Kind: catalog.RepositoryProbeJobKind, Payload: payload}
}

func TestHandle_SuccessfulProbe_TransitionsToActiveAndCreatesComponents(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	jobID := mustSeedRegisteringRepository(t, uow, ids, "project-1", "repo-1")

	prober := &stubProber{evidence: ports.RepositoryProbeEvidence{
		CanonicalPath: "/fixture/local/path", BaseCommit: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", Dirty: false,
		Components: []ports.ProbedComponent{{Name: "service-a", Path: "service-a", Kind: "DIRECTORY"}},
	}}
	handler := repositoryprobe.New(uow, ids, prober)

	if err := handler.Handle(ctx, probeJob(jobID, "repo-1", "project-1")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if prober.calls != 1 {
		t.Fatalf("prober.calls = %d, want 1", prober.calls)
	}

	repo, err := uow.Snapshot.Catalog().GetRepository(ctx, "repo-1")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo.Status != project.RepositoryActive {
		t.Fatalf("Status = %q, want ACTIVE", repo.Status)
	}
	if repo.Version != 3 { // 1 (registered) -> 2 (probing) -> 3 (active)
		t.Fatalf("Version = %d, want 3", repo.Version)
	}
	if repo.LastProbeErrorCode != nil {
		t.Fatalf("LastProbeErrorCode = %v, want nil", repo.LastProbeErrorCode)
	}

	// idsource.NewSequential("id") mints in call order: "id-1" is
	// RegisterRepository's own probe job id, "id-2" is the one Component
	// candidate finishActive creates, "id-3" is the probe attempt row.
	component, err := uow.Snapshot.Catalog().GetComponent(ctx, "id-2")
	if err != nil {
		t.Fatalf("GetComponent(id-2): %v", err)
	}
	if component.Name != "service-a" || component.Path != "service-a" || component.Kind != "DIRECTORY" {
		t.Fatalf("component = %+v, want the discovered service-a candidate", component)
	}

	attempts, err := uow.Snapshot.Catalog().ListRepositoryProbeAttempts(ctx, "repo-1")
	if err != nil {
		t.Fatalf("ListRepositoryProbeAttempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %+v, want exactly 1", attempts)
	}
	if attempts[0].Result == nil || *attempts[0].Result != project.RepositoryActive {
		t.Fatalf("attempts[0] = %+v, want Result=ACTIVE", attempts[0])
	}
	if attempts[0].JobID != jobID {
		t.Fatalf("attempts[0].JobID = %q, want %q", attempts[0].JobID, jobID)
	}
}

func TestHandle_EnvironmentFailure_TransitionsToBlockedWithCode(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	jobID := mustSeedRegisteringRepository(t, uow, ids, "project-1", "repo-1")

	prober := &stubProber{err: apperror.New(apperror.CodeUnavailable, "git executable unusable", true)}
	handler := repositoryprobe.New(uow, ids, prober)

	if err := handler.Handle(ctx, probeJob(jobID, "repo-1", "project-1")); err != nil {
		t.Fatalf("Handle: %v (an environment-classified probe failure must still be a job SUCCESS)", err)
	}

	repo, err := uow.Snapshot.Catalog().GetRepository(ctx, "repo-1")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo.Status != project.RepositoryBlocked {
		t.Fatalf("Status = %q, want BLOCKED", repo.Status)
	}
	if repo.LastProbeErrorCode == nil || *repo.LastProbeErrorCode != string(apperror.CodeUnavailable) {
		t.Fatalf("LastProbeErrorCode = %v, want %q", repo.LastProbeErrorCode, apperror.CodeUnavailable)
	}

	attempts, err := uow.Snapshot.Catalog().ListRepositoryProbeAttempts(ctx, "repo-1")
	if err != nil {
		t.Fatalf("ListRepositoryProbeAttempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Result == nil || *attempts[0].Result != project.RepositoryBlocked {
		t.Fatalf("attempts = %+v, want exactly 1 with Result=BLOCKED", attempts)
	}
	if attempts[0].ErrorCode == nil || *attempts[0].ErrorCode != string(apperror.CodeUnavailable) {
		t.Fatalf("attempts[0].ErrorCode = %v, want %q", attempts[0].ErrorCode, apperror.CodeUnavailable)
	}
}

func TestHandle_ValidationFailure_TransitionsToBlockedWithCode(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	jobID := mustSeedRegisteringRepository(t, uow, ids, "project-1", "repo-1")

	prober := &stubProber{err: apperror.New(apperror.CodeInvalidArgument, "not a Git working tree", false)}
	handler := repositoryprobe.New(uow, ids, prober)

	if err := handler.Handle(ctx, probeJob(jobID, "repo-1", "project-1")); err != nil {
		t.Fatalf("Handle: %v (a validation-classified probe failure must still be a job SUCCESS)", err)
	}

	repo, err := uow.Snapshot.Catalog().GetRepository(ctx, "repo-1")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo.Status != project.RepositoryBlocked {
		t.Fatalf("Status = %q, want BLOCKED", repo.Status)
	}
	if repo.LastProbeErrorCode == nil || *repo.LastProbeErrorCode != string(apperror.CodeInvalidArgument) {
		t.Fatalf("LastProbeErrorCode = %v, want %q", repo.LastProbeErrorCode, apperror.CodeInvalidArgument)
	}
}

// TestHandle_UnclassifiedProbeError_ReturnsErrorForRetry proves a
// ports.RepositoryProber implementation that violates its own contract
// (returning a plain error, not *apperror.Error) is treated as a genuine
// handler failure: Handle returns a non-nil error (leaving the durable
// job un-completed for workerpool's own retry/lease-expiry mechanism)
// rather than silently guessing a BLOCKED classification.
func TestHandle_UnclassifiedProbeError_ReturnsErrorForRetry(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	jobID := mustSeedRegisteringRepository(t, uow, ids, "project-1", "repo-1")

	prober := &stubProber{err: errors.New("boom: not classified")}
	handler := repositoryprobe.New(uow, ids, prober)

	err := handler.Handle(ctx, probeJob(jobID, "repo-1", "project-1"))
	if err == nil {
		t.Fatal("Handle succeeded for an unclassified probe error, want an error")
	}

	// The Repository must be left exactly at PROBING -- neither ACTIVE nor
	// BLOCKED -- since no definitive verdict was reached.
	repo, getErr := uow.Snapshot.Catalog().GetRepository(ctx, "repo-1")
	if getErr != nil {
		t.Fatalf("GetRepository: %v", getErr)
	}
	if repo.Status != project.RepositoryProbing {
		t.Fatalf("Status = %q, want PROBING (left in flight for a retry)", repo.Status)
	}
	attempts, listErr := uow.Snapshot.Catalog().ListRepositoryProbeAttempts(ctx, "repo-1")
	if listErr != nil {
		t.Fatalf("ListRepositoryProbeAttempts: %v", listErr)
	}
	if len(attempts) != 0 {
		t.Fatalf("attempts = %+v, want none (no definitive verdict was reached)", attempts)
	}
}

// TestHandle_AlreadyActive_IdempotentNoOp simulates the crash-recovery
// window this handler's own doc comment describes: a Repository already
// resolved to ACTIVE by an earlier (crashed-before-CompleteJob) claim of
// this same job. A reclaim must return nil without ever calling Probe
// again or touching the Repository/attempt log a second time.
func TestHandle_AlreadyActive_IdempotentNoOp(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	jobID := mustSeedRegisteringRepository(t, uow, ids, "project-1", "repo-1")

	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		}); err != nil {
			return err
		}
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryActive, LastProbeErrorCode: nil,
		})
		return err
	}); err != nil {
		t.Fatalf("pre-transition to ACTIVE: %v", err)
	}

	prober := &stubProber{err: errors.New("must never be called")}
	handler := repositoryprobe.New(uow, ids, prober)
	if err := handler.Handle(ctx, probeJob(jobID, "repo-1", "project-1")); err != nil {
		t.Fatalf("Handle on an already-ACTIVE repository: %v, want nil (idempotent no-op)", err)
	}
	if prober.calls != 0 {
		t.Fatalf("prober.calls = %d, want 0 (must never re-run against an already-resolved repository)", prober.calls)
	}
}

// TestHandle_CrashRecovery_AlreadyProbing_SkipsFirstTransitionAndFinishes
// simulates a crash between the first transaction's commit
// (REGISTERING->PROBING) and the job being marked SUCCEEDED: on reclaim,
// Handle must detect the Repository is already PROBING, skip straight to
// running the probe (no second, redundant REGISTERING->PROBING attempt),
// and finish normally.
func TestHandle_CrashRecovery_AlreadyProbing_SkipsFirstTransitionAndFinishes(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	jobID := mustSeedRegisteringRepository(t, uow, ids, "project-1", "repo-1")

	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		})
		return err
	}); err != nil {
		t.Fatalf("pre-transition to PROBING: %v", err)
	}

	prober := &stubProber{evidence: ports.RepositoryProbeEvidence{BaseCommit: "cafebabecafebabecafebabecafebabecafebabe"}}
	handler := repositoryprobe.New(uow, ids, prober)
	if err := handler.Handle(ctx, probeJob(jobID, "repo-1", "project-1")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if prober.calls != 1 {
		t.Fatalf("prober.calls = %d, want 1", prober.calls)
	}
	repo, err := uow.Snapshot.Catalog().GetRepository(ctx, "repo-1")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo.Status != project.RepositoryActive || repo.Version != 3 {
		t.Fatalf("repo = %+v, want Status=ACTIVE Version=3 (only one more transition beyond the pre-seeded PROBING)", repo)
	}
}

func TestHandle_MalformedPayload_ReturnsError(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	prober := &stubProber{}
	handler := repositoryprobe.New(uow, ids, prober)

	job := ports.DurableJob{ID: "job-bad", Kind: catalog.RepositoryProbeJobKind, Payload: []byte(`{not-json`)}
	if err := handler.Handle(ctx, job); err == nil {
		t.Fatal("Handle succeeded for a malformed payload, want an error")
	}
}

func TestHandle_MissingRepositoryID_ReturnsError(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	prober := &stubProber{}
	handler := repositoryprobe.New(uow, ids, prober)

	job := ports.DurableJob{ID: "job-bad", Kind: catalog.RepositoryProbeJobKind, Payload: []byte(`{"projectId":"project-1"}`)}
	if err := handler.Handle(ctx, job); err == nil {
		t.Fatal("Handle succeeded for a payload missing repositoryId, want an error")
	}
}
