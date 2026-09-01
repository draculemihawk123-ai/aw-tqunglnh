package outbox_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/outbox"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// idempotentSink is a test double proving OutboxSink's documented
// contract: Deliver may be called more than once for the same EventID
// (Dispatcher only guarantees at-least-once), but a well-behaved Sink
// tracks EventID and only ever produces one logical effect.
type idempotentSink struct {
	mu        sync.Mutex
	delivered []ports.OutboxMessage
	seen      map[string]bool
}

func newIdempotentSink() *idempotentSink {
	return &idempotentSink{seen: map[string]bool{}}
}

func (s *idempotentSink) Deliver(_ context.Context, msg ports.OutboxMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delivered = append(s.delivered, msg)
	s.seen[msg.EventID] = true
	return nil
}

func (s *idempotentSink) deliveryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.delivered)
}

func (s *idempotentSink) effectCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

func openDispatchStore(t *testing.T, name string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func appendDomainEvent(t *testing.T, store *sqlite.Store, event ports.DomainEvent) {
	t.Helper()
	err := sqlite.NewUnitOfWork(store).WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Events().Append(context.Background(), event)
	})
	if err != nil {
		t.Fatalf("append domain event %s: %v", event.ID, err)
	}
}

func TestDispatchNext_NothingAvailable_ReturnsFalse(t *testing.T) {
	ctx := context.Background()
	store := openDispatchStore(t, "agentkit-dispatcher-empty.db")
	dispatcher := &outbox.Dispatcher{Store: store, Sink: newIdempotentSink()}

	dispatched, err := dispatcher.DispatchNext(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("DispatchNext: %v", err)
	}
	if dispatched {
		t.Fatal("DispatchNext reported a message dispatched when none was enqueued")
	}
}

// TestDispatchNext_CrashAfterCommitBeforeDispatch_StillDelivers is V1-07's
// own "crash after commit before dispatch" Verify requirement: the
// domain event and its outbox row commit durably regardless of whether a
// dispatcher ever runs; a dispatcher started later still finds and
// delivers it.
func TestDispatchNext_CrashAfterCommitBeforeDispatch_StillDelivers(t *testing.T) {
	ctx := context.Background()
	store := openDispatchStore(t, "agentkit-dispatcher-crash-before-dispatch.db")
	appendDomainEvent(t, store, ports.DomainEvent{
		ID: "evt-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1,
		EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{"id":"proj-1"}`,
		CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
	})

	// Nothing dispatches the message yet — simulating a crash (or simply a
	// dispatcher that hasn't started) right after the commit above.

	sink := newIdempotentSink()
	dispatcher := &outbox.Dispatcher{Store: store, Sink: sink}
	dispatched, err := dispatcher.DispatchNext(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("DispatchNext: %v", err)
	}
	if !dispatched {
		t.Fatal("DispatchNext found nothing to deliver; the committed outbox row was lost")
	}
	if sink.deliveryCount() != 1 || sink.delivered[0].EventID != "evt-1" {
		t.Fatalf("delivered = %+v, want exactly one delivery for evt-1", sink.delivered)
	}
}

// TestDispatchNext_DuplicateDeliveryDoesNotCreateTwoLogicalEffects is
// V1-07's own "duplicate delivery" Verify requirement and completion bar
// ("delivery lặp không tạo hai logical effects"): a dispatcher that
// delivered a message but crashed before confirming it (never calling
// MarkOutboxMessageDispatched) redelivers the exact same message once its
// lease is recovered — at-least-once, not exactly-once — but an
// idempotent Sink collapses that into a single logical effect.
func TestDispatchNext_DuplicateDeliveryDoesNotCreateTwoLogicalEffects(t *testing.T) {
	ctx := context.Background()
	store := openDispatchStore(t, "agentkit-dispatcher-duplicate.db")
	appendDomainEvent(t, store, ports.DomainEvent{
		ID: "evt-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1,
		EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{"id":"proj-1"}`,
		CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
	})

	sink := newIdempotentSink()

	// Simulate a crashed dispatcher: claim and deliver, but never confirm.
	msg, _, err := store.ClaimNextOutboxMessage(ctx, "worker-1", 20*time.Millisecond)
	if err != nil {
		t.Fatalf("ClaimNextOutboxMessage: %v", err)
	}
	if err := sink.Deliver(ctx, msg); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let the lease expire

	if _, err := store.RecoverExpiredOutboxLeases(ctx); err != nil {
		t.Fatalf("RecoverExpiredOutboxLeases: %v", err)
	}

	dispatcher := &outbox.Dispatcher{Store: store, Sink: sink}
	dispatched, err := dispatcher.DispatchNext(ctx, "worker-2", time.Minute)
	if err != nil {
		t.Fatalf("DispatchNext (redelivery): %v", err)
	}
	if !dispatched {
		t.Fatal("expected the recovered message to be redelivered")
	}

	if sink.deliveryCount() != 2 {
		t.Fatalf("deliveryCount = %d, want 2 (the crashed attempt plus the recovered redelivery)", sink.deliveryCount())
	}
	if sink.effectCount() != 1 {
		t.Fatalf("effectCount = %d, want 1 (a repeat delivery of the same EventID must not create a second logical effect)", sink.effectCount())
	}

	// The message is now confirmed dispatched — a further call must find nothing to do.
	dispatchedAgain, err := dispatcher.DispatchNext(ctx, "worker-3", time.Minute)
	if err != nil {
		t.Fatalf("DispatchNext (after confirm): %v", err)
	}
	if dispatchedAgain {
		t.Fatal("message was already confirmed dispatched, should not be claimable again")
	}
}
