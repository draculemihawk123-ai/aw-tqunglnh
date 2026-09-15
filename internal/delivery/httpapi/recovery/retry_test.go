package recovery_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/recovery"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func postRetryBlockedActivation(t *testing.T, serverURL, nodeRunID, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, serverURL+"/node-runs/"+nodeRunID+"/retry-blocked-activation", strings.NewReader(body))
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

func doRetryBlockedActivationRaw(serverURL, nodeRunID, body string) (status int, respBody []byte, err error) {
	req, err := http.NewRequest(http.MethodPost, serverURL+"/node-runs/"+nodeRunID+"/retry-blocked-activation", strings.NewReader(body))
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

func decodeRetryBlockedActivationResponse(t *testing.T, resp *http.Response) recovery.RetryBlockedActivationResponse {
	t.Helper()
	defer resp.Body.Close()
	var body recovery.RetryBlockedActivationResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode RetryBlockedActivationResponse: %v", err)
	}
	return body
}

// TestRetryBlockedActivation_HTTP_UnknownNodeRun_ReturnsResourceHidden
// mirrors every other route in this codebase's own leakage-normalization
// proof for its own primary target.
func TestRetryBlockedActivation_HTTP_UnknownNodeRun_ReturnsResourceHidden(t *testing.T) {
	_, uow := openRecoveryTestStore(t, "retry-unknown-node-run.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)

	resp := postRetryBlockedActivation(t, server.URL, "does-not-exist", `{"reason":"try anyway"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeNotFound {
		t.Fatalf("error.code = %q, want NOT_FOUND", errBody.Error.Code)
	}
}

// TestRetryBlockedActivation_HTTP_MissingReason_ReturnsBadRequest proves
// this handler's own pre-dispatch validation of Reason.
func TestRetryBlockedActivation_HTTP_MissingReason_ReturnsBadRequest(t *testing.T) {
	_, uow := openRecoveryTestStore(t, "retry-missing-reason.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)

	resp := postRetryBlockedActivation(t, server.URL, "does-not-exist", `{}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeInvalidRequest {
		t.Fatalf("error.code = %q, want INVALID_REQUEST", errBody.Error.Code)
	}
}

// TestRetryBlockedActivation_HTTP_NodeRunNotBlocked_ReturnsConflict proves
// ErrNodeRunNotBlocked maps to 409 for a real NodeRun (the START node's own
// activation, from a plain startedRunFixture) that is simply never BLOCKED
// at all.
func TestRetryBlockedActivation_HTTP_NodeRunNotBlocked_ReturnsConflict(t *testing.T) {
	_, uow := openRecoveryTestStore(t, "retry-node-run-not-blocked.db")
	ids := idsource.NewSequential("id")
	server := newTestServer(t, uow, ids)
	started := startedRunFixture(t, uow, ids, "project-1", "repo-1")

	resp := postRetryBlockedActivation(t, server.URL, started.NodeRunID, `{"reason":"not actually blocked"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeConflict {
		t.Fatalf("error.code = %q, want CONFLICT", errBody.Error.Code)
	}
}

// TestRetryBlockedActivation_HTTP_RevalidationStillFails_ReportsFailureReason
// is this task's own central "Không làm" proof: a blocked NodeRun whose own
// isolation condition never actually clears (toggleIsolationChecker with a
// huge failCalls, always failing) retries as a normal 202 Accepted result —
// never an HTTP error — with FailureReason populated and Retried/AlreadyRetried
// both false; the existing blocker is left OPEN, untouched (proved by a
// second retry call below observing the SAME outcome again, never
// AlreadyRetried).
func TestRetryBlockedActivation_HTTP_RevalidationStillFails_ReportsFailureReason(t *testing.T) {
	fixture := blockedAdmissionFixture(t, "retry-revalidation-fails.db", 1<<30)
	server := newTestServerWithDeps(t, fixture.UOW, fixture.IDs, fixture.Isolation, fixture.Registry)

	resp := postRetryBlockedActivation(t, server.URL, fixture.NodeRunID, `{"reason":"try again, still broken"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	got := decodeRetryBlockedActivationResponse(t, resp)
	if got.Retried {
		t.Fatal("Retried = true, want false (isolation is still unavailable)")
	}
	if got.AlreadyRetried {
		t.Fatal("AlreadyRetried = true, want false (this is a genuine revalidation, not a duplicate)")
	}
	if got.FailureReason != string(runtimedomain.TerminationReasonIsolationEnforcementUnavailable) {
		t.Fatalf("FailureReason = %q, want %q", got.FailureReason, runtimedomain.TerminationReasonIsolationEnforcementUnavailable)
	}
	if got.FailureDetail == "" {
		t.Fatal("FailureDetail is empty, want a real diagnostic detail")
	}

	// A second retry call observes the identical still-failing outcome — no
	// new blocked activation was ever created (this task's own explicit
	// Verify bullet).
	second := decodeRetryBlockedActivationResponse(t, postRetryBlockedActivation(t, server.URL, fixture.NodeRunID, `{"reason":"try again"}`))
	if second.Retried || second.AlreadyRetried {
		t.Fatalf("second retry = %+v, want another plain still-failing outcome", second)
	}
}

// TestRetryBlockedActivation_HTTP_Success_ReactivatesNodeRun proves the
// genuine-success path: the SAME toggleIsolationChecker that blocked this
// NodeRun's own original admission now passes (failCalls=1, already
// consumed by the fixture's own setup call) — retry creates exactly one new
// NodeRun activation and enqueues its own async scheduling job.
func TestRetryBlockedActivation_HTTP_Success_ReactivatesNodeRun(t *testing.T) {
	fixture := blockedAdmissionFixture(t, "retry-success.db", 1)
	server := newTestServerWithDeps(t, fixture.UOW, fixture.IDs, fixture.Isolation, fixture.Registry)

	resp := postRetryBlockedActivation(t, server.URL, fixture.NodeRunID, `{"reason":"isolation is available now"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	got := decodeRetryBlockedActivationResponse(t, resp)
	if !got.Retried {
		t.Fatalf("Retried = false, want true; full response: %+v", got)
	}
	if got.AlreadyRetried {
		t.Fatal("AlreadyRetried = true on a genuine fresh success, want false")
	}
	if got.ReactivatedNodeRunID == "" {
		t.Fatal("ReactivatedNodeRunID is empty, want a real minted id")
	}
	if got.ReactivatedNodeRunID == fixture.NodeRunID {
		t.Fatal("ReactivatedNodeRunID reused the ORIGINAL blocked NodeRunID — retry must always mint a fresh id, never revive the old one")
	}

	// A second retry against the SAME (still-BLOCKED, per its own historical
	// record) NodeRunID observes AlreadyRetried — the blocker itself is now
	// RESOLVED, so a redelivered/duplicate call is a safe no-op, never a
	// second reactivation.
	second := decodeRetryBlockedActivationResponse(t, postRetryBlockedActivation(t, server.URL, fixture.NodeRunID, `{"reason":"again"}`))
	if !second.AlreadyRetried {
		t.Fatalf("second retry = %+v, want AlreadyRetried", second)
	}
	if second.Retried {
		t.Fatal("second retry Retried = true, want false (no second reactivation)")
	}
}

// TestRetryBlockedActivation_HTTP_ConcurrentRetryRace_NoDuplicateActivation
// is this task's own explicit Verify bullet: several concurrent POSTs
// against the SAME blocked NodeRun must converge on exactly one fresh
// (Retried=true) reactivation, no matter how many racers actually call this
// endpoint at once — driven with real goroutines against real sqlite
// through the real HTTP handler, mirroring
// run.TestCancelRun_HTTP_ConcurrentCancelRace_OnlyOneFreshRequestOneCoordinatorJob's
// own approach.
func TestRetryBlockedActivation_HTTP_ConcurrentRetryRace_NoDuplicateActivation(t *testing.T) {
	fixture := blockedAdmissionFixture(t, "retry-race.db", 1)
	server := newTestServerWithDeps(t, fixture.UOW, fixture.IDs, fixture.Isolation, fixture.Registry)

	const attempts = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []recovery.RetryBlockedActivationResponse
	var statuses []int
	var callErrors []error
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			status, body, err := doRetryBlockedActivationRaw(server.URL, fixture.NodeRunID, `{"reason":"racer"}`)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				callErrors = append(callErrors, err)
				return
			}
			var result recovery.RetryBlockedActivationResponse
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
	reactivatedIDs := map[string]bool{}
	for i, status := range statuses {
		if status != http.StatusAccepted {
			t.Fatalf("attempt %d status = %d, want 202", i, status)
		}
		if results[i].Retried {
			fresh++
			reactivatedIDs[results[i].ReactivatedNodeRunID] = true
		} else if !results[i].AlreadyRetried {
			t.Fatalf("attempt %d = %+v, want either Retried or AlreadyRetried, never a still-failing outcome (isolation is available)", i, results[i])
		}
	}
	if fresh != 1 {
		t.Fatalf("fresh (Retried=true) responses = %d, want exactly 1 across %d concurrent racers", fresh, attempts)
	}
	if len(reactivatedIDs) != 1 {
		t.Fatalf("distinct ReactivatedNodeRunID values = %d, want exactly 1 (no duplicate activation)", len(reactivatedIDs))
	}
}

// TestRetryBlockedActivation_HTTP_RunNotRetryable_ReturnsConflict proves
// ErrRunNotRetryable maps to 409: the owning WorkflowRun is forced to
// CANCELLED directly (the same "poke the primitive a future command would
// otherwise reach" discipline run.TestCancelRun_HTTP_AlreadyTerminalRun_ReturnsConflict
// already uses — no real command in this codebase can independently
// terminate a Run out from under a still-open admission blocker) before the
// retry call, which must then refuse rather than reactivate into a Run
// that no longer has anywhere to route the new activation.
func TestRetryBlockedActivation_HTTP_RunNotRetryable_ReturnsConflict(t *testing.T) {
	fixture := blockedAdmissionFixture(t, "retry-run-not-retryable.db", 1)
	server := newTestServerWithDeps(t, fixture.UOW, fixture.IDs, fixture.Isolation, fixture.Registry)

	ctx := context.Background()
	err := fixture.UOW.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, fixture.RunID)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
			RunID: fixture.RunID, ExpectedState: run.State, ExpectedVersion: run.Version,
			NextState: runtimedomain.WorkflowRunCancelled,
		})
		return err
	})
	if err != nil {
		t.Fatalf("force run %s CANCELLED: %v", fixture.RunID, err)
	}

	resp := postRetryBlockedActivation(t, server.URL, fixture.NodeRunID, `{"reason":"run already gone"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeConflict {
		t.Fatalf("error.code = %q, want CONFLICT", errBody.Error.Code)
	}
}

// TestRetryBlockedActivation_HTTP_ProviderNotConfigured_ReturnsServiceUnavailable
// is this task's own composition-root gap proof: the SAME blocked NodeRun
// (isolation now passing) retried against a server whose own agent registry
// was never configured with a real executor for the pinned build's provider
// (agentregistry.Empty() — cmd/aw/serve.go's own documented safe default
// when neither --claude-executable nor --codex-executable is set) fails
// closed with a typed 503, never a generic 500 and never a silently-accepted
// request that could never actually succeed.
func TestRetryBlockedActivation_HTTP_ProviderNotConfigured_ReturnsServiceUnavailable(t *testing.T) {
	fixture := blockedAdmissionFixture(t, "retry-provider-not-configured.db", 1)
	// Deliberately NOT fixture.Isolation/fixture.Registry: this server's own
	// isolation checker always passes and its own agent registry is empty —
	// the retry's own admission re-check gets past isolation fine, then
	// fails resolving a live executor for the pinned build's ProviderKey.
	server := newTestServerWithDeps(t, fixture.UOW, fixture.IDs, fake.IsolationEnforcementChecker{}, agentregistry.Empty())

	resp := postRetryBlockedActivation(t, server.URL, fixture.NodeRunID, `{"reason":"provider not configured on this server"}`)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	errBody := decodeErrorResponse(t, resp)
	if errBody.Error.Code != httpapi.ErrorCodeUnavailable {
		t.Fatalf("error.code = %q, want UNAVAILABLE", errBody.Error.Code)
	}
}
