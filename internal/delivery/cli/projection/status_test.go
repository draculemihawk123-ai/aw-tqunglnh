package projection_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	appprojectionrebuild "github.com/taQuangLing/agent-workflow/internal/app/projectionrebuild"
	cliprojection "github.com/taQuangLing/agent-workflow/internal/delivery/cli/projection"
)

func TestRunStatus_UnbuiltProjection_ReportsStale(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateProject(t, deps.UoW, "proj-1")

	var stdout, stderr bytes.Buffer
	err := cliprojection.RunStatus(context.Background(), deps, []string{
		"--project-id=proj-1", "--projection-name=kanban",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunStatus() error = %v", err)
	}

	var result appprojectionrebuild.ProjectionStatusResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode ProjectionStatusResult: %v (%s)", err, stdout.String())
	}
	if result.ProjectID != "proj-1" || result.ProjectionName != "kanban" {
		t.Fatalf("result = %+v, want ProjectID=proj-1 ProjectionName=kanban", result)
	}
	if result.Generation != 0 || result.Cursor != 0 {
		t.Fatalf("result = %+v, want Generation=0 Cursor=0 for a never-built projection", result)
	}
	if result.Status != "STALE" {
		t.Fatalf("Status = %q, want STALE for a never-built projection (never a fabricated LIVE)", result.Status)
	}
}

func TestRunStatus_MissingFlags_UsageError(t *testing.T) {
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
			err := cliprojection.RunStatus(context.Background(), deps, tc.args, &stdout, &stderr)
			if !isUsageError(err) {
				t.Fatalf("RunStatus() error = %v, want a cli.UsageError", err)
			}
		})
	}
}

func TestRunStatus_RejectsPositionalArgs(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := cliprojection.RunStatus(context.Background(), deps, []string{
		"--project-id=proj-1", "--projection-name=kanban", "unexpected",
	}, &stdout, &stderr)
	if !isUsageError(err) {
		t.Fatalf("RunStatus() error = %v, want a cli.UsageError for an unexpected positional argument", err)
	}
}
