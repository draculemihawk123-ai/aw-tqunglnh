package catalog

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"strings"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/readinesscheck"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// V9-08 (gap G8, docs/design/12-v9-harness-alignment.md): `aw repository
// readiness ...`, the CLI half of the repository's readiness profile and
// baseline. Each leaf calls the same internal/app/readinesscheck function the
// HTTP route of the same name calls (internal/delivery/httpapi/catalog/
// readiness.go), through the same envelope/receipt flow as every other command
// in this package: reload the repository first (its path carries no project),
// resolve the principal, build the command, dispatch, encode the result.

const (
	commandTypeSetRepositoryReadinessProfile = "SetRepositoryReadinessProfile"
	commandTypeRequestBaselineCheck          = "RequestBaselineCheck"
	commandTypeAcceptBaselineException       = "AcceptBaselineException"

	appOpGetRepositoryReadiness = "GetRepositoryReadiness"
)

// setReadinessProfileRequestBody mirrors the HTTP request body field for field,
// so an HTTP call and a CLI call for the same profile hash alike.
type setReadinessProfileRequestBody struct {
	Setup        *readinesscheck.CommandInput `json:"setup,omitempty"`
	Verification readinesscheck.CommandInput  `json:"verification"`
}

type verifyReadinessRequestBody struct{}

type acceptBaselineExceptionRequestBody struct {
	BaselineAttemptID string `json:"baselineAttemptId"`
	Reason            string `json:"reason"`
}

// RunRepositoryReadinessShow implements `aw repository readiness show
// <repositoryId>`: the profile (if any) and, for each workspace, the state of
// its baseline, with the latest attempt's classification and output.
func RunRepositoryReadinessShow(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository readiness show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	repositoryID, err := onePositional(fs, "usage: aw repository readiness show <repositoryId>")
	if err != nil {
		return err
	}
	repo, err := appcatalog.GetRepository(ctx, deps.UoW, repositoryID)
	if err != nil {
		return err
	}
	view, err := readinesscheck.GetRepositoryReadiness(ctx, deps.UoW, string(repo.ProjectID), repositoryID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, view)
}

// RunRepositoryReadinessSet implements `aw repository readiness set
// [--file <path>] <repositoryId>` (or the JSON body on stdin): declare or
// change the repository's readiness profile. The body is
//
//	{"setup": {"executable": "npm", "argv": ["ci"], "timeoutSeconds": 600},
//	 "verification": {"executable": "npm", "argv": ["test"], "timeoutSeconds": 600}}
//
// "setup" is optional. A changed profile starts a fresh baseline on every READY
// workspace of the repository.
func RunRepositoryReadinessSet(ctx context.Context, deps Dependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository readiness set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	filePath := cli.BindFileFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	repositoryID, err := onePositional(fs, "usage: aw repository readiness set [--file <path>] <repositoryId> (or pipe the JSON body via stdin)")
	if err != nil {
		return err
	}
	repo, err := appcatalog.GetRepository(ctx, deps.UoW, repositoryID)
	if err != nil {
		return err
	}
	raw, err := cli.ReadBoundedInput(stdin, *filePath, cli.DefaultMaxInputBytes)
	if err != nil {
		return cli.UsageError{Err: err}
	}
	var body setReadinessProfileRequestBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return usageErrorf("decode request body: %v", err)
	}
	normalized, err := json.Marshal(body)
	if err != nil {
		return err
	}
	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeSetRepositoryReadinessProfile, Scope: ports.ProjectScope(string(repo.ProjectID)),
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})
	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return readinesscheck.SetRepositoryReadinessProfile(ctx, deps.UoW, deps.IDs, envelope.Command, readinesscheck.SetRepositoryReadinessProfileRequest{
			ProjectID: string(repo.ProjectID), RepositoryID: repositoryID, Setup: body.Setup, Verification: body.Verification,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}

// RunRepositoryReadinessVerify implements `aw repository readiness verify
// <repositoryId>`: run the baseline again on every READY workspace of the
// repository (after fixing it, or to see a failure reproduce). The result of
// the check is read back with `show` once a worker has run it.
func RunRepositoryReadinessVerify(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository readiness verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	repositoryID, err := onePositional(fs, "usage: aw repository readiness verify <repositoryId>")
	if err != nil {
		return err
	}
	repo, err := appcatalog.GetRepository(ctx, deps.UoW, repositoryID)
	if err != nil {
		return err
	}
	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}
	normalized, err := json.Marshal(verifyReadinessRequestBody{})
	if err != nil {
		return err
	}
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeRequestBaselineCheck, Scope: ports.ProjectScope(string(repo.ProjectID)),
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})
	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return readinesscheck.RequestBaselineCheck(ctx, deps.UoW, deps.IDs, envelope.Command, readinesscheck.RequestBaselineCheckRequest{
			ProjectID: string(repo.ProjectID), RepositoryID: repositoryID,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}

// RunRepositoryReadinessAcceptException implements `aw repository readiness
// accept-exception --attempt-id <id> --reason <text> <repositoryId>`: admit
// writers on a workspace whose baseline failed, recorded with who accepted it
// and why. The failed attempt stays on the record; the acceptance covers that
// attempt only.
func RunRepositoryReadinessAcceptException(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository readiness accept-exception", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	attemptID := fs.String("attempt-id", "", "the failed baseline attempt to accept (shown by `aw repository readiness show`)")
	reason := fs.String("reason", "", "why the failure is accepted (required; recorded with your name)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	repositoryID, err := onePositional(fs, "usage: aw repository readiness accept-exception --attempt-id <id> --reason <text> <repositoryId>")
	if err != nil {
		return err
	}
	if strings.TrimSpace(*attemptID) == "" || strings.TrimSpace(*reason) == "" {
		return usageErrorf("--attempt-id and --reason are required")
	}
	repo, err := appcatalog.GetRepository(ctx, deps.UoW, repositoryID)
	if err != nil {
		return err
	}
	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}
	normalized, err := json.Marshal(acceptBaselineExceptionRequestBody{BaselineAttemptID: *attemptID, Reason: *reason})
	if err != nil {
		return err
	}
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeAcceptBaselineException, Scope: ports.ProjectScope(string(repo.ProjectID)),
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})
	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return readinesscheck.AcceptBaselineException(ctx, deps.UoW, deps.IDs, envelope.Command, readinesscheck.AcceptBaselineExceptionRequest{
			ProjectID: string(repo.ProjectID), RepositoryID: repositoryID, BaselineAttemptID: *attemptID, Reason: *reason,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}

// onePositional returns the single positional argument fs has left, or the
// usage error.
func onePositional(fs *flag.FlagSet, usage string) (string, error) {
	positional := fs.Args()
	if len(positional) != 1 || strings.TrimSpace(positional[0]) == "" {
		return "", usageErrorf("%s", usage)
	}
	return positional[0], nil
}
