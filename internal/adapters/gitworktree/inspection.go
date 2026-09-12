package gitworktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// Provider also satisfies ports.WorkspaceInspectionReader (V6-10C,
// ports/workspaceinspection.go's own doc comment) — Go's structural typing
// needs no change to Provider's own ports.WorkspaceProvider assertion above.
var _ ports.WorkspaceInspectionReader = (*Provider)(nil)

// logFieldSep/logRecordSep delimit ReadRepositoryLog's own `git log
// --format=` output. Both are ASCII control bytes (Unit/Record Separator)
// that can never appear in an object id, an author name/email git itself
// wrote, or an ISO-8601 timestamp — only a maliciously crafted commit
// subject (%s) could ever contain one, and parseLogRecords treats a record
// that fails to split into exactly six fields as reason to stop and report
// Truncated rather than fabricate a corrupted entry (see its own doc
// comment).
const (
	logFieldSep  = "\x1f"
	logRecordSep = "\x1e"
	// maxSubjectLength bounds one commit's own Subject field, independent
	// of ReadRepositoryLogRequest.ByteLimit (which bounds the whole page's
	// raw `git log` output, not any one field) — V6-10C's own "byte ...
	// limits", applied per field so one commit with a pathological subject
	// cannot crowd an entire page's ByteLimit by itself.
	maxSubjectLength = 4096
	// maxDiffSummaryBytes bounds the `git diff --numstat` read
	// ReadDiff runs to build its own Files summary — a fixed internal
	// safety ceiling, independent of the caller's own ByteLimit (which
	// governs the returned Patch instead): a numstat line is only ever a
	// few dozen bytes per changed file, so this ceiling is never meant to
	// be reached by any legitimate diff, only to bound a pathological one.
	maxDiffSummaryBytes int64 = 4 << 20
	// binarySampleSize mirrors git's own "check the first 8000 bytes for a
	// NUL byte" binary-detection heuristic (used by `git diff`/`core.git
	// attributes` internally) for ReadSource's own Binary classification.
	binarySampleSize = 8000
)

// ReadSource implements ports.WorkspaceInspectionReader. See that
// interface's own doc comment for the full safety contract.
func (p *Provider) ReadSource(ctx context.Context, req ports.ReadSourceRequest) (ports.SourceContent, error) {
	if err := ctx.Err(); err != nil {
		return ports.SourceContent{}, err
	}
	inspection, workspacePath, err := p.inspectForRead(ctx, req.Handle)
	if err != nil {
		return ports.SourceContent{}, err
	}
	if err := p.authorizeRevision(ctx, workspacePath, inspection, req.Revision); err != nil {
		return ports.SourceContent{}, err
	}
	treePath, err := normalizeTreePath(req.Path)
	if err != nil {
		return ports.SourceContent{}, err
	}
	byteLimit := positiveOrOne(req.ByteLimit)

	mode, entryType, err := p.lsTreeEntry(ctx, workspacePath, req.Revision.VCSObjectID, treePath)
	if err != nil {
		return ports.SourceContent{}, err
	}
	if mode == "" {
		return ports.SourceContent{}, fmt.Errorf("%w: %s", ErrPathNotFound, treePath)
	}
	if entryType != "blob" || mode == "120000" || mode == "160000" {
		return ports.SourceContent{}, fmt.Errorf("%w: %s", ErrUnsupportedEntry, treePath)
	}

	objectSpec := req.Revision.VCSObjectID + ":" + treePath
	sizeOutput, err := p.runGit(ctx, workspacePath, "cat-file", "-s", objectSpec)
	if err != nil {
		return ports.SourceContent{}, wrapGitContextErr(ctx, err)
	}
	totalBytes, parseErr := strconv.ParseInt(strings.TrimSpace(string(sizeOutput)), 10, 64)
	if parseErr != nil {
		return ports.SourceContent{}, fmt.Errorf("%w: parse blob size: %v", ErrGit, parseErr)
	}

	content, byteTruncated, err := p.runGitCapped(ctx, workspacePath, byteLimit, "cat-file", "-p", objectSpec)
	if err != nil {
		return ports.SourceContent{}, err
	}
	binary := isBinaryContent(content)

	truncated := byteTruncated
	var lineCount int64
	if !binary {
		var lineTruncated bool
		content, lineCount, lineTruncated = clampLines(content, req.LineLimit)
		truncated = truncated || lineTruncated
	}

	return ports.SourceContent{
		Path:       treePath,
		Revision:   req.Revision,
		Content:    content,
		ByteLimit:  byteLimit,
		LineLimit:  req.LineLimit,
		TotalBytes: totalBytes,
		LineCount:  lineCount,
		Truncated:  truncated,
		Binary:     binary,
	}, nil
}

