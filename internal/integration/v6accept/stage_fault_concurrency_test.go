package v6accept

import (
	"net/http"
	"testing"
)

// concurrencyFixture builds a real child WorkItem sitting at BACKLOG,
// eligible for mark-ready — the shared setup scenarios 6 and 7 both race
// against — and returns its id and its current ETag.
func concurrencyFixture(t *testing.T, title string) (j *journey, workItemID, etag string) {
	t.Helper()
	j = newFaultRunner(t)
	j.createRootWorkItem(t, title+"-root")
	workItemID = j.createChild(t, title+"-child", "v6-14a concurrency fixture", j.verification.workflow)
	j.waitWorkspaceReady(t)

	var readiness struct {
		Ready    bool     `json:"ready"`
		Problems []string `json:"problems"`
	}
	j.s.api.get(t, "/projects/"+j.projectID+"/work-items/"+workItemID+"/readiness").requireStatus(t, http.StatusOK).decode(t, &readiness)
	if !readiness.Ready {
		t.Fatalf("work item %s is not ready to mark-ready: %v", workItemID, readiness.Problems)
	}
	view := j.s.api.get(t, "/projects/"+j.projectID+"/work-items/"+workItemID).requireStatus(t, http.StatusOK)
	return j, workItemID, view.etag()
}

// TestV6HTTPAcceptance_Fault_ConcurrentSameIdempotencyKey is V6-14A
// scenario 6: "concurrent same idempotency key". Two real, genuinely
// concurrent HTTP requests (separate goroutines, separate TCP
// connections, never sequential) fire the IDENTICAL command — same
// Idempotency-Key, same body, same If-Match — against the same real
// WorkItem's mark-ready endpoint. Contract §1 point 2's own "transaction
// recheck receipt rồi commit CAS ... atomically" is what this proves end
// to end: SQLite's own single-writer serialization means only ONE of the
// two request's own transactions can ever be first to actually check
// "does a receipt already exist for this key" and, finding none, run the
// real BACKLOG->READY CAS; the other's transaction, whenever it runs,
// finds that receipt already committed and replays its stored result
// rather than re-evaluating the precondition a second time.
//
// Verify: both callers receive byte-identical response bodies (the exact
// same wire result), and the WorkItem's own version — read fresh after
// both calls settle — advanced by EXACTLY one, never two: the "requires
// BACKLOG" precondition was genuinely checked exactly once, not raced.
func TestV6HTTPAcceptance_Fault_ConcurrentSameIdempotencyKey(t *testing.T) {
	requireAcceptance(t)
	j, workItemID, etag := concurrencyFixture(t, "fault-concurrency-same")

	var before struct {
		Version uint64 `json:"version"`
	}
	j.s.api.get(t, "/projects/"+j.projectID+"/work-items/"+workItemID).requireStatus(t, http.StatusOK).decode(t, &before)

	const key = "fault-concurrency-same-key-1"
	outcomes := doRawConcurrent(2, func(i int) httpOutcome {
		return doRaw(j.s.api, http.MethodPost, "/work-items/"+workItemID+"/mark-ready", map[string]any{}, withIdempotencyKey(key), withIfMatch(etag))
	})
	for i, o := range outcomes {
		if o.err != nil {
			t.Fatalf("concurrent call %d: transport error: %v", i, o.err)
		}
	}
	if outcomes[0].status != http.StatusOK || outcomes[1].status != http.StatusOK {
		t.Fatalf("concurrent identical mark-ready calls = %d and %d, want 200 and 200: %s / %s",
			outcomes[0].status, outcomes[1].status, tail(string(outcomes[0].body), 300), tail(string(outcomes[1].body), 300))
	}
	if string(outcomes[0].body) != string(outcomes[1].body) {
		t.Fatalf("concurrent identical mark-ready calls returned DIFFERENT bodies (one must be the other's replayed receipt):\ncall 0: %s\ncall 1: %s",
			outcomes[0].body, outcomes[1].body)
	}

	var after struct {
		Version uint64 `json:"version"`
		Status  string `json:"status"`
	}
	j.s.api.get(t, "/projects/"+j.projectID+"/work-items/"+workItemID).requireStatus(t, http.StatusOK).decode(t, &after)
	if after.Status != "READY" {
		t.Fatalf("work item status after concurrent identical mark-ready = %s, want READY", after.Status)
	}
	if after.Version != before.Version+1 {
		t.Fatalf("work item version after concurrent identical mark-ready = %d, want exactly %d (before+1) — a value of %d+2 would mean the BACKLOG precondition was checked and applied TWICE",
			after.Version, before.Version+1, before.Version)
	}
}

