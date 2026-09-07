-- artifacts: the durable metadata row V5-01 adds on top of the existing V1
-- content-addressed ArtifactStore (0001_initial_schema.sql never created
-- this table — the filesystem store has no database presence of its own
-- until this migration). locator/content_hash/size/media_type/sensitivity/
-- redacted mirror ports.ArtifactRef's own informational fields exactly
-- (internal/app/ports/artifact.go) so a row here can always be turned back
-- into a ports.ArtifactRef for Open/Verify without ever parsing locator
-- itself (docs/architecture/04-go-core-spec.md §4.6's own "ArtifactRef"
-- sketch, adapted to this codebase's existing field names).
--
-- id is a fresh, platform-minted identity (internal/app/idsource), never
-- content-derived: the same underlying bytes MAY back more than one row
-- here (ArtifactStore's own Put already dedupes the bytes themselves on
-- disk), so content_hash is deliberately NOT unique.
--
-- retention_class/attach_state/hold implement ADR-017 and
-- docs/architecture/04-go-core-spec.md §11.2's own "Artifact MAY được lưu
-- với nhãn untrusted/orphan" — see internal/domain/artifact's own doc
-- comments for what each value means. expires_at is nullable: NULL for
-- retention_class CANONICAL_CONTEXT (no blanket TTL, ever); always set for
-- RAW_OUTPUT_TEMP (internal/domain/artifact.ComputeExpiresAt's own 7-day
-- default).
--
-- Nothing in V5-01's own scope ever deletes a row here — a future
-- retention sweeper (V5-14) is the only intended writer of a DELETE
-- against this table, and only for a still-ORPHAN, not-held row past its
-- own expires_at (ADR-017: "Sweeper chỉ xóa owned artifact đúng retention
-- class, không còn hold và đã qua integrity/state check").
CREATE TABLE artifacts (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    locator TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    size INTEGER NOT NULL CHECK (size >= 0),
    media_type TEXT NOT NULL,
    sensitivity TEXT NOT NULL CHECK (sensitivity IN ('PUBLIC', 'SENSITIVE', 'SECRET')),
    redacted INTEGER NOT NULL CHECK (redacted IN (0, 1)),
    retention_class TEXT NOT NULL CHECK (retention_class IN ('RAW_OUTPUT_TEMP', 'CANONICAL_CONTEXT')),
    attach_state TEXT NOT NULL CHECK (attach_state IN ('ORPHAN', 'ATTACHED')),
    hold INTEGER NOT NULL CHECK (hold IN (0, 1)),
    expires_at TEXT,
    created_at TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0)
);

CREATE INDEX idx_artifacts_project ON artifacts(project_id);
CREATE INDEX idx_artifacts_orphan_created ON artifacts(attach_state, created_at);
