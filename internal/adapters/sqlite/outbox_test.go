package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func appendTestEvent(t *testing.T, ctx context.Context, store *Store, id string) {
	t.Helper()
	event := ports.DomainEvent{
		ID: id, AggregateType: "Project", AggregateID: id, Sequence: 1,
		EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{"id":"` + id + `"}`,
		CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
	}
	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return appendEvent(ctx, tx, event)
	}); err != nil {
		t.Fatalf("Append(%s): %v", id, err)
	}
}

func TestClaimNextOutboxMessage_NoneAvailable(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-outbox-none.db")

	_, _, err := store.ClaimNextOutboxMessage(ctx, "dispatcher-1", time.Minute)
	if !errors.Is(err, ports.ErrNoOutboxMessageAvailable) {
		t.Fatalf("err = %v, want ports.ErrNoOutboxMessageAvailable", err)
	}
}

func TestClaimNextOutboxMessage_ClaimsOldestFirst(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-outbox-oldest.db")
	appendTestEvent(t, ctx, store, "evt-1")
	appendTestEvent(t, ctx, store, "evt-2")

	msg, lease, err := store.ClaimNextOutboxMessage(ctx, "dispatcher-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextOutboxMessage: %v", err)
	}
	if msg.EventID != "evt-1" {
		t.Fatalf("claimed event_id = %q, want %q (oldest first)", msg.EventID, "evt-1")
	}
	if msg.Status != ports.OutboxLeased {
		t.Fatalf("status = %q, want %q", msg.Status, ports.OutboxLeased)
	}
	if lease.MessageID != msg.ID || lease.Owner != "dispatcher-1" || lease.Token != msg.LeaseToken {
		t.Fatalf("lease = %+v, want it to match claimed message %+v", lease, msg)
	}

	// The already-claimed row must not be claimable again by another dispatcher.
	msg2, _, err := store.ClaimNextOutboxMessage(ctx, "dispatcher-2", time.Minute)
	if err != nil {
		t.Fatalf("second ClaimNextOutboxMessage: %v", err)
	}
	if msg2.EventID != "evt-2" {
		t.Fatalf("second claimed event_id = %q, want %q", msg2.EventID, "evt-2")
	}
}

func TestMarkOutboxMessageDispatched_Succeeds(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-outbox-mark.db")
	appendTestEvent(t, ctx, store, "evt-1")

	_, lease, err := store.ClaimNextOutboxMessage(ctx, "dispatcher-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextOutboxMessage: %v", err)
	}
	if err := store.MarkOutboxMessageDispatched(ctx, lease); err != nil {
		t.Fatalf("MarkOutboxMessageDispatched: %v", err)
	}

	var status string
	if err := store.db.QueryRowContext(ctx, `SELECT status FROM outbox WHERE id = ?`, lease.MessageID).Scan(&status); err != nil {
		t.Fatalf("read back status: %v", err)
	}
	if status != string(ports.OutboxDispatched) {
		t.Fatalf("status = %q, want %q", status, ports.OutboxDispatched)
	}
}

func TestMarkOutboxMessageDispatched_StaleToken_ReturnsLeaseLost(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-outbox-stale-token.db")
	appendTestEvent(t, ctx, store, "evt-1")

	_, lease, err := store.ClaimNextOutboxMessage(ctx, "dispatcher-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimNextOutboxMessage: %v", err)
	}
	staleLease := lease
	staleLease.Token = lease.Token + 999

	err = store.MarkOutboxMessageDispatched(ctx, staleLease)
	if !errors.Is(err, ports.ErrOutboxLeaseLost) {
		t.Fatalf("err = %v, want ports.ErrOutboxLeaseLost", err)
	}
}

func TestRecoverExpiredOutboxLeases_ReclaimsExpiredLease(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-outbox-recover.db")
	appendTestEvent(t, ctx, store, "evt-1")

	_, lease, err := store.ClaimNextOutboxMessage(ctx, "dispatcher-1", 20*time.Millisecond)
	if err != nil {
		t.Fatalf("ClaimNextOutboxMessage: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	recovered, err := store.RecoverExpiredOutboxLeases(ctx)
	if err != nil {
		t.Fatalf("RecoverExpiredOutboxLeases: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered = %d, want 1", recovered)
	}

	// The stale lease must no longer be able to confirm dispatch...
	if err := store.MarkOutboxMessageDispatched(ctx, lease); !errors.Is(err, ports.ErrOutboxLeaseLost) {
		t.Fatalf("MarkOutboxMessageDispatched with reclaimed lease err = %v, want ports.ErrOutboxLeaseLost", err)
	}
	// ...but the message must be claimable again.
	msg, _, err := store.ClaimNextOutboxMessage(ctx, "dispatcher-2", time.Minute)
	if err != nil {
		t.Fatalf("re-claim after recovery: %v", err)
	}
	if msg.EventID != "evt-1" {
		t.Fatalf("re-claimed event_id = %q, want %q", msg.EventID, "evt-1")
	}
}