// TestV6HTTPAcceptance_Fault_ConcurrentDifferentIdempotencyKeysSameTarget is
// V6-14A scenario 7: "concurrent different idempotency keys, same target".
// Two real, genuinely concurrent requests with DIFFERENT Idempotency-Keys
// but the identical If-Match ExpectedVersion race the same real WorkItem's
// mark-ready endpoint. Because the keys differ, neither call can ever be
// short-circuited by the other's receipt (contract §1 point 2: "key mới
// phải kiểm ExpectedVersion") — both must genuinely reach the fenced CAS,
// and SQLite's own serialized-write transactions mean exactly one of them
// can win it for real.
//
// Verify: exactly one call gets 200 (and the WorkItem really is READY
// afterward), the other gets a real optimistic-concurrency conflict —
// this codebase's own established convention for a lost CAS
// (ports.ErrOptimisticConflict / workapp.ErrWorkItemNotEligibleForReady,
// internal/delivery/httpapi/workitem/errors.go, both mapped to 409) —
// never both succeeding, never a corrupted/merged state, and never a
// version that advanced by more than one.
func TestV6HTTPAcceptance_Fault_ConcurrentDifferentIdempotencyKeysSameTarget(t *testing.T) {
	requireAcceptance(t)
	j, workItemID, etag := concurrencyFixture(t, "fault-concurrency-diff")

	var before struct {
		Version uint64 `json:"version"`
	}
	j.s.api.get(t, "/projects/"+j.projectID+"/work-items/"+workItemID).requireStatus(t, http.StatusOK).decode(t, &before)

	outcomes := doRawConcurrent(2, func(i int) httpOutcome {
		key := "fault-concurrency-diff-key-" + string(rune('A'+i))
		return doRaw(j.s.api, http.MethodPost, "/work-items/"+workItemID+"/mark-ready", map[string]any{}, withIdempotencyKey(key), withIfMatch(etag))
	})
	for i, o := range outcomes {
		if o.err != nil {
			t.Fatalf("concurrent call %d: transport error: %v", i, o.err)
		}
	}

	wins, conflicts := 0, 0
	for i, o := range outcomes {
		switch o.status {
		case http.StatusOK:
			wins++
		case http.StatusConflict, http.StatusPreconditionFailed:
			conflicts++
		default:
			t.Fatalf("concurrent call %d (different keys, same target) = %d, want 200 or a conflict: %s", i, o.status, tail(string(o.body), 300))
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("concurrent different-key mark-ready calls on the same target: %d won, %d conflicted (want exactly 1 and 1) — statuses: %d, %d",
			wins, conflicts, outcomes[0].status, outcomes[1].status)
	}

	var after struct {
		Version uint64 `json:"version"`
		Status  string `json:"status"`
	}
	j.s.api.get(t, "/projects/"+j.projectID+"/work-items/"+workItemID).requireStatus(t, http.StatusOK).decode(t, &after)
	if after.Status != "READY" {
		t.Fatalf("work item status after the race = %s, want READY", after.Status)
	}
	if after.Version != before.Version+1 {
		t.Fatalf("work item version after the race = %d, want exactly %d (before+1) — never merged/corrupted by two winners", after.Version, before.Version+1)
	}
}
