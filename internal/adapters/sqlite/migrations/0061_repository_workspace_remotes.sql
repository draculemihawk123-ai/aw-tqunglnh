-- V12-28 (docs/design/15-v12-ui-operations.md, UX-01, UX-39, ADR-060): the remote branch aw pushed a repository workspace's family
-- branch to, and the pull request it opened for it. One row per repository workspace (a family branch has at most one remote branch
-- and one open pull request at a time). The row is what lets aw tell "a branch aw created" (push again: fast-forward only) from "a
-- branch that was already there" (refused: aw never overwrites a branch it did not create).
CREATE TABLE repository_workspace_remotes (
    repository_workspace_id TEXT PRIMARY KEY REFERENCES repository_workspaces(id),
    project_id TEXT NOT NULL REFERENCES projects(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    remote TEXT NOT NULL,
    remote_branch TEXT NOT NULL,
    pushed_revision TEXT NOT NULL DEFAULT '',
    pull_request_number INTEGER NOT NULL DEFAULT 0,
    pull_request_url TEXT NOT NULL DEFAULT '',
    base_branch TEXT NOT NULL DEFAULT '',
    merged_revision TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL CHECK (version > 0),
    updated_by TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (repository_id, remote, remote_branch)
);
