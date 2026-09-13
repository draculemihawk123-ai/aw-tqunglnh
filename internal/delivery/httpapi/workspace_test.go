package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

const workspaceTestSessionToken = "workspace-test-session-token" //nolint:gosec // test-only fixed token, never a real secret

// workspaceTestServer is a REAL, listening httpapi.Server (V6-10B's own 4
// routes only, wired via the exact RegisterWorkspaceRoutes a composition
// root calls) backed by a real sqlite-backed UnitOfWork — the "realistic
// HTTP round-trip via httptest" this task's own instructions ask for. This
// deliberately goes through the real net.Listener/http.Client and the real
// HostOriginGuard/RequireSessionToken/BindPrincipal middleware chain
// V6-01/V6-01A already ships (httpapi.NewServer), rather than calling a
// handler function directly: Go's enhanced http.ServeMux only populates
// r.PathValue("...") for a request actually routed through it, so any test
// that skipped the real server would never exercise the {projectId}/
// {familyId}/{repositoryWorkspaceId} wildcard extraction this package's own
// handlers depend on.
type workspaceTestServer struct {
	baseURL string
	store   *sqlite.Store
	uow     ports.UnitOfWork
}

func startWorkspaceTestServer(t *testing.T) workspaceTestServer {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "workspace-http.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	routes := httpapi.NewRouteRegistry()
	httpapi.RegisterWorkspaceRoutes(routes, uow, idsource.Random{})

	server, err := httpapi.NewServer(httpapi.Config{
		Host: "127.0.0.1", Port: 0, Routes: routes, IDs: idsource.Random{},
		MaxBodyBytes: 1 << 20, Token: workspaceTestSessionToken,
		Principal: httpapi.LocalPrincipalSnapshot{Actor: "operator", Roles: []string{"operator"}},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	go func() { _ = server.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	return workspaceTestServer{baseURL: "http://" + server.Addr(), store: store, uow: uow}
}

// do issues one real HTTP request against ts and decodes the JSON response
// body (which every route in this file always returns, success or error)
// into a generic map — good enough to assert individual fields without this
// test file importing workspacerelease/workspacereconcile's own result
// types just to decode into them.
func (ts workspaceTestServer) do(t *testing.T, method, path string, headers map[string]string, body string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, ts.baseURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest %s %s: %v", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpapi.SessionTokenHeader, workspaceTestSessionToken)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	decoded := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decode body %q: %v", raw, err)
		}
	}
	return resp, decoded
}

func errorCode(t *testing.T, decoded map[string]any) string {
	t.Helper()
	errObj, ok := decoded["error"].(map[string]any)
	if !ok {
		t.Fatalf("response has no error envelope: %+v", decoded)
	}
	code, _ := errObj["code"].(string)
	return code
}

// seededWorkspace names every real row startWorkspaceTestServer's own
// seedReadyWorkspace plants for one test: a single RepositoryWorkspace at
// generation 1, state READY, inside a WorkspaceSet also at version 1 —
// enough to exercise every one of workspacerelease/workspacereconcile's own
// eligibility branches by then layering exactly one more real transition on
// top (quarantine, a write lease, a seal) per test.
type seededWorkspace struct {
	projectID, familyID, workItemID                     string
	workspaceSetID, repositoryID, repositoryWorkspaceID string
}

func (ts workspaceTestServer) seedReadyWorkspace(t *testing.T, suffix string) seededWorkspace {
	t.Helper()
	ctx := context.Background()
	sw := seededWorkspace{
		projectID: "project-" + suffix, familyID: "family-" + suffix, workItemID: "work-" + suffix,
		workspaceSetID: "set-" + suffix, repositoryID: "repo-" + suffix, repositoryWorkspaceID: "rw-" + suffix,
	}
	if err := sqlite.SeedFixtureOwners(ctx, ts.store, sw.projectID, sw.familyID, sw.workItemID); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := sqlite.SeedFixtureRepositoryWorkspace(ctx, ts.store, sw.projectID, sw.familyID, sw.workspaceSetID, sw.repositoryID, sw.repositoryWorkspaceID); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}
	return sw
}

