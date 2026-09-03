package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
)

// readinessRepository implements ports.ReadinessRepository (V3-07,
// docs/design/05-v3-project-workspace.md) against the
// readiness_profiles/readiness_baseline_attempts/readiness_environment_blockers
// tables (0014_readiness_evidence.sql).
type readinessRepository struct{ tx *sql.Tx }

var _ ports.ReadinessRepository = readinessRepository{}

// --- Profile ---

func (r readinessRepository) SetReadinessProfile(ctx context.Context, profile readiness.Profile) (readiness.Profile, error) {
	return setReadinessProfileTx(ctx, r.tx, profile)
}

func setReadinessProfileTx(ctx context.Context, tx *sql.Tx, profile readiness.Profile) (readiness.Profile, error) {
	repositoryID := string(profile.RepositoryID)
	var projectID string
	err := tx.QueryRowContext(ctx, `SELECT project_id FROM repositories WHERE id = ?`, repositoryID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return readiness.Profile{}, fmt.Errorf("%w: repository %s", ports.ErrPersistenceNotFound, repositoryID)
	}
	if err != nil {
		return readiness.Profile{}, MapSQLiteError(fmt.Errorf("resolve readiness profile repository: %w", err))
	}

	verificationArgv, err := json.Marshal(profile.Verification.Argv)
	if err != nil {
		return readiness.Profile{}, fmt.Errorf("marshal readiness profile verification argv: %w", err)
	}

	var setupExecutable, setupArgv, setupWorkingDirectory sql.NullString
	var setupTimeout sql.NullInt64
	if profile.Setup != nil {
		argv, err := json.Marshal(profile.Setup.Argv)
		if err != nil {
			return readiness.Profile{}, fmt.Errorf("marshal readiness profile setup argv: %w", err)
		}
		setupExecutable = sql.NullString{String: profile.Setup.Executable, Valid: true}
		setupArgv = sql.NullString{String: string(argv), Valid: true}
		setupWorkingDirectory = sql.NullString{String: profile.Setup.WorkingDirectory, Valid: true}
		setupTimeout = sql.NullInt64{Int64: int64(profile.Setup.TimeoutSeconds), Valid: true}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	var currentVersion sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT version FROM readiness_profiles WHERE repository_id = ?`, repositoryID).Scan(&currentVersion); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return readiness.Profile{}, MapSQLiteError(fmt.Errorf("resolve current readiness profile version: %w", err))
	}
	nextVersion := uint64(1)
	if currentVersion.Valid {
		nextVersion = uint64(currentVersion.Int64) + 1
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO readiness_profiles (
    repository_id, project_id,
    setup_executable, setup_argv_json, setup_working_directory, setup_timeout_seconds,
    verification_executable, verification_argv_json, verification_working_directory, verification_timeout_seconds,
    version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (repository_id) DO UPDATE SET
    setup_executable = excluded.setup_executable,
    setup_argv_json = excluded.setup_argv_json,
    setup_working_directory = excluded.setup_working_directory,
    setup_timeout_seconds = excluded.setup_timeout_seconds,
    verification_executable = excluded.verification_executable,
    verification_argv_json = excluded.verification_argv_json,
    verification_working_directory = excluded.verification_working_directory,
    verification_timeout_seconds = excluded.verification_timeout_seconds,
    version = excluded.version,
    updated_at = excluded.updated_at`,
		repositoryID, projectID,
		setupExecutable, setupArgv, setupWorkingDirectory, setupTimeout,
		profile.Verification.Executable, string(verificationArgv), profile.Verification.WorkingDirectory, profile.Verification.TimeoutSeconds,
		nextVersion, now, now,
	); err != nil {
		return readiness.Profile{}, MapSQLiteError(fmt.Errorf("set readiness profile: %w", err))
	}

	result := profile
	result.Version = nextVersion
	return result, nil
}

func (r readinessRepository) GetReadinessProfile(ctx context.Context, repositoryID string) (readiness.Profile, error) {
	return getReadinessProfileTx(ctx, r.tx, repositoryID)
}

