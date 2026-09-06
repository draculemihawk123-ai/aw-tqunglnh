-- V4-13 (docs/design/06-v4-runtime-engine.md, ADR-020): widen
-- durable_jobs.job_class's own CONTROL allow-list to include
-- RECOVERY_REAPER, exactly as ADR-020's own allow-list table names
-- ("CANCEL_RUN_COORDINATOR, WORKSPACE_RECONCILE, WORKSPACE_SET_RELEASE,
-- RECOVERY_REAPER") — migration 23 deliberately excluded it ("turning it
-- into a real durable job is a separate, future decision"); this is that
-- decision. "Reaper job là JobClass=CONTROL nên không tự bị cancel fence"
-- (V4-13's own Thực hiện line) needs a real durable_jobs row to attach
-- JobClass/cancel_epoch fencing to, which is exactly what the recovery
-- reaper's own self-rescheduling CONTROL job (internal/app/runtime,
-- mirroring SCOPE_EXPANSION_RECONCILE's own PollGeneration pattern) is.
--
-- SQLite has no ALTER TABLE ... DROP/MODIFY CONSTRAINT, so widening a CHECK
-- constraint requires the full create-copy-drop-rename rebuild SQLite's own
-- documentation describes for a table other tables hold real foreign-key
-- references into (write_leases.holder_job_id, repository_probe_attempts.job_id,
-- readiness_evidence's own two job_id columns all REFERENCES durable_jobs(id)).
-- Unlike every prior migration that used this same DROP-and-recreate shape
-- (0002/0003/0006/0016/0021's own doc comments), durable_jobs is NOT empty
-- by the time this migration ever runs — every merged V4 task since V4-02
-- has created real durable_jobs rows — so this migration's own Go-side
-- caller (migrations.go's own applyMigrationWithForeignKeysOff,
-- migrationsRequiringForeignKeysOff) runs it with PRAGMA foreign_keys=OFF
-- for its own duration and verifies zero PRAGMA foreign_key_check
-- violations before ever committing — this file's own SQL never has to
-- concern itself with that toggle at all, only with the rebuild itself.
--
-- Every column and index durable_jobs has ever gained (0001's own initial
-- eleven-plus-eight columns, 0023's own run_id/job_class/cancel_epoch) is
-- carried forward unchanged except job_class's own widened CHECK; the
-- explicit column list in the INSERT below is the authoritative "nothing
-- silently dropped" proof, cross-checked by this task's own
-- TestMigration0025_PreservesEveryColumnAndRow.
--
-- project_id also loses its NOT NULL here (confirmed with the user,
-- superseding this migration's own first draft, which kept it NOT NULL and
-- required RECOVERY_REAPER's own job to fabricate a ProjectID it doesn't
-- have): RECOVERY_REAPER is installation-global, not scoped to any one
-- Project, so it is the one job kind ever enqueued with project_id left
-- NULL. The trailing table-level CHECK below is what keeps that a narrow,
-- explicit exception rather than a silent hole — every kind OTHER than
-- RECOVERY_REAPER still requires a real, non-NULL project_id (still
-- FK-checked against projects(id) whenever it IS present, since SQL FK
-- constraints only ever validate non-NULL values), and RECOVERY_REAPER
-- itself must ALSO carry no run_id, no cancel_epoch, and JobClass=CONTROL
-- — the same "kind determines scope" invariant
-- internal/app/ports.ValidateJobScope enforces server-side before any INSERT
-- is even attempted (the second authority for the identical rule, mirroring
-- job_class's own CHECK/map pairing one section below).
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
            (job_class = 'CONTROL' AND kind IN ('CANCEL_RUN_COORDINATOR', 'WORKSPACE_RECONCILIATION', 'WORKSPACE_SET_RELEASE', 'RECOVERY_REAPER'))
            OR (job_class = 'RUN_WORK' AND kind NOT IN ('CANCEL_RUN_COORDINATOR', 'WORKSPACE_RECONCILIATION', 'WORKSPACE_SET_RELEASE', 'RECOVERY_REAPER'))
        ),
    cancel_epoch INTEGER,
    -- RECOVERY_REAPER's own project/run/class/fence scoping, the DB-level
    -- authority for ValidateJobScope's identical rule: exactly one kind
    -- (RECOVERY_REAPER) is exempt from "every job has a real owning
    -- Project", and that same kind must ALSO have no run_id, no
    -- cancel_epoch, and JobClass=CONTROL — never a partial exemption.
    CHECK (
        (kind = 'RECOVERY_REAPER'
            AND project_id IS NULL
            AND run_id IS NULL
            AND job_class = 'CONTROL'
            AND cancel_epoch IS NULL)
        OR
        (kind <> 'RECOVERY_REAPER'
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
