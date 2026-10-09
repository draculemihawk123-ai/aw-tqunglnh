package gitworktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// This file is V11-07's selective local commit: commit only the files an approver
// chose, and either leave the rest in the worktree (KEEP) or move them out of it
// into a hidden ref (PARK) so the next strictly read-only node finds a clean worktree.
//
// Git is driven so that no path is ever interpreted: pathspecs go through
// `--literal-pathspecs ... --pathspec-from-file=- --pathspec-file-nul` (NUL-separated on
// stdin), so spaces, unicode, glob characters and a leading `-` are just bytes. The
// commit never uses `-a`; before it, the staged set is read back and must equal the
// selection exactly, and after it the commit's own file list is compared again.

var (
	_ ports.UnselectedChangeParker = (*Provider)(nil)

	// parkedRefPrefix is the only namespace a parked ref may live in, so a caller
	// can never make aw overwrite a branch or a tag.
	parkedRefPrefix = "refs/agentkit/parked/"
)

// stageExactly runs `git add -A` on exactly paths (deletions included), after clearing the
// index back to HEAD so nothing previously staged by anyone can ride into the commit.
func (p *Provider) stageExactly(ctx context.Context, workspacePath string, paths []string) error {
	if _, err := p.runGit(ctx, workspacePath, "reset", "-q"); err != nil {
		return err
	}
	return p.runPathspecGit(ctx, workspacePath, nil, paths, "add", "-A")
}

// runPathspecGit runs `git --literal-pathspecs <verb...> --pathspec-from-file=-
// --pathspec-file-nul` with paths on stdin, failing on any non-zero exit.
func (p *Provider) runPathspecGit(ctx context.Context, workspacePath string, env []string, paths []string, verb ...string) error {
	if len(paths) == 0 {
		return nil
	}
	var stdin bytes.Buffer
	for _, path := range paths {
		stdin.WriteString(path)
		stdin.WriteByte(0)
	}
	args := append([]string{"--literal-pathspecs"}, verb...)
	args = append(args, "--pathspec-from-file=-", "--pathspec-file-nul")
	_, exitCode, err := p.runGitIO(ctx, workspacePath, env, stdin.Bytes(), args...)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("%w: git %s exited with code %d", ErrGit, verb[0], exitCode)
	}
	return nil
}

// resolveSelection checks every selected file against the change set (what differs from
// HEAD) and returns the sorted, de-duplicated paths. It is where "the worktree still
// has what the approver saw" is decided.
func resolveSelection(entries []ports.WorkingTreeFileChange, selection *ports.LocalCommitSelection) ([]string, error) {
	if selection == nil || len(selection.Files) == 0 {
		return nil, fmt.Errorf("%w: a selective commit needs at least one file", ErrInvalidSpec)
	}
	byPath := make(map[string]ports.WorkingTreeFileChange, len(entries))
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}
	seen := map[string]bool{}
	paths := make([]string, 0, len(selection.Files))
	for _, file := range selection.Files {
		normalized, err := normalizeReportedPath(file.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", ports.ErrPathNotInChangeSet, file.Path)
		}
		if first := strings.SplitN(normalized, "/", 2)[0]; strings.EqualFold(first, ".git") {
			return nil, fmt.Errorf("%w: %q is inside .git", ports.ErrPathNotInChangeSet, file.Path)
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		entry, present := byPath[normalized]
		sawIt := file.ContentHash != "" || file.Status != ""
		switch {
		case !present && sawIt:
			return nil, fmt.Errorf("%w: %q is no longer changed", ports.ErrChangeSetDrift, normalized)
		case !present:
			return nil, fmt.Errorf("%w: %q", ports.ErrPathNotInChangeSet, normalized)
		case file.ContentHash != "" && entry.ContentHash != file.ContentHash:
			return nil, fmt.Errorf("%w: %q changed after it was reviewed", ports.ErrChangeSetDrift, normalized)
		case file.Status != "" && entry.Status != file.Status:
			return nil, fmt.Errorf("%w: %q is now %s, was %s", ports.ErrChangeSetDrift, normalized, entry.Status, file.Status)
		}
		paths = append(paths, normalized)
	}
	sort.Strings(paths)
	return paths, nil
}

