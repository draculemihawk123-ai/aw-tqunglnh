package fake_test

import (
	"context"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
)

func TestUnitOfWork_WithSerializedWrite_CallsFn(t *testing.T) {
	uow := fake.New()
	called := false
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		called = true
		if tx == nil {
			t.Error("fn received a nil Tx")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithSerializedWrite: %v", err)
	}
	if !called {
		t.Fatal("fn was never called")
	}
}

func TestUnitOfWork_WithSerializedWrite_PropagatesFnError(t *testing.T) {
	uow := fake.New()
	sentinel := errors.New("handler refused to commit")
	err := uow.WithSerializedWrite(context.Background(), func(ports.Tx) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}

func TestUnitOfWork_RejectsNestedTransaction(t *testing.T) {
	uow := fake.New()
	outerErr := uow.WithSerializedWrite(context.Background(), func(ports.Tx) error {
		return uow.WithSerializedWrite(context.Background(), func(ports.Tx) error {
			return nil
		})
	})
	if !errors.Is(outerErr, fake.ErrNestedTransaction) {
		t.Fatalf("nested WithSerializedWrite err = %v, want %v", outerErr, fake.ErrNestedTransaction)
	}
}

func TestUnitOfWork_AllowsSequentialTransactionsAfterOneCompletes(t *testing.T) {
	uow := fake.New()
	if err := uow.WithSerializedWrite(context.Background(), func(ports.Tx) error { return nil }); err != nil {
		t.Fatalf("first WithSerializedWrite: %v", err)
	}
	if err := uow.WithSerializedWrite(context.Background(), func(ports.Tx) error { return nil }); err != nil {
		t.Fatalf("second (sequential, not nested) WithSerializedWrite: %v", err)
	}
}

func TestUnitOfWork_NestedFailureStillClearsInTxFlag(t *testing.T) {
	// A rejected nested call must not leave the outer transaction wedged:
	// the outer fn can still return normally and the fake must allow a
	// later, genuinely sequential call afterward.
	uow := fake.New()
	err := uow.WithSerializedWrite(context.Background(), func(ports.Tx) error {
		nestedErr := uow.WithSerializedWrite(context.Background(), func(ports.Tx) error { return nil })
		if !errors.Is(nestedErr, fake.ErrNestedTransaction) {
			t.Fatalf("nested call err = %v, want %v", nestedErr, fake.ErrNestedTransaction)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("outer WithSerializedWrite: %v", err)
	}
	if err := uow.WithSerializedWrite(context.Background(), func(ports.Tx) error { return nil }); err != nil {
		t.Fatalf("call after outer completed: %v", err)
	}
}

func TestQueryStore_PingUnreachable(t *testing.T) {
	store := &fake.QueryStore{Unreachable: true}
	if err := store.Ping(context.Background()); err == nil {
		t.Fatal("Ping on an Unreachable fake QueryStore should fail")
	}
}

func TestQueryStore_PingReachable(t *testing.T) {
	store := &fake.QueryStore{}
	if err := store.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

// --- illustrative app-layer handler, proving V1-05's "app service có
// thể test không SQLite": this file imports only internal/app/ports and
// internal/app/ports/fake — never internal/adapters/sqlite. ---

// exampleHandlerErr is what a command handler shaped like this returns
// when its precondition fails — a stand-in for the typed apperror.Error a
// real V1-06 handler would use.
var errExamplePreconditionFailed = errors.New("example: precondition failed")

// runExampleCommand is a minimal illustrative command handler: it takes a
// ports.UnitOfWork (never a concrete adapter type) and commits through
// it. Real command handlers land in V1-06; this exists only to prove the
// dependency shape is testable without SQLite.
func runExampleCommand(ctx context.Context, uow ports.UnitOfWork, precondition bool) error {
	if !precondition {
		return errExamplePreconditionFailed
	}
	return uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_ = tx // a real handler would call tx.Events().Append(...) etc. once populated
		return nil
	})
}

func TestExampleHandler_SucceedsWithFakeUnitOfWork(t *testing.T) {
	uow := fake.New()
	if err := runExampleCommand(context.Background(), uow, true); err != nil {
		t.Fatalf("runExampleCommand: %v", err)
	}
}

func TestExampleHandler_FailsPreconditionWithoutTouchingUnitOfWork(t *testing.T) {
	uow := fake.New()
	err := runExampleCommand(context.Background(), uow, false)
	if !errors.Is(err, errExamplePreconditionFailed) {
		t.Fatalf("err = %v, want %v", err, errExamplePreconditionFailed)
	}
}
