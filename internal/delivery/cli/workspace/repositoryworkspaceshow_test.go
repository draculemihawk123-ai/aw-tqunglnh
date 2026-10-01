package workspace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliworkspace "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workspace"
)

func TestRepositoryWorkspaceShow_ReportsOneWorkspacesStateAndLease(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")
	if err := sqlite.SeedFixtureWriteLease(context.Background(), env.store, sw.projectID, sw.familyID, sw.workItemID, sw.repositoryID, sw.repositoryWorkspaceID, 1); err != nil {
		t.Fatalf("SeedFixtureWriteLease: %v", err)
	}

	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", sw.projectID, sw.repositoryWorkspaceID}
	if err := cliworkspace.RunRepositoryWorkspaceShow(context.Background(), env.deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryWorkspaceShow: %v, stderr=%s", err, stderr.String())
	}
	var body map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if body["repositoryWorkspaceId"] != sw.repositoryWorkspaceID || body["repositoryId"] != sw.repositoryID {
		t.Fatalf("identities = %+v", body)
	}
	if body["generation"] != float64(1) || body["hasActiveWriteLease"] != true {
		t.Fatalf("generation/lease = %v/%v, want 1/true (a real write lease was acquired)", body["generation"], body["hasActiveWriteLease"])
	}

	// It must describe the workspace exactly as `workspace-set show` does.
	var setOut, setErr bytes.Buffer
	if err := cliworkspace.RunWorkspaceSetShow(context.Background(), env.deps, []string{"--project-id", sw.projectID, sw.familyID}, &setOut, &setErr); err != nil {
		t.Fatalf("RunWorkspaceSetShow: %v", err)
	}
	var set struct {
		RepositoryWorkspaces []map[string]any `json:"repositoryWorkspaces"`
	}
	if err := json.Unmarshal(setOut.Bytes(), &set); err != nil || len(set.RepositoryWorkspaces) != 1 {
		t.Fatalf("workspace-set show result %s (err %v)", setOut.String(), err)
	}
	for _, field := range []string{"state", "version", "generation", "hasActiveWriteLease"} {
		if body[field] != set.RepositoryWorkspaces[0][field] {
			t.Errorf("%s = %v, but workspace-set show reports %v for the same workspace", field, body[field], set.RepositoryWorkspaces[0][field])
		}
	}
}

func TestRepositoryWorkspaceShow_UnknownIDAndForeignProject(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")

	var stdout, stderr bytes.Buffer
	err := cliworkspace.RunRepositoryWorkspaceShow(context.Background(), env.deps, []string{"--project-id", sw.projectID, "no-such-workspace"}, &stdout, &stderr)
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("unknown id: err = %v, want ports.ErrPersistenceNotFound", err)
	}
	if err := cliworkspace.RunRepositoryWorkspaceShow(context.Background(), env.deps, []string{"--project-id", "another-project", sw.repositoryWorkspaceID}, &stdout, &stderr); err == nil {
		t.Fatal("a workspace must not be readable through another project")
	}
	if stdout.Len() != 0 {
		t.Fatalf("a rejected read wrote %q to stdout", stdout.String())
	}
}

func TestRepositoryWorkspaceShow_UsageErrors(t *testing.T) {
	env := newReleaseTestEnv(t)
	for name, args := range map[string][]string{
		"no project id": {"rw-1"},
		"no argument":   {"--project-id", "p1"},
		"blank id":      {"--project-id", "p1", " "},
	} {
		var stdout, stderr bytes.Buffer
		err := cliworkspace.RunRepositoryWorkspaceShow(context.Background(), env.deps, args, &stdout, &stderr)
		if err == nil || !cli.IsUsageError(err) {
			t.Errorf("%s: err = %v, want a usage error", name, err)
		}
	}
}