// sealReleaseSet creates and seals a real ReleaseSet for sw's own family
// through the real internal/app/work.CreateReleaseSet/SealReleaseSet public
// commands, so work.EligibilityAuthority.IsReleaseAuthorized — the real
// ports.ReleaseEligibilityAuthority RegisterWorkspaceRoutes wires in, never a
// fake — reports this family as release-authorized (GC-INV-26: sealed or
// abandoned).
func (ts workspaceTestServer) sealReleaseSet(t *testing.T, sw seededWorkspace) {
	t.Helper()
	ctx := context.Background()
	createCmd := ports.Command{
		ID: "create-release-set-" + sw.familyID, IdempotencyKey: "create-release-set-" + sw.familyID,
		Actor: "operator", Scope: ports.ProjectScope(sw.projectID), Type: "CreateReleaseSet",
		RequestHash: "sha256:test-create-" + sw.familyID, RequestedAt: time.Now().UTC(),
	}
	created, err := work.CreateReleaseSet(ctx, ts.uow, idsource.Random{}, createCmd, work.CreateReleaseSetRequest{
		ProjectID: sw.projectID, FamilyID: sw.familyID,
		Repositories: []work.RepositoryReleaseRequest{{
			RepositoryID: sw.repositoryID, BaseVCSObjectID: "base-sha", ResultVCSObjectID: "base-sha", Verdict: "PASS",
		}},
	})
	if err != nil {
		t.Fatalf("CreateReleaseSet: %v", err)
	}
	sealCmd := ports.Command{
		ID: "seal-release-set-" + sw.familyID, IdempotencyKey: "seal-release-set-" + sw.familyID,
		Actor: "operator", Scope: ports.ProjectScope(sw.projectID), Type: "SealReleaseSet",
		RequestHash: "sha256:test-seal-" + sw.familyID, RequestedAt: time.Now().UTC(), ExpectedVersion: created.Version,
	}
	if _, err := work.SealReleaseSet(ctx, ts.uow, sealCmd, work.SealReleaseSetRequest{ReleaseSetID: created.ReleaseSetID}); err != nil {
		t.Fatalf("SealReleaseSet: %v", err)
	}
}

func etagFor(version uint64) string { return `"` + strconv.FormatUint(version, 10) + `"` }

// --- GET workspace-set state -------------------------------------------

func TestGetWorkspaceSetState_HappyPath_ReturnsStateEtagAndAdvisoryActions(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")

	resp, body := ts.do(t, http.MethodGet, "/projects/"+sw.projectID+"/workspace-sets/"+sw.familyID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %+v", resp.StatusCode, body)
	}
	if got := resp.Header.Get("ETag"); got != etagFor(1) {
		t.Fatalf("ETag = %q, want %q", got, etagFor(1))
	}
	if body["state"] != "READY" {
		t.Fatalf("state = %v, want READY", body["state"])
	}
	repoWorkspaces, _ := body["repositoryWorkspaces"].([]any)
	if len(repoWorkspaces) != 1 {
		t.Fatalf("len(repositoryWorkspaces) = %d, want 1; body = %+v", len(repoWorkspaces), body)
	}
	rw := repoWorkspaces[0].(map[string]any)
	if rw["hasActiveWriteLease"] != false {
		t.Fatalf("repositoryWorkspaces[0].hasActiveWriteLease = %v, want false", rw["hasActiveWriteLease"])
	}
	rwActions, _ := rw["validActions"].([]any)
	if len(rwActions) != 1 || rwActions[0].(map[string]any)["operationId"] != "requestWorkspaceReconciliation" {
		t.Fatalf("repositoryWorkspaces[0].validActions = %+v, want one requestWorkspaceReconciliation advisory", rw["validActions"])
	}
	setActions, _ := body["validActions"].([]any)
	if len(setActions) != 1 || setActions[0].(map[string]any)["operationId"] != "requestWorkspaceSetRelease" {
		t.Fatalf("validActions = %+v, want one requestWorkspaceSetRelease advisory (READY, no quarantine/lease)", body["validActions"])
	}
}

