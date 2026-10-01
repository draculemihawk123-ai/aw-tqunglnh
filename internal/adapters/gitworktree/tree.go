package gitworktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// SnapshotTree implements ports.WorkspaceTreeSnapshotter (V9-01, ADR-030):
// it returns the ID of a tree object holding the workspace's current working
// tree content — every tracked file plus every untracked file that is not
// ignored — without disturbing the workspace in any way.
//
// It works on a PRIVATE, throw-away index (GIT_INDEX_FILE) rather than the
// worktree's own: `read-tree HEAD` seeds that index with the committed tree,
// `add -A` then brings it in line with the working tree (modifications,
// deletions, untracked files; .gitignore is honored exactly as `git status`
// honors it), and `write-tree` turns it into a tree object. Nothing of the
// worktree's real index, HEAD, refs or branches is read-modified-written, so
// a concurrent `git status` or the later local commit (ADR-014) cannot be
// affected. The only persistent side effect is loose blob/tree objects in the
// object store, which have no ref and are eventually reclaimed by `git gc`
// (ADR-030 "Hệ quả").
//
// On a workspace with no uncommitted change the result equals HEAD's tree,
// which is what keeps the pre-V9-01 behavior for a clean worktree.
//
// The temporary index lives in its own fresh directory and the index file is
// deliberately NOT pre-created: git treats an existing zero-byte index file
// as corrupt ("index file smaller than expected"), whereas a missing one is
// simply an empty index — and a directory (instead of a single temp file)
// also sweeps away any `index.lock` a failed run leaves behind, on every OS.
func (p *Provider) SnapshotTree(ctx context.Context, handle ports.WorkspaceHandle) (string, error) {
	workspacePath, err := p.activeWorkspacePath(ctx, handle)
	if err != nil {
		return "", err
	}
	indexDirectory, err := os.MkdirTemp("", "agentkit-tree-index-")
	if err != nil {
		return "", fmt.Errorf("%w: create temporary index directory: %v", ErrGit, err)
	}
	defer func() { _ = os.RemoveAll(indexDirectory) }()
	environment := []string{"GIT_INDEX_FILE=" + filepath.Join(indexDirectory, "index")}

	for _, arguments := range [][]string{{"read-tree", "HEAD"}, {"add", "-A"}} {
		_, exitCode, runErr := p.runGitWithEnv(ctx, workspacePath, environment, arguments...)
		if runErr != nil {
			return "", runErr
		}
		if exitCode != 0 {
			return "", fmt.Errorf("%w: git %s exited with code %d", ErrGit, arguments[0], exitCode)
		}
	}
	output, exitCode, runErr := p.runGitWithEnv(ctx, workspacePath, environment, "write-tree")
	if runErr != nil {
		return "", runErr
	}
	if exitCode != 0 {
		return "", fmt.Errorf("%w: git write-tree exited with code %d", ErrGit, exitCode)
	}
	treeID := strings.TrimSpace(string(output))
	if !validObjectID(treeID) {
		return "", fmt.Errorf("%w: git write-tree returned invalid object id", ErrGit)
	}
	return treeID, nil
}

// DiffTrees implements ports.WorkspaceTreeSnapshotter (V9-01, ADR-030): the
// repository-relative paths whose content or mode differs between tree a and
// tree b, as `git diff-tree -r --name-only -z --no-renames` reports them
// (recursive, NUL-delimited so no path is ever quoted, renames reported as a
// delete plus an add so BOTH sides appear).
//
// Both tree objects are probed first, and a missing one is reported as
// ports.ErrTreeNotFound — distinguishable from every other git failure — so
// the engine can fail the attempt with a clear technical error instead of
// guessing (ADR-030: tree objects have no ref and may be pruned).
func (p *Provider) DiffTrees(ctx context.Context, handle ports.WorkspaceHandle, a, b string) ([]string, error) {
	for _, treeID := range []string{a, b} {
		if !validObjectID(treeID) {
			return nil, fmt.Errorf("%w: %q is not a tree object id", ErrInvalidSpec, treeID)
		}
	}
	workspacePath, err := p.activeWorkspacePath(ctx, handle)
	if err != nil {
		return nil, err
	}
	for _, treeID := range []string{a, b} {
		_, exitCode, probeErr := p.runGitWithExitCode(ctx, workspacePath, "cat-file", "-e", treeID+"^{tree}")
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if exitCode > 0 {
			// `git cat-file -e <id>^{tree}` exits 128 ("Not a valid object
			// name") for a well-formed id that names no object, and 1 for
			// some absent-object shapes; the workspace itself was validated
			// by activeWorkspacePath just above, so either way the tree is
			// what is missing.
			return nil, fmt.Errorf("%w: %s", ports.ErrTreeNotFound, treeID)
		}
		if probeErr != nil {
			return nil, probeErr
		}
		if exitCode != 0 {
			return nil, fmt.Errorf("%w: git cat-file exited with code %d", ErrGit, exitCode)
		}
	}
	// The trailing "--" (as in Diff's `git diff ... base --`) stops git from
	// also stat-ing each tree id as a file name, which Git for Windows can
	// reject as "Filename too long" under a deep workspace path.
	output, err := p.runGit(ctx, workspacePath, "diff-tree", "-r", "--name-only", "-z", "--no-renames", a, b, "--")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, name := range bytes.Split(output, []byte{0}) {
		if len(name) > 0 {
			paths = append(paths, string(name))
		}
	}
	return paths, nil
}

// activeWorkspacePath validates handle exactly as Diff does (identity,
// containment, not released) and returns the workspace's real path.
func (p *Provider) activeWorkspacePath(ctx context.Context, handle ports.WorkspaceHandle) (string, error) {
	inspection, err := p.Inspect(ctx, handle)
	if err != nil {
		return "", err
	}
	if inspection.State == ports.WorkspaceReleased {
		return "", ErrWorkspaceReleased
	}
	return p.workspacePath(handle)
}
