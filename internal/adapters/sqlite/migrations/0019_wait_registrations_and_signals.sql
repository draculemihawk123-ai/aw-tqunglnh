-- V4-08 WAIT persistence and signal semantics
-- (docs/design/06-v4-runtime-engine.md; docs/architecture/04-go-core-spec.md
-- GC-INV-31).
--
-- wait_registrations is the durable authority for one WAIT NodeRun
-- activation's own pending completion — at most one per node_run_id (a
-- fresh reactivation, V4-07's own "rework tạo activation mới", always gets
-- its own fresh registration, never a resurrected one). completion_outcome/
-- timeout_outcome are the exact outcome strings this registration's own
-- compiled WorkflowVersion already pinned at registration time
-- (workflow.WaitNodeConfig.CompletionOutcome/TimeoutOutcome) — SignalWait
-- and the timer job never choose an outcome themselves; they only race a
-- fenced CAS on this row's own state/version, and the winner routes using
-- whichever of these two columns applies. due_at is NULL exactly when
-- timeout_outcome is '' — nothing to wake a timer job for.
--
-- wait_signals is an immutable, append-only record of every real external
-- signal delivery — never mutated or deleted once inserted. signal_key is
-- the caller-supplied external identity, deliberately separate from
-- ports.Command's own idempotency_key (that protects one command call and
-- its own receipt; signal_key protects the same real-world event reported
-- through two different command invocations) — unique per
-- (wait_registration_id, signal_key), so a duplicate delivery of the same
-- external event is recognized and never consumes the registration twice.
--
-- durable_jobs only ever wakes processing up at a registration's own
-- due_at (a TIMER job, GC-INV-31's own "durable job chỉ đánh thức timer và
-- không phải authority của signal") — it is never itself the source of
-- truth for whether a registration is still ACTIVE; only the fenced CAS on
-- wait_registrations.state/version is. consumed_signal_id is a plain
-- (unenforced) reference, not a FOREIGN KEY, matching this schema's own
-- existing convention for optional forward-pointing ref columns (e.g.
-- execution_attempts.context_snapshot_id).
CREATE TABLE wait_registrations (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    run_id TEXT NOT NULL REFERENCES workflow_runs(id),
    node_run_id TEXT NOT NULL REFERENCES node_runs(id),
    node_key TEXT NOT NULL,
    signal_name TEXT NOT NULL DEFAULT '',
    due_at TEXT,
    completion_outcome TEXT NOT NULL,
    timeout_outcome TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL CHECK (state IN ('ACTIVE','CONSUMED','ELAPSED','TIMED_OUT','CANCELLED')),
    consumed_signal_id TEXT,
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (node_run_id)
);

CREATE INDEX wait_registrations_due_idx
    ON wait_registrations (due_at)
    WHERE state = 'ACTIVE' AND due_at IS NOT NULL;

CREATE TABLE wait_signals (
    id TEXT PRIMARY KEY,
    wait_registration_id TEXT NOT NULL REFERENCES wait_registrations(id),
    signal_key TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '',
    payload_hash TEXT NOT NULL,
    actor TEXT NOT NULL,
    received_at TEXT NOT NULL,
    UNIQUE (wait_registration_id, signal_key)
);
