package projectionrebuild

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
)

func testCommand(idempotencyKey, requestHash string, scope ports.CommandScope, cmdType string) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1", CorrelationID: "corr-1",
		Scope: scope, RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type: cmdType, RequestHash: requestHash,
	}
}

func requestRebuild(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, idempotencyKey, requestHash, projectID, projectionName string) (RequestProjectionRebuildResult, error) {
	t.Helper()
	return RequestProjectionRebuild(context.Background(), uow, ids,
		testCommand(idempotencyKey, requestHash, ports.ProjectScope(projectID), "RequestProjectionRebuild"),
		RequestProjectionRebuildRequest{ProjectID: projectID, ProjectionName: projectionName},
	)
}

// TestRequestProjectionRebuild_CreatesOperationJobEventReceipt is this
// task's own happy-path proof: one call atomically creates a REQUESTED
// operation, a durable job, a registered domain event and a command
// receipt, all under one OperationID.
func TestRequestProjectionRebuild_CreatesOperationJobEventReceipt(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("op")

	result, err := requestRebuild(t, uow, ids, "req-1", "hash-1", "project-1", "workitem")
	if err != nil {
		t.Fatalf("RequestProjectionRebuild: %v", err)
	}
	if result.OperationID != "op-1" || result.ProjectID != "project-1" || result.ProjectionName != "workitem" {
		t.Fatalf("result = %+v, want OperationID=op-1 ProjectID=project-1 ProjectionName=workitem", result)
	}
	if result.Phase != string(ports.ProjectionRebuildRequested) {
		t.Fatalf("result.Phase = %s, want %s", result.Phase, ports.ProjectionRebuildRequested)
	}
	if result.JobID == "" {
		t.Fatal("result.JobID is empty, want the enqueued job's own ID")
	}

	stored, err := uow.Snapshot.ProjectionRebuilds().GetOperation(context.Background(), result.OperationID)
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if stored.Phase != ports.ProjectionRebuildRequested || stored.JobID != result.JobID {
		t.Fatalf("stored operation = %+v, want Phase=REQUESTED JobID=%s", stored, result.JobID)
	}
	if stored.W0 != nil || stored.ShadowGeneration != nil || stored.ShadowCursor != nil || stored.CutoverCursor != nil {
		t.Fatalf("stored operation = %+v, want every V6-09A-only field nil (this task never populates them)", stored)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	if len(jobs) != 1 || string(jobs[0].ID) != result.JobID || jobs[0].Kind != ProjectionRebuildJobKind {
		t.Fatalf("jobs = %+v, want exactly one %s job with ID %s", jobs, ProjectionRebuildJobKind, result.JobID)
	}
	if jobs[0].AggregateType != aggregateType || jobs[0].AggregateID != result.OperationID {
		t.Fatalf("job = %+v, want AggregateType=%s AggregateID=%s", jobs[0], aggregateType, result.OperationID)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	if len(events) != 1 || events[0].EventType != ProjectionRebuildRequestedEventType {
		t.Fatalf("events = %+v, want exactly one %s", events, ProjectionRebuildRequestedEventType)
	}
	if events[0].AggregateType != aggregateType || events[0].AggregateID != result.OperationID {
		t.Fatalf("event = %+v, want AggregateType=%s AggregateID=%s", events[0], aggregateType, result.OperationID)
	}
}

// TestRequestProjectionRebuild_Replay_ReturnsSameOperationID is V6-09's own
// "replay" Verify-line scenario: the same IdempotencyKey+RequestHash
// returns the exact same result, never a second operation/job/event.
func TestRequestProjectionRebuild_Replay_ReturnsSameOperationID(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("op")

	first, err := requestRebuild(t, uow, ids, "req-1", "hash-1", "project-1", "workitem")
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	second, err := requestRebuild(t, uow, ids, "req-1", "hash-1", "project-1", "workitem")
	if err != nil {
		t.Fatalf("replayed request: %v", err)
	}
	if first != second {
		t.Fatalf("replay result = %+v, want identical to first %+v", second, first)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	if len(jobs) != 1 {
		t.Fatalf("jobs after replay = %d, want exactly 1 (no second job enqueued)", len(jobs))
	}
	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	if len(events) != 1 {
		t.Fatalf("events after replay = %d, want exactly 1 (no second event appended)", len(events))
	}
}

// TestRequestProjectionRebuild_ReceiptConflict_DifferentHash_Rejected is
// this task's own "same key, different payload is a conflict, never a
// replay" scenario.
func TestRequestProjectionRebuild_ReceiptConflict_DifferentHash_Rejected(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("op")

	if _, err := requestRebuild(t, uow, ids, "req-1", "hash-1", "project-1", "workitem"); err != nil {
		t.Fatalf("first request: %v", err)
	}
	_, err := requestRebuild(t, uow, ids, "req-1", "hash-2", "project-1", "workitem")
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("error = %v, want ErrReceiptConflict", err)
	}
}

// TestRequestProjectionRebuild_ActiveOperationConflict_TypedErrorCarriesID
// is V6-09's own most important new-design-surface scenario: a NEW
// idempotency key, while (ProjectID, ProjectionName) already has a
// nonterminal rebuild operation, must be rejected with a TYPED conflict
// that carries the already-active operation's own ID — never a bare
// generic conflict, and never a silently created second rebuild.
func TestRequestProjectionRebuild_ActiveOperationConflict_TypedErrorCarriesID(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("op")

	first, err := requestRebuild(t, uow, ids, "req-1", "hash-1", "project-1", "workitem")
	if err != nil {
		t.Fatalf("first request: %v", err)
	}

	_, err = requestRebuild(t, uow, ids, "req-2-different-key", "hash-2", "project-1", "workitem")
	if err == nil {
		t.Fatal("second request (new key, active op exists) = nil error, want a typed active-rebuild conflict")
	}
	activeID, ok := ActiveProjectionRebuildOperationID(err)
	if !ok {
		t.Fatalf("ActiveProjectionRebuildOperationID(%v) = (_, false), want (%s, true)", err, first.OperationID)
	}
	if activeID != first.OperationID {
		t.Fatalf("ActiveProjectionRebuildOperationID = %s, want the first request's own OperationID %s", activeID, first.OperationID)
	}

	// Only the ONE operation from the first, successful request exists —
	// the conflicting second request never created a partial second one.
	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	if len(jobs) != 1 {
		t.Fatalf("jobs after rejected second request = %d, want exactly 1", len(jobs))
	}
}

// TestRequestProjectionRebuild_DifferentProjectionName_NotBlocked proves
// the active-operation conflict is scoped to (ProjectID, ProjectionName)
// together, never ProjectID alone: a second, DIFFERENT projection name in
// the same project is never blocked by the first's own active rebuild.
func TestRequestProjectionRebuild_DifferentProjectionName_NotBlocked(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("op")

	if _, err := requestRebuild(t, uow, ids, "req-1", "hash-1", "project-1", "workitem"); err != nil {
		t.Fatalf("first request: %v", err)
	}
	second, err := requestRebuild(t, uow, ids, "req-2", "hash-2", "project-1", "another-projection")
	if err != nil {
		t.Fatalf("second request (different projection name): %v", err)
	}
	if second.OperationID == "op-1" {
		t.Fatal("second request reused the first operation's own ID")
	}
}

func TestRequestProjectionRebuild_MissingProjectID_Rejected(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("op")
	_, err := RequestProjectionRebuild(context.Background(), uow, ids,
		testCommand("req-1", "hash-1", ports.InstallationScope(), "RequestProjectionRebuild"),
		RequestProjectionRebuildRequest{ProjectionName: "workitem"})
	if err == nil {
		t.Fatal("RequestProjectionRebuild with empty ProjectID = nil error, want a validation error")
	}
}

func TestRequestProjectionRebuild_MissingProjectionName_Rejected(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("op")
	_, err := requestRebuild(t, uow, ids, "req-1", "hash-1", "project-1", "")
	if err == nil {
		t.Fatal("RequestProjectionRebuild with empty ProjectionName = nil error, want a validation error")
	}
}

// --- GetProjectionRebuildStatus ---

// TestGetProjectionRebuildStatus_ExactLookup_ReturnsOperation is V6-09's
// own "exact operation lookup" Verify-line scenario at the query layer.
func TestGetProjectionRebuildStatus_ExactLookup_ReturnsOperation(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("op")

	created, err := requestRebuild(t, uow, ids, "req-1", "hash-1", "project-1", "workitem")
	if err != nil {
		t.Fatalf("RequestProjectionRebuild: %v", err)
	}

	status, err := GetProjectionRebuildStatus(context.Background(), uow, created.OperationID)
	if err != nil {
		t.Fatalf("GetProjectionRebuildStatus: %v", err)
	}
	if status.OperationID != created.OperationID || status.ProjectID != "project-1" || status.ProjectionName != "workitem" {
		t.Fatalf("status = %+v, want OperationID=%s ProjectID=project-1 ProjectionName=workitem", status, created.OperationID)
	}
	if status.Phase != string(ports.ProjectionRebuildRequested) || status.JobID != created.JobID {
		t.Fatalf("status = %+v, want Phase=REQUESTED JobID=%s", status, created.JobID)
	}
}

func TestGetProjectionRebuildStatus_NotFound(t *testing.T) {
	uow := fake.New()
	_, err := GetProjectionRebuildStatus(context.Background(), uow, "no-such-operation")
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetProjectionRebuildStatus(missing) error = %v, want ErrPersistenceNotFound", err)
	}
}

func TestGetProjectionRebuildStatus_EmptyID_Rejected(t *testing.T) {
	uow := fake.New()
	_, err := GetProjectionRebuildStatus(context.Background(), uow, "")
	if err == nil {
		t.Fatal("GetProjectionRebuildStatus(\"\") = nil error, want a validation error")
	}
}
