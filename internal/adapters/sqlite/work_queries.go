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

// CountWorkflowRuns returns the total number of rows in workflow_runs —
// internal/app/runtime's own rollback test (V4-02,
// docs/design/06-v4-runtime-engine.md) needs this and the two counts below
// for the identical "no orphan row in any table this command touches" proof
// CreateRootWorkItem's own rollback test above already established.
func (s *Store) CountWorkflowRuns(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_runs`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count workflow runs: %w", err)
	}
	return count, nil
}

// CountExecutionManifests returns the total number of rows in
// execution_manifests.
func (s *Store) CountExecutionManifests(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM execution_manifests`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count execution manifests: %w", err)
	}
	return count, nil
}

// CountNodeRuns returns the total number of rows in node_runs.
func (s *Store) CountNodeRuns(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_runs`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count node runs: %w", err)
	}
	return count, nil
}

// SetWorkItemWorkflowVersionForTest directly pins work_items.workflow_version_id
// (migration 0007) for a test fixture. No application command in this
// codebase can produce this precondition yet — V3-03's own scope note is
// explicit that a WorkItem's contract fields, WorkflowVersionID included,
// are set directly on the exported domain struct by a future caller, not
// through any command this task's own dependency set builds — so this is
// the same "reach a state no real command produces yet via a direct SQL
// write" discipline runtime_manifest_test.go's own tamper tests already use
// for CHECK-constraint-only states. It exists purely for
// internal/app/runtime's own ErrWorkflowVersionMismatch test; it adds no
// write path or business rule of its own.
func (s *Store) SetWorkItemWorkflowVersionForTest(ctx context.Context, workItemID, workflowVersionID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE work_items SET workflow_version_id = ? WHERE id = ?`, workflowVersionID, workItemID)
	if err != nil {
		return fmt.Errorf("pin work item %s to workflow version %s: %w", workItemID, workflowVersionID, err)
	}
	return nil
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