func getReadinessProfileTx(ctx context.Context, tx *sql.Tx, repositoryID string) (readiness.Profile, error) {
	var setupExecutable, setupArgv, setupWorkingDirectory sql.NullString
	var setupTimeout sql.NullInt64
	var verificationExecutable, verificationArgv, verificationWorkingDirectory string
	var verificationTimeout uint32
	var version uint64
	err := tx.QueryRowContext(ctx, `
SELECT setup_executable, setup_argv_json, setup_working_directory, setup_timeout_seconds,
       verification_executable, verification_argv_json, verification_working_directory, verification_timeout_seconds,
       version
FROM readiness_profiles WHERE repository_id = ?`, repositoryID,
	).Scan(
		&setupExecutable, &setupArgv, &setupWorkingDirectory, &setupTimeout,
		&verificationExecutable, &verificationArgv, &verificationWorkingDirectory, &verificationTimeout,
		&version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return readiness.Profile{}, fmt.Errorf("%w: readiness profile for repository %s", ports.ErrPersistenceNotFound, repositoryID)
	}
	if err != nil {
		return readiness.Profile{}, MapSQLiteError(fmt.Errorf("load readiness profile: %w", err))
	}

	var verificationArgvSlice []string
	if err := json.Unmarshal([]byte(verificationArgv), &verificationArgvSlice); err != nil {
		return readiness.Profile{}, fmt.Errorf("unmarshal readiness profile verification argv: %w", err)
	}

	profile := readiness.Profile{
		RepositoryID: project.RepositoryID(repositoryID),
		Verification: readiness.CommandSpec{
			Executable: verificationExecutable, Argv: verificationArgvSlice,
			WorkingDirectory: verificationWorkingDirectory, TimeoutSeconds: verificationTimeout,
		},
		Version: version,
	}
	if setupExecutable.Valid {
		var setupArgvSlice []string
		if err := json.Unmarshal([]byte(setupArgv.String), &setupArgvSlice); err != nil {
			return readiness.Profile{}, fmt.Errorf("unmarshal readiness profile setup argv: %w", err)
		}
		profile.Setup = &readiness.CommandSpec{
			Executable: setupExecutable.String, Argv: setupArgvSlice,
			WorkingDirectory: setupWorkingDirectory.String, TimeoutSeconds: uint32(setupTimeout.Int64),
		}
	}
	return profile, nil
}

// --- BaselineAttempt ---

func (r readinessRepository) RecordBaselineAttempt(ctx context.Context, req ports.RecordBaselineAttemptRequest) (ports.BaselineAttempt, error) {
	return recordBaselineAttemptTx(ctx, r.tx, req)
}

func recordBaselineAttemptTx(ctx context.Context, tx *sql.Tx, req ports.RecordBaselineAttemptRequest) (ports.BaselineAttempt, error) {
	now := time.Now().UTC()
	var exitCode sql.NullInt64
	if req.ExitCode != nil {
		exitCode = sql.NullInt64{Int64: int64(*req.ExitCode), Valid: true}
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO readiness_baseline_attempts (
    id, project_id, repository_workspace_id, repository_id, job_id, stage, outcome,
    exit_code, duration_ms, stdout_excerpt, stderr_excerpt, error_code, error_message, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.ID, req.ProjectID, req.RepositoryWorkspaceID, req.RepositoryID, req.JobID, string(req.Stage), string(req.Outcome),
		exitCode, req.DurationMS, req.StdoutExcerpt, req.StderrExcerpt,
		nullableStringPtr(req.ErrorCode), nullableStringPtr(req.ErrorMessage), now.Format(time.RFC3339Nano),
	); err != nil {
		return ports.BaselineAttempt{}, MapSQLiteError(fmt.Errorf("record baseline attempt: %w", err))
	}

	return ports.BaselineAttempt{
		ID: req.ID, ProjectID: req.ProjectID, RepositoryWorkspaceID: req.RepositoryWorkspaceID,
		RepositoryID: req.RepositoryID, JobID: req.JobID, Stage: req.Stage, Outcome: req.Outcome,
		ExitCode: req.ExitCode, DurationMS: req.DurationMS, StdoutExcerpt: req.StdoutExcerpt, StderrExcerpt: req.StderrExcerpt,
		ErrorCode: req.ErrorCode, ErrorMessage: req.ErrorMessage, CreatedAt: now,
	}, nil
}

func (r readinessRepository) GetBaselineAttemptByJobID(ctx context.Context, jobID string) (ports.BaselineAttempt, error) {
	row := r.tx.QueryRowContext(ctx, `
SELECT id, project_id, repository_workspace_id, repository_id, job_id, stage, outcome,
       exit_code, duration_ms, stdout_excerpt, stderr_excerpt, error_code, error_message, created_at
FROM readiness_baseline_attempts WHERE job_id = ?`, jobID)
	attempt, err := scanBaselineAttemptRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.BaselineAttempt{}, fmt.Errorf("%w: baseline attempt for job %s", ports.ErrPersistenceNotFound, jobID)
	}
	return attempt, err
}

