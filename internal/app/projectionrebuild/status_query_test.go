package projectionrebuild

import (
	"context"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
)

// TestGetProjectionStatus_NoGenerationYet_ReportsStale is
// resolveProjectionState's own first fallback case (kanban/projection_state.go),
// promoted here: a projection nobody has ever built reports STALE with
// Generation/Cursor both 0, never an error and never a fabricated LIVE.
func TestGetProjectionStatus_NoGenerationYet_ReportsStale(t *testing.T) {
	uow := fake.New()
	result, err := GetProjectionStatus(context.Background(), uow, ProjectionStatusRequest{ProjectID: "project-1", ProjectionName: "workitem"})
	if err != nil {
		t.Fatalf("GetProjectionStatus: %v", err)
	}
	if result.Generation != 0 || result.Cursor != 0 || result.Status != string(ports.ProjectionStale) {
		t.Fatalf("result = %+v, want Generation=0 Cursor=0 Status=STALE", result)
	}
	if result.ProjectID != "project-1" || result.ProjectionName != "workitem" {
		t.Fatalf("result = %+v, want ProjectID/ProjectionName echoed back", result)
	}
}

// TestGetProjectionStatus_GenerationWithNoCheckpointYet_ReportsStale is
// resolveProjectionState's own second fallback case: a generation exists
// (EnsureGeneration ran) but the live consumer has never written its own
// first checkpoint for it yet.
func TestGetProjectionStatus_GenerationWithNoCheckpointYet_ReportsStale(t *testing.T) {
	uow := fake.New()
	if err := uow.Snapshot.Projections().EnsureGeneration(context.Background(), "project-1", "workitem", 1, 1, time.Now().UTC()); err != nil {
		t.Fatalf("EnsureGeneration: %v", err)
	}

	result, err := GetProjectionStatus(context.Background(), uow, ProjectionStatusRequest{ProjectID: "project-1", ProjectionName: "workitem"})
	if err != nil {
		t.Fatalf("GetProjectionStatus: %v", err)
	}
	if result.Generation != 1 || result.Cursor != 0 || result.Status != string(ports.ProjectionStale) {
		t.Fatalf("result = %+v, want Generation=1 Cursor=0 Status=STALE", result)
	}
}

// TestGetProjectionStatus_WithCheckpoint_ReportsLive is the healthy-path
// case: a checkpoint exists with Status LIVE, so GetProjectionStatus
// reports it verbatim.
func TestGetProjectionStatus_WithCheckpoint_ReportsLive(t *testing.T) {
	uow := fake.New()
	ctx := context.Background()
	if err := uow.Snapshot.Projections().EnsureGeneration(ctx, "project-1", "workitem", 1, 1, time.Now().UTC()); err != nil {
		t.Fatalf("EnsureGeneration: %v", err)
	}
	if err := uow.Snapshot.Projections().UpsertProjectionCheckpoint(ctx, ports.UpsertProjectionCheckpointRequest{
		ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
		ExpectedCursor: nil, NewCursor: 42, NewStatus: ports.ProjectionLive, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertProjectionCheckpoint: %v", err)
	}

	result, err := GetProjectionStatus(ctx, uow, ProjectionStatusRequest{ProjectID: "project-1", ProjectionName: "workitem"})
	if err != nil {
		t.Fatalf("GetProjectionStatus: %v", err)
	}
	if result.Generation != 1 || result.Cursor != 42 || result.Status != string(ports.ProjectionLive) {
		t.Fatalf("result = %+v, want Generation=1 Cursor=42 Status=LIVE", result)
	}
}

// TestGetProjectionStatus_DegradedCheckpoint_ReportsDegraded proves the
// DEGRADED/STALE freshness values pass through untouched too, not only
// LIVE — this function never narrows or re-derives ports.ProjectionStatus's
// own vocabulary.
func TestGetProjectionStatus_DegradedCheckpoint_ReportsDegraded(t *testing.T) {
	uow := fake.New()
	ctx := context.Background()
	if err := uow.Snapshot.Projections().EnsureGeneration(ctx, "project-1", "workitem", 1, 1, time.Now().UTC()); err != nil {
		t.Fatalf("EnsureGeneration: %v", err)
	}
	if err := uow.Snapshot.Projections().UpsertProjectionCheckpoint(ctx, ports.UpsertProjectionCheckpointRequest{
		ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
		ExpectedCursor: nil, NewCursor: 7, NewStatus: ports.ProjectionDegraded, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertProjectionCheckpoint: %v", err)
	}

	result, err := GetProjectionStatus(ctx, uow, ProjectionStatusRequest{ProjectID: "project-1", ProjectionName: "workitem"})
	if err != nil {
		t.Fatalf("GetProjectionStatus: %v", err)
	}
	if result.Status != string(ports.ProjectionDegraded) {
		t.Fatalf("result.Status = %s, want DEGRADED", result.Status)
	}
}

func TestGetProjectionStatus_MissingProjectID_Rejected(t *testing.T) {
	uow := fake.New()
	_, err := GetProjectionStatus(context.Background(), uow, ProjectionStatusRequest{ProjectionName: "workitem"})
	if err == nil {
		t.Fatal("GetProjectionStatus with empty ProjectID = nil error, want a validation error")
	}
}

func TestGetProjectionStatus_MissingProjectionName_Rejected(t *testing.T) {
	uow := fake.New()
	_, err := GetProjectionStatus(context.Background(), uow, ProjectionStatusRequest{ProjectID: "project-1"})
	if err == nil {
		t.Fatal("GetProjectionStatus with empty ProjectionName = nil error, want a validation error")
	}
}