// ReadDiff implements ports.WorkspaceInspectionReader.
func (p *Provider) ReadDiff(ctx context.Context, req ports.ReadDiffRequest) (ports.DiffContent, error) {
	if err := ctx.Err(); err != nil {
		return ports.DiffContent{}, err
	}
	inspection, workspacePath, err := p.inspectForRead(ctx, req.Handle)
	if err != nil {
		return ports.DiffContent{}, err
	}
	if err := p.authorizeRevision(ctx, workspacePath, inspection, req.BaseRevision); err != nil {
		return ports.DiffContent{}, err
	}
	if err := p.authorizeRevision(ctx, workspacePath, inspection, req.ResultRevision); err != nil {
		return ports.DiffContent{}, err
	}

	byteLimit := positiveOrOne(req.ByteLimit)
	fileLimit := req.FileLimit
	if fileLimit <= 0 {
		fileLimit = 1
	}

	numstatOutput, _, err := p.runGitCapped(ctx, workspacePath, maxDiffSummaryBytes,
		"diff", "--numstat", "-z", "--no-renames",
		req.BaseRevision.VCSObjectID, req.ResultRevision.VCSObjectID, "--")
	if err != nil {
		return ports.DiffContent{}, err
	}
	files, filesTruncated := parseNumstat(numstatOutput, fileLimit)

	patch, patchTruncated, err := p.runGitCapped(ctx, workspacePath, byteLimit,
		"diff", "--binary", "--no-ext-diff", "--no-renames", "--full-index",
		req.BaseRevision.VCSObjectID, req.ResultRevision.VCSObjectID, "--")
	if err != nil {
		return ports.DiffContent{}, err
	}

	return ports.DiffContent{
		BaseRevision:   req.BaseRevision,
		ResultRevision: req.ResultRevision,
		Files:          files,
		Patch:          patch,
		ByteLimit:      byteLimit,
		FileLimit:      fileLimit,
		FilesTruncated: filesTruncated,
		PatchTruncated: patchTruncated,
	}, nil
}

