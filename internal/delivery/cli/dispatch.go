package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// ErrReceiptHashConflict re-exports httpapi.ErrReceiptHashConflict under
// this package's own name (they are the identical sentinel value — see
// its assignment below), so a caller of Dispatch can errors.Is against it
// without also having to import internal/delivery/httpapi itself.
var ErrReceiptHashConflict = httpapi.ErrReceiptHashConflict

// CommandError is returned by Dispatch when a replayed receipt recorded a
// prior failure — the CLI-side counterpart of httpapi.WriteReceiptReplay's
// own error branch (receiptreplay.go). Code carries the original
// apperror.Code so a caller (eventually a composition-root dispatcher)
// can decide the right typed ExitCode, the same way an HTTP handler maps
// it to a status code via StatusForAppErrorCode — this package makes no
// such mapping itself.
type CommandError struct {
	Code    apperror.Code
	Message string
}

func (e *CommandError) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// Execute is the leaf-supplied closure Dispatch runs when no prior
// receipt replay applies: the real application command call. It must
// itself perform the real receipt write (WithSerializedWrite, exactly
// like every application command handler in this codebase already does —
// internal/app/catalog.CreateProject and siblings) — Dispatch itself
// never writes a receipt, mirroring LookupReceipt/ReconcileReceipt's own
// "never writes" contract (receiptreplay.go's own doc comment: "middleware
// không ghi/cache receipt"). This is a read-only latency/UX optimization
// for the common replay case, never a correctness dependency: even if
// Dispatch found no receipt here, the real application command's own
// transaction is still the one and only place a receipt is ever
// authoritatively checked-then-written.
type Execute func(ctx context.Context) (result any, err error)

// DispatchResult is Dispatch's own outcome: either a fresh result
// (Replayed false, Result is whatever execute returned) or a receipt hit
// whose stored success is being replayed verbatim (Replayed true, Result
// is a json.RawMessage decoded from the receipt's own ResultJSON, so
// EncodeCommandResult can nest it without double-encoding or re-shaping
// it).
type DispatchResult struct {
	Result   any
	Replayed bool
}

// Dispatch is the CLI-side equivalent of the HTTP flow V6-02 already
// established: look up a receipt for this exact (actor, scope,
// idempotency key, command type) via httpapi.LookupReceipt; if found,
// reconcile its RequestHash against this request's own hash via
// httpapi.ReconcileReceipt (ErrReceiptHashConflict on a mismatch — a
// caller reusing an idempotency key with a genuinely different payload)
// and replay its stored outcome verbatim (a *CommandError for a stored
// failure, DispatchResult{Replayed: true} for a stored success) — never
// re-running execute in either case. Otherwise it runs execute and
// returns its result unwrapped (Replayed false). Both LookupReceipt and
// ReconcileReceipt are the exact same functions the HTTP delivery layer
// uses (internal/delivery/httpapi/receiptreplay.go) — reused directly
// here, never reimplemented, so a receipt is checked against one single
// replay authority regardless of which delivery mechanism is asking.
func Dispatch(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, execute Execute) (DispatchResult, error) {
	receipt, found, err := httpapi.LookupReceipt(ctx, uow, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
	if err != nil {
		return DispatchResult{}, err
	}
	if found {
		if err := httpapi.ReconcileReceipt(receipt, cmd.RequestHash); err != nil {
			return DispatchResult{}, err
		}
		if receipt.ErrorCode != "" {
			return DispatchResult{}, &CommandError{Code: apperror.Code(receipt.ErrorCode), Message: "replayed: command previously failed"}
		}
		raw := json.RawMessage(receipt.ResultJSON)
		if len(raw) == 0 {
			raw = json.RawMessage("{}")
		}
		return DispatchResult{Result: raw, Replayed: true}, nil
	}

	result, err := execute(ctx)
	if err != nil {
		return DispatchResult{}, err
	}
	return DispatchResult{Result: result, Replayed: false}, nil
}
