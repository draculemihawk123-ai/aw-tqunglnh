package cli_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func fixedNow() time.Time { return time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC) }

func TestBuildEnvelopeGeneratesIdempotencyKeyWhenOmitted(t *testing.T) {
	env := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: config.DefaultLocalPrincipal(), CommandType: "NoOpSample", Scope: ports.InstallationScope(),
		NormalizedPayload: []byte(`{"name":"a"}`), IDSource: idsource.NewSequential("id"), Now: fixedNow,
	})

	if !env.IdempotencyKeyGenerated {
		t.Fatal("IdempotencyKeyGenerated = false, want true when caller supplied none")
	}
	if env.Command.IdempotencyKey == "" {
		t.Fatal("Command.IdempotencyKey is empty after generation")
	}
	if env.Command.Actor != "local-operator" || len(env.Command.ActorRoles) != 1 || env.Command.ActorRoles[0] != "operator" {
		t.Fatalf("Command actor/roles = %q/%v, want the default local principal", env.Command.Actor, env.Command.ActorRoles)
	}
}

func TestBuildEnvelopeKeepsSuppliedIdempotencyKey(t *testing.T) {
	env := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: config.DefaultLocalPrincipal(), CommandType: "NoOpSample", Scope: ports.InstallationScope(),
		NormalizedPayload: []byte(`{}`), IdempotencyKey: "caller-key",
		IDSource: idsource.NewSequential("id"), Now: fixedNow,
	})
	if env.IdempotencyKeyGenerated {
		t.Fatal("IdempotencyKeyGenerated = true, want false when caller supplied a key")
	}
	if env.Command.IdempotencyKey != "caller-key" {
		t.Fatalf("Command.IdempotencyKey = %q, want %q", env.Command.IdempotencyKey, "caller-key")
	}
}

// TestBuildEnvelopeNeverPopulatesActorFromAnythingButPrincipal is the
// "spoof actor absent" verify bullet: EnvelopeRequest has no field other
// than Principal through which a caller could inject a different actor/
// roles, so Command.Actor/ActorRoles always equal exactly what the
// trusted principal carried in, no matter what else is on the request.
func TestBuildEnvelopeNeverPopulatesActorFromAnythingButPrincipal(t *testing.T) {
	principal := config.LocalPrincipal{Actor: "alice", Roles: []string{"operator", "reviewer"}}
	env := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: "NoOpSample", Scope: ports.InstallationScope(),
		NormalizedPayload: []byte(`{}`), IDSource: idsource.NewSequential("id"), Now: fixedNow,
	})
	if env.Command.Actor != "alice" {
		t.Fatalf("Command.Actor = %q, want %q", env.Command.Actor, "alice")
	}
	if len(env.Command.ActorRoles) != 2 || env.Command.ActorRoles[0] != "operator" || env.Command.ActorRoles[1] != "reviewer" {
		t.Fatalf("Command.ActorRoles = %v, want [operator reviewer]", env.Command.ActorRoles)
	}
}

func TestBuildEnvelopeIDAndCorrelationIDAreDeterministic(t *testing.T) {
	// Mirrors httpapi's own newWorkspaceCommand / cmd/aw/definition.go's
	// own newDefinitionCommand: ID/CorrelationID derived from
	// (CommandType, IdempotencyKey), stable and reproducible across a
	// retry.
	env := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: config.DefaultLocalPrincipal(), CommandType: "NoOpSample", Scope: ports.InstallationScope(),
		NormalizedPayload: []byte(`{}`), IdempotencyKey: "fixed-key", Now: fixedNow,
	})
	if env.Command.ID != "NoOpSample-fixed-key" {
		t.Fatalf("Command.ID = %q, want %q", env.Command.ID, "NoOpSample-fixed-key")
	}
	if env.Command.CorrelationID != env.Command.ID {
		t.Fatalf("Command.CorrelationID = %q, want it to equal Command.ID (%q)", env.Command.CorrelationID, env.Command.ID)
	}
}

func newEnvelope(ids idsource.Source, payload string, expectedVersion uint64) cli.Envelope {
	return cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: config.DefaultLocalPrincipal(), CommandType: "NoOpSample", Scope: ports.InstallationScope(),
		NormalizedPayload: []byte(payload), ExpectedVersion: expectedVersion, IDSource: ids, Now: fixedNow,
	})
}