// ReadRepositoryLog implements ports.WorkspaceInspectionReader.
func (p *Provider) ReadRepositoryLog(ctx context.Context, req ports.ReadRepositoryLogRequest) (ports.RepositoryLogPage, error) {
	if err := ctx.Err(); err != nil {
		return ports.RepositoryLogPage{}, err
	}
	inspection, workspacePath, err := p.inspectForRead(ctx, req.Handle)
	if err != nil {
		return ports.RepositoryLogPage{}, err
	}
	if err := p.authorizeRevision(ctx, workspacePath, inspection, req.Anchor); err != nil {
		return ports.RepositoryLogPage{}, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 1
	}
	byteLimit := positiveOrOne(req.ByteLimit)

	startPoint := req.Anchor.VCSObjectID
	if req.Cursor != "" {
		startPoint, err = p.resolveLogCursor(ctx, workspacePath, req.Anchor.VCSObjectID, req.Cursor)
		if err != nil {
			return ports.RepositoryLogPage{}, err
		}
		if startPoint == "" {
			// Cursor already named the root commit of this ancestry: there
			// is nothing further to page.
			return ports.RepositoryLogPage{Anchor: req.Anchor, Limit: limit, ByteLimit: byteLimit}, nil
		}
	}

	format := "%H" + logFieldSep + "%P" + logFieldSep + "%an" + logFieldSep + "%ae" + logFieldSep + "%aI" + logFieldSep + "%s" + logRecordSep
	rawOutput, byteTruncated, err := p.runGitCapped(ctx, workspacePath, byteLimit,
		"log", fmt.Sprintf("--max-count=%d", limit+1), "--format="+format, startPoint)
	if err != nil {
		return ports.RepositoryLogPage{}, err
	}

	entries, parseTruncated := parseLogRecords(rawOutput)
	truncated := byteTruncated || parseTruncated
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	nextCursor := ""
	if hasMore || (truncated && len(entries) > 0) {
		nextCursor = entries[len(entries)-1].CommitID
	}

	return ports.RepositoryLogPage{
		Anchor:     req.Anchor,
		Entries:    entries,
		NextCursor: nextCursor,
		Limit:      limit,
		ByteLimit:  byteLimit,
		Truncated:  truncated,
	}, nil
}

// resolveLogCursor validates cursor as a real commit that is anchor itself
// or one of anchor's own ancestors, then returns the object id `git log`
// should actually start walking from (cursor's own first parent) — empty,
// nil means cursor already named the ancestry's root commit.
func (p *Provider) resolveLogCursor(ctx context.Context, workspacePath, anchor, cursor string) (string, error) {
	if !validObjectID(cursor) {
		return "", fmt.Errorf("%w: cursor is not an exact commit object id", ErrInvalidSpec)
	}
	if _, err := p.runGit(ctx, workspacePath, "cat-file", "-e", cursor+"^{commit}"); err != nil {
		return "", fmt.Errorf("%w: cursor is not a commit in this workspace", ErrInvalidSpec)
	}
	_, exitCode, mbErr := p.runGitWithExitCode(ctx, workspacePath, "merge-base", "--is-ancestor", cursor, anchor)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	switch exitCode {
	case 0:
		// cursor is anchor itself, or a real ancestor of anchor.
	case 1:
		return "", fmt.Errorf("%w: cursor is not part of anchor's own ancestry", ErrInvalidSpec)
	default:
		if mbErr != nil {
			return "", fmt.Errorf("%w: verify cursor ancestry: %v", ErrGit, mbErr)
		}
		return "", fmt.Errorf("%w: verify cursor ancestry: unexpected exit code %d", ErrGit, exitCode)
	}

	parentOutput, _, parentErr := p.runGitWithExitCode(ctx, workspacePath, "rev-parse", "--verify", "--end-of-options", cursor+"^")
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	if parentErr != nil {
		// cursor has no parent (it is the ancestry's root commit).
		return "", nil
	}
	parent := strings.TrimSpace(string(parentOutput))
	if !validObjectID(parent) {
		return "", nil
	}
	return parent, nil
}

// inspectForRead resolves handle to its live ports.WorkspaceInspection and
// real worktree path, refusing a RELEASED workspace — the common prelude
// every WorkspaceInspectionReader method shares.
func (p *Provider) inspectForRead(ctx context.Context, handle ports.WorkspaceHandle) (ports.WorkspaceInspection, string, error) {
	inspection, err := p.Inspect(ctx, handle)
	if err != nil {
		return ports.WorkspaceInspection{}, "", wrapGitContextErr(ctx, err)
	}
	if inspection.State == ports.WorkspaceReleased {
		return ports.WorkspaceInspection{}, "", ErrWorkspaceReleased
	}
	workspacePath, err := p.workspacePath(handle)
	if err != nil {
		return ports.WorkspaceInspection{}, "", err
	}
	return inspection, workspacePath, nil
}

