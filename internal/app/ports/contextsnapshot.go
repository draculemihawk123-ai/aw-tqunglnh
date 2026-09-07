package ports

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
)

// ContextSnapshotRepository is V5-04's Tx accessor for the durable,
// immutable message/resource/RevisionSet manifest bound to one
// ExecutionAttempt (docs/design/07-v5-execution-evidence.md V5-04;
// GC-INV-08: "Mỗi NodeRun pin input hash, effective scope và execution
// profile trước attempt đầu tiên"). It is a separate accessor from the
// legacy runtime.ContextSnapshot's own storage (context_snapshots table,
// still owned by WorkflowPersistence for crash-recovery) — see
// internal/domain/contextsnapshot's own package doc comment for why the
// two are deliberately kept apart.
type ContextSnapshotRepository interface {
	// CreateSnapshot persists a new Snapshot row. The caller MUST have
	// already inserted the owning ExecutionAttempt row (with its own
	// ContextSnapshotID already set to snapshot.ID) inside the SAME
	// transaction, in that order — attempt_context_snapshots.attempt_id
	// has a real foreign key into execution_attempts(id), so the Attempt
	// row must already exist by the time this call runs. Idempotent by
	// ID: a duplicate insert of the identical ID returns the
	// already-stored row.
	CreateSnapshot(ctx context.Context, snapshot contextsnapshot.Snapshot) (contextsnapshot.Snapshot, error)
	// GetSnapshot returns the Snapshot with the given ID, or
	// ErrPersistenceNotFound.
	GetSnapshot(ctx context.Context, id string) (contextsnapshot.Snapshot, error)
	// GetSnapshotByAttemptID returns the one Snapshot bound to attemptID
	// (UNIQUE(attempt_id) — every ExecutionAttempt has at most one), or
	// ErrPersistenceNotFound if none is bound yet. This is what
	// ExecuteNodeHandler's own dispatch-time verification (V4-05) uses:
	// it never needs to read ExecutionAttempt.ContextSnapshotID back
	// through the runtime repository at all, only the AttemptID it
	// already has in hand.
	GetSnapshotByAttemptID(ctx context.Context, attemptID string) (contextsnapshot.Snapshot, error)
}
