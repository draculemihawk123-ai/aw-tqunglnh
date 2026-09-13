package workitem_test

// Real HTTP round-trip coverage for V6-04 (docs/design/08-v6-api-projections.md):
// every test in this file drives a REAL httpapi.Server (real TCP loopback
// listener, real middleware chain, real session-token/Origin/Host guards)
// backed by a REAL *sqlite.Store — never a mock, never a direct row
// fabrication (this repo's own hard rule; mirrors
// internal/delivery/httpapi/server_test.go's own newTestServer idiom,
// adapted for this package's own routes).

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/workitem"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

const testSessionToken = "test-workitem-session-token"

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
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "workitem-http.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	reg := httpapi.NewRouteRegistry()
	workitem.RegisterRoutes(reg, workitem.Dependencies{UnitOfWork: uow, IDs: idsource.Random{}, Clock: clock.System{}})

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

// do issues a real HTTP request against e's own real server. idempotencyKey/
// ifMatch are omitted from the request entirely when "".
func (e *testEnv) do(t *testing.T, method, path, idempotencyKey, ifMatch string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.base+path, reader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(httpapi.SessionTokenHeader, testSessionToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set(httpapi.IdempotencyKeyHeader, idempotencyKey)
	}
	if ifMatch != "" {
		req.Header.Set(httpapi.IfMatchHeader, ifMatch)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
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
// seedProjectSQLite exactly (same direct tx.Catalog().CreateProject call,
// same reasoning: this is fixture setup for a concern V6-03A, not this
// task, exposes over HTTP — a caller-chosen ID keeps every test's own
// path readable).
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

// seedActiveRepository mirrors commands_sqlite_test.go's own
// seedActiveRepositorySQLite exactly: RegisterRepository (the real public
// command) then a direct REGISTERING->PROBING->ACTIVE drive standing in for
// V3-02's own probe worker — test setup, not the behavior under test.
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

func scopeGrantJSON(repositoryID, access, reason string) map[string]any {
	return map[string]any{"repositoryId": repositoryID, "access": access, "reason": reason}
}

// TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet proves this
// task's own "Hoàn thành khi: mọi WorkItem/scope control gọi named
// application command và client không set state" from the router side: the
// closed set of twelve operationIds below is EVERY route this package ever
// registers — no generic status/family/workspace setter route exists,
// mechanically, not merely by omission.
func TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet(t *testing.T) {
	reg := httpapi.NewRouteRegistry()
	workitem.RegisterRoutes(reg, workitem.Dependencies{UnitOfWork: nil, IDs: idsource.Random{}, Clock: clock.System{}})

	want := map[string]bool{
		"createRootWorkItem": true, "listWorkItems": true, "getWorkItem": true,
		"createChildWorkItem": true, "listChildWorkItems": true, "getWorkItemReadiness": true,
		"getTaskFamily": true, "requestScopeExpansion": true, "getScopeExpansionRequest": true,
		"approveScopeExpansion": true, "rejectScopeExpansion": true, "withdrawScopeExpansion": true,
	}
	got := reg.Descriptors()
	if len(got) != len(want) {
		t.Fatalf("len(Descriptors()) = %d, want %d", len(got), len(want))
	}
	for _, d := range got {
		if !want[d.OperationID] {
			t.Errorf("unexpected operationId %q registered", d.OperationID)
		}
		if d.ScopeKind != httpapi.ScopeProject {
			t.Errorf("operationId %q has ScopeKind %q, want PROJECT (ADR-025: WorkItem/TaskFamily/ScopeExpansionRequest are never installation-scoped)", d.OperationID, d.ScopeKind)
		}
		delete(want, d.OperationID)
	}
	if len(want) != 0 {
		t.Errorf("missing operationIds: %v", want)
	}
}

// TestFullJourney_RootChildReadinessFamilyScopeExpansionApproval is this
// task's own end-to-end happy path: root create -> detail -> list -> child
// create -> list children -> readiness explanation -> family detail ->
// request scope expansion -> detail -> approve -> confirm both the request
// and the family reflect the decision.
func TestFullJourney_RootChildReadinessFamilyScopeExpansionApproval(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")

	// 1. Create root.
	createRootResp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-root-1", "", map[string]any{
		"title":        "Root task",
		"initialScope": []any{scopeGrantJSON("repo-a", "WRITE", "root scope")},
	})
	if createRootResp.StatusCode != http.StatusCreated {
		t.Fatalf("POST work-items status = %d, want 201", createRootResp.StatusCode)
	}
	if etag := createRootResp.Header.Get(httpapi.ETagHeader); etag != `"1"` {
		t.Fatalf("POST work-items ETag = %q, want \"1\"", etag)
	}
	var root workapp.CreateRootWorkItemResult
	decodeInto(t, createRootResp, &root)
	if root.ProjectID != "project-1" || root.Status != "BACKLOG" {
		t.Fatalf("root = %+v, want ProjectID=project-1 Status=BACKLOG", root)
	}

	// 2. Authoritative detail.
	detailResp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID, "", "", nil)
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("GET work-item status = %d, want 200", detailResp.StatusCode)
	}
	var detail workapp.WorkItemDetail
	decodeInto(t, detailResp, &detail)
	if detail.WorkItemID != root.WorkItemID || detail.Kind != "ROOT" || detail.Version != 1 {
		t.Fatalf("detail = %+v, want WorkItemID=%s Kind=ROOT Version=1", detail, root.WorkItemID)
	}

	// 3. List (must contain exactly the root so far).
	listResp := env.do(t, http.MethodGet, "/projects/project-1/work-items", "", "", nil)
	var list struct {
		Items []workapp.WorkItemDetail `json:"items"`
	}
	decodeInto(t, listResp, &list)
	if len(list.Items) != 1 || list.Items[0].WorkItemID != root.WorkItemID {
		t.Fatalf("list.Items = %+v, want exactly [root]", list.Items)
	}

	// 4. Create child.
	createChildResp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/children", "idem-child-1", "", map[string]any{
		"title":            "Child task",
		"parentJoinPolicy": "ALL",
		"effectiveScope":   []any{scopeGrantJSON("repo-a", "WRITE", "child scope")},
	})
	if createChildResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createChildResp.Body)
		t.Fatalf("POST children status = %d, want 201, body=%s", createChildResp.StatusCode, body)
	}
	var child workapp.CreateChildWorkItemResult
	decodeInto(t, createChildResp, &child)

	// 5. List children.
	childrenResp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/children", "", "", nil)
	var children struct {
		Items []workapp.WorkItemDetail `json:"items"`
	}
	decodeInto(t, childrenResp, &children)
	if len(children.Items) != 1 || children.Items[0].WorkItemID != child.WorkItemID {
		t.Fatalf("children.Items = %+v, want exactly [child]", children.Items)
	}

	// 6. Readiness explanation — a fresh root/child has no contract yet.
	readinessResp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/readiness", "", "", nil)
	var readiness workapp.WorkItemReadiness
	decodeInto(t, readinessResp, &readiness)
	if readiness.Ready {
		t.Fatal("a freshly created root WorkItem must not report Ready=true")
	}
	if len(readiness.Problems) == 0 {
		t.Fatal("expected at least one concrete readiness problem")
	}

	// 7. Family detail.
	familyResp := env.do(t, http.MethodGet, "/projects/project-1/task-families/"+root.FamilyID, "", "", nil)
	var family workapp.TaskFamilyDetail
	decodeInto(t, familyResp, &family)
	if family.ScopeVersion != 1 || family.RootWorkItemID != root.WorkItemID {
		t.Fatalf("family = %+v, want ScopeVersion=1 RootWorkItemID=%s", family, root.WorkItemID)
	}

	// 8. Request scope expansion for a second, newly-active repository.
	env.seedActiveRepository(t, "project-1", "repo-b")
	requestResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/"+root.FamilyID+"/scope-expansions", "idem-expand-1", "", map[string]any{
		"reason":          "need repo-b too",
		"requestedGrants": []any{scopeGrantJSON("repo-b", "READ", "expand reason")},
	})
	if requestResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(requestResp.Body)
		t.Fatalf("POST scope-expansions status = %d, want 201, body=%s", requestResp.StatusCode, body)
	}
	var expansion workapp.RequestScopeExpansionResult
	decodeInto(t, requestResp, &expansion)
	if expansion.Status != "PENDING" {
		t.Fatalf("expansion.Status = %q, want PENDING", expansion.Status)
	}

	// 9. Detail carries the ETag approve/reject/withdraw will need.
	expansionDetailResp := env.do(t, http.MethodGet, "/projects/project-1/scope-expansions/"+expansion.RequestID, "", "", nil)
	etag := expansionDetailResp.Header.Get(httpapi.ETagHeader)
	if etag != `"1"` {
		t.Fatalf("scope-expansion detail ETag = %q, want \"1\"", etag)
	}
	expansionDetailResp.Body.Close()

	// 10. Approve.
	approveResp := env.do(t, http.MethodPost, "/projects/project-1/scope-expansions/"+expansion.RequestID+"/approve", "idem-approve-1", etag, map[string]any{})
	if approveResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(approveResp.Body)
		t.Fatalf("POST approve status = %d, want 200, body=%s", approveResp.StatusCode, body)
	}
	var approved workapp.ApproveScopeExpansionResult
	decodeInto(t, approveResp, &approved)
	if approved.NewScopeVersion != 2 {
		t.Fatalf("approved.NewScopeVersion = %d, want 2", approved.NewScopeVersion)
	}

	// 11. Both the request and the family now reflect the decision.
	afterExpansionResp := env.do(t, http.MethodGet, "/projects/project-1/scope-expansions/"+expansion.RequestID, "", "", nil)
	var afterExpansion workapp.ScopeExpansionRequestDetail
	decodeInto(t, afterExpansionResp, &afterExpansion)
	if afterExpansion.Status != "APPROVED" {
		t.Fatalf("afterExpansion.Status = %q, want APPROVED", afterExpansion.Status)
	}
	afterFamilyResp := env.do(t, http.MethodGet, "/projects/project-1/task-families/"+root.FamilyID, "", "", nil)
	var afterFamily workapp.TaskFamilyDetail
	decodeInto(t, afterFamilyResp, &afterFamily)
	if afterFamily.ScopeVersion != 2 {
		t.Fatalf("afterFamily.ScopeVersion = %d, want 2", afterFamily.ScopeVersion)
	}
}

