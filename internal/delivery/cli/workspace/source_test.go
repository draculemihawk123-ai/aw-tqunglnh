package workspace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	cliworkspace "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workspace"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func TestRepositoryWorkspaceSource_HappyPath_WritesExactBytesToFile(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)
	outputPath := filepath.Join(t.TempDir(), "service.txt")

	var stdout, stderr bytes.Buffer
	args := sourceArgs(env.baseRevision, "service.txt", outputPath)
	if err := cliworkspace.RunRepositoryWorkspaceSource(context.Background(), env.deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryWorkspaceSource: %v, stderr=%s", err, stderr.String())
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(content) != "line-1\n" {
		t.Fatalf("output file content = %q, want %q", content, "line-1\n")
	}

	var summary map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("decode metadata: %v\nstdout=%s", err, stdout.String())
	}
	if summary["binary"] != false {
		t.Fatalf("binary = %v, want false", summary["binary"])
	}
	if summary["truncated"] != false {
		t.Fatalf("truncated = %v, want false", summary["truncated"])
	}
	if summary["totalBytes"] != float64(len("line-1\n")) {
		t.Fatalf("totalBytes = %v, want %d", summary["totalBytes"], len("line-1\n"))
	}
}

// TestRepositoryWorkspaceSource_OutputDash_StreamsRawBytesToStdout proves
// the "binary stdout" split: --output - writes ONLY raw content bytes to
// stdout (never a JSON document mixed in), with bounded metadata on stderr
// instead — mirrors internal/delivery/cli/evidence/artifact.go's own
// identical split.
func TestRepositoryWorkspaceSource_OutputDash_StreamsRawBytesToStdout(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)

	var stdout, stderr bytes.Buffer
	args := sourceArgs(env.baseRevision, "service.txt", "-")
	if err := cliworkspace.RunRepositoryWorkspaceSource(context.Background(), env.deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryWorkspaceSource: %v, stderr=%s", err, stderr.String())
	}
	if stdout.String() != "line-1\n" {
		t.Fatalf("stdout = %q, want exactly %q (no JSON mixed in)", stdout.String(), "line-1\n")
	}
	if !strings.Contains(stderr.String(), "totalBytes=7") {
		t.Fatalf("stderr = %q, want a diagnostic line naming totalBytes=7", stderr.String())
	}
}

// TestRepositoryWorkspaceSource_LineLimit_TruncatesAndReportsTruncated is
// this task's own "truncation" Verify bullet.
func TestRepositoryWorkspaceSource_LineLimit_TruncatesAndReportsTruncated(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)
	revision := commitWorkspaceChange(t, env.provider, env.handle, "multi.txt", "line-1\nline-2\nline-3\nline-4\nline-5\n")

	outputPath := filepath.Join(t.TempDir(), "multi.txt")
	var stdout, stderr bytes.Buffer
	args := sourceArgs(revision, "multi.txt", outputPath, "--line-limit", "2")
	if err := cliworkspace.RunRepositoryWorkspaceSource(context.Background(), env.deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryWorkspaceSource: %v, stderr=%s", err, stderr.String())
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(content) != "line-1\nline-2\n" {
		t.Fatalf("output file content = %q, want the first 2 lines only", content)
	}

	var summary map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("decode metadata: %v\nstdout=%s", err, stdout.String())
	}
	if summary["truncated"] != true {
		t.Fatalf("truncated = %v, want true", summary["truncated"])
	}
	if summary["lineCount"] != float64(2) {
		t.Fatalf("lineCount = %v, want 2", summary["lineCount"])
	}
}

