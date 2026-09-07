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
