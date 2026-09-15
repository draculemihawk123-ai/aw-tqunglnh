package recovery_test

// newTestServer composes a REAL net/http server around this package's own
// RegisterRoutes — mirroring internal/delivery/httpapi/run's own identical
// helper (httptest_helper_test.go there) exactly, so every test in this
// package drives the actual registered route pattern through a real
// httptest.Server and a real *http.Client, never a bare handler func call.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/recovery"
)

const testActor = "operator-1"

func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: testActor, Roles: []string{"operator"}}
}

// newTestServer builds a test server with a real, always-passing isolation
// checker and an empty agent registry — the right default for every test in
// this package that exercises CancelWorkItem/ResolveWorkItemBlocker (which
// never consult either dependency). retry_test.go's own tests construct
// their own server directly via newTestServerWithDeps instead, since they
// need to control Isolation/Agents precisely.
func newTestServer(t *testing.T, uow ports.UnitOfWork, ids idsource.Source) *httptest.Server {
	t.Helper()
	return newTestServerWithDeps(t, uow, ids, fake.IsolationEnforcementChecker{}, agentregistry.Empty())
}

func newTestServerWithDeps(
	t *testing.T, uow ports.UnitOfWork, ids idsource.Source, isolation ports.IsolationEnforcementChecker, agents *agentregistry.Registry,
) *httptest.Server {
	t.Helper()
	registry := httpapi.NewRouteRegistry()
	recovery.RegisterRoutes(registry, recovery.Dependencies{UOW: uow, IDs: ids, Isolation: isolation, Agents: agents})

	mux := http.NewServeMux()
	for _, d := range registry.Descriptors() {
		mux.HandleFunc(d.Method+" "+d.Path, d.Handler)
	}
	handler := httpapi.Chain(mux, httpapi.BindPrincipal(testPrincipal()))

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func decodeErrorResponse(t *testing.T, resp *http.Response) httpapi.ErrorResponse {
	t.Helper()
	defer resp.Body.Close()
	var body httpapi.ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode ErrorResponse: %v", err)
	}
	return body
}

func mustReadAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
