package decision_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/decision"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func resolveApprovalURL(serverURL, runID, approvalRequestID string) string {
	return serverURL + "/runs/" + runID + "/approval-requests/" + approvalRequestID + "/resolve"
}

func postResolveApproval(t *testing.T, serverURL, runID, approvalRequestID, idempotencyKey, ifMatch, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, resolveApprovalURL(serverURL, runID, approvalRequestID), strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set(httpapi.IdempotencyKeyHeader, idempotencyKey)
	}
	if ifMatch != "" {
		req.Header.Set(httpapi.IfMatchHeader, ifMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", req.URL, err)
	}
	return resp
}

// doResolveApprovalRaw is postResolveApproval's own goroutine-safe twin,
// mirroring run/start_test.go's own doStartRunRaw exactly (testing.T's
// Fatal/FailNow family must only ever be called from the goroutine running
// the test itself).
func doResolveApprovalRaw(serverURL, runID, approvalRequestID, idempotencyKey, ifMatch, body string) (status int, respBody []byte, err error) {
	req, err := http.NewRequest(http.MethodPost, resolveApprovalURL(serverURL, runID, approvalRequestID), strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set(httpapi.IdempotencyKeyHeader, idempotencyKey)
	}
	if ifMatch != "" {
		req.Header.Set(httpapi.IfMatchHeader, ifMatch)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, b, nil
}

func decodeResolveApprovalResponse(t *testing.T, resp *http.Response) decision.ResolveApprovalResponse {
	t.Helper()
	defer resp.Body.Close()
	var body decision.ResolveApprovalResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode ResolveApprovalResponse: %v", err)
	}
	return body
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

func mustGetApprovalRequest(t *testing.T, uow ports.UnitOfWork, id string) runtimedomain.ApprovalRequest {
	t.Helper()
	var request runtimedomain.ApprovalRequest
	err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		request, err = tx.Approvals().GetApprovalRequest(context.Background(), id)
		return err
	})
	if err != nil {
		t.Fatalf("GetApprovalRequest(%s): %v", id, err)
	}
	return request
}

// TestResolveApproval_HTTP_Approve_Success_RoutesAndReturnsWon is the
// handler's own happy path, driven through a real httptest.Server: a
// reviewer approves a real PENDING ApprovalRequest, gets 200/Won=true/
// MatchedRole=reviewer, and the underlying Run genuinely routes to
// end_approved.
func TestResolveApproval_HTTP_Approve_Success_RoutesAndReturnsWon(t *testing.T) {
	_, uow := openDecisionTestStore(t, "approve-success.db")
	ids := idsource.NewSequential("id")
	runID, approvalRequestID := approvalRequestFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 600)

	server := newTestServer(t, uow, ids, reviewerPrincipal())
	resp := postResolveApproval(t, server.URL, runID, approvalRequestID, "idem-approve-1", `"1"`,
		`{"outcome":"approved","reason":"looks good"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, mustBody(t, resp))
	}
	body := decodeResolveApprovalResponse(t, resp)
	if !body.Won || body.State != string(runtimedomain.ApprovalRequestDecided) || body.MatchedRole != "reviewer" {
		t.Fatalf("body = %+v, want Won=true State=DECIDED MatchedRole=reviewer", body)
	}
	if !body.Advanced || body.NextNodeKey != "end_approved" {
		t.Fatalf("body = %+v, want Advanced=true NextNodeKey=end_approved", body)
	}
	if body.ValidActions == nil || len(body.ValidActions) != 0 {
		t.Fatalf("ValidActions = %+v, want an empty (non-nil) slice", body.ValidActions)
	}

	request := mustGetApprovalRequest(t, uow, approvalRequestID)
	if request.State != runtimedomain.ApprovalRequestDecided || request.DecidedBy != "reviewer-1" || request.DecidedRole != "reviewer" || request.DecidedOutcome != "approved" {
		t.Fatalf("persisted request = %+v, want DECIDED by reviewer-1/reviewer/approved", request)
	}
}

// TestResolveApproval_HTTP_UnauthorizedRole_RejectsBeforeTouchingState is
// this task's own explicit "unauthorized role" Verify bullet: an actor
// whose bound roles do not intersect AuthorizedRoles=["reviewer"] is
// rejected 403 FORBIDDEN, and the ApprovalRequest is left byte-for-byte
// untouched (still PENDING at Version 1) — ResolveApproval's own
// "reject BEFORE this call ever touches the request's own state" contract.
func TestResolveApproval_HTTP_UnauthorizedRole_RejectsBeforeTouchingState(t *testing.T) {
	_, uow := openDecisionTestStore(t, "unauthorized-role.db")
	ids := idsource.NewSequential("id")
	runID, approvalRequestID := approvalRequestFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 600)

	server := newTestServer(t, uow, ids, operatorPrincipal())
	resp := postResolveApproval(t, server.URL, runID, approvalRequestID, "idem-unauthorized-1", `"1"`,
		`{"outcome":"approved","reason":"trying anyway"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body=%s)", resp.StatusCode, mustBody(t, resp))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeForbidden {
		t.Fatalf("error.code = %q, want FORBIDDEN", errBody.Error.Code)
	}

	request := mustGetApprovalRequest(t, uow, approvalRequestID)
	if request.State != runtimedomain.ApprovalRequestPending || request.Version != 1 || request.DecidedBy != "" {
		t.Fatalf("persisted request = %+v, want untouched PENDING@1 with no DecidedBy", request)
	}
}

