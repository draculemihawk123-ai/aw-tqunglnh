package message_test

// Real HTTP round-trip coverage for V6-07 (docs/design/08-v6-api-projections.md):
// every test in this file drives a REAL httpapi.Server (real TCP loopback
// listener, real middleware chain, real session-token/Origin/Host guards)
// backed by a REAL *sqlite.Store and a REAL filesystem
// internal/adapters/artifactstore.Store — never a mock, never a direct row
// fabrication (this repo's own hard rule; mirrors
// internal/delivery/httpapi/workitem/workitem_test.go's own newTestEnv
// idiom, extended with the ArtifactStore/Matcher/CursorCodec this
// package's own Dependencies additionally needs).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	message "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

const testSessionToken = "test-message-session-token"

func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}
}

// testEnv is one real Server + real UnitOfWork + real ArtifactStore triple,
// torn down via t.Cleanup.
type testEnv struct {
	server        *httpapi.Server
	base          string
	client        *http.Client
	uow           ports.UnitOfWork
	artifactStore ports.ArtifactStore
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "message-http.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	artifactStore, err := artifactstore.New(filepath.Join(t.TempDir(), "artifacts"))
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}

	// The shared matcher knows testSessionToken as a secret — the same
	// "sessionToken registered as a known secret with the shared redactor"
	// wiring cmd/aw/serve.go itself does — so a test can prove the real
	// process-lifetime Matcher (never a caller-supplied HTTP field, see
	// dependencies.go's own Matcher doc comment) actually redacts against
	// real persisted content.
	matcher := redact.NewMatcher(testSessionToken)
	cursorCodec := httpapi.NewCursorCodec([]byte("test-cursor-signing-secret-0123456789"))

	reg := httpapi.NewRouteRegistry()
	message.RegisterRoutes(reg, message.Dependencies{
		UnitOfWork: uow, ArtifactStore: artifactStore, IDs: idsource.Random{}, Clock: clock.System{},
		Matcher: matcher, Cursor: cursorCodec,
	})

	server, err := httpapi.NewServer(httpapi.Config{
		Host: "127.0.0.1", Port: 0, Routes: reg, IDs: idsource.Random{},
		Logger:       logging.New(io.Discard, logging.JSON, redact.NewMatcher()),
		MaxBodyBytes: 4 * appmessage.MaxContentSize, Token: testSessionToken, Principal: testPrincipal(),
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

	return &testEnv{
		server: server, base: "http://" + server.Addr(), client: &http.Client{Timeout: 10 * time.Second},
		uow: uow, artifactStore: artifactStore,
	}
}

// do issues a real HTTP request against e's own real server.
// idempotencyKey is omitted from the request entirely when "".
func (e *testEnv) do(t *testing.T, method, path, idempotencyKey string, body any) *http.Response {
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

// seedProject mirrors internal/delivery/httpapi/workitem/workitem_test.go's
// own seedProject exactly (real tx.Catalog().CreateProject call — test
// setup, not the behavior under test).
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
			NextStatus: project.RepositoryProbing,
		}); err != nil {
			return err
		}
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryActive,
		})
		return err
	})
	if err != nil {
		t.Fatalf("activate repository %s: %v", repositoryID, err)
	}
}

// seedRootWorkItem creates a root WorkItem via the real
// internal/app/work.CreateRootWorkItem command directly (never via this
// package's own HTTP routes, which are the behavior under test, not test
// setup) — mirrors internal/app/message/commands_test.go's own
// setupFixture idiom.
func (e *testEnv) seedRootWorkItem(t *testing.T, projectID, repositoryID, suffix string) workapp.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	cmd := ports.Command{
		ID: "cmd-root-" + suffix, IdempotencyKey: "idem-root-" + suffix, Actor: "seed",
		CorrelationID: "seed", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "CreateRootWorkItem", RequestHash: "hash-root-" + suffix,
	}
	result, err := workapp.CreateRootWorkItem(ctx, e.uow, idsource.Random{}, cmd, workapp.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: "Task " + suffix,
		InitialScope: []workapp.ScopeGrantRequest{{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"services/" + suffix}, Reason: "seed " + suffix,
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem(%s): %v", suffix, err)
	}
	return result
}

