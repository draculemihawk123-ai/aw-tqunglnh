package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

type workflowRowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type persistedCanonicalWorkflow struct {
	Document     workflow.WorkflowDocument `json:"document"`
	Dependencies []workflow.DependencyPin  `json:"dependencies,omitempty"`
}

func (s *Store) PublishWorkflowVersion(
	ctx context.Context,
	definition workflow.WorkflowDefinition,
	candidate workflow.WorkflowVersion,
) (workflow.WorkflowVersion, error) {
	if err := validateWorkflowPublication(definition, candidate); err != nil {
		return workflow.WorkflowVersion{}, err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return workflow.WorkflowVersion{}, fmt.Errorf("begin workflow publication: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := ensureWorkflowDefinition(ctx, tx, definition, candidate.PublishedAt()); err != nil {
		return workflow.WorkflowVersion{}, err
	}

	// Publishing identical semantic content is idempotent even when the caller
	// generated another candidate ID/version number.
	var existingID workflow.WorkflowVersionID
	err = tx.QueryRowContext(ctx, `
SELECT id FROM workflow_versions WHERE definition_id = ? AND content_hash = ?`,
		definition.ID, candidate.ContentHash()).Scan(&existingID)
	if err == nil {
		existing, loadErr := loadWorkflowVersion(ctx, tx, existingID)
		if loadErr != nil {
			return workflow.WorkflowVersion{}, loadErr
		}
		if err := tx.Commit(); err != nil {
			return workflow.WorkflowVersion{}, fmt.Errorf("commit idempotent workflow publication: %w", err)
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return workflow.WorkflowVersion{}, fmt.Errorf("find workflow content hash: %w", err)
	}

	var hashForID string
	err = tx.QueryRowContext(ctx, `SELECT content_hash FROM workflow_versions WHERE id = ?`, candidate.ID()).Scan(&hashForID)
	if err == nil {
		return workflow.WorkflowVersion{}, fmt.Errorf(
			"%w: workflow version %s already stores hash %s",
			ports.ErrImmutableVersionConflict,
			candidate.ID(),
			hashForID,
		)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return workflow.WorkflowVersion{}, fmt.Errorf("find workflow version id: %w", err)
	}

	var nextVersion uint64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(version_no), 0) + 1 FROM workflow_versions WHERE definition_id = ?`,
		definition.ID).Scan(&nextVersion); err != nil {
		return workflow.WorkflowVersion{}, fmt.Errorf("read next workflow version number: %w", err)
	}
	if candidate.VersionNumber() != nextVersion {
		return workflow.WorkflowVersion{}, fmt.Errorf(
			"%w: workflow definition %s expects version %d, got %d",
			ports.ErrOptimisticConflict,
			definition.ID,
			nextVersion,
			candidate.VersionNumber(),
		)
	}

	manifestJSON, err := json.Marshal(candidate.Dependencies())
	if err != nil {
		return workflow.WorkflowVersion{}, fmt.Errorf("marshal workflow dependency manifest: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO workflow_versions (
    id, definition_id, version_no, schema_version, canonical_content,
    content_hash, dependency_manifest, published_by, published_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		candidate.ID(),
		candidate.DefinitionID(),
		candidate.VersionNumber(),
		candidate.SchemaVersion(),
		string(candidate.CanonicalContent()),
		candidate.ContentHash(),
		string(manifestJSON),
		candidate.PublishedBy(),
		formatWorkflowTime(candidate.PublishedAt()),
	)
	if err != nil {
		// A concurrent publisher can win the content-hash race. In that case
		// return its immutable snapshot, preserving idempotent publish semantics.
		var winnerID workflow.WorkflowVersionID
		winnerErr := tx.QueryRowContext(ctx, `
SELECT id FROM workflow_versions WHERE definition_id = ? AND content_hash = ?`,
			definition.ID, candidate.ContentHash()).Scan(&winnerID)
		if winnerErr == nil {
			winner, loadErr := loadWorkflowVersion(ctx, tx, winnerID)
			if loadErr != nil {
				return workflow.WorkflowVersion{}, loadErr
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return workflow.WorkflowVersion{}, fmt.Errorf("commit concurrent workflow publication: %w", commitErr)
			}
			return winner, nil
		}
		return workflow.WorkflowVersion{}, fmt.Errorf("%w: %v", ports.ErrImmutableVersionConflict, err)
	}

	if err := tx.Commit(); err != nil {
		return workflow.WorkflowVersion{}, fmt.Errorf("commit workflow publication: %w", err)
	}
	return candidate, nil
}

func (s *Store) LoadWorkflowVersion(
	ctx context.Context,
	id workflow.WorkflowVersionID,
) (workflow.WorkflowVersion, error) {
	return loadWorkflowVersion(ctx, s.db, id)
}

func loadWorkflowVersion(
	ctx context.Context,
	queryer workflowRowQueryer,
	id workflow.WorkflowVersionID,
) (workflow.WorkflowVersion, error) {
	if id == "" {
		return workflow.WorkflowVersion{}, errors.New("workflow version id is required")
	}

	var (
		definitionID          workflow.WorkflowDefinitionID
		definitionProjectID   sql.NullString
		definitionName        string
		definitionStatus      workflow.DefinitionStatus
		definitionVersion     uint64
		versionNumber         uint64
		schemaVersion         string
		canonicalContent      string
		contentHash           string
		dependencyManifestRaw string
		publishedBy           string
		publishedAtRaw        string
	)
	err := queryer.QueryRowContext(ctx, `
SELECT
    v.definition_id, d.project_id, d.name, d.status, d.version,
    v.version_no, v.schema_version, v.canonical_content, v.content_hash,
    v.dependency_manifest, v.published_by, v.published_at
FROM workflow_versions AS v
JOIN workflow_definitions AS d ON d.id = v.definition_id
WHERE v.id = ?`, id).Scan(
		&definitionID,
		&definitionProjectID,
		&definitionName,
		&definitionStatus,
		&definitionVersion,
		&versionNumber,
		&schemaVersion,
		&canonicalContent,
		&contentHash,
		&dependencyManifestRaw,
		&publishedBy,
		&publishedAtRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return workflow.WorkflowVersion{}, fmt.Errorf("%w: workflow version %s", ports.ErrPersistenceNotFound, id)
	}
	if err != nil {
		return workflow.WorkflowVersion{}, fmt.Errorf("load workflow version row: %w", err)
	}

	var snapshot persistedCanonicalWorkflow
	if err := json.Unmarshal([]byte(canonicalContent), &snapshot); err != nil {
		return workflow.WorkflowVersion{}, fmt.Errorf("decode canonical workflow %s: %w", id, err)
	}
	var persistedManifest workflow.DependencyManifest
	if err := json.Unmarshal([]byte(dependencyManifestRaw), &persistedManifest); err != nil {
		return workflow.WorkflowVersion{}, fmt.Errorf("decode workflow dependency manifest %s: %w", id, err)
	}
	canonicalManifest := workflow.DependencyManifest{Pins: snapshot.Dependencies}
	if !sameDependencyManifest(canonicalManifest, persistedManifest) {
		return workflow.WorkflowVersion{}, fmt.Errorf(
			"%w: canonical and indexed dependency manifests differ for %s",
			ports.ErrImmutableVersionConflict,
			id,
		)
	}

	publishedAt, err := parseWorkflowTime(publishedAtRaw)
	if err != nil {
		return workflow.WorkflowVersion{}, err
	}
	definition := workflow.WorkflowDefinition{
		ID:      definitionID,
		Name:    definitionName,
		Status:  definitionStatus,
		Version: definitionVersion,
	}
	if definitionProjectID.Valid {
		projectID := project.ProjectID(definitionProjectID.String)
		definition.ProjectID = &projectID
	}
	rebuilt, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID:     id,
		VersionNumber: versionNumber,
		Document:      snapshot.Document,
		Dependencies:  canonicalManifest,
		PublishedBy:   publishedBy,
		PublishedAt:   publishedAt,
	})
	if err != nil {
		return workflow.WorkflowVersion{}, fmt.Errorf("rebuild persisted workflow version %s: %w", id, err)
	}
	if rebuilt.SchemaVersion() != schemaVersion || rebuilt.ContentHash() != contentHash ||
		!bytes.Equal(rebuilt.CanonicalContent(), []byte(canonicalContent)) {
		return workflow.WorkflowVersion{}, fmt.Errorf(
			"%w: persisted workflow version %s failed snapshot/hash verification",
			ports.ErrImmutableVersionConflict,
			id,
		)
	}
	return rebuilt, nil
}

func (s *Store) StartWorkflowRun(ctx context.Context, run runtime.WorkflowRun) error {
	if run.ID == "" || run.ProjectID == "" || run.WorkItemID == "" || run.FamilyID == "" ||
		run.WorkflowVersionID == "" || run.ScopeVersion == 0 {
		return errors.New("workflow run identities, pinned version and scope are required")
	}
	if run.State != runtime.WorkflowRunCreated || run.Version != 1 || run.StartedAt != nil || run.FinishedAt != nil {
		return errors.New("a new workflow run must be CREATED at version 1 without timestamps")
	}
	sharedState := run.SharedState
	if len(sharedState) == 0 {
		sharedState = json.RawMessage(`{}`)
	}
	if !json.Valid(sharedState) {
		return errors.New("workflow run shared state must be valid JSON")
	}

	pinnedVersion, err := s.LoadWorkflowVersion(ctx, run.WorkflowVersionID)
	if err != nil {
		return err
	}
	if pinnedVersion.ContentHash() != run.WorkflowVersionHash ||
		!sameDependencyManifest(pinnedVersion.Dependencies(), run.PinnedDependencies) {
		return fmt.Errorf("%w: workflow run %s", ports.ErrPinnedVersionMismatch, run.ID)
	}

	_, err = s.db.ExecContext(ctx, `
INSERT INTO workflow_runs (
    id, project_id, work_item_id, workflow_version_id, family_id,
    scope_version, state, shared_state_json, version,
    started_at, finished_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, NULL, NULL,
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`,
		run.ID,
		run.ProjectID,
		run.WorkItemID,
		run.WorkflowVersionID,
		run.FamilyID,
		run.ScopeVersion,
		run.State,
		string(sharedState),
	)
	if err != nil {
		var existing int
		lookupErr := s.db.QueryRowContext(ctx, `SELECT 1 FROM workflow_runs WHERE id = ?`, run.ID).Scan(&existing)
		if lookupErr == nil {
			return fmt.Errorf("%w: workflow run %s", ports.ErrPersistenceAlreadyExists, run.ID)
		}
		return fmt.Errorf("start workflow run %s: %w", run.ID, err)
	}
	return nil
}

func (s *Store) LoadWorkflowRun(
	ctx context.Context,
	id runtime.WorkflowRunID,
) (runtime.WorkflowRun, error) {
	return loadWorkflowRun(ctx, s.db, id)
}

func loadWorkflowRun(
	ctx context.Context,
	queryer workflowRowQueryer,
	id runtime.WorkflowRunID,
) (runtime.WorkflowRun, error) {
	if id == "" {
		return runtime.WorkflowRun{}, errors.New("workflow run id is required")
	}
	var (
		run             runtime.WorkflowRun
		sharedStateRaw  string
		startedAtRaw    sql.NullString
		finishedAtRaw   sql.NullString
		workflowVersion workflow.WorkflowVersionID
	)
	err := queryer.QueryRowContext(ctx, `
SELECT id, project_id, work_item_id, workflow_version_id, family_id,
       scope_version, state, shared_state_json, version, started_at, finished_at
FROM workflow_runs
WHERE id = ?`, id).Scan(
		&run.ID,
		&run.ProjectID,
		&run.WorkItemID,
		&workflowVersion,
		&run.FamilyID,
		&run.ScopeVersion,
		&run.State,
		&sharedStateRaw,
		&run.Version,
		&startedAtRaw,
		&finishedAtRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.WorkflowRun{}, fmt.Errorf("%w: workflow run %s", ports.ErrPersistenceNotFound, id)
	}
	if err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("load workflow run row: %w", err)
	}
	if !json.Valid([]byte(sharedStateRaw)) {
		return runtime.WorkflowRun{}, fmt.Errorf("workflow run %s contains invalid shared state", id)
	}
	run.SharedState = json.RawMessage(append([]byte(nil), sharedStateRaw...))
	run.WorkflowVersionID = workflowVersion

	pinnedVersion, err := loadWorkflowVersion(ctx, queryer, workflowVersion)
	if err != nil {
		return runtime.WorkflowRun{}, err
	}
	run.WorkflowVersionHash = pinnedVersion.ContentHash()
	run.PinnedDependencies = pinnedVersion.Dependencies()
	if startedAtRaw.Valid {
		startedAt, err := parseWorkflowTime(startedAtRaw.String)
		if err != nil {
			return runtime.WorkflowRun{}, err
		}
		run.StartedAt = &startedAt
	}
	if finishedAtRaw.Valid {
		finishedAt, err := parseWorkflowTime(finishedAtRaw.String)
		if err != nil {
			return runtime.WorkflowRun{}, err
		}
		run.FinishedAt = &finishedAt
	}
	return run, nil
}

func (s *Store) CompareAndSwapWorkflowRun(
	ctx context.Context,
	transition ports.WorkflowRunTransition,
) (runtime.WorkflowRun, error) {
	if err := validateWorkflowRunTransition(transition); err != nil {
		return runtime.WorkflowRun{}, err
	}
	if transition.ExpectedState == runtime.WorkflowRunRunning && isTerminalWorkflowRunState(transition.NextState) {
		return runtime.WorkflowRun{}, ports.ErrWorkerFinalizationNeeded
	}
	timestamp := formatWorkflowTime(transition.OccurredAt)
	startedAt := ""
	if transition.NextState == runtime.WorkflowRunRunning {
		startedAt = timestamp
	}
	finishedAt := ""
	if isTerminalWorkflowRunState(transition.NextState) {
		finishedAt = timestamp
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("begin workflow run transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
UPDATE workflow_runs
SET state = ?,
    shared_state_json = ?,
    version = version + 1,
    started_at = CASE WHEN ? <> '' AND started_at IS NULL THEN ? ELSE started_at END,
    finished_at = CASE WHEN ? <> '' THEN ? ELSE finished_at END,
    updated_at = ?
WHERE id = ? AND state = ? AND version = ?`,
		transition.NextState,
		string(transition.SharedState),
		startedAt,
		startedAt,
		finishedAt,
		finishedAt,
		timestamp,
		transition.RunID,
		transition.ExpectedState,
		transition.ExpectedVersion,
	)
	if err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("transition workflow run %s: %w", transition.RunID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("read workflow run transition result: %w", err)
	}
	if affected != 1 {
		var exists int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM workflow_runs WHERE id = ?`, transition.RunID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return runtime.WorkflowRun{}, fmt.Errorf("%w: workflow run %s", ports.ErrPersistenceNotFound, transition.RunID)
		}
		if lookupErr != nil {
			return runtime.WorkflowRun{}, fmt.Errorf("check stale workflow run transition: %w", lookupErr)
		}
		return runtime.WorkflowRun{}, fmt.Errorf(
			"%w: workflow run %s expected %s@%d",
			ports.ErrOptimisticConflict,
			transition.RunID,
			transition.ExpectedState,
			transition.ExpectedVersion,
		)
	}

	updated, err := loadWorkflowRun(ctx, tx, transition.RunID)
	if err != nil {
		return runtime.WorkflowRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("commit workflow run transition: %w", err)
	}
	return updated, nil
}

// FinalizeWorkflowRun is the fenced worker path for a terminal workflow
// transition. It deliberately commits the workflow state, audit event and
// durable-job acknowledgement in one SQLite transaction. A worker that has
// lost its job lease or any workspace fence cannot make a terminal result
// authoritative.
func (s *Store) FinalizeWorkflowRun(
	ctx context.Context,
	finalization ports.WorkerWorkflowRunFinalization,
) (runtime.WorkflowRun, error) {
	if err := validateWorkerWorkflowRunFinalization(finalization); err != nil {
		return runtime.WorkflowRun{}, err
	}
	// A fenced finalization is idempotent at the transaction boundary: a busy
	// SQLite writer rolls the entire transaction back, so retrying can either
	// commit this exact fenced transition or return the semantic conflict/lease
	// loss produced by the winner. Do not leak SQLITE_BUSY to the worker as a
	// false domain outcome.
	for attempt := 0; attempt < 8; attempt++ {
		run, err := s.finalizeWorkflowRunOnce(ctx, finalization)
		if err == nil || !isSQLiteBusy(err) || attempt == 7 {
			return run, err
		}
		delay := time.Duration(1<<attempt) * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return runtime.WorkflowRun{}, ctx.Err()
		case <-timer.C:
		}
	}
	return runtime.WorkflowRun{}, errors.New("unreachable finalization retry state")
}

func (s *Store) finalizeWorkflowRunOnce(
	ctx context.Context,
	finalization ports.WorkerWorkflowRunFinalization,
) (runtime.WorkflowRun, error) {

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("begin fenced workflow finalization: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := validateActiveFinalizationJob(ctx, tx, finalization); err != nil {
		return runtime.WorkflowRun{}, err
	}
	for _, grant := range finalization.WriteLeases {
		if err := validateActiveWriteLeaseInTx(ctx, tx, finalization.JobLease, grant); err != nil {
			return runtime.WorkflowRun{}, err
		}
	}

	transition := finalization.Transition
	timestamp := formatWorkflowTime(transition.OccurredAt)
	result, err := tx.ExecContext(ctx, `
UPDATE workflow_runs
SET state = ?,
    shared_state_json = ?,
    version = version + 1,
    finished_at = ?,
    updated_at = ?
WHERE id = ? AND state = ? AND version = ?`,
		transition.NextState,
		string(transition.SharedState),
		timestamp,
		timestamp,
		transition.RunID,
		transition.ExpectedState,
		transition.ExpectedVersion,
	)
	if err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("finalize workflow run %s: %w", transition.RunID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("read fenced workflow finalization result: %w", err)
	}
	if affected != 1 {
		return runtime.WorkflowRun{}, fmt.Errorf(
			"%w: workflow run %s expected %s@%d",
			ports.ErrOptimisticConflict,
			transition.RunID,
			transition.ExpectedState,
			transition.ExpectedVersion,
		)
	}

	payload, err := json.Marshal(map[string]string{
		"jobId":         string(finalization.JobLease.JobID),
		"jobLeaseOwner": finalization.JobLease.Owner,
		"runId":         string(transition.RunID),
		"terminalState": string(transition.NextState),
	})
	if err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("encode workflow finalization event: %w", err)
	}
	var sequence uint64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(sequence), 0) + 1
FROM domain_events
WHERE aggregate_type = 'WorkflowRun' AND aggregate_id = ?`, transition.RunID).Scan(&sequence); err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("allocate workflow finalization event sequence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO domain_events(
    id, project_id, aggregate_type, aggregate_id, sequence,
    event_type, schema_version, payload_json, correlation_id, created_at
)
SELECT ?, project_id, 'WorkflowRun', id, ?,
       'WORKFLOW_RUN_FINALIZED', 1, ?, ?,
       strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM workflow_runs
WHERE id = ?`,
		finalization.EventID,
		sequence,
		string(payload),
		finalization.CorrelationID,
		transition.RunID,
	); err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("append workflow finalization event: %w", err)
	}

	result, err = tx.ExecContext(ctx, `
UPDATE durable_jobs
SET state = 'SUCCEEDED',
    lease_until = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    heartbeat_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    version = version + 1,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ?
  AND state = 'LEASED'
  AND lease_owner = ?
  AND lease_token = ?
  AND julianday(lease_until) > julianday('now')`,
		finalization.JobLease.JobID,
		finalization.JobLease.Owner,
		finalization.JobLease.Token,
	)
	if err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("complete fenced finalization job: %w", err)
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("read fenced job completion result: %w", err)
	}
	if affected != 1 {
		return runtime.WorkflowRun{}, ports.ErrJobLeaseLost
	}

	updated, err := loadWorkflowRun(ctx, tx, transition.RunID)
	if err != nil {
		return runtime.WorkflowRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("commit fenced workflow finalization: %w", err)
	}
	return updated, nil
}

func validateWorkerWorkflowRunFinalization(finalization ports.WorkerWorkflowRunFinalization) error {
	transition := finalization.Transition
	if err := validateWorkflowRunTransition(transition); err != nil {
		return err
	}
	if transition.ExpectedState != runtime.WorkflowRunRunning || !isTerminalWorkflowRunState(transition.NextState) {
		return errors.New("worker workflow finalization must transition RUNNING to a terminal state")
	}
	if err := validateJobLease(finalization.JobLease); err != nil {
		return err
	}
	if strings.TrimSpace(finalization.EventID) == "" || strings.TrimSpace(finalization.CorrelationID) == "" {
		return errors.New("worker workflow finalization event and correlation ids are required")
	}
	seen := make(map[string]struct{}, len(finalization.WriteLeases))
	for _, grant := range finalization.WriteLeases {
		if err := validateWriteLeaseGrant(grant); err != nil {
			return err
		}
		if grant.HolderJobID != finalization.JobLease.JobID ||
			grant.HolderJobLeaseToken != finalization.JobLease.Token ||
			grant.Owner != finalization.JobLease.Owner {
			return errors.New("write lease proof does not belong to finalization job lease")
		}
		key := fmt.Sprintf("%s\x00%d", grant.RepositoryWorkspaceID, grant.Generation)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate write lease proof for %s generation %d", grant.RepositoryWorkspaceID, grant.Generation)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateActiveFinalizationJob(
	ctx context.Context,
	tx *sql.Tx,
	finalization ports.WorkerWorkflowRunFinalization,
) error {
	var valid int
	err := tx.QueryRowContext(ctx, `
SELECT 1
FROM durable_jobs AS job
JOIN workflow_runs AS run ON run.id = job.aggregate_id
WHERE job.id = ?
  AND job.project_id = run.project_id
  AND job.aggregate_type = 'WorkflowRun'
  AND job.aggregate_id = ?
  AND job.state = 'LEASED'
  AND job.lease_owner = ?
  AND job.lease_token = ?
  AND julianday(job.lease_until) > julianday('now')`,
		finalization.JobLease.JobID,
		finalization.Transition.RunID,
		finalization.JobLease.Owner,
		finalization.JobLease.Token,
	).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.ErrJobLeaseLost
	}
	if err != nil {
		return fmt.Errorf("validate finalization job lease: %w", err)
	}
	return nil
}

func validateActiveWriteLeaseInTx(
	ctx context.Context,
	tx *sql.Tx,
	jobLease ports.JobLease,
	grant ports.WriteLeaseGrant,
) error {
	var valid int
	err := tx.QueryRowContext(ctx, `
SELECT 1
FROM write_leases AS lease
JOIN durable_jobs AS job ON job.id = lease.holder_job_id
JOIN repository_workspaces AS rw ON rw.id = lease.repository_workspace_id
JOIN execution_attempts AS attempt ON attempt.id = lease.holder_attempt_id
WHERE lease.repository_workspace_id = ? AND lease.generation = ? AND lease.fence_token = ?
  AND lease.holder_job_id = ? AND lease.holder_job_lease_token = ?
  AND lease.holder_attempt_id = ? AND lease.lease_owner = ?
  AND julianday(lease.lease_until) > julianday('now')
  AND job.state = 'LEASED' AND job.lease_owner = lease.lease_owner
  AND job.lease_token = lease.holder_job_lease_token
  AND julianday(job.lease_until) > julianday('now')
  AND rw.generation = lease.generation AND rw.state = 'READY'
  AND attempt.state = 'RUNNING'`,
		grant.RepositoryWorkspaceID,
		grant.Generation,
		grant.FenceToken,
		jobLease.JobID,
		jobLease.Token,
		grant.HolderAttemptID,
		jobLease.Owner,
	).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.ErrWriteLeaseLost
	}
	if err != nil {
		return fmt.Errorf("validate finalization write lease: %w", err)
	}
	return nil
}

func ensureWorkflowDefinition(
	ctx context.Context,
	tx *sql.Tx,
	definition workflow.WorkflowDefinition,
	publishedAt time.Time,
) error {
	var projectID any
	if definition.ProjectID != nil {
		projectID = *definition.ProjectID
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO workflow_definitions (
    id, project_id, name, status, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING`,
		definition.ID,
		projectID,
		definition.Name,
		definition.Status,
		definition.Version,
		formatWorkflowTime(publishedAt),
		formatWorkflowTime(publishedAt),
	)
	if err != nil {
		return fmt.Errorf("persist workflow definition %s: %w", definition.ID, err)
	}

	var (
		storedProject sql.NullString
		storedName    string
		storedStatus  workflow.DefinitionStatus
		storedVersion uint64
	)
	if err := tx.QueryRowContext(ctx, `
SELECT project_id, name, status, version FROM workflow_definitions WHERE id = ?`,
		definition.ID).Scan(&storedProject, &storedName, &storedStatus, &storedVersion); err != nil {
		return fmt.Errorf("verify workflow definition %s: %w", definition.ID, err)
	}
	expectedProject := ""
	if definition.ProjectID != nil {
		expectedProject = string(*definition.ProjectID)
	}
	if storedProject.Valid != (definition.ProjectID != nil) || storedProject.String != expectedProject ||
		storedName != definition.Name || storedStatus != definition.Status || storedVersion != definition.Version {
		return fmt.Errorf(
			"%w: workflow definition %s differs from its persisted identity",
			ports.ErrPersistenceAlreadyExists,
			definition.ID,
		)
	}
	return nil
}

