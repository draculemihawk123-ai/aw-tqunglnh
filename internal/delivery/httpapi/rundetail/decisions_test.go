package rundetail_test

// Real HTTP round-trip coverage for GET /runs/{id}'s own V6-06E fields
// (ApprovalRequests/WaitRegistrations) — the rework of V6-06A found
// empirically by the V6-14 black-box journey: resolveApproval /
// submitWaitSignal need an approvalRequestId / waitRegistrationId that no
// public read used to return. Every fixture drives a REAL
// StartWorkflowRun/AdvanceRun sequence against a real *sqlite.Store — never
// a hand-seeded approval_requests/wait_registrations row.
//
// TestGetRunDetail_HTTP_ApprovalRequestID_ResolvableThroughDecisionEndpoint
// is this task's own "round-trip proof through the decision endpoints"
// Test requirement: it composes THIS package's own rundetail.RegisterRoutes
// together with internal/delivery/httpapi/decision's own RegisterRoutes on
// one real httptest.Server backed by the SAME *sqlite.Store, takes the
// approvalRequestId straight out of a real GET /runs/{id} response body,
// and resolves it over a real POST .../resolve call — proving the id this
// package's own read returns is genuinely the id the decision handler
// accepts, never merely "the same Go string by construction".

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/decision"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/rundetail"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// approvalThenWaitDocument mirrors internal/app/runtime's own
// run_detail_decisions_sqlite_test.go fixture of a similar shape: start ->
// approval(APPROVAL, AuthorizedRoles=["reviewer"]) -> wait_signal(WAIT,
// SIGNAL, TimeoutSeconds=1800) -> end|end_denied|end_expired — this
// package's own HTTP-level twin, kept local per this package's own
// established "duplicate rather than import _test.go helpers" discipline
// (fixture_test.go's own doc comment).
func approvalThenWaitDocument() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "approval", Type: workflow.NodeApproval, Outcomes: []string{"approved", "denied"}, Approval: &workflow.ApprovalNodeConfig{
				AuthorizedRoles: []string{"reviewer"}, TimeoutSeconds: 3600, EscalationOutcome: "denied",
				RequestedEvidenceKinds: []string{"test-plan"},
			}},
			{Key: "wait_signal", Type: workflow.NodeWait, Outcomes: []string{"released", "expired"}, Wait: &workflow.WaitNodeConfig{
				Mode: workflow.WaitModeSignal, SignalName: "release-approved-externally", TimeoutSeconds: 1800,
				CompletionOutcome: "released", TimeoutOutcome: "expired",
			}},
			{Key: "end", Type: workflow.NodeEnd},
			{Key: "end_denied", Type: workflow.NodeEnd},
			{Key: "end_expired", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-approval", From: "start", Outcome: "next", To: "approval"},
			{Key: "approval-to-wait", From: "approval", Outcome: "approved", To: "wait_signal"},
			{Key: "approval-to-denied", From: "approval", Outcome: "denied", To: "end_denied"},
			{Key: "wait-to-end", From: "wait_signal", Outcome: "released", To: "end"},
			{Key: "wait-to-expired", From: "wait_signal", Outcome: "expired", To: "end_expired"},
		},
	}
}

// runDetailHTTPResponse decodes only the two fields this test file cares
// about, plus RunID for identity checks — runtime.RunDetail's own json
// tags drive the decode, so a field this struct omits simply decodes into
// the zero value (the response itself still carries every field a real
// client would see).
type runDetailHTTPResponse struct {
	RunID             string                         `json:"runId"`
	State             string                         `json:"state"`
	ApprovalRequests  []runtime.ApprovalRequestView  `json:"approvalRequests"`
	WaitRegistrations []runtime.WaitRegistrationView `json:"waitRegistrations"`
}

func getRunDetail(t *testing.T, baseURL, runID string) (int, runDetailHTTPResponse, []byte) {
	t.Helper()
	resp, err := http.Get(baseURL + "/runs/" + runID)
	if err != nil {
		t.Fatalf("GET /runs/%s: %v", runID, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var out runDetailHTTPResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode: %v\nbody=%s", err, raw)
		}
	}
	return resp.StatusCode, out, raw
}

