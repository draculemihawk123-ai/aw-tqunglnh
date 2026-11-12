-- V12-01 (docs/design/15-v12-ui-operations.md): a local commit whose job could not be carried to an
-- end used to stay REQUESTED for ever — the worker answered every claim with an error, the lease
-- expired, the job went DEAD and nothing closed the operation or said why. The operation now closes
-- FAILED with the typed reason EXECUTION_ERROR, and the new column failure_detail keeps the last
-- error the worker saw (redacted), so the API, CLI and UI can show it.
--
-- Widening the failure_reason CHECK needs the create-copy-drop-rename rebuild 0043/0049/0050 used.
-- Since 0050, release_set_local_commit_selections REFERENCES release_set_local_commits(id), so the
-- DROP runs with PRAGMA foreign_keys=OFF (migrationsRequiringForeignKeysOff[54]). Every column and
-- index is carried forward unchanged; the explicit column lists are the "nothing silently dropped"
-- proof, and failure_detail starts NULL for every existing row.
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
        'WORKSPACE_QUARANTINED', 'MARKER_DRIFT', 'NO_CHANGES', 'HEAD_DRIFT', 'CHANGESET_DRIFT', 'EXECUTION_ERROR'
    )),
    failure_detail TEXT,
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
