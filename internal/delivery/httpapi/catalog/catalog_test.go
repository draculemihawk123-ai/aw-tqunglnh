package catalog_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	httpcatalog "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/catalog"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// testActor is the LocalPrincipalSnapshot.Actor every test in this package
// dispatches as — mirroring receiptreplay_test.go's own literal "operator"
// actor.
const testActor = "operator-1"

// newTestHandler builds a real sqlite-backed http.Handler serving every
// route this package registers, wired the same way a real composition
// root would (RegisterRoutes into a fresh httpapi.RouteRegistry, then the
// exact mux-building loop server.go's own NewServer uses), wrapped with
// only httpapi.BindPrincipal — the one piece of request context this
// package's own handlers actually read. It deliberately skips the rest of
// NewServer's own middleware chain (HostOriginGuard, RequireSessionToken,
// CorrelationID, Recover, MaxBytes): those are V6-01/V6-01A's own already-
// proven concerns (server_test.go, security_test.go), not something this
// task's own tests need to re-prove — this harness isolates exactly what
// V6-03A added, per the design doc's own "isolated route/schema goldens"
// Verify line.
func newTestHandler(t *testing.T) (http.Handler, ports.UnitOfWork) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "httpapi-catalog.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	registry := httpapi.NewRouteRegistry()
	httpcatalog.RegisterRoutes(registry, httpcatalog.Dependencies{UoW: uow, IDs: idsource.Random{}})

	mux := http.NewServeMux()
	for _, d := range registry.Descriptors() {
		mux.HandleFunc(d.Method+" "+d.Path, d.Handler)
	}

	principal := httpapi.LocalPrincipalSnapshot{Actor: testActor, Roles: []string{"operator"}}
	return httpapi.BindPrincipal(principal)(mux), uow
}

// doRequest issues method/path with an optional JSON body against
// handler, setting Idempotency-Key/If-Match headers only when non-empty —
// the one shared low-level request builder every test in this package
// uses, so header-setting can never quietly drift between test files.
func doRequest(handler http.Handler, method, path, idempotencyKey, ifMatch, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set(httpapi.IdempotencyKeyHeader, idempotencyKey)
	}
	if ifMatch != "" {
		req.Header.Set(httpapi.IfMatchHeader, ifMatch)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// moveRepositoryToBlocked drives a freshly REGISTERING repository straight
// through REGISTERING->PROBING->BLOCKED via direct Catalog() calls against
// the real sqlite UnitOfWork — standing in for what V3-02's own
// REPOSITORY_PROBE job handler would otherwise do (no real Git-backed
// prober is wired into this HTTP test suite). Mirrors
// internal/app/catalog/commands_test.go's own moveRepositoryToBlocked
// helper exactly (same two transitions, same version progression),
// generalized to the ports.UnitOfWork interface so it runs against the
// real sqlite adapter this file's own tests use rather than the fake.
// Returns the Version the repository ends up at (3) so a test can build
// the correct If-Match.
func moveRepositoryToBlocked(t *testing.T, uow ports.UnitOfWork, repositoryID string) uint64 {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		}); err != nil {
			return err
		}
		code := "INVALID_ARGUMENT"
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryBlocked, LastProbeErrorCode: &code,
		})
		return err
	})
	if err != nil {
		t.Fatalf("moveRepositoryToBlocked(%s): %v", repositoryID, err)
	}
	return 3
}

// createTestProject dispatches a real POST /projects and returns the
// generated ProjectID — shared setup for every repository/component test
// in this package, never a direct Catalog() insert.
func createTestProject(t *testing.T, handler http.Handler, idempotencyKey, name string) string {
	t.Helper()
	rec := doRequest(handler, http.MethodPost, "/projects", idempotencyKey, "", `{"name":"`+name+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /projects = %d, body %s, want 201", rec.Code, rec.Body.String())
	}
	id := jsonField(t, rec.Body.String(), "projectId")
	if id == "" {
		t.Fatalf("POST /projects response %s missing projectId", rec.Body.String())
	}
	return id
}

// jsonField extracts one top-level string field from a JSON object body —
// a tiny, dependency-free helper so every test file does not need its own
// throwaway struct just to read one field back out.
func jsonField(t *testing.T, body, field string) string {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode JSON body %s: %v", body, err)
	}
	value, _ := raw[field].(string)
	return value
}
