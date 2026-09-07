package ports

import (
	"context"
	"time"
)

// AgentEventRecord is one durable, append-only agent_events row (V5-08A,
// docs/design/07-v5-execution-evidence.md — the production contract for the
// generic schema V4-01 already owns). PayloadJSON is the caller's own
// already-normalized, already-redacted encoding of everything about the
// event except the columns broken out below; this repository never
// redacts, validates registry membership or interprets Kind itself — see
// internal/app/agentevents's own Sink, the sole intended caller, for that
// contract.
type AgentEventRecord struct {
	ID            string
	AttemptID     string
	Sequence      uint64
	Kind          string
	SchemaVersion int
	PayloadJSON   string
	CreatedAt     time.Time
}

// AgentEventsRepository is V5-08A's Tx accessor for the durable agent_events
// table (populated now, the same "gets a real interface from the start"
// treatment AdapterBuilds/Readiness/Wait/Approvals/Artifacts/Messages/
// ContextSnapshots above already received): this concern has zero
// pre-existing Go callers anywhere in this codebase, so it gets a real
// interface immediately rather than a placeholder.
type AgentEventsRepository interface {
	// AppendBatch inserts every record in one call, inside the caller's
	// existing transaction — the persistence half of V5-08A's own "batching
	// có bound" (agentevents.Sink buffers events in memory and flushes
	// through here, rather than one round trip per event). UNIQUE(attempt_id,
	// sequence) at the storage layer rejects a genuine duplicate with a
	// different payload; a byte-identical re-delivery of an already-stored
	// (AttemptID, Sequence) is a safe no-op — the same idempotent-replay
	// discipline StoreCheckpoint already established for its own sibling
	// table. Records within one call may span at most one AttemptID — see
	// agentevents.Sink's own doc comment for why a batch is always
	// single-attempt.
	AppendBatch(ctx context.Context, records []AgentEventRecord) error
	// ListByAttempt returns every AgentEventRecord for attemptID, ordered
	// oldest-Sequence-first.
	ListByAttempt(ctx context.Context, attemptID string) ([]AgentEventRecord, error)
}
