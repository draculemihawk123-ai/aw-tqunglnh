package decision_test

// newTestServer composes a REAL net/http server around this package's own
// RegisterRoutes — mirroring internal/delivery/httpapi/run's own
// httptest_helper_test.go exactly (see that file's own doc comment for why
// only httpapi.BindPrincipal is layered on top of the mux; V6-01/V6-01A's
// own HostOriginGuard/RequireSessionToken/CorrelationID/Recover/MaxBytes
// middleware are that task's own already-tested concerns, orthogonal to
// this package's own route logic).
//
// Unlike run's own single fixed testPrincipal, this package's own tests
// need TWO distinct principals — one carrying the "reviewer" role
// approvalDocument's own AuthorizedRoles names, one that deliberately does
// not — to exercise the "unauthorized role"/"spoof" Verify bullets, so
// newTestServer takes the principal as a parameter instead of hardcoding
// one.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/decision"
)

// reviewerPrincipal carries the exact role approvalDocument's own
// AuthorizedRoles=["reviewer"] authorizes.
func reviewerPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: "reviewer-1", Roles: []string{"reviewer"}}
}

// operatorPrincipal deliberately does NOT carry "reviewer" — this
// package's own "unauthorized role" fixture.
func operatorPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: "operator-1", Roles: []string{"operator"}}
}

func newTestServer(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, principal httpapi.LocalPrincipalSnapshot) *httptest.Server {
	t.Helper()
	registry := httpapi.NewRouteRegistry()
	decision.RegisterRoutes(registry, decision.Dependencies{UOW: uow, IDs: ids})

	mux := http.NewServeMux()
	for _, d := range registry.Descriptors() {
		mux.HandleFunc(d.Method+" "+d.Path, d.Handler)
	}
	handler := httpapi.Chain(mux, httpapi.BindPrincipal(principal))

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}
