package releaseset_test

// Real HTTP round-trip coverage for V6-10F (docs/design/08-v6-api-projections.md):
// every test in this file drives a REAL httpapi.Server (real TCP loopback
// listener, real middleware chain, real session-token/Origin/Host guards)
// backed by a REAL *sqlite.Store — never a mock, never a direct row
// fabrication (this repo's own hard rule; mirrors
// internal/delivery/httpapi/workitem/workitem_test.go's own newTestEnv
// idiom, adapted for this package's own routes). Fixture setup for the
// owning Project/TaskFamily/RepositoryWorkspace rows uses the sqlite
// package's own exported SeedFixtureOwners/SeedFixtureRepositoryWorkspace
// helpers (internal/adapters/sqlite/fixtures.go) — the same helpers
// internal/app/releasesetcommit's own execute_test.go already uses,
// explicitly built for a cross-package acceptance test like this one to
// satisfy real foreign keys without reaching into sqlite's unexported
// internals; production code must never call them.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/releasesetcommit"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/releaseset"
)

const testSessionToken = "test-releaseset-session-token"

func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}
}

// testEnv is one real Server + real UnitOfWork/Store pair, torn down via
// t.Cleanup.
type testEnv struct {
	server *httpapi.Server
	base   string
	client *http.Client
	store  *sqlite.Store
	uow    ports.UnitOfWork
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "releaseset-http.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	reg := httpapi.NewRouteRegistry()
	releaseset.RegisterRoutes(reg, releaseset.Dependencies{UnitOfWork: uow, IDs: idsource.Random{}, Clock: clock.System{}})

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

	return &testEnv{server: server, base: "http://" + server.Addr(), client: &http.Client{Timeout: 10 * time.Second}, store: store, uow: uow}
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

// seedFamily mirrors internal/app/releasesetcommit/execute_test.go's own
// newExecuteFixture: one real Project/TaskFamily pair via sqlite's own
// exported SeedFixtureOwners.
func (e *testEnv) seedFamily(t *testing.T, projectID, familyID string) {
	t.Helper()
	if err := sqlite.SeedFixtureOwners(context.Background(), e.store, projectID, familyID, "work-"+familyID); err != nil {
		t.Fatalf("SeedFixtureOwners(%s, %s): %v", projectID, familyID, err)
	}
}

// seedReadyWorkspace mirrors execute_test.go's own use of
// SeedFixtureRepositoryWorkspace: one real repository + READY
// RepositoryWorkspace at generation 1/version 1, with a placeholder,
// non-resolvable locator — sufficient for every test in this file except
// the one that actually drives the real worker (see
// TestLocalCommitStatus_PartialAcrossTwoRepositories below), since this
// package's own HTTP handlers never touch a real workspace filesystem
// themselves (V6-10F's own "Không làm").
func (e *testEnv) seedReadyWorkspace(t *testing.T, projectID, familyID, workspaceSetID, repositoryID, repositoryWorkspaceID string) {
	t.Helper()
	if err := sqlite.SeedFixtureRepositoryWorkspace(context.Background(), e.store, projectID, familyID, workspaceSetID, repositoryID, repositoryWorkspaceID); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace(%s): %v", repositoryWorkspaceID, err)
	}
}

func repositoryReleaseJSON(repositoryID, base, result, verdict string) map[string]any {
	return map[string]any{
		"repositoryId": repositoryID, "baseVcsObjectId": base, "resultVcsObjectId": result, "verdict": verdict,
	}
}

// releaseSetListBody is this test file's own local mirror of
// releaseset's own unexported releaseSetListResponse wire shape — a test in
// package releaseset_test cannot reference an unexported type, so it
// decodes into an identically-tagged local type instead (only the JSON
// shape has to match).
type releaseSetListBody struct {
	Items []workapp.ReleaseSetDetail `json:"items"`
}

// --- route inventory ---

// TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet proves this
// task's own closed route set — mirrors workitem_test.go's own identical
// test.
func TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet(t *testing.T) {
	reg := httpapi.NewRouteRegistry()
	releaseset.RegisterRoutes(reg, releaseset.Dependencies{UnitOfWork: nil, IDs: idsource.Random{}, Clock: clock.System{}})

	want := map[string]bool{
		"createReleaseSet": true, "listReleaseSetsForFamily": true, "getReleaseSet": true,
		"sealReleaseSet": true, "abandonReleaseSet": true,
		"requestReleaseSetLocalCommit": true, "getReleaseSetLocalCommitStatus": true,
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
			t.Errorf("operationId %q has ScopeKind %q, want PROJECT (ADR-025: ReleaseSet/ReleaseSetLocalCommit are never installation-scoped)", d.OperationID, d.ScopeKind)
		}
		delete(want, d.OperationID)
	}
	if len(want) != 0 {
		t.Errorf("missing operationIds: %v", want)
	}
}

// TestRegisterRoutes_ExcludesAnyRemoteGitVerb is V6-10F's own "route
// inventory itself must exclude any remote verb" Verify line, enforced
// mechanically: no registered route's Path or OperationID may name a
// remote-Git operation. This whole task family is explicitly LOCAL-only
// (no push/fetch/PR/merge/rebase/force-push, ever — commands.go's own
// package doc comment). Checked as whole WORDS (Path/OperationID split on
// any non-letter and on a camelCase boundary), never a bare substring
// match — a naive substring check would flag "pr" inside "projects" or
// "releaseSets" as a false positive.
func TestRegisterRoutes_ExcludesAnyRemoteGitVerb(t *testing.T) {
	reg := httpapi.NewRouteRegistry()
	releaseset.RegisterRoutes(reg, releaseset.Dependencies{UnitOfWork: nil, IDs: idsource.Random{}, Clock: clock.System{}})

	remoteVerbs := map[string]bool{
		"push": true, "fetch": true, "pull": true, "pr": true, "merge": true,
		"rebase": true, "remote": true, "force": true,
	}
	for _, d := range reg.Descriptors() {
		for _, word := range routeWords(d.Path) {
			if remoteVerbs[word] {
				t.Errorf("route %s %s names a remote-Git verb %q — this task family is LOCAL-only", d.Method, d.Path, word)
			}
		}
		for _, word := range routeWords(d.OperationID) {
			if remoteVerbs[word] {
				t.Errorf("operationId %q names a remote-Git verb %q — this task family is LOCAL-only", d.OperationID, word)
			}
		}
	}
}

// routeWords splits s into lowercase words on both non-letter characters
// (/, {, }, -, _) and camelCase boundaries, so "releaseSets"/"release-sets"
// and "getReleaseSetLocalCommitStatus" all decompose into their own real
// component words ("release", "sets", "get", "release", "set", "local",
// "commit", "status", ...) rather than being scanned as one opaque blob a
// substring check could false-positive against.
func routeWords(s string) []string {
	var words []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			words = append(words, string(current))
			current = nil
		}
	}
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			flush()
			current = append(current, r+('a'-'A'))
		case r >= 'a' && r <= 'z':
			current = append(current, r)
		default:
			flush()
		}
	}
	flush()
	return words
}

// --- create / get / list / seal / abandon happy path ---