// TestCreateRootWorkItem_SameIdempotencyKey_ReplaysWithoutCreatingSecondWorkItem
// proves V6-02's own replay contract end-to-end through this package's real
// HTTP handler: a genuine retry (same Idempotency-Key, same body) gets back
// the exact original WorkItemID with a 200 (WriteReceiptReplay's own fixed
// status, never a second 201), and ListWorkItems still reports exactly one
// row.
func TestCreateRootWorkItem_SameIdempotencyKey_ReplaysWithoutCreatingSecondWorkItem(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")

	body := map[string]any{"title": "Root task", "initialScope": []any{scopeGrantJSON("repo-a", "WRITE", "root scope")}}
	first := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-replay-1", "", body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", first.StatusCode)
	}
	var firstResult workapp.CreateRootWorkItemResult
	decodeInto(t, first, &firstResult)

	second := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-replay-1", "", body)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 (WriteReceiptReplay always 200)", second.StatusCode)
	}
	var secondResult workapp.CreateRootWorkItemResult
	decodeInto(t, second, &secondResult)
	if secondResult.WorkItemID != firstResult.WorkItemID {
		t.Fatalf("replay WorkItemID = %q, want exact original %q", secondResult.WorkItemID, firstResult.WorkItemID)
	}

	listResp := env.do(t, http.MethodGet, "/projects/project-1/work-items", "", "", nil)
	var list struct {
		Items []workapp.WorkItemDetail `json:"items"`
	}
	decodeInto(t, listResp, &list)
	if len(list.Items) != 1 {
		t.Fatalf("len(list.Items) = %d, want exactly 1 (replay must not create a second row)", len(list.Items))
	}
}

