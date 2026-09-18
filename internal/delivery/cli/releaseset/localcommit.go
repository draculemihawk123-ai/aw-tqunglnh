package releaseset

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/releasesetcommit"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// requestLocalCommitHashPayload mirrors
// internal/delivery/httpapi/releaseset/local_commit_commands.go's own
// requestReleaseSetLocalCommitBody byte-for-byte — same field names, same
// json tags, deliberately excluding ProjectID/ReleaseSetID (both travel as
// --project-id/--release-set-id flags instead, the identical "path segment
// never re-declared in the hashed body" convention
// internal/delivery/cli/run/start.go's own startRunHashPayload documents at
// length) — so an HTTP call and an equivalent CLI call for "the same"
// RequestReleaseSetLocalCommit hash and replay identically
// (cli.BuildEnvelope's own doc comment).
type requestLocalCommitHashPayload struct {
	ExpectedReleaseSetVersion uint64 `json:"expectedReleaseSetVersion"`
	RepositoryWorkspaceID     string `json:"repositoryWorkspaceId"`
	ExpectedWorkspaceVersion  uint64 `json:"expectedWorkspaceVersion"`
	Message                   string `json:"message"`
	AuthorName                string `json:"authorName"`
	AuthorEmail               string `json:"authorEmail"`
}

// LocalCommitResult is `release-set local-commit`'s own JSON result payload
// (nested inside cli.ResultEnvelope.Result):
// releasesetcommit.RequestReleaseSetLocalCommitResult verbatim, plus the
// final observed workapp.ReleaseSetLocalCommitStatus when --wait was set
// and a terminal state (COMMITTED or FAILED) was reached before any
// timeout/interrupt. Mirrors internal/delivery/cli/run.StartResult exactly.
type LocalCommitResult struct {
	releasesetcommit.RequestReleaseSetLocalCommitResult
	// Wait is populated only when --wait was set and cli.Wait returned a
	// terminal observation — nil on a --wait timeout/interrupt, exactly
	// like run.StartResult.Wait's own identical contract.
	Wait *workapp.ReleaseSetLocalCommitStatus `json:"wait,omitempty"`
}

