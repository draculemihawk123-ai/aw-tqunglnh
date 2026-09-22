package clicompose

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
)

// Execute runs one `aw <resource> <action> [flags]` invocation end to end
// and returns the process exit code — the whole one-shot CLI contract of
// ADR-028 in one function:
//
//  1. strip the global composition options (ParseGlobalOptions);
//  2. resolve the route from the leading words;
//  3. `-h/--help` prints the leaf's own flag help without opening anything;
//  4. a high-impact command (cli.Descriptor.HighImpact) passes the
//     confirmation gate BEFORE any dependency is built or dispatch happens:
//     --yes, or an interactive "y" at a TTY prompt; --json/non-interactive
//     without --yes is the typed PRECONDITION_FAILED failure
//     (confirmation=required) and touches nothing;
//  5. only then does factory build the dependencies the route needs and the
//     leaf's own Run* function run;
//  6. the outcome becomes an exit code (0/1/2, cli.ExitCodeFor) and, on
//     failure, exactly one typed error document on stdout in --json mode
//     (unless the leaf already wrote its own result document there — a
//     failure never adds a second document) or one `aw: ...` line on stderr
//     otherwise.
//
// Execute never writes to stdout except through the leaf or that one error
// document, so JSON consumers always get exactly one parseable document.
func Execute(ctx context.Context, factory DepsFactory, getenv func(string) string, args []string, streams IO) cli.ExitCode {
	return ExecuteWith(ctx, Routes(), cli.All(), factory, getenv, args, streams)
}

// ExecuteWith is Execute over an explicit route table and descriptor set —
// the seam the composition tests use to drive the real gate logic with
// instrumented routes, exactly like every other framework seam in
// internal/delivery/cli (Sleeper, ConfirmOptions.Interactive). Production
// always goes through Execute.
func ExecuteWith(ctx context.Context, routes []Route, descriptors []cli.Descriptor, factory DepsFactory, getenv func(string) string, args []string, streams IO) cli.ExitCode {
	stdout := &countingWriter{w: streams.Stdout}
	streams.Stdout = stdout
	jsonMode := hasFlag(args, "json")

	err := execute(ctx, routes, descriptors, factory, getenv, args, streams)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return cli.ExitSuccess
	}
	reportFailure(streams.Stderr, stdout, jsonMode, err)
	return exitCodeFor(err)
}

func exitCodeFor(err error) cli.ExitCode {
	if errors.Is(err, cli.ErrConfirmationRequired) {
		// A missing --yes is an invocation problem the operator fixes by
		// re-running with the flag, the same class as a missing required
		// flag — but it is deliberately reported with its own typed code
		// (PRECONDITION_FAILED), not INVALID_ARGUMENT.
		return cli.ExitUsage
	}
	return cli.ExitCodeFor(err)
}

func execute(ctx context.Context, routes []Route, descriptors []cli.Descriptor, factory DepsFactory, getenv func(string) string, args []string, streams IO) error {
	opts, rest, err := ParseGlobalOptions(args, getenv)
	if err != nil {
		return err
	}
	route, routeArgs, err := resolveRoute(routes, rest)
	if err != nil {
		return err
	}

	if isHelp(routeArgs) {
		return runHelp(ctx, route, routeArgs, streams)
	}

	if highImpact, err := routeIsHighImpact(route, descriptors); err != nil {
		return err
	} else if highImpact {
		gated, err := confirm(route, routeArgs, streams)
		if err != nil {
			return err
		}
		routeArgs = gated
	}

	deps, cleanup, err := factory(ctx, opts, route.Needs)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}
	return route.Run(ctx, &deps, routeArgs, streams)
}

// runHelp asks the leaf for its own flag help with an empty Deps: every
// leaf parses its flags before touching a dependency, so `-h` never needs a
// database. A leaf that would dereference a nil dependency first is caught
// and reported as generic usage instead of a panic.
func runHelp(ctx context.Context, route Route, args []string, streams IO) (err error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(streams.Stderr, "usage: aw %s [flags] (run with the required flags for details)\n", route.Name())
			err = flag.ErrHelp
		}
	}()
	runErr := route.Run(ctx, &Deps{}, args, streams)
	if runErr == nil {
		fmt.Fprintf(streams.Stderr, "usage: aw %s [flags]\n", route.Name())
		return flag.ErrHelp
	}
	return runErr
}

