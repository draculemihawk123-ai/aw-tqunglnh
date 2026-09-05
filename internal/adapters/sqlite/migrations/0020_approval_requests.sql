-- V4-09 APPROVAL semantics
-- (docs/design/06-v4-runtime-engine.md; HE-14-S03, GC-INV-11, HE-08-M08).
--
-- approval_requests is the durable authority for one APPROVAL NodeRun
-- activation's own pending decision — at most one per node_run_id (a fresh
-- reactivation, V4-07's own "rework tạo activation mới", always gets its
-- own fresh request, never a resurrected one). authorized_roles_json and
-- requested_evidence_kinds_json are the exact values this request's own
-- compiled ApprovalNodeConfig already pinned at request time (never
-- re-read from the WorkflowVersion again later). escalation_outcome is
-- required (V4-09's own correction to ApprovalNodeConfig, node_config.go)
-- since timeout_seconds is itself already required — a due_at with
-- nowhere to route once it actually fires would reintroduce the exact
-- unbounded wait requiring a timeout exists to prevent, so every request
-- row always has both.
--
-- decided_by/decided_role/decided_outcome/reason/decided_at are populated
-- only once, atomically with the CAS to DECIDED (HE-08-M08's own "MUST
-- ghi actor, cause, previous/new state, time và evidence/approval refs") —
-- never independently, never for ESCALATED (a timeout has no deciding
-- actor by definition).
--
-- durable_jobs only ever wakes processing up at this row's own due_at (a
-- TIMER job) — it is never itself the authority over whether a request is
-- still PENDING; only a fenced CAS on approval_requests.state/version is,
-- the identical GC-INV-31-style discipline V4-08's own wait_registrations
-- already established for a different node type.
CREATE TABLE approval_requests (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    run_id TEXT NOT NULL REFERENCES workflow_runs(id),
    node_run_id TEXT NOT NULL REFERENCES node_runs(id),
    node_key TEXT NOT NULL,
    authorized_roles_json TEXT NOT NULL,
    requested_evidence_kinds_json TEXT NOT NULL DEFAULT '[]',
    due_at TEXT NOT NULL,
    escalation_outcome TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('PENDING','DECIDED','ESCALATED','CANCELLED')),
    decided_by TEXT,
    decided_role TEXT,
    decided_outcome TEXT,
    reason TEXT,
    decided_at TEXT,
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (node_run_id)
);

CREATE INDEX approval_requests_due_idx
    ON approval_requests (due_at)
    WHERE state = 'PENDING';
