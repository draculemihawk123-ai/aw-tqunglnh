-- V5-14 (docs/design/07-v5-execution-evidence.md; ADR-017; go-core-spec
-- §19): widen durable_jobs's own two CHECK constraints the identical way
-- migration 25 did for RECOVERY_REAPER, this time adding ARTIFACT_SWEEP —
-- the retention sweeper's own self-rescheduling CONTROL job (mirroring
-- RECOVERY_REAPER's own shape exactly): a single, global, installation-wide
-- coordinator with no per-Project scope of its own (a content-addressed
-- Locator can be shared by Artifact rows across different Projects —
-- 0027_artifacts.sql's own "content_hash is deliberately NOT unique" — so
-- this sweep cannot be pinned to any one Project any more than
-- RECOVERY_REAPER's own project_id can be).
--
-- Same rebuild technique as migration 25 (SQLite has no ALTER TABLE ...
-- DROP/MODIFY CONSTRAINT), same reason it needs
-- migrationsRequiringForeignKeysOff (write_leases.holder_job_id,
-- repository_probe_attempts.job_id, readiness_evidence's own two job_id
-- columns all REFERENCES durable_jobs(id), and real rows exist by the time
-- this migration runs against any real database).
--
-- Every column and index durable_jobs has ever had (0001's own initial
-- columns, 0023's own run_id/job_class/cancel_epoch, 0025's own widened
-- CHECK) is carried forward unchanged except these two CHECKs; the explicit
-- column list in the INSERT below is the authoritative "nothing silently
-- dropped" proof.
CREATE TABLE durable_jobs_new (
    id TEXT PRIMARY KEY,
    project_id TEXT REFERENCES projects(id),
    kind TEXT NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('AVAILABLE','LEASED','SUCCEEDED','FAILED','DEAD','CANCELLED')),
    available_at TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 0,
    claim_count INTEGER NOT NULL DEFAULT 0,
    max_claims INTEGER NOT NULL CHECK (max_claims > 0),
    lease_owner TEXT,
    lease_token INTEGER NOT NULL DEFAULT 0,
    lease_until TEXT,
    heartbeat_at TEXT,
    idempotency_key TEXT NOT NULL UNIQUE,
    last_error_code TEXT,
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    run_id TEXT REFERENCES workflow_runs(id),
    job_class TEXT NOT NULL DEFAULT 'RUN_WORK'
        CHECK (
            (job_class = 'CONTROL' AND kind IN ('CANCEL_RUN_COORDINATOR', 'WORKSPACE_RECONCILIATION', 'WORKSPACE_SET_RELEASE', 'RECOVERY_REAPER', 'ARTIFACT_SWEEP'))
            OR (job_class = 'RUN_WORK' AND kind NOT IN ('CANCEL_RUN_COORDINATOR', 'WORKSPACE_RECONCILIATION', 'WORKSPACE_SET_RELEASE', 'RECOVERY_REAPER', 'ARTIFACT_SWEEP'))
        ),
    cancel_epoch INTEGER,
    -- RECOVERY_REAPER/ARTIFACT_SWEEP's own project/run/class/fence scoping,
    -- the DB-level authority for ValidateJobScope's identical rule: these
    -- two kinds are exempt from "every job has a real owning Project", and
    -- each must ALSO have no run_id, no cancel_epoch, and JobClass=CONTROL
    -- — never a partial exemption.
    CHECK (
        (kind IN ('RECOVERY_REAPER', 'ARTIFACT_SWEEP')
            AND project_id IS NULL
            AND run_id IS NULL
            AND job_class = 'CONTROL'
            AND cancel_epoch IS NULL)
        OR
        (kind NOT IN ('RECOVERY_REAPER', 'ARTIFACT_SWEEP')
            AND project_id IS NOT NULL)
    )
);

INSERT INTO durable_jobs_new (
    id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at,
    priority, claim_count, max_claims, lease_owner, lease_token, lease_until, heartbeat_at,
    idempotency_key, last_error_code, version, created_at, updated_at, run_id, job_class, cancel_epoch
)
SELECT
    id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at,
    priority, claim_count, max_claims, lease_owner, lease_token, lease_until, heartbeat_at,
    idempotency_key, last_error_code, version, created_at, updated_at, run_id, job_class, cancel_epoch
FROM durable_jobs;

DROP TABLE durable_jobs;

ALTER TABLE durable_jobs_new RENAME TO durable_jobs;

CREATE INDEX durable_jobs_claim_idx
    ON durable_jobs (state, available_at, priority DESC, created_at);

CREATE INDEX idx_durable_jobs_claim_run_work
    ON durable_jobs (priority DESC, created_at, id)
    WHERE job_class = 'RUN_WORK' AND state = 'AVAILABLE' AND cancel_epoch IS NULL;

CREATE INDEX idx_durable_jobs_claim_control
    ON durable_jobs (priority DESC, created_at, id)
    WHERE job_class = 'CONTROL' AND state = 'AVAILABLE';

CREATE INDEX idx_durable_jobs_run_id ON durable_jobs (run_id) WHERE run_id IS NOT NULL;