// TestResolveApproval_HTTP_Spoof_UnknownActorFieldRejected400 is this
// task's own explicit "spoof" Verify bullet: an actor with no "reviewer"
// role who tries to smuggle an elevated role claim into the request body
// itself (rather than relying on the bound principal) never even reaches
// the authorization check — ResolveApprovalBody has no actor/roles field
// at all, so httpapi.DecodeJSON's own strict "unknown fields rejected"
// decoding fails the request closed with 400 before ActorRoles is even
// read, proving the body can never influence who this call authenticates
// as.
func TestResolveApproval_HTTP_Spoof_UnknownActorFieldRejected400(t *testing.T) {
	_, uow := openDecisionTestStore(t, "spoof.db")
	ids := idsource.NewSequential("id")
	runID, approvalRequestID := approvalRequestFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 600)

	server := newTestServer(t, uow, ids, operatorPrincipal())
	resp := postResolveApproval(t, server.URL, runID, approvalRequestID, "idem-spoof-1", `"1"`,
		`{"outcome":"approved","reason":"x","actorRoles":["reviewer"]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", resp.StatusCode, mustBody(t, resp))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeInvalidRequest {
		t.Fatalf("error.code = %q, want INVALID_REQUEST", errBody.Error.Code)
	}

	request := mustGetApprovalRequest(t, uow, approvalRequestID)
	if request.State != runtimedomain.ApprovalRequestPending || request.Version != 1 {
		t.Fatalf("persisted request = %+v, want untouched PENDING@1", request)
	}
}

// TestResolveApproval_HTTP_StaleCrossRunTarget_404 is this task's own
// explicit "stale/cross-project target" Verify bullet: an ApprovalRequestID
// that is real but the URL's own RunID names a DIFFERENT (also real) run —
// leakage-normalized identically to a genuinely nonexistent
// ApprovalRequestID, never a status that would let a caller distinguish
// "wrong run" from "does not exist at all".
func TestResolveApproval_HTTP_StaleCrossRunTarget_404(t *testing.T) {
	_, uow := openDecisionTestStore(t, "cross-run.db")
	ids := idsource.NewSequential("id")
	seedProject(t, uow, "project-1")
	_, approvalRequestID := approvalRequestFixtureInProject(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 600)
	otherRunID, _ := approvalRequestFixtureInProject(t, uow, ids, "project-1", "repo-2", "wf-def-2", "wf-v-2", 600)

	server := newTestServer(t, uow, ids, reviewerPrincipal())
	resp := postResolveApproval(t, server.URL, otherRunID, approvalRequestID, "idem-cross-run-1", `"1"`,
		`{"outcome":"approved"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", resp.StatusCode, mustBody(t, resp))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeNotFound {
		t.Fatalf("error.code = %q, want NOT_FOUND", errBody.Error.Code)
	}
}

