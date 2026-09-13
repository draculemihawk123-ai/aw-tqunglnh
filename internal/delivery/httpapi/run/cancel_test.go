package run_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/run"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func postCancelRun(t *testing.T, serverURL, runID, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, serverURL+"/runs/"+runID+"/cancel", strings.NewReader(body))
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

// doCancelRunRaw is postCancelRun's own goroutine-safe twin — see
// doStartRunRaw's own doc comment (start_test.go) for why this must not
// call any testing.T Fatal*/Error* method itself.
func doCancelRunRaw(serverURL, runID, body string) (status int, respBody []byte, err error) {
	req, err := http.NewRequest(http.MethodPost, serverURL+"/runs/"+runID+"/cancel", strings.NewReader(body))
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

func decodeCancelRunResponse(t *testing.T, resp *http.Response) run.CancelRunResponse {
	t.Helper()
	defer resp.Body.Close()
	var body run.CancelRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode CancelRunResponse: %v", err)
	}
	return body
}

// startedRunFixture starts one real Run (through this package's own HTTP
// handler, not the raw application command) over a fresh readyWorkItemFixture
// and returns its StartRunResponse — the common precondition every cancel
// test in this file needs.
func startedRunFixture(t *testing.T, server *httptest.Server, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) run.StartRunResponse {
	t.Helper()
	root := readyWorkItemFixture(t, uow, ids, projectID, repositoryID)
	version := publishTestWorkflowVersion(t, uow, projectID, "wf-def-"+repositoryID, "wf-v-"+repositoryID)
	resp := postStartRun(t, server.URL, root.WorkItemID, "idem-start-"+repositoryID, `{"workflowVersionId":"`+string(version.ID())+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("start run status = %d, want 201; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	return decodeStartRunResponse(t, resp)
}

// TestCancelRun_HTTP_Success_ReturnsAcceptedCancelling is this task's own
// most direct proof of its own "Thực hiện" line: cancel trả CANCELLING,
// không giả CANCELLED — a fresh cancel call is 202 Accepted with State
// CANCELLING, never a synchronous CANCELLED.
func TestCancelRun_HTTP_Success_ReturnsAcceptedCancelling(t *testing.T) {
	_, uow := openRunTestStore(t, "cancel-success.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	started := startedRunFixture(t, server, uow, ids, "project-1", "repo-1")

	resp := postCancelRun(t, server.URL, started.RunID, `{"reason":"operator requested cancel"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	got := decodeCancelRunResponse(t, resp)
	if got.RunID != started.RunID {
		t.Fatalf("RunID = %q, want %q", got.RunID, started.RunID)
	}
	if got.State != "CANCELLING" {
		t.Fatalf("State = %q, want CANCELLING (never a synchronous CANCELLED)", got.State)
	}
	if got.AlreadyRequested {
		t.Fatal("AlreadyRequested = true on the first call, want false")
	}
	if got.CoordinatorJobID == "" {
		t.Fatal("CoordinatorJobID is empty on a fresh cancel, want a real enqueued job id")
	}
	if len(got.ValidActions) != 0 {
		t.Fatalf("ValidActions = %+v, want empty once CANCELLING", got.ValidActions)
	}
}

// TestCancelRun_HTTP_CancelTwice_SecondIsAlreadyRequestedNoSecondJob is this
// task's own "cancel twice" Verify bullet: a second call against the same
// Run is a graceful 202 AlreadyRequested, never an error, and never enqueues
// a second CANCEL_RUN_COORDINATOR job.
func TestCancelRun_HTTP_CancelTwice_SecondIsAlreadyRequestedNoSecondJob(t *testing.T) {
	store, uow := openRunTestStore(t, "cancel-twice.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	started := startedRunFixture(t, server, uow, ids, "project-1", "repo-1")

	first := decodeCancelRunResponse(t, postCancelRun(t, server.URL, started.RunID, `{"reason":"first"}`))
	if first.AlreadyRequested {
		t.Fatalf("first cancel = %+v, want a fresh request", first)
	}

	secondResp := postCancelRun(t, server.URL, started.RunID, `{"reason":"second, different reason"}`)
	if secondResp.StatusCode != http.StatusAccepted {
		t.Fatalf("second status = %d, want 202 (graceful, not an error); body: %s", secondResp.StatusCode, mustReadAll(t, secondResp.Body))
	}
	second := decodeCancelRunResponse(t, secondResp)
	if !second.AlreadyRequested {
		t.Fatalf("second cancel = %+v, want AlreadyRequested", second)
	}

	count, err := store.CountDurableJobsByIdempotencyKey(context.Background(), "cancel-run-coordinator:"+started.RunID)
	if err != nil {
		t.Fatalf("CountDurableJobsByIdempotencyKey: %v", err)
	}
	if count != 1 {
		t.Fatalf("CANCEL_RUN_COORDINATOR job count = %d, want exactly 1 (duplicate cancel must never enqueue a second)", count)
	}
}

// TestCancelRun_HTTP_UnknownRun_ReturnsResourceHidden mirrors
// TestStartWorkflowRun_HTTP_UnknownWorkItem_ReturnsResourceHidden's own
// leakage-normalization proof for the cancel route.
func TestCancelRun_HTTP_UnknownRun_ReturnsResourceHidden(t *testing.T) {
	_, uow := openRunTestStore(t, "cancel-unknown-run.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)

	resp := postCancelRun(t, server.URL, "does-not-exist", `{"reason":"cleanup"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeNotFound {
		t.Fatalf("error.code = %q, want NOT_FOUND", errBody.Error.Code)
	}
}

// TestCancelRun_HTTP_MissingReason_ReturnsBadRequest proves this handler
// validates its own body shape before ever dispatching — runtime.CancelRun
// itself also refuses an empty Reason, but this handler must never let that
// raw, untyped "runtime: Reason is required" error reach a client as an
// unclassified 500.
func TestCancelRun_HTTP_MissingReason_ReturnsBadRequest(t *testing.T) {
	_, uow := openRunTestStore(t, "cancel-missing-reason.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	started := startedRunFixture(t, server, uow, ids, "project-1", "repo-1")

	resp := postCancelRun(t, server.URL, started.RunID, `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeInvalidRequest {
		t.Fatalf("error.code = %q, want INVALID_REQUEST", errBody.Error.Code)
	}
}

// TestCancelRun_HTTP_AlreadyTerminalRun_ReturnsConflict proves ADR-020's own
// "chỉ PASS và FAIL làm Run terminal" at this route: a genuinely finished
// Run refuses cancellation outright (409), never a silent AlreadyRequested
// no-op. The Run is forced to SUCCEEDED directly via TransitionWorkflowRunState
// (same "poke the primitive directly" discipline internal/app/runtime's own
// cancel_run_test.go already uses — no real completion-policy command exists
// in this codebase yet to reach SUCCEEDED for real).
func TestCancelRun_HTTP_AlreadyTerminalRun_ReturnsConflict(t *testing.T) {
	_, uow := openRunTestStore(t, "cancel-already-terminal.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	started := startedRunFixture(t, server, uow, ids, "project-1", "repo-1")

	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, started.RunID)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
			RunID: started.RunID, ExpectedState: run.State, ExpectedVersion: run.Version,
			NextState: runtimedomain.WorkflowRunSucceeded,
		})
		return err
	})
	if err != nil {
		t.Fatalf("force run %s SUCCEEDED: %v", started.RunID, err)
	}

	resp := postCancelRun(t, server.URL, started.RunID, `{"reason":"too late"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeConflict {
		t.Fatalf("error.code = %q, want CONFLICT", errBody.Error.Code)
	}
}

// TestCancelRun_HTTP_ConcurrentCancelRace_OnlyOneFreshRequestOneCoordinatorJob
// is this task's own "cancel race" Verify bullet, driven with real
// goroutines against real sqlite through the real HTTP handler (this task's
// own explicit review instruction, mirroring internal/app/runtime/cancel_run_race_test.go's
// own approach at the application layer): several concurrent POSTs against
// the SAME Run must converge on exactly one fresh (AlreadyRequested=false)
// outcome and exactly one CANCEL_RUN_COORDINATOR job, no matter how many
// racers actually call this endpoint at once.
func TestCancelRun_HTTP_ConcurrentCancelRace_OnlyOneFreshRequestOneCoordinatorJob(t *testing.T) {
	store, uow := openRunTestStore(t, "cancel-race.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	started := startedRunFixture(t, server, uow, ids, "project-1", "repo-1")

	const attempts = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []run.CancelRunResponse
	var statuses []int
	var callErrors []error
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		reason := "racer"
		go func() {
			defer wg.Done()
			status, body, err := doCancelRunRaw(server.URL, started.RunID, `{"reason":"`+reason+`"}`)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				callErrors = append(callErrors, err)
				return
			}
			var result run.CancelRunResponse
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
		if results[i].RunID != started.RunID {
			t.Fatalf("attempt %d RunID = %q, want %q", i, results[i].RunID, started.RunID)
		}
	}
	if fresh != 1 {
		t.Fatalf("fresh (AlreadyRequested=false) responses = %d, want exactly 1 across %d concurrent racers", fresh, attempts)
	}

	count, err := store.CountDurableJobsByIdempotencyKey(context.Background(), "cancel-run-coordinator:"+started.RunID)
	if err != nil {
		t.Fatalf("CountDurableJobsByIdempotencyKey: %v", err)
	}
	if count != 1 {
		t.Fatalf("CANCEL_RUN_COORDINATOR job count = %d, want exactly 1 across %d concurrent racers", count, attempts)
	}
}
