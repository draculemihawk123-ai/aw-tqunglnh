-- V5-14 (docs/design/07-v5-execution-evidence.md, ADR-017, docs/architecture/
-- 04-go-core-spec.md §19): widen artifacts.attach_state's own CHECK
-- allow-list to include PURGED — the retention sweeper's own terminal state
-- for a row whose underlying ports.ArtifactStore content has actually been
-- deleted (internal/domain/artifact.Purged). The row itself, its own
-- content_hash/locator/created_at/version, is NEVER deleted — only ever
-- reached from ORPHAN (0027_artifacts.sql's own "Sweeper chỉ xóa
-- owned artifact... đã qua integrity/state check" — this task's own scope
-- never purges a currently-ATTACHED row).
--
-- SQLite has no ALTER TABLE ... DROP/MODIFY CONSTRAINT, so widening a CHECK
-- constraint requires the full create-copy-drop-rename rebuild
-- 0025_recovery_reaper_job_class.sql's own doc comment already describes in
-- detail for the identical situation — and, like that migration, this one
-- needs PRAGMA foreign_keys=OFF for its own duration (see
-- migrationsRequiringForeignKeysOff in migrations.go): messages.
-- content_artifact_id REFERENCES artifacts(id) (0028_messages.sql), and by
-- the time this migration runs against any real database, real message rows
-- referencing real artifact rows already exist.
--
-- Every column and index artifacts has ever had (0027_artifacts.sql's own
-- fourteen columns and two indexes) is carried forward unchanged except
-- attach_state's own widened CHECK; the explicit column list in the INSERT
-- below is the authoritative "nothing silently dropped" proof.
CREATE TABLE artifacts_new (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    locator TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    size INTEGER NOT NULL CHECK (size >= 0),
    media_type TEXT NOT NULL,
    sensitivity TEXT NOT NULL CHECK (sensitivity IN ('PUBLIC', 'SENSITIVE', 'SECRET')),
    redacted INTEGER NOT NULL CHECK (redacted IN (0, 1)),
    retention_class TEXT NOT NULL CHECK (retention_class IN ('RAW_OUTPUT_TEMP', 'CANONICAL_CONTEXT')),
    attach_state TEXT NOT NULL CHECK (attach_state IN ('ORPHAN', 'ATTACHED', 'PURGED')),
    hold INTEGER NOT NULL CHECK (hold IN (0, 1)),
    expires_at TEXT,
    created_at TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0)
);

INSERT INTO artifacts_new (
    id, project_id, locator, content_hash, size, media_type, sensitivity, redacted,
    retention_class, attach_state, hold, expires_at, created_at, version
)
SELECT
    id, project_id, locator, content_hash, size, media_type, sensitivity, redacted,
    retention_class, attach_state, hold, expires_at, created_at, version
FROM artifacts;

DROP TABLE artifacts;

ALTER TABLE artifacts_new RENAME TO artifacts;

CREATE INDEX idx_artifacts_project ON artifacts(project_id);
CREATE INDEX idx_artifacts_orphan_created ON artifacts(attach_state, created_at);

-- idx_artifacts_locator: the sweeper's own group-eligibility/refcount query
-- (V5-14, "liveness/refcount khi nhiều row dùng cùng content-addressed
-- locator") reads every row sharing a Locator, across every Project — this
-- index is what makes that a real index scan rather than a full table scan.
CREATE INDEX idx_artifacts_locator ON artifacts(locator);
