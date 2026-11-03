-- V12-23 (docs/design/15-v12-ui-operations.md, UX-35, ADR-057): a spending cap for a batch of work — a root work item and every task
-- under it. The figures compared with it are the provider CLI's own reported cost (USAGE_REPORTED agent events). One row per root work
-- item that has a cap; no row = no cap. Going over warns on the cost page and stops the family queue from starting another task.
CREATE TABLE work_item_budgets (
    work_item_id TEXT PRIMARY KEY REFERENCES work_items(id),
    project_id TEXT NOT NULL REFERENCES projects(id),
    budget_usd REAL NOT NULL CHECK (budget_usd > 0),
    version INTEGER NOT NULL CHECK (version > 0),
    updated_by TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX idx_work_item_budgets_project ON work_item_budgets(project_id);
