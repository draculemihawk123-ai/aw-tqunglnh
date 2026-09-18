package workitem

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// createChildWorkItemBody is `work-item create-child`'s own request body
// shape — deliberately without parentWorkItemId (the leaf's own positional
// argument) or projectId: unlike `work-item create`, this subcommand binds
// no --project-id flag at all (see this file's own doc comment on Run
// below), mirroring
// internal/delivery/httpapi/workitem/workitem_commands.go's own
// createChildWorkItemBody.
type createChildWorkItemBody struct {
	Title            string           `json:"title"`
	ParentJoinPolicy string           `json:"parentJoinPolicy"`
	SourceNodeRunID  string           `json:"sourceNodeRunId,omitempty"`
	EffectiveScope   []scopeGrantBody `json:"effectiveScope"`
}

// RunWorkItemCreateChild implements `aw work-item create-child <parentWorkItemId>`
// — a mutation over workapp.CreateChildWorkItem. Deliberately no
// --project-id flag: workapp.CreateChildWorkItemRequest itself carries no
// ProjectID field at all — a child's ProjectID/FamilyID are always
// inherited from the parent's own already-persisted row (GC-INV-01), never
// re-declared by the caller — so this leaf never invents a --project-id
// flag nothing downstream would even check it against (a flag like that
// would either be silently ignored or, worse, misrepresent the real
// contract, exactly the class of mistake this task's own brief warns
// against for run cancel's own precedent). Instead, ProjectID is derived
// SOLELY by reloading the parent WorkItem's own real, stored row
// (loadWorkItemProjectID, helpers.go) — mirroring
// internal/delivery/cli/run/start.go's own identical
// "loadWorkItemProjectID... reload the WorkItem for its own authoritative
// ProjectID" discipline, and internal/delivery/httpapi/workitem's own
// handleCreateChildWorkItem (which reloads the parent via workapp.GetWorkItem
// before ever building a command, "không tin ID shape, payload hoặc
// projection").
func RunWorkItemCreateChild(ctx context.Context, deps Dependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item create-child", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	filePath := cli.BindFileFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw work-item create-child <parentWorkItemId> [--file <path>] (or pipe the JSON body via stdin)")
	}
	parentWorkItemID := positional[0]
	if strings.TrimSpace(parentWorkItemID) == "" {
		return errors.New("cli: work-item create-child: <parentWorkItemId> argument is required")
	}

	raw, err := cli.ReadBoundedInput(stdin, *filePath, cli.DefaultMaxInputBytes)
	if err != nil {
		return cli.UsageError{Err: err}
	}
	var body createChildWorkItemBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return usageErrorf("decode request body: %v", err)
	}
	if strings.TrimSpace(body.Title) == "" {
		return usageErrorf("title is required")
	}
	if strings.TrimSpace(body.ParentJoinPolicy) == "" {
		return usageErrorf("parentJoinPolicy is required")
	}
	if err := validateScopeGrantBodies("effectiveScope", body.EffectiveScope); err != nil {
		return err
	}
	normalized, err := json.Marshal(body)
	if err != nil {
		return err
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	projectID, err := loadWorkItemProjectID(ctx, deps.UoW, parentWorkItemID)
	if err != nil {
		return err
	}
	scope := ports.ProjectScope(projectID)

	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeCreateChildWorkItem, Scope: scope,
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return workapp.CreateChildWorkItem(ctx, deps.UoW, deps.IDs, envelope.Command, workapp.CreateChildWorkItemRequest{
			ParentWorkItemID: parentWorkItemID, Title: body.Title, ParentJoinPolicy: body.ParentJoinPolicy,
			SourceNodeRunID: body.SourceNodeRunID, EffectiveScope: toScopeGrantRequests(body.EffectiveScope),
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
