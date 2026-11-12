-- V12-31 (ADR-063): the QA checklist an approver filled in when deciding a checklist APPROVAL — the gate's QA_CHECKLIST record.
-- One row per decided approval request; items_json holds every item with its result, note and attached message ids.
CREATE TABLE approval_checklists (
    approval_request_id TEXT PRIMARY KEY REFERENCES approval_requests(id),
    project_id TEXT NOT NULL REFERENCES projects(id),
    run_id TEXT NOT NULL REFERENCES workflow_runs(id),
    node_run_id TEXT NOT NULL REFERENCES node_runs(id),
    work_item_id TEXT NOT NULL REFERENCES work_items(id),
    verdict TEXT NOT NULL CHECK (verdict IN ('PASSED', 'FAILED')),
    items_json TEXT NOT NULL,
    decided_by TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX approval_checklists_work_item ON approval_checklists(work_item_id);