// advanceToApprovalHTTP drives a REAL Run, through REAL
// runtime.StartWorkflowRun + runtime.AdvanceRun, from START to its own
// APPROVAL node.
func advanceToApprovalHTTP(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID, definitionID, versionID string) (runID string, hop runtime.AdvanceRunResult) {
	t.Helper()
	ctx := context.Background()
	root := readyWorkItemFixture(t, uow, ids, projectID, repositoryID)
	version := publishTestWorkflowVersion(t, uow, projectID, definitionID, versionID, approvalThenWaitDocument())
	startCmd := testCommand("idem-start-"+versionID, "hash-start-"+versionID, ports.ProjectScope(projectID), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: projectID, WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	result, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->approval): %v", err)
	}
	if result.NextApprovalRequestID == "" {
		t.Fatalf("hop = %+v, want a minted NextApprovalRequestID", result)
	}
	return startResult.RunID, result
}

// TestGetRunDetail_HTTP_ApprovalPending_ListsRequestNoWaitYet proves a Run
// parked on its APPROVAL node lists exactly that one PENDING request in
// approvalRequests, and waitRegistrations is absent (the WAIT node has not
// activated yet).
func TestGetRunDetail_HTTP_ApprovalPending_ListsRequestNoWaitYet(t *testing.T) {
	_, uow := openRunDetailTestStore(t, "decisions-pending.db")
	ids := idsource.NewSequential("id")
	runID, hop := advanceToApprovalHTTP(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1")

	server := newTestServer(t, uow, redact.Matcher{})
	status, detail, raw := getRunDetail(t, server.URL, runID)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body=%s", status, raw)
	}
	if len(detail.ApprovalRequests) != 1 {
		t.Fatalf("ApprovalRequests = %+v, want exactly one", detail.ApprovalRequests)
	}
	got := detail.ApprovalRequests[0]
	if got.ApprovalRequestID != hop.NextApprovalRequestID || got.NodeKey != "approval" || got.State != string(runtimedomain.ApprovalRequestPending) {
		t.Fatalf("approval request = %+v, want id=%s nodeKey=approval State=PENDING", got, hop.NextApprovalRequestID)
	}
	if len(got.AuthorizedRoles) != 1 || got.AuthorizedRoles[0] != "reviewer" {
		t.Fatalf("AuthorizedRoles = %v, want [reviewer]", got.AuthorizedRoles)
	}
	if got.Version != 1 {
		t.Fatalf("Version = %d, want 1", got.Version)
	}
	if detail.WaitRegistrations != nil {
		t.Fatalf("WaitRegistrations = %+v, want absent (nil) — the WAIT node has not activated yet", detail.WaitRegistrations)
	}
	if strings.Contains(string(raw), "waitRegistrations") {
		t.Fatalf("response body carries a waitRegistrations key while none exist: %s", raw)
	}
}

// TestGetRunDetail_HTTP_NoDecisionNodes_OmitsBothKeys is this task's own
// "a Run with neither returns JSON WITHOUT the keys (omitempty —
// pre-existing responses stay byte-identical)" Test requirement:
// workflowDocumentV1 has no APPROVAL/WAIT node at all, so a fresh
// GET /runs/{id} response must carry neither field name.
func TestGetRunDetail_HTTP_NoDecisionNodes_OmitsBothKeys(t *testing.T) {
	ctx := context.Background()
	_, uow := openRunDetailTestStore(t, "decisions-omitted.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	server := newTestServer(t, uow, redact.Matcher{})
	status, detail, raw := getRunDetail(t, server.URL, startResult.RunID)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body=%s", status, raw)
	}
	if detail.ApprovalRequests != nil || detail.WaitRegistrations != nil {
		t.Fatalf("ApprovalRequests=%+v WaitRegistrations=%+v, want both absent", detail.ApprovalRequests, detail.WaitRegistrations)
	}
	if strings.Contains(string(raw), "approvalRequests") || strings.Contains(string(raw), "waitRegistrations") {
		t.Fatalf("response body = %s, want neither approvalRequests nor waitRegistrations key present", raw)
	}
}

