package workitem

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Dependencies is everything this package's own subcommands need — the
// union of what internal/delivery/httpapi/workitem.Dependencies and
// internal/delivery/httpapi/recovery.Dependencies each separately need for
// the one operation of theirs (CancelWorkItem) this package's own cancel.go
// also wraps.
type Dependencies struct {
	// UoW is the one real ports.UnitOfWork every subcommand dispatches
	// through.
	UoW ports.UnitOfWork
	// IDs mints every fresh ID a subcommand needs: BuildEnvelope's own
	// generated --idempotency-key (create/create-child/mark-ready) and the
	// WorkItemCancellationIntent ID runtime.CancelWorkItem itself mints
	// (cancel).
	IDs idsource.Source
	// Now overrides BuildEnvelope's own default time source
	// (time.Now().UTC()) — nil in production, set only for deterministic
	// tests.
	Now func() time.Time
}

// This block registers this package's own seven Descriptors into
// cli.Default from this package's own init() — the "own package, own
// init()" discipline descriptor.go's own doc comment describes, so no
// shared registry file ever needs editing for this leaf to add itself.
// Path/Scope mirror the "Command surface to build" this task's own brief
// lists one for one; HTTPOperationID mirrors
// internal/delivery/httpapi/workitem/routes.go's and
// internal/delivery/httpapi/recovery/recovery.go's own RegisterRoutes
// OperationID values exactly — the same operation this leaf's own dispatch
// calls into by way of the shared application function AppOperation names.
func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "list"}, Scope: cli.ScopeProject,
		AppOperation: appOpListWorkItems, HTTPOperationID: "listWorkItems",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "show"}, Scope: cli.ScopeProject,
		AppOperation: appOpGetWorkItem, HTTPOperationID: "getWorkItem",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "create"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeCreateRootWorkItem, HTTPOperationID: "createRootWorkItem",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "create-child"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeCreateChildWorkItem, HTTPOperationID: "createChildWorkItem",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "readiness"}, Scope: cli.ScopeProject,
		AppOperation: appOpExplainWorkItemReadiness, HTTPOperationID: "getWorkItemReadiness",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "mark-ready"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeMarkWorkItemReady, HTTPOperationID: "markWorkItemReady",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "cancel"}, Scope: cli.ScopeProject,
		AppOperation: appOpCancelWorkItem, HTTPOperationID: "cancelWorkItem",
	})
}
