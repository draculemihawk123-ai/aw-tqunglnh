-- work_item_effective_scopes (V3-05, docs/design/05-v3-project-workspace.md;
-- docs/design/01-system-design.md §6.1's own row: "work_item/repository/
-- access/paths/scope_version | subset family scope"; go-core-spec §8's
-- CreateChildWorkItem: "Child cùng family, effective scope là tập con";
-- GC-INV-05: "Child/node/attempt không được mở rộng quyền vượt scope family
-- đã phê duyệt").
--
-- This is a SEPARATE table from family_repository_scopes
-- (0001_initial_schema.sql), not new rows appended to it: a child's own
-- effective scope is a per-WorkItem *subset declaration* checked against
-- the family's already-audited grant history
-- (internal/domain/work.ValidateEffectiveScopes), never a new grant of its
-- own — the family's own add-only RepositoryScope history stays the single
-- source of "what was ever approved and why"; this table only ever records
-- "what THIS work item's own activity may touch, which must already be
-- covered by that history".
--
-- Columns beyond the design doc's own minimal row listing
-- (work_item/repository/access/paths/scope_version): project_id and
-- family_id are carried for the same query-convenience/FK-safety reason
-- family_repository_scopes carries a redundant project_id alongside its
-- own family_id (0001_initial_schema.sql) — both are derivable by joining
-- back through work_items/task_families, but a caller reading this table
-- alone (e.g. a future scope-guard check, HE-07-M04) should not have to.
-- reason/added_by/created_at are also carried, beyond the design doc's own
-- terse listing: internal/domain/work.RepositoryScope (the domain type this
-- table's rows round-trip through end to end, both when
-- CreateChildWorkItem first validates a candidate via
-- work.NewRepositoryScope and when a later reader reconstructs a stored row
-- the identical way scanRepositoryScopeRow already does for
-- family_repository_scopes) always carries a Reason/AddedBy/AddedAt of its
-- own — reusing that one already-tested domain type end to end for both
-- tables (construction, ValidateEffectiveScopes's own []RepositoryScope
-- parameter type, and persistence) is simpler and less error-prone than
-- inventing a second, near-duplicate "effective scope" domain type that
-- drops exactly the three fields the design doc's row happens not to name;
-- see docs/design/01-system-design.md §6's own intro ("mutable aggregate có
-- version") for why every table in this section carries this kind of audit
-- metadata even where a given row's own terse column listing does not spell
-- it out.
--
-- PRIMARY KEY mirrors family_repository_scopes' own shape exactly
-- (family/repository/scope_version/access), substituting work_item_id for
-- family_id: this task's own CreateChildWorkItem only ever writes each
-- (work_item_id, repository_id, access) combination once, at the family's
-- current ScopeVersion at child-creation time, but keeping scope_version in
-- the key (rather than assuming it is always redundant with a single latest
-- value) leaves room for a later task to record a re-validated effective
-- scope at a newer family ScopeVersion without an ALTER — the identical
-- forward-compatibility reasoning family_repository_scopes' own add-only
-- design already established for its sibling table, and no CHECK constraint
-- here does anything that table's own CHECK constraints do not already do.
CREATE TABLE work_item_effective_scopes (
    work_item_id TEXT NOT NULL REFERENCES work_items(id),
    project_id TEXT NOT NULL REFERENCES projects(id),
    family_id TEXT NOT NULL REFERENCES task_families(id),
    repository_id TEXT NOT NULL REFERENCES repositories(id),
    scope_version INTEGER NOT NULL CHECK (scope_version > 0),
    access TEXT NOT NULL CHECK (access IN ('READ','WRITE')),
    paths_json TEXT NOT NULL,
    reason TEXT NOT NULL,
    added_by TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (work_item_id, repository_id, scope_version, access)
);