// TestFullJourney_CreateGetListSealAndLocalCommitRequestStatus is this
// task's own end-to-end happy path: create -> detail -> list -> seal ->
// request local commit -> observe its own accepted status.
func TestFullJourney_CreateGetListSealAndLocalCommitRequestStatus(t *testing.T) {
	env := newTestEnv(t)
	env.seedFamily(t, "project-1", "family-1")
	env.seedReadyWorkspace(t, "project-1", "family-1", "set-1", "repo-a", "rw-a")

	// 1. Create.
	createResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-create-1", "", map[string]any{
		"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev", "result-rev", "PASS")},
	})
	if createResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createResp.Body)
		t.Fatalf("POST release-sets status = %d, want 201, body=%s", createResp.StatusCode, body)
	}
	if etag := createResp.Header.Get(httpapi.ETagHeader); etag != `"1"` {
		t.Fatalf("POST release-sets ETag = %q, want \"1\"", etag)
	}
	var created workapp.ReleaseSetResult
	decodeInto(t, createResp, &created)
	if created.ProjectID != "project-1" || created.FamilyID != "family-1" || created.State != "CREATED" {
		t.Fatalf("created = %+v, want ProjectID=project-1 FamilyID=family-1 State=CREATED", created)
	}

	// 2. Authoritative detail.
	detailResp := env.do(t, http.MethodGet, "/projects/project-1/release-sets/"+created.ReleaseSetID, "", "", nil)
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("GET release-set status = %d, want 200", detailResp.StatusCode)
	}
	var detail workapp.ReleaseSetDetail
	decodeInto(t, detailResp, &detail)
	if detail.ReleaseSetID != created.ReleaseSetID || len(detail.Entries) != 1 || detail.Entries[0].RepositoryID != "repo-a" {
		t.Fatalf("detail = %+v, want exactly one entry for repo-a", detail)
	}

	// 3. List (must contain exactly this release set).
	listResp := env.do(t, http.MethodGet, "/projects/project-1/task-families/family-1/release-sets", "", "", nil)
	var list releaseSetListBody
	decodeInto(t, listResp, &list)
	if len(list.Items) != 1 || list.Items[0].ReleaseSetID != created.ReleaseSetID {
		t.Fatalf("list.Items = %+v, want exactly [created]", list.Items)
	}

	// 4. Seal.
	sealResp := env.do(t, http.MethodPost, "/projects/project-1/release-sets/"+created.ReleaseSetID+"/seal", "idem-seal-1", `"1"`, map[string]any{})
	if sealResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(sealResp.Body)
		t.Fatalf("POST seal status = %d, want 200, body=%s", sealResp.StatusCode, body)
	}
	var sealed workapp.ReleaseSetResult
	decodeInto(t, sealResp, &sealed)
	if sealed.State != "SEALED" {
		t.Fatalf("sealed.State = %q, want SEALED", sealed.State)
	}

	// 5. Request a local commit for repo-a's own RepositoryWorkspace.
	requestResp := env.do(t, http.MethodPost, "/projects/project-1/release-sets/"+created.ReleaseSetID+"/local-commits", "idem-lc-1", "", map[string]any{
		"expectedReleaseSetVersion": 2, "repositoryWorkspaceId": "rw-a", "expectedWorkspaceVersion": 1,
		"message": "record repository result", "authorName": "Release Bot", "authorEmail": "release-bot@example.invalid",
	})
	if requestResp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(requestResp.Body)
		t.Fatalf("POST local-commits status = %d, want 202, body=%s", requestResp.StatusCode, body)
	}
	var accepted releasesetcommit.RequestReleaseSetLocalCommitResult
	decodeInto(t, requestResp, &accepted)
	if accepted.State != "REQUESTED" || accepted.ReleaseSetID != created.ReleaseSetID || accepted.RepositoryWorkspaceID != "rw-a" {
		t.Fatalf("accepted = %+v, want State=REQUESTED ReleaseSetID=%s RepositoryWorkspaceID=rw-a", accepted, created.ReleaseSetID)
	}
	if accepted.JobID == "" || accepted.Marker == "" {
		t.Fatalf("accepted = %+v, want non-empty JobID/Marker", accepted)
	}

	// 6. Observe the accepted operation's own status — never a synchronous
	// "done" result (V6-10F's own "Không làm").
	statusResp := env.do(t, http.MethodGet, "/projects/project-1/release-sets/"+created.ReleaseSetID+"/local-commits/"+accepted.ReleaseSetLocalCommitID, "", "", nil)
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("GET local-commit status = %d, want 200", statusResp.StatusCode)
	}
	var status workapp.ReleaseSetLocalCommitStatus
	decodeInto(t, statusResp, &status)
	if status.State != "REQUESTED" || status.ResultVCSObjectID != "" {
		t.Fatalf("status = %+v, want State=REQUESTED and no ResultVCSObjectID yet (the real Git work has not run)", status)
	}
	if status.ReleaseSetLocalCommitID != accepted.ReleaseSetLocalCommitID || status.RepositoryWorkspaceID != "rw-a" {
		t.Fatalf("status = %+v, want it to name the exact operation just requested", status)
	}
}

// TestAbandonReleaseSet_HappyPath proves the sibling terminal transition.
func TestAbandonReleaseSet_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	env.seedFamily(t, "project-1", "family-1")
	env.seedReadyWorkspace(t, "project-1", "family-1", "set-1", "repo-a", "rw-a")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-abandon-create", "", map[string]any{
		"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev", "result-rev", "FAIL")},
	})
	var created workapp.ReleaseSetResult
	decodeInto(t, createResp, &created)

	abandonResp := env.do(t, http.MethodPost, "/projects/project-1/release-sets/"+created.ReleaseSetID+"/abandon", "idem-abandon-1", `"1"`, map[string]any{})
	if abandonResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(abandonResp.Body)
		t.Fatalf("POST abandon status = %d, want 200, body=%s", abandonResp.StatusCode, body)
	}
	var abandoned workapp.ReleaseSetResult
	decodeInto(t, abandonResp, &abandoned)
	if abandoned.State != "ABANDONED" {
		t.Fatalf("abandoned.State = %q, want ABANDONED", abandoned.State)
	}
}

