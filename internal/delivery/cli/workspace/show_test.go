package workspace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	cliworkspace "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workspace"
)

func TestWorkspaceSetShow_HappyPath_ReturnsStateAndLease(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")
	if err := sqlite.SeedFixtureWriteLease(context.Background(), env.store, sw.projectID, sw.familyID, sw.workItemID, sw.repositoryID, sw.repositoryWorkspaceID, 1); err != nil {
		t.Fatalf("SeedFixtureWriteLease: %v", err)
	}

	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", sw.projectID, sw.familyID}
	if err := cliworkspace.RunWorkspaceSetShow(context.Background(), env.deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunWorkspaceSetShow: %v, stderr=%s", err, stderr.String())
	}

	var body map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode result: %v\nstdout=%s", err, stdout.String())
	}
	if body["state"] != "READY" {
		t.Fatalf("state = %v, want READY", body["state"])
	}
	if body["version"] != float64(1) {
		t.Fatalf("version = %v, want 1", body["version"])
	}
	repoWorkspaces, _ := body["repositoryWorkspaces"].([]any)
	if len(repoWorkspaces) != 1 {
		t.Fatalf("len(repositoryWorkspaces) = %d, want 1; body = %+v", len(repoWorkspaces), body)
	}
	rw := repoWorkspaces[0].(map[string]any)
	if rw["hasActiveWriteLease"] != true {
		t.Fatalf("repositoryWorkspaces[0].hasActiveWriteLease = %v, want true (a real write lease was acquired)", rw["hasActiveWriteLease"])
	}
	if rw["generation"] != float64(1) {
		t.Fatalf("repositoryWorkspaces[0].generation = %v, want 1", rw["generation"])
	}
}

func TestWorkspaceSetShow_UnknownFamily_ReturnsNotFound(t *testing.T) {
	env := newReleaseTestEnv(t)

	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", "project-x", "does-not-exist"}
	err := cliworkspace.RunWorkspaceSetShow(context.Background(), env.deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunWorkspaceSetShow: want error for an unknown family, got nil")
	}
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("RunWorkspaceSetShow error = %v, want ports.ErrPersistenceNotFound", err)
	}
}

func TestWorkspaceSetShow_MissingProjectID_ReturnsUsageError(t *testing.T) {
	env := newReleaseTestEnv(t)

	var stdout, stderr bytes.Buffer
	err := cliworkspace.RunWorkspaceSetShow(context.Background(), env.deps, []string{"family-1"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunWorkspaceSetShow: want error for a missing --project-id, got nil")
	}
}