func TestGetWorkspaceSetState_UnknownFamily_ReturnsResourceHidden(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	resp, body := ts.do(t, http.MethodGet, "/projects/project-x/workspace-sets/does-not-exist", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if errorCode(t, body) != "NOT_FOUND" {
		t.Fatalf("error.code = %q, want NOT_FOUND", errorCode(t, body))
	}
}

// TestGetWorkspaceSetState_CrossProject_ReturnsIdenticalResourceHidden is
// this task's own "scope" Verify bullet: a caller naming the WRONG project
// for a WorkspaceSet that genuinely exists must see the exact same 404 an
// unknown family produces — V6-10C's own established leakage-normalization
// policy, proven here byte-for-byte against the real HTTP response, not
// merely at the application-query layer (queries_test.go already covers
// that half).
func TestGetWorkspaceSetState_CrossProject_ReturnsIdenticalResourceHidden(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")

	_, unknownBody := ts.do(t, http.MethodGet, "/projects/project-x/workspace-sets/does-not-exist", nil, "")
	resp, crossBody := ts.do(t, http.MethodGet, "/projects/some-other-project/workspace-sets/"+sw.familyID, nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if errorCode(t, crossBody) != errorCode(t, unknownBody) || crossBody["error"].(map[string]any)["message"] != unknownBody["error"].(map[string]any)["message"] {
		t.Fatalf("cross-project response %+v must be identical to unknown-resource response %+v", crossBody, unknownBody)
	}
}

// --- GET repository-workspace state -------------------------------------

func TestGetRepositoryWorkspaceState_HappyPath_ReflectsRealActiveWriteLease(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")

	if err := sqlite.SeedFixtureWriteLease(context.Background(), ts.store, sw.projectID, sw.familyID, sw.workItemID, sw.repositoryID, sw.repositoryWorkspaceID, 1); err != nil {
		t.Fatalf("SeedFixtureWriteLease: %v", err)
	}

	resp, body := ts.do(t, http.MethodGet, "/projects/"+sw.projectID+"/repository-workspaces/"+sw.repositoryWorkspaceID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %+v", resp.StatusCode, body)
	}
	if body["hasActiveWriteLease"] != true {
		t.Fatalf("hasActiveWriteLease = %v, want true (a real write lease was acquired)", body["hasActiveWriteLease"])
	}
	if body["generation"] != float64(1) {
		t.Fatalf("generation = %v, want 1", body["generation"])
	}
	// A real, live writer means reconcile is NOT among the advisory actions
	// — READY still qualifies by state, so this also proves ValidActions is
	// keyed off State only for the standalone repository-workspace query
	// (release's own lease-aware suppression is a WorkspaceSet-level policy,
	// deliberately not mirrored onto reconcile's own advisory, since a real
	// write lease is not one of ErrWorkspaceNotReconcilable's own reasons).
	actions, _ := body["validActions"].([]any)
	if len(actions) != 1 || actions[0].(map[string]any)["operationId"] != "requestWorkspaceReconciliation" {
		t.Fatalf("validActions = %+v, want one requestWorkspaceReconciliation advisory", body["validActions"])
	}
}

// --- POST release: request validation -----------------------------------

func TestRequestWorkspaceSetRelease_MissingIdempotencyKey_Returns400(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")
	resp, body := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/workspace-sets/"+sw.familyID+"/release",
		map[string]string{"If-Match": etagFor(1)}, "{}")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %+v", resp.StatusCode, body)
	}
	if errorCode(t, body) != "INVALID_REQUEST" {
		t.Fatalf("error.code = %q, want INVALID_REQUEST", errorCode(t, body))
	}
}

func TestRequestWorkspaceSetRelease_MissingIfMatch_Returns400(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")
	resp, body := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/workspace-sets/"+sw.familyID+"/release",
		map[string]string{"Idempotency-Key": "key-1"}, "{}")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %+v", resp.StatusCode, body)
	}
}

func TestRequestWorkspaceSetRelease_MalformedIfMatch_Returns400(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")
	resp, body := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/workspace-sets/"+sw.familyID+"/release",
		map[string]string{"Idempotency-Key": "key-1", "If-Match": "not-a-strong-etag"}, "{}")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %+v", resp.StatusCode, body)
	}
}

// --- POST release: eligibility refusals ----------------------------------

// TestRequestWorkspaceSetRelease_NotAuthorized_Returns403 is GC-INV-26's own
// refusal: no ReleaseSet exists for this family at all, so
// work.EligibilityAuthority (the real port, no fake) reports unauthorized.
func TestRequestWorkspaceSetRelease_NotAuthorized_Returns403(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")

	resp, body := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/workspace-sets/"+sw.familyID+"/release",
		map[string]string{"Idempotency-Key": "key-1", "If-Match": etagFor(1)}, "{}")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %+v", resp.StatusCode, body)
	}
	if errorCode(t, body) != "FORBIDDEN" {
		t.Fatalf("error.code = %q, want FORBIDDEN", errorCode(t, body))
	}
}

