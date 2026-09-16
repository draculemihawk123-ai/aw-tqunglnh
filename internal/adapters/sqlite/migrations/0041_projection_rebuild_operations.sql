-- projection_rebuild_operations: V6-09's own idempotent rebuild
-- intent/job/operation-status record (docs/design/08-v6-api-projections.md
-- V6-09) — distinct from projection_generations/projection_rows/
-- projection_checkpoints/projection_poison (migration 0039), which this
-- table never touches: V6-09's own "Không làm" line is explicit that
-- RequestProjectionRebuild never clears rows, edits a cursor, or invokes
-- a worker inline. This table only ever records that a rebuild was
-- REQUESTED and, for a later task's own worker (V6-09A, not yet built),
-- its own subsequent phase/watermark progress.
--
-- phase is the operation's own closed lifecycle (see
-- internal/app/ports/projectionrebuild.go's own ProjectionRebuildPhase doc
-- comment for the full phase-by-phase mapping to V6-09A's own design
-- prose): REQUESTED (this task's own only ever-written phase) ->
-- SNAPSHOTTING -> BUILDING -> CUTTING_OVER -> SUCCEEDED | FAILED. Every
-- later phase belongs exclusively to V6-09A's own not-yet-built worker.
--
-- idx_projection_rebuild_operations_active is a DB-level backstop for the
-- SAME "at most one nonterminal operation per (project_id,
-- projection_name)" invariant RequestProjectionRebuild's own
-- application-level check-then-insert already enforces under this
-- Store's global BEGIN-IMMEDIATE write serialization (txrunner.go): every
-- RunSerializedWrite call already takes its write-intent lock at BEGIN,
-- so two concurrent RequestProjectionRebuild calls can never actually
-- interleave their own check-then-insert — but this index still exists
-- so the invariant is also enforced at the schema level, matching
-- release_set_local_commits.marker's own identical "UNIQUE column plus an
-- explicit pre-check" belt-and-suspenders precedent (migration 0037).
-- The WHERE clause's four phase values must stay in sync BY HAND with
-- ports.NonterminalProjectionRebuildPhases — SQL cannot reference a Go
-- slice.
--
-- w0/shadow_generation/shadow_cursor/cutover_cursor are all NULL until
-- V6-09A's own worker actually reaches the phase that first populates
-- them (SNAPSHOTTING/BUILDING/CUTTING_OVER respectively) — this task
-- never writes any of the four. error_code/error_message are the safe
-- (never a raw internal cause, stack trace or SQL error string),
-- operator-facing failure summary for a FAILED operation — mirrors
-- apperror.Error's own "Message/Details ... safe to log, return over the
-- API, or show an operator" discipline; this task never populates them
-- either.
CREATE TABLE projection_rebuild_operations (
    id TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id),
    projection_name TEXT NOT NULL,
    phase TEXT NOT NULL CHECK (phase IN (
        'REQUESTED', 'SNAPSHOTTING', 'BUILDING', 'CUTTING_OVER', 'SUCCEEDED', 'FAILED'
    )),
    w0 INTEGER,
    shadow_generation INTEGER,
    shadow_cursor INTEGER,
    cutover_cursor INTEGER,
    error_code TEXT,
    error_message TEXT,
    job_id TEXT NOT NULL,
    requested_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0)
);

CREATE INDEX idx_projection_rebuild_operations_scope ON projection_rebuild_operations(project_id, projection_name);

CREATE UNIQUE INDEX idx_projection_rebuild_operations_active
    ON projection_rebuild_operations(project_id, projection_name)
    WHERE phase IN ('REQUESTED', 'SNAPSHOTTING', 'BUILDING', 'CUTTING_OVER');
