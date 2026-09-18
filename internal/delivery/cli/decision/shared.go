package decision

import (
	"context"
	"flag"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// loadPrincipal resolves --principal-config into a validated
// config.LocalPrincipal — see internal/delivery/cli/scopeexpansion's own
// identically-implemented helper for the full rationale (duplicated here
// per this codebase's own "sibling packages duplicate a small helper"
// discipline).
func loadPrincipal(path string) (config.LocalPrincipal, error) {
	principal, err := config.LoadLocalPrincipalFile(path)
	if err != nil {
		return config.LocalPrincipal{}, err
	}
	if err := config.ValidateLocalPrincipal(principal); err != nil {
		return config.LocalPrincipal{}, err
	}
	return principal, nil
}

// usageErrorf builds a cli.UsageError — mirrors
// internal/delivery/cli/scopeexpansion's own usageErrorf exactly.
func usageErrorf(format string, args ...any) error {
	return cli.UsageError{Err: fmt.Errorf(format, args...)}
}

// parseFlags mirrors internal/delivery/cli/scopeexpansion's own parseFlags
// exactly.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return err
		}
		return cli.UsageError{Err: err}
	}
	return nil
}

// loadApprovalRequestForUpdate reloads approvalRequestID via a direct
// tx.Approvals().GetApprovalRequest read — mirrors
// internal/delivery/httpapi/decision/approval.go's own
// loadApprovalRequestForUpdate exactly (translated from an
// http.ResponseWriter short-circuit to a plain (value, error) return, the
// same translation internal/delivery/cli/definitions/helpers.go's own
// loadDefinitionInScope already applies to its own HTTP precedent): proves
// the request genuinely exists AND belongs to runID before this command
// ever reads Idempotency-Key/flags, and hands back its own ProjectID
// (this invocation's own ports.CommandScope) and current Version (the
// --expected-version precondition approval.go's own ResolveApproval
// checks) in one reload — never a second one.
func loadApprovalRequestForUpdate(ctx context.Context, uow ports.UnitOfWork, runID, approvalRequestID string) (runtimedomain.ApprovalRequest, error) {
	var request runtimedomain.ApprovalRequest
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var loadErr error
		request, loadErr = tx.Approvals().GetApprovalRequest(ctx, approvalRequestID)
		return loadErr
	})
	if err != nil {
		return runtimedomain.ApprovalRequest{}, err
	}
	if string(request.RunID) != runID {
		return runtimedomain.ApprovalRequest{}, fmt.Errorf("cli/decision: approval request %s belongs to run %s, not %s", approvalRequestID, request.RunID, runID)
	}
	return request, nil
}

// loadWaitRegistrationForRun reloads waitRegistrationID via a direct
// tx.Wait().GetWaitRegistration read — mirrors
// internal/delivery/httpapi/decision/wait.go's own
// loadWaitRegistrationForRun exactly, identical reasoning to
// loadApprovalRequestForUpdate above. Its Version is never compared
// against anything (see this package's own doc comment for why `wait
// signal` carries no --expected-version at all).
func loadWaitRegistrationForRun(ctx context.Context, uow ports.UnitOfWork, runID, waitRegistrationID string) (runtimedomain.WaitRegistration, error) {
	var registration runtimedomain.WaitRegistration
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var loadErr error
		registration, loadErr = tx.Wait().GetWaitRegistration(ctx, waitRegistrationID)
		return loadErr
	})
	if err != nil {
		return runtimedomain.WaitRegistration{}, err
	}
	if string(registration.RunID) != runID {
		return runtimedomain.WaitRegistration{}, fmt.Errorf("cli/decision: wait registration %s belongs to run %s, not %s", waitRegistrationID, registration.RunID, runID)
	}
	return registration, nil
}
