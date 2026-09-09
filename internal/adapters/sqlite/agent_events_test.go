package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func appendAgentEvents(ctx context.Context, tx *sql.Tx, records []ports.AgentEventRecord) error {
	return agentEventsRepository{tx: tx}.AppendBatch(ctx, records)
}

func seedAgentEventsFixtureAttempt(ctx context.Context, store *Store, attemptID string) error {
	if err := SeedFixtureOwners(ctx, store, "proj-1", "family-1", "work-1"); err != nil {
		return err
	}
	return SeedFixtureExecutionAttempt(ctx, store, "proj-1", "family-1", "work-1", attemptID)
}

func TestAgentEventsRepository_AppendBatch_PersistsRowsOrderedBySequence(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-agent-events-append.db")
	const attemptID = "attempt-1"
	if err := seedAgentEventsFixtureAttempt(ctx, store, attemptID); err != nil {
		t.Fatalf("seed fixture attempt: %v", err)
	}

	now := time.Now().UTC()
	records := []ports.AgentEventRecord{
		{ID: "evt-1", AttemptID: attemptID, Sequence: 1, Kind: "EXECUTION_STARTED", SchemaVersion: 1, PayloadJSON: `{}`, CreatedAt: now},
		{ID: "evt-2", AttemptID: attemptID, Sequence: 2, Kind: "ASSISTANT_MESSAGE", SchemaVersion: 1, PayloadJSON: `{"message":"hi"}`, CreatedAt: now},
	}
	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return appendAgentEvents(ctx, tx, records)
	}); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}

	var loaded []ports.AgentEventRecord
	if err := store.RunReadOnly(ctx, func(tx *sql.Tx) error {
		var err error
		loaded, err = agentEventsRepository{tx: tx}.ListByAttempt(ctx, attemptID)
		return err
	}); err != nil {
		t.Fatalf("ListByAttempt: %v", err)
	}
	if len(loaded) != 2 || loaded[0].Sequence != 1 || loaded[0].Kind != "EXECUTION_STARTED" ||
		loaded[1].Sequence != 2 || loaded[1].Kind != "ASSISTANT_MESSAGE" {
		t.Fatalf("loaded = %+v, want 2 rows ordered by sequence", loaded)
	}
}

func TestAgentEventsRepository_AppendBatch_RejectsOversizedPayload(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-agent-events-oversized.db")
	const attemptID = "attempt-1"
	if err := seedAgentEventsFixtureAttempt(ctx, store, attemptID); err != nil {
		t.Fatalf("seed fixture attempt: %v", err)
	}

	oversized := strings.Repeat("x", maxAgentEventPayloadBytes+1)
	record := ports.AgentEventRecord{
		ID: "evt-1", AttemptID: attemptID, Sequence: 1, Kind: "ASSISTANT_MESSAGE",
		SchemaVersion: 1, PayloadJSON: oversized, CreatedAt: time.Now().UTC(),
	}
	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return appendAgentEvents(ctx, tx, []ports.AgentEventRecord{record})
	})
	if !errors.Is(err, ErrAgentEventPayloadTooLarge) {
		t.Fatalf("AppendBatch oversized payload error = %v, want ErrAgentEventPayloadTooLarge", err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_events WHERE attempt_id = ?`, attemptID).Scan(&count); err != nil {
		t.Fatalf("count agent_events: %v", err)
	}
	if count != 0 {
		t.Fatalf("agent_events rows after rejected oversized payload = %d, want 0", count)
	}
}

func TestAgentEventsRepository_AppendBatch_RejectsDuplicateSequence(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-agent-events-duplicate.db")
	const attemptID = "attempt-1"
	if err := seedAgentEventsFixtureAttempt(ctx, store, attemptID); err != nil {
		t.Fatalf("seed fixture attempt: %v", err)
	}

	first := ports.AgentEventRecord{ID: "evt-1", AttemptID: attemptID, Sequence: 1, Kind: "EXECUTION_STARTED", SchemaVersion: 1, PayloadJSON: `{}`, CreatedAt: time.Now().UTC()}
	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return appendAgentEvents(ctx, tx, []ports.AgentEventRecord{first})
	}); err != nil {
		t.Fatalf("AppendBatch (first): %v", err)
	}

	duplicate := ports.AgentEventRecord{ID: "evt-1-again", AttemptID: attemptID, Sequence: 1, Kind: "DIAGNOSTIC", SchemaVersion: 1, PayloadJSON: `{}`, CreatedAt: time.Now().UTC()}
	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return appendAgentEvents(ctx, tx, []ports.AgentEventRecord{duplicate})
	})
	if err == nil {
		t.Fatal("AppendBatch (duplicate attempt_id+sequence) succeeded, want a UNIQUE constraint error")
	}
}

// TestAgentEventsRepository_AppendBatch_RoundTripsArtifactRefs is the V5-08B
// audit finding fix (deferred from V5-08A, 2026-09-09): AgentEventRecord's
// own ArtifactRefs field did not exist at all, so the INSERT statement
// hardcoded artifact_refs_json='[]' regardless of what the caller passed —
// silently discarding an ARTIFACT_PRODUCED event's own real artifact
// references. Proves both directions: a record WITH refs round-trips them
// exactly, and a record with none (nil) reads back as an empty, non-nil
// slice rather than erroring on the stored '[]'.
func TestAgentEventsRepository_AppendBatch_RoundTripsArtifactRefs(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-agent-events-artifact-refs.db")
	const attemptID = "attempt-1"
	if err := seedAgentEventsFixtureAttempt(ctx, store, attemptID); err != nil {
		t.Fatalf("seed fixture attempt: %v", err)
	}

	withRefs := ports.AgentEventRecord{
		ID: "evt-1", AttemptID: attemptID, Sequence: 1, Kind: "ARTIFACT_PRODUCED", SchemaVersion: 1,
		PayloadJSON: `{}`, ArtifactRefs: []string{"artifact-a", "artifact-b"}, CreatedAt: time.Now().UTC(),
	}
	withoutRefs := ports.AgentEventRecord{
		ID: "evt-2", AttemptID: attemptID, Sequence: 2, Kind: "EXECUTION_FINISHED", SchemaVersion: 1,
		PayloadJSON: `{}`, CreatedAt: time.Now().UTC(),
	}
	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return appendAgentEvents(ctx, tx, []ports.AgentEventRecord{withRefs, withoutRefs})
	}); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}

	var loaded []ports.AgentEventRecord
	if err := store.RunReadOnly(ctx, func(tx *sql.Tx) error {
		var err error
		loaded, err = agentEventsRepository{tx: tx}.ListByAttempt(ctx, attemptID)
		return err
	}); err != nil {
		t.Fatalf("ListByAttempt: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded = %+v, want 2 rows", loaded)
	}
	if got := loaded[0].ArtifactRefs; len(got) != 2 || got[0] != "artifact-a" || got[1] != "artifact-b" {
		t.Fatalf("loaded[0].ArtifactRefs = %#v, want [artifact-a artifact-b]", got)
	}
	if got := loaded[1].ArtifactRefs; len(got) != 0 {
		t.Fatalf("loaded[1].ArtifactRefs = %#v, want empty", got)
	}
}
