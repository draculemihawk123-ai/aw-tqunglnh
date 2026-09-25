package apicontract

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// wantRouteCount is a pinned, explicit snapshot of how many routes the
// real production composition (internal/delivery/httpcompose.ComposeRoutes,
// called with every one of its 19+ leaf packages' own RegisterRoutes)
// registers today. TestRouteInventory_ExactCount fails the moment this
// number changes for ANY reason — a leaf task adding/removing a route, a
// leaf task being wired into ComposeRoutes for the first time, a
// composition-root regression — forcing a developer to consciously update
// this constant (and, ideally, extend mustContainSample below) rather than
// silently letting the registered route set drift unnoticed.
const wantRouteCount = 90

// mustContainSample spot-checks a representative sample of descriptors
// spanning the full ComposeRoutes call sequence: the three inline
// health/bootstrap routes registered first, then one route from each of
// several leaf packages spread across early/middle/late in that sequence
// (see internal/delivery/httpcompose/compose.go's own call order).
var mustContainSample = []struct {
	method, path, operationID string
}{
	{http.MethodGet, "/health/live", "healthLive"},
	{http.MethodGet, "/health/ready", "healthReady"},
	{http.MethodGet, "/", "bootstrap"},
	{http.MethodPost, "/projects", "projectsCreate"},
	{http.MethodPost, "/projects/{projectId}/work-items", "createRootWorkItem"},
	{http.MethodPost, "/work-items/{workItemId}/runs", "startWorkflowRun"},
	{http.MethodGet, "/runs/{id}", "getRunDetail"},
	{http.MethodGet, "/doctor", "doctor"},
	{http.MethodGet, "/projects/{projectId}/work-items/kanban", "listWorkItemKanban"},
	{http.MethodGet, "/projects/{id}/events/watch", "watchProjectEvents"},
	{http.MethodPost, "/projects/{id}/projection/rebuild", "requestProjectionRebuild"},
}

// TestRouteInventory_ExactCount is V6-12's own "exact route inventory"
// Verify bullet: it proves the real composition root
// (internal/delivery/httpcompose.ComposeRoutes, built here with real
// temporary infrastructure exactly the way cmd/aw/serve.go builds it at
// real startup — see composetest_test.go's own buildRealRegistry)
// produces exactly wantRouteCount descriptors, and that every entry in
// mustContainSample is present with the exact operationId expected.
//
// Uniqueness of (Method,Path) and of OperationID across the whole set is
// NOT re-tested here: httpapi.RouteRegistry.Register already panics on
// either duplicate at registration time (route.go, exercised by
// route_test.go's own dedicated tests) — buildRealRegistry calling
// ComposeRoutes without panicking, which every test in this package does,
// is already a live proof that this specific, real 88-route set contains
// no duplicate of either kind.
func TestRouteInventory_ExactCount(t *testing.T) {
	routes := buildRealRegistry(t)
	descriptors := routes.Descriptors()

	if len(descriptors) != wantRouteCount {
		t.Errorf("got %d registered routes, want exactly %d — if this is an intentional route "+
			"addition/removal, update wantRouteCount (and testdata/golden/contract.json) after review",
			len(descriptors), wantRouteCount)
	}

	byKey := make(map[string]httpapi.RouteDescriptor, len(descriptors))
	for _, d := range descriptors {
		byKey[d.Method+" "+d.Path] = d
	}
	for _, want := range mustContainSample {
		d, ok := byKey[want.method+" "+want.path]
		if !ok {
			t.Errorf("missing expected route %s %s (operationId %s)", want.method, want.path, want.operationID)
			continue
		}
		if d.OperationID != want.operationID {
			t.Errorf("%s %s: operationId = %q, want %q", want.method, want.path, d.OperationID, want.operationID)
		}
	}
}

// pathParamPattern matches one `{param}` segment placeholder in a
// RouteDescriptor.Path, e.g. "{id}" or "{workItemId}".
var pathParamPattern = regexp.MustCompile(`\{[^{}]+\}`)

// concretePath instantiates path (a registration-time pattern such as
// "/projects/{id}/repositories") into a real, requestable URL path by
// substituting every `{param}` segment with a fixed, non-empty literal —
// net/http.ServeMux's own pattern matching only cares that SOME non-empty
// segment occupies that position, never its actual value.
func concretePath(path string) string {
	return pathParamPattern.ReplaceAllString(path, "x")
}