// authorizeRevision proves revision is one of inspection's own two known
// commits (BaseRevision or CurrentRevision) for the exact repository and
// generation handle actually resolves to — never an arbitrary, caller-named
// ref expression (ports/workspaceinspection.go's own "resolve exact
// authorized revision"). This runs independently of whatever
// authorization internal/app/workspaceinspection already performed against
// Project/Repository/WorkspaceSet ownership: an adapter must never trust a
// caller-supplied revision on its own say-so.
func (p *Provider) authorizeRevision(
	ctx context.Context,
	workspacePath string,
	inspection ports.WorkspaceInspection,
	revision workspace.Revision,
) error {
	if revision.RepositoryID != inspection.RepositoryID || revision.WorkspaceGeneration != inspection.Generation {
		return fmt.Errorf("%w: revision does not belong to this workspace", ErrInvalidSpec)
	}
	if !validObjectID(revision.VCSObjectID) {
		return fmt.Errorf("%w: revision must be an exact commit object id", ErrInvalidSpec)
	}
	if revision.VCSObjectID != inspection.BaseRevision.VCSObjectID && revision.VCSObjectID != inspection.CurrentRevision.VCSObjectID {
		return fmt.Errorf("%w: revision is not an authorized revision for this workspace", ErrInvalidSpec)
	}
	if _, err := p.runGit(ctx, workspacePath, "cat-file", "-e", revision.VCSObjectID+"^{commit}"); err != nil {
		return wrapGitContextErr(ctx, fmt.Errorf("%w: revision is not a commit in workspace: %v", ErrInvalidSpec, err))
	}
	return nil
}

// lsTreeEntry returns the (mode, type) of path's own tree entry at
// revision, or ("", "", nil) when no such entry exists. path is passed as
// its own argv element after a literal "--", so it is always treated as a
// pathspec, never an option, regardless of its own content — V6-10C's own
// "option injection" guard, mirrored from Diff's existing trailing "--".
func (p *Provider) lsTreeEntry(ctx context.Context, workspacePath, revision, treePath string) (mode string, objectType string, err error) {
	output, runErr := p.runGit(ctx, workspacePath, "ls-tree", revision, "--", treePath)
	if runErr != nil {
		return "", "", wrapGitContextErr(ctx, runErr)
	}
	trimmed := strings.TrimRight(string(output), "\n")
	if trimmed == "" {
		return "", "", nil
	}
	line := strings.SplitN(trimmed, "\n", 2)[0]
	tabIndex := strings.IndexByte(line, '\t')
	if tabIndex < 0 {
		return "", "", fmt.Errorf("%w: unexpected ls-tree output", ErrGit)
	}
	fields := strings.Fields(line[:tabIndex])
	if len(fields) != 3 {
		return "", "", fmt.Errorf("%w: unexpected ls-tree output", ErrGit)
	}
	return fields[0], fields[1], nil
}