// TestResolveApproval_HTTP_UnknownApprovalRequestID_404 covers the plain
// not-found half of the same leakage-normalization policy.
func TestResolveApproval_HTTP_UnknownApprovalRequestID_404(t *testing.T) {
	_, uow := openDecisionTestStore(t, "unknown-id.db")
	ids := idsource.NewSequential("id")
	runID, _ := approvalRequestFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 600)

	server := newTestServer(t, uow, ids, reviewerPrincipal())
	resp := postResolveApproval(t, server.URL, runID, "does-not-exist", "idem-unknown-1", `"1"`, `{"outcome":"approved"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", resp.StatusCode, mustBody(t, resp))
	}
}

// TestResolveApproval_HTTP_MissingIdempotencyKey_400 and
// TestResolveApproval_HTTP_MissingIfMatch_400 cover the two required
// headers this task's own CommandEnvelope/expected-version discipline
// demands.
func TestResolveApproval_HTTP_MissingIdempotencyKey_400(t *testing.T) {
	_, uow := openDecisionTestStore(t, "missing-idem.db")
	ids := idsource.NewSequential("id")
	runID, approvalRequestID := approvalRequestFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 600)

	server := newTestServer(t, uow, ids, reviewerPrincipal())
	resp := postResolveApproval(t, server.URL, runID, approvalRequestID, "", `"1"`, `{"outcome":"approved"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", resp.StatusCode, mustBody(t, resp))
	}
}

