-- V4-10 FORK branch tokens
-- (docs/design/06-v4-runtime-engine.md; HE-14-M09).
--
-- Correction to branch_tokens (V4-01, migration 0016) found while building
-- this task's own real consumer: the original UNIQUE(run_id, fork_key,
-- branch_key) assumed a FORK node is only ever activated once per Run —
-- true when V4-01 designed it, false now that V4-07's own business-rework
-- cycle budget can legitimately re-enter the SAME FORK node key more than
-- once within one Run. A token must be scoped to the exact FORK
-- ACTIVATION (its own NodeRun) that spawned it, not merely its NodeKey.
-- fork_node_run_id is that activation's own NodeRunID; fork_key is kept
-- alongside it purely for query/audit (unchanged meaning). branch_tokens
-- has never been written to by any real application code (only its own
-- V4-01 unit tests exercised CreateBranchToken, against disposable test
-- databases) — safe to DROP/CREATE outright, the same precedent V4-01's
-- own migration 0016 already used for execution_attempts.
DROP TABLE branch_tokens;

CREATE TABLE branch_tokens (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES workflow_runs(id),
    fork_node_run_id TEXT NOT NULL REFERENCES node_runs(id),
    fork_key TEXT NOT NULL,
    branch_key TEXT NOT NULL,
    current_node_key TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('ACTIVE','SUCCEEDED','FAILED','CANCELLED')),
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (fork_node_run_id, branch_key)
);

-- node_runs gains branch_token_id (V4-10): nullable — nil for every
-- NodeRun outside a FORK's own branch (the common case, including the
-- FORK's own NodeRun itself and anything reached before/after one). A
-- non-nil value identifies which BranchToken this exact NodeRun activation
-- belongs to; advanceRunTx propagates it unchanged across every ordinary
-- hop within a branch, alongside a CAS on that same token's own
-- current_node_key, so both stay in lock-step (confirmed with the user
-- before writing this task's code).
ALTER TABLE node_runs ADD COLUMN branch_token_id TEXT REFERENCES branch_tokens(id);
