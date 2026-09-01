package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Exported fixture identities for each crash-worker fault point. These used
// to be unexported constants private to one _test.go file each; they are
// exported here so both the existing integration tests and any standalone
// caller (internal/spikeacceptance's SPK-04 scenario, which cannot reach
// into a _test.go file) can seed and recognize the exact same fixture rows.

// CrashClaimRunID/CrashClaimJobID are the run/job ids the ClaimAndHang and
// FinalizeAndHang fault points (crashworker.go's
// runClaimAndOptionallyFinalizeWorker) operate on.
const (
	CrashClaimRunID = "workflow-run-crash"
	CrashClaimJobID = "job-crash"
)

const (
	CrashCheckpointRunID              = "workflow-run-crash-ckpt"
	CrashCheckpointNodeRunID          = "node-run-crash-ckpt"
	CrashCheckpointInterruptedAttempt = "attempt-before-crash-ckpt"
	CrashCheckpointID                 = "checkpoint-crash-ckpt-1"
	CrashCheckpointContextSnapshotID  = "context-crash-ckpt-1"
	CrashCheckpointReplacementAttempt = "attempt-after-crash-ckpt"
)

const (
	CrashAttemptTermRunID                 = "workflow-run-crash-term"
	CrashAttemptTermNodeRunID             = "node-run-crash-term"
	CrashAttemptTermAttemptID             = "attempt-crash-term"
	CrashAttemptTermRepositoryID          = "repo-crash-term"
	CrashAttemptTermWorkspaceSetID        = "workspace-set-crash-term"
	CrashAttemptTermRepositoryWorkspaceID = "rw-crash-term"
	CrashAttemptTermBaseRevision          = "rev-crash-term-base"
)

const (
	CrashNodeDispatchRunID              = "workflow-run-crash-dispatch"
	CrashNodeDispatchNodeRunID          = "node-run-crash-dispatch"
	CrashNodeDispatchJobID              = "job-crash-dispatch-current"
	CrashNodeDispatchNextJobID          = "job-crash-dispatch-next"
	CrashNodeDispatchNextIdempotencyKey = "dispatch-next-node-crash"
)

const (
	CrashIntentRunID          = "workflow-run-crash-intent"
	CrashIntentNodeRunID      = "node-run-crash-intent"
	CrashIntentJobID          = "job-crash-intent"
	CrashIntentIdempotencyKey = "dispatch-intent-crash"
)

// SeedCrashResumeOwners inserts the project/task-family/work-item chain every
// crash-worker fixture's workflow run is owned by.
func SeedCrashResumeOwners(ctx context.Context, store *Store) error {
	const timestamp = "2026-08-28T00:00:00Z"
	_, err := store.db.ExecContext(ctx, `
INSERT INTO projects(id, name, status, version, created_at, updated_at)
VALUES ('project-crash', 'Crash/restart spike', 'ACTIVE', 1, ?, ?);

INSERT INTO task_families(
  id, project_id, root_work_item_id, scope_version, status, version, created_at, updated_at
) VALUES ('family-crash', 'project-crash', 'work-item-crash', 1, 'ACTIVE', 1, ?, ?);

INSERT INTO work_items(
  id, project_id, kind, parent_id, family_id, title, status, version, created_at, updated_at
) VALUES (
  'work-item-crash', 'project-crash', 'ROOT', NULL, 'family-crash',
  'Crash/restart spike', 'ACTIVE', 1, ?, ?
);`,
		timestamp, timestamp,
		timestamp, timestamp,
		timestamp, timestamp,
	)
	if err != nil {
		return fmt.Errorf("seed crash/restart owners: %w", err)
	}
	return nil
}

// CrashResumeWorkflowDefinition is the one workflow definition every
// crash-worker fixture publishes versions under.
func CrashResumeWorkflowDefinition() workflow.WorkflowDefinition {
	projectID := project.ProjectID("project-crash")
	return workflow.WorkflowDefinition{
		ID:        "workflow-definition-crash",
		ProjectID: &projectID,
		Name:      "Crash/restart workflow",
		Status:    workflow.DefinitionActive,
		Version:   1,
	}
}