// TestCreateRootWorkItem_SameKeyDifferentBody_ConflictsBeforeSecondInsert
// proves V6-02's own "different body conflict trước I/O" line end-to-end.
func TestCreateRootWorkItem_SameKeyDifferentBody_ConflictsBeforeSecondInsert(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")

	first := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-conflict-1", "", map[string]any{
		"title": "Widget", "initialScope": []any{scopeGrantJSON("repo-a", "WRITE", "root scope")},
	})
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", first.StatusCode)
	}
	first.Body.Close()

	second := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-conflict-1", "", map[string]any{
		"title": "Gadget", "initialScope": []any{scopeGrantJSON("repo-a", "WRITE", "root scope")},
	})
	if second.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(second.Body)
		t.Fatalf("same-key-different-body status = %d, want 409, body=%s", second.StatusCode, body)
	}
}

// TestGetWorkItem_AnotherProject_ReturnsNotFound proves the leakage-
// normalized cross-project response end-to-end: a WorkItem that genuinely
// exists, just not in the project named by the path, produces the exact
// same 404 shape a truly nonexistent ID would.
func TestGetWorkItem_AnotherProject_ReturnsNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedProject(t, "project-2")
	env.seedActiveRepository(t, "project-1", "repo-a")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-cross-1", "", map[string]any{
		"title": "Root task", "initialScope": []any{scopeGrantJSON("repo-a", "WRITE", "root scope")},
	})
	var root workapp.CreateRootWorkItemResult
	decodeInto(t, createResp, &root)

	crossResp := env.do(t, http.MethodGet, "/projects/project-2/work-items/"+root.WorkItemID, "", "", nil)
	if crossResp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-project GET status = %d, want 404", crossResp.StatusCode)
	}
	crossBytes, err := io.ReadAll(crossResp.Body)
	crossResp.Body.Close()
	if err != nil {
		t.Fatalf("read cross-project response body: %v", err)
	}

	unknownResp := env.do(t, http.MethodGet, "/projects/project-2/work-items/genuinely-unknown-id", "", "", nil)
	if unknownResp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown-ID GET status = %d, want 404", unknownResp.StatusCode)
	}
	unknownBytes, err := io.ReadAll(unknownResp.Body)
	unknownResp.Body.Close()
	if err != nil {
		t.Fatalf("read unknown-ID response body: %v", err)
	}

	if string(crossBytes) != string(unknownBytes) {
		t.Fatalf("cross-project response = %s, unknown-ID response = %s, want byte-identical (leakage normalization)", crossBytes, unknownBytes)
	}
}

