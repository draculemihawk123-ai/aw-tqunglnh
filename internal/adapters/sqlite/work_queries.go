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

// CountWorkItemEffectiveScopes returns the total number of rows in
// work_item_effective_scopes (V3-05) — used the same way
// CountFamilyRepositoryScopes is used for family_repository_scopes, and to
// prove a rolled-back CreateChildWorkItem attempt left zero effective-scope
// rows behind.
func (s *Store) CountWorkItemEffectiveScopes(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_item_effective_scopes`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count work item effective scopes: %w", err)
	}
	return count, nil
}

// CountDurableJobsByKind answers how many durable_jobs rows exist for one
// job kind, regardless of idempotency key — used by V3-05's own "Hoàn thành
// khi: tạo child không enqueue provision workspace mới" test to prove
// CreateChildWorkItem enqueues zero new WORKSPACE_PROVISION jobs (unlike
// CountDurableJobsByIdempotencyKey, which needs one exact key per job, this
// counts across every job of a kind so a test can assert a before/after
// total stayed unchanged without knowing every idempotency key in advance).
func (s *Store) CountDurableJobsByKind(ctx context.Context, kind string) (int, error) {
	if kind == "" {
		return 0, fmt.Errorf("job kind is required")
	}
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM durable_jobs WHERE kind = ?`, kind,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count durable jobs by kind: %w", err)
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

// CountScopeExpansionRequests returns the total number of rows in
// scope_expansion_requests (V3-08) — used the same way CountWorkItems etc.
// above are used, to prove a rolled-back RequestScopeExpansion attempt left
// zero rows behind.
func (s *Store) CountScopeExpansionRequests(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scope_expansion_requests`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count scope expansion requests: %w", err)
	}
	return count, nil
}

// CountScopeExpansionRequestsByStatus answers how many scope_expansion_requests
// rows exist for one status, regardless of family — used by the duplicate-
// approval/withdraw-idempotency tests to assert exactly one request ever
// reached a given terminal status.
func (s *Store) CountScopeExpansionRequestsByStatus(ctx context.Context, status string) (int, error) {
	if status == "" {
		return 0, fmt.Errorf("status is required")
	}
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM scope_expansion_requests WHERE status = ?`, status,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("count scope expansion requests by status: %w", err)
	}
	return count, nil
}
