-- release_set_local_commits: V6-10E's own durable intent+result record for
-- RequestReleaseSetLocalCommit (docs/design/08-v6-api-projections.md
-- V6-10E; ADR-014, AK-ARCH-015C, GC-INV-26) — one row per requested local
-- Git commit operation against a ReleaseSet's own repository entry. This is
-- NOT a second copy of release_sets/release_set_repositories: it records
-- the OPERATION of materializing one repository's own current workspace
-- state as a real local commit, never a remote-authority record (no push/
-- PR/merge/force-push column exists here, by construction).
--
-- expected_release_set_version/expected_generation/expected_workspace_version
-- are this request's own pinned "ReleaseSet entry/version, workspace
-- generation/fence" (V6-10E's own Thực hiện line) — captured once, at
-- request time, from whatever was then current; a worker revalidates
-- Generation fresh before ever touching real Git state.
--
-- marker is the deterministic operation marker (sha256 of ReleaseSetID +
-- ReleaseSet version + RepositoryWorkspaceID + workspace generation +
-- actor + message hash) embedded as a trailer in the real commit message
-- this operation creates — the durable value a crash-recovery retry
-- reconciles against the workspace's own current HEAD commit BEFORE ever
-- calling LocalCommitCreator again (see internal/app/releasesetcommit's
-- own doc comment). UNIQUE: two distinct intents may never share one
-- marker (V6-10E's own "marker collision" Verify case).
--
-- parent_vcs_object_id is populated by PinReleaseSetLocalCommitParent
-- (still REQUESTED) before the real, mutating Git call ever happens — see
-- that method's own doc comment for why this exists independently of
-- result_vcs_object_id, which is populated only once state is COMMITTED.
--
-- state: REQUESTED (intent+job durably recorded, no terminal outcome yet)
-- -> COMMITTED (real commit created or reconciled, result recorded) or
-- FAILED (typed, non-retryable outcome — see
-- work.ReleaseSetLocalCommitFailureReason's own closed set, mirrored here
-- by hand in the CHECK constraint below).
CREATE TABLE release_set_local_commits (
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
        'WORKSPACE_QUARANTINED', 'MARKER_DRIFT'
    )),
    parent_vcs_object_id TEXT,
    result_vcs_object_id TEXT,
    job_id TEXT,
    created_at TEXT NOT NULL,
    completed_at TEXT,
    version INTEGER NOT NULL CHECK (version > 0)
);

CREATE INDEX idx_release_set_local_commits_release_set ON release_set_local_commits(release_set_id);
CREATE INDEX idx_release_set_local_commits_workspace ON release_set_local_commits(repository_workspace_id);

-- local_commit_write_leases: this task's own narrow, job-holder-only
-- mutual-exclusion lease over a RepositoryWorkspace's real Git state —
-- deliberately NOT a reuse of write_leases (0001_initial_schema.sql).
-- Every write_leases query (AcquireWriteLeases/HeartbeatWriteLeases/
-- ValidateWriteLease, internal/adapters/sqlite/scheduling.go) hard-requires
-- a live execution_attempts row (INNER JOIN ... WHERE attempt.state =
-- 'RUNNING') — that table is scoped to AGENT node execution attempts, a
-- concept this CONTROL-class job (RequestReleaseSetLocalCommit) has no
-- instance of. Reusing it would mean either fabricating a fake
-- execution_attempts row (a real domain concept this operation is not) or
-- loosening that table's own attempt-coupling for every existing AGENT
-- caller — both rejected as out of this task's own scope and a real
-- regression risk to already-merged V5-15D/E code. This table instead
-- holds a lease keyed to a JobLease alone (holder_job_id/
-- holder_job_lease_token/lease_owner), mirroring write_leases' own
-- fence_token/lease_until/CAS-on-conflict shape exactly, minus the attempt
-- column — the same "narrow port for a new capability, zero change to the
-- existing implementer" precedent ports.LocalCommitCreator (localcommit.go)
-- itself already established.
CREATE TABLE local_commit_write_leases (
    repository_workspace_id TEXT NOT NULL REFERENCES repository_workspaces(id),
    generation INTEGER NOT NULL,
    fence_token INTEGER NOT NULL,
    holder_job_id TEXT NOT NULL,
    holder_job_lease_token INTEGER NOT NULL,
    lease_owner TEXT NOT NULL,
    lease_until TEXT NOT NULL,
    PRIMARY KEY (repository_workspace_id, generation)
);
