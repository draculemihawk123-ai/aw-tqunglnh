-- V9-06 (docs/design/12-v9-harness-alignment.md V9-06, ADR-033, gap G6): widen
-- blockers.type's own CHECK allow-list to include RUN_FAILED — the blocker
-- internal/app/runtime/completion.go's transitionRunToFailedTx now opens when a
-- WorkflowRun ends FAILED, so the WorkItem moves ACTIVE -> BLOCKED and an
-- operator can resolve it (ResolveWorkItemBlocker, mode RESOLVED; never
-- WAIVED) and start the next Run on the SAME WorkItem. Before this migration a
-- failed Run left the WorkItem ACTIVE, which StartWorkflowRun refuses, and the
-- only way to try again was to cancel the WorkItem and recreate it.
--
-- SQLite has no ALTER TABLE ... DROP/MODIFY CONSTRAINT, so widening a CHECK
-- constraint requires the full create-copy-drop-rename rebuild
-- 0032_artifacts_purged_state.sql's own doc comment describes. Like
-- 0043_release_set_local_commit_no_changes.sql, and unlike 0025/0032/0034, this
-- migration does NOT need PRAGMA foreign_keys=OFF: no other table REFERENCES
-- blockers (it only has outgoing foreign keys — project_id, work_item_id,
-- source_run_id, source_node_run_id, source_attempt_id, decision_artifact_id),
-- so DROP TABLE has no referencing row to trip over. 0014's own
-- readiness_environment_blockers is a different table with its own
-- type = 'BASELINE_ENVIRONMENT_ERROR' CHECK and is untouched. No trigger is
-- defined on blockers.
--
-- Every column and index blockers has ever had (0024_work_item_blockers.sql's
-- own fifteen columns and its one index) is carried forward unchanged except
-- type's widened CHECK; the explicit column list in the INSERT below is the
-- authoritative "nothing silently dropped" proof. Existing rows keep their
-- ids, states, timestamps and versions byte for byte.
CREATE TABLE blockers_new (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    work_item_id TEXT NOT NULL REFERENCES work_items(id),
    type TEXT NOT NULL CHECK (type IN (
        'RUN_CANCELLED', 'RUN_FAILED', 'COMPLETION_POLICY_FAILED', 'SCOPE_EXPANSION_REQUIRED',
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
