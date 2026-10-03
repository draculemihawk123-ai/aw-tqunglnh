-- readiness_baseline_attempts.profile_version and readiness_baseline_exceptions:
-- V9-08's baseline gate (docs/design/12-v9-harness-alignment.md V9-08, gap G8).
--
-- Until V9-08 a baseline attempt was evidence nobody read: nothing started one
-- on its own and nothing stopped a writer when it failed. V9-08 makes it a
-- gate: a WorkItem that may WRITE to a repository with a readiness profile is
-- admitted only when the latest baseline for that profile PASSED, or when an
-- operator accepted the failure with an audit record. Two facts have to be
-- durable for that:
--
-- 1. readiness_baseline_attempts.profile_version: which version of the
--    repository's readiness profile the attempt ran
--    (readiness_profiles.version, bumped on every change). A baseline only
--    vouches for the profile it ran, so a changed profile makes every earlier
--    attempt stale and the baseline runs again. NULL for every attempt written
--    before this migration, which therefore vouches for no profile; plain ADD
--    COLUMN, no backfill (the same shape as migrations 0042, 0044 and 0046).
--
-- 2. readiness_baseline_exceptions: an operator's accepted exception for one
--    failed attempt -- who, when, and the reason -- append-only like the
--    attempts themselves. It names the attempt, not the repository, so a later
--    failing attempt (a changed profile, a new commit that broke the
--    repository) needs its own acceptance. UNIQUE (baseline_attempt_id): one
--    exception per attempt; accepting twice is the same fact.
ALTER TABLE readiness_baseline_attempts ADD COLUMN profile_version INTEGER;

CREATE TABLE readiness_baseline_exceptions (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    baseline_attempt_id TEXT NOT NULL REFERENCES readiness_baseline_attempts(id),
    repository_workspace_id TEXT NOT NULL REFERENCES repository_workspaces(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    reason TEXT NOT NULL CHECK (length(trim(reason)) > 0),
    accepted_by TEXT NOT NULL CHECK (length(trim(accepted_by)) > 0),
    accepted_at TEXT NOT NULL,
    UNIQUE (baseline_attempt_id)
);

CREATE INDEX readiness_baseline_exceptions_workspace_idx
    ON readiness_baseline_exceptions(repository_workspace_id, accepted_at);
