-- V12-30 (docs/design/15-v12-ui-operations.md, UX-32, ADR-062): running a worktree's application for manual QA.
--
-- run_app_profiles: per repository, how to run its app (processes as argv, ports, environment, secret names, preview URL, idle timeout),
-- a JSON document validated by internal/app/runapp.
--
-- run_app_sessions: one run of a profile in a repository workspace. aw serve only records requests (REQUESTED, STOP_REQUESTED); aw worker
-- runs the processes (RUNNING) and ends the session (STOPPED, FAILED). One active session per repository (its ports are fixed), enforced
-- by the partial unique index.
CREATE TABLE run_app_profiles (
    repository_id TEXT PRIMARY KEY REFERENCES repositories(id),
    project_id TEXT NOT NULL REFERENCES projects(id),
    document TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    updated_by TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE run_app_sessions (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    repository_workspace_id TEXT NOT NULL REFERENCES repository_workspaces(id),
    state TEXT NOT NULL CHECK (state IN ('REQUESTED', 'RUNNING', 'STOP_REQUESTED', 'STOPPED', 'FAILED')),
    profile_version INTEGER NOT NULL,
    requested_by TEXT NOT NULL,
    worker_id TEXT NOT NULL DEFAULT '',
    deadline_at TEXT NOT NULL,
    started_at TEXT,
    ended_at TEXT,
    problem TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_run_app_sessions_active ON run_app_sessions(repository_id)
    WHERE state IN ('REQUESTED', 'RUNNING', 'STOP_REQUESTED');
CREATE INDEX idx_run_app_sessions_workspace ON run_app_sessions(repository_workspace_id, created_at);