// CompileCrashResumeWorkflowVersion compiles one workflow version for a
// crash-worker fixture, pinned to a single "skill" dependency so distinct
// fixtures never collide by content hash.
func CompileCrashResumeWorkflowVersion(
	definition workflow.WorkflowDefinition,
	id workflow.WorkflowVersionID,
	versionNumber uint64,
	document workflow.WorkflowDocument,
	dependencyVersion string,
) (workflow.WorkflowVersion, error) {
	version, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID:     id,
		VersionNumber: versionNumber,
		Document:      document,
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{
				Kind:    "skill",
				Key:     "implement",
				Version: dependencyVersion,
				Hash:    "sha256:" + dependencyVersion,
			},
		}},
		PublishedBy: "crash-restart-spike",
		PublishedAt: time.Date(2026, 8, 28, int(versionNumber), 0, 0, 0, time.UTC),
	})
	if err != nil {
		return workflow.WorkflowVersion{}, fmt.Errorf("compile crash/restart workflow %s: %w", id, err)
	}
	return version, nil
}

// CrashResumeWorkflowDocumentV1 is a two-node (agent -> end) document.
func CrashResumeWorkflowDocumentV1() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"execute"}},
			{Key: "implement", Type: workflow.NodeAgent, Outcomes: []string{"done"}, ExecutorRef: "agent/default"},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-implement", From: "start", Outcome: "execute", To: "implement"},
			{Key: "implement-end", From: "implement", Outcome: "done", To: "end"},
		},
	}
}

// CrashResumeWorkflowDocumentV2 adds a verify node after implement, used to
// prove a later-published version never moves an already-running pin.
func CrashResumeWorkflowDocumentV2() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"execute"}},
			{Key: "implement", Type: workflow.NodeAgent, Outcomes: []string{"verify"}, ExecutorRef: "agent/default"},
			{Key: "verify", Type: workflow.NodeCommand, Outcomes: []string{"done"}, ExecutorRef: "command/test"},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-implement", From: "start", Outcome: "execute", To: "implement"},
			{Key: "implement-verify", From: "implement", Outcome: "verify", To: "verify"},
			{Key: "verify-end", From: "verify", Outcome: "done", To: "end"},
		},
	}
}

// SeedCrashCheckpointNodeRunAndAttempt seeds the one node_runs and one
// execution_attempts row checkpoints/context_snapshots have real foreign
// keys against, for the CheckpointThenHang fault point.
func SeedCrashCheckpointNodeRunAndAttempt(ctx context.Context, store *Store, runID runtime.WorkflowRunID) error {
	const timestamp = "2026-08-28T12:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO node_runs(
  id, run_id, node_key, activation_sequence, iteration, state,
  input_state_hash, version, created_at, updated_at
) VALUES (?, ?, 'implement', 1, 0, 'RUNNING', 'sha256:input-crash-ckpt', 1, ?, ?);`,
		CrashCheckpointNodeRunID, runID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed crash checkpoint node run: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO execution_attempts(
  id, node_run_id, attempt_no, state, provider_key, execution_profile_hash,
  input_revision_set_json, version, created_at, updated_at
) VALUES (?, ?, 1, 'RUNNING', 'codex', 'sha256:profile-crash-ckpt', '[]', 1, ?, ?);`,
		CrashCheckpointInterruptedAttempt, CrashCheckpointNodeRunID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed crash checkpoint execution attempt: %w", err)
	}
	return nil
}