// assertStoredContent loads artifactID's own row via the real UnitOfWork
// and opens its real content through the real ArtifactStore — mirrors
// internal/app/message/commands_test.go's own identical read-back idiom,
// the established pattern this codebase already uses to verify redaction
// against ACTUAL persisted content, never merely an absence-of-code check.
func assertStoredContent(t *testing.T, env *testEnv, artifactID, want string) {
	t.Helper()
	ctx := context.Background()
	var ref ports.ArtifactRef
	if err := env.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		a, err := tx.Artifacts().GetArtifact(ctx, artifactID)
		ref = ports.ArtifactRef{Locator: a.Locator, SHA256: a.ContentHash, Size: a.Size}
		return err
	}); err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	reader, err := env.artifactStore.Open(ctx, ref)
	if err != nil {
		t.Fatalf("Open content: %v", err)
	}
	defer reader.Close()
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read content: %v", err)
	}
	if string(content) != want {
		t.Fatalf("stored content = %q, want %q", content, want)
	}
}

func appendBody(role, content, contentType, sensitivity, attemptID string) map[string]any {
	body := map[string]any{"role": role, "content": content, "contentType": contentType}
	if sensitivity != "" {
		body["sensitivity"] = sensitivity
	}
	if attemptID != "" {
		body["attemptId"] = attemptID
	}
	return body
}

// TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet mirrors
// workitem_test.go's own identical test: the closed set of four
// operationIds below is EVERY route this package ever registers —
// appendConversationAttachment (V6-07A) joins the original V6-07 three.
func TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet(t *testing.T) {
	reg := httpapi.NewRouteRegistry()
	message.RegisterRoutes(reg, message.Dependencies{IDs: idsource.Random{}, Clock: clock.System{}})

	want := map[string]bool{
		"appendMessage": true, "listMessages": true, "getMessageContextSnapshot": true,
		"appendConversationAttachment": true,
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
			t.Errorf("operationId %q has ScopeKind %q, want PROJECT", d.OperationID, d.ScopeKind)
		}
		delete(want, d.OperationID)
	}
	if len(want) != 0 {
		t.Errorf("missing operationIds: %v", want)
	}
}

// --- AppendMessage ---

func TestAppendMessage_HappyPath_PersistsRetrievableContent(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	resp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "idem-msg-1",
		appendBody("USER", "hello from the user", "text/plain", "", ""))
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201, body=%s", resp.StatusCode, body)
	}
	var result appmessage.AppendMessageResult
	decodeInto(t, resp, &result)
	if result.WorkItemID != root.WorkItemID || result.ProjectID != "project-1" || result.Sequence != 1 {
		t.Fatalf("result = %+v", result)
	}
	assertStoredContent(t, env, result.ContentArtifactID, "hello from the user")
}

func TestAppendMessage_Idempotent_ReplaysWithoutDuplicating(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	body := appendBody("USER", "hello", "text/plain", "", "")

	first := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "idem-msg-1", body)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status = %d, want 201", first.StatusCode)
	}
	var firstResult appmessage.AppendMessageResult
	decodeInto(t, first, &firstResult)

	second := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "idem-msg-1", body)
	if second.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(second.Body)
		t.Fatalf("replay status = %d, want 200, body=%s", second.StatusCode, body)
	}
	var secondResult appmessage.AppendMessageResult
	decodeInto(t, second, &secondResult)
	if firstResult != secondResult {
		t.Fatalf("replay = %+v, want identical %+v", secondResult, firstResult)
	}

	listResp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "", nil)
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	decodeInto(t, listResp, &list)
	if len(list.Items) != 1 {
		t.Fatalf("len(items) = %d, want 1 (no duplicate from the replay)", len(list.Items))
	}
}

func TestAppendMessage_DifferentBodySameIdempotencyKey_ReturnsConflict(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	first := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "idem-msg-1",
		appendBody("USER", "hello", "text/plain", "", ""))
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status = %d, want 201", first.StatusCode)
	}
	first.Body.Close()

	second := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "idem-msg-1",
		appendBody("USER", "a totally different message", "text/plain", "", ""))
	if second.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(second.Body)
		t.Fatalf("status = %d, want 409, body=%s", second.StatusCode, body)
	}
}

