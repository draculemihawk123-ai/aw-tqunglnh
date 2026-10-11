-- V11-12 (docs/design/14-v11-bugfix.md, ADR-043): a wrongly registered repository can be corrected, retired or removed, and a failed probe says why.
--
-- 1. repository_probe_attempts keeps what the probe learned about a failure, not just a public code: failure_kind (the closed ports.ProbeFailureKind
--    vocabulary), error_detail (Git's stderr, redacted and cut to 2 KiB) and error_hint (what to do, with a pasteable command). error_code and
--    error_message stay as they were; all three new columns are NULL for every existing row and for every successful probe.
--
-- 2. repositories: UNIQUE (project_id, name) becomes a partial unique index that ignores DISABLED rows, so a retired repository gives its name back and
--    the same name can be registered again. id stays unique forever. SQLite cannot drop a table constraint, so repositories is rebuilt (create-copy-drop-
--    rename) with PRAGMA foreign_keys=OFF (migrationsRequiringForeignKeysOff): more than ten tables REFERENCE repositories(id) and the DROP would be
--    refused while any such row exists. Every column is carried forward unchanged; the explicit column lists are the "nothing silently dropped" proof.
--
-- 3. deleted_repositories: the id of a repository that was deleted is never reused (events and receipts keyed by it would otherwise collide), so
--    DeleteRepository leaves a tombstone and RegisterRepository refuses an id found here.

ALTER TABLE repository_probe_attempts ADD COLUMN failure_kind TEXT;
ALTER TABLE repository_probe_attempts ADD COLUMN error_detail TEXT;
ALTER TABLE repository_probe_attempts ADD COLUMN error_hint TEXT;

CREATE TABLE repositories_new (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    name TEXT NOT NULL,
    local_path TEXT NOT NULL,
    default_ref TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('REGISTERING','PROBING','ACTIVE','BLOCKED','DISABLED')),
    last_probe_error_code TEXT,
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT INTO repositories_new (
    id, project_id, name, local_path, default_ref, status, last_probe_error_code, version, created_at, updated_at
)
SELECT
    id, project_id, name, local_path, default_ref, status, last_probe_error_code, version, created_at, updated_at
FROM repositories;

DROP TABLE repositories;

ALTER TABLE repositories_new RENAME TO repositories;

CREATE UNIQUE INDEX repositories_project_name_live_uidx ON repositories(project_id, name) WHERE status <> 'DISABLED';

CREATE TABLE deleted_repositories (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    name TEXT NOT NULL,
    local_path TEXT NOT NULL,
    reason TEXT NOT NULL,
    actor TEXT NOT NULL,
    deleted_at TEXT NOT NULL
);
