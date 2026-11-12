-- V12-04 (docs/design/15-v12-ui-operations.md, ADR-047): two more blocker types. ADAPTER_PROBE_FAILED is the admission blocker
-- for a provider CLI that could not be measured at all (not a drift: `claude.exe --version` exiting 0xC0000142 on a worker that
-- lost its console used to be reported as ADAPTER_BUILD_DRIFT). PROVIDER_LOGIN_REQUIRED blocks an AGENT node whose provider CLI
-- is not signed in, instead of failing the run. Same rebuild as 0055; nothing REFERENCES blockers.
CREATE TABLE blockers_new (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    work_item_id TEXT NOT NULL REFERENCES work_items(id),
    type TEXT NOT NULL CHECK (type IN (
        'RUN_CANCELLED', 'RUN_FAILED', 'COMPLETION_POLICY_FAILED', 'SCOPE_EXPANSION_REQUIRED',
        'ISOLATION_ENFORCEMENT_UNAVAILABLE', 'ADAPTER_BUILD_DRIFT',
        'CAPABILITY_REQUIREMENT_UNSATISFIED', 'WRITE_CAPABILITY_OR_GRANT_MISSING', 'RELEASE_FAILED',
        'PROVIDER_QUOTA_EXCEEDED', 'ADAPTER_PROBE_FAILED', 'PROVIDER_LOGIN_REQUIRED'
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

INSERT INTO blockers_new (
    id, project_id, work_item_id, type, state, source_run_id, source_node_run_id, source_attempt_id,
    reason, opened_at, resolved_at, resolved_by, resolution_note, decision_artifact_id, version
)
SELECT
    id, project_id, work_item_id, type, state, source_run_id, source_node_run_id, source_attempt_id,
    reason, opened_at, resolved_at, resolved_by, resolution_note, decision_artifact_id, version
FROM blockers;

DROP TABLE blockers;

ALTER TABLE blockers_new RENAME TO blockers;

CREATE INDEX idx_blockers_work_item ON blockers(work_item_id, state);