// RunLocalCommit implements
//
//	aw release-set local-commit --project-id <projectId> --release-set-id <id>
//	  --expected-release-set-version <n> --repository-workspace-id <id>
//	  --expected-workspace-version <n> --message <text> --author-name <name>
//	  --author-email <email> [--wait] [--wait-timeout <duration>]
//
// — the full cli.BuildEnvelope/cli.Dispatch CommandEnvelope flow over
// releasesetcommit.RequestReleaseSetLocalCommit, mirroring
// internal/delivery/httpapi/releaseset/local_commit_commands.go's own
// handleRequestReleaseSetLocalCommit for the request itself.
//
// RequestReleaseSetLocalCommit does ONLY database work inside one
// transaction — it never touches real Git (see this package's own doc
// comment for the full producer/consumer split this mirrors). Without
// --wait this leaf returns almost immediately with State: "REQUESTED" and
// a JobID; --wait polls workapp.GetReleaseSetLocalCommitStatus — a pure
// read query — until the operation reaches a terminal state (COMMITTED or
// FAILED) or --wait-timeout elapses/the context is cancelled, built by
// copying internal/delivery/cli/run/start.go's own `run start --wait`
// wiring exactly: the ObserveFunc below calls nothing but
// GetReleaseSetLocalCommitStatus, so a timed-out or interrupted --wait can
// never itself retry the mutation or touch real Git — see localcommit_test.go
// for the explicit proof (a forced FAILED state observed via --wait without
// ever re-dispatching the request). A --wait outcome (success, timeout, or
// interrupt) never changes this leaf's own already-committed request
// result: the JSON result envelope is always written to stdout once the
// request itself has succeeded (fresh or replayed), regardless of what
// --wait subsequently observes — the identical "diagnostics to stderr, the
// waitErr is returned only AFTER encoding" ordering start.go's own doc
// comment explains at length.
//
// This is NOT internal/delivery/cli/decision.SignalWait — that is a
// completely different, unrelated "wait" (a WAIT-node external-signal
// command). This function calls cli.Wait for a purely observational poll
// of an async job's own status; it never signals or resumes anything.
func RunLocalCommit(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("release-set local-commit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	projectID := cli.BindProjectFlag(fs)
	releaseSetID := fs.String("release-set-id", "", "release set this local commit belongs to (required)")
	expectedReleaseSetVersion := fs.Uint64("expected-release-set-version", 0, "expected current ReleaseSet version — the exact-revision fence (required, must be > 0)")
	repositoryWorkspaceID := fs.String("repository-workspace-id", "", "repository workspace to commit in (required)")
	expectedWorkspaceVersion := fs.Uint64("expected-workspace-version", 0, "expected current RepositoryWorkspace version — the exact-revision fence (required, must be > 0)")
	message := fs.String("message", "", "commit message (required)")
	authorName := fs.String("author-name", "", "commit author name (required)")
	authorEmail := fs.String("author-email", "", "commit author email (required)")
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	wait, waitTimeout := cli.BindWaitFlags(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw release-set local-commit --project-id <projectId> --release-set-id <id> --expected-release-set-version <n> --repository-workspace-id <id> --expected-workspace-version <n> --message <text> --author-name <name> --author-email <email>")
	}
	if *projectID == "" {
		return usageErrorf("--project-id is required")
	}
	if strings.TrimSpace(*releaseSetID) == "" {
		return usageErrorf("--release-set-id is required")
	}
	if *expectedReleaseSetVersion == 0 {
		return usageErrorf("--expected-release-set-version is required and must be greater than zero")
	}
	if strings.TrimSpace(*repositoryWorkspaceID) == "" {
		return usageErrorf("--repository-workspace-id is required")
	}
	if *expectedWorkspaceVersion == 0 {
		return usageErrorf("--expected-workspace-version is required and must be greater than zero")
	}
	if strings.TrimSpace(*message) == "" {
		return usageErrorf("--message is required")
	}
	if strings.TrimSpace(*authorName) == "" {
		return usageErrorf("--author-name is required")
	}
	if strings.TrimSpace(*authorEmail) == "" {
		return usageErrorf("--author-email is required")
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	hashPayload, err := json.Marshal(requestLocalCommitHashPayload{
		ExpectedReleaseSetVersion: *expectedReleaseSetVersion, RepositoryWorkspaceID: *repositoryWorkspaceID,
		ExpectedWorkspaceVersion: *expectedWorkspaceVersion, Message: *message, AuthorName: *authorName, AuthorEmail: *authorEmail,
	})
	if err != nil {
		return fmt.Errorf("cli: release-set local-commit: marshal hash payload: %w", err)
	}

	scope := ports.ProjectScope(*projectID)
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeRequestReleaseSetLocalCommit, Scope: scope,
		NormalizedPayload: hashPayload, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return releasesetcommit.RequestReleaseSetLocalCommit(ctx, deps.UoW, deps.IDs, envelope.Command, releasesetcommit.RequestReleaseSetLocalCommitRequest{
			ProjectID: *projectID, ReleaseSetID: *releaseSetID, ExpectedReleaseSetVersion: *expectedReleaseSetVersion,
			RepositoryWorkspaceID: *repositoryWorkspaceID, ExpectedWorkspaceVersion: *expectedWorkspaceVersion,
			Message: *message, AuthorName: *authorName, AuthorEmail: *authorEmail,
		})
	})
	if err != nil {
		return err
	}

	requestResult, err := decodeLocalCommitRequestResult(dispatched.Result)
	if err != nil {
		return err
	}

	result := LocalCommitResult{RequestReleaseSetLocalCommitResult: requestResult}
	var waitErr error
	if *wait {
		observed, err := cli.Wait(ctx, func(ctx context.Context) (any, bool, error) {
			status, err := workapp.GetReleaseSetLocalCommitStatus(ctx, deps.UoW, requestResult.ReleaseSetLocalCommitID)
			if err != nil {
				return nil, false, err
			}
			return status, isTerminalLocalCommitState(status.State), nil
		}, cli.WaitOptions{Timeout: *waitTimeout, Sleep: deps.Sleep})
		switch {
		case err != nil:
			waitErr = err
			cli.Diagnosticf(stderr, "release set local commit %s: --wait did not observe a terminal state: %v", requestResult.ReleaseSetLocalCommitID, err)
		default:
			if status, ok := observed.(workapp.ReleaseSetLocalCommitStatus); ok {
				result.Wait = &status
			}
		}
	}

	if encErr := cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: result,
	}); encErr != nil {
		return encErr
	}
	return waitErr
}

// decodeLocalCommitRequestResult normalizes cli.Dispatch's own two possible
// shapes for DispatchResult.Result: a fresh execution returns the typed
// releasesetcommit.RequestReleaseSetLocalCommitResult verbatim, while a
// replay returns a json.RawMessage decoded from the stored receipt —
// mirrors internal/delivery/cli/run/start.go's own decodeStartResult
// exactly.
func decodeLocalCommitRequestResult(raw any) (releasesetcommit.RequestReleaseSetLocalCommitResult, error) {
	switch v := raw.(type) {
	case releasesetcommit.RequestReleaseSetLocalCommitResult:
		return v, nil
	case json.RawMessage:
		var result releasesetcommit.RequestReleaseSetLocalCommitResult
		if err := json.Unmarshal(v, &result); err != nil {
			return releasesetcommit.RequestReleaseSetLocalCommitResult{}, fmt.Errorf("cli: release-set local-commit: decode replayed result: %w", err)
		}
		return result, nil
	default:
		return releasesetcommit.RequestReleaseSetLocalCommitResult{}, fmt.Errorf("cli: release-set local-commit: unexpected dispatch result type %T", raw)
	}
}

// isTerminalLocalCommitState reports whether state is one of the two states
// past which a ReleaseSetLocalCommit will never again change on its own —
// see workdomain.ReleaseSetLocalCommitState's own closed
// REQUESTED/COMMITTED/FAILED set (internal/domain/work/release_set_local_commit.go).
func isTerminalLocalCommitState(state string) bool {
	return state == "COMMITTED" || state == "FAILED"
}
