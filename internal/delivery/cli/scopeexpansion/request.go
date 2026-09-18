package scopeexpansion

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"scope-expansion", "request"}, Scope: cli.ScopeProject,
		AppOperation: "RequestScopeExpansion", HTTPOperationID: "requestScopeExpansion",
	})
}

const commandTypeRequestScopeExpansion = "RequestScopeExpansion"

// scopeGrantBody mirrors internal/delivery/httpapi/workitem's own
// scopeGrantBody (dto.go there) exactly — same field names, same json
// tags — so a request built this way canonicalizes to the identical bytes
// an equivalent HTTP request would.
type scopeGrantBody struct {
	RepositoryID string   `json:"repositoryId"`
	Access       string   `json:"access"`
	PathScopes   []string `json:"pathScopes,omitempty"`
	Reason       string   `json:"reason"`
}

func (g scopeGrantBody) toRequest() workapp.ScopeGrantRequest {
	return workapp.ScopeGrantRequest{RepositoryID: g.RepositoryID, Access: g.Access, PathScopes: g.PathScopes, Reason: g.Reason}
}

// requestScopeExpansionBody is `scope-expansion request`'s own --file/stdin
// wire body — mirrors internal/delivery/httpapi/workitem's own
// requestScopeExpansionBody exactly: deliberately without familyId (this
// invocation's own positional <familyId> argument) or requestId
// (work.RequestScopeExpansionRequest.RequestID's own doc comment: "A
// public/UI-facing caller must never be allowed to choose its own
// RequestID").
type requestScopeExpansionBody struct {
	RequestedGrants      []scopeGrantBody `json:"requestedGrants"`
	Reason               string           `json:"reason"`
	ReferencedWorkItemID string           `json:"referencedWorkItemId,omitempty"`
}

type requestFlags struct {
	principal, project, idempotencyKey, filePath *string
}

// bindRequestFlags is Request's own flag-binding step, factored out so
// flags_test.go can scan exactly the flags this command really binds
// without having to execute Request itself (this task's own "spoof" Verify
// bullet: an explicit test proving no --actor/--role flag exists anywhere
// on this command).
func bindRequestFlags(fs *flag.FlagSet) requestFlags {
	return requestFlags{
		principal:      cli.BindPrincipalFlag(fs),
		project:        cli.BindProjectFlag(fs),
		idempotencyKey: cli.BindIdempotencyKeyFlag(fs),
		filePath:       cli.BindFileFlag(fs),
	}
}

// Request implements `aw scope-expansion request <familyId> --project-id
// <projectId> [--idempotency-key <key>] [--file <path>]` (or pipe the JSON
// body via stdin) — a CREATE-shaped mutation over
// workapp.RequestScopeExpansion, mirroring
// internal/delivery/httpapi/workitem/scope_expansion_commands.go's own
// handleRequestScopeExpansion: the named TaskFamily is reloaded via
// workapp.GetTaskFamily FIRST (both to derive this invocation's own
// ports.CommandScope and to fail closed, leakage-normalized, on an unknown
// or cross-project familyId) before --idempotency-key or the body are ever
// read.
func Request(ctx context.Context, deps Dependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("scope-expansion request", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := bindRequestFlags(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw scope-expansion request <familyId> --project-id <projectId> [--file <path>] (or pipe the JSON body via stdin)")
	}
	familyID := positional[0]
	if strings.TrimSpace(*f.project) == "" {
		return usageErrorf("--project-id is required")
	}

	scope := ports.ProjectScope(*f.project)
	if _, err := workapp.GetTaskFamily(ctx, deps.UOW, scope, familyID); err != nil {
		return err
	}

	raw, err := cli.ReadBoundedInput(stdin, *f.filePath, cli.DefaultMaxInputBytes)
	if err != nil {
		return cli.UsageError{Err: err}
	}
	var body requestScopeExpansionBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return usageErrorf("decode request body: %v", err)
	}

	principal, err := loadPrincipal(*f.principal)
	if err != nil {
		return err
	}

	normalized, err := json.Marshal(body)
	if err != nil {
		return err
	}

	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeRequestScopeExpansion, Scope: scope,
		NormalizedPayload: normalized, IdempotencyKey: *f.idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	grants := make([]workapp.ScopeGrantRequest, 0, len(body.RequestedGrants))
	for _, g := range body.RequestedGrants {
		grants = append(grants, g.toRequest())
	}

	dispatched, err := cli.Dispatch(ctx, deps.UOW, envelope.Command, func(ctx context.Context) (any, error) {
		return workapp.RequestScopeExpansion(ctx, deps.UOW, deps.IDs, envelope.Command, workapp.RequestScopeExpansionRequest{
			FamilyID: familyID, RequestedGrants: grants, Reason: body.Reason, ReferencedWorkItemID: body.ReferencedWorkItemID,
			// RequestID deliberately left blank — see this file's own
			// requestScopeExpansionBody doc comment.
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