func (r readinessRepository) ListBaselineAttempts(ctx context.Context, repositoryWorkspaceID string) ([]ports.BaselineAttempt, error) {
	rows, err := r.tx.QueryContext(ctx, `
SELECT id, project_id, repository_workspace_id, repository_id, job_id, stage, outcome,
       exit_code, duration_ms, stdout_excerpt, stderr_excerpt, error_code, error_message, created_at
FROM readiness_baseline_attempts WHERE repository_workspace_id = ? ORDER BY julianday(created_at)`, repositoryWorkspaceID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list baseline attempts: %w", err))
	}
	defer rows.Close()

	var result []ports.BaselineAttempt
	for rows.Next() {
		attempt, err := scanBaselineAttemptRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, MapSQLiteError(fmt.Errorf("iterate baseline attempts: %w", err))
	}
	return result, nil
}

func scanBaselineAttemptRow(row repositoryRowScanner) (ports.BaselineAttempt, error) {
	var id, projectID, repositoryWorkspaceID, repositoryID, jobID, stage, outcome, createdAtText string
	var exitCode sql.NullInt64
	var durationMS int64
	var stdoutExcerpt, stderrExcerpt string
	var errorCode, errorMessage sql.NullString
	if err := row.Scan(
		&id, &projectID, &repositoryWorkspaceID, &repositoryID, &jobID, &stage, &outcome,
		&exitCode, &durationMS, &stdoutExcerpt, &stderrExcerpt, &errorCode, &errorMessage, &createdAtText,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.BaselineAttempt{}, err
		}
		return ports.BaselineAttempt{}, MapSQLiteError(fmt.Errorf("scan baseline attempt row: %w", err))
	}
	createdAt, err := parseDBTime(createdAtText)
	if err != nil {
		return ports.BaselineAttempt{}, err
	}
	attempt := ports.BaselineAttempt{
		ID: id, ProjectID: projectID, RepositoryWorkspaceID: repositoryWorkspaceID, RepositoryID: repositoryID, JobID: jobID,
		Stage: readiness.Stage(stage), Outcome: readiness.BaselineOutcome(outcome),
		DurationMS: durationMS, StdoutExcerpt: stdoutExcerpt, StderrExcerpt: stderrExcerpt, CreatedAt: createdAt,
	}
	if exitCode.Valid {
		value := int(exitCode.Int64)
		attempt.ExitCode = &value
	}
	if errorCode.Valid {
		attempt.ErrorCode = &errorCode.String
	}
	if errorMessage.Valid {
		attempt.ErrorMessage = &errorMessage.String
	}
	return attempt, nil
}

// --- EnvironmentBlocker ---

