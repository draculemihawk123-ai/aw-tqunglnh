package kanban_test

// Real HTTP round-trip coverage for V6-10 (docs/design/08-v6-api-projections.md):
// every test in this file drives a REAL httpapi.Server backed by a REAL
// *sqlite.Store — never a mock, never a direct row fabrication for anything
// that has a real public command (this repo's own hard rule) — mirroring
// internal/delivery/httpapi/workitem/workitem_test.go's own newTestEnv
// idiom exactly. The one deliberate exception, matching that same file's
// own seedProject/seedActiveRepository precedent ("fixture setup for a
// concern a DIFFERENT task owns, not this task's own behavior under test"):
// projection rows/checkpoints/generations (V6-08/V6-08A's own live
// consumer, not yet built as of this task) are seeded directly via
// tx.Projections(), and a family's RepositoryWorkspace rows (V3-06's own
// provisioning worker, never actually run here) are seeded directly via
// tx.Work().CreateRepositoryWorkspace — everything else (Project/Repository/
// WorkItem/TaskFamily/WorkspaceSet) is created through the REAL public
// application commands this task's own dependencies (V6-00, V6-04A) already
// require to exist.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/kanban"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

const testSessionToken = "test-kanban-session-token"
const testCursorSecret = "test-kanban-cursor-secret-0123456789"

func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}
}

// testEnv is one real Server + real UnitOfWork pair, torn down via
// t.Cleanup.
type testEnv struct {
	server *httpapi.Server
	base   string
	client *http.Client
	uow    ports.UnitOfWork
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "kanban-http.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	cursorCodec := httpapi.NewCursorCodec([]byte(testCursorSecret))

	reg := httpapi.NewRouteRegistry()
	kanban.RegisterRoutes(reg, kanban.Dependencies{UnitOfWork: uow, Cursor: cursorCodec})

	server, err := httpapi.NewServer(httpapi.Config{
		Host: "127.0.0.1", Port: 0, Routes: reg, IDs: idsource.Random{},
		Logger:       logging.New(io.Discard, logging.JSON, redact.NewMatcher()),
		MaxBodyBytes: 1 << 20, Token: testSessionToken, Principal: testPrincipal(),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	t.Cleanup(func() {
		if err := server.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Serve returned error after Shutdown: %v", err)
		}
	})

	return &testEnv{server: server, base: "http://" + server.Addr(), client: &http.Client{Timeout: 10 * time.Second}, uow: uow}
}

// get issues a real GET request against e's own real server.
func (e *testEnv) get(t *testing.T, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.base+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(httpapi.SessionTokenHeader, testSessionToken)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func decodeInto(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}

// seedProject mirrors internal/app/work/commands_sqlite_test.go's own
// seedProjectSQLite exactly.
func (e *testEnv) seedProject(t *testing.T, id string) {
	t.Helper()
	err := e.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: "project " + id})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

// seedActiveRepository mirrors workitem_test.go's own seedActiveRepository
// exactly: RegisterRepository (the real public command) then a direct
// REGISTERING->PROBING->ACTIVE drive standing in for V3-02's own probe
// worker.
func (e *testEnv) seedActiveRepository(t *testing.T, projectID, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	regCmd := ports.Command{
		ID: "cmd-reg-" + repositoryID, IdempotencyKey: "idem-reg-" + repositoryID, Actor: "seed",
		CorrelationID: "seed", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "RegisterRepository", RequestHash: "hash-reg-" + repositoryID,
	}
	if _, err := catalog.RegisterRepository(ctx, e.uow, idsource.Random{}, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: repositoryID,
		RemoteLocator: "https://example.invalid/" + repositoryID + ".git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository(%s): %v", repositoryID, err)
	}
	err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		}); err != nil {
			return err
		}
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryActive, LastProbeErrorCode: nil,
		})
		return err
	})
	if err != nil {
		t.Fatalf("activate repository %s: %v", repositoryID, err)
	}
}

// createRoot creates a real root WorkItem (+TaskFamily+WorkspaceSet intent)
// via the real public CreateRootWorkItem command — every repositoryID must
// already be seedActiveRepository'd.
func (e *testEnv) createRoot(t *testing.T, projectID, title string, grants ...workapp.ScopeGrantRequest) workapp.CreateRootWorkItemResult {
	t.Helper()
	cmd := ports.Command{
		ID: "cmd-root-" + title, IdempotencyKey: "idem-root-" + title, Actor: "seed",
		CorrelationID: "seed", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "CreateRootWorkItem", RequestHash: "hash-root-" + title,
	}
	result, err := workapp.CreateRootWorkItem(context.Background(), e.uow, idsource.Random{}, cmd, workapp.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: title, InitialScope: grants,
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem(%s): %v", title, err)
	}
	return result
}

