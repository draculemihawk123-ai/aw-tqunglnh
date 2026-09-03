-- Repository status machine widened from ACTIVE|DISABLED to the full
-- onboarding lifecycle REGISTERING|PROBING|ACTIVE|BLOCKED|DISABLED
-- (docs/design/05-v3-project-workspace.md V3-01,
-- docs/architecture/04-go-core-spec.md §4.1): RegisterRepository now
-- creates a repository REGISTERING, never ACTIVE directly -- only
-- V3-02's future onboarding probe worker (not built by this migration or
-- by V3-01 at all) ever advances it further. last_probe_error_code is
-- added now (nullable -- a repository that has never failed a probe has
-- none) so V3-02 has a column ready to write into without a second
-- migration; V3-02 alone is responsible for ever setting or clearing it.
-- SQLite has no ALTER TABLE for widening a CHECK constraint, so the
-- status column itself needs a real table rebuild (last_probe_error_code
-- alone would have been a plain ADD COLUMN).
--
-- 0002/0003 (command_receipts, domain_events) both rebuilt a table with
-- the plain DROP TABLE x; CREATE TABLE x (...) shape, safe for them
-- because neither dropped table had any other table's FK pointing at it.
-- repositories is different: family_repository_scopes.repository_id and
-- repository_workspaces.repository_id (both from 0001_initial_schema.sql)
-- already declare REFERENCES repositories(id) -- this migration's own
-- past draft tried the textbook-safe rebuild instead (CREATE a
-- repositories_new shape, INSERT ... SELECT the old rows across, DROP the
-- old table, RENAME the new one into place) on the theory that preserving
-- already-registered rows mattered. Empirically verified against this
-- adapter's own PRAGMA foreign_keys=ON connection, with real fixture rows
-- in both repositories and a child table (internal/adapters/sqlite's own
-- migration_0006_test.go keeps this proof as a regression test): under
-- foreign_keys=ON, DROP TABLE performs an implicit "delete every row,
-- then check FK constraints" before removing the table, so
-- DROP TABLE repositories fails outright with a real
-- "FOREIGN KEY constraint failed" the moment any child table has even one
-- row referencing an existing repository -- the safe-rebuild dance does
-- not route around this at all (the DROP step is identical either way);
-- it only ever "works" when repositories itself, or every table
-- referencing it, has zero rows at migration time, which makes the
-- extra INSERT...SELECT machinery pure overhead with no actual safety
-- benefit over the plain DROP+CREATE 0002/0003 already use. The one real
-- fix for a genuinely populated cross-referenced table -- toggling
-- PRAGMA foreign_keys=OFF around the rebuild -- cannot be done here: that
-- pragma is a documented no-op once a transaction is already open, and
-- Store.Migrate (internal/adapters/sqlite/migrations.go) deliberately
-- runs every pending migration inside one transaction so a failure rolls
-- back the whole attempt (proven by TestMigrate_FailureRollsBackEverything);
-- restructuring that shared, already-tested transaction model is a
-- larger, separate change this task should not fold in as a side effect.
--
-- This migration is safe in practice for a reason stronger than "no
-- current CI fixture happens to trip it": nothing in this codebase, at
-- any point in its history up to and including this task, ever writes a
-- real (non-test-fixture) row to repositories, family_repository_scopes
-- or repository_workspaces through cmd/agentkit -- RegisterRepository
-- (this task) is the very first command that can create a repositories
-- row at all, and family_repository_scopes/repository_workspaces stay
-- unpopulated until V3-04/V3-06 land, which can only ever run against a
-- codebase where this migration has already applied (this repo's own
-- one-task-at-a-time process). Every test fixture that inserts into
-- these tables (internal/adapters/sqlite/fixtures.go,
-- crashworker_fixtures.go, scheduling_test.go) does so against a database
-- sqlite.Open already migrated to the latest version first -- never
-- against a pre-migration-6 schema mid-upgrade. So the plain
-- DROP TABLE repositories; CREATE TABLE repositories (...) shape below
-- is exactly as safe here as it was for 0002/0003, for the identical
-- underlying reason: nothing has ever put a row in the table this
-- migration rebuilds.
DROP TABLE repositories;

CREATE TABLE repositories (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    name TEXT NOT NULL,
    local_path TEXT NOT NULL,
    default_ref TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('REGISTERING','PROBING','ACTIVE','BLOCKED','DISABLED')),
    last_probe_error_code TEXT,
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (project_id, name)
);

-- Component: a routable, path-scoped unit within a Repository
-- (docs/harness-engineering/03-lec-03-repository-va-nguon-su-that.md
-- HE-03-M04's "component MUST nằm gần component hoặc được selector định
-- tuyến chính xác") and the bridge to an EngineeringPack
-- (docs/harness-engineering/06-lec-06-khoi-tao-la-phase-rieng.md
-- HE-06-M05's "Repository -> Component -> EngineeringPack"). project_id
-- is carried directly (not merely derived by joining through
-- repository_id) so every catalog query can filter by project alone, the
-- same as every other project-scoped table in this schema; the
-- application layer is responsible for proving it actually matches the
-- owning repository's own project_id (rejecting a mismatch as
-- ports.ErrCrossProjectReference) since SQLite cannot express "match a
-- value in a joined row" as a declarative FK/CHECK constraint.
CREATE TABLE components (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    kind TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (repository_id, path)
);

CREATE INDEX components_project_idx ON components(project_id);

-- component_pack_assignments pins an exact EngineeringPackVersion to a
-- Component with an effective time and acting actor
-- (docs/design/01-system-design.md §6.2: "lưu component, pack version,
-- effective time và actor để resolved configuration không phải suy ngầm
-- từ UI"). It is append-only -- assigning a new pack version to the same
-- Component inserts a new row, never updates an existing one, so the
-- full assignment history (and "what was in effect at time T") stays
-- queryable. pack_version_id is a bare string, not a foreign key: it
-- names an EngineeringPackVersion by id, but internal/domain/project
-- cannot import internal/domain/engineeringpack without creating an
-- import cycle (engineeringpack already imports internal/domain/definition,
-- which imports internal/domain/project) -- so this identity is carried
-- as an opaque value, the same cross-kind-reference-by-value convention
-- policy.ResourceRef already established for a resource identity tuple.
CREATE TABLE component_pack_assignments (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    component_id TEXT NOT NULL REFERENCES components(id),
    pack_version_id TEXT NOT NULL,
    effective_at TEXT NOT NULL,
    actor TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (component_id, effective_at)
);

CREATE INDEX component_pack_assignments_component_idx
    ON component_pack_assignments(component_id, effective_at);
