package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunPackAssignmentList implements `aw pack-assignment list <componentId>`
// — a read-only query composing appcatalog.ListComponentPackAssignments
// (the full append-only history) and
// appcatalog.GetEffectiveComponentPackAssignment (which one, if any, is
// effective right now) into one packAssignmentListView, exactly like
// internal/delivery/httpapi/catalog/component.go's own
// listComponentPackAssignments. The Component is reloaded first (by ID
// alone, like GetRepository) both to learn its ProjectID for the response
// and to give a nonexistent componentId a real
// ports.ErrPersistenceNotFound rather than a silently empty list.
func RunPackAssignmentList(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("pack-assignment list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw pack-assignment list <componentId>")
	}
	componentID := positional[0]

	component, err := appcatalog.GetComponent(ctx, deps.UoW, componentID)
	if err != nil {
		return err
	}
	assignments, err := appcatalog.ListComponentPackAssignments(ctx, deps.UoW, componentID)
	if err != nil {
		return err
	}
	views := make([]packAssignmentView, 0, len(assignments))
	for _, a := range assignments {
		views = append(views, newPackAssignmentView(a))
	}

	now := time.Now().UTC()
	if deps.Now != nil {
		now = deps.Now()
	}
	// GetEffectiveComponentPackAssignment returns ports.ErrPersistenceNotFound
	// when the Component has simply never been assigned a pack yet — an
	// expected, common state (every Component starts this way), not a
	// query failure; Effective stays nil for exactly that one case, the
	// same discipline
	// internal/delivery/httpapi/catalog/component.go's own
	// listComponentPackAssignments already follows.
	var effectiveView *packAssignmentView
	effective, err := appcatalog.GetEffectiveComponentPackAssignment(ctx, deps.UoW, componentID, now)
	switch {
	case err == nil:
		view := newPackAssignmentView(effective)
		effectiveView = &view
	case errors.Is(err, ports.ErrPersistenceNotFound):
		// No assignment effective as of `now` — leave effectiveView nil.
	default:
		return err
	}

	return cli.EncodeQueryResult(stdout, packAssignmentListView{
		ComponentID: componentID, ProjectID: string(component.ProjectID),
		Assignments: views, Effective: effectiveView,
	})
}

// assignComponentPackRequestBody is `aw pack-assignment assign`'s own
// request body shape (read via --file/stdin), mirroring
// internal/delivery/httpapi/catalog/component.go's own
// assignComponentPackRequestBody: ComponentID/ProjectID/Actor are
// deliberately not fields here — ComponentID is the command's own
// positional argument, ProjectID is reloaded (see RunPackAssignmentAssign
// below), and Actor is authentication context read from the resolved
// principal (ADR-028: "Actor và ActorRoles ... MUST NOT được phép override
// actor/roles" — never a value a caller could set through the body).
// EffectiveAt is optional: an omitted value defaults to the moment this
// command is processed ("assign this pack effective right now"); an
// explicit value lets a caller pin a pack version effective in the past or
// scheduled for the future.
type assignComponentPackRequestBody struct {
	PackVersionID string     `json:"packVersionId"`
	EffectiveAt   *time.Time `json:"effectiveAt,omitempty"`
}

// RunPackAssignmentAssign implements `aw pack-assignment assign
// <componentId>` — a mutation over appcatalog.AssignComponentPack.
// PackVersionID is stored and returned verbatim — "assignment pin exact
// version" (this task's own "Thực hiện" line) — never resolved against
// "latest" or any other indirection this leaf could introduce.
func RunPackAssignmentAssign(ctx context.Context, deps Dependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("pack-assignment assign", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	filePath := cli.BindFileFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw pack-assignment assign <componentId> [--file <path>] (or pipe the JSON body via stdin)")
	}
	componentID := positional[0]

	component, err := appcatalog.GetComponent(ctx, deps.UoW, componentID)
	if err != nil {
		return err
	}

	raw, err := cli.ReadBoundedInput(stdin, *filePath, cli.DefaultMaxInputBytes)
	if err != nil {
		return cli.UsageError{Err: err}
	}
	var body assignComponentPackRequestBody
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

	scope := ports.ProjectScope(string(component.ProjectID))
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeAssignComponentPack, Scope: scope,
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	effectiveAt := envelope.Command.RequestedAt
	if body.EffectiveAt != nil {
		effectiveAt = body.EffectiveAt.UTC()
	}

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return appcatalog.AssignComponentPack(ctx, deps.UoW, deps.IDs, envelope.Command, appcatalog.AssignComponentPackRequest{
			ProjectID: string(component.ProjectID), ComponentID: componentID,
			PackVersionID: body.PackVersionID, EffectiveAt: effectiveAt, Actor: principal.Actor,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