// --- replay ---

// TestCreateReleaseSet_SameIdempotencyKey_ReplaysWithoutCreatingSecondReleaseSet
// proves V6-02's own replay contract end-to-end.
func TestCreateReleaseSet_SameIdempotencyKey_ReplaysWithoutCreatingSecondReleaseSet(t *testing.T) {
	env := newTestEnv(t)
	env.seedFamily(t, "project-1", "family-1")
	env.seedReadyWorkspace(t, "project-1", "family-1", "set-1", "repo-a", "rw-a")

	body := map[string]any{"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev", "result-rev", "PASS")}}
	first := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-replay-1", "", body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", first.StatusCode)
	}
	var firstResult workapp.ReleaseSetResult
	decodeInto(t, first, &firstResult)

	second := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-replay-1", "", body)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 (WriteReceiptReplay always 200)", second.StatusCode)
	}
	var secondResult workapp.ReleaseSetResult
	decodeInto(t, second, &secondResult)
	if secondResult.ReleaseSetID != firstResult.ReleaseSetID {
		t.Fatalf("replay ReleaseSetID = %q, want exact original %q", secondResult.ReleaseSetID, firstResult.ReleaseSetID)
	}

	listResp := env.do(t, http.MethodGet, "/projects/project-1/task-families/family-1/release-sets", "", "", nil)
	var list releaseSetListBody
	decodeInto(t, listResp, &list)
	if len(list.Items) != 1 {
		t.Fatalf("len(list.Items) = %d, want exactly 1 (replay must not create a second row)", len(list.Items))
	}
}

// TestCreateReleaseSet_SameKeyDifferentBody_ConflictsBeforeSecondInsert
// proves V6-02's own "different body conflict trước I/O" line end-to-end.
func TestCreateReleaseSet_SameKeyDifferentBody_ConflictsBeforeSecondInsert(t *testing.T) {
	env := newTestEnv(t)
	env.seedFamily(t, "project-1", "family-1")
	env.seedReadyWorkspace(t, "project-1", "family-1", "set-1", "repo-a", "rw-a")

	first := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-conflict-1", "", map[string]any{
		"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev", "result-rev", "PASS")},
	})
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", first.StatusCode)
	}
	first.Body.Close()

	second := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-conflict-1", "", map[string]any{
		"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev", "a-different-result-rev", "PASS")},
	})
	if second.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(second.Body)
		t.Fatalf("same-key-different-body status = %d, want 409, body=%s", second.StatusCode, body)
	}
}

// TestRequestReleaseSetLocalCommit_SameIdempotencyKey_ReplaysWithoutSecondOperation
// mirrors the create-side replay test for the local-commit request route.
func TestRequestReleaseSetLocalCommit_SameIdempotencyKey_ReplaysWithoutSecondOperation(t *testing.T) {
	env := newTestEnv(t)
	env.seedFamily(t, "project-1", "family-1")
	env.seedReadyWorkspace(t, "project-1", "family-1", "set-1", "repo-a", "rw-a")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-lcreplay-create", "", map[string]any{
		"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev", "result-rev", "PASS")},
	})
	var created workapp.ReleaseSetResult
	decodeInto(t, createResp, &created)

	body := map[string]any{
		"expectedReleaseSetVersion": 1, "repositoryWorkspaceId": "rw-a", "expectedWorkspaceVersion": 1,
		"message": "record repository result", "authorName": "Release Bot", "authorEmail": "release-bot@example.invalid",
	}
	first := env.do(t, http.MethodPost, "/projects/project-1/release-sets/"+created.ReleaseSetID+"/local-commits", "idem-lc-replay-1", "", body)
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first request status = %d, want 202", first.StatusCode)
	}
	var firstResult releasesetcommit.RequestReleaseSetLocalCommitResult
	decodeInto(t, first, &firstResult)

	second := env.do(t, http.MethodPost, "/projects/project-1/release-sets/"+created.ReleaseSetID+"/local-commits", "idem-lc-replay-1", "", body)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 (WriteReceiptReplay always 200)", second.StatusCode)
	}
	var secondResult releasesetcommit.RequestReleaseSetLocalCommitResult
	decodeInto(t, second, &secondResult)
	if secondResult.ReleaseSetLocalCommitID != firstResult.ReleaseSetLocalCommitID {
		t.Fatalf("replay ReleaseSetLocalCommitID = %q, want exact original %q", secondResult.ReleaseSetLocalCommitID, firstResult.ReleaseSetLocalCommitID)
	}
}