// TestRequestWorkspaceSetRelease_QuarantinedRepository_Returns423 is this
// task's own "quarantine refusal" Verify bullet: a real
// ports.WorkspaceLifecycle.QuarantineRepositoryWorkspace transition (never a
// fabricated row) blocks release, mapped onto errorcode.CodeWorkspaceQuarantined
// (423 Locked) via writeWorkspaceCommandError. The ReleaseSet is sealed
// first, so this proves the QUARANTINE refusal specifically, not
// authorization.
func TestRequestWorkspaceSetRelease_QuarantinedRepository_Returns423(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")
	ts.sealReleaseSet(t, sw)

	if err := ts.store.QuarantineRepositoryWorkspace(context.Background(), ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(sw.repositoryWorkspaceID), ExpectedVersion: 1,
		Reason: "test: simulated lease-losing writer", EventID: "evt-quarantine-1",
	}); err != nil {
		t.Fatalf("QuarantineRepositoryWorkspace: %v", err)
	}

	resp, body := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/workspace-sets/"+sw.familyID+"/release",
		map[string]string{"Idempotency-Key": "key-1", "If-Match": etagFor(1)}, "{}")
	if resp.StatusCode != http.StatusLocked {
		t.Fatalf("status = %d, want 423; body = %+v", resp.StatusCode, body)
	}
	if errorCode(t, body) != "CONFLICT" {
		t.Fatalf("error.code = %q, want CONFLICT (423 still wires the CONFLICT wire code — see StatusForAppErrorCode)", errorCode(t, body))
	}
}

// TestRequestWorkspaceSetRelease_ActiveWriteLease_Returns409 is this task's
// own "writer refusal" Verify bullet: a real write lease
// (sqlite.SeedFixtureWriteLease drives the real EnqueueJob/ClaimJob/
// AcquireWriteLeases path) blocks release.
func TestRequestWorkspaceSetRelease_ActiveWriteLease_Returns409(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")
	ts.sealReleaseSet(t, sw)

	if err := sqlite.SeedFixtureWriteLease(context.Background(), ts.store, sw.projectID, sw.familyID, sw.workItemID, sw.repositoryID, sw.repositoryWorkspaceID, 1); err != nil {
		t.Fatalf("SeedFixtureWriteLease: %v", err)
	}

	resp, body := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/workspace-sets/"+sw.familyID+"/release",
		map[string]string{"Idempotency-Key": "key-1", "If-Match": etagFor(1)}, "{}")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %+v", resp.StatusCode, body)
	}
}

func TestRequestWorkspaceSetRelease_StaleIfMatch_Returns412(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")
	ts.sealReleaseSet(t, sw)

	resp, body := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/workspace-sets/"+sw.familyID+"/release",
		map[string]string{"Idempotency-Key": "key-1", "If-Match": etagFor(99)}, "{}")
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412; body = %+v", resp.StatusCode, body)
	}
}

// --- POST release: happy path + idempotency ------------------------------

func TestRequestWorkspaceSetRelease_HappyPath_Returns200AndEnqueuesRealJob(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")
	ts.sealReleaseSet(t, sw)

	resp, body := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/workspace-sets/"+sw.familyID+"/release",
		map[string]string{"Idempotency-Key": "key-1", "If-Match": etagFor(1)}, "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %+v", resp.StatusCode, body)
	}
	jobID, _ := body["releaseJobId"].(string)
	if jobID == "" {
		t.Fatalf("releaseJobId missing/empty in %+v", body)
	}

	var hasJob bool
	if err := ts.uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		hasJob, err = tx.Jobs().HasActiveJobForAggregateIDs(context.Background(), []string{sw.workspaceSetID})
		return err
	}); err != nil {
		t.Fatalf("HasActiveJobForAggregateIDs: %v", err)
	}
	if !hasJob {
		t.Fatal("expected a real, active WORKSPACE_SET_RELEASE job for the workspace set's own AggregateID, found none")
	}
}

// TestRequestWorkspaceSetRelease_Replay_ReturnsIdenticalJobID proves the
// "replay" Verify bullet through the real HTTP layer: the exact same
// Idempotency-Key+If-Match dispatched twice returns the exact same
// releaseJobId both times — idsource.Random{} means a genuinely-minted
// second job would almost certainly produce a different id, exactly the
// same replay-identity proof receiptreplay_test.go's own
// TestCreateProject_SameKeySameBody_ReplaysExactSameProjectID already
// established for CreateProject.
func TestRequestWorkspaceSetRelease_Replay_ReturnsIdenticalJobID(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")
	ts.sealReleaseSet(t, sw)
	headers := map[string]string{"Idempotency-Key": "key-1", "If-Match": etagFor(1)}

	firstResp, first := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/workspace-sets/"+sw.familyID+"/release", headers, "{}")
	if firstResp.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body = %+v", firstResp.StatusCode, first)
	}
	secondResp, second := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/workspace-sets/"+sw.familyID+"/release", headers, "{}")
	if secondResp.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200; body = %+v", secondResp.StatusCode, second)
	}
	if first["releaseJobId"] != second["releaseJobId"] {
		t.Fatalf("replay releaseJobId = %v, want the exact original %v", second["releaseJobId"], first["releaseJobId"])
	}
}