// TestApproveScopeExpansion_StaleIfMatch_PreconditionFailed proves this
// package's own pre-dispatch version check: an If-Match naming a version
// this request is not actually at gets 412, never silently proceeding.
func TestApproveScopeExpansion_StaleIfMatch_PreconditionFailed(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	env.seedActiveRepository(t, "project-1", "repo-b")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-stale-1", "", map[string]any{
		"title": "Root task", "initialScope": []any{scopeGrantJSON("repo-a", "WRITE", "root scope")},
	})
	var root workapp.CreateRootWorkItemResult
	decodeInto(t, createResp, &root)

	requestResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/"+root.FamilyID+"/scope-expansions", "idem-stale-expand-1", "", map[string]any{
		"reason": "need repo-b", "requestedGrants": []any{scopeGrantJSON("repo-b", "READ", "expand")},
	})
	var expansion workapp.RequestScopeExpansionResult
	decodeInto(t, requestResp, &expansion)

	staleResp := env.do(t, http.MethodPost, "/projects/project-1/scope-expansions/"+expansion.RequestID+"/approve", "idem-stale-approve-1", `"99"`, map[string]any{})
	if staleResp.StatusCode != http.StatusPreconditionFailed {
		body, _ := io.ReadAll(staleResp.Body)
		t.Fatalf("stale If-Match status = %d, want 412, body=%s", staleResp.StatusCode, body)
	}
}

