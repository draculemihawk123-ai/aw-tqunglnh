package ports

import (
	"context"
	"errors"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// ErrWorkspaceQuarantined reports that a RepositoryWorkspace cannot be
// released, or accept a new writer, while it is QUARANTINED. Nothing
// un-quarantines a generation in place; RecreateRepositoryWorkspace is the
// only way past it, and it always produces a new generation.
var ErrWorkspaceQuarantined = errors.New("repository workspace is quarantined")

// QuarantineRepositoryWorkspaceUpdate is the fenced CAS transition that moves
// a RepositoryWorkspace from READY to QUARANTINED once a fresh worker cannot
// prove a lease-losing writer's mutation is absent (see
// worker.ReconcileMutatingAttempt). It never itself decides whether
// quarantine is warranted; the caller supplies Reason from that decision.
type QuarantineRepositoryWorkspaceUpdate struct {
	RepositoryWorkspaceID workspace.RepositoryWorkspaceID
	ExpectedVersion       uint64
	Reason                string
	EventID               string
	CorrelationID         string
	OccurredAt            time.Time
}

// AdvanceRepositoryWorkspaceRevisionUpdate records that a READY
// RepositoryWorkspace's HEAD moved to Revision because aw itself committed to
// it (a ReleaseSet local commit). RepositoryWorkspace.CurrentRevision is what
// a new Run pins as the revision it starts from; before V9-16 it stayed at the
// revision the worktree was provisioned from, so the second Run of a family
// pinned a revision that was no longer HEAD and every MACHINE_GATE of it failed
// its freshness check. Fenced on (id, state=READY, version): a workspace that
// was quarantined or released since the caller read it is never advanced.
type AdvanceRepositoryWorkspaceRevisionUpdate struct {
	RepositoryWorkspaceID workspace.RepositoryWorkspaceID
	ExpectedVersion       uint64
	Revision              string
	OccurredAt            time.Time
}

// ReleaseRepositoryWorkspaceUpdate is the fenced CAS transition from READY to
// RELEASED. It deliberately has no path out of QUARANTINED: cleanup of a
// quarantined generation is refused with ErrWorkspaceQuarantined, because a
// stale writer may still hold a live process handle on it
// (docs/design/02-v0-spike-verdict.md V0-07).
type ReleaseRepositoryWorkspaceUpdate struct {
	RepositoryWorkspaceID workspace.RepositoryWorkspaceID
	ExpectedVersion       uint64
	EventID               string
	CorrelationID         string
	OccurredAt            time.Time
}

// RecreateRepositoryWorkspaceRequest reconciles a QUARANTINED
// RepositoryWorkspace by inserting the next generation as a new, independent
// row; the previous generation's row is left QUARANTINED permanently as
// evidence, never reused. The new generation number is always the previous
// row's generation plus one, decided by the store, not the caller: it is a
// monotonic counter like fence_token or lease_token elsewhere in this
// package. Callers provision the new physical workspace
// (WorkspaceProvider.Provision with Generation = previous+1, which the
// caller can compute from the RepositoryWorkspace it already holds) before
// calling this, then persist the resulting locator/branch here as the new
// runtime-authoritative generation.
type RecreateRepositoryWorkspaceRequest struct {
	PreviousRepositoryWorkspaceID workspace.RepositoryWorkspaceID
	PreviousExpectedVersion       uint64
	NewRepositoryWorkspaceID      workspace.RepositoryWorkspaceID
	Locator                       string
	BranchRef                     string
	BaseRevision                  string
	EventID                       string
	CorrelationID                 string
	OccurredAt                    time.Time
}

// WorkspaceLifecycle is the runtime-authority state machine for a
// RepositoryWorkspace: READY -> QUARANTINED -> superseded by a new
// generation, or READY -> RELEASED. It is deliberately separate from
// WorkspaceProvider: this port owns which generation is authoritative for
// writers and evidence; WorkspaceProvider owns the physical worktree.
type WorkspaceLifecycle interface {
	QuarantineRepositoryWorkspace(context.Context, QuarantineRepositoryWorkspaceUpdate) error
	ReleaseRepositoryWorkspace(context.Context, ReleaseRepositoryWorkspaceUpdate) error
	RecreateRepositoryWorkspace(context.Context, RecreateRepositoryWorkspaceRequest) (workspace.RepositoryWorkspace, error)
}
