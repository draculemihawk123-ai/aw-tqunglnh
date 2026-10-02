package projection

import (
	"reflect"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// V9-06 / ADR-033 reducer tests: the card counts the Runs a WorkItem starts,
// follows the NEW run on a rerun, sits in BLOCKED while a RUN_FAILED blocker is
// open and returns to READY (no live run) once it is resolved.

func applyReducer(t *testing.T, c *Catalog, eventType string, prior WorkItemCardRow, payloadJSON string) WorkItemCardRow {
	t.Helper()
	classification, ok := c.Classify(eventschema.EventKey{EventType: eventType, SchemaVersion: 1})
	if !ok || classification.Outcome != Apply {
		t.Fatalf("%s v1 is not an Apply classification (ok=%v)", eventType, ok)
	}
	got, err := classification.Reducer(prior, payloadJSON)
	if err != nil {
		t.Fatalf("%s reducer: %v", eventType, err)
	}
	return got
}

// TestReducers_FailResolveRerunWalk walks the exact event sequence the runtime
// appends for fail -> resolve -> rerun, and asserts the card after every event.
func TestReducers_FailResolveRerunWalk(t *testing.T) {
	c := NewCatalog()
	row := WorkItemCardRow{WorkItemID: "wi-1", Status: statusReady}

	// run 1 starts
	row = applyReducer(t, c, "WorkflowRunStarted", row, `{"runId":"run-1","workItemId":"wi-1","nodeRunId":"nr-1","nodeKey":"start"}`)
	want := WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, ActiveRunID: "run-1", ActiveRunStatus: runStatusActive, RunCount: 1}
	if !reflect.DeepEqual(row, want) {
		t.Fatalf("after run 1 started = %+v, want %+v", row, want)
	}

	// run 1 fails, then its RUN_FAILED blocker opens (the order the runtime appends them in)
	row = applyReducer(t, c, "RUN_FAILED", row, `{"runId":"run-1","workItemId":"wi-1","reason":"RUN_FAILED"}`)
	row = applyReducer(t, c, "WORK_ITEM_BLOCKED", row, `{"workItemId":"wi-1","blockerId":"run-1-run-failed-blocker","blockerType":"RUN_FAILED","sourceRunId":"run-1"}`)
	want = WorkItemCardRow{
		WorkItemID: "wi-1", Status: statusBlocked, ActiveRunID: "run-1", ActiveRunStatus: runStatusFailed,
		BlockerCount: 1, TopBlockerType: "RUN_FAILED", RunCount: 1,
	}
	if !reflect.DeepEqual(row, want) {
		t.Fatalf("after run 1 failed = %+v, want %+v", row, want)
	}

	// the operator resolves it: READY, no blocker, no live run (the last run's
	// outcome badge stays), the count unchanged
	row = applyReducer(t, c, "WORK_ITEM_BLOCKER_RESOLVED", row,
		`{"workItemId":"wi-1","blockerId":"run-1-run-failed-blocker","blockerType":"RUN_FAILED","resolutionMode":"RESOLVED","resolvedBy":"operator","workItemUnblocked":true,"newWorkItemStatus":"READY"}`)
	want = WorkItemCardRow{WorkItemID: "wi-1", Status: statusReady, ActiveRunStatus: runStatusFailed, RunCount: 1}
	if !reflect.DeepEqual(row, want) {
		t.Fatalf("after the resolve = %+v, want %+v", row, want)
	}

	// run 2 starts: the card follows the new run and counts two
	row = applyReducer(t, c, "WorkflowRunStarted", row, `{"runId":"run-2","workItemId":"wi-1","nodeRunId":"nr-2","nodeKey":"start"}`)
	want = WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, ActiveRunID: "run-2", ActiveRunStatus: runStatusActive, RunCount: 2}
	if !reflect.DeepEqual(row, want) {
		t.Fatalf("after run 2 started = %+v, want %+v", row, want)
	}

	// replay safety of the pure reducers: the same event on the same prior is the same row
	again := applyReducer(t, c, "WorkflowRunStarted", WorkItemCardRow{WorkItemID: "wi-1", Status: statusReady, ActiveRunStatus: runStatusFailed, RunCount: 1},
		`{"runId":"run-2","workItemId":"wi-1","nodeRunId":"nr-2","nodeKey":"start"}`)
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("WorkflowRunStarted is not deterministic: %+v != %+v", again, want)
	}
}

