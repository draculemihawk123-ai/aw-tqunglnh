-- V9-09 (docs/design/12-v9-harness-alignment.md V9-09): widen
-- release_set_local_commits.failure_reason's own CHECK allow-list to include
-- NO_CHANGES — the typed, non-retryable terminal reason
-- internal/app/releasesetcommit.ExecuteReleaseSetLocalCommit records when the
-- target worktree has nothing to commit (work.FailureNoChanges). Before this
-- migration the worker returned the adapter's "nothing to commit" error as an
-- ordinary failure, so the job was re-claimed until it went DEAD while the
-- operation stayed REQUESTED forever. The reason is stored on the operation
-- row (the same column MARKER_DRIFT / WORKSPACE_QUARANTINED already use) so
-- `aw release-set local-commit status`, the HTTP status route and the
-- ReleaseSetLocalCommitFailed event all report it with no new surface.
--
-- SQLite has no ALTER TABLE ... DROP/MODIFY CONSTRAINT, so widening a CHECK
-- constraint requires the full create-copy-drop-rename rebuild
-- 0032_artifacts_purged_state.sql's own doc comment describes. Unlike 0025/
-- 0032/0034 this migration does NOT need PRAGMA foreign_keys=OFF: no other
-- table REFERENCES release_set_local_commits (it only has outgoing foreign
-- keys), so DROP TABLE has no referencing row to trip over.
--
-- Every column and index release_set_local_commits has ever had
-- (0037_release_set_local_commits.sql's own twenty-two columns and two
-- indexes) is carried forward unchanged except failure_reason's widened
-- CHECK; the explicit column list in the INSERT below is the authoritative
-- "nothing silently dropped" proof.
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
        'WORKSPACE_QUARANTINED', 'MARKER_DRIFT', 'NO_CHANGES'
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
