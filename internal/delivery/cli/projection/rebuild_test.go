package projection_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	appprojectionrebuild "github.com/taQuangLing/agent-workflow/internal/app/projectionrebuild"
	cliprojection "github.com/taQuangLing/agent-workflow/internal/delivery/cli/projection"
)

func decodeRebuildResult(t *testing.T, stdout *bytes.Buffer) appprojectionrebuild.RequestProjectionRebuildResult {
	t.Helper()
	var result appprojectionrebuild.RequestProjectionRebuildResult
	if err := json.Unmarshal([]byte(resultField(t, stdout.String())), &result); err != nil {
		t.Fatalf("decode RequestProjectionRebuildResult: %v (%s)", err, stdout.String())
	}
	return result
}

func TestRunRebuild_Success(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateProject(t, deps.UoW, "proj-1")

	var stdout, stderr bytes.Buffer
	err := cliprojection.RunRebuild(context.Background(), deps, []string{
		"--project-id=proj-1", "--projection-name=kanban", "--idempotency-key=rebuild-1",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunRebuild() error = %v", err)
	}

	result := decodeRebuildResult(t, &stdout)
	if result.OperationID == "" {
		t.Fatal("OperationID is empty")
	}
	if result.ProjectID != "proj-1" || result.ProjectionName != "kanban" {
		t.Fatalf("result = %+v, want ProjectID=proj-1 ProjectionName=kanban", result)
	}
	if result.Phase != "REQUESTED" {
		t.Fatalf("Phase = %q, want REQUESTED (the only phase this command ever writes)", result.Phase)
	}
	if result.JobID == "" {
		t.Fatal("JobID is empty")
	}
	if replayedField(t, stdout.String()) {
		t.Fatal("Replayed = true on the first, fresh call")
	}
}

// TestRunRebuild_ReplaySameIdempotencyKey_SameOperationID is this task's own
// "replay" Verify bullet: retrying `projection rebuild` with the identical
// --idempotency-key (and identical --project-id/--projection-name, so the
// canonical request hash matches) replays the exact same OperationID rather
// than creating a second rebuild operation.
func TestRunRebuild_ReplaySameIdempotencyKey_SameOperationID(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateProject(t, deps.UoW, "proj-1")
	args := []string{"--project-id=proj-1", "--projection-name=kanban", "--idempotency-key=rebuild-replay"}

	var first bytes.Buffer
	if err := cliprojection.RunRebuild(context.Background(), deps, args, &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunRebuild() error = %v", err)
	}
	firstResult := decodeRebuildResult(t, &first)

	var second bytes.Buffer
	if err := cliprojection.RunRebuild(context.Background(), deps, args, &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second RunRebuild() error = %v", err)
	}
	secondResult := decodeRebuildResult(t, &second)

	if !replayedField(t, second.String()) {
		t.Fatal("Replayed = false on the second, identical call — want true")
	}
	if secondResult.OperationID != firstResult.OperationID {
		t.Fatalf("second call OperationID = %q, want the same as the first call %q", secondResult.OperationID, firstResult.OperationID)
	}
}

// TestRunRebuild_ActiveConflict_SurfacesActiveOperationID is this task's own
// "rebuild interruption" Verify bullet: a `projection rebuild` call with a
// NEW idempotency key while (project, projection) already has a nonterminal
// rebuild operation in progress must surface a typed
// *cliprojection.ActiveRebuildConflictError carrying the already-active
// operation's own ID — never a bare/opaque error, and never a second
// operation silently created.
func TestRunRebuild_ActiveConflict_SurfacesActiveOperationID(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateProject(t, deps.UoW, "proj-1")

	var first bytes.Buffer
	if err := cliprojection.RunRebuild(context.Background(), deps, []string{
		"--project-id=proj-1", "--projection-name=kanban", "--idempotency-key=rebuild-first",
	}, &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunRebuild() error = %v", err)
	}
	firstResult := decodeRebuildResult(t, &first)

	var second bytes.Buffer
	err := cliprojection.RunRebuild(context.Background(), deps, []string{
		"--project-id=proj-1", "--projection-name=kanban", "--idempotency-key=rebuild-second",
	}, &second, &bytes.Buffer{})
	if err == nil {
		t.Fatal("second RunRebuild() error = nil, want the typed active-rebuild conflict")
	}
	var conflict *cliprojection.ActiveRebuildConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("second RunRebuild() error = %v (%T), want *cliprojection.ActiveRebuildConflictError", err, err)
	}
	if conflict.ActiveOperationID != firstResult.OperationID {
		t.Fatalf("conflict.ActiveOperationID = %q, want the first call's own OperationID %q", conflict.ActiveOperationID, firstResult.OperationID)
	}
	if second.Len() != 0 {
		t.Fatalf("stdout got written to on a failed rebuild call: %q", second.String())
	}
}

func TestRunRebuild_MissingFlags_UsageError(t *testing.T) {
	deps := newTestDeps(t)

	tests := []struct {
		name string
		args []string
	}{
		{name: "missing project id", args: []string{"--projection-name=kanban"}},
		{name: "missing projection name", args: []string{"--project-id=proj-1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := cliprojection.RunRebuild(context.Background(), deps, tc.args, &stdout, &stderr)
			if !isUsageError(err) {
				t.Fatalf("RunRebuild() error = %v, want a cli.UsageError", err)
			}
		})
	}
}
