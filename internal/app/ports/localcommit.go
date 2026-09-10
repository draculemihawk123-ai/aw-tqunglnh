package ports

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// LocalCommitCreator creates a typed, audited local Git commit inside a
// workspace this WorkspaceProvider issued the handle for (V5-10A,
// docs/design/07-v5-execution-evidence.md; AK-ARCH-015C: "Alpha từ chối
// push/PR/merge/force-push trước khi adapter remote được gọi"). That
// rejection is structural, not a runtime check this interface performs:
// CreateLocalCommit is the only Git-mutating method this port — or any
// port in this codebase — declares, so no caller can reach a remote
// operation through it even in principle; there is no remote adapter for
// one to reach.
//
// Declared here as its own narrow port rather than a widening of
// WorkspaceProvider (workspace.go), mirroring WorkspaceDirectoryResolver's
// own reasoning (readiness.go): internal/adapters/gitworktree.Provider
// structurally satisfies this via Go's structural typing with zero change
// to its own ports.WorkspaceProvider assertion, and only this task's own
// application-layer callers need to depend on this one capability — every
// existing WorkspaceProvider caller is untouched.
type LocalCommitCreator interface {
	CreateLocalCommit(ctx context.Context, req CreateLocalCommitRequest) (workspace.Revision, error)
}

// CreateLocalCommitRequest is what a caller supplies to
// LocalCommitCreator.CreateLocalCommit. AuthorName/AuthorEmail make the
// commit's own audit trail explicit (a real `git commit` in a fresh
// worktree cannot rely on ambient user.name/user.email being configured)
// rather than implicit ambient Git config — "typed/audited" (this task's
// own Thực hiện line) means the caller states who is committing and why,
// not that the adapter infers it.
type CreateLocalCommitRequest struct {
	Handle      WorkspaceHandle
	Message     string
	AuthorName  string
	AuthorEmail string
}
