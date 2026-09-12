package ports

import (
	"context"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// WorkspaceInspectionReader is the narrow, read-only adapter port for
// bounded Git content inspection (V6-10C,
// docs/design/08-v6-api-projections.md: "public GetSource, GetDiff,
// GetRepositoryLog before HTTP/CLI exposure"). It is declared as its own
// port — narrower than WorkspaceProvider (workspace.go) — for the identical
// reason LocalCommitCreator (localcommit.go) is: internal/adapters/gitworktree.Provider
// structurally satisfies this via Go's structural typing with zero change to
// its own ports.WorkspaceProvider assertion, and only this task's own
// application-layer callers (internal/app/workspaceinspection) need to
// depend on this one capability.
//
// Every method here operates on an already-provisioned WorkspaceHandle — it
// never accepts a raw filesystem path, an arbitrary symbolic Git ref
// expression ("HEAD~1", "origin/main", ...), or a caller-chosen argv
// fragment. Revisions are always workspace.Revision values the caller
// already resolved from the workspace's own live Inspect() state (its
// BaseRevision or CurrentRevision, V6-10C's own "resolve exact authorized
// revision") — a real adapter implementation independently re-validates
// this (RepositoryID/WorkspaceGeneration match, VCSObjectID is a full,
// lowercase-hex commit object id, and the id equals one of the workspace's
// own two known-good revisions) rather than trusting the caller, mirroring
// WorkspaceProvider.Diff's existing "base revision does not belong to
// workspace" check. This is the structural reason "arbitrary ref
// expression/argv" (this task's own Không làm line) can never reach a real
// `git` invocation: nothing accepted here can ever be interpreted as a Git
// option (a valid object id can never start with "-") or expand to more
// than exactly one already-known commit.
//
// No method here can mutate anything: there is no write path, no HEAD move,
// no commit, no working-tree change — every real `git` invocation a
// conforming adapter runs is one of {cat-file, ls-tree, diff, log,
// merge-base, rev-parse}, all read-only by construction.
type WorkspaceInspectionReader interface {
	// ReadSource returns req.Path's exact blob content as it exists at
	// req.Revision — never the live working tree, even when req.Revision
	// equals the workspace's current HEAD (a concurrent write to the
	// workspace after req.Revision was resolved must never change what this
	// call returns). req.Path is a Git tree path (forward-slash separated,
	// relative to the repository root) the adapter independently normalizes
	// and validates before ever building a Git object spec from it —
	// rejecting an empty path, a NUL byte, a ".."  segment, a leading "/"
	// or a leading "-" (ErrInvalidSpec) — and independently resolves the
	// path's own tree entry (via `git ls-tree`) before reading its content,
	// refusing a directory, a symlink (mode 120000) or a submodule gitlink
	// (mode 160000) entry outright (ErrUnsupportedEntry) rather than ever
	// returning a symlink's own target text as if it were real file
	// content — V6-10C's own "reject traversal, symlink/reparse".
	ReadSource(ctx context.Context, req ReadSourceRequest) (SourceContent, error)
	// ReadDiff returns the exact patch between req.BaseRevision and
	// req.ResultRevision — two independently authorized, already-resolved
	// commits (never the live working tree) — bounded by req.ByteLimit
	// (total patch bytes) and req.FileLimit (file entries in the returned
	// summary).
	ReadDiff(ctx context.Context, req ReadDiffRequest) (DiffContent, error)
	// ReadRepositoryLog returns one page of req.Anchor's own commit
	// ancestry, starting at req.Anchor (req.Cursor empty) or resuming
	// immediately after req.Cursor's own commit (req.Cursor non-empty) —
	// V6-10C's own "log exact anchor+cursor". A non-empty req.Cursor is
	// independently re-validated as a real commit that is req.Anchor itself
	// or one of its ancestors (`git merge-base --is-ancestor`) before it is
	// ever used to seed a `git log` invocation: a caller cannot use Cursor
	// to walk history outside the one ancestry chain req.Anchor already
	// authorized.
	ReadRepositoryLog(ctx context.Context, req ReadRepositoryLogRequest) (RepositoryLogPage, error)
}

// ReadSourceRequest is what a caller supplies to
// WorkspaceInspectionReader.ReadSource. ByteLimit/LineLimit are always
// already-clamped, positive values by the time a real adapter sees them —
// clamping caller input into a sane [1, max] range is the application
// query's own job (internal/app/workspaceinspection), not this port's.
type ReadSourceRequest struct {
	Handle    WorkspaceHandle
	Revision  workspace.Revision
	Path      string
	ByteLimit int64
	LineLimit int64
}

// SourceContent is ReadSource's bounded result. TotalBytes is the file's
// real size at Revision even when Truncated is true (so a caller can show
// "showing the first N of TotalBytes bytes" without a second round trip).
// Binary and Truncated are always explicit — never inferred by a caller
// from Content's own shape (V6-10C's own "binary and truncation typed").
type SourceContent struct {
	Path       string
	Revision   workspace.Revision
	Content    []byte
	ByteLimit  int64
	LineLimit  int64
	TotalBytes int64
	LineCount  int64
	Truncated  bool
	Binary     bool
}

// ReadDiffRequest is what a caller supplies to
// WorkspaceInspectionReader.ReadDiff.
type ReadDiffRequest struct {
	Handle         WorkspaceHandle
	BaseRevision   workspace.Revision
	ResultRevision workspace.Revision
	ByteLimit      int64
	FileLimit      int
}

// DiffFileChange is one changed path's own bounded summary — additions/
// deletions are both zero and Binary is true for a file Git itself cannot
// diff as text (mirrors `git diff --numstat`'s own "-\t-\tpath" marker).
type DiffFileChange struct {
	Path      string
	Additions int64
	Deletions int64
	Binary    bool
}

// DiffContent is ReadDiff's bounded result. FilesTruncated/PatchTruncated
// are independent: a diff can have more changed files than FileLimit while
// its raw Patch still fits within ByteLimit, or vice versa.
type DiffContent struct {
	BaseRevision   workspace.Revision
	ResultRevision workspace.Revision
	Files          []DiffFileChange
	Patch          []byte
	ByteLimit      int64
	FileLimit      int
	FilesTruncated bool
	PatchTruncated bool
}

// ReadRepositoryLogRequest is what a caller supplies to
// WorkspaceInspectionReader.ReadRepositoryLog. Limit bounds how many commits
// one page returns; ByteLimit bounds the raw `git log` output a real
// adapter ever reads into memory for one call, regardless of how large an
// individual commit's own message is.
type ReadRepositoryLogRequest struct {
	Handle    WorkspaceHandle
	Anchor    workspace.Revision
	Cursor    string
	Limit     int
	ByteLimit int64
}

// RepositoryLogEntry is one commit in a RepositoryLogPage. Subject is
// already bounded to a fixed maximum length by a real adapter (never the
// full, potentially unbounded commit message body) — SubjectTruncated
// reports whether that bound actually cut it.
type RepositoryLogEntry struct {
	CommitID         string
	ParentIDs        []string
	AuthorName       string
	AuthorEmail      string
	AuthoredAt       time.Time
	Subject          string
	SubjectTruncated bool
}

// RepositoryLogPage is ReadRepositoryLog's bounded, one-page result.
// NextCursor is empty when Anchor's own ancestry is fully exhausted —
// never when Truncated stopped the page early solely because ByteLimit was
// reached (a caller must still be able to resume from the last real entry
// this call did manage to return in that case, so NextCursor is always the
// last returned Entries' own CommitID whenever any entry is truncated away
// by ByteLimit rather than exhausted by real history).
type RepositoryLogPage struct {
	Anchor     workspace.Revision
	Entries    []RepositoryLogEntry
	NextCursor string
	Limit      int
	ByteLimit  int64
	Truncated  bool
}