// createChild creates a real child WorkItem via the real public
// CreateChildWorkItem command.
func (e *testEnv) createChild(t *testing.T, projectID, parentWorkItemID, title string, effectiveScope ...workapp.ScopeGrantRequest) workapp.CreateChildWorkItemResult {
	t.Helper()
	cmd := ports.Command{
		ID: "cmd-child-" + title, IdempotencyKey: "idem-child-" + title, Actor: "seed",
		CorrelationID: "seed", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "CreateChildWorkItem", RequestHash: "hash-child-" + title,
	}
	result, err := workapp.CreateChildWorkItem(context.Background(), e.uow, idsource.Random{}, cmd, workapp.CreateChildWorkItemRequest{
		ParentWorkItemID: parentWorkItemID, Title: title, ParentJoinPolicy: "ALL_CHILDREN_DONE", EffectiveScope: effectiveScope,
	})
	if err != nil {
		t.Fatalf("CreateChildWorkItem(%s): %v", title, err)
	}
	return result
}

// seedRepositoryWorkspace directly writes one RepositoryWorkspace row under
// an already-real WorkspaceSetID (V3-06's own provisioning worker is never
// actually run in this package's own tests — this stands in for it,
// exactly the way workitem_test.go's own seedActiveRepository stands in for
// V3-02's probe worker).
func (e *testEnv) seedRepositoryWorkspace(t *testing.T, workspaceSetID, repositoryID string, generation uint64, state workspace.RepositoryWorkspaceState) {
	t.Helper()
	err := e.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Work().CreateRepositoryWorkspace(context.Background(), workspace.RepositoryWorkspace{
			ID: workspace.RepositoryWorkspaceID(idsource.Random{}.NewID()), WorkspaceSetID: workspace.WorkspaceSetID(workspaceSetID),
			RepositoryID: project.RepositoryID(repositoryID), Generation: generation,
			Locator:      "https://example.invalid/" + repositoryID + ".git",
			BaseRevision: "0000000000000000000000000000000000000000", State: state, Version: 1,
		})
		return err
	})
	if err != nil {
		t.Fatalf("seedRepositoryWorkspace(%s/%s): %v", workspaceSetID, repositoryID, err)
	}
}

// seedProjectionRow directly writes one V6-08 projection row — this
// package's own tests are the FIRST in this codebase to exercise
// ports.ProjectionRepository from an HTTP handler's own test suite; no
// live consumer (V6-08A) exists yet to populate rows the ordinary way, so
// every test seeds them directly, exactly as this task's own doc citations
// anticipate ("V6-08A ... the real data source every test in this package
// seeds directly").
func (e *testEnv) seedProjectionRow(t *testing.T, projectID string, generation uint64, card projection.WorkItemCardRow, journalPosition uint64) {
	t.Helper()
	payload, err := card.CanonicalJSON()
	if err != nil {
		t.Fatalf("card.CanonicalJSON(%s): %v", card.WorkItemID, err)
	}
	err = e.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Projections().UpsertProjectionRow(context.Background(), ports.ProjectionRow{
			ProjectID: projectID, ProjectionName: projection.ProjectionName, Generation: generation,
			EntityKey: card.WorkItemID, PayloadJSON: string(payload), LastAppliedJournalPosition: journalPosition,
			UpdatedAt: time.Now().UTC(),
		})
	})
	if err != nil {
		t.Fatalf("seedProjectionRow(%s): %v", card.WorkItemID, err)
	}
}

// ensureGeneration creates (idempotently) projectID's own active generation
// for the "workitem" projection.
func (e *testEnv) ensureGeneration(t *testing.T, projectID string, generation uint64) {
	t.Helper()
	err := e.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Projections().EnsureGeneration(context.Background(), projectID, projection.ProjectionName, generation, 1, time.Now().UTC())
	})
	if err != nil {
		t.Fatalf("ensureGeneration(%s/%d): %v", projectID, generation, err)
	}
}

// upsertCheckpoint creates or advances projectID/generation's own
// checkpoint to (cursorPos, status) — reads the current Cursor first (when
// a checkpoint already exists) so the CAS ExpectedCursor is always correct,
// letting a test call this more than once for the SAME generation (e.g. to
// flip LIVE -> DEGRADED) without hand-tracking the prior cursor itself.
func (e *testEnv) upsertCheckpoint(t *testing.T, projectID string, generation uint64, cursorPos uint64, status ports.ProjectionStatus) {
	t.Helper()
	err := e.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		var expected *uint64
		existing, getErr := tx.Projections().GetProjectionCheckpoint(context.Background(), projectID, projection.ProjectionName, generation)
		switch {
		case getErr == nil:
			cursorCopy := existing.Cursor
			expected = &cursorCopy
		case errors.Is(getErr, ports.ErrPersistenceNotFound):
			expected = nil
		default:
			return getErr
		}
		return tx.Projections().UpsertProjectionCheckpoint(context.Background(), ports.UpsertProjectionCheckpointRequest{
			ProjectID: projectID, ProjectionName: projection.ProjectionName, Generation: generation,
			ExpectedCursor: expected, NewCursor: cursorPos, NewStatus: status, UpdatedAt: time.Now().UTC(),
		})
	})
	if err != nil {
		t.Fatalf("upsertCheckpoint(%s/%d): %v", projectID, generation, err)
	}
}