// routeIsHighImpact reports whether the route's descriptors demand
// confirmation, failing closed on a split verdict: two descriptors that
// share one route path (the definition dual-scope pair) must agree, since
// the operator types one command and cannot be half-confirmed.
func routeIsHighImpact(route Route, descriptors []cli.Descriptor) (bool, error) {
	var found bool
	var high bool
	for _, d := range descriptors {
		if strings.Join(d.Path, " ") != route.Name() {
			continue
		}
		if found && d.HighImpact != high {
			return false, fmt.Errorf("clicompose: descriptors for %q disagree on HighImpact — refusing to guess", route.Name())
		}
		found, high = true, d.HighImpact
	}
	return high, nil
}

// confirm applies the ADR-028 confirmation rule to a high-impact route and
// returns args with --yes removed (the leaf never defines that flag; the
// composition root owns it).
func confirm(route Route, args []string, streams IO) ([]string, error) {
	assumeYes := hasFlag(args, "yes")
	stripped := stripFlag(args, "yes")
	ok, err := cli.Confirm(cli.ConfirmOptions{
		Prompt:      fmt.Sprintf("aw %s %s is a high-impact command; proceed", route.Name(), strings.Join(stripped, " ")),
		AssumeYes:   assumeYes,
		Interactive: streams.Interactive,
		JSON:        hasFlag(args, "json"),
		Stdin:       streams.Stdin,
		Prompter:    streams.Stderr,
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrConfirmationDeclined
	}
	return stripped, nil
}

// countingWriter counts bytes the leaf wrote to stdout so a failure knows
// whether a result document already exists there.
type countingWriter struct {
	w interface{ Write([]byte) (int, error) }
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// reportFailure renders err. In --json mode with an untouched stdout it is
// the one typed error document (httpapi.ErrorResponse — the same wire shape
// and code vocabulary HTTP failures use); in every other case it is one
// line on stderr, so a result document already on stdout is never followed
// by a second one.
func reportFailure(stderr interface{ Write([]byte) (int, error) }, stdout *countingWriter, jsonMode bool, err error) {
	body := failureBody(err)
	if jsonMode && stdout.n == 0 {
		encoded, marshalErr := json.MarshalIndent(httpapi.ErrorResponse{Error: body}, "", "  ")
		if marshalErr == nil {
			fmt.Fprintln(stdout, string(encoded))
			return
		}
	}
	fmt.Fprintln(stderr, "aw:", body.Message)
}

// failureBody maps err into the typed error payload. Order matters: the
// confirmation refusal and usage errors are the CLI's own classes; an
// *apperror.Error reuses HTTP's own status-independent code mapping so a
// failing command reports the same code over HTTP and the CLI; everything
// else is INTERNAL carrying the leaf's own already-sanitized message.
func failureBody(err error) httpapi.ErrorBody {
	switch {
	case errors.Is(err, cli.ErrConfirmationRequired):
		return httpapi.ErrorBody{
			Code:    httpapi.ErrorCode(errorcode.CodePreconditionFailed),
			Message: err.Error(),
			Details: []httpapi.ErrorDetail{{Field: "confirmation", Message: "required"}},
		}
	case errors.Is(err, ErrConfirmationDeclined):
		return httpapi.ErrorBody{Code: httpapi.ErrorCodeConflict, Message: err.Error()}
	case cli.IsUsageError(err):
		return httpapi.ErrorBody{Code: httpapi.ErrorCodeInvalidRequest, Message: err.Error()}
	}
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		_, code := httpapi.StatusForAppErrorCode(appErr.Code)
		return httpapi.ErrorBody{Code: code, Message: appErr.Message}
	}
	return httpapi.ErrorBody{Code: httpapi.ErrorCodeInternal, Message: err.Error()}
}
