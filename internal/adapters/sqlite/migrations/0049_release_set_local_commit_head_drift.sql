-- V11-01 (docs/design/14-v11-bugfix.md): widen
-- release_set_local_commits.failure_reason's own CHECK allow-list to include
-- HEAD_DRIFT — the typed, non-retryable terminal reason
-- internal/app/releasesetcommit.ExecuteReleaseSetLocalCommit records when the
-- worktree's HEAD is not the revision aw recorded as the RepositoryWorkspace's
-- CurrentRevision (work.FailureHeadDrift): someone committed in the worktree
-- outside aw. A local commit made on top of that HEAD would fold the outside
-- commits into the history aw treats as its own with no audit, so the operation
-- closes FAILED instead (the operator adopts the commits with
-- `aw repository-workspace adopt-head`, or puts the worktree back, and requests
-- a new local commit). The reason is stored on the operation row, the same column
-- MARKER_DRIFT / WORKSPACE_QUARANTINED / NO_CHANGES already use, so the status
-- query and the ReleaseSetLocalCommitFailed event report it with no new surface.
--
-- SQLite has no ALTER TABLE ... DROP/MODIFY CONSTRAINT, so widening a CHECK
-- constraint requires the same create-copy-drop-rename rebuild
-- 0043_release_set_local_commit_no_changes.sql used. No other table REFERENCES
-- release_set_local_commits, so PRAGMA foreign_keys=OFF is not needed. Every column and index is
-- carried forward unchanged except failure_reason's widened CHECK; the explicit
-- column list in the INSERT is the "nothing silently dropped" proof.
CREATE TABLE release_set_local_commits_new (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    release_set_id TEXT NOT NULL REFERENCES release_sets(id),
    expected_release_set_version INTEGER NOT NULL CHECK (expected_release_set_version > 0),
    repository_workspace_id TEXT NOT NULL REFERENCES repository_workspaces(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    expected_generation INTEGER NOT NULL CHECK (expected_generation > 0),
    expected_workspace_version INTEGER NOT NULL CHECK (expected_workspace_version > 0),
    actor TEXT NOT NULL,
    message TEXT NOT NULL,
    author_name TEXT NOT NULL,
    author_email TEXT NOT NULL,
    message_hash TEXT NOT NULL,
    marker TEXT NOT NULL UNIQUE,
    state TEXT NOT NULL CHECK (state IN ('REQUESTED', 'COMMITTED', 'FAILED')),
    failure_reason TEXT CHECK (failure_reason IS NULL OR failure_reason IN (
        'WORKSPACE_QUARANTINED', 'MARKER_DRIFT', 'NO_CHANGES', 'HEAD_DRIFT'
    )),
    parent_vcs_object_id TEXT,
    result_vcs_object_id TEXT,
    job_id TEXT,
    created_at TEXT NOT NULL,
    completed_at TEXT,
    version INTEGER NOT NULL CHECK (version > 0)
);

INSERT INTO release_set_local_commits_new (
    id, project_id, release_set_id, expected_release_set_version, repository_workspace_id, repository_id,
    expected_generation, expected_workspace_version, actor, message, author_name, author_email, message_hash,
    marker, state, failure_reason, parent_vcs_object_id, result_vcs_object_id, job_id, created_at, completed_at, version
)
SELECT
    id, project_id, release_set_id, expected_release_set_version, repository_workspace_id, repository_id,
    expected_generation, expected_workspace_version, actor, message, author_name, author_email, message_hash,
    marker, state, failure_reason, parent_vcs_object_id, result_vcs_object_id, job_id, created_at, completed_at, version
FROM release_set_local_commits;

DROP TABLE release_set_local_commits;

ALTER TABLE release_set_local_commits_new RENAME TO release_set_local_commits;

CREATE INDEX idx_release_set_local_commits_release_set ON release_set_local_commits(release_set_id);
CREATE INDEX idx_release_set_local_commits_workspace ON release_set_local_commits(repository_workspace_id);
