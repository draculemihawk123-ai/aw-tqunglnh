package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
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

// SeedFixtureRepositoryWorkspace inserts the minimum repository, workspace
// set and repository_workspace rows a write-lease/generation acceptance
// scenario needs: one RepositoryWorkspace at generation 1, state READY. See
// SeedFixtureOwners for why these exist only for cross-package acceptance
// scenarios; production code must never call this.
func SeedFixtureRepositoryWorkspace(
	ctx context.Context, store *Store,
	projectID, familyID, workspaceSetID, repositoryID, repositoryWorkspaceID string,
) error {
	return seedFixtureRepositoryWorkspaceTx(ctx, store, projectID, familyID, workspaceSetID, repositoryID, repositoryWorkspaceID, "opaque:"+repositoryID)
}

// SeedFixtureRepositoryWorkspaceWithLocator mirrors SeedFixtureRepositoryWorkspace
// exactly, except the caller supplies the RepositoryWorkspace's own Locator
// rather than a fixed, non-resolvable "opaque:<id>" placeholder — V6-10E's
// own acceptance tests need this so sqlite's row and a real
// gitworktree.Provider.Provision result name the SAME real, on-disk
// workspace (a real Git commit only means anything against a real
// filesystem locator, never the placeholder every other fixture caller
// uses). See SeedFixtureOwners for why these exist only for cross-package
// acceptance scenarios; production code must never call this.
func SeedFixtureRepositoryWorkspaceWithLocator(
	ctx context.Context, store *Store,
	projectID, familyID, workspaceSetID, repositoryID, repositoryWorkspaceID, locator string,
) error {
	return seedFixtureRepositoryWorkspaceTx(ctx, store, projectID, familyID, workspaceSetID, repositoryID, repositoryWorkspaceID, locator)
}

func seedFixtureRepositoryWorkspaceTx(
	ctx context.Context, store *Store,
	projectID, familyID, workspaceSetID, repositoryID, repositoryWorkspaceID, locator string,
) error {
	const timestamp = "2026-08-28T16:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO repositories(id, project_id, name, local_path, default_ref, status, version, created_at, updated_at)
VALUES (?, ?, ?, 'C:/fixture', 'main', 'ACTIVE', 1, ?, ?);`,
		repositoryID, projectID, repositoryID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture repository: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO workspace_sets(id, project_id, family_id, state, version, created_at, updated_at)
VALUES (?, ?, ?, 'READY', 1, ?, ?);`,
		workspaceSetID, projectID, familyID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture workspace set: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO repository_workspaces(
  id, project_id, workspace_set_id, family_id, repository_id, generation,
  locator, branch_ref, base_revision, current_revision, state, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 1, ?, ?, 'base-rev', 'base-rev', 'READY', 1, ?, ?);`,
		repositoryWorkspaceID, projectID, workspaceSetID, familyID, repositoryID,
		locator, "agentkit/"+repositoryID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture repository workspace: %w", err)
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

// SeedFixtureSecondAttempt inserts a second ExecutionAttempt (attempt_no=2)
// against a NodeRun that SeedFixtureNodeRunAndAttempt already seeded, for
// acceptance scenarios that need two concurrent attempt holders racing
// against each other (e.g. SPK-08's write-lease race).
func SeedFixtureSecondAttempt(ctx context.Context, store *Store, nodeRunID runtime.NodeRunID, attemptID runtime.ExecutionAttemptID) error {
	const timestamp = "2026-08-28T16:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO execution_attempts(
  id, node_run_id, attempt_no, state, provider_key, execution_profile_hash,
  input_revision_set_json, version, created_at, updated_at
) VALUES (?, ?, 2, 'RUNNING', 'claude', 'sha256:profile-fixture', '[]', 1, ?, ?);`,
		attemptID, nodeRunID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture second execution attempt: %w", err)
	}
	return nil
}