func TestAppendMessage_MissingIdempotencyKey_Returns400(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	resp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "",
		appendBody("USER", "hello", "text/plain", "", ""))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestAppendMessage_RejectsInvalidRequests(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	base := "/projects/project-1/work-items/" + root.WorkItemID + "/messages"

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing role", appendBody("", "hello", "text/plain", "", "")},
		{"invalid role", appendBody("NOT_A_ROLE", "hello", "text/plain", "", "")},
		{"missing content", appendBody("USER", "", "text/plain", "", "")},
		{"missing contentType", appendBody("USER", "hello", "", "", "")},
		{"invalid sensitivity", appendBody("USER", "hello", "text/plain", "NOT_A_LEVEL", "")},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := env.do(t, http.MethodPost, base, fmt.Sprintf("idem-invalid-%d", i), tc.body)
			if resp.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, body)
			}
			resp.Body.Close()
		})
	}
}

func TestAppendMessage_ContentTooLarge_Returns413(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	oversized := strings.Repeat("a", appmessage.MaxContentSize+1)
	resp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "idem-msg-1",
		appendBody("USER", oversized, "text/plain", "", ""))
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 413, body=%s", resp.StatusCode, body)
	}
}

func TestAppendMessage_UnknownWorkItem_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")

	resp := env.do(t, http.MethodPost, "/projects/project-1/work-items/does-not-exist/messages", "idem-msg-1",
		appendBody("USER", "hello", "text/plain", "", ""))
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestAppendMessage_WorkItemBelongsToAnotherProject_ReturnsHiddenNotFound is
// this task's own "scope" Verify bullet: a real WorkItem, just not one
// belonging to the project named in the path, must be indistinguishable
// from a genuinely nonexistent one (V6-02A's leakage-normalization policy)
// — never trust an unverified WorkItemID to leak another project's
// messages.
func TestAppendMessage_WorkItemBelongsToAnotherProject_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	env.seedProject(t, "project-2")
	env.seedActiveRepository(t, "project-2", "repo-b")
	other := env.seedRootWorkItem(t, "project-2", "repo-b", "2")

	resp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+other.WorkItemID+"/messages", "idem-msg-1",
		appendBody("USER", "leak attempt", "text/plain", "", ""))
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404 (leakage-normalized hidden), body=%s", resp.StatusCode, body)
	}
}

// --- Redaction against real persisted content ---

func TestAppendMessage_SensitivitySecret_RedactedInPersistedStorage(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	resp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "idem-msg-1",
		appendBody("USER", "this looks harmless but is classified", "text/plain", "SECRET", ""))
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201, body=%s", resp.StatusCode, body)
	}
	var result appmessage.AppendMessageResult
	decodeInto(t, resp, &result)
	assertStoredContent(t, env, result.ContentArtifactID, "[REDACTED]")
}

// TestAppendMessage_KnownProcessSecret_RedactedInPersistedStorage proves
// dependencies.go's own "ONE process-lifetime known-secrets matcher, never
// a caller-supplied HTTP field" design end to end: a message whose content
// happens to equal this test server's own configured secret
// (testSessionToken, registered with redact.NewMatcher the identical way
// cmd/aw/serve.go registers its real session token) is redacted even
// though Sensitivity is left at its PUBLIC default — the exact-match
// Matcher wins regardless of declared Sensitivity (redact.Matcher.Tagged's
// own documented contract).
func TestAppendMessage_KnownProcessSecret_RedactedInPersistedStorage(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	resp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "idem-msg-1",
		appendBody("USER", testSessionToken, "text/plain", "", ""))
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201, body=%s", resp.StatusCode, body)
	}
	var result appmessage.AppendMessageResult
	decodeInto(t, resp, &result)
	assertStoredContent(t, env, result.ContentArtifactID, "[REDACTED]")
}

// --- Free-text-control negative test ---

