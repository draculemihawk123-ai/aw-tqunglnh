package workspace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	cliworkspace "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workspace"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func TestRepositoryWorkspaceDiff_HappyPath_ReturnsRealPatch(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)
	current := commitWorkspaceChange(t, env.provider, env.handle, "service.txt", "line-1\nline-2\n")

	var stdout, stderr bytes.Buffer
	args := diffArgs(env.baseRevision, current)
	if err := cliworkspace.RunRepositoryWorkspaceDiff(context.Background(), env.deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryWorkspaceDiff: %v, stderr=%s", err, stderr.String())
	}

	var body struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
		Patch []byte `json:"patch"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v\nstdout=%s", err, stdout.String())
	}
	if len(body.Files) != 1 || body.Files[0].Path != "service.txt" {
		t.Fatalf("files = %+v, want exactly one entry for service.txt", body.Files)
	}
	if len(body.Patch) == 0 {
		t.Fatal("patch is empty, want a real unified diff")
	}
}

// TestRepositoryWorkspaceDiff_UnauthorizedRevision_RejectedOpaquely is this
// task's own "stale revision" Verify bullet for diff: an unauthorized base
// revision is rejected with the identical opaque message.
func TestRepositoryWorkspaceDiff_UnauthorizedRevision_RejectedOpaquely(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)

	unauthorized := env.baseRevision
	unauthorized.VCSObjectID = "0000000000000000000000000000000000000000"

	var stdout, stderr bytes.Buffer
	args := diffArgs(unauthorized, env.currentRevision)
	err := cliworkspace.RunRepositoryWorkspaceDiff(context.Background(), env.deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunRepositoryWorkspaceDiff: want an error for an unauthorized base revision, got nil")
	}
	const wantOpaqueMessage = "workspace: the request names an invalid, unauthorized, or unreadable revision, path, or cursor"
	if err.Error() != wantOpaqueMessage {
		t.Fatalf("RunRepositoryWorkspaceDiff error = %q, want the exact opaque message %q", err.Error(), wantOpaqueMessage)
	}
}

func TestRepositoryWorkspaceDiff_MissingResultRevision_ReturnsUsageError(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)

	var stdout, stderr bytes.Buffer
	args := []string{
		"--project-id", inspectProjectID, "--repository-id", inspectRepositoryID, "--workspace-set-id", inspectWorkspaceSetID,
		"--base-revision", env.baseRevision.VCSObjectID, "--base-revision-generation", "1",
		inspectRepositoryWorkspaceID,
	} // --result-revision deliberately omitted
	err := cliworkspace.RunRepositoryWorkspaceDiff(context.Background(), env.deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunRepositoryWorkspaceDiff: want a usage error for a missing --result-revision, got nil")
	}
}

// V11-06: --working-tree shows the worktree's uncommitted state on top of the base.
func TestRepositoryWorkspaceDiff_WorkingTree_ShowsUncommittedChanges(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)
	dir, err := env.provider.WorkingDirectory(context.Background(), env.handle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "service.txt"), []byte("line-1\nline-2\n"), 0o644); err != nil {
		t.Fatalf("modify: %v", err)
	}

	var stdout, stderr bytes.Buffer
	args := []string{
		"--project-id", inspectProjectID, "--repository-id", inspectRepositoryID, "--workspace-set-id", inspectWorkspaceSetID,
		"--base-revision", env.currentRevision.VCSObjectID, "--base-revision-generation", "1", "--working-tree",
		inspectRepositoryWorkspaceID,
	}
	if err := cliworkspace.RunRepositoryWorkspaceDiff(context.Background(), env.deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryWorkspaceDiff --working-tree: %v, stderr=%s", err, stderr.String())
	}
	var body struct {
		Uncommitted     bool   `json:"uncommitted"`
		TotalFiles      int    `json:"totalFiles"`
		ChangeSetDigest string `json:"changeSetDigest"`
		Files           []struct {
			Path   string `json:"path"`
			Status string `json:"status"`
		} `json:"files"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v\nstdout=%s", err, stdout.String())
	}
	if !body.Uncommitted || body.TotalFiles != 1 || len(body.Files) != 1 || body.Files[0].Path != "service.txt" || body.Files[0].Status != "MODIFIED" || body.ChangeSetDigest == "" {
		t.Fatalf("body = %+v, want one MODIFIED service.txt in an uncommitted diff with a digest", body)
	}
}

func TestRepositoryWorkspaceDiff_WorkingTreeAndResultRevisionAreExclusive(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)
	var stdout, stderr bytes.Buffer
	args := append(diffArgs(env.baseRevision, env.currentRevision), "--working-tree")
	if err := cliworkspace.RunRepositoryWorkspaceDiff(context.Background(), env.deps, args, &stdout, &stderr); err == nil {
		t.Fatal("--working-tree together with --result-revision was accepted")
	}
}