// runGitCapped runs one real `git` invocation and reads at most maxBytes+1
// bytes of its stdout, reporting truncated=true (and killing the process
// rather than waiting for it to finish producing output nothing will ever
// read) the instant more than maxBytes bytes are seen. This is the one
// place in this package that bounds a Git subprocess's own output size
// independent of the object it is reading (a blob, a patch or a log page
// can each be arbitrarily large on disk) — V6-10C's own "App+adapter
// enforce byte/line/file/commit limits".
func (p *Provider) runGitCapped(ctx context.Context, directory string, maxBytes int64, arguments ...string) ([]byte, bool, error) {
	if maxBytes <= 0 {
		maxBytes = 1
	}
	commandArguments := append([]string{"-C", directory}, arguments...)
	command := exec.CommandContext(ctx, p.gitExecutable, commandArguments...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	stdout, pipeErr := command.StdoutPipe()
	if pipeErr != nil {
		return nil, false, fmt.Errorf("%w: open git stdout pipe: %v", ErrGit, pipeErr)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr

	if err := command.Start(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, false, ctxErr
		}
		return nil, false, fmt.Errorf("%w: start git %s: %v", ErrGit, firstArg(arguments), err)
	}

	data, readErr := io.ReadAll(io.LimitReader(stdout, maxBytes+1))
	truncated := int64(len(data)) > maxBytes
	if truncated {
		data = data[:maxBytes]
		// The command's own remaining output will never be read; kill it
		// rather than let it block writing to a full pipe buffer or run to
		// completion for no reason.
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()

	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, false, ctxErr
	}
	if truncated {
		// The process was killed once its own output exceeded maxBytes:
		// its exit status and any residual read error are both artifacts
		// of that kill, never a real failure worth reporting.
		return data, true, nil
	}
	if readErr != nil {
		return nil, false, fmt.Errorf("%w: read git %s output: %v", ErrGit, firstArg(arguments), readErr)
	}
	if waitErr != nil {
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) {
			message := strings.TrimSpace(stderr.String())
			if len(message) > 4096 {
				message = message[:4096]
			}
			if message != "" {
				return nil, false, fmt.Errorf("%w: git %s exited with code %d: %s", ErrGit, firstArg(arguments), exitError.ExitCode(), message)
			}
			return nil, false, fmt.Errorf("%w: git %s exited with code %d", ErrGit, firstArg(arguments), exitError.ExitCode())
		}
		return nil, false, fmt.Errorf("%w: wait for git %s: %v", ErrGit, firstArg(arguments), waitErr)
	}
	return data, false, nil
}

func firstArg(arguments []string) string {
	if len(arguments) == 0 {
		return ""
	}
	return arguments[0]
}

// wrapGitContextErr reports ctx's own cancellation/deadline error instead
// of err whenever ctx is already done — every call site in this file that
// runs a real git subprocess uses this so ReadSource/ReadDiff/
// ReadRepositoryLog's own cancellation stays observable via errors.Is even
// though the underlying *Provider.runGit helper always wraps a killed
// process's own failure in ErrGit first.
func wrapGitContextErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func positiveOrOne(value int64) int64 {
	if value <= 0 {
		return 1
	}
	return value
}

// normalizeTreePath validates raw as an exact, already-normalized Git tree
// path — forward-slash separated, relative, no ".." segment, no NUL byte,
// no leading "/" and no leading "-" (V6-10C's own "reject traversal ...
// option injection"). It never itself normalizes a path that needed it
// (e.g. collapsing "a/./b" or a trailing slash): a caller must already
// supply the exact path, so a path requiring cleanup is rejected rather
// than silently rewritten — the same "reject, don't guess" discipline this
// package's own ensureLexicallyWithin already applies to filesystem paths.
func normalizeTreePath(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("%w: path is required", ErrInvalidSpec)
	}
	if strings.IndexByte(raw, 0) >= 0 {
		return "", fmt.Errorf("%w: path contains a NUL byte", ErrInvalidSpec)
	}
	if strings.ContainsRune(raw, '\\') {
		return "", fmt.Errorf("%w: path must use forward slashes", ErrInvalidSpec)
	}
	if strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("%w: path must be relative", ErrInvalidSpec)
	}
	if strings.HasPrefix(raw, "-") {
		return "", fmt.Errorf("%w: path must not begin with '-'", ErrInvalidSpec)
	}
	cleaned := path.Clean(raw)
	if cleaned != raw || cleaned == "." {
		return "", fmt.Errorf("%w: path is not in normalized form", ErrInvalidSpec)
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("%w: path contains an invalid segment", ErrInvalidSpec)
		}
	}
	return cleaned, nil
}

