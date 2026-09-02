-- The immutable AdapterBuildVersion registry (docs/design/04-v2-definition-plane.md
-- V2-07A, ADR-022). This table is deliberately separate from
-- definitions/definition_versions (0004_shared_definitions.sql):
-- AdapterBuildVersion is not a DefinitionKind, is never authored through
-- POST /definitions/{kind}/publish, and carries no project_id — it is
-- installation-scoped, per go-core-spec.md's own command table. No code
-- in this repo ever issues an UPDATE or DELETE against this table: a
-- row's id is a content-address (a hash of its own candidate tuple), so
-- a genuinely different measured build always gets its own new row
-- rather than overwriting an existing one (ADR-022's "run đang chạy
-- không bao giờ được tự động repin" — nothing here can repin anything,
-- since nothing here ever changes an existing row).
CREATE TABLE adapter_build_versions (
    id TEXT PRIMARY KEY,
    provider_key TEXT NOT NULL,
    executable_path TEXT NOT NULL,
    executable_content_hash TEXT NOT NULL,
    protocol_version TEXT NOT NULL,
    capability_manifest TEXT NOT NULL,
    capability_manifest_hash TEXT NOT NULL,
    os TEXT NOT NULL,
    toolchain TEXT NOT NULL,
    config_identity TEXT NOT NULL,
    registered_by TEXT NOT NULL,
    registered_at TEXT NOT NULL
);

CREATE INDEX adapter_build_versions_provider_idx ON adapter_build_versions(provider_key);

-- The per-installation candidate-token signing key ADR-022 requires
-- ("sinh lúc khởi tạo, giữ ngoài repository và xoay được"). Exactly one
-- row (id=1) is ever active. Rotation replaces it in place: every
-- outstanding token signed under the old key instantly fails
-- verification, which is the intended behavior, not a bug to work
-- around.
CREATE TABLE adapter_build_signing_keys (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    secret_key_hex TEXT NOT NULL,
    created_at TEXT NOT NULL
);