// TestRepositoryWorkspaceSource_BinaryContent_StreamsUnmodifiedBytes is
// this task's own "binary" Verify bullet: a binary file's Binary=true is
// surfaced correctly and its content still streams via --output as exact,
// uncorrupted raw bytes (never line-clamped the way text content is —
// ports.SourceContent's own contract, ReadSource's own doc comment).
func TestRepositoryWorkspaceSource_BinaryContent_StreamsUnmodifiedBytes(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)
	binaryContent := "binary\x00content\x00here\x00with\x00nulls"
	revision := commitWorkspaceChange(t, env.provider, env.handle, "binary.dat", binaryContent)

	outputPath := filepath.Join(t.TempDir(), "binary.dat")
	var stdout, stderr bytes.Buffer
	args := sourceArgs(revision, "binary.dat", outputPath)
	if err := cliworkspace.RunRepositoryWorkspaceSource(context.Background(), env.deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryWorkspaceSource: %v, stderr=%s", err, stderr.String())
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(content) != binaryContent {
		t.Fatalf("output file content corrupted: got %q bytes, want %q bytes (%d vs %d bytes)", content, binaryContent, len(content), len(binaryContent))
	}

	var summary map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &summary); err != nil {
		t.Fatalf("decode metadata: %v\nstdout=%s", err, stdout.String())
	}
	if summary["binary"] != true {
		t.Fatalf("binary = %v, want true", summary["binary"])
	}
}

// TestRepositoryWorkspaceSource_PathTraversal_RejectedOpaquely is this
// task's own "traversal" Verify bullet: a ".." path segment is rejected
// with the SAME opaque error mapInspectionQueryError produces for every
// other adapter-internal condition — never a more specific "traversal
// detected" message (this task's own explicit "deliberate boundary, not a
// gap to fill in" instruction).
func TestRepositoryWorkspaceSource_PathTraversal_RejectedOpaquely(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)

	var stdout, stderr bytes.Buffer
	args := sourceArgs(env.baseRevision, "../outside.txt", filepath.Join(t.TempDir(), "out.txt"))
	err := cliworkspace.RunRepositoryWorkspaceSource(context.Background(), env.deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunRepositoryWorkspaceSource: want an error for a path-traversal attempt, got nil")
	}
	const wantOpaqueMessage = "workspace: the request names an invalid, unauthorized, or unreadable revision, path, or cursor"
	if err.Error() != wantOpaqueMessage {
		t.Fatalf("RunRepositoryWorkspaceSource error = %q, want the exact opaque message %q (never a more specific traversal message)", err.Error(), wantOpaqueMessage)
	}
}

// TestRepositoryWorkspaceSource_StaleRevision_RejectedOpaquely is this
// task's own "stale revision" Verify bullet: an unauthorized/arbitrary
// revision (never one of the workspace's own two known-good revisions) is
// rejected cleanly, with the identical opaque message.
func TestRepositoryWorkspaceSource_StaleRevision_RejectedOpaquely(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)

	staleRevision := env.baseRevision
	staleRevision.VCSObjectID = "0000000000000000000000000000000000000000"

	var stdout, stderr bytes.Buffer
	args := sourceArgs(staleRevision, "service.txt", filepath.Join(t.TempDir(), "out.txt"))
	err := cliworkspace.RunRepositoryWorkspaceSource(context.Background(), env.deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunRepositoryWorkspaceSource: want an error for a stale/unauthorized revision, got nil")
	}
	const wantOpaqueMessage = "workspace: the request names an invalid, unauthorized, or unreadable revision, path, or cursor"
	if err.Error() != wantOpaqueMessage {
		t.Fatalf("RunRepositoryWorkspaceSource error = %q, want the exact opaque message %q", err.Error(), wantOpaqueMessage)
	}
}

func TestRepositoryWorkspaceSource_MissingScopeFlag_ReturnsUsageError(t *testing.T) {
	env := newInspectionTestEnv(t, workspace.RepositoryWorkspaceReady)

	var stdout, stderr bytes.Buffer
	args := []string{
		"--project-id", inspectProjectID, "--workspace-set-id", inspectWorkspaceSetID,
		"--revision", env.baseRevision.VCSObjectID, "--revision-generation", strconv.FormatUint(env.baseRevision.WorkspaceGeneration, 10),
		"--path", "service.txt", "--output", "-", inspectRepositoryWorkspaceID,
	} // --repository-id deliberately omitted
	err := cliworkspace.RunRepositoryWorkspaceSource(context.Background(), env.deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunRepositoryWorkspaceSource: want a usage error for a missing --repository-id, got nil")
	}
}
