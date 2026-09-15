-- attachment_prepare_claims: V6-07A's own durable "prepare claim" (
-- docs/design/08-v6-api-projections.md V6-07A) — the upload-side sibling of
-- V5-14's artifact_locator_purge_claims (migration 33), same shape of
-- problem (a durable claim that fences a specific piece of content-addressed
-- state against concurrent/crashed operations) but for the OPPOSITE
-- direction: claiming BEFORE a new attachment's own content is ever Put,
-- not before an existing Locator's content is deleted.
--
-- upload_id is deterministic (internal/app/message.deterministicUploadID),
-- never randomly minted: it is derived from EXACTLY the same
-- (actor, scope, idempotency_key, command_type) tuple a command receipt is
-- itself keyed by, so a genuine HTTP retry of the identical
-- AppendConversationAttachment call (same Idempotency-Key) always
-- recomputes the SAME upload_id and finds/resumes its own prior claim here,
-- while two independent callers that merely happen to upload byte-identical
-- content (different Idempotency-Key) always get two DIFFERENT upload_ids —
-- never colliding just because their raw bytes match (that's what
-- ArtifactStore's own content-addressed Put already dedupes, one layer
-- down).
--
-- actor/idempotency_key are stored (never used to forge a write — only to
-- let a later read-only pass, e.g. ResumeOrCleanExpiredAttachmentClaims,
-- ask "did the ORIGINAL caller's own receipt already land?" via a plain
-- tx.Receipts().Load call) so an abandoned claim can be resolved without
-- ever needing to reconstruct the original HTTP request.
--
-- declared_sha256 is the caller-declared digest this claim was FIRST
-- created with — a resumed retry's own freshly declared digest is compared
-- against this stored value before anything is ever reused (a mismatch
-- here, same upload_id/different digest, is a real conflict: the
-- Idempotency-Key was reused for a semantically different upload, never
-- silently resolved).
--
-- state is 'SPOOLING' (claimed, no verified blob yet) or 'BLOB_READY'
-- (ArtifactStore.Put+Verify already succeeded for this exact declared
-- digest — locator/actual_sha256/size are populated). There is no terminal
-- "DONE" state: once the owning AppendConversationAttachment call's own
-- serialized-write transaction commits (Artifact metadata + Message ref +
-- event + receipt, atomically), this row is deleted (released) — the
-- receipt row itself is the durable, permanent proof of completion from
-- that point on, mirroring ReleaseArtifactLocatorClaim's own
-- delete-on-release discipline exactly.
--
-- claim_owner/claimed_at/version together are this row's own lease: a
-- crashed attempt's claim can be safely taken over (CAS on version, bumping
-- claim_owner/claimed_at) once claimed_at is stale, the same
-- optimistic-CAS-fenced "steal an abandoned lease" discipline write_leases
-- already uses elsewhere in this codebase, scaled down to this table's own
-- single-row-per-claim shape (no separate fence-token column needed: this
-- row's own PRIMARY KEY plus version is already the fence).
CREATE TABLE attachment_prepare_claims (
    upload_id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    work_item_id TEXT NOT NULL REFERENCES work_items(id),
    attempt_id TEXT REFERENCES execution_attempts(id),
    actor TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    role TEXT NOT NULL,
    content_type TEXT NOT NULL,
    sensitivity TEXT NOT NULL CHECK (sensitivity IN ('PUBLIC', 'SENSITIVE', 'SECRET')),
    retention_class TEXT NOT NULL CHECK (retention_class IN ('RAW_OUTPUT_TEMP', 'CANONICAL_CONTEXT')),
    declared_sha256 TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('SPOOLING', 'BLOB_READY')),
    locator TEXT,
    actual_sha256 TEXT,
    size INTEGER,
    claim_owner TEXT NOT NULL,
    claimed_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    -- locator/actual_sha256/size are set together, exactly when state
    -- transitions to BLOB_READY — never partially populated.
    CHECK (
        (state = 'SPOOLING' AND locator IS NULL AND actual_sha256 IS NULL AND size IS NULL)
        OR
        (state = 'BLOB_READY' AND locator IS NOT NULL AND actual_sha256 IS NOT NULL AND size IS NOT NULL)
    )
);

-- ResumeOrCleanExpiredAttachmentClaims' own candidate scan: every claim
-- whose claimed_at is old enough that its owner is presumed dead.
CREATE INDEX idx_attachment_prepare_claims_claimed_at ON attachment_prepare_claims(claimed_at);
