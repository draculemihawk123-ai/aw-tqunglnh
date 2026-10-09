package gitworktree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// This file is V11-06's ReadWorkingTreeDiff (ports.WorkspaceInspectionReader):
// what the worktree differs by from a revision BEFORE anything is committed — the
// view an approval gate needs, since the changes an agent made sit in the
// worktree until a ReleaseSet local commit (ADR-014) and no commit exists to diff.
//
// Everything is read-only. The change set comes from three Git reads —
// `git diff --name-status`, `git diff --numstat`, `git ls-files --others
// --exclude-standard` — all run with GIT_OPTIONAL_LOCKS=0 (see runGitWithEnv),
// none of which stages, resets or moves HEAD. File CONTENT is read directly from
// the worktree only through os.Lstat and a bounded open of a regular file inside
// it; a symlink is never followed. The patch of an untracked file is rendered
// here as an all-added unified diff rather than by `git diff --no-index`, so no
// Git process ever sees an untracked path.

const (
	// maxWorkingTreeEntries bounds the change set ReadWorkingTreeDiff will
	// describe. A larger one (typically build output nobody ignored) is refused
	// rather than described partially: the digest and the commit of V11-07 must
	// cover the whole set or nothing.
	maxWorkingTreeEntries = 20000
	// maxUntrackedPatchFileBytes bounds how much of one untracked text file is
	// rendered into the patch; the file's hash and line count still cover it all.
	maxUntrackedPatchFileBytes = 1 << 20
)

// ErrWorkingTreeTooLarge is returned by ReadWorkingTreeDiff when the change set
// has more than maxWorkingTreeEntries paths. An alias of ports.ErrWorkingTreeTooLarge
// so the delivery layers can match it without importing this package.
var ErrWorkingTreeTooLarge = ports.ErrWorkingTreeTooLarge

// ReadWorkingTreeDiff implements ports.WorkspaceInspectionReader.
func (p *Provider) ReadWorkingTreeDiff(ctx context.Context, req ports.ReadWorkingTreeDiffRequest) (ports.WorkingTreeDiff, error) {
	if err := ctx.Err(); err != nil {
		return ports.WorkingTreeDiff{}, err
	}
	inspection, workspacePath, err := p.inspectForRead(ctx, req.Handle)
	if err != nil {
		return ports.WorkingTreeDiff{}, err
	}
	if err := p.authorizeRevision(ctx, workspacePath, inspection, req.BaseRevision); err != nil {
		return ports.WorkingTreeDiff{}, err
	}
	byteLimit := positiveOrOne(req.ByteLimit)
	fileLimit := req.FileLimit
	if fileLimit <= 0 {
		fileLimit = 1
	}
	base := req.BaseRevision.VCSObjectID

	entries, err := p.collectChangeSet(ctx, workspacePath, base)
	if err != nil {
		return ports.WorkingTreeDiff{}, err
	}

	result := ports.WorkingTreeDiff{
		BaseRevision: req.BaseRevision, TotalFiles: len(entries), ByteLimit: byteLimit, FileLimit: fileLimit,
		ChangeSetDigest: changeSetDigest(entries),
	}

	// Patch: the tracked part from Git (no --binary: a binary file is a name and a
	// size here, not base85), then every untracked file as an all-added diff, all
	// within one byte budget.
	patch, patchTruncated, err := p.runGitCapped(ctx, workspacePath, byteLimit,
		"diff", "--no-ext-diff", "--no-renames", "--full-index", base, "--")
	if err != nil {
		return ports.WorkingTreeDiff{}, err
	}
	var buffer bytes.Buffer
	buffer.Write(patch)
	for _, change := range entries {
		if patchTruncated || !change.Untracked {
			continue
		}
		remaining := byteLimit - int64(buffer.Len())
		if remaining <= 0 {
			patchTruncated = true
			break
		}
		rendered, cut := renderUntrackedPatch(workspacePath, change, remaining)
		buffer.Write(rendered)
		if cut {
			patchTruncated = true
		}
	}
	result.Patch = buffer.Bytes()
	result.PatchTruncated = patchTruncated
	if len(entries) > fileLimit {
		entries = entries[:fileLimit]
		result.FilesTruncated = true
	}
	result.Files = entries
	return result, nil
}

