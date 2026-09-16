package noderun

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"node-run", "retry-blocked"}, Scope: cli.ScopeProject,
		AppOperation: "RetryBlockedActivation", HTTPOperationID: "retryBlockedActivation",
	})
}

// loadPrincipal mirrors internal/delivery/cli/run's own identically-named,
// identically-implemented helper (run/shared.go) — duplicated rather than
// shared across these two sibling leaf-command packages, the same
// "sibling packages duplicate a small helper" discipline
// internal/delivery/httpapi/rundetail/fixture_test.go's own doc comment
// already establishes for test helpers, applied here to two sibling
// production leaf packages instead.
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

// RetryBlockedResult is `node-run retry-blocked`'s own JSON result — a
// camelCase-JSON projection of runtime.RetryBlockedActivationResult
// (which carries no json tags of its own), mirroring
// internal/delivery/httpapi/recovery's own RetryBlockedActivationResponse
// mapping discipline (retry.go there) exactly, including its own doc
// comment: exactly one of AlreadyRetried, Retried or FailureReason is ever
// populated — runtime.RetryBlockedActivationResult's own three-way
// outcome, verbatim, never collapsed or re-interpreted here.
type RetryBlockedResult struct {
	NodeRunID            string `json:"nodeRunId"`
	AlreadyRetried       bool   `json:"alreadyRetried"`
	Retried              bool   `json:"retried"`
	ReactivatedNodeRunID string `json:"reactivatedNodeRunId,omitempty"`
	FailureReason        string `json:"failureReason,omitempty"`
	FailureDetail        string `json:"failureDetail,omitempty"`
}

// RetryBlocked implements `aw node-run retry-blocked <nodeRunId>`: a direct
// dispatch to runtime.RetryBlockedActivationHandler.Retry
// (retry_blocked_activation.go) — mirrors
// internal/delivery/httpapi/recovery's own RetryBlockedActivationHTTPHandler
// exactly (retry.go there): no ports.Command/Idempotency-Key/If-Match at
// all (that handler's own doc comment: "none of them takes a
// ports.Command... confirmed by reading all three signatures before
// writing any handler here"), idempotent by the admission blocker's own
// State instead (AlreadyRetried covers a redelivered or concurrently-raced
// duplicate call).
//
// Written via cli.EncodeQueryResult, not cli.EncodeCommandResult/
// cli.ResultEnvelope, for the identical reason `run cancel` is
// (internal/delivery/cli/run/cancel.go's own doc comment): this command
// carries no idempotency key to report.
func RetryBlocked(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("node-run retry-blocked", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	reason := fs.String("reason", "", "reason for retrying this blocked activation (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	nodeRunID := fs.Arg(0)
	if strings.TrimSpace(nodeRunID) == "" {
		return errors.New("cli: node-run retry-blocked: <nodeRunId> argument is required")
	}
	if strings.TrimSpace(*reason) == "" {
		return errors.New("cli: node-run retry-blocked: --reason is required")
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	handler := runtime.NewRetryBlockedActivationHandler(deps.UOW, deps.IDs, deps.Isolation, deps.Agents)
	result, err := handler.Retry(ctx, runtime.RetryBlockedActivationRequest{
		NodeRunID: nodeRunID, Actor: principal.Actor, Reason: *reason, CorrelationID: deps.IDs.NewID(),
	})
	if err != nil {
		return err
	}

	return cli.EncodeQueryResult(stdout, RetryBlockedResult{
		NodeRunID: result.NodeRunID, AlreadyRetried: result.AlreadyRetried, Retried: result.Retried,
		ReactivatedNodeRunID: result.ReactivatedNodeRunID,
		FailureReason:        string(result.FailureReason), FailureDetail: result.FailureDetail,
	})
}
