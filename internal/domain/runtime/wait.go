package runtime

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// WaitRegistrationID identifies one wait_registrations row.
type WaitRegistrationID string

// WaitSignalID identifies one immutable wait_signals row.
type WaitSignalID string

// WaitRegistrationState is the closed set of states a WaitRegistration may
// reach (V4-08, GC-INV-31). ACTIVE is the only non-terminal one — durable_jobs
// (the timer job) only ever wakes processing up at DueAt; it is never
// itself the authority over whether this registration is still ACTIVE,
// which a fenced CAS on this row's own State/Version always is.
type WaitRegistrationState string

const (
	WaitRegistrationActive WaitRegistrationState = "ACTIVE"
	// WaitRegistrationConsumed is a SIGNAL-mode registration resolved by a
	// real, recorded WaitSignal (ConsumedSignalID is always set here).
	WaitRegistrationConsumed WaitRegistrationState = "CONSUMED"
	// WaitRegistrationElapsed is a DURATION-mode registration whose own
	// DurationSeconds reached its due time — a normal, designed
	// completion (never a failure), routed via CompletionOutcome exactly
	// like WaitRegistrationConsumed is. Kept as its own distinct state
	// (rather than reusing WaitRegistrationConsumed, which would wrongly
	// imply a signal existed) since DURATION mode never has one.
	WaitRegistrationElapsed WaitRegistrationState = "ELAPSED"
	// WaitRegistrationTimedOut is a SIGNAL-mode registration whose own
	// TimeoutSeconds ceiling elapsed with no signal ever recorded — routed
	// via TimeoutOutcome.
	WaitRegistrationTimedOut  WaitRegistrationState = "TIMED_OUT"
	WaitRegistrationCancelled WaitRegistrationState = "CANCELLED"
)

// WaitRegistration is the durable authority for one WAIT NodeRun
// activation's own pending completion (V4-08, GC-INV-31). At most one
// exists per NodeRunID — a fresh NodeRun activation (a rework/reactivation,
// V4-07) always gets its own fresh registration, never a resurrected one.
//
// CompletionOutcome/TimeoutOutcome are the exact outcome strings this
// registration's own node pinned in its compiled WorkflowVersion at the
// moment this registration was created (workflow.WaitNodeConfig.
// CompletionOutcome/TimeoutOutcome, confirmed with the user before writing
// this task's code) — SignalWait and the timer job never choose an outcome
// themselves; they only race a CAS on this row, and the winner routes using
// whichever of these two fields applies. TimeoutOutcome is "" when this
// registration has no timeout ceiling at all (DURATION always completes
// via CompletionOutcome; SIGNAL with no TimeoutSeconds never times out).
//
// DueAt is nil exactly when TimeoutOutcome is "" — no due time to wake a
// timer job for. ConsumedSignalID is set only once, atomically with the
// CAS to CONSUMED (never independently) — see
// ports.WaitRepository.TransitionWaitRegistration's own doc comment.
type WaitRegistration struct {
	ID                WaitRegistrationID
	ProjectID         project.ProjectID
	RunID             WorkflowRunID
	NodeRunID         NodeRunID
	NodeKey           string
	SignalName        string
	DueAt             *time.Time
	CompletionOutcome string
	TimeoutOutcome    string
	State             WaitRegistrationState
	ConsumedSignalID  *WaitSignalID
	Version           uint64
}

// NewWaitRegistration validates and builds a new ACTIVE WaitRegistration at
// version 1.
func NewWaitRegistration(
	id WaitRegistrationID, projectID project.ProjectID, runID WorkflowRunID, nodeRunID NodeRunID, nodeKey string,
	signalName string, dueAt *time.Time, completionOutcome, timeoutOutcome string,
) (WaitRegistration, error) {
	nodeKey = strings.TrimSpace(nodeKey)
	signalName = strings.TrimSpace(signalName)
	completionOutcome = strings.TrimSpace(completionOutcome)
	timeoutOutcome = strings.TrimSpace(timeoutOutcome)
	if id == "" || projectID == "" || runID == "" || nodeRunID == "" || nodeKey == "" {
		return WaitRegistration{}, errors.New("wait registration identities are required")
	}
	if completionOutcome == "" {
		return WaitRegistration{}, errors.New("wait registration completion outcome is required")
	}
	if timeoutOutcome != "" && dueAt == nil {
		return WaitRegistration{}, errors.New("wait registration with a timeout outcome requires a due time")
	}
	var pinnedDueAt *time.Time
	if dueAt != nil {
		due := dueAt.UTC()
		pinnedDueAt = &due
	}
	return WaitRegistration{
		ID: id, ProjectID: projectID, RunID: runID, NodeRunID: nodeRunID, NodeKey: nodeKey,
		SignalName: signalName, DueAt: pinnedDueAt, CompletionOutcome: completionOutcome, TimeoutOutcome: timeoutOutcome,
		State: WaitRegistrationActive, Version: 1,
	}, nil
}

// WaitSignal is one immutable, durably-recorded external signal delivery
// (V4-08, GC-INV-31) — never mutated or deleted once inserted. SignalKey is
// the caller-supplied, opaque, stable identity of the real-world event this
// row represents (confirmed with the user before writing this task's
// code): unique per (WaitRegistrationID, SignalKey), so the exact same
// external event reported through two different command invocations
// (different actors, different ports.Command.IdempotencyKey values) is
// still recognized as one signal, never consumed twice. This is a
// deliberately separate concept from ports.Command's own IdempotencyKey,
// which only protects a single command call and its own receipt — a
// caller with no external identity of its own may deliberately reuse their
// command's IdempotencyKey as SignalKey, but this package never assumes
// that equivalence or generates one on a caller's behalf.
type WaitSignal struct {
	ID                 WaitSignalID
	WaitRegistrationID WaitRegistrationID
	SignalKey          string
	PayloadJSON        json.RawMessage
	PayloadHash        string
	Actor              string
	ReceivedAt         time.Time
}

// NewWaitSignal validates and builds one immutable signal record.
func NewWaitSignal(
	id WaitSignalID, waitRegistrationID WaitRegistrationID, signalKey string,
	payload json.RawMessage, payloadHash, actor string, receivedAt time.Time,
) (WaitSignal, error) {
	signalKey = strings.TrimSpace(signalKey)
	payloadHash = strings.TrimSpace(payloadHash)
	actor = strings.TrimSpace(actor)
	if id == "" || waitRegistrationID == "" || signalKey == "" {
		return WaitSignal{}, errors.New("wait signal identities are required")
	}
	if payloadHash == "" {
		return WaitSignal{}, errors.New("wait signal payload hash is required")
	}
	if actor == "" {
		return WaitSignal{}, errors.New("wait signal actor is required")
	}
	if len(payload) > 0 && !json.Valid(payload) {
		return WaitSignal{}, errors.New("wait signal payload must be valid JSON")
	}
	if receivedAt.IsZero() {
		return WaitSignal{}, errors.New("wait signal received timestamp is required")
	}
	return WaitSignal{
		ID: id, WaitRegistrationID: waitRegistrationID, SignalKey: signalKey,
		PayloadJSON: append(json.RawMessage(nil), payload...), PayloadHash: payloadHash, Actor: actor,
		ReceivedAt: receivedAt.UTC(),
	}, nil
}
