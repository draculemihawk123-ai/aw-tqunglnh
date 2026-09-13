// Package catalog is V6-03A's own HTTP route slice
// (docs/design/08-v6-api-projections.md V6-03A): "expose Project catalog,
// repository onboarding và discovered component/pack assignment" over the
// real internal/app/catalog application authority (CreateProject/
// RegisterRepository/RetryRepositoryProbe/AssignComponentPack and their
// read-only siblings). It is the first endpoint task to actually exist, so
// it is also the first to follow the design doc's own §1.8 contract
// ("Mỗi endpoint/CLI task sở hữu subpackage, descriptor/schema fragment và
// test riêng"): this package owns its own route descriptors, request/
// response DTOs and tests, and a composition root (cmd/aw/serve.go) only
// ever calls RegisterRoutes once — exactly the extension point V6-01A's
// own composition root already left behind ("a later endpoint task's own
// composition-root wiring adds its own routes.Register call here without
// needing to touch this file's shared setup").
//
// Every handler in this package follows the one flow
// internal/delivery/httpapi's own commandenvelope.go/receiptreplay.go doc
// comments describe: authenticate/authorize (principal read from context,
// never from the request) → reload the authoritative target when the
// route's own path does not already carry a ProjectID (see
// internal/app/catalog.GetRepository/GetComponent's own doc comments for
// exactly when and why a bare `/repositories/{id}` or
// `/components/{id}/...` route needs this and a `/projects/{id}/...` route
// does not) → RequireIdempotencyKey (and RequireIfMatch for the one
// update-shaped mutation, retry-probe) → CanonicalizeJSON → SemanticHash →
// LookupReceipt → replay via WriteReceiptReplay, conflict via
// ErrReceiptHashConflict, or — only once the receipt is absent — check any
// current-state precondition and dispatch the real application command →
// EncodeResult. This package never opens a write transaction or records a
// receipt itself (internal/archtest's own
// TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt already walks the whole
// internal/delivery/httpapi tree, this subpackage included, and enforces
// exactly that) and never calls internal/app/catalog.CreateComponent
// (V6-03A's own "Không làm: ... expose helper CreateComponent" — proven by
// internal/archtest's own TestHTTPAPICatalogNeverCallsCreateComponent, the
// same AST-scan idiom).
package catalog

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// maxRequestBodyBytes bounds every JSON body this package's handlers
// canonicalize — mirroring internal/delivery/httpapi/receiptreplay_test.go's
// own buildCreateProjectCommand, the established precedent for a catalog
// command's own body size (small identity/name payloads, never a binary/
// content upload). The composition root's own Config.MaxBodyBytes
// (server.go's MaxBytes middleware) already bounds the raw connection
// before this package ever reads a byte; this is a second, package-local
// bound independent of whatever that outer value happens to be configured
// as, exactly like CanonicalizeJSON's own limitBytes parameter is designed
// for.
const maxRequestBodyBytes = 1 << 20

// Dependencies is what RegisterRoutes needs to wire every route in this
// package — the composition root passes its own already-constructed
// ports.UnitOfWork/idsource.Source in; this package never reaches for a
// global or constructs either itself.
type Dependencies struct {
	UoW ports.UnitOfWork
	IDs idsource.Source
}

// handler holds Dependencies for every method below — unexported so no
// other package can construct one directly except through RegisterRoutes.
type handler struct {
	uow ports.UnitOfWork
	ids idsource.Source
}

// RegisterRoutes registers every V6-03A route into registry. A composition
// root calls this once, after its own shared setup (health/bootstrap),
// passing the same ports.UnitOfWork/idsource.Source it already
// constructed.
func RegisterRoutes(registry *httpapi.RouteRegistry, deps Dependencies) {
	h := &handler{uow: deps.UoW, ids: deps.IDs}

	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects", OperationID: "projectsCreate",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: h.createProject,
	})
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects", OperationID: "projectsList",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: h.listProjects,
	})
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{id}", OperationID: "projectsGet",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: h.getProject,
	})

	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{id}/repositories", OperationID: "projectRepositoriesRegister",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: h.registerRepository,
	})
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{id}/repositories", OperationID: "projectRepositoriesList",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: h.listProjectRepositories,
	})
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/repositories/{id}", OperationID: "repositoriesGet",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: h.getRepository,
	})
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/repositories/{id}/onboarding", OperationID: "repositoriesOnboarding",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: h.getRepositoryOnboarding,
	})
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/repositories/{id}/retry-probe", OperationID: "repositoriesRetryProbe",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: h.retryRepositoryProbe,
	})

	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{id}/components", OperationID: "projectComponentsList",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: h.listProjectComponents,
	})
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/components/{id}/pack-assignments", OperationID: "componentPackAssignmentsList",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: h.listComponentPackAssignments,
	})
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/components/{id}/pack-assignments", OperationID: "componentPackAssignmentsAssign",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: h.assignComponentPack,
	})
}