// recordReceiptAndResult mirrors what a real application command handler
// does (WithSerializedWrite writes state + receipt atomically) — Dispatch
// itself never writes a receipt (see cli.Execute's own doc comment), so
// every test's own "execute" closure has to, exactly like a real leaf's
// application-layer command handler would.
func recordReceiptAndResult(t *testing.T, ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, result map[string]any, errorCode apperror.Code) (any, error) {
	t.Helper()
	var resultJSON string
	if result != nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}
		resultJSON = string(encoded)
	}
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey, CommandType: cmd.Type,
			RequestHash: cmd.RequestHash, ResultJSON: resultJSON, ErrorCode: string(errorCode), CreatedAt: cmd.RequestedAt,
		})
	})
	if err != nil {
		return nil, err
	}
	if errorCode != "" {
		return nil, &cli.CommandError{Code: errorCode, Message: "sample failed"}
	}
	return result, nil
}

func TestDispatchRunsExecuteOnFirstCall(t *testing.T) {
	uow := fake.New()
	env := newEnvelope(idsource.NewSequential("id"), `{"a":1}`, 0)
	calls := 0
	result, err := cli.Dispatch(context.Background(), uow, env.Command, func(ctx context.Context) (any, error) {
		calls++
		return recordReceiptAndResult(t, ctx, uow, env.Command, map[string]any{"ok": true}, "")
	})
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("execute called %d times, want 1", calls)
	}
	if result.Replayed {
		t.Fatal("Replayed = true on first call, want false")
	}
}

func TestDispatchReplaysSecondCallWithSameKeyAndHash(t *testing.T) {
	uow := fake.New()
	env := newEnvelope(idsource.NewSequential("id"), `{"a":1}`, 0)
	calls := 0
	execute := func(ctx context.Context) (any, error) {
		calls++
		return recordReceiptAndResult(t, ctx, uow, env.Command, map[string]any{"ok": true}, "")
	}

	if _, err := cli.Dispatch(context.Background(), uow, env.Command, execute); err != nil {
		t.Fatalf("first Dispatch() error = %v", err)
	}
	result, err := cli.Dispatch(context.Background(), uow, env.Command, execute)
	if err != nil {
		t.Fatalf("second Dispatch() error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("execute called %d times across 2 Dispatch calls, want 1 (second must replay, not re-execute)", calls)
	}
	if !result.Replayed {
		t.Fatal("Replayed = false on second call with identical key+hash, want true")
	}
	raw, ok := result.Result.(json.RawMessage)
	if !ok {
		t.Fatalf("Result type = %T, want json.RawMessage", result.Result)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode replayed result: %v", err)
	}
	if decoded["ok"] != true {
		t.Fatalf("replayed result = %v, want ok:true", decoded)
	}
}

func TestDispatchSameKeyDifferentHashConflicts(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	first := newEnvelope(ids, `{"a":1}`, 0)
	second := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: config.DefaultLocalPrincipal(), CommandType: "NoOpSample", Scope: ports.InstallationScope(),
		NormalizedPayload: []byte(`{"a":2}`), IdempotencyKey: first.Command.IdempotencyKey, IDSource: ids, Now: fixedNow,
	})

	execute := func(ctx context.Context) (any, error) {
		return recordReceiptAndResult(t, ctx, uow, first.Command, map[string]any{"ok": true}, "")
	}
	if _, err := cli.Dispatch(context.Background(), uow, first.Command, execute); err != nil {
		t.Fatalf("first Dispatch() error = %v", err)
	}

	_, err := cli.Dispatch(context.Background(), uow, second.Command, func(ctx context.Context) (any, error) {
		t.Fatal("execute must not run when the receipt hash conflicts")
		return nil, nil
	})
	if !errors.Is(err, cli.ErrReceiptHashConflict) {
		t.Fatalf("Dispatch() error = %v, want ErrReceiptHashConflict", err)
	}
}

func TestDispatchReplaysAStoredFailureAsCommandError(t *testing.T) {
	uow := fake.New()
	env := newEnvelope(idsource.NewSequential("id"), `{"a":1}`, 0)
	execute := func(ctx context.Context) (any, error) {
		return recordReceiptAndResult(t, ctx, uow, env.Command, nil, apperror.CodeInvalidArgument)
	}
	// The first call genuinely fails (this call IS the original failure
	// being recorded) — its own returned error is not what this test is
	// about, only that it leaves a failure receipt behind.
	if _, err := cli.Dispatch(context.Background(), uow, env.Command, execute); err == nil {
		t.Fatal("first Dispatch() error = nil, want the original failure")
	}

	_, err := cli.Dispatch(context.Background(), uow, env.Command, func(ctx context.Context) (any, error) {
		t.Fatal("execute must not run when replaying a stored failure")
		return nil, nil
	})
	var cmdErr *cli.CommandError
	if !errors.As(err, &cmdErr) {
		t.Fatalf("Dispatch() error = %v (%T), want *cli.CommandError", err, err)
	}
	if cmdErr.Code != apperror.CodeInvalidArgument {
		t.Fatalf("CommandError.Code = %q, want %q", cmdErr.Code, apperror.CodeInvalidArgument)
	}
}
