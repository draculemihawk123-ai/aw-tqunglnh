-- Shared Definition/Version tables for the 8 DefinitionKinds that do not
-- have their own dedicated tables (docs/design/04-v2-definition-plane.md
-- V2-02): Block, Skill, Layer, Engineering Pack, Agent Profile, Command,
-- Gate, Policy. WORKFLOW is deliberately excluded from the kind CHECK
-- constraint below — it keeps using workflow_definitions/workflow_versions
-- (from 0001_initial_schema.sql) untouched, so this migration never
-- touches workflow_runs.workflow_version_id's existing foreign key or any
-- already-published workflow data. Application code never sees this as
-- two persistence layouts: ports.DefinitionPublisher is the one entry
-- point, routing to whichever table pair a Kind actually uses.
CREATE TABLE definitions (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN (
        'BLOCK', 'SKILL', 'LAYER', 'ENGINEERING_PACK',
        'AGENT_PROFILE', 'COMMAND', 'GATE', 'POLICY'
    )),
    project_id TEXT REFERENCES projects(id),
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('DRAFT', 'ACTIVE', 'ARCHIVED')),
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX definitions_kind_idx ON definitions(kind);
CREATE INDEX definitions_project_idx ON definitions(project_id);

-- definition_versions has no kind column of its own: a version's kind is
-- always the same as its definition's (enforced at the application layer
-- when publishing, since SQLite cannot express "match a value in the
-- parent row" as a declarative FK/CHECK constraint) — deriving it via the
-- definition_id foreign key instead of duplicating it here means it can
-- never drift out of sync with its own definition.
CREATE TABLE definition_versions (
    id TEXT PRIMARY KEY,
    definition_id TEXT NOT NULL REFERENCES definitions(id),
    version_no INTEGER NOT NULL CHECK (version_no > 0),
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    canonical_source TEXT NOT NULL,
    source_hash TEXT NOT NULL,
    compiled_snapshot TEXT NOT NULL,
    compiled_hash TEXT NOT NULL,
    dependency_manifest TEXT NOT NULL,
    published_by TEXT NOT NULL,
    published_at TEXT NOT NULL,
    UNIQUE (definition_id, version_no),
    UNIQUE (definition_id, compiled_hash)
);