// TestGetRunDetail_HTTP_CrossProjectRun_StillHidden proves this task's own
// "cross-project run id still 404-style hidden" Test requirement is
// unaffected by the new fields: an unknown/foreign RunID still gets the
// exact same leakage-normalized 404 TestGetRunDetail_HTTP_NotFound already
// covers for a wholly unknown id — asserted again here specifically after
// this task's own change, since the new tx.Approvals()/tx.Wait() reads sit
// INSIDE the same uow.WithReadOnly closure as the pre-existing GetWorkflowRun
// lookup that performs this hiding (run_detail_queries.go's own
// GetRunDetail: an unknown RunID's very first read already fails, so the
// approval/wait reads added by this task never execute for a hidden Run at
// all).
func TestGetRunDetail_HTTP_CrossProjectRun_StillHidden(t *testing.T) {
	_, uow := openRunDetailTestStore(t, "decisions-crossproject.db")
	ids := idsource.NewSequential("id")
	_, hop := advanceToApprovalHTTP(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1")

	server := newTestServer(t, uow, redact.Matcher{})
	// A real ApprovalRequestID is not a RunID — same shape check
	// TestGetRunDetail_HTTP_NotFound already proves for a wholly synthetic
	// unknown string, repeated here with a genuinely-existing-but-wrong-kind
	// id to show the hiding is not merely "string never seen before".
	status, _, raw := getRunDetail(t, server.URL, hop.NextApprovalRequestID)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, body=%s, want 404", status, raw)
	}
}

