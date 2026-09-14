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

func postCancelWorkItem(t *testing.T, serverURL, workItemID, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, serverURL+"/work-items/"+workItemID+"/cancel", strings.NewReader(body))
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

func doCancelWorkItemRaw(serverURL, workItemID, body string) (status int, respBody []byte, err error) {
	req, err := http.NewRequest(http.MethodPost, serverURL+"/work-items/"+workItemID+"/cancel", strings.NewReader(body))
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

func decodeCancelWorkItemResponse(t *testing.T, resp *http.Response) recovery.CancelWorkItemResponse {
	t.Helper()
	defer resp.Body.Close()
	var body recovery.CancelWorkItemResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode CancelWorkItemResponse: %v", err)
	}
	return body
}

// TestCancelWorkItem_HTTP_ZeroRuns_ImmediatelyCancelled proves the common
// case: a WorkItem with no Run at all closes out synchronously within the
// SAME call — Status is CANCELLED already by the time this response is
// written (cancel_work_item.go's own "the common case... close out right
// now" branch), still 202 Accepted per this handler's own doc comment (a
// request accepted into CancelWorkItem's own quiesce protocol, never a
// status this handler invents from Status alone).
func TestCancelWorkItem_HTTP_ZeroRuns_ImmediatelyCancelled(t *testing.T) {
	_, uow := openRecoveryTestStore(t, "cancel-workitem-zero-runs.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")

	resp := postCancelWorkItem(t, server.URL, root.WorkItemID, `{"reason":"no longer needed"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	got := decodeCancelWorkItemResponse(t, resp)
	if got.WorkItemID != root.WorkItemID {
		t.Fatalf("WorkItemID = %q, want %q", got.WorkItemID, root.WorkItemID)
	}
	if got.AlreadyRequested {
		t.Fatal("AlreadyRequested = true on the first call, want false")
	}
	if got.Status != "CANCELLED" {
		t.Fatalf("Status = %q, want CANCELLED (zero runs — nothing left to quiesce)", got.Status)
	}
}

// TestCancelWorkItem_HTTP_ActiveRun_QuiescesRatherThanFakingTerminal proves
// this task's own "cancellation của active Run trả quiesce state, không giả
// CANCELLED ngay" line at the WorkItem level: a WorkItem with a genuinely
// non-terminal Run is NOT forced into CANCELLED by this call — the
// underlying Run is driven into CANCELLING (cancelRunTx, composed inside
// CancelWorkItem's own transaction) and the WorkItem itself stays at
// whatever Status it already had until that Run later finishes quiescing.
func TestCancelWorkItem_HTTP_ActiveRun_QuiescesRatherThanFakingTerminal(t *testing.T) {
	_, uow := openRecoveryTestStore(t, "cancel-workitem-active-run.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	started := startedRunFixture(t, uow, ids, "project-1", "repo-1")

	resp := postCancelWorkItem(t, server.URL, started.WorkItemID, `{"reason":"abandon this task"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	got := decodeCancelWorkItemResponse(t, resp)
	if got.AlreadyRequested {
		t.Fatal("AlreadyRequested = true on the first call, want false")
	}
	if got.Status == "CANCELLED" {
		t.Fatal("Status = CANCELLED, want the WorkItem to stay non-terminal while its Run is still quiescing")
	}
}

// TestCancelWorkItem_HTTP_Twice_SecondIsAlreadyRequested proves idempotency
// by WorkItemID — the WorkItem-level mirror of
// run.TestCancelRun_HTTP_CancelTwice_SecondIsAlreadyRequestedNoSecondJob.
func TestCancelWorkItem_HTTP_Twice_SecondIsAlreadyRequested(t *testing.T) {
	_, uow := openRecoveryTestStore(t, "cancel-workitem-twice.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")

	first := decodeCancelWorkItemResponse(t, postCancelWorkItem(t, server.URL, root.WorkItemID, `{"reason":"first"}`))
	if first.AlreadyRequested {
		t.Fatalf("first cancel = %+v, want a fresh request", first)
	}

	secondResp := postCancelWorkItem(t, server.URL, root.WorkItemID, `{"reason":"second, different reason"}`)
	if secondResp.StatusCode != http.StatusAccepted {
		t.Fatalf("second status = %d, want 202 (graceful, not an error); body: %s", secondResp.StatusCode, mustReadAll(t, secondResp.Body))
	}
	second := decodeCancelWorkItemResponse(t, secondResp)
	if !second.AlreadyRequested {
		t.Fatalf("second cancel = %+v, want AlreadyRequested", second)
	}
}

// TestCancelWorkItem_HTTP_UnknownWorkItem_ReturnsResourceHidden mirrors
// run.TestCancelRun_HTTP_UnknownRun_ReturnsResourceHidden's own leakage-
// normalization proof, at the WorkItem level.
func TestCancelWorkItem_HTTP_UnknownWorkItem_ReturnsResourceHidden(t *testing.T) {
	_, uow := openRecoveryTestStore(t, "cancel-workitem-unknown.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)

	resp := postCancelWorkItem(t, server.URL, "does-not-exist", `{"reason":"cleanup"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeNotFound {
		t.Fatalf("error.code = %q, want NOT_FOUND", errBody.Error.Code)
	}
}

// TestCancelWorkItem_HTTP_MissingReason_ReturnsBadRequest mirrors
// run.TestCancelRun_HTTP_MissingReason_ReturnsBadRequest.
func TestCancelWorkItem_HTTP_MissingReason_ReturnsBadRequest(t *testing.T) {
	_, uow := openRecoveryTestStore(t, "cancel-workitem-missing-reason.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")

	resp := postCancelWorkItem(t, server.URL, root.WorkItemID, `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeInvalidRequest {
		t.Fatalf("error.code = %q, want INVALID_REQUEST", errBody.Error.Code)
	}
}

// TestCancelWorkItem_HTTP_ConcurrentCancelRace_OnlyOneFreshRequest mirrors
// run.TestCancelRun_HTTP_ConcurrentCancelRace_OnlyOneFreshRequestOneCoordinatorJob
// at the WorkItem level: several concurrent POSTs against the SAME WorkItem
// must converge on exactly one fresh (AlreadyRequested=false) outcome, no
// matter how many racers actually call this endpoint at once — proving
// CancelWorkItem's own idempotent-by-WorkItemID intent record is race-safe
// through the real HTTP handler, not just at the application layer.
func TestCancelWorkItem_HTTP_ConcurrentCancelRace_OnlyOneFreshRequest(t *testing.T) {
	_, uow := openRecoveryTestStore(t, "cancel-workitem-race.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")

	const attempts = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []recovery.CancelWorkItemResponse
	var statuses []int
	var callErrors []error
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			status, body, err := doCancelWorkItemRaw(server.URL, root.WorkItemID, `{"reason":"racer"}`)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				callErrors = append(callErrors, err)
				return
			}
			var result recovery.CancelWorkItemResponse
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
		if status != http.StatusAccepted {
			t.Fatalf("attempt %d status = %d, want 202", i, status)
		}
		if !results[i].AlreadyRequested {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("fresh (AlreadyRequested=false) responses = %d, want exactly 1 across %d concurrent racers", fresh, attempts)
	}
}
