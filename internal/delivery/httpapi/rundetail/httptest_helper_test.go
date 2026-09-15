package rundetail_test

// newTestServer composes a REAL net/http server around this package's own
// RegisterRoutes — mirrors internal/delivery/httpapi/run's own identical
// helper (httptest_helper_test.go) exactly: every test in this file drives
// the actual registered route pattern (including the {id} wildcard
// extraction) through a real httptest.Server and a real *http.Client,
// never a bare handler func call.
//
// Only httpapi.BindPrincipal is layered on top of the mux — V6-01/V6-01A's
// own session-token/Host/Origin guard middleware is that task's own
// already-tested concern, orthogonal to this package's own route logic.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/rundetail"
)

const testActor = "operator-1"

func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: testActor, Roles: []string{"operator"}}
}

func newTestServer(t *testing.T, uow ports.UnitOfWork, matcher redact.Matcher) *httptest.Server {
	t.Helper()
	registry := httpapi.NewRouteRegistry()
	cursor := httpapi.NewCursorCodec([]byte("test-rundetail-cursor-signing-secret"))
	rundetail.RegisterRoutes(registry, rundetail.Dependencies{UnitOfWork: uow, Matcher: matcher, Cursor: cursor})

	mux := http.NewServeMux()
	for _, d := range registry.Descriptors() {
		mux.HandleFunc(d.Method+" "+d.Path, d.Handler)
	}
	handler := httpapi.Chain(mux, httpapi.BindPrincipal(testPrincipal()))

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}
