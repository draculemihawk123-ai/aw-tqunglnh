-- artifact_sweep_state: the durable, singleton generation cursor the
-- ARTIFACT_SWEEP CONTROL job (V5-14) fences its own self-rescheduling
-- against, mirroring recovery_reaper_state (migration 26) exactly —
-- exactly one row ever exists (id is CHECKed to the literal 'singleton'),
-- and every sweep job execution reads the current generation, does its own
-- sweep, then CASes generation -> generation+1 and enqueues the next sweep
-- job keyed by IdempotencyKey "artifact-sweep:<next generation>".
--
-- dry_run defaults to 1 (true) — this task's own contract ("Dry-run và
-- report là mặc định; thao tác thật phải explicit"): a fresh installation
-- never deletes real Artifact content until an operator explicitly flips
-- this to 0 via a real command (internal/app/artifactsweep). While true,
-- the sweep still runs its full candidate-scan and group-eligibility logic
-- and records a durable manifest of what it WOULD have done, but never
-- claims a Locator or calls ports.ArtifactStore.Delete.
CREATE TABLE artifact_sweep_state (
    id TEXT PRIMARY KEY CHECK (id = 'singleton'),
    generation INTEGER NOT NULL,
    dry_run INTEGER NOT NULL CHECK (dry_run IN (0, 1)),
    version INTEGER NOT NULL CHECK (version > 0),
    updated_at TEXT NOT NULL
);

INSERT INTO artifact_sweep_state (id, generation, dry_run, version, updated_at)
VALUES ('singleton', 0, 1, 1, '1970-01-01T00:00:00Z');