// TestRouteInventory_ServedEqualsDeclaredBothDirections is V6-12's own
// "reverse-check that served routes exactly equal declared routes (both
// directions)" Thực hiện line, and the Verify bullet's own "should also...
// include one test proving no route exists in the real server that isn't
// in your generated contract and vice versa" instruction.
//
// internal/delivery/httpapi.NewServer's own construction (server.go) is a
// single loop — "mux := http.NewServeMux(); for _, d := range
// cfg.Routes.Descriptors() { mux.HandleFunc(d.Method+" "+d.Path,
// d.Handler) }" — with no other call anywhere in that package ever
// registering a pattern into that mux. This test reproduces that EXACT
// same loop directly against net/http.ServeMux (the real stdlib router,
// not a fake), skipping only NewServer's own security-middleware wrapping
// (HostOriginGuard/RequireSessionToken/CorrelationID/Recover/MaxBytes —
// V6-01/V6-01A's own already-proven concerns, exercised end-to-end by
// server_test.go/security_test.go; not this task's own concern, and
// orthogonal to whether a route is registered at all), the same
// deliberate scope-narrowing internal/delivery/httpapi/catalog's own
// newTestHandler test helper already established for isolated route
// testing in this codebase.
func TestRouteInventory_ServedEqualsDeclaredBothDirections(t *testing.T) {
	routes := buildRealRegistry(t)
	descriptors := routes.Descriptors()

	mux := http.NewServeMux()
	for _, d := range descriptors {
		mux.HandleFunc(d.Method+" "+d.Path, d.Handler)
	}

	// Direction 1: every DECLARED route is actually SERVED. For each
	// descriptor, ask the real mux which registered pattern would handle a
	// concrete request for that exact method + path (every {param}
	// substituted with a literal), and assert it is the SAME pattern
	// NewServer itself would have registered it under — i.e. the mux
	// actually dispatches this exact operation to itself, not to some
	// other, unexpectedly-broader pattern.
	for _, d := range descriptors {
		req := httptest.NewRequest(d.Method, concretePath(d.Path), nil)
		_, pattern := mux.Handler(req)
		wantPattern := d.Method + " " + d.Path
		if pattern != wantPattern {
			t.Errorf("operationId %s: request %s %s matched mux pattern %q, want %q",
				d.OperationID, d.Method, req.URL.Path, pattern, wantPattern)
		}
	}

	// Direction 2: nothing is SERVED that was not DECLARED. Because the
	// loop above is the only place anything is ever registered into mux
	// (mirroring NewServer's own single loop), this direction holds by
	// construction — the checks below additionally prove it operationally
	// for a handful of concrete "this must NOT match anything" cases,
	// the same way a route that was accidentally over-registered (e.g. a
	// typo'd path swallowing more than intended) would actually be caught
	// here.
	//
	// Every case below deliberately avoids GET: "GET /" (bootstrap) is a
	// real, registered net/http.ServeMux SUBTREE pattern (a trailing-slash
	// pattern matches every path with no more specific match — stdlib's
	// own documented behavior, not a V6-12 bug), so ANY GET request that
	// does not match a more specific route legitimately falls through to
	// it. That is real, existing production behavior this test must not
	// contradict — the meaningful "not served" proof here is a
	// method/path combination no descriptor registers at all.
	mustNotMatch := []struct{ method, path string }{
		{http.MethodPost, "/this-route-was-never-registered"},
		{http.MethodDelete, "/projects"},     // /projects only registers GET/POST
		{http.MethodDelete, "/health/live"},  // /health/live only registers GET
		{http.MethodPatch, "/settings/safe"}, // /settings/safe only registers GET/PUT
	}
	for _, c := range mustNotMatch {
		req := httptest.NewRequest(c.method, c.path, nil)
		_, pattern := mux.Handler(req)
		if pattern != "" {
			t.Errorf("request %s %s unexpectedly matched mux pattern %q — a route is being served "+
				"that was never declared via ComposeRoutes/RouteRegistry.Register",
				c.method, c.path, pattern)
		}
	}
}

// TestUndocumentedOperationsSnapshot pins CheckUndocumentedOperations' own
// current, reviewed result — see that function's own doc comment
// (uxgap.go) for why this is a deliberately coarser signal than
// CheckUXGaps' own hard gate (uxgap_test.go): every operationId below was
// manually reviewed and confirmed to be an expected companion/sibling
// route (a project-scoped mirror of a global definition endpoint, a
// detail-fetch GET alongside an already-documented list GET, and similar)
// that docs/design/11-v6-00-ux-artifact.md never enumerated as its own
// separate Actions/Queries row, rather than a genuinely undocumented
// concept. A CHANGE to this list — in either direction — fails the test
// and forces a conscious review of the new/removed entry, exactly like a
// golden file.
func TestUndocumentedOperationsSnapshot(t *testing.T) {
	rows := parseRealUXDoc(t)
	contract := buildRealContract(t)

	got := CheckUndocumentedOperations(rows, contract)
	want := []string{
		"createProjectDefinition",
		"diffProjectDefinitionVersions",
		"getAdapterBuild",
		"getMessageContextSnapshot",
		"getProjectDefinition",
		"getProjectDefinitionVersion",
		"getProjectionStatus",
		"getScopeExpansionRequest",
		"getTaskFamily",
		"getWorkItemProjectedDetail",
		"listProjectDefinitionVersions",
		"listWorkItems",
		"publishProjectDefinitionVersion",
		"repositoriesOnboarding",
		"resolveWorkItemBlocker",
		"staticAsset",
		"uiShell",
		"validateProjectDefinitionDraft",
	}
	assertStringSliceEqual(t, "CheckUndocumentedOperations", got, want)
}
