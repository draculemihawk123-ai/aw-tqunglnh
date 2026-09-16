-- projection_checkpoints gets V6-08A's own consumer lease/fence columns
-- (docs/design/08-v6-api-projections.md V6-08A) — the SAME per-(project,
-- projection_name, generation) row V6-08's own migration 0039 already
-- created, extended rather than a new table: the checkpoint IS the
-- consumer's own state, one row per generation, exactly the shape a lease
-- acquire-or-renew needs to CAS against atomically alongside cursor/status.
--
-- Mirrors write_leases' own acquire-or-steal-if-expired shape exactly
-- (migration 0001's own write_leases table, internal/adapters/sqlite/
-- scheduling.go's own acquireWriteLeasesOnce): fence_token increments on
-- every successful acquire-or-renew (including a steal of an expired
-- lease), never reset; a consumer that re-validates fence_token inside its
-- own later apply transaction and finds it changed knows its lease was
-- stolen out from under it and must abort without committing anything —
-- the "stale fence" case V6-08A's own Verify line names. lease_owner/
-- lease_until/heartbeat_at are all NULL until the first ever
-- AcquireOrRenewProjectionConsumerLease call for a given generation (the
-- SAME statement that creates the checkpoint row in the first place, via
-- INSERT ... ON CONFLICT DO UPDATE — see projection_repository.go).
ALTER TABLE projection_checkpoints ADD COLUMN fence_token INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projection_checkpoints ADD COLUMN lease_owner TEXT;
ALTER TABLE projection_checkpoints ADD COLUMN lease_until TEXT;
ALTER TABLE projection_checkpoints ADD COLUMN heartbeat_at TEXT;