// TestConcurrentApproveAndReject_ExactlyOneDecisionWins is this task's own
// explicit "concurrent decisions" Verify requirement, proven with real
// goroutines against real sqlite: two different HTTP callers race Approve
// and Reject against the SAME PENDING request (different Idempotency-Keys,
// both quoting the same, still-current If-Match) — sqlite's own
// _txlock=immediate serializes the two WithSerializedWrite transactions,
// and ApproveScopeExpansion/RejectScopeExpansion's own fresh
// PENDING-status check (internal/app/work/scope_expansion.go) is what
// actually decides the winner: exactly one call succeeds (200), the other
// is rejected — never both succeeding, never a silently corrupted mixed
// state.
//
// The loser's own exact status code depends on exactly how the two requests
// interleave, and a real run legitimately produces either one: if both
// goroutines' own pre-dispatch reload (loadScopeExpansionRequestForUpdate)
// happens before either has committed, both see Version 1 and pass their
// own If-Match precondition, and it is the DOMAIN command's own fresh
// PENDING-status check, inside its serialized transaction, that rejects the
// loser with ErrScopeExpansionNotPending (409). If instead the winner fully
// commits before the loser's own reload even runs, the loser's own reload
// already observes Version 2 against its hardcoded If-Match "1", and this
// package's own HTTP-layer precondition check (scope_expansion_commands.go)
// rejects it first, as a stale If-Match (412) — the domain command is never
// even dispatched in that interleaving. Both are the SAME race being caught
// correctly at two different, both-real defense layers; this test asserts
// on the set {409, 412} rather than a single hardcoded code, because Go's
// own goroutine scheduling makes it flaky to assume specifically which
// layer wins on any given run. Whichever it is, the important invariant —
// exactly one decision, never both, never neither — is what actually
// matters and is what this test really enforces.
func TestConcurrentApproveAndReject_ExactlyOneDecisionWins(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	env.seedActiveRepository(t, "project-1", "repo-b")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-race-1", "", map[string]any{
		"title": "Root task", "initialScope": []any{scopeGrantJSON("repo-a", "WRITE", "root scope")},
	})
	var root workapp.CreateRootWorkItemResult
	decodeInto(t, createResp, &root)

	requestResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/"+root.FamilyID+"/scope-expansions", "idem-race-expand-1", "", map[string]any{
		"reason": "need repo-b", "requestedGrants": []any{scopeGrantJSON("repo-b", "READ", "expand")},
	})
	var expansion workapp.RequestScopeExpansionResult
	decodeInto(t, requestResp, &expansion)

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		resp := env.do(t, http.MethodPost, "/projects/project-1/scope-expansions/"+expansion.RequestID+"/approve", "idem-race-approve-1", `"1"`, map[string]any{})
		statuses[0] = resp.StatusCode
		resp.Body.Close()
	}()
	go func() {
		defer wg.Done()
		resp := env.do(t, http.MethodPost, "/projects/project-1/scope-expansions/"+expansion.RequestID+"/reject", "idem-race-reject-1", `"1"`, map[string]any{"decisionNote": "racing rejection"})
		statuses[1] = resp.StatusCode
		resp.Body.Close()
	}()
	wg.Wait()

	successes, losers := 0, 0
	for _, status := range statuses {
		switch status {
		case http.StatusOK:
			successes++
		case http.StatusConflict, http.StatusPreconditionFailed:
			// Both are the same race caught at two different defense
			// layers — see this test's own doc comment.
			losers++
		default:
			t.Fatalf("unexpected status in race: %d (statuses=%v)", status, statuses)
		}
	}
	if successes != 1 || losers != 1 {
		t.Fatalf("statuses = %v, want exactly one 200 and one loser (409 or 412)", statuses)
	}

	finalResp := env.do(t, http.MethodGet, "/projects/project-1/scope-expansions/"+expansion.RequestID, "", "", nil)
	var final workapp.ScopeExpansionRequestDetail
	decodeInto(t, finalResp, &final)
	if final.Status != "APPROVED" && final.Status != "REJECTED" {
		t.Fatalf("final.Status = %q, want exactly one terminal decision (APPROVED or REJECTED)", final.Status)
	}
}

