-- V12-25 (docs/design/15-v12-ui-operations.md, UX-13, ADR-058): per-project operator settings, one row per (project, key). The first key is
-- commit_message_template: the default commit message of an approval-bound commit, in the repository's convention (e.g. "feat: {title}").
-- A key/value row keeps the projects table and its aggregate unchanged; each value carries its own version for compare-and-swap.
CREATE TABLE project_settings (
    project_id TEXT NOT NULL REFERENCES projects(id),
    key TEXT NOT NULL CHECK (length(key) BETWEEN 1 AND 64),
    value TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    updated_by TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (project_id, key)
);
