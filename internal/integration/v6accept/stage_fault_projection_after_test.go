package v6accept

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestV6HTTPAcceptance_Fault_CrashAfterRebuildCutover is V6-14A scenario 5:
// "crash after rebuild cutover". Unlike scenario 4 (the shadow build
// genuinely in flight), this scenario needs no race at all: it lets a real
// projection rebuild run to completion — the SAME journey.projectionRebuild
// stage_projection_test.go's own happy path already exercises and proves
// (generation advances by exactly one, rebuilt rows equal the live ones
// they replaced) — confirms the new generation is observably LIVE, THEN
// hard-kills+restarts the worker against that already-stable state.
//
// Verify: a crash of an already-cut-over, idle projection must be a pure
// no-op. The kanban board's own projected rows (freshness stripped, the
// same normalizeRows-by-value technique stage_projection_test.go's own
// projectionRebuild uses) are byte-identical before and after, and the
// generation number is unchanged — nothing about the already-committed
// projection may move just because the process under it crashed and came
// back.
func TestV6HTTPAcceptance_Fault_CrashAfterRebuildCutover(t *testing.T) {
	requireAcceptance(t)
	j := newFaultStack(t)
	j.createRootWorkItem(t, "fault-projection-after-root")
	j.projectionRebuild(t)

	before := j.projectionLive(t, 1)
	if before.Freshness.Status != "LIVE" {
		t.Fatalf("freshness status before the crash = %s, want LIVE", before.Freshness.Status)
	}

	j.s.hardKillWorker(t)
	j.s.startWorker(t)

	after := j.projectionLive(t, len(before.Items))
	if after.Freshness.Generation != before.Freshness.Generation {
		t.Fatalf("projection generation changed across a crash+restart of an already-stable projection: before=%d after=%d",
			before.Freshness.Generation, after.Freshness.Generation)
	}
	beforeRows := normalizeRows(t, before.Items)
	afterRows := normalizeRows(t, after.Items)
	if !reflect.DeepEqual(beforeRows, afterRows) {
		beforeJSON, _ := json.Marshal(beforeRows)
		afterJSON, _ := json.Marshal(afterRows)
		t.Fatalf("projected rows differ across a crash+restart of an already-stable projection:\nbefore: %s\nafter:  %s", beforeJSON, afterJSON)
	}
}
