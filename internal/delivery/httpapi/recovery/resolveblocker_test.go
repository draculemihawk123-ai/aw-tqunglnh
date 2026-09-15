package recovery_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/recovery"
)

func postResolveBlocker(t *testing.T, serverURL, blockerID, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, serverURL+"/work-item-blockers/"+blockerID+"/resolve", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", req.URL, err)
	}
	return resp
}

func doResolveBlockerRaw(serverURL, blockerID, body string) (status int, respBody []byte, err error) {
	req, err := http.NewRequest(http.MethodPost, serverURL+"/work-item-blockers/"+blockerID+"/resolve", strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
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

func decodeResolveBlockerResponse(t *testing.T, resp *http.Response) recovery.ResolveWorkItemBlockerResponse {
	t.Helper()
	defer resp.Body.Close()
	var body recovery.ResolveWorkItemBlockerResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode ResolveWorkItemBlockerResponse: %v", err)
	}
	return body
}

// TestResolveWorkItemBlocker_HTTP_Resolved_UnblocksWorkItem proves the plain
// RESOLVED path against a real RUN_CANCELLED blocker (runCancelledBlockerFixture):
// 200 OK, State RESOLVED, WorkItemUnblocked true (this was the WorkItem's
// only OPEN blocker) and WorkItemStatus reporting the real post-unblock
// status.
func TestResolveWorkItemBlocker_HTTP_Resolved_UnblocksWorkItem(t *testing.T) {
	store, uow := openRecoveryTestStore(t, "resolve-blocker-resolved.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	_, blockerID := runCancelledBlockerFixture(t, store, uow, ids, "project-1", "repo-1")

	resp := postResolveBlocker(t, server.URL, blockerID, `{"mode":"RESOLVED","reason":"operator confirmed safe to close"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	got := decodeResolveBlockerResponse(t, resp)
	if got.BlockerID != blockerID {
		t.Fatalf("BlockerID = %q, want %q", got.BlockerID, blockerID)
	}
	if got.AlreadyResolved {
		t.Fatal("AlreadyResolved = true on the first call, want false")
	}
	if got.State != "RESOLVED" {
		t.Fatalf("State = %q, want RESOLVED", got.State)
	}
	if !got.WorkItemUnblocked {
		t.Fatal("WorkItemUnblocked = false, want true (this was the work item's only OPEN blocker)")
	}
}

// TestResolveWorkItemBlocker_HTTP_Waived_RecordsDecisionArtifact proves the
// WAIVED path (RUN_CANCELLED is Waivable per workdomain.BlockerType's own
// closed table) — requires PolicyGrantRef, and succeeds with State WAIVED.
func TestResolveWorkItemBlocker_HTTP_Waived_RecordsDecisionArtifact(t *testing.T) {
	store, uow := openRecoveryTestStore(t, "resolve-blocker-waived.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	_, blockerID := runCancelledBlockerFixture(t, store, uow, ids, "project-1", "repo-1")

	resp := postResolveBlocker(t, server.URL, blockerID,
		`{"mode":"WAIVED","reason":"operator accepted the cancellation","policyGrantRef":"policy-grant-1"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	got := decodeResolveBlockerResponse(t, resp)
	if got.State != "WAIVED" {
		t.Fatalf("State = %q, want WAIVED", got.State)
	}
}

// TestResolveWorkItemBlocker_HTTP_WaivedWithoutPolicyGrant_ReturnsBadRequest
// proves ErrWaiveRequiresPolicyGrant maps to 400.
func TestResolveWorkItemBlocker_HTTP_WaivedWithoutPolicyGrant_ReturnsBadRequest(t *testing.T) {
	store, uow := openRecoveryTestStore(t, "resolve-blocker-waived-no-grant.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	_, blockerID := runCancelledBlockerFixture(t, store, uow, ids, "project-1", "repo-1")

	resp := postResolveBlocker(t, server.URL, blockerID, `{"mode":"WAIVED","reason":"trying to skip the grant"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeInvalidRequest {
		t.Fatalf("error.code = %q, want INVALID_REQUEST", errBody.Error.Code)
	}
}

// TestResolveWorkItemBlocker_HTTP_MissingMode_ReturnsBadRequest proves
// ErrResolutionModeRequired maps to 400 — "Mode MUST chọn tường minh, không
// có mặc định" (resolve_work_item_blocker.go's own package doc comment).
func TestResolveWorkItemBlocker_HTTP_MissingMode_ReturnsBadRequest(t *testing.T) {
	store, uow := openRecoveryTestStore(t, "resolve-blocker-missing-mode.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	_, blockerID := runCancelledBlockerFixture(t, store, uow, ids, "project-1", "repo-1")

	resp := postResolveBlocker(t, server.URL, blockerID, `{"reason":"no mode supplied"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeInvalidRequest {
		t.Fatalf("error.code = %q, want INVALID_REQUEST", errBody.Error.Code)
	}
}

// TestResolveWorkItemBlocker_HTTP_MissingReason_ReturnsBadRequest proves
// this handler's own pre-dispatch validation of Reason (a raw, non-sentinel
// error inside runtime.ResolveWorkItemBlocker that this handler must never
// let leak through as an unclassified 500).
func TestResolveWorkItemBlocker_HTTP_MissingReason_ReturnsBadRequest(t *testing.T) {
	store, uow := openRecoveryTestStore(t, "resolve-blocker-missing-reason.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	_, blockerID := runCancelledBlockerFixture(t, store, uow, ids, "project-1", "repo-1")

	resp := postResolveBlocker(t, server.URL, blockerID, `{"mode":"RESOLVED"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeInvalidRequest {
		t.Fatalf("error.code = %q, want INVALID_REQUEST", errBody.Error.Code)
	}
}

// TestResolveWorkItemBlocker_HTTP_UnknownBlocker_ReturnsResourceHidden
// mirrors the leakage-normalization proof every other route in this
// codebase already establishes for its own primary target.
func TestResolveWorkItemBlocker_HTTP_UnknownBlocker_ReturnsResourceHidden(t *testing.T) {
	_, uow := openRecoveryTestStore(t, "resolve-blocker-unknown.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)

	resp := postResolveBlocker(t, server.URL, "does-not-exist", `{"mode":"RESOLVED","reason":"cleanup"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeNotFound {
		t.Fatalf("error.code = %q, want NOT_FOUND", errBody.Error.Code)
	}
}

// TestResolveWorkItemBlocker_HTTP_NonTerminalRun_ReturnsConflict proves
// ErrWorkItemHasNonTerminalRun maps to 409: a second, still-active Run on
// the SAME WorkItem (started AFTER the first was cancelled) blocks
// resolution of the RUN_CANCELLED blocker the first Run left behind.
func TestResolveWorkItemBlocker_HTTP_NonTerminalRun_ReturnsConflict(t *testing.T) {
	store, uow := openRecoveryTestStore(t, "resolve-blocker-nonterminal-run.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	workItemID, blockerID := runCancelledBlockerFixture(t, store, uow, ids, "project-1", "repo-1")

	// Start a second Run for the SAME WorkItem over a second published
	// workflow version — the WorkItem itself is still READY (CancelRun only
	// ever quiesces the Run, never the WorkItem's own Status by itself), so
	// StartWorkflowRun accepts it.
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-second", "wf-v-second")
	startSecondRunForWorkItem(t, uow, ids, "project-1", workItemID, string(version.ID()))

	resp := postResolveBlocker(t, server.URL, blockerID, `{"mode":"RESOLVED","reason":"try to close while another run is active"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeConflict {
		t.Fatalf("error.code = %q, want CONFLICT", errBody.Error.Code)
	}
}

// TestResolveWorkItemBlocker_HTTP_ResolveTwice_SecondIsAlreadyResolved
// proves resolved/waived replay no-op theo core (this task's own "Thực
// hiện" line): a second call against an already-RESOLVED blocker is a
// graceful 200 AlreadyResolved, never an error, and this handler adds no
// idempotency logic of its own on top — it trusts
// runtime.ResolveWorkItemBlockerResult.AlreadyResolved verbatim.
func TestResolveWorkItemBlocker_HTTP_ResolveTwice_SecondIsAlreadyResolved(t *testing.T) {
	store, uow := openRecoveryTestStore(t, "resolve-blocker-twice.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	_, blockerID := runCancelledBlockerFixture(t, store, uow, ids, "project-1", "repo-1")

	first := decodeResolveBlockerResponse(t, postResolveBlocker(t, server.URL, blockerID, `{"mode":"RESOLVED","reason":"first"}`))
	if first.AlreadyResolved {
		t.Fatalf("first resolve = %+v, want a fresh resolution", first)
	}

	secondResp := postResolveBlocker(t, server.URL, blockerID, `{"mode":"WAIVED","reason":"second, different mode entirely","policyGrantRef":"grant-1"}`)
	if secondResp.StatusCode != http.StatusOK {
		t.Fatalf("second status = %d, want 200 (graceful, not an error); body: %s", secondResp.StatusCode, mustReadAll(t, secondResp.Body))
	}
	second := decodeResolveBlockerResponse(t, secondResp)
	if !second.AlreadyResolved {
		t.Fatalf("second resolve = %+v, want AlreadyResolved", second)
	}
	if second.State != "RESOLVED" {
		t.Fatalf("second resolve State = %q, want RESOLVED (the FIRST call's own real outcome, never re-decided by the second, differently-shaped call)", second.State)
	}
}

// TestResolveWorkItemBlocker_HTTP_ConcurrentResolveRace_ExactlyOneFreshDecision
// proves resolution is race-safe through the real HTTP handler: several
// concurrent POSTs against the SAME blocker must converge on exactly one
// fresh (AlreadyResolved=false) decision.
func TestResolveWorkItemBlocker_HTTP_ConcurrentResolveRace_ExactlyOneFreshDecision(t *testing.T) {
	store, uow := openRecoveryTestStore(t, "resolve-blocker-race.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	_, blockerID := runCancelledBlockerFixture(t, store, uow, ids, "project-1", "repo-1")

	const attempts = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []recovery.ResolveWorkItemBlockerResponse
	var statuses []int
	var callErrors []error
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			status, body, err := doResolveBlockerRaw(server.URL, blockerID, `{"mode":"RESOLVED","reason":"racer"}`)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				callErrors = append(callErrors, err)
				return
			}
			var result recovery.ResolveWorkItemBlockerResponse
			if jsonErr := json.Unmarshal(body, &result); jsonErr != nil {
				callErrors = append(callErrors, jsonErr)
				return
			}
			statuses = append(statuses, status)
			results = append(results, result)
		}()
	}
	wg.Wait()

	for _, err := range callErrors {
		t.Fatalf("concurrent request failed: %v", err)
	}
	if len(results) != attempts {
		t.Fatalf("got %d responses, want %d", len(results), attempts)
	}
	fresh := 0
	for i, status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want 200", i, status)
		}
		if !results[i].AlreadyResolved {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("fresh (AlreadyResolved=false) responses = %d, want exactly 1 across %d concurrent racers", fresh, attempts)
	}
}