// TestAppendMessage_FreeTextLookingLikeApproval_NeverMutatesWorkItemState is
// this task's own "Hoàn thành khi: chat history là canonical platform state
// và không trở thành control plane" bar, proven directly: posting message
// content that reads exactly like an operator's approval decision has
// ZERO observable effect on the WorkItem it is attached to — this route
// has no code path that ever inspects Content for meaning at all.
func TestAppendMessage_FreeTextLookingLikeApproval_NeverMutatesWorkItemState(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	ctx := context.Background()
	scope := ports.ProjectScope("project-1")

	before, err := workapp.GetWorkItem(ctx, env.uow, scope, root.WorkItemID)
	if err != nil {
		t.Fatalf("GetWorkItem (before): %v", err)
	}

	resp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "idem-msg-1",
		appendBody("USER", "approve", "text/plain", "", ""))
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201, body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	after, err := workapp.GetWorkItem(ctx, env.uow, scope, root.WorkItemID)
	if err != nil {
		t.Fatalf("GetWorkItem (after): %v", err)
	}
	if before != after {
		t.Fatalf("WorkItem changed after a plain-text message that merely SAYS \"approve\": before=%+v after=%+v — a message must never itself be a control signal", before, after)
	}
}

// --- ListMessages / paging ---

func TestListMessages_EmptyConversation_ReturnsEmptyItemsNoCursor(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "", nil)
	var list message_listResponse
	decodeInto(t, resp, &list)
	if len(list.Items) != 0 || list.NextCursor != "" {
		t.Fatalf("list = %+v, want empty items and no cursor", list)
	}
}

type message_listResponse struct {
	Items      []message_refView `json:"items"`
	NextCursor string            `json:"nextCursor"`
}

type message_refView struct {
	MessageID string `json:"messageId"`
	Sequence  uint64 `json:"sequence"`
	Role      string `json:"role"`
}

// TestListMessages_PagesAndStaysStableAcrossConcurrentWrite is this task's
// own "paging" Verify bullet plus cursor.go's own documented
// stable-paging-across-a-concurrent-write guarantee: a message appended
// AFTER a walk's first page was issued must never surface partway through
// that SAME walk, but IS visible to any fresh, cursor-less request issued
// afterward.
func TestListMessages_PagesAndStaysStableAcrossConcurrentWrite(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")
	base := "/projects/project-1/work-items/" + root.WorkItemID + "/messages"

	for i := 1; i <= 2; i++ {
		resp := env.do(t, http.MethodPost, base, fmt.Sprintf("idem-msg-%d", i),
			appendBody("USER", fmt.Sprintf("message %d", i), "text/plain", "", ""))
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("append %d status = %d, body=%s", i, resp.StatusCode, body)
		}
		resp.Body.Close()
	}

	page1Resp := env.do(t, http.MethodGet, base+"?limit=1", "", nil)
	var page1 message_listResponse
	decodeInto(t, page1Resp, &page1)
	if len(page1.Items) != 1 || page1.Items[0].Sequence != 1 || page1.NextCursor == "" {
		t.Fatalf("page1 = %+v, want exactly [seq1] with a NextCursor", page1)
	}

	// A concurrent write lands BETWEEN page 1 and page 2 of the SAME walk.
	thirdResp := env.do(t, http.MethodPost, base, "idem-msg-3", appendBody("USER", "message 3", "text/plain", "", ""))
	if thirdResp.StatusCode != http.StatusCreated {
		t.Fatalf("append 3 status = %d", thirdResp.StatusCode)
	}
	thirdResp.Body.Close()

	page2Resp := env.do(t, http.MethodGet, base+"?limit=1&cursor="+url.QueryEscape(page1.NextCursor), "", nil)
	var page2 message_listResponse
	decodeInto(t, page2Resp, &page2)
	if len(page2.Items) != 1 || page2.Items[0].Sequence != 2 {
		t.Fatalf("page2 = %+v, want exactly [seq2]", page2)
	}
	if page2.NextCursor != "" {
		t.Fatalf("page2.NextCursor = %q, want empty — the walk begun before message 3 was appended must never surface it", page2.NextCursor)
	}

	freshResp := env.do(t, http.MethodGet, base, "", nil)
	var fresh message_listResponse
	decodeInto(t, freshResp, &fresh)
	if len(fresh.Items) != 3 {
		t.Fatalf("fresh list len = %d, want 3 (a fresh, cursor-less request DOES see the new message)", len(fresh.Items))
	}
}

