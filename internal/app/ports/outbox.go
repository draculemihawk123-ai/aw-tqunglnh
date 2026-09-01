package ports

import (
	"context"
	"errors"
	"time"
)

// OutboxStatus is one outbox message's delivery lifecycle state.
type OutboxStatus string

const (
	OutboxAvailable  OutboxStatus = "AVAILABLE"
	OutboxLeased     OutboxStatus = "LEASED"
	OutboxDispatched OutboxStatus = "DISPATCHED"
)

// OutboxMessage is one durable delivery record. EventsRepository.Append
// writes exactly one of these in the same transaction as the domain event
// it carries (docs/design/03-v1-alpha-foundation.md V1-07, GC-INV-16), so
// a committed event can never end up missing its outbox record even if
// the process crashes immediately after commit. Publishing/dispatch
// itself always happens outside that transaction (ADR-008 §11.1: "Job/
// outbox được ghi cùng transition... publisher/executor xử lý ngoài
// transaction").
type OutboxMessage struct {
	ID          string
	EventID     string
	Topic       string
	PayloadJSON string
	Status      OutboxStatus
	AvailableAt time.Time
	LeaseOwner  string
	LeaseToken  uint64
	LeaseUntil  *time.Time
	CreatedAt   time.Time
}

// OutboxLease is the fencing proof ClaimNextOutboxMessage hands back for
// the one message it just claimed — the same pattern JobLease uses for
// durable jobs (internal/app/ports/scheduling.go): MarkOutboxMessageDispatched
// must present the exact owner and token, never just the message ID, so a
// lease a dispatcher lost to expiry/reclaim can never falsely confirm
// delivery out from under whoever reclaimed it.
type OutboxLease struct {
	MessageID string
	Owner     string
	Token     uint64
}

var (
	ErrNoOutboxMessageAvailable = errors.New("no outbox message is available to dispatch")
	ErrOutboxLeaseLost          = errors.New("outbox message lease is no longer authoritative")
)

// OutboxDispatchStore is the persistence side of outbox delivery: claim
// the next ready message under a fencing lease, confirm it dispatched, or
// reclaim leases a crashed dispatcher never confirmed.
type OutboxDispatchStore interface {
	ClaimNextOutboxMessage(ctx context.Context, owner string, ttl time.Duration) (OutboxMessage, OutboxLease, error)
	MarkOutboxMessageDispatched(ctx context.Context, lease OutboxLease) error
	RecoverExpiredOutboxLeases(ctx context.Context) (int64, error)
}

// OutboxSink delivers one outbox message to whatever real transport a
// later task wires up (SSE, webhook, ...). Deliver MUST be idempotent per
// Message.EventID: OutboxDispatchStore guarantees at-least-once delivery,
// not exactly-once — a crash between a successful Deliver and the message
// being confirmed dispatched causes the SAME message to be redelivered,
// and a repeat delivery must never produce a second logical effect (V1-07's
// own "Hoàn thành khi").
type OutboxSink interface {
	Deliver(ctx context.Context, msg OutboxMessage) error
}
