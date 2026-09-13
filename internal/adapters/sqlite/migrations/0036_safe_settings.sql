-- safe_settings: V6-10G's own versioned, singleton safe-settings row
-- (docs/design/08-v6-api-projections.md V6-10G; ADR-016, ADR-017, ADR-025,
-- ADR-028) — a SECOND configuration layer that sits on top of, and never
-- mutates, internal/app/config.Config's own immutable-per-process startup
-- config: a closed, 7-field allowlist (managed workspace/artifact roots,
-- evidence/raw-output retention, process output limit, provider executable
-- path, provider default model, provider credential-reference ID) an
-- operator may replace WHILE THE SERVER IS RUNNING, that only ever takes
-- effect on the NEXT restart (never hot-reloaded).
--
-- Exactly one row ever exists (id is CHECKed to the literal 'singleton',
-- mirroring recovery_reaper_state's own established singleton-row
-- discipline, migration 0026) — there is no "row does not exist yet" state
-- a caller needs to handle separately from "nothing has been configured";
-- the INSERT below seeds that "never configured" state directly, as the
-- zero-value desired document (every field empty/zero), at version 1.
--
-- desired_json is the FULL desired document (V6-10G's own "Store full
-- desired document + version": every update replaces the whole document
-- together, never a per-field patch) — internal/domain/safesettings.SafeSettings'
-- own JSON encoding (see that package's own MarshalJSON/UnmarshalJSON doc
-- comment for why evidenceRetention is a Go-syntax duration string, not a
-- raw nanosecond integer). updated_by is NULL for the seed row (no operator
-- ever wrote it) and always the acting Command.Actor thereafter.
CREATE TABLE safe_settings (
    id TEXT PRIMARY KEY CHECK (id = 'singleton'),
    desired_json TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    updated_at TEXT NOT NULL,
    updated_by TEXT
);

INSERT INTO safe_settings (id, desired_json, version, updated_at, updated_by)
VALUES (
    'singleton',
    '{"managedWorkspaceRoot":"","managedArtifactRoot":"","evidenceRetention":"0s","processOutputLimit":0,"providerExecutablePath":"","providerDefaultModel":"","providerCredentialRef":""}',
    1,
    '1970-01-01T00:00:00Z',
    NULL
);
