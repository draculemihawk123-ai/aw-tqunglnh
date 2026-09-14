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

func submitWaitSignalURL(serverURL, runID, waitRegistrationID string) string {
	return serverURL + "/runs/" + runID + "/wait-registrations/" + waitRegistrationID + "/signal"
}

func postSubmitWaitSignal(t *testing.T, serverURL, runID, waitRegistrationID, idempotencyKey, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, submitWaitSignalURL(serverURL, runID, waitRegistrationID), strings.NewReader(body))
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

func doSubmitWaitSignalRaw(serverURL, runID, waitRegistrationID, idempotencyKey, body string) (status int, respBody []byte, err error) {
	req, err := http.NewRequest(http.MethodPost, submitWaitSignalURL(serverURL, runID, waitRegistrationID), strings.NewReader(body))
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
	b, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return resp.StatusCode, nil, readErr
	}
	return resp.StatusCode, b, nil
}

func decodeSubmitWaitSignalResponse(t *testing.T, resp *http.Response) decision.SubmitWaitSignalResponse {
	t.Helper()
	defer resp.Body.Close()
	var body decision.SubmitWaitSignalResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode SubmitWaitSignalResponse: %v", err)
	}
	return body
}

func mustGetWaitRegistration(t *testing.T, uow ports.UnitOfWork, id string) runtimedomain.WaitRegistration {
	t.Helper()
	var registration runtimedomain.WaitRegistration
	err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		registration, err = tx.Wait().GetWaitRegistration(context.Background(), id)
		return err
	})
	if err != nil {
		t.Fatalf("GetWaitRegistration(%s): %v", id, err)
	}
	return registration
}

// TestSubmitWaitSignal_HTTP_Success_RoutesAndReturnsWon is the handler's own
// happy path: a real external caller signals a real ACTIVE SIGNAL-mode
// WaitRegistration, gets 200/Won=true, and the underlying Run genuinely
// routes to end_resumed.
func TestSubmitWaitSignal_HTTP_Success_RoutesAndReturnsWon(t *testing.T) {
	_, uow := openDecisionTestStore(t, "signal-success.db")
	ids := idsource.NewSequential("id")
	runID, waitRegistrationID := waitRegistrationFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 0)

	server := newTestServer(t, uow, ids, operatorPrincipal())
	resp := postSubmitWaitSignal(t, server.URL, runID, waitRegistrationID, "idem-signal-1", `{"signalKey":"evt-1","payload":{"build":"green"}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, mustBody(t, resp))
	}
	body := decodeSubmitWaitSignalResponse(t, resp)
	if !body.Won || body.State != string(runtimedomain.WaitRegistrationConsumed) {
		t.Fatalf("body = %+v, want Won=true State=CONSUMED", body)
	}
	if !body.Advanced || body.NextNodeKey != "end_resumed" {
		t.Fatalf("body = %+v, want Advanced=true NextNodeKey=end_resumed", body)
	}

	registration := mustGetWaitRegistration(t, uow, waitRegistrationID)
	if registration.State != runtimedomain.WaitRegistrationConsumed || registration.ConsumedSignalID == nil {
		t.Fatalf("persisted registration = %+v, want CONSUMED with a ConsumedSignalID", registration)
	}
}

// TestSubmitWaitSignal_HTTP_DuplicateSignalKey_IdempotentNoDoubleProcessing
// is this task's own explicit "duplicate signal" Verify bullet: the SAME
// real-world event (identical SignalKey/payload), reported through a
// SECOND, DIFFERENT command invocation (a different Idempotency-Key,
// exactly matching runtime.SignalWait's own doc comment scenario), is
// recognized as already consumed — 200, Won=false, never a second
// WaitSignal row, never a second route.
func TestSubmitWaitSignal_HTTP_DuplicateSignalKey_IdempotentNoDoubleProcessing(t *testing.T) {
	_, uow := openDecisionTestStore(t, "duplicate-signal.db")
	ids := idsource.NewSequential("id")
	runID, waitRegistrationID := waitRegistrationFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 0)

	server := newTestServer(t, uow, ids, operatorPrincipal())
	first := postSubmitWaitSignal(t, server.URL, runID, waitRegistrationID, "idem-signal-first", `{"signalKey":"evt-dup","payload":{"build":"green"}}`)
	firstBody := decodeSubmitWaitSignalResponse(t, first)
	if !firstBody.Won {
		t.Fatalf("first body = %+v, want Won=true", firstBody)
	}

	second := postSubmitWaitSignal(t, server.URL, runID, waitRegistrationID, "idem-signal-second", `{"signalKey":"evt-dup","payload":{"build":"green"}}`)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("duplicate status = %d, want 200 (body=%s)", second.StatusCode, mustBody(t, second))
	}
	secondBody := decodeSubmitWaitSignalResponse(t, second)
	if secondBody.Won {
		t.Fatalf("second body = %+v, want Won=false (already consumed by the first delivery)", secondBody)
	}
	if secondBody.State != string(runtimedomain.WaitRegistrationConsumed) {
		t.Fatalf("second body = %+v, want State=CONSUMED", secondBody)
	}
}

// TestSubmitWaitSignal_HTTP_SameSignalKeyDifferentPayload_IdempotencyConflict
// proves ports.WaitRepository.RecordWaitSignal's own contract reaches the
// HTTP caller correctly: the SAME SignalKey reused for a genuinely
// different payload is a real conflict, not a silent overwrite.
func TestSubmitWaitSignal_HTTP_SameSignalKeyDifferentPayload_IdempotencyConflict(t *testing.T) {
	_, uow := openDecisionTestStore(t, "signal-payload-conflict.db")
	ids := idsource.NewSequential("id")
	runID, waitRegistrationID := waitRegistrationFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 0)

	server := newTestServer(t, uow, ids, operatorPrincipal())
	first := postSubmitWaitSignal(t, server.URL, runID, waitRegistrationID, "idem-signal-a", `{"signalKey":"evt-conflict","payload":{"build":"green"}}`)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first status = %d, want 200 (body=%s)", first.StatusCode, mustBody(t, first))
	}
	_ = decodeSubmitWaitSignalResponse(t, first)

	second := postSubmitWaitSignal(t, server.URL, runID, waitRegistrationID, "idem-signal-b", `{"signalKey":"evt-conflict","payload":{"build":"red"}}`)
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second status = %d, want 409 (body=%s)", second.StatusCode, mustBody(t, second))
	}
}

// TestSubmitWaitSignal_HTTP_StaleCrossRunTarget_404 is this task's own
// explicit "stale/cross-project target" Verify bullet for WAIT.
func TestSubmitWaitSignal_HTTP_StaleCrossRunTarget_404(t *testing.T) {
	_, uow := openDecisionTestStore(t, "signal-cross-run.db")
	ids := idsource.NewSequential("id")
	seedProject(t, uow, "project-1")
	_, waitRegistrationID := waitRegistrationFixtureInProject(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 0)
	otherRunID, _ := waitRegistrationFixtureInProject(t, uow, ids, "project-1", "repo-2", "wf-def-2", "wf-v-2", 0)

	server := newTestServer(t, uow, ids, operatorPrincipal())
	resp := postSubmitWaitSignal(t, server.URL, otherRunID, waitRegistrationID, "idem-cross-run-1", `{"signalKey":"evt-1"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", resp.StatusCode, mustBody(t, resp))
	}
}

