-- V5-14 (docs/architecture/04-go-core-spec.md §19: "Sweeper phải check
-- reference/hold atomically trước xóa"): the retention sweeper's own durable
-- deletion-intent record, keyed by ports.ArtifactRef.Locator itself (not by
-- any one Artifact row's ID) — a Locator's real content may be shared by
-- multiple Artifact rows, even across different Projects
-- (0027_artifacts.sql: "content_hash is deliberately NOT unique"), so the
-- claim that gates a real ports.ArtifactStore.Delete call must be scoped to
-- the shared resource itself, not to any single row referencing it.
--
-- claim_owner is an opaque identifier for whatever sweep run/job holds the
-- claim (e.g. a durable job ID) — enough for a later crash-recovery pass to
-- tell "this is my own still-in-flight claim, safe to resume" from "an
-- abandoned claim left by a run that never finished", without this
-- migration itself needing to know the shape of that job.
--
-- This table is the OTHER half of the TOCTOU protection this task's own
-- contract requires: InsertArtifact (artifact_repository.go) checks it
-- before ever inserting a new row against a Locator, and rejects if a claim
-- is currently open — so a fresh Put of byte-identical content can never
-- race a sweep that is mid-way through deleting that exact content's bytes.
--
-- No REFERENCES into artifacts(locator): locator is not a candidate key on
-- that table (deliberately not unique, per above) — this is a standalone
-- claim registry an application-layer sweep consults and maintains, not a
-- foreign-key relationship.
CREATE TABLE artifact_locator_purge_claims (
    locator TEXT PRIMARY KEY,
    claim_owner TEXT NOT NULL,
    claimed_at TEXT NOT NULL
);