// isBinaryContent mirrors Git's own binary-detection heuristic: a NUL byte
// anywhere in the first binarySampleSize bytes.
func isBinaryContent(content []byte) bool {
	sample := content
	if len(sample) > binarySampleSize {
		sample = sample[:binarySampleSize]
	}
	return bytes.IndexByte(sample, 0) >= 0
}

// clampLines bounds content to at most lineLimit lines (lineLimit <= 0
// means unbounded), returning the possibly-shortened content, the real
// number of lines actually returned, and whether clamping occurred. A
// trailing partial line (content not ending in '\n') still counts as one
// line.
func clampLines(content []byte, lineLimit int64) ([]byte, int64, bool) {
	if len(content) == 0 {
		return content, 0, false
	}
	var lineCount int64
	cut := -1
	for i, b := range content {
		if b != '\n' {
			continue
		}
		lineCount++
		if lineLimit > 0 && lineCount == lineLimit && cut == -1 {
			cut = i + 1
		}
	}
	if content[len(content)-1] != '\n' {
		lineCount++
	}
	if lineLimit <= 0 || lineCount <= lineLimit {
		return content, lineCount, false
	}
	if cut == -1 {
		cut = len(content)
	}
	return content[:cut], lineLimit, true
}

// parseNumstat parses `git diff --numstat -z --no-renames` output (each
// record "<added>\t<deleted>\t<path>", NUL-terminated) into bounded
// DiffFileChange entries, reporting whether fileLimit cut the real file
// count short.
func parseNumstat(output []byte, fileLimit int) ([]ports.DiffFileChange, bool) {
	var files []ports.DiffFileChange
	for _, record := range bytes.Split(output, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		parts := bytes.SplitN(record, []byte("\t"), 3)
		if len(parts) != 3 {
			continue
		}
		addedField, deletedField, filePath := string(parts[0]), string(parts[1]), string(parts[2])
		binary := addedField == "-" || deletedField == "-"
		var added, deleted int64
		if !binary {
			added, _ = strconv.ParseInt(addedField, 10, 64)
			deleted, _ = strconv.ParseInt(deletedField, 10, 64)
		}
		files = append(files, ports.DiffFileChange{Path: filePath, Additions: added, Deletions: deleted, Binary: binary})
	}
	if len(files) > fileLimit {
		return files[:fileLimit], true
	}
	return files, false
}

// parseLogRecords parses ReadRepositoryLog's own `git log --format=...`
// output (logRecordSep-terminated records of exactly six logFieldSep-joined
// fields) into RepositoryLogEntry values. A record that does not split into
// exactly six fields — only reachable via a maliciously crafted commit
// subject embedding a literal record/field separator byte, or via
// ByteLimit cutting the raw output off mid-record — stops parsing and
// reports truncated=true rather than fabricate a corrupted entry from a
// partial record.
func parseLogRecords(raw []byte) ([]ports.RepositoryLogEntry, bool) {
	var entries []ports.RepositoryLogEntry
	for _, record := range strings.Split(string(raw), logRecordSep) {
		record = strings.Trim(record, "\n")
		if strings.TrimSpace(record) == "" {
			continue
		}
		fields := strings.Split(record, logFieldSep)
		if len(fields) != 6 {
			return entries, true
		}
		authoredAt, _ := time.Parse(time.RFC3339, fields[4])
		subject := fields[5]
		subjectTruncated := false
		if len(subject) > maxSubjectLength {
			subject = subject[:maxSubjectLength]
			subjectTruncated = true
		}
		var parentIDs []string
		if strings.TrimSpace(fields[1]) != "" {
			parentIDs = strings.Fields(fields[1])
		}
		entries = append(entries, ports.RepositoryLogEntry{
			CommitID:         fields[0],
			ParentIDs:        parentIDs,
			AuthorName:       fields[2],
			AuthorEmail:      fields[3],
			AuthoredAt:       authoredAt,
			Subject:          subject,
			SubjectTruncated: subjectTruncated,
		})
	}
	return entries, false
}
