package sqlite

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// SeedFixtureOwners and SeedFixtureNodeRunAndAttempt insert the minimum
// project/family/work-item owner, NodeRun and ExecutionAttempt rows that
// checkpoints and context snapshots have real foreign keys against.
//
// They exist only so acceptance tests that must live in another package —
// SPK-12's real-provider recovery scenario in internal/adapters/providers
// must re-exec the providers_test binary, so it cannot also live in this
// package — can satisfy those constraints without reaching into this
// package's unexported internals. Production code must never call these.
// Callers create the WorkflowRun itself between the two calls through the
// normal public API (runtime.NewWorkflowRun + Store.StartWorkflowRun), the
// same order every other crash/recovery test in this package already uses.
func SeedFixtureOwners(ctx context.Context, store *Store, projectID, familyID, workItemID string) error {
	const timestamp = "2026-08-28T16:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO projects(id, name, status, version, created_at, updated_at)
VALUES (?, 'Test fixture project', 'ACTIVE', 1, ?, ?);`,
		projectID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture project: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO task_families(id, project_id, root_work_item_id, scope_version, status, version, created_at, updated_at)
VALUES (?, ?, ?, 1, 'ACTIVE', 1, ?, ?);`,
		familyID, projectID, workItemID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture family: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO work_items(id, project_id, kind, parent_id, family_id, title, status, version, created_at, updated_at)
VALUES (?, ?, 'ROOT', NULL, ?, 'Test fixture work item', 'ACTIVE', 1, ?, ?);`,
		workItemID, projectID, familyID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture work item: %w", err)
	}
	return nil
}

func SeedFixtureNodeRunAndAttempt(
	ctx context.Context,
	store *Store,
	runID runtime.WorkflowRunID,
	nodeRunID runtime.NodeRunID,
	attemptID runtime.ExecutionAttemptID,
) error {
	const timestamp = "2026-08-28T16:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO node_runs(
  id, run_id, node_key, activation_sequence, iteration, state,
  input_state_hash, version, created_at, updated_at
) VALUES (?, ?, 'implement', 1, 0, 'RUNNING', 'sha256:input-fixture', 1, ?, ?);`,
		nodeRunID, runID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture node run: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO execution_attempts(
  id, node_run_id, attempt_no, state, provider_key, execution_profile_hash,
  input_revision_set_json, version, created_at, updated_at
) VALUES (?, ?, 1, 'RUNNING', 'codex', 'sha256:profile-fixture', '[]', 1, ?, ?);`,
		attemptID, nodeRunID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture execution attempt: %w", err)
	}
	return nil
}
