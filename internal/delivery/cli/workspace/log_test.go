package workspace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	cliworkspace "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workspace"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

type logPage struct {
	Entries []struct {
		CommitID string `json:"commitId"`
	} `json:"entries"`
	NextCursor string `json:"nextCursor"`
}

// TestRepositoryWorkspaceLog_PaginatesWithPassThroughCursor is this task's
// own "generation/replay" pagination Verify bullet's log-specific sibling:
// --cursor is passed straight through with no local codec at all (this
// task's own brief), and a real, independently-adapter-revalidated commit
// id advances the page.
func TestRepositoryWorkspaceLog_PaginatesWithPassThroughCursor(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)
	var anchor workspace.Revision
	for i := 0; i < 3; i++ {
		anchor = commitWorkspaceChange(t, env.provider, env.handle, "service.txt", fmt.Sprintf("revision-%d\n", i))
	}

	var stdout1, stderr1 bytes.Buffer
	args := logArgs(anchor, "", "2")
	if err := cliworkspace.RunRepositoryWorkspaceLog(context.Background(), env.deps, args, &stdout1, &stderr1); err != nil {
		t.Fatalf("RunRepositoryWorkspaceLog (page1): %v, stderr=%s", err, stderr1.String())
	}
	var page1 logPage
	if err := json.Unmarshal(stdout1.Bytes(), &page1); err != nil {
		t.Fatalf("decode page1: %v\nstdout=%s", err, stdout1.String())
	}
	if len(page1.Entries) != 2 || page1.NextCursor == "" {
		t.Fatalf("page1 = %+v, want 2 entries and a non-empty cursor", page1)
	}

	var stdout2, stderr2 bytes.Buffer
	args2 := logArgs(anchor, page1.NextCursor, "2")
	if err := cliworkspace.RunRepositoryWorkspaceLog(context.Background(), env.deps, args2, &stdout2, &stderr2); err != nil {
		t.Fatalf("RunRepositoryWorkspaceLog (page2): %v, stderr=%s", err, stderr2.String())
	}
	var page2 logPage
	if err := json.Unmarshal(stdout2.Bytes(), &page2); err != nil {
		t.Fatalf("decode page2: %v\nstdout=%s", err, stdout2.String())
	}
	if len(page2.Entries) == 0 {
		t.Fatal("page2 returned zero entries")
	}
	if page2.Entries[0].CommitID == page1.Entries[0].CommitID {
		t.Fatal("page2 repeated page1's own first entry — cursor did not advance")
	}
}

// TestRepositoryWorkspaceLog_InvalidCursor_RejectedOpaquely: a cursor that
// is not a real ancestor of --anchor is rejected — the identical opaque
// message, never a "cursor is not an ancestor" specific message
// (mapInspectionQueryError's own deliberate boundary).
func TestRepositoryWorkspaceLog_InvalidCursor_RejectedOpaquely(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)

	var stdout, stderr bytes.Buffer
	args := logArgs(env.baseRevision, "not-a-real-commit-object-id", "")
	err := cliworkspace.RunRepositoryWorkspaceLog(context.Background(), env.deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunRepositoryWorkspaceLog: want an error for an invalid cursor, got nil")
	}
	const wantOpaqueMessage = "workspace: the request names an invalid, unauthorized, or unreadable revision, path, or cursor"
	if err.Error() != wantOpaqueMessage {
		t.Fatalf("RunRepositoryWorkspaceLog error = %q, want the exact opaque message %q", err.Error(), wantOpaqueMessage)
	}
}

func TestRepositoryWorkspaceLog_InvalidLimit_ReturnsError(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)

	var stdout, stderr bytes.Buffer
	args := logArgs(env.baseRevision, "", "-5")
	err := cliworkspace.RunRepositoryWorkspaceLog(context.Background(), env.deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunRepositoryWorkspaceLog: want an error for a negative --limit, got nil")
	}
}

func TestRepositoryWorkspaceLog_MissingAnchor_ReturnsUsageError(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)

	var stdout, stderr bytes.Buffer
	args := []string{
		"--project-id", inspectProjectID, "--repository-id", inspectRepositoryID, "--workspace-set-id", inspectWorkspaceSetID,
		inspectRepositoryWorkspaceID,
	} // --anchor deliberately omitted
	err := cliworkspace.RunRepositoryWorkspaceLog(context.Background(), env.deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunRepositoryWorkspaceLog: want a usage error for a missing --anchor, got nil")
	}
}
