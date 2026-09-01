package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

const outboxMessageColumns = `
id, event_id, topic, payload_json, status, available_at,
lease_owner, lease_token, lease_until, created_at`

// ClaimNextOutboxMessage implements ports.OutboxDispatchStore, mirroring
// ClaimJob's single atomic UPDATE...RETURNING CAS pattern
// (internal/adapters/sqlite/scheduling.go) rather than a separate
// SELECT-then-UPDATE, so two dispatchers racing to claim can never both
// win the same row.
func (s *Store) ClaimNextOutboxMessage(ctx context.Context, owner string, ttl time.Duration) (ports.OutboxMessage, ports.OutboxLease, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return ports.OutboxMessage{}, ports.OutboxLease{}, errors.New("outbox lease owner is required")
	}
	modifier, err := leaseModifier(ttl)
	if err != nil {
		return ports.OutboxMessage{}, ports.OutboxLease{}, err
	}

	row := s.db.QueryRowContext(ctx, `
WITH candidate AS (
    SELECT id
    FROM outbox
    WHERE status = 'AVAILABLE'
      AND julianday(available_at) <= julianday('now')
    ORDER BY created_at, id
    LIMIT 1
)
UPDATE outbox
SET status = 'LEASED',
    lease_owner = ?,
    lease_token = lease_token + 1,
    lease_until = strftime('%Y-%m-%dT%H:%M:%fZ', 'now', ?)
WHERE id = (SELECT id FROM candidate)
  AND status = 'AVAILABLE'
RETURNING `+outboxMessageColumns, owner, modifier)
	msg, err := scanOutboxMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.OutboxMessage{}, ports.OutboxLease{}, ports.ErrNoOutboxMessageAvailable
	}
	if err != nil {
		return ports.OutboxMessage{}, ports.OutboxLease{}, fmt.Errorf("claim outbox message: %w", err)
	}
	lease := ports.OutboxLease{MessageID: msg.ID, Owner: msg.LeaseOwner, Token: msg.LeaseToken}
	return msg, lease, nil
}

// MarkOutboxMessageDispatched implements ports.OutboxDispatchStore. It
// CAS's on the exact owner+token the caller was handed at claim time, so
// a lease already reclaimed by RecoverExpiredOutboxLeases (and possibly
// re-claimed by a different owner) can never be falsely confirmed by the
// dispatcher that lost it.
func (s *Store) MarkOutboxMessageDispatched(ctx context.Context, lease ports.OutboxLease) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE outbox
SET status = 'DISPATCHED'
WHERE id = ?
  AND status = 'LEASED'
  AND lease_owner = ?
  AND lease_token = ?
  AND julianday(lease_until) > julianday('now')`, lease.MessageID, lease.Owner, lease.Token)
	if err != nil {
		return fmt.Errorf("mark outbox message dispatched: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read mark outbox message dispatched result: %w", err)
	}
	if affected != 1 {
		return ports.ErrOutboxLeaseLost
	}
	return nil
}

// RecoverExpiredOutboxLeases implements ports.OutboxDispatchStore: it
// reclaims every LEASED message whose lease expired without a confirmed
// dispatch, making it claimable again — the mechanism a crashed
// dispatcher's in-flight message recovers through.
func (s *Store) RecoverExpiredOutboxLeases(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE outbox
SET status = 'AVAILABLE',
    lease_owner = NULL,
    lease_until = NULL
WHERE status = 'LEASED'
  AND julianday(lease_until) <= julianday('now')`)
	if err != nil {
		return 0, fmt.Errorf("recover expired outbox leases: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read recover expired outbox leases result: %w", err)
	}
	return affected, nil
}

func scanOutboxMessage(scanner rowScanner) (ports.OutboxMessage, error) {
	var msg ports.OutboxMessage
	var status string
	var availableAtText string
	var leaseOwner sql.NullString
	var leaseUntilText sql.NullString
	var createdAtText string
	if err := scanner.Scan(
		&msg.ID, &msg.EventID, &msg.Topic, &msg.PayloadJSON, &status, &availableAtText,
		&leaseOwner, &msg.LeaseToken, &leaseUntilText, &createdAtText,
	); err != nil {
		return ports.OutboxMessage{}, err
	}
	msg.Status = ports.OutboxStatus(status)
	msg.LeaseOwner = leaseOwner.String

	var err error
	if msg.AvailableAt, err = parseDBTime(availableAtText); err != nil {
		return ports.OutboxMessage{}, err
	}
	if msg.CreatedAt, err = parseDBTime(createdAtText); err != nil {
		return ports.OutboxMessage{}, err
	}
	if leaseUntilText.Valid {
		leaseUntil, err := parseDBTime(leaseUntilText.String)
		if err != nil {
			return ports.OutboxMessage{}, err
		}
		msg.LeaseUntil = &leaseUntil
	}
	return msg, nil
}

var _ ports.OutboxDispatchStore = (*Store)(nil)