// createSelectedLocalCommit is CreateLocalCommit for a non-nil Selection. The caller has
// already validated the message and author and holds the local-commit write lease.
func (p *Provider) createSelectedLocalCommit(ctx context.Context, workspacePath string, req ports.CreateLocalCommitRequest, message, authorName, authorEmail string) error {
	entries, err := p.collectChangeSet(ctx, workspacePath, "HEAD")
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return ErrNothingToCommit
	}
	paths, err := resolveSelection(entries, req.Selection)
	if err != nil {
		return err
	}

	if err := p.stageExactly(ctx, workspacePath, paths); err != nil {
		return err
	}
	staged, err := p.nameList(ctx, workspacePath, "diff", "--cached", "--name-only", "-z", "--no-renames", "HEAD", "--")
	if err != nil {
		return err
	}
	if !samePaths(staged, paths) {
		_, _ = p.runGit(ctx, workspacePath, "reset", "-q")
		return fmt.Errorf("%w: staging produced %d paths for a selection of %d", ErrGit, len(staged), len(paths))
	}
	if _, err := p.runGit(ctx, workspacePath,
		"-c", "user.name="+authorName, "-c", "user.email="+authorEmail,
		"commit", "-m", message,
	); err != nil {
		return err
	}
	committed, err := p.nameList(ctx, workspacePath, "diff-tree", "--no-commit-id", "--name-only", "-r", "-z", "--no-renames", "HEAD")
	if err != nil {
		return err
	}
	if !samePaths(committed, paths) {
		return fmt.Errorf("%w: the commit holds %d paths for a selection of %d", ErrGit, len(committed), len(paths))
	}
	return nil
}

func (p *Provider) nameList(ctx context.Context, workspacePath string, args ...string) ([]string, error) {
	output, err := p.runGit(ctx, workspacePath, args...)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, name := range splitNul(output) {
		normalized, err := normalizeReportedPath(name)
		if err != nil {
			return nil, err
		}
		names = append(names, normalized)
	}
	sort.Strings(names)
	return names, nil
}

