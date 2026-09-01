package fake_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
)

func TestFakeEventsRepository_AppendThenItems_RoundTrips(t *testing.T) {
	uow := fake.New()
	event := ports.DomainEvent{
		ID: "evt-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1,
		EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{}`,
		CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
	}
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Events().Append(context.Background(), event)
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	items := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	if len(items) != 1 || items[0].ID != "evt-1" {
		t.Fatalf("Items() = %+v, want exactly one event with ID evt-1", items)
	}
}

func TestFakeEventsRepository_DuplicateAggregateSequence_Rejected(t *testing.T) {
	uow := fake.New()
	first := ports.DomainEvent{
		ID: "evt-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1,
		EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{}`,
		CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
	}
	second := first
	second.ID = "evt-2"

	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Events().Append(context.Background(), first)
	}); err != nil {
		t.Fatalf("Append first: %v", err)
	}

	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Events().Append(context.Background(), second)
	})
	if err == nil {
		t.Fatal("Append with a duplicate (aggregate_type, aggregate_id, sequence) should fail")
	}
}

func TestFakeEventsRepository_FailedAttempt_DoesNotPersist(t *testing.T) {
	uow := fake.New()
	sentinel := errors.New("handler refused to commit")
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		if appendErr := tx.Events().Append(context.Background(), ports.DomainEvent{
			ID: "evt-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1,
			EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{}`,
			CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
		}); appendErr != nil {
			return appendErr
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}

	items := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	if len(items) != 0 {
		t.Fatalf("Items() = %+v, want empty (a failed attempt must never persist)", items)
	}
}

func TestFakeReceiptsRepository_RecordThenLoad_RoundTrips(t *testing.T) {
	uow := fake.New()
	receipt := ports.Receipt{
		Actor: "actor-1", Scope: ports.ProjectScope("proj-1"), IdempotencyKey: "idem-1",
		CommandType: "CreateProject", RequestHash: "hash-a", ResultJSON: `{"id":"p1"}`,
		CreatedAt: time.Now().UTC(),
	}
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Receipts().Record(context.Background(), receipt)
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	err = uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		got, found, err := tx.Receipts().Load(context.Background(), "actor-1", ports.ProjectScope("proj-1"), "idem-1", "CreateProject")
		if err != nil {
			return err
		}
		if !found {
			t.Fatal("Load did not find the recorded receipt")
		}
		if got.RequestHash != receipt.RequestHash {
			t.Fatalf("got.RequestHash = %q, want %q", got.RequestHash, receipt.RequestHash)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithReadOnly: %v", err)
	}
}

func TestFakeReceiptsRepository_DuplicateDifferentPayload_ReturnsConflict(t *testing.T) {
	uow := fake.New()
	first := ports.Receipt{
		Actor: "actor-1", Scope: ports.InstallationScope(), IdempotencyKey: "idem-1",
		CommandType: "RotateKey", RequestHash: "hash-a", CreatedAt: time.Now().UTC(),
	}
	second := first
	second.RequestHash = "hash-b"

	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Receipts().Record(context.Background(), first)
	}); err != nil {
		t.Fatalf("Record first: %v", err)
	}

	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Receipts().Record(context.Background(), second)
	})
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("Record second (different hash, same key) err = %v, want ports.ErrReceiptConflict", err)
	}
}

func TestFakeReceiptsRepository_FailedAttempt_DoesNotPersist(t *testing.T) {
	uow := fake.New()
	sentinel := errors.New("handler refused to commit")
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		if recordErr := tx.Receipts().Record(context.Background(), ports.Receipt{
			Actor: "actor-1", Scope: ports.InstallationScope(), IdempotencyKey: "idem-1",
			CommandType: "RotateKey", RequestHash: "hash-a", CreatedAt: time.Now().UTC(),
		}); recordErr != nil {
			return recordErr
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}

	err = uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		_, found, err := tx.Receipts().Load(context.Background(), "actor-1", ports.InstallationScope(), "idem-1", "RotateKey")
		if err != nil {
			return err
		}
		if found {
			t.Fatal("Load found a receipt from a failed attempt (must never persist)")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithReadOnly: %v", err)
	}
}
