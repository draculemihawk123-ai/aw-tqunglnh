package ports

import (
	"context"
	"errors"
)

// ErrTreeNotFound is returned by WorkspaceTreeSnapshotter.DiffTrees when a
// tree object it was asked about does not exist (ADR-030, V9-01). A tree
// object has no ref pointing at it, so `git gc` may prune it once the
// repository's prune grace period lapses; the engine treats this as a
// technical failure of the attempt that needed the tree and never
// re-snapshots to replace one it recorded (ADR-030 "Hệ quả").
var ErrTreeNotFound = errors.New("workspace tree object not found")

// WorkspaceTreeSnapshotter is the narrow port ADR-030 (V9-01) adds beside
// WorkspaceProvider so the engine can measure "did THIS attempt change
// anything" for a read-only attempt (a CHECKER-role AGENT or a MACHINE_GATE)
// without being fooled by changes an EARLIER node of the same run left
// uncommitted in the worktree (ADR-014 keeps a maker's changes uncommitted
// until ReleaseSet/local commit, so `Diff` against the pinned commit always
// shows them).
//
// A separate interface rather than two more WorkspaceProvider methods on
// purpose: WorkspaceProvider has dozens of test doubles across the repo, and
// only the real git adapter can answer these. Callers type-assert the
// provider they were handed; a provider that does not implement this port
// leaves read-only attempts on the stricter pre-V9-01 rule (the `Diff` must
// be completely empty) — fail-closed, never fail-open.
type WorkspaceTreeSnapshotter interface {
	// SnapshotTree returns the ID of a tree object describing the working
	// tree content of handle's workspace right now: every tracked file plus
	// every untracked file that is not ignored. It must not touch the
	// workspace's own index, and must not create a commit, ref or branch.
	// On a clean workspace the result equals the tree of HEAD, which is what
	// keeps the pre-V9-01 behavior for a workspace nothing has changed.
	SnapshotTree(ctx context.Context, handle WorkspaceHandle) (treeID string, err error)

	// DiffTrees returns the paths (slash-separated, repository-relative,
	// sorted as git emits them) whose content or mode differs between tree
	// a and tree b. It returns ErrTreeNotFound (possibly wrapped) when
	// either tree object does not exist; DiffTrees(h, id, id) is therefore
	// also the cheapest way to ask "does this tree object still exist".
	DiffTrees(ctx context.Context, handle WorkspaceHandle, a, b string) ([]string, error)
}
