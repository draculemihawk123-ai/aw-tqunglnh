package ports

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// WaitRepository is V4-08's own Tx accessor (docs/design/06-v4-runtime-engine.md
// V4-08, GC-INV-31): the durable authority for a WAIT NodeRun's own pending
// completion (WaitRegistration) and the immutable, consume-once record of
// every real external signal delivery (WaitSignal) — see both types' own
// doc comments (internal/domain/runtime/wait.go) for the full contract.
// This task owns wait_registrations/wait_signals end to end, the same
// "gets a real interface from the start" treatment AdapterBuildRepository/
// ReadinessRepository already established for a concern their own task
// owned outright.
type WaitRepository interface {
	// CreateWaitRegistration inserts a new ACTIVE WaitRegistration.
	// ErrPersistenceAlreadyExists for a reused ID or a NodeRunID that
	// already has a registration (UNIQUE(node_run_id) — at most one
	// registration per NodeRun activation, ever).
	CreateWaitRegistration(ctx context.Context, registration runtime.WaitRegistration) (runtime.WaitRegistration, error)
	// GetWaitRegistration returns the WaitRegistration named by id, or
	// ErrPersistenceNotFound.
	GetWaitRegistration(ctx context.Context, id string) (runtime.WaitRegistration, error)
	// ListWaitRegistrationsForRun is populated now (V4-12B,
	// docs/design/06-v4-runtime-engine.md): every WaitRegistration whose
	// own RunID matches — the CANCEL_RUN_COORDINATOR job's own sweep uses
	// this to find every still-ACTIVE registration of a cancelling Run and
	// CAS it CANCELLED. Deliberately unfiltered by state.
	ListWaitRegistrationsForRun(ctx context.Context, runID string) ([]runtime.WaitRegistration, error)

	// RecordWaitSignal inserts a new immutable WaitSignal row for
	// signal.WaitRegistrationID/SignalKey. If a row with that exact
	// (WaitRegistrationID, SignalKey) pair already exists, this is an
	// idempotent replay of the same real external event: when the existing
	// row's own PayloadHash equals signal.PayloadHash, it is returned
	// unchanged with existed=true (never a second row, never re-consumed);
	// when the hashes differ (the same SignalKey reused for a genuinely
	// different payload), this returns an error matching
	// errorcode.CodeIdempotencyConflict via apperror — never silently
	// overwriting the original.
	RecordWaitSignal(ctx context.Context, signal runtime.WaitSignal) (recorded runtime.WaitSignal, existed bool, err error)

	// TransitionWaitRegistration is the fenced CAS that closes a
	// WaitRegistration's own race: exactly one of a SignalWait and a firing
	// timer job may ever win it for a given registration.
	// ExpectedState/ExpectedVersion mismatch is ErrOptimisticConflict — the
	// exact mechanism a concurrent loser observes, never a silent no-op.
	TransitionWaitRegistration(ctx context.Context, req TransitionWaitRegistrationRequest) (runtime.WaitRegistration, error)
}

// TransitionWaitRegistrationRequest is the CAS request for
// WaitRepository.TransitionWaitRegistration.
type TransitionWaitRegistrationRequest struct {
	WaitRegistrationID string
	ExpectedState      runtime.WaitRegistrationState
	ExpectedVersion    uint64
	NextState          runtime.WaitRegistrationState
	// ConsumedSignalID is required when NextState is
	// runtime.WaitRegistrationConsumed, and must be empty otherwise.
	ConsumedSignalID string
}