func TestResolveApproval_HTTP_MissingIfMatch_400(t *testing.T) {
	_, uow := openDecisionTestStore(t, "missing-ifmatch.db")
	ids := idsource.NewSequential("id")
	runID, approvalRequestID := approvalRequestFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 600)

	server := newTestServer(t, uow, ids, reviewerPrincipal())
	resp := postResolveApproval(t, server.URL, runID, approvalRequestID, "idem-no-ifmatch-1", "", `{"outcome":"approved"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", resp.StatusCode, mustBody(t, resp))
	}
}

// TestResolveApproval_HTTP_StaleIfMatch_PreconditionFailed proves the
// If-Match precondition this task's own "expected version" Thực hiện line
// calls for actually applies: a caller claiming a version the
// ApprovalRequest was never at is rejected before ResolveApproval is ever
// dispatched, and the request is left untouched.
func TestResolveApproval_HTTP_StaleIfMatch_PreconditionFailed(t *testing.T) {
	_, uow := openDecisionTestStore(t, "stale-ifmatch.db")
	ids := idsource.NewSequential("id")
	runID, approvalRequestID := approvalRequestFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 600)

	server := newTestServer(t, uow, ids, reviewerPrincipal())
	resp := postResolveApproval(t, server.URL, runID, approvalRequestID, "idem-stale-1", `"99"`, `{"outcome":"approved"}`)
	if resp.StatusCode != http.StatusPreconditionFailed && resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 412 or 409 (body=%s)", resp.StatusCode, mustBody(t, resp))
	}

	request := mustGetApprovalRequest(t, uow, approvalRequestID)
	if request.State != runtimedomain.ApprovalRequestPending || request.Version != 1 {
		t.Fatalf("persisted request = %+v, want untouched PENDING@1", request)
	}
}

// TestResolveApproval_HTTP_DuplicateIdempotencyKey_Replays proves a retry
// with the exact same Idempotency-Key/body replays the first call's own
// stored result rather than deciding a second time.
func TestResolveApproval_HTTP_DuplicateIdempotencyKey_Replays(t *testing.T) {
	_, uow := openDecisionTestStore(t, "duplicate-idem.db")
	ids := idsource.NewSequential("id")
	runID, approvalRequestID := approvalRequestFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 600)

	server := newTestServer(t, uow, ids, reviewerPrincipal())
	first := postResolveApproval(t, server.URL, runID, approvalRequestID, "idem-dup-1", `"1"`, `{"outcome":"approved","reason":"first"}`)
	firstBody := decodeResolveApprovalResponse(t, first)
	if !firstBody.Won {
		t.Fatalf("first body = %+v, want Won=true", firstBody)
	}

	second := postResolveApproval(t, server.URL, runID, approvalRequestID, "idem-dup-1", `"1"`, `{"outcome":"approved","reason":"first"}`)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 (body=%s)", second.StatusCode, mustBody(t, second))
	}
	secondBody := decodeResolveApprovalResponse(t, second)
	if secondBody.ResolveApprovalResult != firstBody.ResolveApprovalResult {
		t.Fatalf("replay body = %+v, want identical to first %+v", secondBody, firstBody)
	}
}

// TestResolveApproval_HTTP_ConcurrentDecisions_ExactlyOneWins is this
// task's own explicit "concurrent decision" Verify bullet, driven through
// real concurrent HTTP calls against a real sqlite.Store: several distinct
// Idempotency-Keys all racing to resolve the SAME PENDING ApprovalRequest.
// This intentionally does NOT assert a fixed HTTP status per racer (a
// racer's own If-Match precondition check can legitimately observe the
// state either before or after the true winner's commit, depending on
// scheduling — see this package's own decision.go doc comment) — instead
// it asserts the invariant that actually matters: every response is either
// a well-formed success or a well-formed conflict (never a 500/malformed
// body), and the ApprovalRequest itself ends up decided EXACTLY once.
func TestResolveApproval_HTTP_ConcurrentDecisions_ExactlyOneWins(t *testing.T) {
	_, uow := openDecisionTestStore(t, "concurrent.db")
	ids := idsource.NewSequential("id")
	runID, approvalRequestID := approvalRequestFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 600)

	server := newTestServer(t, uow, ids, reviewerPrincipal())

	const racers = 5
	var wg sync.WaitGroup
	var mu sync.Mutex
	statuses := make([]int, 0, racers)
	wonCount := 0

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			status, respBody, err := doResolveApprovalRaw(server.URL, runID, approvalRequestID,
				"idem-race-"+string(rune('a'+n)), `"1"`, `{"outcome":"approved","reason":"race"}`)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Errorf("racer %d: request error: %v", n, err)
				return
			}
			statuses = append(statuses, status)
			if status == http.StatusOK {
				var body decision.ResolveApprovalResponse
				if jsonErr := json.Unmarshal(respBody, &body); jsonErr != nil {
					t.Errorf("racer %d: decode response: %v (body=%s)", n, jsonErr, respBody)
					return
				}
				if body.Won {
					wonCount++
				}
			} else if status != http.StatusPreconditionFailed && status != http.StatusConflict {
				t.Errorf("racer %d: status = %d, want 200/409/412 (body=%s)", n, status, respBody)
			}
		}(i)
	}
	wg.Wait()

	if wonCount != 1 {
		t.Fatalf("wonCount = %d across statuses %v, want exactly 1", wonCount, statuses)
	}

	request := mustGetApprovalRequest(t, uow, approvalRequestID)
	if request.State != runtimedomain.ApprovalRequestDecided || request.DecidedOutcome != "approved" {
		t.Fatalf("persisted request = %+v, want exactly one DECIDED/approved", request)
	}
}

func mustBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	resp.Body.Close()
	return string(b)
}