func validateWorkflowPublication(
	definition workflow.WorkflowDefinition,
	candidate workflow.WorkflowVersion,
) error {
	if definition.ID == "" || strings.TrimSpace(definition.Name) == "" || definition.Version == 0 {
		return errors.New("workflow definition id, name and version are required")
	}
	switch definition.Status {
	case workflow.DefinitionDraft, workflow.DefinitionActive:
	case workflow.DefinitionArchived:
		return errors.New("cannot publish an archived workflow definition")
	default:
		return fmt.Errorf("unsupported workflow definition status %q", definition.Status)
	}
	if candidate.ID() == "" || candidate.DefinitionID() != definition.ID ||
		candidate.VersionNumber() == 0 || strings.TrimSpace(candidate.SchemaVersion()) == "" ||
		len(candidate.CanonicalContent()) == 0 || strings.TrimSpace(candidate.ContentHash()) == "" ||
		strings.TrimSpace(candidate.PublishedBy()) == "" || candidate.PublishedAt().IsZero() {
		return errors.New("compiled workflow version is incomplete or belongs to another definition")
	}
	if !json.Valid(candidate.CanonicalContent()) {
		return errors.New("compiled workflow canonical content must be valid JSON")
	}
	return nil
}

func validateWorkflowRunTransition(transition ports.WorkflowRunTransition) error {
	if transition.RunID == "" || transition.ExpectedVersion == 0 || transition.OccurredAt.IsZero() {
		return errors.New("workflow run transition id, expected version and timestamp are required")
	}
	if !json.Valid(transition.SharedState) {
		return errors.New("workflow run transition shared state must be valid JSON")
	}
	allowed := false
	switch transition.ExpectedState {
	case runtime.WorkflowRunCreated:
		allowed = transition.NextState == runtime.WorkflowRunRunning ||
			transition.NextState == runtime.WorkflowRunCancelled
	case runtime.WorkflowRunRunning:
		allowed = transition.NextState == runtime.WorkflowRunWaiting ||
			transition.NextState == runtime.WorkflowRunBlocked ||
			transition.NextState == runtime.WorkflowRunSucceeded ||
			transition.NextState == runtime.WorkflowRunFailed ||
			transition.NextState == runtime.WorkflowRunCancelled
	case runtime.WorkflowRunWaiting:
		allowed = transition.NextState == runtime.WorkflowRunRunning ||
			transition.NextState == runtime.WorkflowRunBlocked ||
			transition.NextState == runtime.WorkflowRunFailed ||
			transition.NextState == runtime.WorkflowRunCancelled
	case runtime.WorkflowRunBlocked:
		allowed = transition.NextState == runtime.WorkflowRunRunning ||
			transition.NextState == runtime.WorkflowRunFailed ||
			transition.NextState == runtime.WorkflowRunCancelled
	}
	if !allowed {
		return fmt.Errorf(
			"workflow run transition %s -> %s is not allowed",
			transition.ExpectedState,
			transition.NextState,
		)
	}
	return nil
}

func isTerminalWorkflowRunState(state runtime.WorkflowRunState) bool {
	return state == runtime.WorkflowRunSucceeded || state == runtime.WorkflowRunFailed ||
		state == runtime.WorkflowRunCancelled
}

func sameDependencyManifest(left, right workflow.DependencyManifest) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func formatWorkflowTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseWorkflowTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse workflow timestamp %q: %w", value, err)
	}
	return parsed, nil
}

var _ ports.WorkflowPersistence = (*Store)(nil)
