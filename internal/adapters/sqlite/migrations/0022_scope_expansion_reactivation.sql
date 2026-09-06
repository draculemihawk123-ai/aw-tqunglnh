-- V4-12A: attempt_scope_expansion_origins is the durable link between one
-- BLOCKED ExecutionAttempt (TerminationReason=SCOPE_EXPANSION_REQUIRED) and
-- the real work.ScopeExpansionRequest raised on its behalf — confirmed with
-- the user before writing this task's own runtime code, correcting an
-- initial design that would have backfilled request_id AFTER the real
-- request was created (a crash between those two writes could leave an
-- approved request with no way back to the Attempt that needed it).
-- request_id is instead RESERVED — generated and persisted here, in the
-- SAME fenced transaction that CASes the Attempt/NodeRun to BLOCKED —
-- before the REQUEST_SCOPE_EXPANSION job ever calls the real
-- work.RequestScopeExpansion command with that exact, pre-minted ID.
--
-- reactivated_node_run_id is UNIQUE and starts NULL: the fenced CAS a
-- concurrent/duplicate-delivered SCOPE_EXPANSION_RECONCILE job uses to
-- guarantee AT MOST ONE reactivation NodeRun is ever created per origin,
-- no matter how many times that job is (re)claimed.
--
-- reconcile_status/poll_generation belong to the SCOPE_EXPANSION_RECONCILE
-- job's own self-rescheduling state machine (internal/app/runtime): PENDING
-- while still waiting on a human decision or on WorkspaceSet provisioning;
-- REACTIVATED once the new NodeRun activation exists; REJECTED when the
-- underlying request was REJECTED/WITHDRAWN (the Attempt/NodeRun/Run stay
-- BLOCKED — Alpha has no automatic resume path, only CancelRun); and
-- NEEDS_RECOVERY when the family's own WorkspaceSet reached a terminal,
-- non-READY state (BLOCKED/RELEASING/RELEASED) this job must never poll
-- against forever. poll_generation is CAS'd alongside every successor-job
-- enqueue so a duplicate delivery of the SAME reconcile attempt can never
-- mint two successor jobs.
CREATE TABLE attempt_scope_expansion_origins (
    attempt_id TEXT PRIMARY KEY REFERENCES execution_attempts(id),
    node_run_id TEXT NOT NULL REFERENCES node_runs(id),
    run_id TEXT NOT NULL REFERENCES workflow_runs(id),
    work_item_id TEXT NOT NULL,
    family_id TEXT NOT NULL,
    request_id TEXT NOT NULL UNIQUE,
    proposal_json TEXT NOT NULL,
    proposal_hash TEXT NOT NULL,
    reactivated_node_run_id TEXT UNIQUE REFERENCES node_runs(id),
    reconcile_status TEXT NOT NULL CHECK (reconcile_status IN ('PENDING', 'REACTIVATED', 'REJECTED', 'NEEDS_RECOVERY')) DEFAULT 'PENDING',
    poll_generation INTEGER NOT NULL DEFAULT 0,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_attempt_scope_expansion_origins_run_id ON attempt_scope_expansion_origins (run_id);

-- reactivation_reason: "" for every ordinary activation (the default,
-- covering every NodeRun this codebase has ever created before this task),
-- set to "SCOPE_EXPANDED" only for a NodeRun this task's own reactivation
-- logic creates. A plain string, not a foreign-key/enum column — V4-12A is
-- the only real producer today, and a second reactivation reason (if one is
-- ever needed later) can extend this same free-text convention without a
-- schema migration of its own.
ALTER TABLE node_runs ADD COLUMN reactivation_reason TEXT NOT NULL DEFAULT '';
