package projection

import (
	"reflect"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// TestReducers_DeterministicGolden is V6-08's own "deterministic reducer/
// golden" Verify requirement: for every classified Apply entry, applying
// its own Reducer to a fixed prior row plus a fixed payload always
// produces the exact same expected row, every single call — the core
// correctness property V6-08A's own crash-recovery replay (re-applying the
// same event after a crash) depends on completely.
func TestReducers_DeterministicGolden(t *testing.T) {
	tests := []struct {
		name        string
		eventType   string
		version     int
		prior       WorkItemCardRow
		payloadJSON string
		want        WorkItemCardRow
	}{
		{
			name: "RootWorkItemCreated creates a BACKLOG card",
			eventType: "RootWorkItemCreated", version: 1,
			prior:       WorkItemCardRow{},
			payloadJSON: `{"workItemId":"wi-1","projectId":"p-1","familyId":"f-1","workspaceSetId":"ws-1","title":"Root task","scopeCount":2}`,
			want: WorkItemCardRow{
				WorkItemID: "wi-1", ProjectID: "p-1", FamilyID: "f-1", Title: "Root task",
				IsRoot: true, WorkspaceSetID: "ws-1", Status: statusBacklog,
			},
		},
		{
			name: "ChildWorkItemCreated creates a BACKLOG card with no WorkspaceSetID",
			eventType: "ChildWorkItemCreated", version: 1,
			prior:       WorkItemCardRow{},
			payloadJSON: `{"workItemId":"wi-2","projectId":"p-1","familyId":"f-1","parentWorkItemId":"wi-1","title":"Child task","scopeCount":1}`,
			want: WorkItemCardRow{
				WorkItemID: "wi-2", ProjectID: "p-1", FamilyID: "f-1", Title: "Child task",
				ParentWorkItemID: "wi-1", IsRoot: false, Status: statusBacklog,
			},
		},
		{
			name: "WORK_ITEM_MARKED_READY moves BACKLOG to READY",
			eventType: "WORK_ITEM_MARKED_READY", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusBacklog},
			payloadJSON: `{"workItemId":"wi-1","projectId":"p-1","familyId":"f-1","markedBy":"operator"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusReady},
		},
		{
			name: "WORK_ITEM_MARKED_READY never regresses a terminal DONE card",
			eventType: "WORK_ITEM_MARKED_READY", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusDone},
			payloadJSON: `{"workItemId":"wi-1","projectId":"p-1","familyId":"f-1","markedBy":"operator"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusDone},
		},
		{
			name: "ScopeExpansionRequested increments the pending count",
			eventType: "ScopeExpansionRequested", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, PendingScopeExpansionCount: 1},
			payloadJSON: `{"requestId":"r-1","familyId":"f-1","projectId":"p-1","referencedWorkItemId":"wi-1","grantCount":2}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, PendingScopeExpansionCount: 2},
		},
		{
			name: "ScopeExpansionApproved decrements the pending count, floored at zero",
			eventType: "ScopeExpansionApproved", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, PendingScopeExpansionCount: 0},
			payloadJSON: `{"requestId":"r-1","familyId":"f-1","projectId":"p-1","newScopeVersion":2,"referencedWorkItemId":"wi-1","approvedBy":"operator"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, PendingScopeExpansionCount: 0},
		},
		{
			name: "ScopeExpansionRejected decrements the pending count",
			eventType: "ScopeExpansionRejected", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, PendingScopeExpansionCount: 1},
			payloadJSON: `{"requestId":"r-1","familyId":"f-1","decisionNote":"no","rejectedBy":"operator"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, PendingScopeExpansionCount: 0},
		},
		{
			name: "ScopeExpansionWithdrawn decrements the pending count",
			eventType: "ScopeExpansionWithdrawn", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, PendingScopeExpansionCount: 1},
			payloadJSON: `{"requestId":"r-1","familyId":"f-1","withdrawnBy":"operator"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, PendingScopeExpansionCount: 0},
		},
		{
			name: "WorkflowRunStarted moves READY to ACTIVE and records the Run",
			eventType: "WorkflowRunStarted", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusReady},
			payloadJSON: `{"runId":"run-1","workItemId":"wi-1","nodeRunId":"nr-1","nodeKey":"start"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, ActiveRunID: "run-1", ActiveRunStatus: runStatusActive},
		},
		{
			name: "RUN_CANCELLATION_REQUESTED sets a transient CANCELLING badge for the matching Run",
			eventType: "RUN_CANCELLATION_REQUESTED", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, ActiveRunID: "run-1", ActiveRunStatus: runStatusActive},
			payloadJSON: `{"runId":"run-1","workItemId":"wi-1","actor":"operator","reason":"stop"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, ActiveRunID: "run-1", ActiveRunStatus: runStatusCancelling},
		},
		{
			name: "RUN_CANCELLATION_REQUESTED for a stale/non-matching RunID is a no-op",
			eventType: "RUN_CANCELLATION_REQUESTED", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, ActiveRunID: "run-2", ActiveRunStatus: runStatusActive},
			payloadJSON: `{"runId":"run-1","workItemId":"wi-1","actor":"operator","reason":"stop"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, ActiveRunID: "run-2", ActiveRunStatus: runStatusActive},
		},
		{
			name: "RUN_COMPLETION_REQUESTED sets a transient COMPLETING badge",
			eventType: "RUN_COMPLETION_REQUESTED", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, ActiveRunID: "run-1", ActiveRunStatus: runStatusActive},
			payloadJSON: `{"runId":"run-1","workItemId":"wi-1","endNodeRunId":"nr-9","endNodeKey":"end"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, ActiveRunID: "run-1", ActiveRunStatus: runStatusCompleting},
		},
		{
			name: "RUN_FAILED moves the card to BLOCKED (no dedicated FAILED status exists)",
			eventType: "RUN_FAILED", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, ActiveRunID: "run-1", ActiveRunStatus: runStatusActive},
			payloadJSON: `{"runId":"run-1","workItemId":"wi-1","reason":"crashed"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusBlocked, ActiveRunID: "run-1", ActiveRunStatus: runStatusFailed},
		},
		{
			name: "RUN_CANCELLED clears the active Run and returns an otherwise-ACTIVE card to READY",
			eventType: "RUN_CANCELLED", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, ActiveRunID: "run-1", ActiveRunStatus: runStatusCancelling},
			payloadJSON: `{"runId":"run-1","workItemId":"wi-1"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusReady, ActiveRunStatus: runStatusCancelled},
		},
		{
			name: "WORKFLOW_RUN_FINALIZED(COMPLETED) moves the card to DONE",
			eventType: "WORKFLOW_RUN_FINALIZED", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, ActiveRunID: "run-1", ActiveRunStatus: runStatusCompleting},
			payloadJSON: `{"jobId":"job-1","jobLeaseOwner":"owner-1","runId":"run-1","terminalState":"COMPLETED"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusDone},
		},
		{
			name: "WORKFLOW_RUN_FINALIZED(FAILED) moves the card to BLOCKED",
			eventType: "WORKFLOW_RUN_FINALIZED", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, ActiveRunID: "run-1", ActiveRunStatus: runStatusActive},
			payloadJSON: `{"jobId":"job-1","jobLeaseOwner":"owner-1","runId":"run-1","terminalState":"FAILED"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusBlocked, ActiveRunStatus: runStatusFailed},
		},
		{
			name: "WORK_ITEM_BLOCKED moves the card to BLOCKED and records the blocker",
			eventType: "WORK_ITEM_BLOCKED", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive},
			payloadJSON: `{"workItemId":"wi-1","blockerId":"b-1","blockerType":"AWAITING_APPROVAL","sourceRunId":"run-1"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusBlocked, BlockerCount: 1, TopBlockerType: "AWAITING_APPROVAL"},
		},
		{
			name: "WORK_ITEM_BLOCKER_RESOLVED trusts the event's own NewWorkItemStatus",
			eventType: "WORK_ITEM_BLOCKER_RESOLVED", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusBlocked, BlockerCount: 1, TopBlockerType: "AWAITING_APPROVAL"},
			payloadJSON: `{"workItemId":"wi-1","blockerId":"b-1","blockerType":"AWAITING_APPROVAL","resolutionMode":"RETRY","resolvedBy":"operator","workItemUnblocked":true,"newWorkItemStatus":"ACTIVE"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, BlockerCount: 0},
		},
		{
			name: "WORK_ITEM_BLOCKER_RESOLVED with WorkItemUnblocked=false leaves Status alone",
			eventType: "WORK_ITEM_BLOCKER_RESOLVED", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusBlocked, BlockerCount: 2, TopBlockerType: "AWAITING_APPROVAL"},
			payloadJSON: `{"workItemId":"wi-1","blockerId":"b-1","blockerType":"AWAITING_APPROVAL","resolutionMode":"RETRY","resolvedBy":"operator","workItemUnblocked":false}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusBlocked, BlockerCount: 1, TopBlockerType: "AWAITING_APPROVAL"},
		},
		{
			name: "WORK_ITEM_CANCELLED always wins, terminal",
			eventType: "WORK_ITEM_CANCELLED", version: 1,
			prior:       WorkItemCardRow{WorkItemID: "wi-1", Status: statusActive, ActiveRunID: "run-1", ActiveRunStatus: runStatusActive},
			payloadJSON: `{"workItemId":"wi-1","jobId":"job-1"}`,
			want:        WorkItemCardRow{WorkItemID: "wi-1", Status: statusCancelled},
		},
	}

	c := NewCatalog()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			classification, ok := c.Classify(eventschema.EventKey{EventType: tt.eventType, SchemaVersion: tt.version})
			if !ok {
				t.Fatalf("Classify(%s v%d): not found", tt.eventType, tt.version)
			}
			if classification.Outcome != Apply {
				t.Fatalf("Classify(%s v%d): Outcome = %v, want Apply", tt.eventType, tt.version, classification.Outcome)
			}
			for i := 0; i < 3; i++ {
				got, err := classification.Reducer(tt.prior, tt.payloadJSON)
				if err != nil {
					t.Fatalf("call %d: Reducer error = %v", i, err)
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Fatalf("call %d: Reducer(prior, payload) = %+v, want %+v", i, got, tt.want)
				}
			}
		})
	}
}

// TestEntityKeyOf_DirectAndDeferredResolution covers both EntityKeyOf
// shapes this Catalog uses: direct extraction from the payload's own
// WorkItemID-shaped field, and the deferred (ok=false) case for events
// whose payload never names a WorkItemID at all.
func TestEntityKeyOf_DirectAndDeferredResolution(t *testing.T) {
	c := NewCatalog()

	rootCreated, _ := c.Classify(eventschema.EventKey{EventType: "RootWorkItemCreated", SchemaVersion: 1})
	key, ok, err := rootCreated.EntityKeyOf(`{"workItemId":"wi-1","projectId":"p-1","familyId":"f-1","workspaceSetId":"ws-1","title":"Root"}`)
	if err != nil || !ok || key != "wi-1" {
		t.Fatalf("RootWorkItemCreated EntityKeyOf = (%q, %v, %v), want (wi-1, true, nil)", key, ok, err)
	}

	finalized, _ := c.Classify(eventschema.EventKey{EventType: "WORKFLOW_RUN_FINALIZED", SchemaVersion: 1})
	if finalized.EntityKeyOf != nil {
		t.Fatal("WORKFLOW_RUN_FINALIZED: EntityKeyOf should be nil (payload has no WorkItemID)")
	}
	if finalized.EntityKeyNote == "" {
		t.Fatal("WORKFLOW_RUN_FINALIZED: EntityKeyNote should document the resolution strategy")
	}

	scopeRequestedWithTarget, _ := c.Classify(eventschema.EventKey{EventType: "ScopeExpansionRequested", SchemaVersion: 1})
	key, ok, err = scopeRequestedWithTarget.EntityKeyOf(`{"requestId":"r-1","familyId":"f-1","projectId":"p-1","referencedWorkItemId":"wi-2","grantCount":1}`)
	if err != nil || !ok || key != "wi-2" {
		t.Fatalf("ScopeExpansionRequested (with target) EntityKeyOf = (%q, %v, %v), want (wi-2, true, nil)", key, ok, err)
	}
	key, ok, err = scopeRequestedWithTarget.EntityKeyOf(`{"requestId":"r-1","familyId":"f-1","projectId":"p-1","grantCount":1}`)
	if err != nil || ok || key != "" {
		t.Fatalf("ScopeExpansionRequested (no target) EntityKeyOf = (%q, %v, %v), want (\"\", false, nil)", key, ok, err)
	}

	scopeRejected, _ := c.Classify(eventschema.EventKey{EventType: "ScopeExpansionRejected", SchemaVersion: 1})
	key, ok, err = scopeRejected.EntityKeyOf(`{"requestId":"r-1","familyId":"f-1","decisionNote":"no","rejectedBy":"operator"}`)
	if err != nil || ok || key != "" {
		t.Fatalf("ScopeExpansionRejected EntityKeyOf = (%q, %v, %v), want (\"\", false, nil) — always deferred", key, ok, err)
	}
}

// TestCanonicalRowHash_DeterministicAndSensitiveToContent is V6-08's own
// "canonical snapshot hash" Verify requirement.
func TestCanonicalRowHash_DeterministicAndSensitiveToContent(t *testing.T) {
	row := WorkItemCardRow{WorkItemID: "wi-1", ProjectID: "p-1", FamilyID: "f-1", Title: "Root", IsRoot: true, Status: statusBacklog}

	first, err := row.CanonicalRowHash()
	if err != nil {
		t.Fatalf("CanonicalRowHash: %v", err)
	}
	second, err := row.CanonicalRowHash()
	if err != nil {
		t.Fatalf("CanonicalRowHash (second call): %v", err)
	}
	if first != second {
		t.Fatalf("CanonicalRowHash is not deterministic: %q != %q", first, second)
	}
	if len(first) < len("sha256:") || first[:len("sha256:")] != "sha256:" {
		t.Fatalf("CanonicalRowHash = %q, want a sha256:<hex> string", first)
	}

	changed := row
	changed.Status = statusReady
	changedHash, err := changed.CanonicalRowHash()
	if err != nil {
		t.Fatalf("CanonicalRowHash (changed row): %v", err)
	}
	if changedHash == first {
		t.Fatal("CanonicalRowHash did not change after the row's own Status changed")
	}

	// Two rows with byte-identical field values, even when one was built
	// field-by-field in a different order (Go struct literal field order
	// never affects the marshaled result), hash identically — proving this
	// is a genuine content hash, not an accidental incidental artifact of
	// construction order.
	reordered := WorkItemCardRow{Status: statusBacklog, IsRoot: true, Title: "Root", FamilyID: "f-1", ProjectID: "p-1", WorkItemID: "wi-1"}
	reorderedHash, err := reordered.CanonicalRowHash()
	if err != nil {
		t.Fatalf("CanonicalRowHash (reordered fields): %v", err)
	}
	if reorderedHash != first {
		t.Fatalf("CanonicalRowHash differs for the SAME content built in a different field order: %q != %q", reorderedHash, first)
	}
}