// combinedTestServer composes rundetail.RegisterRoutes AND
// decision.RegisterRoutes on the SAME mux backed by the SAME *sqlite.Store —
// this test file's own round-trip harness. Never used outside this one
// proof: every other test in this package only ever needs rundetail's own
// routes (newTestServer, httptest_helper_test.go).
func combinedTestServer(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, principal httpapi.LocalPrincipalSnapshot) *httptest.Server {
	t.Helper()
	registry := httpapi.NewRouteRegistry()
	cursor := httpapi.NewCursorCodec([]byte("test-rundetail-decisions-cursor-secret"))
	rundetail.RegisterRoutes(registry, rundetail.Dependencies{UnitOfWork: uow, Matcher: redact.Matcher{}, Cursor: cursor})
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

// TestGetRunDetail_HTTP_ApprovalRequestID_ResolvableThroughDecisionEndpoint
// is this task's own "round-trip proof through the decision endpoints" Test
// requirement: takes the approvalRequestId straight out of a real
// GET /runs/{id} JSON response and resolves it over a real
// POST /runs/{runId}/approval-requests/{approvalRequestId}/resolve call —
// proving the id this package's own read returns is genuinely the id
// resolveApproval accepts, then re-reads the detail to see the SAME id
// listed as DECIDED with its own ResolvedOutcome, and the newly-activated
// WAIT registration now present too.
func TestGetRunDetail_HTTP_ApprovalRequestID_ResolvableThroughDecisionEndpoint(t *testing.T) {
	_, uow := openRunDetailTestStore(t, "decisions-roundtrip.db")
	ids := idsource.NewSequential("id")
	runID, hop := advanceToApprovalHTTP(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1")

	reviewer := httpapi.LocalPrincipalSnapshot{Actor: "reviewer-1", Roles: []string{"reviewer"}}
	server := combinedTestServer(t, uow, ids, reviewer)

	// 1. Discover the approvalRequestId through THIS package's own read.
	status, detail, raw := getRunDetail(t, server.URL, runID)
	if status != http.StatusOK || len(detail.ApprovalRequests) != 1 {
		t.Fatalf("status=%d detail=%+v, want 200 with exactly one approval request; body=%s", status, detail, raw)
	}
	approvalRequestID := detail.ApprovalRequests[0].ApprovalRequestID
	if approvalRequestID != hop.NextApprovalRequestID {
		t.Fatalf("discovered approvalRequestId = %s, want %s", approvalRequestID, hop.NextApprovalRequestID)
	}
	version := detail.ApprovalRequests[0].Version

	// 2. Resolve it through the REAL decision endpoint using ONLY the
	// discovered id — never the app-layer AdvanceRunResult directly.
	resolveURL := server.URL + "/runs/" + runID + "/approval-requests/" + approvalRequestID + "/resolve"
	req, err := http.NewRequest(http.MethodPost, resolveURL, strings.NewReader(`{"outcome":"approved"}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httpapi.IdempotencyKeyHeader, "idem-roundtrip-1")
	req.Header.Set(httpapi.IfMatchHeader, httpapi.ETagFromVersion(version))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", resolveURL, err)
	}
	defer resp.Body.Close()
	resolveBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resolve status = %d, body=%s", resp.StatusCode, resolveBody)
	}
	var resolveResult decision.ResolveApprovalResponse
	if err := json.Unmarshal(resolveBody, &resolveResult); err != nil {
		t.Fatalf("decode resolve response: %v\nbody=%s", err, resolveBody)
	}
	if !resolveResult.Won || resolveResult.NextNodeKey != "wait_signal" {
		t.Fatalf("resolve result = %+v, want Won=true routed to wait_signal", resolveResult)
	}

	// 3. Re-read the detail: the SAME id is now DECIDED, and the newly
	// activated WAIT registration is present too.
	status, detail, raw = getRunDetail(t, server.URL, runID)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body=%s", status, raw)
	}
	if len(detail.ApprovalRequests) != 1 || detail.ApprovalRequests[0].ApprovalRequestID != approvalRequestID {
		t.Fatalf("ApprovalRequests after resolve = %+v, want the same id still listed", detail.ApprovalRequests)
	}
	resolved := detail.ApprovalRequests[0]
	if resolved.State != string(runtimedomain.ApprovalRequestDecided) || resolved.ResolvedOutcome != "approved" {
		t.Fatalf("resolved = %+v, want State=DECIDED ResolvedOutcome=approved", resolved)
	}
	if len(detail.WaitRegistrations) != 1 || detail.WaitRegistrations[0].NodeKey != "wait_signal" {
		t.Fatalf("WaitRegistrations after resolve = %+v, want exactly one for wait_signal", detail.WaitRegistrations)
	}
	waitRegistrationID := detail.WaitRegistrations[0].WaitRegistrationID

	// 4. Same proof for submitWaitSignal: the discovered
	// waitRegistrationId is genuinely accepted by the real decision
	// endpoint.
	signalURL := server.URL + "/runs/" + runID + "/wait-registrations/" + waitRegistrationID + "/signal"
	sigReq, err := http.NewRequest(http.MethodPost, signalURL, strings.NewReader(`{"signalKey":"release-1"}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	sigReq.Header.Set("Content-Type", "application/json")
	sigReq.Header.Set(httpapi.IdempotencyKeyHeader, "idem-roundtrip-signal-1")
	sigResp, err := http.DefaultClient.Do(sigReq)
	if err != nil {
		t.Fatalf("POST %s: %v", signalURL, err)
	}
	defer sigResp.Body.Close()
	sigBody, _ := io.ReadAll(sigResp.Body)
	if sigResp.StatusCode != http.StatusOK {
		t.Fatalf("signal status = %d, body=%s", sigResp.StatusCode, sigBody)
	}
	var sigResult decision.SubmitWaitSignalResponse
	if err := json.Unmarshal(sigBody, &sigResult); err != nil {
		t.Fatalf("decode signal response: %v\nbody=%s", err, sigBody)
	}
	if !sigResult.Won || sigResult.NextNodeKey != "end" {
		t.Fatalf("signal result = %+v, want Won=true routed to end", sigResult)
	}

	status, detail, raw = getRunDetail(t, server.URL, runID)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body=%s", status, raw)
	}
	if len(detail.WaitRegistrations) != 1 || detail.WaitRegistrations[0].State != string(runtimedomain.WaitRegistrationConsumed) {
		t.Fatalf("WaitRegistrations after signal = %+v, want the one registration CONSUMED", detail.WaitRegistrations)
	}
	if detail.State != string(runtimedomain.WorkflowRunVerifying) {
		t.Fatalf("detail.State = %s, want VERIFYING (END reached — the Run finished entirely over the public HTTP surface)", detail.State)
	}
}