// TestRequestWorkspaceSetRelease_SameKeyDifferentIfMatch_ReturnsConflict is
// this route's own version of "different body conflicts": the request body
// is always "{}" for this route, but ExpectedVersion (from If-Match) is
// itself part of SemanticHash's own input — reusing an Idempotency-Key with
// a DIFFERENT If-Match is exactly as much a semantic payload change as a
// different JSON field would be for a route whose body is not empty, and
// must conflict the identical way.
func TestRequestWorkspaceSetRelease_SameKeyDifferentIfMatch_ReturnsConflict(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")
	ts.sealReleaseSet(t, sw)

	firstResp, first := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/workspace-sets/"+sw.familyID+"/release",
		map[string]string{"Idempotency-Key": "key-1", "If-Match": etagFor(1)}, "{}")
	if firstResp.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body = %+v", firstResp.StatusCode, first)
	}
	resp, body := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/workspace-sets/"+sw.familyID+"/release",
		map[string]string{"Idempotency-Key": "key-1", "If-Match": etagFor(2)}, "{}")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %+v", resp.StatusCode, body)
	}
}

// --- POST reconcile --------------------------------------------------

func TestRequestWorkspaceReconciliation_HappyPath_Returns200AndEnqueuesRealJob(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")

	resp, body := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/repository-workspaces/"+sw.repositoryWorkspaceID+"/reconcile",
		map[string]string{"Idempotency-Key": "key-1", "If-Match": etagFor(1)}, "{}")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %+v", resp.StatusCode, body)
	}
	jobID, _ := body["reconciliationJobId"].(string)
	if jobID == "" {
		t.Fatalf("reconciliationJobId missing/empty in %+v", body)
	}

	var hasJob bool
	if err := ts.uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		hasJob, err = tx.Jobs().HasActiveJobForAggregateIDs(context.Background(), []string{sw.repositoryWorkspaceID})
		return err
	}); err != nil {
		t.Fatalf("HasActiveJobForAggregateIDs: %v", err)
	}
	if !hasJob {
		t.Fatal("expected a real, active WORKSPACE_RECONCILIATION job for the repository workspace's own AggregateID, found none")
	}
}

func TestRequestWorkspaceReconciliation_Replay_ReturnsIdenticalJobID(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")
	headers := map[string]string{"Idempotency-Key": "key-1", "If-Match": etagFor(1)}

	firstResp, first := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/repository-workspaces/"+sw.repositoryWorkspaceID+"/reconcile", headers, "{}")
	if firstResp.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body = %+v", firstResp.StatusCode, first)
	}
	secondResp, second := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/repository-workspaces/"+sw.repositoryWorkspaceID+"/reconcile", headers, "{}")
	if secondResp.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200; body = %+v", secondResp.StatusCode, second)
	}
	if first["reconciliationJobId"] != second["reconciliationJobId"] {
		t.Fatalf("replay reconciliationJobId = %v, want the exact original %v", second["reconciliationJobId"], first["reconciliationJobId"])
	}
}

// TestRequestWorkspaceReconciliation_NotReconcilable_Returns409 drives the
// repository workspace to RELEASED through the real
// ports.WorkspaceLifecycle.ReleaseRepositoryWorkspace transition (a state
// ErrWorkspaceNotReconcilable never allows), then confirms reconcile refuses
// it for real rather than assuming the mapping from a unit test alone.
func TestRequestWorkspaceReconciliation_NotReconcilable_Returns409(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")

	if err := ts.store.ReleaseRepositoryWorkspace(context.Background(), ports.ReleaseRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(sw.repositoryWorkspaceID), ExpectedVersion: 1,
		EventID: "evt-release-1",
	}); err != nil {
		t.Fatalf("ReleaseRepositoryWorkspace: %v", err)
	}

	resp, body := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/repository-workspaces/"+sw.repositoryWorkspaceID+"/reconcile",
		map[string]string{"Idempotency-Key": "key-1", "If-Match": etagFor(2)}, "{}")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %+v", resp.StatusCode, body)
	}
}

