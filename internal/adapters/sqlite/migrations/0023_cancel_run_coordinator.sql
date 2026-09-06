-- V4-12B (docs/design/06-v4-runtime-engine.md, ADR-020): CancelRun is a
-- quiesce PROTOCOL with a durable, fenced coordinator — never a bare CAS
-- straight to CANCELLED. This migration adds the two pieces of durable
-- state that protocol needs:
--
--   1. workflow_runs.cancel_epoch: NULL until a RunCancellationIntent
--      commits, then set to 1 in that SAME transaction — never reset,
--      never incremented again (a Run is cancelled at most once, and
--      run_cancellation_intents.run_id is already UNIQUE).
--
--   2. durable_jobs gains run_id/job_class/cancel_epoch. job_class is
--      derived from kind by a fixed, fail-closed Go mapping
--      (ports.ClassifyJobKind, confirmed with the user before writing
--      this migration: RUN_WORK is the default for every kind except the
--      closed CONTROL allow-list CANCEL_RUN_COORDINATOR/
--      WORKSPACE_RECONCILIATION/WORKSPACE_SET_RELEASE — never a
--      caller-supplied field, so no INSERT ever has to get this right by
--      hand) — but the CHECK constraint below is the actual authority:
--      it enforces the Kind<->JobClass mapping at the database level too,
--      so a hand-rolled INSERT that tried to claim job_class='CONTROL'
--      for a Kind outside that allow-list is rejected by SQLite itself,
--      not just by trusting the Go helper. RECOVERY_REAPER (named in the
--      ADR's own allow-list) is deliberately excluded: it names no real
--      durable_jobs row today (Store.RecoverExpiredJobs is a direct,
--      ticker-driven maintenance sweep with no lease/claim/RunID of its
--      own) — JobClass/cancel_epoch fencing has nothing to attach to.
--      Turning it into a real durable job is a separate, future decision.
--      cancel_epoch on a JOB row (distinct from the Run's own column
--      above) is set to 1 in the SAME transaction as the Run's own
--      cancel_epoch bump, for every non-terminal RUN_WORK job belonging
--      to that run — an AVAILABLE one is also flipped straight to
--      CANCELLED (nothing was ever dispatched for it); a LEASED one stays
--      LEASED (a worker already claimed it) but is now fenced, so that
--      worker's own re-check at its two checkpoints (before QUEUED->
--      RUNNING and immediately before starting real execution) sees the
--      epoch and backs off rather than proceeding.
--
--      Every table this project has ever migrated before has been empty
--      at ALTER TABLE time (migrations apply once, straight through, to a
--      brand-new database file, never to one with pre-existing rows) —
--      so the CHECK constraint below never has to validate against any
--      row this migration itself did not just default-fill, exactly the
--      same reasoning 0021_fork_branch_tokens.sql's own doc comment
--      already relies on for its DROP-and-recreate.
ALTER TABLE workflow_runs ADD COLUMN cancel_epoch INTEGER;

ALTER TABLE durable_jobs ADD COLUMN run_id TEXT REFERENCES workflow_runs(id);

ALTER TABLE durable_jobs ADD COLUMN job_class TEXT NOT NULL DEFAULT 'RUN_WORK'
    CHECK (
        (job_class = 'CONTROL' AND kind IN ('CANCEL_RUN_COORDINATOR', 'WORKSPACE_RECONCILIATION', 'WORKSPACE_SET_RELEASE'))
        OR (job_class = 'RUN_WORK' AND kind NOT IN ('CANCEL_RUN_COORDINATOR', 'WORKSPACE_RECONCILIATION', 'WORKSPACE_SET_RELEASE'))
    );

ALTER TABLE durable_jobs ADD COLUMN cancel_epoch INTEGER;

-- Claim CAS is split by class (docs/design/06-v4-runtime-engine.md's own
-- "Claim CAS tách theo class"): a RUN_WORK job additionally requires
-- cancel_epoch IS NULL so a fenced job can never be claimed again once its
-- owning Run starts cancelling; a CONTROL job (including the very
-- CANCEL_RUN_COORDINATOR job CancelRun itself enqueues) is never fenced by
-- cancel_epoch at all — fencing CONTROL jobs would make the coordinator
-- job that is supposed to DO the cancelling unable to ever run.
CREATE INDEX idx_durable_jobs_claim_run_work
    ON durable_jobs (priority DESC, created_at, id)
    WHERE job_class = 'RUN_WORK' AND state = 'AVAILABLE' AND cancel_epoch IS NULL;

CREATE INDEX idx_durable_jobs_claim_control
    ON durable_jobs (priority DESC, created_at, id)
    WHERE job_class = 'CONTROL' AND state = 'AVAILABLE';

-- Used by the CANCEL_RUN_COORDINATOR job's own sweep (fence+cancel every
-- non-terminal RUN_WORK job of one Run in a single UPDATE).
CREATE INDEX idx_durable_jobs_run_id ON durable_jobs (run_id) WHERE run_id IS NOT NULL;
