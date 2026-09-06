package fake

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// WaitRepository is an in-memory ports.WaitRepository — V4-08 gives this
// concern its first real behavior, mirroring ReadinessRepository's own
// "sqlite-free app-layer test" discipline.
type WaitRepository struct {
	registrations      map[string]runtime.WaitRegistration // by ID
	registrationByNode map[string]string                   // NodeRunID -> WaitRegistrationID, UNIQUE(node_run_id)
	signals            map[string]runtime.WaitSignal       // by ID
	signalByRegAndKey  map[string]string                   // WaitRegistrationID+"\x00"+SignalKey -> WaitSignalID
}

var _ ports.WaitRepository = (*WaitRepository)(nil)

func (r *WaitRepository) clone() *WaitRepository {
	registrations := make(map[string]runtime.WaitRegistration, len(r.registrations))
	for k, v := range r.registrations {
		registrations[k] = v
	}
	registrationByNode := make(map[string]string, len(r.registrationByNode))
	for k, v := range r.registrationByNode {
		registrationByNode[k] = v
	}
	signals := make(map[string]runtime.WaitSignal, len(r.signals))
	for k, v := range r.signals {
		signals[k] = v
	}
	signalByRegAndKey := make(map[string]string, len(r.signalByRegAndKey))
	for k, v := range r.signalByRegAndKey {
		signalByRegAndKey[k] = v
	}
	return &WaitRepository{
		registrations: registrations, registrationByNode: registrationByNode,
		signals: signals, signalByRegAndKey: signalByRegAndKey,
	}
}

func (r *WaitRepository) CreateWaitRegistration(_ context.Context, registration runtime.WaitRegistration) (runtime.WaitRegistration, error) {
	key := string(registration.ID)
	if _, exists := r.registrations[key]; exists {
		return runtime.WaitRegistration{}, fmt.Errorf("fake: %w: wait registration %s", ports.ErrPersistenceAlreadyExists, key)
	}
	nodeRunKey := string(registration.NodeRunID)
	if _, exists := r.registrationByNode[nodeRunKey]; exists {
		return runtime.WaitRegistration{}, fmt.Errorf("fake: %w: node run %s already has a wait registration", ports.ErrPersistenceAlreadyExists, nodeRunKey)
	}
	if r.registrations == nil {
		r.registrations = map[string]runtime.WaitRegistration{}
	}
	if r.registrationByNode == nil {
		r.registrationByNode = map[string]string{}
	}
	r.registrations[key] = registration
	r.registrationByNode[nodeRunKey] = key
	return registration, nil
}

func (r *WaitRepository) GetWaitRegistration(_ context.Context, id string) (runtime.WaitRegistration, error) {
	registration, ok := r.registrations[id]
	if !ok {
		return runtime.WaitRegistration{}, fmt.Errorf("fake: %w: wait registration %s", ports.ErrPersistenceNotFound, id)
	}
	return registration, nil
}

// ListWaitRegistrationsForRun mirrors sqlite's ListWaitRegistrationsForRun
// (V4-12B).
func (r *WaitRepository) ListWaitRegistrationsForRun(_ context.Context, runID string) ([]runtime.WaitRegistration, error) {
	var registrations []runtime.WaitRegistration
	for _, registration := range r.registrations {
		if string(registration.RunID) == runID {
			registrations = append(registrations, registration)
		}
	}
	return registrations, nil
}

// RecordWaitSignal mirrors sqlite's identical idempotent-replay/conflict
// contract — see ports.WaitRepository.RecordWaitSignal's own doc comment.
func (r *WaitRepository) RecordWaitSignal(_ context.Context, signal runtime.WaitSignal) (runtime.WaitSignal, bool, error) {
	dedupeKey := string(signal.WaitRegistrationID) + "\x00" + signal.SignalKey
	if existingID, exists := r.signalByRegAndKey[dedupeKey]; exists {
		existing := r.signals[existingID]
		if existing.PayloadHash != signal.PayloadHash {
			return runtime.WaitSignal{}, false, apperror.New(
				errorcode.CodeIdempotencyConflict,
				fmt.Sprintf("wait signal key %q already recorded with a different payload for registration %s", signal.SignalKey, signal.WaitRegistrationID),
				false,
			)
		}
		return existing, true, nil
	}
	if r.signals == nil {
		r.signals = map[string]runtime.WaitSignal{}
	}
	if r.signalByRegAndKey == nil {
		r.signalByRegAndKey = map[string]string{}
	}
	r.signals[string(signal.ID)] = signal
	r.signalByRegAndKey[dedupeKey] = string(signal.ID)
	return signal, false, nil
}

// TransitionWaitRegistration mirrors sqlite's identical fenced CAS — a
// stale caller (wrong ExpectedState/ExpectedVersion) gets
// ErrOptimisticConflict, never a silent overwrite.
func (r *WaitRepository) TransitionWaitRegistration(_ context.Context, req ports.TransitionWaitRegistrationRequest) (runtime.WaitRegistration, error) {
	registration, ok := r.registrations[req.WaitRegistrationID]
	if !ok {
		return runtime.WaitRegistration{}, fmt.Errorf("fake: %w: wait registration %s", ports.ErrPersistenceNotFound, req.WaitRegistrationID)
	}
	if registration.State != req.ExpectedState || registration.Version != req.ExpectedVersion {
		return runtime.WaitRegistration{}, fmt.Errorf(
			"fake: %w: wait registration %s expected %s@%d",
			ports.ErrOptimisticConflict, req.WaitRegistrationID, req.ExpectedState, req.ExpectedVersion,
		)
	}
	registration.State = req.NextState
	if req.ConsumedSignalID != "" {
		ref := runtime.WaitSignalID(req.ConsumedSignalID)
		registration.ConsumedSignalID = &ref
	}
	registration.Version++
	r.registrations[req.WaitRegistrationID] = registration
	return registration, nil
}
