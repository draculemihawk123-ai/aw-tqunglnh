-- V11-07 (docs/design/14-v11-bugfix.md, ADR-040): approve and commit in one action, choosing the
-- files. Three schema changes, one migration so a half-applied V11-07 cannot exist:
--
-- 1. release_set_local_commits.failure_reason's CHECK allow-list gains CHANGESET_DRIFT
--    (work.FailureChangeSetDrift): the worktree no longer holds what the approver reviewed
--    (a selected file changed, or is no longer changed), so the operation closes FAILED before
--    anything is staged. Determinate, so the job is not retried.
-- 2. blockers.type's CHECK gains RELEASE_FAILED (work.BlockerReleaseFailed): the decision
--    already moved the run on and cannot be rolled back, so a commit that then fails FAILED
--    (drift, no changes, quarantined workspace, HEAD drift) opens this blocker on the WorkItem
--    to make it visible on the Board; the operator retries the commit and resolves it.
-- 3. release_set_local_commit_selections: for an operation that commits only some files, the
--    files and hashes the approver saw, the digest, what to do with the rest (KEEP/PARK), where
--    the rest was parked, and a bounded copy of the patch the approver reviewed — so what was
--    approved can be read back later even after the worktree changed. A row exists only for a
--    selective operation (approval-bound or manual with explicit paths); an operation without
--    one commits the whole worktree exactly as before.
--
-- SQLite has no ALTER TABLE ... DROP/MODIFY CONSTRAINT, so (1) and (2) use the create-copy-drop-
-- rename rebuild 0043/0045/0049 used. At the point they run, nothing REFERENCES either table
-- (the new selections table is created after the rebuilds), so PRAGMA foreign_keys=OFF is not
-- needed. Every column and index is carried forward unchanged except the widened CHECK; the
-- explicit column lists in the INSERTs are the "nothing silently dropped" proof.

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
        'WORKSPACE_QUARANTINED', 'MARKER_DRIFT', 'NO_CHANGES', 'HEAD_DRIFT', 'CHANGESET_DRIFT'
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

CREATE TABLE blockers_new (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    work_item_id TEXT NOT NULL REFERENCES work_items(id),
    type TEXT NOT NULL CHECK (type IN (
        'RUN_CANCELLED', 'RUN_FAILED', 'COMPLETION_POLICY_FAILED', 'SCOPE_EXPANSION_REQUIRED',
        'ISOLATION_ENFORCEMENT_UNAVAILABLE', 'ADAPTER_BUILD_DRIFT',
        'CAPABILITY_REQUIREMENT_UNSATISFIED', 'WRITE_CAPABILITY_OR_GRANT_MISSING', 'RELEASE_FAILED'
    )),
    state TEXT NOT NULL CHECK (state IN ('OPEN', 'RESOLVED', 'WAIVED')),
    source_run_id TEXT REFERENCES workflow_runs(id),
    source_node_run_id TEXT REFERENCES node_runs(id),
    source_attempt_id TEXT REFERENCES execution_attempts(id),
    reason TEXT NOT NULL,
    opened_at TEXT NOT NULL,
    resolved_at TEXT,
    resolved_by TEXT,
    resolution_note TEXT,
    decision_artifact_id TEXT REFERENCES decision_artifacts(id),
    version INTEGER NOT NULL CHECK (version > 0)
);

INSERT INTO blockers_new (
    id, project_id, work_item_id, type, state, source_run_id, source_node_run_id, source_attempt_id,
    reason, opened_at, resolved_at, resolved_by, resolution_note, decision_artifact_id, version
)
SELECT
    id, project_id, work_item_id, type, state, source_run_id, source_node_run_id, source_attempt_id,
    reason, opened_at, resolved_at, resolved_by, resolution_note, decision_artifact_id, version
FROM blockers;

DROP TABLE blockers;

ALTER TABLE blockers_new RENAME TO blockers;

CREATE INDEX idx_blockers_work_item ON blockers(work_item_id, state);

-- approval_request_id / work_item_id / run_id are NULL for a manual selective commit; set when the
-- operation was created by a decision, so a failure can open RELEASE_FAILED on the right WorkItem.
-- parked_ref / parked_paths_json are set once ParkRemainingChanges ran; restored_at once
-- RestoreParkedChanges did. patch is the bounded unified diff of the selected files as the
-- approver saw it (empty when the caller supplied none).
CREATE TABLE release_set_local_commit_selections (
    release_set_local_commit_id TEXT PRIMARY KEY REFERENCES release_set_local_commits(id),
    approval_request_id TEXT,
    work_item_id TEXT,
    run_id TEXT,
    files_json TEXT NOT NULL,
    change_set_digest TEXT NOT NULL,
    unselected TEXT NOT NULL CHECK (unselected IN ('KEEP', 'PARK')),
    parked_ref TEXT,
    parked_paths_json TEXT,
    patch BLOB,
    patch_truncated INTEGER NOT NULL DEFAULT 0 CHECK (patch_truncated IN (0, 1)),
    restored_at TEXT,
    created_at TEXT NOT NULL
);

CREATE INDEX idx_release_set_local_commit_selections_approval
    ON release_set_local_commit_selections(approval_request_id);
