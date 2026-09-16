-- projection_* tables: V6-08's own frozen, generation-aware Kanban/
-- task-detail projection schema (docs/design/08-v6-api-projections.md
-- V6-08). This migration only creates the schema + supporting index; no
-- live consumer (V6-08A), rebuild (V6-09) or read endpoint (V6-10) writes
-- to these tables yet in this task.
--
-- Design: ONE named projection ("workitem") serves BOTH the Kanban card
-- list (Screen 5) and the projected WorkItem detail (Screen 7's own
-- projected-not-authoritative sibling data, per V6-10's own "Phạm vi:
-- projected card/detail only") — a single row per WorkItem is the read
-- model; V6-10's own HTTP layer decides whether to render it compact
-- (list) or expanded (single item), never two separately maintained
-- projections of the same underlying entity. EntityKey is always the
-- WorkItemID for this projection name; a future second ProjectionName can
-- reuse this exact same table shape without a new migration.
--
-- generation-aware rebuild (AK-ARCH-022): a rebuild (V6-09/V6-09A) never
-- mutates rows of the currently-active generation in place — it builds a
-- brand-new generation's own full row set, then one atomic transaction
-- flips projection_generations.active_generation. Old-generation rows are
-- garbage-collected later (V6-09A's own "shadow cleanup"), not by this
-- migration. Every row/checkpoint/poison record is therefore always
-- scoped by (project_id, projection_name, generation) so two generations
-- can coexist mid-rebuild without ever colliding.

CREATE TABLE projection_generations (
    project_id TEXT NOT NULL REFERENCES projects(id),
    projection_name TEXT NOT NULL,
    active_generation INTEGER NOT NULL CHECK (active_generation > 0),
    schema_version INTEGER NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (project_id, projection_name)
);

-- projection_rows: the read model itself. payload_json is the row's own
-- canonical (stable field order, no non-deterministic whitespace) JSON
-- encoding of a projection.WorkItemCardRow — canonical so
-- CanonicalRowHash (used by V6-09A's own "canonical old/new diff" verify
-- requirement) is reproducible byte-for-byte from a re-read row.
-- last_applied_journal_position is this ROW's own per-entity watermark
-- (distinct from the projection-wide cursor in projection_checkpoints):
-- V6-08A's own duplicate-suppression ("Duplicate position<=cursor
-- no-op") needs a per-row fence too, since a poison/crash-recovery replay
-- can re-deliver an already-applied position for one entity while the
-- projection-wide cursor has already moved past it for OTHER entities.
CREATE TABLE projection_rows (
    project_id TEXT NOT NULL REFERENCES projects(id),
    projection_name TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    entity_key TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    last_applied_journal_position INTEGER NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (project_id, projection_name, generation, entity_key)
);

-- Kanban's own "filter theo repository/component" (Screen 5) and stable
-- paging both need to scan a generation's full row set without a table
-- scan; entity_key alone is not a useful secondary order.
CREATE INDEX idx_projection_rows_generation ON projection_rows(project_id, projection_name, generation);

-- projection_checkpoints: the projection-wide (not per-row) live-consumer
-- watermark + freshness/degraded status V6-08A CASes atomically alongside
-- row apply. cursor is the greatest scanned global JournalPosition (V6-08's
-- own "Cursor is greatest scanned global JournalPosition" rule — a
-- foreign-project position still advances this cursor, per V6-08A's own
-- "Foreign-project positions advance scan cursor without row changes").
-- status distinguishes a healthy live projection (LIVE) from one that has
-- fallen behind past a freshness threshold (STALE, V6-08A/V6-10's own
-- concern to set, not this migration's) or hit an unrecoverable poison
-- event for this generation (DEGRADED, freezes at last-good cursor).
CREATE TABLE projection_checkpoints (
    project_id TEXT NOT NULL REFERENCES projects(id),
    projection_name TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    cursor INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL CHECK (status IN ('LIVE', 'DEGRADED', 'STALE')) DEFAULT 'LIVE',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (project_id, projection_name, generation)
);

-- projection_poison: one row per event the live consumer could not apply
-- deterministically (corrupt payload, unknown-but-relevant schema, missing
-- referenced authority, aggregate-sequence violation — V6-08's own
-- "Gap chỉ là..." exhaustive list). Recording a poison event is what
-- flips projection_checkpoints.status to DEGRADED; V6-08A's own separate
-- transaction records this AFTER rolling back the failed apply attempt, so
-- a poison row never coexists with a partially-applied row for the same
-- event (V6-08A's own "Failure rolls back rows/cursor; separate tx records
-- poison").
CREATE TABLE projection_poison (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    projection_name TEXT NOT NULL,
    generation INTEGER NOT NULL CHECK (generation > 0),
    journal_position INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    schema_version INTEGER NOT NULL,
    reason TEXT NOT NULL,
    recorded_at TEXT NOT NULL
);

CREATE INDEX idx_projection_poison_scope ON projection_poison(project_id, projection_name, generation);