func samePaths(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func validParkedRef(ref string) bool {
	return strings.HasPrefix(ref, parkedRefPrefix) && len(ref) > len(parkedRefPrefix) && len(ref) <= 256 &&
		!strings.ContainsAny(ref, " \t\r\n\x00~^:?*[\\") && !strings.Contains(ref, "..") && !strings.HasSuffix(ref, "/") && !strings.HasSuffix(ref, ".lock")
}

// ParkRemainingChanges implements ports.UnselectedChangeParker.
func (p *Provider) ParkRemainingChanges(ctx context.Context, handle ports.WorkspaceHandle, ref string) (ports.ParkedChanges, error) {
	if !validParkedRef(ref) {
		return ports.ParkedChanges{}, fmt.Errorf("%w: %q is not a ref under %s", ErrInvalidSpec, ref, parkedRefPrefix)
	}
	workspacePath, err := p.activeWorkspacePath(ctx, handle)
	if err != nil {
		return ports.ParkedChanges{}, err
	}
	entries, err := p.collectChangeSet(ctx, workspacePath, "HEAD")
	if err != nil {
		return ports.ParkedChanges{}, err
	}
	parked := ports.ParkedChanges{Ref: ref}
	if len(entries) == 0 {
		return parked, nil
	}
	paths := make([]string, 0, len(entries))
	var added, restorable []string
	for _, entry := range entries {
		paths = append(paths, entry.Path)
		if entry.Status == ports.WorkingTreeFileAdded {
			added = append(added, entry.Path)
		} else {
			restorable = append(restorable, entry.Path)
		}
	}

	// 1. Store the leftover changes as a commit on top of HEAD, built from a private
	// index so the worktree's own index, HEAD and files are untouched until step 2.
	treeID, err := p.treeOfPaths(ctx, workspacePath, paths)
	if err != nil {
		return ports.ParkedChanges{}, err
	}
	head, err := p.resolveCommit(ctx, workspacePath, "HEAD")
	if err != nil {
		return ports.ParkedChanges{}, err
	}
	previous := ""
	if out, exitCode, _ := p.runGitWithEnv(ctx, workspacePath, nil, "rev-parse", "--verify", "-q", ref+"^{commit}"); exitCode == 0 {
		previous = strings.TrimSpace(string(out))
	}
	alreadyParked := false
	if previous != "" {
		if out, exitCode, _ := p.runGitWithEnv(ctx, workspacePath, nil, "rev-parse", "--verify", "-q", ref+"^{tree}"); exitCode == 0 && strings.TrimSpace(string(out)) == treeID {
			alreadyParked = true // parked with exactly this content before (a crash): only the clean-up is left
		}
	}
	if !alreadyParked {
		args := []string{"-c", "user.name=aw", "-c", "user.email=aw@localhost", "commit-tree", treeID, "-p", head}
		if previous != "" {
			args = append(args, "-p", previous)
		}
		args = append(args, "-m", "aw: changes parked at approval")
		out, err := p.runGit(ctx, workspacePath, args...)
		if err != nil {
			return ports.ParkedChanges{}, err
		}
		commit := strings.TrimSpace(string(out))
		if !validObjectID(commit) {
			return ports.ParkedChanges{}, fmt.Errorf("%w: git commit-tree returned an invalid object id", ErrGit)
		}
		if _, err := p.runGit(ctx, workspacePath, "update-ref", ref, commit); err != nil {
			return ports.ParkedChanges{}, err
		}
	}

	// 2. Clean exactly those paths from the worktree. Tracked/deleted ones go back to
	// HEAD; added ones leave the index and are deleted (inside the worktree only,
	// never through a symlink).
	if err := p.runPathspecGit(ctx, workspacePath, nil, restorable, "checkout", "HEAD"); err != nil {
		return ports.ParkedChanges{}, err
	}
	if err := p.runPathspecGit(ctx, workspacePath, nil, added, "rm", "-q", "--cached", "--ignore-unmatch"); err != nil {
		return ports.ParkedChanges{}, err
	}
	for _, path := range added {
		full := filepath.Join(workspacePath, filepath.FromSlash(path))
		if err := ensureLexicallyWithin(workspacePath, full); err != nil {
			return ports.ParkedChanges{}, fmt.Errorf("%w: parked path %q", ErrUnsafePath, path)
		}
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return ports.ParkedChanges{}, fmt.Errorf("remove parked path %q: %w", path, err)
		}
	}
	parked.Paths = paths
	return parked, nil
}

// treeOfPaths returns a tree holding HEAD plus the current state of paths, built in a
// private temporary index (see SnapshotTree for why the index is a fresh directory).
func (p *Provider) treeOfPaths(ctx context.Context, workspacePath string, paths []string) (string, error) {
	indexDirectory, err := os.MkdirTemp("", "agentkit-park-index-")
	if err != nil {
		return "", fmt.Errorf("%w: create temporary index directory: %v", ErrGit, err)
	}
	defer func() { _ = os.RemoveAll(indexDirectory) }()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(indexDirectory, "index")}
	if _, exitCode, err := p.runGitWithEnv(ctx, workspacePath, env, "read-tree", "HEAD"); err != nil || exitCode != 0 {
		if err == nil {
			err = fmt.Errorf("%w: git read-tree exited with code %d", ErrGit, exitCode)
		}
		return "", err
	}
	if err := p.runPathspecGit(ctx, workspacePath, env, paths, "add", "-A"); err != nil {
		return "", err
	}
	out, exitCode, err := p.runGitWithEnv(ctx, workspacePath, env, "write-tree")
	if err != nil || exitCode != 0 {
		if err == nil {
			err = fmt.Errorf("%w: git write-tree exited with code %d", ErrGit, exitCode)
		}
		return "", err
	}
	treeID := strings.TrimSpace(string(out))
	if !validObjectID(treeID) {
		return "", fmt.Errorf("%w: git write-tree returned an invalid object id", ErrGit)
	}
	return treeID, nil
}