// TestConcurrentWithdrawAndApprove_ExactlyOneWins is this task's own
// explicit "withdrawal race" Verify requirement: WithdrawScopeExpansion is
// this task's own responsibility ("thuộc task này, chỉ khi pending và không
// tạo grant/amendment") — proven here racing for real against Approve, with
// the identical real-goroutines/real-sqlite discipline the decision race
// above uses. See that test's own doc comment for why the loser's exact
// status (409 vs 412) is asserted as a set, not a single hardcoded code.
func TestConcurrentWithdrawAndApprove_ExactlyOneWins(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	env.seedActiveRepository(t, "project-1", "repo-b")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-wrace-1", "", map[string]any{
		"title": "Root task", "initialScope": []any{scopeGrantJSON("repo-a", "WRITE", "root scope")},
	})
	var root workapp.CreateRootWorkItemResult
	decodeInto(t, createResp, &root)

	requestResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/"+root.FamilyID+"/scope-expansions", "idem-wrace-expand-1", "", map[string]any{
		"reason": "need repo-b", "requestedGrants": []any{scopeGrantJSON("repo-b", "READ", "expand")},
	})
	var expansion workapp.RequestScopeExpansionResult
	decodeInto(t, requestResp, &expansion)

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		resp := env.do(t, http.MethodPost, "/projects/project-1/scope-expansions/"+expansion.RequestID+"/withdraw", "idem-wrace-withdraw-1", `"1"`, map[string]any{})
		statuses[0] = resp.StatusCode
		resp.Body.Close()
	}()
	go func() {
		defer wg.Done()
		resp := env.do(t, http.MethodPost, "/projects/project-1/scope-expansions/"+expansion.RequestID+"/approve", "idem-wrace-approve-1", `"1"`, map[string]any{})
		statuses[1] = resp.StatusCode
		resp.Body.Close()
	}()
	wg.Wait()

	successes, losers := 0, 0
	for _, status := range statuses {
		switch status {
		case http.StatusOK:
			successes++
		case http.StatusConflict, http.StatusPreconditionFailed:
			losers++
		default:
			t.Fatalf("unexpected status in withdrawal race: %d (statuses=%v)", status, statuses)
		}
	}
	if successes != 1 || losers != 1 {
		t.Fatalf("statuses = %v, want exactly one 200 and one loser (409 or 412)", statuses)
	}

	finalResp := env.do(t, http.MethodGet, "/projects/project-1/scope-expansions/"+expansion.RequestID, "", "", nil)
	var final workapp.ScopeExpansionRequestDetail
	decodeInto(t, finalResp, &final)
	if final.Status != "APPROVED" && final.Status != "WITHDRAWN" {
		t.Fatalf("final.Status = %q, want exactly one terminal decision (APPROVED or WITHDRAWN)", final.Status)
	}
}

// TestWithdrawScopeExpansion_AlreadyWithdrawn_IsIdempotentNoOp proves
// WithdrawScopeExpansion's own business-level idempotency
// (internal/app/work/scope_expansion.go's own doc comment: "idempotent" at
// the business level, not merely the command-envelope level) survives the
// HTTP layer: withdrawing twice, with two genuinely DIFFERENT Idempotency-
// Keys (simulating two independent callers, not a single retry), both
// succeed with the same terminal WITHDRAWN status — the second call's own
// If-Match must be a fresh reload's ETag, since Withdraw bumped the version.
func TestWithdrawScopeExpansion_AlreadyWithdrawn_IsIdempotentNoOp(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	env.seedActiveRepository(t, "project-1", "repo-b")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-widem-1", "", map[string]any{
		"title": "Root task", "initialScope": []any{scopeGrantJSON("repo-a", "WRITE", "root scope")},
	})
	var root workapp.CreateRootWorkItemResult
	decodeInto(t, createResp, &root)

	requestResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/"+root.FamilyID+"/scope-expansions", "idem-widem-expand-1", "", map[string]any{
		"reason": "need repo-b", "requestedGrants": []any{scopeGrantJSON("repo-b", "READ", "expand")},
	})
	var expansion workapp.RequestScopeExpansionResult
	decodeInto(t, requestResp, &expansion)

	first := env.do(t, http.MethodPost, "/projects/project-1/scope-expansions/"+expansion.RequestID+"/withdraw", "idem-widem-w1", `"1"`, map[string]any{})
	if first.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(first.Body)
		t.Fatalf("first withdraw status = %d, want 200, body=%s", first.StatusCode, body)
	}
	var firstResult workapp.WithdrawScopeExpansionResult
	decodeInto(t, first, &firstResult)
	if firstResult.Status != "WITHDRAWN" {
		t.Fatalf("firstResult.Status = %q, want WITHDRAWN", firstResult.Status)
	}

	// Reload for the fresh ETag (Withdraw bumped Version 1 -> 2).
	afterResp := env.do(t, http.MethodGet, "/projects/project-1/scope-expansions/"+expansion.RequestID, "", "", nil)
	afterETag := afterResp.Header.Get(httpapi.ETagHeader)
	afterResp.Body.Close()
	if afterETag != `"2"` {
		t.Fatalf("post-withdraw ETag = %q, want \"2\"", afterETag)
	}

	second := env.do(t, http.MethodPost, "/projects/project-1/scope-expansions/"+expansion.RequestID+"/withdraw", "idem-widem-w2", afterETag, map[string]any{})
	if second.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(second.Body)
		t.Fatalf("second withdraw status = %d, want 200 (business-level idempotent no-op), body=%s", second.StatusCode, body)
	}
	var secondResult workapp.WithdrawScopeExpansionResult
	decodeInto(t, second, &secondResult)
	if secondResult.Status != "WITHDRAWN" {
		t.Fatalf("secondResult.Status = %q, want WITHDRAWN", secondResult.Status)
	}
}

