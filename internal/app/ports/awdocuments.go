package ports

import "context"

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
}

// AwDocumentsExport is what an export did: each file of .aw/features/<rest> copied to docs/features/<rest>.
type AwDocumentsExport struct {
	Exported  []string `json:"exported"`
	Unchanged []string `json:"unchanged"`
	// Conflicts are targets that exist with other content; with any, nothing is written unless overwrite was asked.
	Conflicts []string `json:"conflicts"`
}

// AwDocumentStore reads and exports a worktree's aw-private documents (the Git workspace provider).
type AwDocumentStore interface {
	ListAwDocuments(ctx context.Context, handle WorkspaceHandle) ([]AwDocument, error)
	ExportAwDocuments(ctx context.Context, handle WorkspaceHandle, overwrite bool) (AwDocumentsExport, error)
}