// --- stale / precondition ---

// TestSealReleaseSet_StaleIfMatch_PreconditionFailed proves this package's
// own pre-dispatch version check.
func TestSealReleaseSet_StaleIfMatch_PreconditionFailed(t *testing.T) {
	env := newTestEnv(t)
	env.seedFamily(t, "project-1", "family-1")
	env.seedReadyWorkspace(t, "project-1", "family-1", "set-1", "repo-a", "rw-a")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-stale-create", "", map[string]any{
		"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev", "result-rev", "PASS")},
	})
	var created workapp.ReleaseSetResult
	decodeInto(t, createResp, &created)

	staleResp := env.do(t, http.MethodPost, "/projects/project-1/release-sets/"+created.ReleaseSetID+"/seal", "idem-stale-seal-1", `"99"`, map[string]any{})
	if staleResp.StatusCode != http.StatusPreconditionFailed {
		body, _ := io.ReadAll(staleResp.Body)
		t.Fatalf("stale If-Match status = %d, want 412, body=%s", staleResp.StatusCode, body)
	}
}

// TestSealReleaseSet_AlreadySealed_Conflict proves workapp.ErrReleaseSetNotOpen
// surfaces as a real, visible 409 — a second, genuinely distinct seal
// attempt (different Idempotency-Key, never a replay of the first) against
// an already-terminal ReleaseSet must never silently succeed a second time.
func TestSealReleaseSet_AlreadySealed_Conflict(t *testing.T) {
	env := newTestEnv(t)
	env.seedFamily(t, "project-1", "family-1")
	env.seedReadyWorkspace(t, "project-1", "family-1", "set-1", "repo-a", "rw-a")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-double-seal-create", "", map[string]any{
		"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev", "result-rev", "PASS")},
	})
	var created workapp.ReleaseSetResult
	decodeInto(t, createResp, &created)

	first := env.do(t, http.MethodPost, "/projects/project-1/release-sets/"+created.ReleaseSetID+"/seal", "idem-double-seal-1", `"1"`, map[string]any{})
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first seal status = %d, want 200", first.StatusCode)
	}
	first.Body.Close()

	// Fresh reload for the new ETag (Seal bumped Version 1 -> 2).
	detailResp := env.do(t, http.MethodGet, "/projects/project-1/release-sets/"+created.ReleaseSetID, "", "", nil)
	etag := detailResp.Header.Get(httpapi.ETagHeader)
	detailResp.Body.Close()

	second := env.do(t, http.MethodPost, "/projects/project-1/release-sets/"+created.ReleaseSetID+"/seal", "idem-double-seal-2", etag, map[string]any{})
	if second.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(second.Body)
		t.Fatalf("second seal (already sealed) status = %d, want 409, body=%s", second.StatusCode, body)
	}
}

// TestRequestReleaseSetLocalCommit_StaleReleaseSetVersion_Conflict proves a
// caller's stale ExpectedReleaseSetVersion is a real, typed conflict —
// never silently pinning the wrong version.
func TestRequestReleaseSetLocalCommit_StaleReleaseSetVersion_Conflict(t *testing.T) {
	env := newTestEnv(t)
	env.seedFamily(t, "project-1", "family-1")
	env.seedReadyWorkspace(t, "project-1", "family-1", "set-1", "repo-a", "rw-a")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-stale-lc-create", "", map[string]any{
		"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev", "result-rev", "PASS")},
	})
	var created workapp.ReleaseSetResult
	decodeInto(t, createResp, &created)

	resp := env.do(t, http.MethodPost, "/projects/project-1/release-sets/"+created.ReleaseSetID+"/local-commits", "idem-stale-lc-1", "", map[string]any{
		"expectedReleaseSetVersion": 999, "repositoryWorkspaceId": "rw-a", "expectedWorkspaceVersion": 1,
		"message": "record repository result", "authorName": "Release Bot", "authorEmail": "release-bot@example.invalid",
	})
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("stale ExpectedReleaseSetVersion status = %d, want 409, body=%s", resp.StatusCode, body)
	}
}

