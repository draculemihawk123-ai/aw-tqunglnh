package ports

import (
	"context"
	"errors"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/safesettings"
)

// ErrSafeSettingsCorrupt is returned by SafeSettingsRepository.Get when the
// persisted desired document fails to decode — V6-10G's own Verify line
// "corrupt persisted settings fail readiness/Doctor typed": a bit-rotted or
// hand-edited row must surface as this one, typed, well-known condition, so
// a readiness check or internal/app/doctor.CheckSafeSettings can report it
// by name instead of panicking or silently falling back to a default
// desired document.
var ErrSafeSettingsCorrupt = errors.New("safe settings: persisted desired document failed to decode")

// SafeSettingsRecord is the one durable, versioned row
// SafeSettingsRepository reads/writes (V6-10G, ADR-016/017/025/028):
// exactly one singleton row, seeded by migration
// (internal/adapters/sqlite/migrations/0036_safe_settings.sql) at Version 1
// with the zero-value ("never configured") desired document — there is no
// "row does not exist yet" state a caller needs to handle separately from
// "nothing has been configured".
type SafeSettingsRecord struct {
	Desired   safesettings.SafeSettings
	Version   uint64
	UpdatedAt time.Time
	// UpdatedBy is the Command.Actor that produced this row — empty for the
	// migration-seeded row, which no operator ever actually wrote.
	UpdatedBy string
}

// UpdateSafeSettingsRequest is the CAS request for
// SafeSettingsRepository.Update: the full desired document replaces
// whatever was there before (V6-10G's own "Store full desired document +
// version" — never a per-field patch), fenced by ExpectedVersion exactly
// like every other CAS in this codebase (e.g. TransitionReleaseSetState).
type UpdateSafeSettingsRequest struct {
	Desired         safesettings.SafeSettings
	ExpectedVersion uint64
	UpdatedBy       string
	OccurredAt      time.Time
}

// SafeSettingsRepository is V6-10G's Tx accessor: a single versioned,
// optimistic-concurrency row, updated by full-document replacement — never
// an event-sourced append-log of individual field changes (see this
// package's own SafeSettingsRecord doc comment). Populated now (V6-10G)
// with real methods from the start, the same "gets a real interface from
// the start" treatment AdapterBuildRepository/ReadinessRepository/
// WaitRepository/ApprovalRepository already received for a concern its own
// task owns end to end.
type SafeSettingsRepository interface {
	// Get returns the current SafeSettingsRecord — never
	// ErrPersistenceNotFound, since the singleton row always exists once
	// migration 0036 has run. Returns ErrSafeSettingsCorrupt if the
	// persisted desired document fails to decode.
	Get(ctx context.Context) (SafeSettingsRecord, error)
	// Update performs the CAS: req.ExpectedVersion must match the row's
	// current version, or the whole call fails with ErrOptimisticConflict —
	// a stale caller never silently overwrites a desired document another
	// caller already replaced. On success, version increments by exactly
	// one and the returned SafeSettingsRecord reflects the new row.
	Update(ctx context.Context, req UpdateSafeSettingsRequest) (SafeSettingsRecord, error)
}
