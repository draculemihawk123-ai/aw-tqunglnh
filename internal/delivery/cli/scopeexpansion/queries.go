package scopeexpansion

import (
	"context"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"scope-expansion", "show"}, Scope: cli.ScopeProject,
		AppOperation: "GetScopeExpansionRequest", HTTPOperationID: "getScopeExpansionRequest",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"scope-expansion", "list"}, Scope: cli.ScopeProject,
		AppOperation: "ListFamilyScopeExpansionRequests", HTTPOperationID: "listFamilyScopeExpansionRequests",
	})
}

// scopeExpansionListResult wraps the collection in an object (rather than a
// bare top-level JSON array), mirroring internal/delivery/httpapi/workitem's
// own scopeExpansionRequestListResponse exactly.
type scopeExpansionListResult struct {
	Items []workapp.ScopeExpansionRequestDetail `json:"items"`
}

// Show implements `aw scope-expansion show <requestId> --project-id
// <projectId>` — the authoritative ScopeExpansionRequest detail, a pure read
// over workapp.GetScopeExpansionRequest mirroring
// internal/delivery/httpapi/workitem's own handleGetScopeExpansionRequest. The
// result carries the request's current Version, which a later `approve`,
// `reject` or `withdraw` supplies as --expected-version: this is how an
// operator learns it.
func Show(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("scope-expansion show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw scope-expansion show <requestId> --project-id <projectId>")
	}
	requestID := positional[0]
	if strings.TrimSpace(requestID) == "" {
		return usageErrorf("<requestId> argument is required")
	}
	if strings.TrimSpace(*projectID) == "" {
		return usageErrorf("--project-id is required")
	}

	detail, err := workapp.GetScopeExpansionRequest(ctx, deps.UOW, ports.ProjectScope(*projectID), requestID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, detail)
}

// List implements `aw scope-expansion list <familyId> --project-id
// <projectId>` — every ScopeExpansionRequest of one TaskFamily, every status,
// a pure read over workapp.ListFamilyScopeExpansionRequests mirroring
// handleListFamilyScopeExpansionRequests. It is the discovery step for `show`,
// `approve`, `reject` and `withdraw`: a PENDING request's id cannot be learned
// any other way.
func List(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("scope-expansion list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw scope-expansion list <familyId> --project-id <projectId>")
	}
	familyID := positional[0]
	if strings.TrimSpace(familyID) == "" {
		return usageErrorf("<familyId> argument is required")
	}
	if strings.TrimSpace(*projectID) == "" {
		return usageErrorf("--project-id is required")
	}

	requests, err := workapp.ListFamilyScopeExpansionRequests(ctx, deps.UOW, ports.ProjectScope(*projectID), familyID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, scopeExpansionListResult{Items: requests})
}