// --- leakage normalization ---

// TestGetReleaseSet_AnotherProject_ReturnsNotFound proves the
// leakage-normalized cross-project response end-to-end.
func TestGetReleaseSet_AnotherProject_ReturnsNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedFamily(t, "project-1", "family-1")
	env.seedFamily(t, "project-2", "family-2")
	env.seedReadyWorkspace(t, "project-1", "family-1", "set-1", "repo-a", "rw-a")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-cross-1", "", map[string]any{
		"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev", "result-rev", "PASS")},
	})
	var created workapp.ReleaseSetResult
	decodeInto(t, createResp, &created)

	crossResp := env.do(t, http.MethodGet, "/projects/project-2/release-sets/"+created.ReleaseSetID, "", "", nil)
	if crossResp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-project GET status = %d, want 404", crossResp.StatusCode)
	}
	crossBytes, err := io.ReadAll(crossResp.Body)
	crossResp.Body.Close()
	if err != nil {
		t.Fatalf("read cross-project response body: %v", err)
	}

	unknownResp := env.do(t, http.MethodGet, "/projects/project-2/release-sets/genuinely-unknown-id", "", "", nil)
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

// TestGetReleaseSetLocalCommitStatus_WrongReleaseSetInPath_ReturnsNotFound
// proves this route's own extra leakage-normalization layer beyond plain
// ProjectID scoping: a ReleaseSetLocalCommitID that genuinely exists (and
// belongs to the right project) but is requested under a DIFFERENT release
// set's own URL must be just as invisible as an unknown ID.
func TestGetReleaseSetLocalCommitStatus_WrongReleaseSetInPath_ReturnsNotFound(t *testing.T) {
	env := newTestEnv(t)
	// One family/repository/RepositoryWorkspace, but TWO ReleaseSets both
	// referencing repo-a (a RepositoryWorkspace's own version is never
	// bumped merely by REQUESTING a local commit — only the eventual real
	// Git worker, never run in this test, would do that — so the same
	// rw-a@version-1 fence is valid for a local-commit request against
	// either ReleaseSet).
	env.seedFamily(t, "project-1", "family-1")
	env.seedReadyWorkspace(t, "project-1", "family-1", "set-1", "repo-a", "rw-a")

	createFirstResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-wrongrs-create-1", "", map[string]any{
		"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev-1", "result-rev-1", "PASS")},
	})
	var firstReleaseSet workapp.ReleaseSetResult
	decodeInto(t, createFirstResp, &firstReleaseSet)

	createSecondResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-wrongrs-create-2", "", map[string]any{
		"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev-2", "result-rev-2", "PASS")},
	})
	var secondReleaseSet workapp.ReleaseSetResult
	decodeInto(t, createSecondResp, &secondReleaseSet)

	requestResp := env.do(t, http.MethodPost, "/projects/project-1/release-sets/"+firstReleaseSet.ReleaseSetID+"/local-commits", "idem-wrongrs-lc-1", "", map[string]any{
		"expectedReleaseSetVersion": 1, "repositoryWorkspaceId": "rw-a", "expectedWorkspaceVersion": 1,
		"message": "record repository result", "authorName": "Release Bot", "authorEmail": "release-bot@example.invalid",
	})
	var accepted releasesetcommit.RequestReleaseSetLocalCommitResult
	decodeInto(t, requestResp, &accepted)

	// The same operation ID, but nested under the OTHER release set's own path.
	wrongResp := env.do(t, http.MethodGet, "/projects/project-1/release-sets/"+secondReleaseSet.ReleaseSetID+"/local-commits/"+accepted.ReleaseSetLocalCommitID, "", "", nil)
	if wrongResp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(wrongResp.Body)
		t.Fatalf("wrong-release-set-in-path GET status = %d, want 404, body=%s", wrongResp.StatusCode, body)
	}
}

// --- schema validation ---

