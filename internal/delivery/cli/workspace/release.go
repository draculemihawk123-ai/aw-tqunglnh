package workspace

import (
	"context"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacerelease"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"workspace-set", "release"}, Scope: cli.ScopeProject,
		AppOperation: "RequestWorkspaceSetRelease", HTTPOperationID: "requestWorkspaceSetRelease",
	})
}

// commandTypeRequestWorkspaceSetRelease is byte-for-byte identical to the
// commandType string literal internal/delivery/httpapi/workspacerelease.go's
// own requestWorkspaceSetReleaseHandler already passes to newWorkspaceCommand
// — required so an HTTP call and a CLI call for an equivalent request hash
// and replay identically (cli.BuildEnvelope's own doc comment).
const commandTypeRequestWorkspaceSetRelease = "RequestWorkspaceSetRelease"

// RunWorkspaceSetRelease implements `aw workspace-set release <familyId>
// --project-id <projectId> --expected-version <n> [--idempotency-key
// <key>]` — the full cli.BuildEnvelope/cli.Dispatch CommandEnvelope flow
// over workspacerelease.RequestWorkspaceSetRelease, mirroring
// internal/delivery/httpapi/workspacerelease.go's own
// requestWorkspaceSetReleaseHandler exactly: --expected-version is this
// leaf's own CLI equivalent of HTTP's required strong If-Match
// (cli.BindExpectedVersionFlag's own doc comment) — 0 (unset) is rejected
// here as a usage error before ever reaching
// workspacerelease.RequestWorkspaceSetRelease, which independently rejects
// a zero ExpectedVersion too (defense in depth, not a redundant check this
// leaf could safely skip: that function's own doc comment states this
// precondition is part of its own public contract, not merely an HTTP-side
// convenience).
//
// deps.Authority (ports.ReleaseEligibilityAuthority) is threaded straight
// through to workspacerelease.RequestWorkspaceSetRelease — this leaf never
// constructs its own; see Dependencies' own doc comment (doc.go) for why a
// composition root builds it once via work.NewEligibilityAuthority(uow).
func RunWorkspaceSetRelease(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("workspace-set release", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	projectID := cli.BindProjectFlag(fs)
	expectedVersion := cli.BindExpectedVersionFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw workspace-set release <familyId> --project-id <projectId> --expected-version <n>")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}
	familyID := positional[0]
	if *expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the WorkspaceSet version you observed, the CLI equivalent of HTTP's If-Match)")
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	scope := ports.ProjectScope(*projectID)
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeRequestWorkspaceSetRelease, Scope: scope,
		NormalizedPayload: []byte("{}"), ExpectedVersion: *expectedVersion,
		IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UOW, envelope.Command, func(ctx context.Context) (any, error) {
		return workspacerelease.RequestWorkspaceSetRelease(ctx, deps.UOW, deps.IDs, deps.Authority, envelope.Command, workspacerelease.RequestWorkspaceSetReleaseRequest{
			FamilyID: familyID, ProjectID: *projectID,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
