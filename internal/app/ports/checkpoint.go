package ports

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// CheckpointsRepository is V5-08B's Tx accessor for the durable
// checkpoints table (docs/design/07-v5-execution-evidence.md). It exists
// for exactly one caller: FinalizeExecutionAttempt's own COMPLETION
// checkpoint, which V5-08B's own locked decision #2 (protocol step 3,
// baocaov5checklist.md) requires to commit atomically with the rest of a
// fenced finalize transaction. agentevents.Sink's own mid-run checkpoints
// deliberately keep using the pre-existing legacy autocommit
// CheckpointStore (agentevents.go's own captureCheckpointLocked doc
// comment already explains why promoting THAT call site onto ports.Tx is
// out of scope) — this accessor is additive, not a replacement for it.
type CheckpointsRepository interface {
	// InsertCheckpoint persists checkpoint inside the caller's existing
	// transaction. Unlike the legacy CheckpointStore.StoreCheckpoint
	// (whose own autocommit path re-loads and compares on a UNIQUE
	// conflict, since more than one independent caller can legitimately
	// race to store the identical mid-run checkpoint), a completion
	// checkpoint has exactly one caller inside exactly one transaction —
	// FinalizeExecutionAttempt computes Sequence fresh every time and
	// never presents this method with an (AttemptID, Sequence) pair it
	// has already used, so a genuine UNIQUE(attempt_id, sequence)
	// violation reaching here is a real bug, surfaced as an ordinary
	// error (mirrors AgentEventsRepository.AppendBatch's own identical
	// discipline).
	InsertCheckpoint(ctx context.Context, checkpoint runtime.Checkpoint) error
}