// SeedFixtureExecutionAttempt inserts the same minimal workflow_definitions/
// workflow_versions/workflow_runs/node_runs/execution_attempts chain
// SeedFixtureWriteLease builds inline (see that function's own doc comment
// for why this whole chain is seeded by plain INSERTs rather than the real
// publish/StartWorkflowRun pipeline), but stops short of ever calling
// EnqueueJob/ClaimJob/AcquireWriteLeases itself — it exists for a caller
// (a V3-era acceptance test living outside this package, per
// SeedFixtureOwners's own doc comment) that needs a real, valid
// runtime.ExecutionAttemptID to satisfy write_leases.holder_attempt_id's
// own foreign key while driving EnqueueJob/ClaimJob/AcquireWriteLeases
// itself — e.g. to acquire more than one lease under different AttemptIDs
// and inspect/release the resulting grants directly, which
// SeedFixtureWriteLease's own single-shot, grant-discarding shape does not
// support. projectID/familyID/workItemID must already exist
// (SeedFixtureOwners). production code must never call this.
func SeedFixtureExecutionAttempt(ctx context.Context, store *Store, projectID, familyID, workItemID, attemptID string) error {
	const timestamp = "2026-08-28T16:00:00Z"
	suffix := attemptID
	definitionID := "wf-def-" + suffix
	versionID := "wf-ver-" + suffix
	runID := "wf-run-" + suffix
	nodeRunID := "node-run-" + suffix

	if _, err := store.db.ExecContext(ctx, `
INSERT INTO workflow_definitions(id, project_id, name, status, version, created_at, updated_at)
VALUES (?, ?, 'Fixture workflow', 'ACTIVE', 1, ?, ?);`,
		definitionID, projectID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture workflow definition: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO workflow_versions(
  id, definition_id, version_no, schema_version, canonical_content, content_hash,
  dependency_manifest, published_by, published_at
) VALUES (?, ?, 1, 1, '{}', ?, '{}', 'test-fixture', ?);`,
		versionID, definitionID, "sha256:fixture-"+suffix, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture workflow version: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO workflow_runs(
  id, project_id, work_item_id, workflow_version_id, family_id,
  scope_version, state, shared_state_json, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 1, 'RUNNING', '{}', 1, ?, ?);`,
		runID, projectID, workItemID, versionID, familyID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture workflow run: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO node_runs(
  id, run_id, node_key, activation_sequence, iteration, state,
  input_state_hash, version, created_at, updated_at
) VALUES (?, ?, 'agent', 1, 0, 'RUNNING', 'sha256:input-fixture', 1, ?, ?);`,
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

// SeedFixtureWriteLease inserts one real, currently-active (non-expired)
// write_leases row for repositoryWorkspaceID (V3-11's own "active lease"
// acceptance scenario, docs/design/05-v3-project-workspace.md), together
// with the minimal workflow_definitions/workflow_versions/workflow_runs/
// node_runs/execution_attempts/durable_jobs chain write_leases's own real
// foreign keys require. Every one of those upstream rows is a V4/V5-era
// runtime concept this V3-era codebase has no real production writer for
// yet (see SeedFixtureNodeRunAndAttempt's own doc comment for the identical
// reasoning) — Store.StartWorkflowRun itself additionally requires an
// already-published WorkflowVersion (LoadWorkflowVersion), so this seeds
// the whole chain with plain INSERTs instead, mirroring
// scheduling_test.go's own seedSchedulingFixture technique (same package,
// so it can use store.db directly) rather than the real publish/start
// pipeline. Calls the real, already-tested Store.EnqueueJob/ClaimJob/
// AcquireWriteLeases for the write_leases row itself, so the row this
// produces is byte-for-byte what that real production path would have
// written. projectID/familyID/workItemID must already exist
// (SeedFixtureOwners); repositoryID/repositoryWorkspaceID/generation must
// already exist and be READY (SeedFixtureRepositoryWorkspace) — Store's
// own INSERT...WHERE rw.state = 'READY' guard requires it. See
// SeedFixtureOwners for why this exists only for cross-package acceptance
// tests; production code must never call this.
func SeedFixtureWriteLease(
	ctx context.Context, store *Store,
	projectID, familyID, workItemID, repositoryID, repositoryWorkspaceID string, generation uint64,
) error {
	const timestamp = "2026-08-28T16:00:00Z"
	suffix := repositoryWorkspaceID
	definitionID := "wf-def-" + suffix
	versionID := "wf-ver-" + suffix
	runID := "wf-run-" + suffix
	nodeRunID := "node-run-" + suffix
	attemptID := "attempt-" + suffix
	jobID := "job-lease-" + suffix

	if _, err := store.db.ExecContext(ctx, `
INSERT INTO workflow_definitions(id, project_id, name, status, version, created_at, updated_at)
VALUES (?, ?, 'Fixture workflow', 'ACTIVE', 1, ?, ?);`,
		definitionID, projectID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture workflow definition: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO workflow_versions(
  id, definition_id, version_no, schema_version, canonical_content, content_hash,
  dependency_manifest, published_by, published_at
) VALUES (?, ?, 1, 1, '{}', ?, '{}', 'test-fixture', ?);`,
		versionID, definitionID, "sha256:fixture-"+suffix, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture workflow version: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO workflow_runs(
  id, project_id, work_item_id, workflow_version_id, family_id,
  scope_version, state, shared_state_json, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, 1, 'RUNNING', '{}', 1, ?, ?);`,
		runID, projectID, workItemID, versionID, familyID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed fixture workflow run: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO node_runs(
  id, run_id, node_key, activation_sequence, iteration, state,
  input_state_hash, version, created_at, updated_at
) VALUES (?, ?, 'agent', 1, 0, 'RUNNING', 'sha256:input-fixture', 1, ?, ?);`,
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
		return fmt.Errorf("seed fixture write-lease execution attempt: %w", err)
	}

	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(jobID), ProjectID: project.ProjectID(projectID), Kind: "EXECUTE_NODE",
		AggregateType: "ExecutionAttempt", AggregateID: attemptID, MaxClaims: 3,
		IdempotencyKey: "fixture-write-lease-job:" + suffix,
	}); err != nil {
		return fmt.Errorf("seed fixture write lease job: %w", err)
	}
	_, jobLease, err := store.ClaimJob(ctx, "fixture-worker", time.Hour)
	if err != nil {
		return fmt.Errorf("claim fixture write lease job: %w", err)
	}

	if _, err := store.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
		JobLease:  jobLease,
		AttemptID: runtime.ExecutionAttemptID(attemptID),
		Targets: []ports.WorkspaceLeaseTarget{{
			RepositoryID:          project.RepositoryID(repositoryID),
			RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(repositoryWorkspaceID),
			Generation:            generation,
		}},
		TTL: time.Hour,
	}); err != nil {
		return fmt.Errorf("seed fixture write lease: %w", err)
	}
	return nil
}