// RestoreParkedChanges implements ports.UnselectedChangeParker.
func (p *Provider) RestoreParkedChanges(ctx context.Context, handle ports.WorkspaceHandle, ref string) ([]string, error) {
	if !validParkedRef(ref) {
		return nil, fmt.Errorf("%w: %q is not a ref under %s", ErrInvalidSpec, ref, parkedRefPrefix)
	}
	workspacePath, err := p.activeWorkspacePath(ctx, handle)
	if err != nil {
		return nil, err
	}
	out, exitCode, _ := p.runGitWithEnv(ctx, workspacePath, nil, "rev-parse", "--verify", "-q", ref+"^{commit}")
	if exitCode != 0 {
		return nil, fmt.Errorf("%w: nothing is parked under %s", ErrGit, ref)
	}
	parkedCommit := strings.TrimSpace(string(out))
	parkedBase := parkedCommit + "^1"

	changed, err := p.runGit(ctx, workspacePath, "diff-tree", "--no-commit-id", "--name-status", "-r", "-z", "--no-renames", parkedBase, parkedCommit)
	if err != nil {
		return nil, err
	}
	statuses, err := parseNameStatusZ(changed)
	if err != nil {
		return nil, err
	}
	if len(statuses) == 0 {
		_, err := p.runGit(ctx, workspacePath, "update-ref", "-d", ref)
		return nil, err
	}

	// Refuse to write over anything: no parked path may be changed in the worktree now,
	// and HEAD must hold, for each, what it held when the files were parked.
	current, err := p.collectChangeSet(ctx, workspacePath, "HEAD")
	if err != nil {
		return nil, err
	}
	dirty := map[string]bool{}
	for _, entry := range current {
		dirty[entry.Path] = true
	}
	var toWrite, toDelete []string
	for _, st := range statuses {
		if dirty[st.path] {
			return nil, fmt.Errorf("%w: %q is changed in the worktree", ports.ErrParkedRestoreConflict, st.path)
		}
		then, err := p.blobAt(ctx, workspacePath, parkedBase, st.path)
		if err != nil {
			return nil, err
		}
		now, err := p.blobAt(ctx, workspacePath, "HEAD", st.path)
		if err != nil {
			return nil, err
		}
		if then != now {
			return nil, fmt.Errorf("%w: %q was changed by a commit made after the files were parked", ports.ErrParkedRestoreConflict, st.path)
		}
		if st.status == ports.WorkingTreeFileDeleted {
			toDelete = append(toDelete, st.path)
		} else {
			toWrite = append(toWrite, st.path)
		}
	}

	if len(toWrite) > 0 {
		// checkout <ref> writes the files and stages them; reset puts the index back so
		// they are plain uncommitted changes again, as they were before parking.
		if err := p.runPathspecGit(ctx, workspacePath, nil, toWrite, "checkout", parkedCommit); err != nil {
			return nil, err
		}
		if err := p.runPathspecGit(ctx, workspacePath, nil, toWrite, "reset", "-q"); err != nil {
			return nil, err
		}
	}
	for _, path := range toDelete {
		full := filepath.Join(workspacePath, filepath.FromSlash(path))
		if err := ensureLexicallyWithin(workspacePath, full); err != nil {
			return nil, fmt.Errorf("%w: parked path %q", ErrUnsafePath, path)
		}
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove %q: %w", path, err)
		}
	}
	if _, err := p.runGit(ctx, workspacePath, "update-ref", "-d", ref); err != nil {
		return nil, err
	}
	restored := make([]string, 0, len(statuses))
	for _, st := range statuses {
		restored = append(restored, st.path)
	}
	sort.Strings(restored)
	return restored, nil
}

// blobAt returns the object id of path in revision's tree, or "" if it is not there.
func (p *Provider) blobAt(ctx context.Context, workspacePath, revision, path string) (string, error) {
	out, err := p.runGit(ctx, workspacePath, "ls-tree", "-z", revision, "--", path)
	if err != nil {
		return "", err
	}
	record := strings.TrimRight(string(out), "\x00")
	if record == "" {
		return "", nil
	}
	// "<mode> <type> <oid>\t<path>"
	meta, _, found := strings.Cut(record, "\t")
	if !found {
		return "", fmt.Errorf("%w: malformed git ls-tree output", ErrGit)
	}
	fields := strings.Fields(meta)
	if len(fields) != 3 {
		return "", fmt.Errorf("%w: malformed git ls-tree output", ErrGit)
	}
	return fields[2], nil
}
