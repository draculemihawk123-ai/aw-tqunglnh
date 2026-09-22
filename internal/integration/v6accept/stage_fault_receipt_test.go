package v6accept

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// TestV6HTTPAcceptance_Fault_CrashAfterReceiptCommit is V6-14A scenario 1:
// "crash after receipt commit". CreateProject is the simplest real command
// with full receipt semantics (contract §1 point 2) that needs no
// project/repository prerequisite of its own, so this scenario needs
// nothing beyond a bare started stack.
//
// The real signal polled for: an INDEPENDENT HTTP connection's own
// GET /projects lists the new project by name. Since CreateProject's own
// transaction commits the Project row, its event and its receipt
// atomically in ONE transaction (contract §1 point 2 — "commit CAS +
// event/outbox/job + receipt + result atomically"), that independent read
// becoming non-empty is real, externally observable proof the transaction
// has already committed durably to disk; it can only ever fire AFTER the
// commit, never before or racing it (SQLite's own transaction isolation).
// serve is then hard-killed immediately, racing to land between that
// commit and the ORIGINAL request's own response bytes reaching this
// test's other, still in-flight connection — proving the crash lands at,
// or after, the receipt commit, never before it.
//
// Whichever side of that narrow race the hard kill actually lands on, the
// invariant this scenario exists to prove holds either way: after restart,
// replaying the EXACT SAME Idempotency-Key/body returns the SAME project,
// and the real row count for that name is exactly 1 — never a duplicate
// create from "replay didn't find a receipt so it ran the command again".
func TestV6HTTPAcceptance_Fault_CrashAfterReceiptCommit(t *testing.T) {
	requireAcceptance(t)
	s := newStack(t)
	s.start(t)
	t.Cleanup(func() {
		if s.serve != nil {
			s.serve.dumpOnFailure(t)
		}
		if s.worker != nil {
			s.worker.dumpOnFailure(t)
		}
	})

	const projectName = "fault-receipt-crash-project"
	const idemKey = "fault-receipt-crash-key-1"
	requestBody, err := json.Marshal(map[string]string{"name": projectName})
	if err != nil {
		t.Fatalf("encode request body: %v", err)
	}

	type createOutcome struct {
		responded bool
		status    int
		body      []byte
		err       error
	}
	outcome := make(chan createOutcome, 1)
	go func() {
		request, buildErr := http.NewRequest(http.MethodPost, s.api.baseURL+"/projects", bytes.NewReader(requestBody))
		if buildErr != nil {
			outcome <- createOutcome{err: buildErr}
			return
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(httpapi.SessionTokenHeader, s.api.token)
		request.Header.Set(httpapi.IdempotencyKeyHeader, idemKey)
		response, doErr := privateHTTPClient().Do(request)
		if doErr != nil {
			outcome <- createOutcome{err: doErr}
			return
		}
		defer response.Body.Close()
		payload, readErr := io.ReadAll(response.Body)
		outcome <- createOutcome{responded: readErr == nil, status: response.StatusCode, body: payload, err: readErr}
	}()

	type projectListView struct {
		Projects []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	var observedID string
	deadline := time.Now().Add(20 * time.Second)
	for observedID == "" {
		if time.Now().After(deadline) {
			t.Fatal("an independent GET /projects read never observed the new project — the create request never became visible")
		}
		response := s.api.get(t, "/projects")
		if response.status == http.StatusOK {
			var list projectListView
			response.decode(t, &list)
			for _, project := range list.Projects {
				if project.Name == projectName {
					observedID = project.ID
					break
				}
			}
		}
	}

	// The real crash: hard-kill serve THE INSTANT the commit became
	// observable, racing the original request's own in-flight response.
	s.hardKillServe(t)

	select {
	case o := <-outcome:
		t.Logf("original create-project call: responded=%v status=%d err=%v body=%s", o.responded, o.status, o.err, tail(string(o.body), 300))
	case <-time.After(5 * time.Second):
		t.Log("original create-project call never returned before the test moved on (connection reset by the hard kill)")
	}

	s.startServe(t)

	// Replay: EXACT same Idempotency-Key and body, on the fresh process.
	replay := s.api.post(t, "/projects", map[string]string{"name": projectName}, withIdempotencyKey(idemKey)).
		requireStatus(t, http.StatusOK, http.StatusCreated)
	var replayed struct {
		ProjectID string `json:"projectId"`
	}
	replay.decode(t, &replayed)
	if replayed.ProjectID != observedID {
		t.Fatalf("replay returned project id %q, want the SAME id %q the independent read observed before the crash", replayed.ProjectID, observedID)
	}

	var list projectListView
	s.api.get(t, "/projects").requireStatus(t, http.StatusOK).decode(t, &list)
	count := 0
	for _, project := range list.Projects {
		if project.Name == projectName {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("projects named %q after crash+restart+replay = %d, want exactly 1 (a duplicate would mean the replay re-ran the command instead of finding its own receipt)", projectName, count)
	}
}
