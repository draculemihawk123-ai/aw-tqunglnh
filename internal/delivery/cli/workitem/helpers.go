package workitem

import (
	"context"
	"flag"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// Command-type constants for this package's own three
// cli.BuildEnvelope/cli.Dispatch-shaped mutations. Each value is
// byte-for-byte identical to the commandType string literal
// internal/delivery/httpapi/workitem's own handlers already pass to
// prepareCreateCommand/prepareUpdateCommand (workitem_commands.go,
// mark_ready_command.go) — the same hard requirement
// internal/delivery/cli/catalog/helpers.go's own identical constant block
// documents: an HTTP call and a CLI call for what should be "the same
// command" must hash and replay identically via the shared
// httpapi.SemanticHash/LookupReceipt/ReconcileReceipt authority, which only
// holds if both delivery mechanisms use the identical literal.
const (
	commandTypeCreateRootWorkItem  = "CreateRootWorkItem"
	commandTypeCreateChildWorkItem = "CreateChildWorkItem"
	commandTypeMarkWorkItemReady   = "MarkWorkItemReady"
)

// AppOperation constants for this package's own four non-envelope
// leaves — descriptor.go's own "fourth column of ADR-028's eventual
// inventory". Each name mirrors the internal/app function it calls
// directly (queries.go's GetWorkItem/ListWorkItems/ExplainWorkItemReadiness,
// runtime.CancelWorkItem).
const (
	appOpListWorkItems            = "ListWorkItems"
	appOpGetWorkItem              = "GetWorkItem"
	appOpExplainWorkItemReadiness = "ExplainWorkItemReadiness"
	appOpCancelWorkItem           = "CancelWorkItem"
)

// loadPrincipal resolves --principal-config into a validated
// config.LocalPrincipal — the same two-step (LoadLocalPrincipalFile then
// ValidateLocalPrincipal) `aw serve` itself performs (cmd/aw/serve.go).
// Deliberately duplicated rather than exported from a shared location —
// internal/delivery/cli/run/shared.go's own doc comment on loadPrincipal
// explains why sibling leaf-command packages duplicate this small helper
// rather than share one.
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

// usageErrorf builds a cli.UsageError from a formatted message — every
// bad-invocation condition in this package (wrong argument count, a
// required flag left empty) returns one of these rather than a bare error,
// mirroring internal/delivery/cli/catalog/helpers.go's own identical
// helper.
func usageErrorf(format string, args ...any) error {
	return cli.UsageError{Err: fmt.Errorf(format, args...)}
}

// parseFlags parses args into fs and wraps a parse failure as a
// cli.UsageError — mirroring internal/delivery/cli/catalog/helpers.go's own
// identical helper.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return err
		}
		return cli.UsageError{Err: err}
	}
	return nil
}

// loadWorkItemProjectID mirrors internal/delivery/cli/run/shared.go's own
// identically-named, identically-implemented helper: a read-only reload of
// workItemID's own real, current ProjectID, direct via tx.Work().GetWorkItem
// — never through workapp.GetWorkItem (which requires an already-known
// project scope this package's own create-child subcommand does not carry,
// exactly the same "no {projectId} to trust" shape
// internal/delivery/httpapi/workitem's own handleCreateChildWorkItem
// resolves by reloading the parent first). Used by create.go's sibling
// create-child.go so that leaf never trusts a caller-supplied project id for
// a request whose own application-layer shape
// (workapp.CreateChildWorkItemRequest) carries no ProjectID field at all —
// the child's ProjectID/FamilyID are always inherited from the parent's own
// stored row (GC-INV-01).
func loadWorkItemProjectID(ctx context.Context, uow ports.UnitOfWork, workItemID string) (string, error) {
	item, err := loadWorkItemRaw(ctx, uow, workItemID)
	if err != nil {
		return "", err
	}
	return string(item.ProjectID), nil
}

// loadWorkItemRaw reloads workItemID's own real, current row directly via
// tx.Work().GetWorkItem — never through workapp.GetWorkItem (queries.go),
// which requires an already-known project scope. Mirrors
// internal/delivery/httpapi/workitem/mark_ready_command.go's own
// loadWorkItemForMarkReady exactly, for the identical reason documented
// there: this package's own mark-ready subcommand has no {projectId}-style
// input to trust (ADR-028 §30/V6-04A's own route shape has no {projectId}
// segment at all), so ProjectID is derived SOLELY by reloading the
// WorkItem's own real, stored row — contract point 3's "không tin ID shape,
// payload hoặc projection" satisfied the same way: the server (here, this
// CLI leaf) derives Project/scope from the authoritative target, never from
// anything the caller supplies.
func loadWorkItemRaw(ctx context.Context, uow ports.UnitOfWork, workItemID string) (workdomain.WorkItem, error) {
	var item workdomain.WorkItem
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		item, err = tx.Work().GetWorkItem(ctx, workItemID)
		return err
	})
	return item, err
}