func TestListMessages_InvalidCursor_Returns400(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/messages?cursor=not-a-real-token", "", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestListMessages_CursorFromAnotherWorkItem_ResyncRequired proves
// cursor.go's own Bind/QUERY_CHANGED resync path is actually reachable
// through this route: a valid, correctly-signed cursor minted for one
// WorkItem's own walk must never silently resume against a different
// WorkItem's own list.
func TestListMessages_CursorFromAnotherWorkItem_ResyncRequired(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	rootA := env.seedRootWorkItem(t, "project-1", "repo-a", "a")
	rootB := env.seedRootWorkItem(t, "project-1", "repo-a", "b")

	for i := 1; i <= 2; i++ {
		resp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+rootA.WorkItemID+"/messages",
			fmt.Sprintf("idem-a-%d", i), appendBody("USER", fmt.Sprintf("message %d", i), "text/plain", "", ""))
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("append to A status = %d", resp.StatusCode)
		}
		resp.Body.Close()
	}
	page1Resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+rootA.WorkItemID+"/messages?limit=1", "", nil)
	var page1 message_listResponse
	decodeInto(t, page1Resp, &page1)
	if page1.NextCursor == "" {
		t.Fatalf("expected a NextCursor from work item A's own first page")
	}

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+rootB.WorkItemID+"/messages?cursor="+url.QueryEscape(page1.NextCursor), "", nil)
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 409 RESYNC_REQUIRED, body=%s", resp.StatusCode, body)
	}
}

func TestListMessages_WorkItemBelongsToAnotherProject_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedProject(t, "project-2")
	env.seedActiveRepository(t, "project-2", "repo-b")
	other := env.seedRootWorkItem(t, "project-2", "repo-b", "2")

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+other.WorkItemID+"/messages", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// --- getMessageContextSnapshot: scope + absent cases (fixture-heavy
// "resolves when present" case lives in context_snapshot_test.go) ---

func TestGetMessageContextSnapshot_MessageHasNoAttempt_Returns404(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	appendResp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+root.WorkItemID+"/messages", "idem-msg-1",
		appendBody("USER", "no attempt linkage here", "text/plain", "", ""))
	var result appmessage.AppendMessageResult
	decodeInto(t, appendResp, &result)

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/messages/"+result.MessageID+"/context-snapshot", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, body)
	}
}

func TestGetMessageContextSnapshot_UnknownMessage_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.seedRootWorkItem(t, "project-1", "repo-a", "1")

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+root.WorkItemID+"/messages/does-not-exist/context-snapshot", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestGetMessageContextSnapshot_MessageBelongsToAnotherWorkItem_ReturnsHiddenNotFound
// proves the handler cross-checks a resolved Message's own stored
// WorkItemID against the path's {workItemId} (never trusting the path
// alone) — a real Message ID, just attached to a DIFFERENT WorkItem in the
// SAME project, must still be indistinguishable from nonexistent.
func TestGetMessageContextSnapshot_MessageBelongsToAnotherWorkItem_ReturnsHiddenNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	rootA := env.seedRootWorkItem(t, "project-1", "repo-a", "a")
	rootB := env.seedRootWorkItem(t, "project-1", "repo-a", "b")

	appendResp := env.do(t, http.MethodPost, "/projects/project-1/work-items/"+rootA.WorkItemID+"/messages", "idem-msg-1",
		appendBody("USER", "belongs to A", "text/plain", "", ""))
	var result appmessage.AppendMessageResult
	decodeInto(t, appendResp, &result)

	resp := env.do(t, http.MethodGet, "/projects/project-1/work-items/"+rootB.WorkItemID+"/messages/"+result.MessageID+"/context-snapshot", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 404, body=%s", resp.StatusCode, body)
	}
}
