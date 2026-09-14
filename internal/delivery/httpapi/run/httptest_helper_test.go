package run_test

// newTestServer composes a REAL net/http server around this package's own
// RegisterRoutes — mirroring internal/delivery/httpapi/server.go's own
// mux.HandleFunc(d.Method+" "+d.Path, d.Handler) composition exactly, so
// every test in this package drives the actual registered route pattern
// (including the {workItemId}/{runId} wildcard extraction this package
// hand-wrote) through a real httptest.Server and a real *http.Client, never
// a bare handler func call that would only assert on this package's own
// good faith about what net/http.ServeMux does with its patterns.
//
// Only httpapi.BindPrincipal is layered on top of the mux — V6-01/V6-01A's
// own HostOriginGuard/RequireSessionToken/CorrelationID/Recover/MaxBytes
// middleware are that task's own already-tested concerns, orthogonal to
// this package's own route logic, and would only add unrelated ceremony
// (a session token, an exact Host header) to every request this file sends.
// A handler that reads httpapi.CorrelationIDFromContext without that
// middleware ever having run simply observes "" (its own documented safe
// default) — never a test failure.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/run"
)

const testActor = "operator-1"

func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: testActor, Roles: []string{"operator"}}
}

func newTestServer(t *testing.T, uow ports.UnitOfWork, ids idsource.Source) *httptest.Server {
	t.Helper()
	registry := httpapi.NewRouteRegistry()
	run.RegisterRoutes(registry, run.Dependencies{UOW: uow, IDs: ids})

	mux := http.NewServeMux()
	for _, d := range registry.Descriptors() {
		mux.HandleFunc(d.Method+" "+d.Path, d.Handler)
	}
	handler := httpapi.Chain(mux, httpapi.BindPrincipal(testPrincipal()))

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}