// SeedCrashAttemptTermNodeRunAndAttempt seeds the node_runs/execution_attempts
// pair the process-exit fault points classify and terminate.
func SeedCrashAttemptTermNodeRunAndAttempt(ctx context.Context, store *Store, runID runtime.WorkflowRunID) error {
	const timestamp = "2026-08-28T13:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO node_runs(
  id, run_id, node_key, activation_sequence, iteration, state,
  input_state_hash, version, created_at, updated_at
) VALUES (?, ?, 'implement', 1, 0, 'RUNNING', 'sha256:input-crash-term', 1, ?, ?);`,
		CrashAttemptTermNodeRunID, runID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed crash attempt-termination node run: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO execution_attempts(
  id, node_run_id, attempt_no, state, provider_key, execution_profile_hash,
  input_revision_set_json, version, created_at, updated_at
) VALUES (?, ?, 1, 'RUNNING', 'codex', 'sha256:profile-crash-term', '[]', 1, ?, ?);`,
		CrashAttemptTermAttemptID, CrashAttemptTermNodeRunID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed crash attempt-termination execution attempt: %w", err)
	}
	return nil
}

// SeedCrashAttemptTermWorkspace seeds the repository/workspace-set/
// repository-workspace chain the mutating process-exit fault point
// reconciles against.
func SeedCrashAttemptTermWorkspace(ctx context.Context, store *Store) error {
	const timestamp = "2026-08-28T13:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO repositories(id, project_id, name, local_path, default_ref, status, version, created_at, updated_at)
VALUES (?, 'project-crash', 'attempt-term-repo', 'C:/fixture/attempt-term', 'main', 'ACTIVE', 1, ?, ?);`,
		CrashAttemptTermRepositoryID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed crash attempt-termination repository: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO workspace_sets(id, project_id, family_id, state, version, created_at, updated_at)
VALUES (?, 'project-crash', 'family-crash', 'READY', 1, ?, ?);`,
		CrashAttemptTermWorkspaceSetID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed crash attempt-termination workspace set: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO repository_workspaces(
  id, project_id, workspace_set_id, family_id, repository_id, generation,
  locator, branch_ref, base_revision, current_revision, state, version, created_at, updated_at
) VALUES (?, 'project-crash', ?, 'family-crash', ?, 1,
          'opaque:attempt-term', 'agentkit/family-crash/attempt-term', ?, ?, 'READY', 1, ?, ?);`,
		CrashAttemptTermRepositoryWorkspaceID, CrashAttemptTermWorkspaceSetID, CrashAttemptTermRepositoryID,
		CrashAttemptTermBaseRevision, CrashAttemptTermBaseRevision, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed crash attempt-termination repository workspace: %w", err)
	}
	return nil
}

// SeedCrashNodeDispatchNodeRun seeds the one node_runs row the
// NodeDispatch fault point completes and dispatches from.
func SeedCrashNodeDispatchNodeRun(ctx context.Context, store *Store, runID runtime.WorkflowRunID) error {
	const timestamp = "2026-08-28T14:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO node_runs(
  id, run_id, node_key, activation_sequence, iteration, state,
  input_state_hash, version, created_at, updated_at
) VALUES (?, ?, 'implement', 1, 0, 'RUNNING', 'sha256:input-crash-dispatch', 1, ?, ?);`,
		CrashNodeDispatchNodeRunID, runID, timestamp, timestamp,
	); err != nil {
		return fmt.Errorf("seed crash node-dispatch node run: %w", err)
	}
	return nil
}

// CrashNodeDispatchNextJobRequest is the downstream job the NodeDispatch
// fault point's transaction dispatches, and its replay must reproduce
// byte-for-byte.
func CrashNodeDispatchNextJobRequest() ports.EnqueueJobRequest {
	return ports.EnqueueJobRequest{
		ID:             CrashNodeDispatchNextJobID,
		ProjectID:      "project-crash",
		Kind:           "EXECUTE_NODE",
		AggregateType:  "WorkflowRun",
		AggregateID:    CrashNodeDispatchRunID,
		Payload:        []byte(`{}`),
		MaxClaims:      3,
		IdempotencyKey: CrashNodeDispatchNextIdempotencyKey,
	}
}