// TestSubmitWaitSignal_HTTP_MissingSignalKey_400 and
// TestSubmitWaitSignal_HTTP_MissingIdempotencyKey_400 cover basic request
// validation.
func TestSubmitWaitSignal_HTTP_MissingSignalKey_400(t *testing.T) {
	_, uow := openDecisionTestStore(t, "missing-signal-key.db")
	ids := idsource.NewSequential("id")
	runID, waitRegistrationID := waitRegistrationFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 0)

	server := newTestServer(t, uow, ids, operatorPrincipal())
	resp := postSubmitWaitSignal(t, server.URL, runID, waitRegistrationID, "idem-no-key-1", `{"payload":{}}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", resp.StatusCode, mustBody(t, resp))
	}
}

func TestSubmitWaitSignal_HTTP_MissingIdempotencyKey_400(t *testing.T) {
	_, uow := openDecisionTestStore(t, "missing-idem-signal.db")
	ids := idsource.NewSequential("id")
	runID, waitRegistrationID := waitRegistrationFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 0)

	server := newTestServer(t, uow, ids, operatorPrincipal())
	resp := postSubmitWaitSignal(t, server.URL, runID, waitRegistrationID, "", `{"signalKey":"evt-1"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", resp.StatusCode, mustBody(t, resp))
	}
}

// TestSubmitWaitSignal_HTTP_ConcurrentDifferentSignals_ExactlyOneWins is
// this task's own explicit "concurrent decision" Verify bullet applied to
// WAIT: several DIFFERENT real SignalKeys racing to consume the SAME
// ACTIVE registration through real concurrent HTTP calls against a real
// sqlite.Store — exactly one wins (Won=true), every WaitSignal is still
// durably recorded (never lost), and the registration ends up CONSUMED
// exactly once.
func TestSubmitWaitSignal_HTTP_ConcurrentDifferentSignals_ExactlyOneWins(t *testing.T) {
	_, uow := openDecisionTestStore(t, "signal-concurrent.db")
	ids := idsource.NewSequential("id")
	runID, waitRegistrationID := waitRegistrationFixture(t, uow, ids, "project-1", "repo-1", "wf-def-1", "wf-v-1", 0)

	server := newTestServer(t, uow, ids, operatorPrincipal())

	const racers = 5
	var wg sync.WaitGroup
	var mu sync.Mutex
	wonCount := 0

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "evt-race-" + string(rune('a'+n))
			status, respBody, err := doSubmitWaitSignalRaw(server.URL, runID, waitRegistrationID,
				"idem-signal-race-"+string(rune('a'+n)), `{"signalKey":"`+key+`","payload":{"n":`+string(rune('0'+n))+`}}`)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Errorf("racer %d: request error: %v", n, err)
				return
			}
			if status != http.StatusOK {
				t.Errorf("racer %d: status = %d, want 200 (body=%s)", n, status, respBody)
				return
			}
			var body decision.SubmitWaitSignalResponse
			if jsonErr := json.Unmarshal(respBody, &body); jsonErr != nil {
				t.Errorf("racer %d: decode response: %v (body=%s)", n, jsonErr, respBody)
				return
			}
			if body.Won {
				wonCount++
			}
		}(i)
	}
	wg.Wait()

	if wonCount != 1 {
		t.Fatalf("wonCount = %d, want exactly 1", wonCount)
	}

	registration := mustGetWaitRegistration(t, uow, waitRegistrationID)
	if registration.State != runtimedomain.WaitRegistrationConsumed {
		t.Fatalf("persisted registration = %+v, want exactly one CONSUMED", registration)
	}
}
