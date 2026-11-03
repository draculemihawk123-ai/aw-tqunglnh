-- V12-19 (docs/design/15-v12-ui-operations.md, UX-30, ADR-054): the family task queue. A family shares one worktree, so its tasks
-- run one after another: an operator queues several WorkItems, and the worker starts the next one when the task the queue started
-- before it is DONE and no run of the family is active (and when the task it waits for, if any, is DONE).
--
-- One row per WorkItem (it has at most one place in its family's queue). state QUEUED → STARTED (run_id) or REMOVED; a REMOVED
-- entry may be queued again (the row is updated, version + 1). problem says why the head did not start on the last try.
CREATE TABLE work_item_queue_entries (
    work_item_id TEXT PRIMARY KEY REFERENCES work_items(id),
    project_id TEXT NOT NULL REFERENCES projects(id),
    family_id TEXT NOT NULL REFERENCES task_families(id),
    position INTEGER NOT NULL,
    waits_for_work_item_id TEXT REFERENCES work_items(id),
    state TEXT NOT NULL CHECK (state IN ('QUEUED', 'STARTED', 'REMOVED')),
    run_id TEXT REFERENCES workflow_runs(id),
    problem TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_work_item_queue_family ON work_item_queue_entries(family_id, state, position);