// collectChangeSet returns every path whose worktree state differs from revision (tracked
// modifications, deletions, type changes and untracked files that are not ignored), sorted
// by path, each with size and content hash. It is the one definition of "the change set"
// shared by ReadWorkingTreeDiff and the selective local commit (V11-07), so what an approver
// is shown is exactly what a commit is checked against.
func (p *Provider) collectChangeSet(ctx context.Context, workspacePath, base string) ([]ports.WorkingTreeFileChange, error) {
	// Tracked changes against base: status letters, then line counts.
	statusOutput, _, err := p.runGitCapped(ctx, workspacePath, maxDiffSummaryBytes, "diff", "--name-status", "-z", "--no-renames", base, "--")
	if err != nil {
		return nil, err
	}
	numstatOutput, _, err := p.runGitCapped(ctx, workspacePath, maxDiffSummaryBytes, "diff", "--numstat", "-z", "--no-renames", base, "--")
	if err != nil {
		return nil, err
	}
	untrackedOutput, _, err := p.runGitCapped(ctx, workspacePath, maxDiffSummaryBytes, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}

	statuses, err := parseNameStatusZ(statusOutput)
	if err != nil {
		return nil, err
	}
	counts := parseNumstatCounts(numstatOutput)
	untracked := splitNul(untrackedOutput)
	if len(statuses)+len(untracked) > maxWorkingTreeEntries {
		return nil, fmt.Errorf("%w: %d paths (limit %d)", ErrWorkingTreeTooLarge, len(statuses)+len(untracked), maxWorkingTreeEntries)
	}

	entries := make([]ports.WorkingTreeFileChange, 0, len(statuses)+len(untracked))
	for _, st := range statuses {
		change := ports.WorkingTreeFileChange{Path: st.path, Status: st.status}
		if c, ok := counts[st.path]; ok {
			change.Additions, change.Deletions, change.Binary = c.additions, c.deletions, c.binary
		}
		if err := p.describeWorkingFile(workspacePath, &change); err != nil {
			return nil, err
		}
		entries = append(entries, change)
	}
	for _, name := range untracked {
		normalized, err := normalizeReportedPath(name)
		if err != nil {
			return nil, err
		}
		change := ports.WorkingTreeFileChange{Path: normalized, Status: ports.WorkingTreeFileAdded, Untracked: true}
		if err := p.describeWorkingFile(workspacePath, &change); err != nil {
			return nil, err
		}
		entries = append(entries, change)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

type nameStatus struct {
	status ports.WorkingTreeFileStatus
	path   string
}

// parseNameStatusZ parses `git diff --name-status -z --no-renames`: records of
// "<letter>\0<path>\0".
func parseNameStatusZ(raw []byte) ([]nameStatus, error) {
	fields := splitNul(raw)
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("%w: malformed git diff --name-status output", ErrGit)
	}
	out := make([]nameStatus, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		var status ports.WorkingTreeFileStatus
		switch fields[i] {
		case "A":
			status = ports.WorkingTreeFileAdded
		case "M":
			status = ports.WorkingTreeFileModified
		case "D":
			status = ports.WorkingTreeFileDeleted
		case "T":
			status = ports.WorkingTreeFileTypeChanged
		default:
			// U (unmerged), X, B: surface as a modification rather than hide the path.
			status = ports.WorkingTreeFileModified
		}
		normalized, err := normalizeReportedPath(fields[i+1])
		if err != nil {
			return nil, err
		}
		out = append(out, nameStatus{status: status, path: normalized})
	}
	return out, nil
}

type lineCounts struct {
	additions, deletions int64
	binary               bool
}

// parseNumstatCounts parses `git diff --numstat -z --no-renames` into per-path
// counts ("-" counts mark a binary file).
func parseNumstatCounts(raw []byte) map[string]lineCounts {
	counts := map[string]lineCounts{}
	for _, record := range bytes.Split(raw, []byte{0}) {
		parts := bytes.SplitN(record, []byte("\t"), 3)
		if len(parts) != 3 {
			continue
		}
		added, deleted := string(parts[0]), string(parts[1])
		path := strings.ReplaceAll(string(parts[2]), "\\", "/")
		c := lineCounts{binary: added == "-" || deleted == "-"}
		if !c.binary {
			c.additions, _ = strconv.ParseInt(added, 10, 64)
			c.deletions, _ = strconv.ParseInt(deleted, 10, 64)
		}
		counts[path] = c
	}
	return counts
}

func splitNul(raw []byte) []string {
	var out []string
	for _, part := range bytes.Split(raw, []byte{0}) {
		if len(part) > 0 {
			out = append(out, string(part))
		}
	}
	return out
}

// describeWorkingFile fills size, hash, symlink and (for an untracked file) the
// line counts of change from the worktree, reading only inside it and never
// following a symlink. A deleted path has no content.
func (p *Provider) describeWorkingFile(workspacePath string, change *ports.WorkingTreeFileChange) error {
	if change.Status == ports.WorkingTreeFileDeleted {
		return nil
	}
	full := filepath.Join(workspacePath, filepath.FromSlash(change.Path))
	if err := ensureLexicallyWithin(workspacePath, full); err != nil {
		return fmt.Errorf("%w: changed path %q", ErrUnsafePath, change.Path)
	}
	info, err := os.Lstat(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Removed between the Git read and now: report it as deleted, not as an error.
			change.Status, change.Untracked = ports.WorkingTreeFileDeleted, false
			return nil
		}
		return fmt.Errorf("stat changed path %q: %w", change.Path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(full)
		if err != nil {
			return fmt.Errorf("read symlink %q: %w", change.Path, err)
		}
		sum := sha256.Sum256([]byte(target))
		change.Symlink, change.SizeBytes, change.ContentHash = true, int64(len(target)), hex.EncodeToString(sum[:])
		return nil
	}
	if !info.Mode().IsRegular() {
		// A directory, a device or a socket is no file content.
		return nil
	}
	file, err := os.Open(full)
	if err != nil {
		return fmt.Errorf("open changed path %q: %w", change.Path, err)
	}
	defer file.Close()
	hash := sha256.New()
	var size, lines int64
	var sample []byte
	sawBinary, lastByteNewline := false, true
	buf := make([]byte, 64*1024)
	for {
		n, readErr := file.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			hash.Write(chunk)
			if len(sample) < binarySampleSize {
				room := binarySampleSize - len(sample)
				if room > n {
					room = n
				}
				sample = append(sample, chunk[:room]...)
			}
			size += int64(n)
			if change.Untracked {
				lines += int64(bytes.Count(chunk, []byte{'\n'}))
				lastByteNewline = chunk[n-1] == '\n'
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read changed path %q: %w", change.Path, readErr)
		}
	}
	sawBinary = isBinaryContent(sample)
	change.SizeBytes, change.ContentHash = size, hex.EncodeToString(hash.Sum(nil))
	if change.Untracked {
		change.Binary = sawBinary
		if !sawBinary {
			if size > 0 && !lastByteNewline {
				lines++
			}
			change.Additions = lines
		}
	}
	return nil
}

// changeSetDigest hashes every entry's status, path and content hash in path
// order, with unambiguous separators, so it changes iff the change set does.
func changeSetDigest(entries []ports.WorkingTreeFileChange) string {
	hash := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(hash, "%s\x00%s\x00%s\x00", e.Status, e.Path, e.ContentHash)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// renderUntrackedPatch renders one untracked file as an all-added unified diff
// within budget bytes, reporting whether the budget cut it. A binary file or a
// symlink is one explanatory line, never content.
func renderUntrackedPatch(workspacePath string, change ports.WorkingTreeFileChange, budget int64) (rendered []byte, cut bool) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "diff --git a/%s b/%s\nnew file mode %s\n", change.Path, change.Path, map[bool]string{true: "120000", false: "100644"}[change.Symlink])
	switch {
	case change.Symlink:
		fmt.Fprintf(&b, "Symbolic link %s (%d bytes of link target) added\n", change.Path, change.SizeBytes)
	case change.Binary:
		fmt.Fprintf(&b, "Binary file %s (%d bytes) added\n", change.Path, change.SizeBytes)
	case change.SizeBytes == 0:
		// an empty file has no hunk
	default:
		content, truncated := readUntrackedText(workspacePath, change.Path)
		shown := int64(bytes.Count(content, []byte{'\n'}))
		if len(content) > 0 && content[len(content)-1] != '\n' {
			shown++
		}
		fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", change.Path, shown)
		for _, line := range strings.SplitAfter(string(content), "\n") {
			if line == "" {
				continue
			}
			b.WriteString("+" + line)
			if !strings.HasSuffix(line, "\n") {
				b.WriteString("\n\\ No newline at end of file\n")
			}
		}
		if truncated {
			b.WriteString("\\ (rest of file not shown)\n")
		}
	}
	out := b.Bytes()
	if int64(len(out)) > budget {
		return out[:budget], true
	}
	return out, false
}

func readUntrackedText(workspacePath, path string) ([]byte, bool) {
	file, err := os.Open(filepath.Join(workspacePath, filepath.FromSlash(path)))
	if err != nil {
		return nil, false
	}
	defer file.Close()
	data, _ := io.ReadAll(io.LimitReader(file, maxUntrackedPatchFileBytes+1))
	if len(data) > maxUntrackedPatchFileBytes {
		return data[:maxUntrackedPatchFileBytes], true
	}
	return data, false
}
