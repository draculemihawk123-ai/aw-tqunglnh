package diagnostics_test

// newTestServer composes a REAL net/http server around this package's own
// RegisterRoutes — mirrors internal/delivery/httpapi/recovery's own
// identical helper exactly.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/diagnostics"
)

const testActor = "operator-1"

func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: testActor, Roles: []string{"operator"}}
}

func newTestServer(t *testing.T, uow ports.UnitOfWork, isolation ports.IsolationEnforcementChecker, agents *agentregistry.Registry) *httptest.Server {
	t.Helper()
	return newTestServerWithPrincipal(t, uow, isolation, agents, testPrincipal())
}

// newTestServerWithPrincipal lets a test control the bound
// LocalPrincipalSnapshot directly — this package's own "wrong role" Verify
// proof (TestGetRunDiagnostics_HTTP_ArbitraryRoleStillReads) needs a
// principal holding a role with no special relationship to this query at
// all, to prove this read is not accidentally role-gated.
func newTestServerWithPrincipal(
	t *testing.T, uow ports.UnitOfWork, isolation ports.IsolationEnforcementChecker, agents *agentregistry.Registry, principal httpapi.LocalPrincipalSnapshot,
) *httptest.Server {
	t.Helper()
	registry := httpapi.NewRouteRegistry()
	diagnostics.RegisterRoutes(registry, diagnostics.Dependencies{UOW: uow, Isolation: isolation, Agents: agents})

	mux := http.NewServeMux()
	for _, d := range registry.Descriptors() {
		mux.HandleFunc(d.Method+" "+d.Path, d.Handler)
	}
	handler := httpapi.Chain(mux, httpapi.BindPrincipal(principal))

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
