-- recovery_reaper_state: the durable, singleton generation cursor the
-- RECOVERY_REAPER CONTROL job (V4-13, docs/design/06-v4-runtime-engine.md,
-- ADR-020) fences its own self-rescheduling against — the exact "generation"
-- the task's own Verify line names ("hai coordinator tranh recovery vẫn chỉ
-- tạo một recovery job cho generation"), mirroring
-- ScopeExpansionOrigin.PollGeneration's own per-origin self-rescheduling
-- fence (V4-12A) but for a single, global coordinator with no per-Run/
-- per-Attempt origin row of its own to attach a generation counter to.
-- Exactly one row ever exists (id is CHECKed to the literal 'singleton', not
-- just a bare TEXT PRIMARY KEY, so an accidental second row is rejected by
-- the schema itself, not merely by convention). Every reaper job execution
-- reads the current generation, does its own sweep, then CASes
-- generation -> generation+1 and enqueues the next reaper job keyed by
-- IdempotencyKey "recovery-reaper:<next generation>" — two coordinators
-- racing to enqueue that SAME next job (both having read the SAME current
-- generation) collide on that identical idempotency key, so EnqueueJob's
-- own established idempotent-insert-or-return-existing contract is what
-- actually guarantees "only one recovery job per generation", not any
-- locking this table itself performs.
CREATE TABLE recovery_reaper_state (
    id TEXT PRIMARY KEY CHECK (id = 'singleton'),
    generation INTEGER NOT NULL,
    version INTEGER NOT NULL CHECK (version > 0),
    updated_at TEXT NOT NULL
);

INSERT INTO recovery_reaper_state (id, generation, version, updated_at)
VALUES ('singleton', 0, 1, '1970-01-01T00:00:00Z');
