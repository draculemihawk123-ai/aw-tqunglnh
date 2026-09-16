package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// TestSampleNoOpLeafEndToEnd is V6-15B's own "no-op/sample descriptor
// test" (Phạm vi: "internal/delivery/cli framework and no-op/sample
// descriptor test"). No real domain leaf exists yet — every V6-15C+ task
// builds the first one — so this constructs a synthetic "sample no-op"
// leaf entirely inside this test file and drives it through every shared
// primitive this framework provides, end to end, against a real
// (in-memory) ports.UnitOfWork and the real httpapi receipt-replay
// functions Dispatch wraps: register a descriptor, build an envelope
// (with a generated idempotency key), dispatch it (first run executes for
// real, a retry with the identical envelope replays instead of
// re-executing), encode the JSON result, and poll a synthetic --wait
// observer to a terminal state — the same sequence a real `aw sample
// no-op` leaf would follow once one exists.
func TestSampleNoOpLeafEndToEnd(t *testing.T) {
	registry := cli.NewRegistry()
	descriptor := cli.Descriptor{
		Path: []string{"sample", "no-op"}, Scope: cli.ScopeInstallation,
		AppOperation: "SampleNoOp", HTTPOperationID: cli.CLILocalOperation,
	}
	if err := registry.Register(descriptor); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if got := registry.All(); len(got) != 1 || got[0].AppOperation != "SampleNoOp" {
		t.Fatalf("All() = %+v, want exactly the sample descriptor", got)
	}

	uow := fake.New()
	ids := idsource.NewSequential("sample")
	principal := config.DefaultLocalPrincipal()
	now := func() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC) }

	buildEnvelope := func() cli.Envelope {
		return cli.BuildEnvelope(cli.EnvelopeRequest{
			Principal: principal, CommandType: descriptor.AppOperation, Scope: ports.InstallationScope(),
			NormalizedPayload: []byte(`{"noop":true}`), IDSource: ids, Now: now,
		})
	}

	execute := func(cmd ports.Command) cli.Execute {
		return func(ctx context.Context) (any, error) {
			payload := map[string]any{"noop": true}
			encoded, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			writeErr := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
				return tx.Receipts().Record(ctx, ports.Receipt{
					Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey, CommandType: cmd.Type,
					RequestHash: cmd.RequestHash, ResultJSON: string(encoded), CreatedAt: cmd.RequestedAt,
				})
			})
			if writeErr != nil {
				return nil, writeErr
			}
			return payload, nil
		}
	}

	envelope := buildEnvelope()
	if !envelope.IdempotencyKeyGenerated {
		t.Fatal("sample leaf omitted --idempotency-key, so BuildEnvelope must have generated one")
	}

	first, err := cli.Dispatch(context.Background(), uow, envelope.Command, execute(envelope.Command))
	if err != nil {
		t.Fatalf("first Dispatch() error = %v", err)
	}
	if first.Replayed {
		t.Fatal("first run reported Replayed = true, want false")
	}

	var stdout bytes.Buffer
	if err := cli.EncodeCommandResult(&stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: first.Replayed, Result: first.Result,
	}); err != nil {
		t.Fatalf("EncodeCommandResult() error = %v", err)
	}
	if !strings.Contains(stdout.String(), envelope.Command.IdempotencyKey) {
		t.Fatalf("stdout does not carry back the generated idempotency key: %q", stdout.String())
	}

	// A deliberate retry with the exact same envelope (same generated key,
	// same payload -> same hash) must replay rather than execute again.
	second, err := cli.Dispatch(context.Background(), uow, envelope.Command, func(ctx context.Context) (any, error) {
		t.Fatal("retry with identical key+hash must replay, not execute again")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("replay Dispatch() error = %v", err)
	}
	if !second.Replayed {
		t.Fatal("retry with identical key+hash reported Replayed = false, want true")
	}

	// --wait against a synthetic observe function standing in for a real
	// job/run poll — proves the framework's own generic wait helper works
	// for a leaf that has nothing real to observe yet.
	polls := 0
	state, err := cli.Wait(context.Background(), func(ctx context.Context) (any, bool, error) {
		polls++
		return "DONE", polls >= 2, nil
	}, cli.WaitOptions{Interval: time.Millisecond})
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if state != "DONE" || polls != 2 {
		t.Fatalf("Wait() state=%v polls=%d, want DONE after exactly 2 polls", state, polls)
	}

	// A high-impact confirmation gate a sample leaf might use before its
	// own mutation: noninteractive requires --yes.
	if _, err := cli.Confirm(cli.ConfirmOptions{Prompt: "run sample no-op?", Interactive: false}); err == nil {
		t.Fatal("Confirm() error = nil for a noninteractive session without --yes, want ErrConfirmationRequired")
	}
	confirmed, err := cli.Confirm(cli.ConfirmOptions{AssumeYes: true})
	if err != nil || !confirmed {
		t.Fatalf("Confirm() with --yes = (%v, %v), want (true, nil)", confirmed, err)
	}
}
