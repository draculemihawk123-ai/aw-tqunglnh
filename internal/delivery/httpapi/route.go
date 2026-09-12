// Package httpapi is V6-01's own composition: a production loopback HTTP
// server with lifecycle, health, bounded decode/error and a route
// registration primitive, so a later endpoint task (V6-03A onward) never
// has to edit a shared router file to add its own routes
// (docs/design/08-v6-api-projections.md V6-01's own "Phạm vi"). It
// deliberately implements no business endpoint, browser security token or
// receipt store — those are V6-01A/V6-02/V6-02A and the endpoint tasks
// themselves.
package httpapi

import (
	"fmt"
	"net/http"
	"sync"
)

// ScopeKind names which of ADR-025's two closed command-scope kinds a
// route belongs to (docs/architecture/02-architecture-decisions.md
// ADR-025: CommandScope = INSTALLATION | PROJECT(ProjectID)). A later
// authorization task reads this off the matched route's own descriptor
// rather than re-deriving it from the path.
type ScopeKind string

const (
	ScopeInstallation ScopeKind = "INSTALLATION"
	ScopeProject      ScopeKind = "PROJECT"
)

// RouteDescriptor is the metadata every registered route carries — the
// exact field set V6-01's own design doc line names: "{Method, Path,
// OperationID, ScopeKind, RequestSchema, ResponseSchema, Handler}".
// RequestSchema/ResponseSchema are deliberately opaque (any): V6-01's own
// "Không làm" excludes implementing business endpoints or an OpenAPI
// model here — V6-02A defines the real shared schema-fragment contract,
// and V6-12 is the only task that ever reads these values instead of just
// checking they are present. A route-owning task supplies whatever
// concrete schema value later becomes appropriate once V6-02A lands;
// until then, any non-nil placeholder satisfies registration.
type RouteDescriptor struct {
	Method         string
	Path           string
	OperationID    string
	ScopeKind      ScopeKind
	RequestSchema  any
	ResponseSchema any
	Handler        http.HandlerFunc
}

type routeKey struct {
	Method string
	Path   string
}

// RouteRegistry maps (Method, Path) to a registered RouteDescriptor.
// Register is meant to be called at composition-root startup, before the
// server begins serving: a duplicate (Method, Path) registration, or one
// missing required metadata, panics immediately — a programming error to
// catch at boot, never a runtime condition a caller branches on, mirroring
// internal/app/eventschema.Registry and internal/app/workerpool.Registry's
// own identical "Register panics on duplicate" discipline (V6-01's own
// Verify line: "descriptor trùng fail").
type RouteRegistry struct {
	mu     sync.RWMutex
	routes map[routeKey]RouteDescriptor
	order  []routeKey
}

// NewRouteRegistry returns an empty RouteRegistry.
func NewRouteRegistry() *RouteRegistry {
	return &RouteRegistry{routes: map[routeKey]RouteDescriptor{}}
}

// Register adds d to the registry. It panics if any required field is
// empty/nil, or if (d.Method, d.Path) was already registered.
func (r *RouteRegistry) Register(d RouteDescriptor) {
	if d.Method == "" {
		panic("httpapi: RouteDescriptor.Method is required")
	}
	if d.Path == "" {
		panic("httpapi: RouteDescriptor.Path is required")
	}
	if d.OperationID == "" {
		panic(fmt.Sprintf("httpapi: RouteDescriptor.OperationID is required (%s %s)", d.Method, d.Path))
	}
	if d.ScopeKind != ScopeInstallation && d.ScopeKind != ScopeProject {
		panic(fmt.Sprintf("httpapi: RouteDescriptor.ScopeKind is required and must be INSTALLATION or PROJECT (%s %s)", d.Method, d.Path))
	}
	if d.RequestSchema == nil {
		panic(fmt.Sprintf("httpapi: RouteDescriptor.RequestSchema is required (%s %s)", d.Method, d.Path))
	}
	if d.ResponseSchema == nil {
		panic(fmt.Sprintf("httpapi: RouteDescriptor.ResponseSchema is required (%s %s)", d.Method, d.Path))
	}
	if d.Handler == nil {
		panic(fmt.Sprintf("httpapi: RouteDescriptor.Handler is required (%s %s)", d.Method, d.Path))
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	key := routeKey{Method: d.Method, Path: d.Path}
	if _, exists := r.routes[key]; exists {
		panic(fmt.Sprintf("httpapi: route %s %s already registered", d.Method, d.Path))
	}
	r.routes[key] = d
	r.order = append(r.order, key)
}

// Descriptors returns every registered RouteDescriptor, in registration
// order. Callers must not mutate the returned slice's Handler identity
// expectations — it is a read-only snapshot.
func (r *RouteRegistry) Descriptors() []RouteDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RouteDescriptor, 0, len(r.order))
	for _, key := range r.order {
		out = append(out, r.routes[key])
	}
	return out
}
