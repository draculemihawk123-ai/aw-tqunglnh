package run_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/run"
)

func postStartRun(t *testing.T, serverURL, workItemID, idempotencyKey, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, serverURL+"/work-items/"+workItemID+"/runs", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set(httpapi.IdempotencyKeyHeader, idempotencyKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", req.URL, err)
	}
	return resp
}

// doStartRunRaw is postStartRun's own goroutine-safe twin: it returns plain
// values instead of calling any t.Fatal*, because testing.T's Fatal/FailNow
// family MUST be called only from the goroutine running the test itself
// (testing.T's own documented contract) — TestStartWorkflowRun_HTTP_ConcurrentSameIdempotencyKey_OnlyOneRunCreated
// below is the one test in this file that calls this from spawned
// goroutines, collecting results under a mutex and asserting only after
// wg.Wait() back on the main goroutine.
func doStartRunRaw(serverURL, workItemID, idempotencyKey, body string) (status int, respBody []byte, err error) {
	req, err := http.NewRequest(http.MethodPost, serverURL+"/work-items/"+workItemID+"/runs", strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set(httpapi.IdempotencyKeyHeader, idempotencyKey)
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

func decodeStartRunResponse(t *testing.T, resp *http.Response) run.StartRunResponse {
	t.Helper()
	defer resp.Body.Close()
	var body run.StartRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode StartRunResponse: %v", err)
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

// TestStartWorkflowRun_HTTP_Success_ReturnsCreatedRunningWithCancelValidAction
// is the handler's own happy path, driven through a real httptest.Server:
// 201 Created, a RunID, State RUNNING, and this task's own "mutation valid
// actions" advisory naming cancelRun as the one next-valid action.
func TestStartWorkflowRun_HTTP_Success_ReturnsCreatedRunningWithCancelValidAction(t *testing.T) {
	_, uow := openRunTestStore(t, "start-success.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")
	server := newTestServer(t, uow, ids)

	resp := postStartRun(t, server.URL, root.WorkItemID, "idem-start-1", `{"workflowVersionId":"`+string(version.ID())+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	got := decodeStartRunResponse(t, resp)
	if got.RunID == "" || got.NodeRunID == "" || got.JobID == "" {
		t.Fatalf("response = %+v, want non-empty RunID/NodeRunID/JobID", got)
	}
	if got.State != "RUNNING" {
		t.Fatalf("State = %q, want RUNNING", got.State)
	}
	if got.WorkItemID != root.WorkItemID {
		t.Fatalf("WorkItemID = %q, want %q", got.WorkItemID, root.WorkItemID)
	}
	if len(got.ValidActions) != 1 || got.ValidActions[0].OperationID != "cancelRun" {
		t.Fatalf("ValidActions = %+v, want exactly one cancelRun advisory", got.ValidActions)
	}
}

// TestStartWorkflowRun_HTTP_MissingIdempotencyKey_ReturnsBadRequest proves
// V6-02's own "Idempotency-Key bắt buộc" is enforced at this route, not just
// documented.
func TestStartWorkflowRun_HTTP_MissingIdempotencyKey_ReturnsBadRequest(t *testing.T) {
	_, uow := openRunTestStore(t, "start-missing-key.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")
	server := newTestServer(t, uow, ids)

	resp := postStartRun(t, server.URL, root.WorkItemID, "", `{"workflowVersionId":"`+string(version.ID())+`"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeInvalidRequest {
		t.Fatalf("error.code = %q, want INVALID_REQUEST", errBody.Error.Code)
	}
}

// TestStartWorkflowRun_HTTP_MissingWorkflowVersionId_ReturnsBadRequest proves
// this handler validates its own body shape before ever dispatching.
func TestStartWorkflowRun_HTTP_MissingWorkflowVersionId_ReturnsBadRequest(t *testing.T) {
	_, uow := openRunTestStore(t, "start-missing-version.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	server := newTestServer(t, uow, ids)

	resp := postStartRun(t, server.URL, root.WorkItemID, "idem-start-1", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
}

// TestStartWorkflowRun_HTTP_UnknownWorkItem_ReturnsResourceHidden proves
// contract point 3's leakage-normalization policy: an unknown WorkItemID
// fails closed as a plain 404 before this handler ever touches the body or
// the Idempotency-Key header.
func TestStartWorkflowRun_HTTP_UnknownWorkItem_ReturnsResourceHidden(t *testing.T) {
	_, uow := openRunTestStore(t, "start-unknown-workitem.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)

	resp := postStartRun(t, server.URL, "does-not-exist", "idem-start-1", `{"workflowVersionId":"wf-v-1"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeNotFound {
		t.Fatalf("error.code = %q, want NOT_FOUND", errBody.Error.Code)
	}
}

// TestStartWorkflowRun_HTTP_Replay_SameKeySameBody_ReturnsIdenticalResult is
// this task's own "replay" Verify bullet: a genuine retry with the same
// Idempotency-Key and body returns the exact original RunID via
// httpapi.WriteReceiptReplay's own 200 path, and never creates a second
// workflow_runs row.
func TestStartWorkflowRun_HTTP_Replay_SameKeySameBody_ReturnsIdenticalResult(t *testing.T) {
	store, uow := openRunTestStore(t, "start-replay.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")
	server := newTestServer(t, uow, ids)
	requestBody := `{"workflowVersionId":"` + string(version.ID()) + `"}`

	first := decodeStartRunResponse(t, postStartRun(t, server.URL, root.WorkItemID, "idem-start-1", requestBody))

	replayResp := postStartRun(t, server.URL, root.WorkItemID, "idem-start-1", requestBody)
	if replayResp.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 (httpapi.WriteReceiptReplay's own contract); body: %s", replayResp.StatusCode, mustReadAll(t, replayResp.Body))
	}
	replay := decodeStartRunResponse(t, replayResp)
	if replay.RunID != first.RunID {
		t.Fatalf("replay RunID = %q, want exact original %q", replay.RunID, first.RunID)
	}

	count, err := store.CountWorkflowRuns(context.Background())
	if err != nil {
		t.Fatalf("CountWorkflowRuns: %v", err)
	}
	if count != 1 {
		t.Fatalf("workflow_runs row count = %d, want exactly 1 (replay must not create a second run)", count)
	}
}

// TestStartWorkflowRun_HTTP_SameKeyDifferentBody_ReturnsConflict is this
// task's own "different body conflict" half of the replay Verify bullet.
func TestStartWorkflowRun_HTTP_SameKeyDifferentBody_ReturnsConflict(t *testing.T) {
	_, uow := openRunTestStore(t, "start-hash-conflict.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	versionA := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-a", "wf-v-a")
	versionB := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-b", "wf-v-b")
	server := newTestServer(t, uow, ids)

	first := postStartRun(t, server.URL, root.WorkItemID, "idem-start-1", `{"workflowVersionId":"`+string(versionA.ID())+`"}`)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status = %d, want 201; body: %s", first.StatusCode, mustReadAll(t, first.Body))
	}
	first.Body.Close()

	second := postStartRun(t, server.URL, root.WorkItemID, "idem-start-1", `{"workflowVersionId":"`+string(versionB.ID())+`"}`)
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second status = %d, want 409; body: %s", second.StatusCode, mustReadAll(t, second.Body))
	}
	errBody := decodeErrorResponse(t, second)
	if errBody.Error.Code != httpapi.ErrorCodeConflict {
		t.Fatalf("error.code = %q, want CONFLICT", errBody.Error.Code)
	}
}

// TestStartWorkflowRun_HTTP_PinnedWorkflowVersionMismatch_ReturnsConflict is
// this task's own "pinned conflict" Verify bullet: a WorkItem already
// pinned to one WorkflowVersionID refuses a start request naming a
// different one — mirrors internal/app/runtime/commands_sqlite_test.go's
// own TestStartWorkflowRun_SQLite_WorkflowVersionMismatch_RejectsPinnedWorkItem,
// through this package's own HTTP handler instead of calling the command
// directly.
func TestStartWorkflowRun_HTTP_PinnedWorkflowVersionMismatch_ReturnsConflict(t *testing.T) {
	store, uow := openRunTestStore(t, "start-pinned-conflict.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	pinned := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-pinned", "wf-v-pinned")
	requested := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-requested", "wf-v-requested")
	if err := store.SetWorkItemWorkflowVersionForTest(context.Background(), root.WorkItemID, string(pinned.ID())); err != nil {
		t.Fatalf("pin work item to a workflow version: %v", err)
	}
	server := newTestServer(t, uow, ids)

	resp := postStartRun(t, server.URL, root.WorkItemID, "idem-start-1", `{"workflowVersionId":"`+string(requested.ID())+`"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeConflict {
		t.Fatalf("error.code = %q, want CONFLICT", errBody.Error.Code)
	}
}

// TestStartWorkflowRun_HTTP_SameIdempotencyKeyDifferentWorkItem_ConflictsRatherThanCrossReplays
// proves start.go's own startRunHashPayload design decision: folding the
// path-derived WorkItemID into the semantic hash. An Idempotency-Key is a
// client-owned promise ("this key names exactly one logical request"); a
// client that breaks that promise by reusing one key for two genuinely
// different WorkItems must get the SAME safe outcome this whole
// architecture already gives "same key, different body" everywhere else
// (TestStartWorkflowRun_HTTP_SameKeyDifferentBody_ReturnsConflict, right
// above) — a 409 Conflict, NEVER a silent, wrong replay of WorkItem A's own
// RunID for a request that actually named WorkItem B. Without WorkItemID in
// the hash, this second call's payload ({workflowVersionId} alone) would
// hash IDENTICALLY to the first call's, and httpapi.ReconcileReceipt would
// wrongly treat it as a genuine replay, returning WorkItem A's own stored
// RunID as if it were WorkItem B's new run — the exact bug this test
// exists to catch.
func TestStartWorkflowRun_HTTP_SameIdempotencyKeyDifferentWorkItem_ConflictsRatherThanCrossReplays(t *testing.T) {
	store, uow := openRunTestStore(t, "start-cross-workitem.db")
	ids := idsource.NewSequential("id")
	seedProject(t, uow, "project-1")
	rootA := readyWorkItemFixtureInExistingProject(t, uow, ids, "project-1", "repo-a")
	rootB := readyWorkItemFixtureInExistingProject(t, uow, ids, "project-1", "repo-b")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")
	server := newTestServer(t, uow, ids)
	requestBody := `{"workflowVersionId":"` + string(version.ID()) + `"}`

	const reusedKey = "idem-reused-across-workitems"
	respA := postStartRun(t, server.URL, rootA.WorkItemID, reusedKey, requestBody)
	if respA.StatusCode != http.StatusCreated {
		t.Fatalf("WorkItem A status = %d, want 201; body: %s", respA.StatusCode, mustReadAll(t, respA.Body))
	}
	resultA := decodeStartRunResponse(t, respA)

	// The same key, reused for a DIFFERENT WorkItem: must conflict, never
	// silently return WorkItem A's own RunID mislabeled as WorkItem B's.
	respB := postStartRun(t, server.URL, rootB.WorkItemID, reusedKey, requestBody)
	if respB.StatusCode != http.StatusConflict {
		t.Fatalf("WorkItem B status = %d, want 409 (reusing A's own Idempotency-Key for a different WorkItem must conflict, never cross-replay A's RunID); body: %s",
			respB.StatusCode, mustReadAll(t, respB.Body))
	}
	errBody := decodeErrorResponse(t, respB)
	if errBody.Error.Code != httpapi.ErrorCodeConflict {
		t.Fatalf("error.code = %q, want CONFLICT", errBody.Error.Code)
	}

	count, err := store.CountWorkflowRuns(context.Background())
	if err != nil {
		t.Fatalf("CountWorkflowRuns: %v", err)
	}
	if count != 1 {
		t.Fatalf("workflow_runs row count = %d, want exactly 1 (WorkItem A's own run only — the rejected WorkItem B call must create nothing)", count)
	}

	// A properly DISTINCT key for WorkItem B works normally and creates its
	// own independent run — proving the 409 above was really about the key
	// collision, not some other defect blocking WorkItem B outright.
	respB2 := postStartRun(t, server.URL, rootB.WorkItemID, "idem-start-b-distinct-key", requestBody)
	if respB2.StatusCode != http.StatusCreated {
		t.Fatalf("WorkItem B with its own distinct key: status = %d, want 201; body: %s", respB2.StatusCode, mustReadAll(t, respB2.Body))
	}
	resultB := decodeStartRunResponse(t, respB2)
	if resultB.RunID == "" || resultB.RunID == resultA.RunID {
		t.Fatalf("WorkItem B RunID = %q, want a real, distinct RunID from WorkItem A's %q", resultB.RunID, resultA.RunID)
	}
	if resultB.WorkItemID != rootB.WorkItemID {
		t.Fatalf("resultB.WorkItemID = %q, want %q", resultB.WorkItemID, rootB.WorkItemID)
	}

	count, err = store.CountWorkflowRuns(context.Background())
	if err != nil {
		t.Fatalf("CountWorkflowRuns (after distinct key): %v", err)
	}
	if count != 2 {
		t.Fatalf("workflow_runs row count = %d, want exactly 2 (one genuine run per WorkItem, once each used its own key)", count)
	}
}

// TestStartWorkflowRun_HTTP_ConcurrentSameIdempotencyKey_OnlyOneRunCreated is
// a real concurrent race using real goroutines against real sqlite (this
// task's own review instruction), proving THIS package's own
// httpapi.LookupReceipt fast-path-then-dispatch code never lets two
// concurrent identical requests create two Runs: even when both racers
// observe "no receipt yet" from the fast path and both proceed to dispatch
// runtime.StartWorkflowRun, SQLite's own writer serialization means exactly
// one of the two StartWorkflowRun calls actually creates the Run — the
// other's own transaction re-checks the receipt authoritatively and
// replays the just-committed result. Both HTTP calls therefore succeed
// (2xx) and report the identical RunID; only the exact status code of the
// race LOSER is a documented, harmless cosmetic difference (it takes this
// handler's own 201 branch, since runtime.StartWorkflowRun returns a
// normal, non-error result either way — see start.go's own NOTE).
func TestStartWorkflowRun_HTTP_ConcurrentSameIdempotencyKey_OnlyOneRunCreated(t *testing.T) {
	store, uow := openRunTestStore(t, "start-concurrent-same-key.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")
	server := newTestServer(t, uow, ids)
	requestBody := `{"workflowVersionId":"` + string(version.ID()) + `"}`

	const attempts = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	var runIDs []string
	var statuses []int
	var callErrors []error
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			status, body, err := doStartRunRaw(server.URL, root.WorkItemID, "idem-concurrent-1", requestBody)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				callErrors = append(callErrors, err)
				return
			}
			var result run.StartRunResponse
			if jsonErr := json.Unmarshal(body, &result); jsonErr != nil {
				callErrors = append(callErrors, jsonErr)
				return
			}
			runIDs = append(runIDs, result.RunID)
			statuses = append(statuses, status)
		}()
	}
	wg.Wait()

	for _, err := range callErrors {
		t.Fatalf("concurrent request failed: %v", err)
	}
	if len(statuses) != attempts {
		t.Fatalf("got %d responses, want %d", len(statuses), attempts)
	}
	for i, status := range statuses {
		if status != http.StatusCreated && status != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want 200 or 201", i, status)
		}
	}
	first := runIDs[0]
	for i, id := range runIDs {
		if id == "" || id != first {
			t.Fatalf("runIDs = %v, want every concurrent call to observe the identical RunID %q (index %d differs)", runIDs, first, i)
		}
	}
	count, err := store.CountWorkflowRuns(context.Background())
	if err != nil {
		t.Fatalf("CountWorkflowRuns: %v", err)
	}
	if count != 1 {
		t.Fatalf("workflow_runs row count = %d, want exactly 1 across %d concurrent identical requests", count, attempts)
	}
}

func mustReadAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
