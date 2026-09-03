-- WorkItem's own contract fields (docs/design/05-v3-project-workspace.md
-- V3-03; docs/architecture/04-go-core-spec.md §4.2's WorkItem struct
-- sketch; docs/design/01-system-design.md §6.1's work_items row:
-- "schema_version, kind, title, behavior, acceptance_json,
-- verification_json, risk, exclusions_json, workflow_version_id"). A
-- plain additive ALTER TABLE ADD COLUMN set is correct and sufficient
-- here, unlike 0006_repository_status_components.sql's repositories
-- rebuild: work_items' existing status CHECK constraint
-- (BACKLOG|READY|ACTIVE|BLOCKED|DONE|CANCELLED,
-- 0001_initial_schema.sql) already matches go-core-spec exactly and
-- needs no change, and none of these new columns need a CHECK tight
-- enough to force a rebuild (risk stays a plain string column — see
-- internal/domain/work/work.go's own RiskLevel doc comment for why no
-- citation grounds a closed, CHECK-enforceable enum for it).
--
-- Every new column is nullable. Nothing in this repository writes a
-- work_items row with any of these fields yet: the real public creators
-- (CreateRootWorkItem/CreateChildWorkItem, go-core-spec §8) are V3-04's
-- job, not built by this migration or by V3-03 at all
-- ("Validator MAY chạy trước V3-04 nhưng không persist WorkItem hoặc
-- transition độc lập"). Existing fixture rows
-- (internal/adapters/sqlite/fixtures.go, crashworker_fixtures.go) insert
-- work_items by explicit column list without any of these fields and
-- must keep working unmodified. Nullability here is a schema
-- accommodation for those pre-V3-03 rows, not a statement that a real
-- WorkItem's contract fields are optional at the domain level — that
-- completeness bar is internal/domain/work.ValidateReadinessGate's job,
-- enforced in Go, not via a DB constraint: it runs before READY, at a
-- point no row need exist yet ("contract validation có thể chạy trước
-- transaction nhưng không được persist WorkItem mồ côi",
-- go-core-spec §8).
ALTER TABLE work_items ADD COLUMN schema_version INTEGER;
ALTER TABLE work_items ADD COLUMN behavior TEXT;
ALTER TABLE work_items ADD COLUMN acceptance_json TEXT;
ALTER TABLE work_items ADD COLUMN verification_json TEXT;
ALTER TABLE work_items ADD COLUMN risk TEXT;
ALTER TABLE work_items ADD COLUMN exclusions_json TEXT;
ALTER TABLE work_items ADD COLUMN workflow_version_id TEXT REFERENCES workflow_versions(id);