func (r readinessRepository) OpenEnvironmentBlocker(ctx context.Context, req ports.OpenEnvironmentBlockerRequest) (ports.EnvironmentBlocker, bool, error) {
	existing, err := getOpenEnvironmentBlockerTx(ctx, r.tx, req.RepositoryWorkspaceID)
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		return ports.EnvironmentBlocker{}, false, err
	}

	now := time.Now().UTC()
	if _, err := r.tx.ExecContext(ctx, `
INSERT INTO readiness_environment_blockers (
    id, project_id, repository_workspace_id, repository_id, job_id, type, reason, status, created_at, resolved_at
) VALUES (?, ?, ?, ?, ?, ?, ?, 'OPEN', ?, NULL)`,
		req.ID, req.ProjectID, req.RepositoryWorkspaceID, req.RepositoryID, req.JobID,
		readiness.EnvironmentBlockerType, req.Reason, now.Format(time.RFC3339Nano),
	); err != nil {
		return ports.EnvironmentBlocker{}, false, MapSQLiteError(fmt.Errorf("open environment blocker: %w", err))
	}

	return ports.EnvironmentBlocker{
		ID: req.ID, ProjectID: req.ProjectID, RepositoryWorkspaceID: req.RepositoryWorkspaceID,
		RepositoryID: req.RepositoryID, JobID: req.JobID, Type: readiness.EnvironmentBlockerType,
		Reason: req.Reason, Status: readiness.BlockerOpen, CreatedAt: now,
	}, false, nil
}

func (r readinessRepository) ResolveOpenEnvironmentBlocker(ctx context.Context, repositoryWorkspaceID string, resolvedAt time.Time) error {
	_, err := r.tx.ExecContext(ctx, `
UPDATE readiness_environment_blockers
SET status = 'RESOLVED', resolved_at = ?
WHERE repository_workspace_id = ? AND status = 'OPEN'`,
		resolvedAt.UTC().Format(time.RFC3339Nano), repositoryWorkspaceID,
	)
	if err != nil {
		return MapSQLiteError(fmt.Errorf("resolve open environment blocker: %w", err))
	}
	return nil
}

func (r readinessRepository) GetOpenEnvironmentBlocker(ctx context.Context, repositoryWorkspaceID string) (ports.EnvironmentBlocker, error) {
	return getOpenEnvironmentBlockerTx(ctx, r.tx, repositoryWorkspaceID)
}

func getOpenEnvironmentBlockerTx(ctx context.Context, tx *sql.Tx, repositoryWorkspaceID string) (ports.EnvironmentBlocker, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, project_id, repository_workspace_id, repository_id, job_id, type, reason, status, created_at, resolved_at
FROM readiness_environment_blockers WHERE repository_workspace_id = ? AND status = 'OPEN'`, repositoryWorkspaceID)

	var id, projectID, workspaceID, repositoryID, jobID, blockerType, reason, status, createdAtText string
	var resolvedAtText sql.NullString
	err := row.Scan(&id, &projectID, &workspaceID, &repositoryID, &jobID, &blockerType, &reason, &status, &createdAtText, &resolvedAtText)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.EnvironmentBlocker{}, fmt.Errorf("%w: open environment blocker for repository workspace %s", ports.ErrPersistenceNotFound, repositoryWorkspaceID)
	}
	if err != nil {
		return ports.EnvironmentBlocker{}, MapSQLiteError(fmt.Errorf("load open environment blocker: %w", err))
	}
	createdAt, err := parseDBTime(createdAtText)
	if err != nil {
		return ports.EnvironmentBlocker{}, err
	}
	blocker := ports.EnvironmentBlocker{
		ID: id, ProjectID: projectID, RepositoryWorkspaceID: workspaceID, RepositoryID: repositoryID, JobID: jobID,
		Type: blockerType, Reason: reason, Status: readiness.BlockerStatus(status), CreatedAt: createdAt,
	}
	if resolvedAtText.Valid {
		resolvedAt, err := parseDBTime(resolvedAtText.String)
		if err != nil {
			return ports.EnvironmentBlocker{}, err
		}
		blocker.ResolvedAt = &resolvedAt
	}
	return blocker, nil
}
