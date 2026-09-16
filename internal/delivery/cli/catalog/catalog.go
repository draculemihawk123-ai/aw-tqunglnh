// Package catalog is V6-15D's own CLI leaf
// (docs/design/08-v6-api-projections.md V6-15D): "bootstrap Project/
// repository/component/pack catalog từ terminal" — the exact same
// internal/app/catalog application authority
// internal/delivery/httpapi/catalog already wraps for HTTP
// (CreateProject/RegisterRepository/RetryRepositoryProbe/
// AssignComponentPack and their read-only siblings), reshaped into `aw
// project/repository/component/pack-assignment ...` subcommands on top of
// internal/delivery/cli's own shared framework (V6-15B).
//
// This package owns its own Descriptor registrations (registered into
// cli.Default from its own init(), below) and its own tests — per the
// design doc's own §1 rule 8 ("Parallel work không sửa registry chung. Mỗi
// endpoint/CLI task sở hữu subpackage, descriptor/schema fragment và test
// riêng ... Chỉ V6-15O compose CLI/parity registry"). It deliberately never
// touches cmd/aw/main.go or cmd/aw/cli.go: routing real os.Args to this
// leaf's own Run* functions is V6-15O's own future composition-root job,
// not this task's — this package's own tests instead call those Run*
// functions directly (a real, in-process --json-style invocation, just not
// one reached by spawning the built binary), exactly the same "own
// package's OWN tests prove the descriptor + dispatch/query work" contract
// this task's own brief names.
//
// Every mutating Run* function follows the identical flow
// internal/delivery/cli's own doc.go describes and
// internal/delivery/httpapi/catalog's own handlers already established for
// HTTP: resolve the acting principal (config.LoadLocalPrincipalFile +
// config.ValidateLocalPrincipal, the exact mechanism `aw serve`'s own
// --principal-config flag already uses) → reload the authoritative target
// first when the command's own path does not already carry a ProjectID
// (GetRepository/GetComponent — see those functions' own doc comments in
// internal/app/catalog/commands.go for exactly when and why) → build a
// ports.Command via cli.BuildEnvelope → cli.Dispatch (which itself reuses
// internal/delivery/httpapi's own SemanticHash/LookupReceipt/
// ReconcileReceipt, so a receipt written by an HTTP call and a receipt
// written by a CLI call for an equivalent request are checked against the
// exact same replay authority) → cli.EncodeCommandResult. Every read-only
// Run* function calls the matching internal/app/catalog query directly and
// writes its own typed view (this file's sibling views.go) via
// cli.EncodeQueryResult — domain types (project.Project/Repository/
// Component/ComponentPackAssignment, ports.RepositoryProbeAttempt) carry no
// JSON tags of their own (they are not, and must never become, a wire
// contract this package does not control), so this package defines its own
// small, explicit, camelCase-tagged view types, mirroring
// internal/delivery/httpapi/catalog/views.go's own reasoning exactly.
//
// This package never calls internal/app/catalog.CreateComponent: V6-15D's
// own "Không làm" line explicitly forbids "no generic probe/component
// creation" — every Component this leaf can ever list was inserted by
// V3-02's own onboarding-probe worker, never by anything reachable from
// here.
package catalog

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Dependencies is what every Run* function in this package needs — the
// CLI-side counterpart of internal/delivery/httpapi/catalog.Dependencies.
// A future composition root (V6-15O) constructs one from its own
// already-built ports.UnitOfWork/idsource.Source and passes it into
// whichever Run* function it dispatches to; this package never reaches for
// a global or constructs either itself.
type Dependencies struct {
	UoW ports.UnitOfWork
	IDs idsource.Source
	// Now defaults to time.Now().UTC when nil, exactly like
	// cli.EnvelopeRequest.Now's own doc comment — a caller only needs to
	// supply this for deterministic tests.
	Now func() time.Time
}

// This block registers V6-15D's own ten Descriptors into cli.Default from
// this package's own init() — the "own package, own init()" discipline
// descriptor.go's own doc comment describes, so no shared registry file
// ever needs editing for this leaf to add itself. Path/Scope mirror the
// "Command surface to build" this task's own brief lists one for one;
// HTTPOperationID mirrors internal/delivery/httpapi/catalog/catalog.go's
// own RegisterRoutes OperationID values exactly, the same operation this
// leaf's own dispatch calls into by way of the shared
// internal/app/catalog application function AppOperation names.
func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"project", "list"}, Scope: cli.ScopeInstallation,
		AppOperation: appOpListProjects, HTTPOperationID: "projectsList",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"project", "create"}, Scope: cli.ScopeInstallation,
		AppOperation: commandTypeCreateProject, HTTPOperationID: "projectsCreate",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"project", "show"}, Scope: cli.ScopeProject,
		AppOperation: appOpGetProject, HTTPOperationID: "projectsGet",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository", "list"}, Scope: cli.ScopeProject,
		AppOperation: appOpListProjectRepositories, HTTPOperationID: "projectRepositoriesList",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository", "register"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeRegisterRepository, HTTPOperationID: "projectRepositoriesRegister",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository", "onboarding"}, Scope: cli.ScopeProject,
		AppOperation: appOpRepositoryOnboarding, HTTPOperationID: "repositoriesOnboarding",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository", "retry-probe"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeRetryRepositoryProbe, HTTPOperationID: "repositoriesRetryProbe",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"component", "list"}, Scope: cli.ScopeProject,
		AppOperation: appOpListComponents, HTTPOperationID: "projectComponentsList",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"pack-assignment", "list"}, Scope: cli.ScopeProject,
		AppOperation: appOpListComponentPackAssignments, HTTPOperationID: "componentPackAssignmentsList",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"pack-assignment", "assign"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeAssignComponentPack, HTTPOperationID: "componentPackAssignmentsAssign",
	})
}