// TestRequestWorkspaceReconciliation_StaleIfMatch_Returns412 is a GENUINELY
// stale scenario, not merely a made-up wrong number: a real quarantine
// transition (V5-15D's own fenced CAS) bumps the repository workspace's own
// version 1->2 for real, and a caller still presenting the pre-quarantine
// If-Match ("1") — exactly what a client that fetched state before the
// quarantine happened would send — is rejected as stale.
func TestRequestWorkspaceReconciliation_StaleIfMatch_Returns412(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")

	if err := ts.store.QuarantineRepositoryWorkspace(context.Background(), ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(sw.repositoryWorkspaceID), ExpectedVersion: 1,
		Reason: "test: simulated lease-losing writer", EventID: "evt-quarantine-1",
	}); err != nil {
		t.Fatalf("QuarantineRepositoryWorkspace: %v", err)
	}

	resp, body := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/repository-workspaces/"+sw.repositoryWorkspaceID+"/reconcile",
		map[string]string{"Idempotency-Key": "key-1", "If-Match": etagFor(1)}, "{}") // stale: real current version is 2
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412; body = %+v", resp.StatusCode, body)
	}

	// The CURRENT If-Match (2) succeeds — proving 412 above was really about
	// staleness, not e.g. reconcile refusing QUARANTINED outright (it must
	// not: QUARANTINED is one of the two reconcilable states).
	okResp, okBody := ts.do(t, http.MethodPost, "/projects/"+sw.projectID+"/repository-workspaces/"+sw.repositoryWorkspaceID+"/reconcile",
		map[string]string{"Idempotency-Key": "key-2", "If-Match": etagFor(2)}, "{}")
	if okResp.StatusCode != http.StatusOK {
		t.Fatalf("current-version status = %d, want 200; body = %+v", okResp.StatusCode, okBody)
	}
}

func TestRequestWorkspaceReconciliation_CrossProject_ReturnsResourceHidden(t *testing.T) {
	ts := startWorkspaceTestServer(t)
	sw := ts.seedReadyWorkspace(t, "1")

	resp, body := ts.do(t, http.MethodPost, "/projects/some-other-project/repository-workspaces/"+sw.repositoryWorkspaceID+"/reconcile",
		map[string]string{"Idempotency-Key": "key-1", "If-Match": etagFor(1)}, "{}")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %+v", resp.StatusCode, body)
	}
	if errorCode(t, body) != "NOT_FOUND" {
		t.Fatalf("error.code = %q, want NOT_FOUND", errorCode(t, body))
	}
}

// --- route registration / scope ------------------------------------------

// TestRegisterWorkspaceRoutes_RegistersFourProjectScopedRoutes is this
// task's own "scope" Verify bullet at the registration level: every one of
// V6-10B's own 4 routes must declare ScopeProject (ADR-025) — none of these
// resources has installation-wide meaning — and every expected OperationID
// must be present exactly once (RouteRegistry.Register's own
// duplicate-OperationID panic already guards uniqueness structurally; this
// asserts the SET is exactly the 4 this task owns).
func TestRegisterWorkspaceRoutes_RegistersFourProjectScopedRoutes(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "workspace-routes.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)

	routes := httpapi.NewRouteRegistry()
	httpapi.RegisterWorkspaceRoutes(routes, uow, idsource.Random{})

	descriptors := routes.Descriptors()
	if len(descriptors) != 4 {
		t.Fatalf("len(descriptors) = %d, want 4", len(descriptors))
	}
	wantOperationIDs := map[string]bool{
		"getWorkspaceSetState": false, "getRepositoryWorkspaceState": false,
		"requestWorkspaceSetRelease": false, "requestWorkspaceReconciliation": false,
	}
	for _, d := range descriptors {
		if d.ScopeKind != httpapi.ScopeProject {
			t.Fatalf("route %s %s: ScopeKind = %v, want ScopeProject", d.Method, d.Path, d.ScopeKind)
		}
		if _, known := wantOperationIDs[d.OperationID]; !known {
			t.Fatalf("unexpected OperationID %q for route %s %s", d.OperationID, d.Method, d.Path)
		}
		wantOperationIDs[d.OperationID] = true
	}
	for op, seen := range wantOperationIDs {
		if !seen {
			t.Fatalf("operation %q was never registered", op)
		}
	}
}
