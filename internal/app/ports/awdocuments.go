package ports

import (
	"context"
	"time"
)

// V12-33 (ADR-065): the aw-private directory of a worktree. Git ignores it (aw lists it in the repository's info/exclude), so what an
// agent writes there is never committed and never a scope violation; the kit keeps per-task working documents in
// .aw/features/<feature>/tasks/<task>/ when the project chose the AW location.

// AwPrivateDirectory is the aw-private directory at a worktree's root.
const AwPrivateDirectory = ".aw"

// AwDocument is one file under .aw/features/ (Path is relative to the worktree, with "/").
type AwDocument struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
	// Source is WORKTREE (read from the worktree) or SNAPSHOT (aw's copy: the worktree no longer has it, or is gone).
	Source string `json:"source,omitempty"`
	// SnapshotAt is when aw last copied it (empty: never copied yet).
	SnapshotAt *time.Time `json:"snapshotAt,omitempty"`
}

// AwDocument sources.
const (
	AwDocumentFromWorktree = "WORKTREE"
	AwDocumentFromSnapshot = "SNAPSHOT"
)

// AwDocumentsExport is what an export did: each file of .aw/features/<rest> copied to docs/features/<rest>.
type AwDocumentsExport struct {
	Exported  []string `json:"exported"`
	Unchanged []string `json:"unchanged"`
	// Conflicts are targets that exist with other content; with any, nothing is written unless overwrite was asked.
	Conflicts []string `json:"conflicts"`
}

// AwDocumentStore reads a worktree's aw-private documents and writes documents into its docs/features/ (the Git workspace provider).
type AwDocumentStore interface {
	ListAwDocuments(ctx context.Context, handle WorkspaceHandle) ([]AwDocument, error)
	// WriteFeatureDocuments writes each document's content at docs/features/<rest> for its path .aw/features/<rest>; with a target
	// that exists with other content and overwrite false, it writes nothing and lists the conflicts.
	WriteFeatureDocuments(ctx context.Context, handle WorkspaceHandle, docs []AwDocument, overwrite bool) (AwDocumentsExport, error)
}

// AwDocumentSnapshot is aw's latest copy of one aw-private document (an ATTACHED artifact), kept when the worktree goes away.
type AwDocumentSnapshot struct {
	FamilyID     string
	RepositoryID string
	// Path is relative to the worktree (".aw/features/<feature>/tasks/<task>/plan.md").
	Path       string
	ProjectID  string
	ArtifactID string
	SHA256     string
	Size       int64
	UpdatedAt  time.Time
}

// AwDocumentSnapshotRepository is the Tx accessor of the snapshots.
type AwDocumentSnapshotRepository interface {
	ListAwDocumentSnapshots(ctx context.Context, familyID, repositoryID string) ([]AwDocumentSnapshot, error)
	// PutAwDocumentSnapshot inserts or replaces the snapshot of (family, repository, path).
	PutAwDocumentSnapshot(ctx context.Context, snapshot AwDocumentSnapshot) error
}