// TestCreateReleaseSet_EmptyRepositories_Returns400WithFieldDetail proves
// this package's own pre-dispatch field validation produces a precise
// ErrorDetail rather than a generic failure.
func TestCreateReleaseSet_EmptyRepositories_Returns400WithFieldDetail(t *testing.T) {
	env := newTestEnv(t)
	env.seedFamily(t, "project-1", "family-1")

	resp := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-validate-1", "", map[string]any{
		"repositories": []any{},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty-repositories status = %d, want 400", resp.StatusCode)
	}
	var errBody httpapi.ErrorResponse
	decodeInto(t, resp, &errBody)
	if len(errBody.Error.Details) != 1 || errBody.Error.Details[0].Field != "repositories" {
		t.Fatalf("errBody.Error.Details = %+v, want exactly one detail for field \"repositories\"", errBody.Error.Details)
	}
}

// TestCreateReleaseSet_InvalidVerdict_Returns400WithFieldDetail proves the
// per-entry verdict validation this package's own dto.go adds ahead of the
// domain's own bare errors.New.
func TestCreateReleaseSet_InvalidVerdict_Returns400WithFieldDetail(t *testing.T) {
	env := newTestEnv(t)
	env.seedFamily(t, "project-1", "family-1")
	env.seedReadyWorkspace(t, "project-1", "family-1", "set-1", "repo-a", "rw-a")

	resp := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-validate-2", "", map[string]any{
		"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev", "result-rev", "NOT_A_REAL_VERDICT")},
	})
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("invalid-verdict status = %d, want 400, body=%s", resp.StatusCode, body)
	}
	var errBody httpapi.ErrorResponse
	decodeInto(t, resp, &errBody)
	if len(errBody.Error.Details) != 1 || errBody.Error.Details[0].Field != "repositories[0].verdict" {
		t.Fatalf("errBody.Error.Details = %+v, want exactly one detail for field \"repositories[0].verdict\"", errBody.Error.Details)
	}
}

// TestRequestReleaseSetLocalCommit_MissingMessage_Returns400WithFieldDetail
// mirrors the identical pre-dispatch discipline for the local-commit
// request route's own body.
func TestRequestReleaseSetLocalCommit_MissingMessage_Returns400WithFieldDetail(t *testing.T) {
	env := newTestEnv(t)
	env.seedFamily(t, "project-1", "family-1")
	env.seedReadyWorkspace(t, "project-1", "family-1", "set-1", "repo-a", "rw-a")

	createResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-lcvalidate-create", "", map[string]any{
		"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev", "result-rev", "PASS")},
	})
	var created workapp.ReleaseSetResult
	decodeInto(t, createResp, &created)

	resp := env.do(t, http.MethodPost, "/projects/project-1/release-sets/"+created.ReleaseSetID+"/local-commits", "idem-lcvalidate-1", "", map[string]any{
		"expectedReleaseSetVersion": 1, "repositoryWorkspaceId": "rw-a", "expectedWorkspaceVersion": 1,
		"message": "", "authorName": "Release Bot", "authorEmail": "release-bot@example.invalid",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing-message status = %d, want 400", resp.StatusCode)
	}
	var errBody httpapi.ErrorResponse
	decodeInto(t, resp, &errBody)
	if len(errBody.Error.Details) != 1 || errBody.Error.Details[0].Field != "message" {
		t.Fatalf("errBody.Error.Details = %+v, want exactly one detail for field \"message\"", errBody.Error.Details)
	}
}

// --- idempotency-key required ---

// TestMutatingRoutes_RequireIdempotencyKey proves every mutating route in
// this package rejects a request missing Idempotency-Key before ever
// touching a domain command — sampled via createReleaseSet, representative
// of the shared prepareCreateCommand/prepareUpdateCommand preamble every
// other mutating handler also runs through.
func TestMutatingRoutes_RequireIdempotencyKey(t *testing.T) {
	env := newTestEnv(t)
	env.seedFamily(t, "project-1", "family-1")
	env.seedReadyWorkspace(t, "project-1", "family-1", "set-1", "repo-a", "rw-a")

	resp := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "", "", map[string]any{
		"repositories": []any{repositoryReleaseJSON("repo-a", "base-rev", "result-rev", "PASS")},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing Idempotency-Key status = %d, want 400", resp.StatusCode)
	}
}
