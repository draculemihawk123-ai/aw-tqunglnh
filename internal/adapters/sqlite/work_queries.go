package sqlite

import (
	"context"
	"fmt"
)

// The read-only queries in this file exist for the identical reason
// spk04_queries.go's own top-of-file comment gives: internal/app/work's own
// rollback test (V3-04, docs/design/05-v3-project-workspace.md) lives
// outside this package and so cannot reach Store's unexported db field the
// way an in-package test (e.g. unitofwork_test.go) can — these narrow,
// whole-table counts are exactly what that test needs to prove a rolled-back
// CreateRootWorkItem attempt left zero rows behind across every table it
// touches (this task's own "Hoàn thành khi: không có root task orphan
// family/workspace hoặc job intent thiếu"), the strongest possible "no
// orphan" proof since the failed attempt's own minted IDs (idsource.Random)
// are never recoverable to look up individually. None of them add any write
// path or business rule.

// CountWorkItems returns the total number of rows in work_items.
func (s *Store) CountWorkItems(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_items`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count work items: %w", err)
	}
	return count, nil
}

// CountTaskFamilies returns the total number of rows in task_families.
func (s *Store) CountTaskFamilies(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_families`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count task families: %w", err)
	}
	return count, nil
}

// CountWorkspaceSets returns the total number of rows in workspace_sets.
func (s *Store) CountWorkspaceSets(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_sets`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count workspace sets: %w", err)
	}
	return count, nil
}

// CountFamilyRepositoryScopes returns the total number of rows in
// family_repository_scopes.
func (s *Store) CountFamilyRepositoryScopes(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM family_repository_scopes`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count family repository scopes: %w", err)
	}
	return count, nil
}

// CountDomainEventsByType answers how many domain_events rows exist for one
// event_type, regardless of aggregate — a coarser sibling of
// CountDomainEvents (spk04_queries.go), for a caller that does not know
// (and, after a rolled-back attempt that never returned a result, cannot
// ever know) which AggregateID a specific event would have carried.
func (s *Store) CountDomainEventsByType(ctx context.Context, eventType string) (int, error) {
	if eventType == "" {
		return 0, fmt.Errorf("event type is required")
	}
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM domain_events WHERE event_type = ?`, eventType,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count domain events by type: %w", err)
	}
	return count, nil
}

// CountCommandReceiptsByIdempotencyKey answers how many command_receipts
// rows exist under one idempotency key — used to prove a failed command
// attempt never recorded a receipt (and so remains retryable), the
// receipt-side counterpart of CountDurableJobsByIdempotencyKey.
func (s *Store) CountCommandReceiptsByIdempotencyKey(ctx context.Context, idempotencyKey string) (int, error) {
	if idempotencyKey == "" {
		return 0, fmt.Errorf("idempotency key is required")
	}
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM command_receipts WHERE idempotency_key = ?`, idempotencyKey,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count command receipts by idempotency key: %w", err)
	}
	return count, nil
}
