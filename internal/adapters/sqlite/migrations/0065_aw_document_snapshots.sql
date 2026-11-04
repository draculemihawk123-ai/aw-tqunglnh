-- V12-33 (ADR-065): the latest copy, kept by aw as an artifact, of each per-task document a worktree holds in its aw-private
-- directory (.aw/features/...), so releasing the worktree does not lose it. One row per (family, repository, path).
CREATE TABLE aw_document_snapshots (
    family_id TEXT NOT NULL REFERENCES task_families(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    path TEXT NOT NULL,
    project_id TEXT NOT NULL REFERENCES projects(id),
    artifact_id TEXT NOT NULL REFERENCES artifacts(id),
    sha256 TEXT NOT NULL,
    size INTEGER NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (family_id, repository_id, path)
);