// TestCreateChildWorkItem_EffectiveScopeExceedsFamilyScope_Returns400 proves
// the multi-repo/subset negative matrix's own core case: a child asking for
// WRITE on a repository the family never granted at all is rejected as a
// request-shape problem, never silently narrowed or silently granted.
func TestCreateChildWorkItem_EffectiveScopeExceedsFamilyScope_Returns400(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	env.seedActiveRepository(t, "project-1", "repo-b")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-subset-1", "", map[string]any{
		"title": "Root task", "initialScope": []any{scopeGrantJSON("repo-a", "READ", "root scope")},
	})
	var root workapp.CreateRootWorkItemResult
	decodeInto(t, createResp, &root)

	// repo-b was never granted to the family at all.
	childResp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/children", "idem-subset-child-1", "", map[string]any{
		"title": "Child task", "parentJoinPolicy": "ALL",
		"effectiveScope": []any{scopeGrantJSON("repo-b", "READ", "child scope")},
	})
	if childResp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(childResp.Body)
		t.Fatalf("ungranted-repository child create status = %d, want 400, body=%s", childResp.StatusCode, body)
	}
	childResp.Body.Close()

	// A READ->WRITE escalation on an already-granted repository is the same
	// kind of rejection.
	escalateResp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/children", "idem-subset-child-2", "", map[string]any{
		"title": "Child task 2", "parentJoinPolicy": "ALL",
		"effectiveScope": []any{scopeGrantJSON("repo-a", "WRITE", "escalate")},
	})
	if escalateResp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(escalateResp.Body)
		t.Fatalf("READ->WRITE escalation child create status = %d, want 400, body=%s", escalateResp.StatusCode, body)
	}
	escalateResp.Body.Close()
}

// TestRequestScopeExpansion_MissingReason_Returns400WithFieldDetail proves
// this package's own pre-dispatch field validation produces a precise
// ErrorDetail rather than a generic failure.
func TestRequestScopeExpansion_MissingReason_Returns400WithFieldDetail(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "idem-validate-1", "", map[string]any{
		"title": "Root task", "initialScope": []any{scopeGrantJSON("repo-a", "READ", "root scope")},
	})
	var root workapp.CreateRootWorkItemResult
	decodeInto(t, createResp, &root)

	resp := env.do(t, http.MethodPost, "/projects/project-1/task-families/"+root.FamilyID+"/scope-expansions", "idem-validate-2", "", map[string]any{
		"reason": "", "requestedGrants": []any{scopeGrantJSON("repo-a", "READ", "expand")},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing-reason status = %d, want 400", resp.StatusCode)
	}
	var errBody httpapi.ErrorResponse
	decodeInto(t, resp, &errBody)
	if len(errBody.Error.Details) != 1 || errBody.Error.Details[0].Field != "reason" {
		t.Fatalf("errBody.Error.Details = %+v, want exactly one detail for field \"reason\"", errBody.Error.Details)
	}
}

// TestMutatingRoutes_RequireIdempotencyKey proves every one of this
// package's own six mutating routes rejects a request missing
// Idempotency-Key before ever touching a domain command — sampled via
// CreateRootWorkItem, representative of the shared prepareCreateCommand/
// prepareUpdateCommand preamble every other mutating handler also runs
// through.
func TestMutatingRoutes_RequireIdempotencyKey(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")

	resp := env.do(t, http.MethodPost, "/projects/project-1/work-items", "", "", map[string]any{
		"title": "Root task", "initialScope": []any{scopeGrantJSON("repo-a", "READ", "root scope")},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing Idempotency-Key status = %d, want 400", resp.StatusCode)
	}
}
