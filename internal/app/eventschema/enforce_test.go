package eventschema_test

import (
	"context"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// recordingEventsRepository is a minimal ports.EventsRepository test
// double that records every event it actually received, so a test can
// assert EnforcingEventsRepository never delegated a rejected Append.
type recordingEventsRepository struct {
	appended []ports.DomainEvent
}

func (r *recordingEventsRepository) Append(_ context.Context, event ports.DomainEvent) error {
	r.appended = append(r.appended, event)
	return nil
}

func (r *recordingEventsRepository) ScanJournal(_ context.Context, _ uint64, _ int) ([]ports.JournalEvent, error) {
	return nil, nil
}

func TestEnforcingEventsRepository_Append_UnregisteredRejectedWithoutReachingInner(t *testing.T) {
	inner := &recordingEventsRepository{}
	enforcing := eventschema.EnforcingEventsRepository{Inner: inner, Registry: eventschema.NewRegistry()}

	err := enforcing.Append(context.Background(), ports.DomainEvent{
		ID: "evt-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1,
		EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{}`,
		CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
	})
	if apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("err = %v, want apperror.CodeInvalidArgument", err)
	}
	if len(inner.appended) != 0 {
		t.Fatalf("inner.appended = %+v, want empty (a rejected event must never reach the wrapped repository)", inner.appended)
	}
}

func TestEnforcingEventsRepository_Append_RegisteredDelegatesToInner(t *testing.T) {
	inner := &recordingEventsRepository{}
	registry := eventschema.NewRegistry()
	registry.Register("ProjectCreated", 1, func(string) (any, error) { return nil, nil })
	enforcing := eventschema.EnforcingEventsRepository{Inner: inner, Registry: registry}

	event := ports.DomainEvent{
		ID: "evt-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1,
		EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{}`,
		CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
	}
	if err := enforcing.Append(context.Background(), event); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if len(inner.appended) != 1 || inner.appended[0].ID != "evt-1" {
		t.Fatalf("inner.appended = %+v, want exactly evt-1", inner.appended)
	}
}

func TestEnforcingEventsRepository_Append_RegisteredDifferentVersionStillRejected(t *testing.T) {
	inner := &recordingEventsRepository{}
	registry := eventschema.NewRegistry()
	registry.Register("ProjectCreated", 1, func(string) (any, error) { return nil, nil })
	enforcing := eventschema.EnforcingEventsRepository{Inner: inner, Registry: registry}

	// v2 was never registered, only v1 — this must still be rejected.
	err := enforcing.Append(context.Background(), ports.DomainEvent{
		ID: "evt-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1,
		EventType: "ProjectCreated", SchemaVersion: 2, PayloadJSON: `{}`,
		CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
	})
	if apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("err = %v, want apperror.CodeInvalidArgument", err)
	}
	if len(inner.appended) != 0 {
		t.Fatalf("inner.appended = %+v, want empty", inner.appended)
	}
}
