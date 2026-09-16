package run_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	clirun "github.com/taQuangLing/agent-workflow/internal/delivery/cli/run"
)

func TestRunTimeline_ReturnsEntries(t *testing.T) {
	deps, ids := newRunDeps(t)
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())

	var startOut, startErr bytes.Buffer
	if err := clirun.Start(context.Background(), deps, []string{
		"--workflow-version-id", string(version.ID()), "--idempotency-key", "idem-1", root.WorkItemID,
	}, &startOut, &startErr); err != nil {
		t.Fatalf("Start: %v, stderr=%s", err, startErr.String())
	}
	runID := decodeStartRunID(t, startOut.Bytes())

	var stdout, stderr bytes.Buffer
	if err := clirun.Timeline(context.Background(), deps, []string{runID}, &stdout, &stderr); err != nil {
		t.Fatalf("Timeline: %v, stderr=%s", err, stderr.String())
	}
	var result clirun.TimelineResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode TimelineResult: %v\nstdout=%s", err, stdout.String())
	}
	if len(result.Entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1 (one NODE_RUN entry for START)", len(result.Entries))
	}
	if result.Entries[0].Kind != runtime.TimelineEntryNodeRun {
		t.Fatalf("Entries[0].Kind = %q, want NODE_RUN", result.Entries[0].Kind)
	}
}

// TestRunTimeline_PagesAndCursorContinues mirrors
// TestRunGraph_PagesAndCursorContinues, proving the identical "paging"
// Verify bullet for `run timeline` — keyed by the 3-part
// (ActivationSequence, NodeRunID, subOrder) total order instead of Graph's
// 2-part key (timeline.go's own doc comment).
func TestRunTimeline_PagesAndCursorContinues(t *testing.T) {
	deps, ids := newRunDeps(t)
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", routerChainDocument())

	var startOut, startErr bytes.Buffer
	if err := clirun.Start(context.Background(), deps, []string{
		"--workflow-version-id", string(version.ID()), "--idempotency-key", "idem-1", root.WorkItemID,
	}, &startOut, &startErr); err != nil {
		t.Fatalf("Start: %v, stderr=%s", err, startErr.String())
	}
	runID := decodeStartRunID(t, startOut.Bytes())
	var startEnvelope struct {
		Result clirun.StartResult `json:"result"`
	}
	if err := json.Unmarshal(startOut.Bytes(), &startEnvelope); err != nil {
		t.Fatalf("decode start envelope: %v", err)
	}

	hop1, err := runtime.AdvanceRun(context.Background(), deps.UOW, ids, runtime.AdvanceRunRequest{
		RunID: runID, NodeRunID: startEnvelope.Result.NodeRunID,
	})
	if err != nil {
		t.Fatalf("AdvanceRun (start->router1): %v", err)
	}
	if _, err := runtime.AdvanceRun(context.Background(), deps.UOW, ids, runtime.AdvanceRunRequest{
		RunID: runID, NodeRunID: hop1.NextNodeRunID,
	}); err != nil {
		t.Fatalf("AdvanceRun (router1->router2): %v", err)
	}
	// 3 NODE_RUN entries exist now (start, router1, router2); ROUTER
	// nodes never produce an EXECUTION_ATTEMPT entry (structural, no real
	// Attempt), so Entries has exactly 3 items.

	var page1Out, page1Err bytes.Buffer
	if err := clirun.Timeline(context.Background(), deps, []string{"--limit", "2", runID}, &page1Out, &page1Err); err != nil {
		t.Fatalf("Timeline (page 1): %v, stderr=%s", err, page1Err.String())
	}
	var page1 clirun.TimelineResult
	if err := json.Unmarshal(page1Out.Bytes(), &page1); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if len(page1.Entries) != 2 {
		t.Fatalf("page 1 len(Entries) = %d, want 2", len(page1.Entries))
	}
	if page1.NextCursor == "" {
		t.Fatal("page 1 NextCursor is empty, want a continuation cursor (3 entries exist, limit=2)")
	}

	var page2Out, page2Err bytes.Buffer
	if err := clirun.Timeline(context.Background(), deps, []string{"--limit", "2", "--cursor", page1.NextCursor, runID}, &page2Out, &page2Err); err != nil {
		t.Fatalf("Timeline (page 2): %v, stderr=%s", err, page2Err.String())
	}
	var page2 clirun.TimelineResult
	if err := json.Unmarshal(page2Out.Bytes(), &page2); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if len(page2.Entries) != 1 {
		t.Fatalf("page 2 len(Entries) = %d, want 1", len(page2.Entries))
	}
	if page2.NextCursor != "" {
		t.Fatalf("page 2 NextCursor = %q, want empty (fully paged)", page2.NextCursor)
	}

	seen := map[string]bool{}
	for _, e := range page1.Entries {
		key := string(e.Kind) + ":" + e.NodeRunID
		seen[key] = true
	}
	for _, e := range page2.Entries {
		key := string(e.Kind) + ":" + e.NodeRunID
		if seen[key] {
			t.Fatalf("entry %s appeared on both pages", key)
		}
		seen[key] = true
	}
	if len(seen) != 3 {
		t.Fatalf("total distinct entries across both pages = %d, want 3", len(seen))
	}
}

func TestRunTimeline_MalformedCursorIsRejected(t *testing.T) {
	deps, ids := newRunDeps(t)
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())

	var startOut, startErr bytes.Buffer
	if err := clirun.Start(context.Background(), deps, []string{
		"--workflow-version-id", string(version.ID()), "--idempotency-key", "idem-1", root.WorkItemID,
	}, &startOut, &startErr); err != nil {
		t.Fatalf("Start: %v, stderr=%s", err, startErr.String())
	}
	runID := decodeStartRunID(t, startOut.Bytes())

	var stdout, stderr bytes.Buffer
	err := clirun.Timeline(context.Background(), deps, []string{"--cursor", "garbage", runID}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Timeline with a malformed --cursor succeeded, want ErrCursorInvalid")
	}
	if !errors.Is(err, clirun.ErrCursorInvalid) {
		t.Fatalf("Timeline error = %v, want errors.Is(..., clirun.ErrCursorInvalid)", err)
	}
}
