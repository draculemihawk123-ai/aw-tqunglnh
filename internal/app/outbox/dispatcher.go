// Package outbox drives at-least-once delivery of durable outbox
// messages (docs/design/03-v1-alpha-foundation.md V1-07). The persistence
// side (claim/mark-dispatched/reclaim-expired-lease) is a
// ports.OutboxDispatchStore: an adapter (internal/adapters/sqlite) writes
// the outbox row transactionally with the domain event it carries
// (GC-INV-16); Dispatcher only ever reads/claims/confirms it afterward,
// outside that transaction (ADR-008 §11.1).
package outbox

import (
	"context"
	"errors"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Dispatcher drives one outbox message at a time through Store and Sink.
type Dispatcher struct {
	Store ports.OutboxDispatchStore
	Sink  ports.OutboxSink
}

// DispatchNext claims the next ready message and delivers it through
// Sink, confirming the claim only after Sink.Deliver succeeds. It reports
// (false, nil) when there is nothing ready to dispatch.
//
// A lease claimed here but never confirmed — the process crashes, or
// Sink.Deliver itself fails — is left LEASED: a later call to
// RecoverExpiredLeases makes the message claimable again once its lease
// expires, and Sink.Deliver is invoked again for the exact same message.
// This is the "idempotent dispatcher cursor" V1-07 asks for: at-least-once
// delivery, with Sink responsible for making a repeat delivery of the
// same EventID a no-op rather than a second logical effect.
func (d *Dispatcher) DispatchNext(ctx context.Context, owner string, ttl time.Duration) (bool, error) {
	msg, lease, err := d.Store.ClaimNextOutboxMessage(ctx, owner, ttl)
	if errors.Is(err, ports.ErrNoOutboxMessageAvailable) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := d.Sink.Deliver(ctx, msg); err != nil {
		return true, err
	}
	return true, d.Store.MarkOutboxMessageDispatched(ctx, lease)
}

// RecoverExpiredLeases reclaims every outbox message whose lease expired
// without a confirmed dispatch, making each one claimable again.
func (d *Dispatcher) RecoverExpiredLeases(ctx context.Context) (int64, error) {
	return d.Store.RecoverExpiredOutboxLeases(ctx)
}
