package projection_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	appprojectionrebuild "github.com/taQuangLing/agent-workflow/internal/app/projectionrebuild"
	cliprojection "github.com/taQuangLing/agent-workflow/internal/delivery/cli/projection"
)

func TestRunRebuildStatus_ExactByIDLookup(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateProject(t, deps.UoW, "proj-1")

	var rebuildOut bytes.Buffer
	if err := cliprojection.RunRebuild(context.Background(), deps, []string{
		"--project-id=proj-1", "--projection-name=kanban", "--idempotency-key=rebuild-1",
	}, &rebuildOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunRebuild() error = %v", err)
	}
	created := decodeRebuildResult(t, &rebuildOut)

	var stdout, stderr bytes.Buffer
	if err := cliprojection.RunRebuildStatus(context.Background(), deps, []string{created.OperationID}, &stdout, &stderr); err != nil {
		t.Fatalf("RunRebuildStatus() error = %v", err)
	}

	var status appprojectionrebuild.ProjectionRebuildStatus
	if err := json.Unmarshal(stdout.Bytes(), &status); err != nil {
		t.Fatalf("decode ProjectionRebuildStatus: %v (%s)", err, stdout.String())
	}
	if status.OperationID != created.OperationID {
		t.Fatalf("OperationID = %q, want %q", status.OperationID, created.OperationID)
	}
	if status.ProjectID != "proj-1" || status.ProjectionName != "kanban" {
		t.Fatalf("status = %+v, want ProjectID=proj-1 ProjectionName=kanban", status)
	}
	if status.Phase != "REQUESTED" {
		t.Fatalf("Phase = %q, want REQUESTED", status.Phase)
	}
}

// TestRunRebuildStatus_UnknownID_NeverInfersLatest proves this task's own
// "Không làm: no latest-operation inference" line: an unknown operationId
// fails outright — there is no fallback to "the most recent operation for
// some project" anywhere in this command.
func TestRunRebuildStatus_UnknownID_NeverInfersLatest(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := cliprojection.RunRebuildStatus(context.Background(), deps, []string{"no-such-operation"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunRebuildStatus() error = nil, want ports.ErrPersistenceNotFound for an unknown operation id")
	}
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("RunRebuildStatus() error = %v, want it to wrap ports.ErrPersistenceNotFound", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout got written to on a failed lookup: %q", stdout.String())
	}
}

func TestRunRebuildStatus_RequiresExactlyOneArgument(t *testing.T) {
	deps := newTestDeps(t)

	tests := []struct {
		name string
		args []string
	}{
		{name: "no args", args: nil},
		{name: "too many args", args: []string{"op-1", "op-2"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := cliprojection.RunRebuildStatus(context.Background(), deps, tc.args, &stdout, &stderr)
			if !isUsageError(err) {
				t.Fatalf("RunRebuildStatus() error = %v, want a cli.UsageError", err)
			}
		})
	}
}