// TestReducers_BlockerResolved_OnlyUnblockToReadyDropsTheActiveRun: the card
// keeps its Run when the resolution leaves the WorkItem BLOCKED (another
// blocker still open) or sends it back to ACTIVE (the same Run resumes).
func TestReducers_BlockerResolved_OnlyUnblockToReadyDropsTheActiveRun(t *testing.T) {
	c := NewCatalog()
	live := WorkItemCardRow{WorkItemID: "wi-1", Status: statusBlocked, ActiveRunID: "run-1", ActiveRunStatus: runStatusFailed, BlockerCount: 2, TopBlockerType: "RUN_FAILED", RunCount: 1}

	stillBlocked := applyReducer(t, c, "WORK_ITEM_BLOCKER_RESOLVED", live,
		`{"workItemId":"wi-1","blockerId":"b-1","blockerType":"RUN_FAILED","resolutionMode":"RESOLVED","resolvedBy":"operator","workItemUnblocked":false}`)
	if stillBlocked.ActiveRunID != "run-1" || stillBlocked.Status != statusBlocked || stillBlocked.BlockerCount != 1 {
		t.Fatalf("resolution that leaves another blocker open = %+v, want BLOCKED still following run-1", stillBlocked)
	}

	resumed := applyReducer(t, c, "WORK_ITEM_BLOCKER_RESOLVED", WorkItemCardRow{WorkItemID: "wi-1", Status: statusBlocked, ActiveRunID: "run-1", ActiveRunStatus: runStatusActive, BlockerCount: 1, TopBlockerType: "SCOPE_EXPANSION_REQUIRED"},
		`{"workItemId":"wi-1","blockerId":"b-2","blockerType":"SCOPE_EXPANSION_REQUIRED","resolutionMode":"RESOLVED","resolvedBy":"system","workItemUnblocked":true,"newWorkItemStatus":"ACTIVE"}`)
	if resumed.ActiveRunID != "run-1" || resumed.Status != statusActive {
		t.Fatalf("resolution that resumes the same run = %+v, want ACTIVE still following run-1", resumed)
	}

	cancelled := applyReducer(t, c, "WORK_ITEM_BLOCKER_RESOLVED", WorkItemCardRow{WorkItemID: "wi-1", Status: statusCancelled, ActiveRunID: "", BlockerCount: 1},
		`{"workItemId":"wi-1","blockerId":"b-3","blockerType":"RUN_FAILED","resolutionMode":"RESOLVED","resolvedBy":"operator","workItemUnblocked":true,"newWorkItemStatus":"READY"}`)
	if cancelled.Status != statusCancelled {
		t.Fatalf("a terminal card must never regress, got %+v", cancelled)
	}
}

// TestWorkItemCardRow_RunCountIsOmittedWhenZero keeps a row that never ran
// byte-identical (and so hash-identical) to one written before V9-06, and
// carries the count in the canonical JSON once there is one.
func TestWorkItemCardRow_RunCountIsOmittedWhenZero(t *testing.T) {
	never := WorkItemCardRow{WorkItemID: "wi-1", ProjectID: "p-1", FamilyID: "f-1", Title: "Root", IsRoot: true, Status: statusBacklog}
	neverJSON, err := never.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if strings.Contains(string(neverJSON), "runCount") {
		t.Fatalf("a row that never ran serializes runCount: %s", neverJSON)
	}

	ran := never
	ran.RunCount = 2
	ranJSON, err := ran.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if !strings.Contains(string(ranJSON), `"runCount":2`) {
		t.Fatalf("a row with two runs serializes %s, want runCount 2", ranJSON)
	}
	neverHash, _ := never.CanonicalRowHash()
	ranHash, _ := ran.CanonicalRowHash()
	if neverHash == ranHash {
		t.Fatal("CanonicalRowHash ignores RunCount")
	}
}

// TestCatalog_RerunReducersBumpedTheirHandlerVersion: the two reducers whose
// logic V9-06 changed carry handlerVersion 2 (the catalog's own convention for
// "this event's apply logic changed"), the others stay at 1.
func TestCatalog_RerunReducersBumpedTheirHandlerVersion(t *testing.T) {
	c := NewCatalog()
	for eventType, want := range map[string]int{
		"WorkflowRunStarted": 2, "WORK_ITEM_BLOCKER_RESOLVED": 2, "RUN_FAILED": 1, "WORK_ITEM_BLOCKED": 1,
	} {
		classification, ok := c.Classify(eventschema.EventKey{EventType: eventType, SchemaVersion: 1})
		if !ok || classification.HandlerVersion != want {
			t.Errorf("%s handlerVersion = %d (ok=%v), want %d", eventType, classification.HandlerVersion, ok, want)
		}
	}
	if RowSchemaVersion != 2 {
		t.Errorf("RowSchemaVersion = %d, want 2 (WorkItemCardRow gained RunCount)", RowSchemaVersion)
	}
}
