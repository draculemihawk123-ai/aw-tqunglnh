-- blockers: the durable, typed reason a WorkItem sits BLOCKED (V4-12C,
-- docs/design/06-v4-runtime-engine.md; ADR-020: "Admission blocker được lưu
-- thành một row `blockers` với type bằng chính TerminationReason"). A
-- WorkItem stays BLOCKED for as long as it has at least one row here with
-- state = 'OPEN'; CancelWorkItem/ResolveWorkItemBlocker are the only two
-- commands with authority to change that (transitioning OPEN -> RESOLVED or
-- OPEN -> WAIVED), and the scope-expansion reconcile flow (V4-12A/V4-12C)
-- transitions its own SCOPE_EXPANSION_REQUIRED row OPEN -> RESOLVED the
-- moment a reactivated NodeRun activation is created.
--
-- type mirrors runtime.TerminationReason's own literal values (this table
-- deliberately has no FK into any runtime-owned enum table — TerminationReason
-- lives in Go as internal/domain/runtime's own closed type, work_items'
-- package cannot import it without a cycle, see blocker.go's own doc
-- comment) — the CHECK constraint below is this table's own closed-set
-- enforcement, kept in lockstep with runtime.knownTerminationReasons'
-- blocker-relevant subset by hand.
--
-- source_run_id/source_node_run_id/source_attempt_id are nullable: a
-- RUN_CANCELLED blocker always carries source_run_id; a
-- SCOPE_EXPANSION_REQUIRED blocker always carries all three; a blocker a
-- future task creates from a context with no such runtime origin (e.g. an
-- admission-reason blocker raised before any Attempt row exists) leaves
-- them blank.
--
-- decision_artifact_id is populated only for a WAIVED resolution — the
-- required, immutable evidence record for that decision (ADR-020:
-- "WAIVED cần actor, reason, policy grant và một DecisionArtifact").
CREATE TABLE blockers (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    work_item_id TEXT NOT NULL REFERENCES work_items(id),
    type TEXT NOT NULL CHECK (type IN (
        'RUN_CANCELLED', 'COMPLETION_POLICY_FAILED', 'SCOPE_EXPANSION_REQUIRED',
        'ISOLATION_ENFORCEMENT_UNAVAILABLE', 'ADAPTER_BUILD_DRIFT',
        'CAPABILITY_REQUIREMENT_UNSATISFIED', 'WRITE_CAPABILITY_OR_GRANT_MISSING'
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

CREATE INDEX idx_blockers_work_item ON blockers(work_item_id, state);
