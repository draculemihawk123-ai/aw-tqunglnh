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

func TestRunGraph_ReturnsNodesEdgesAndActivations(t *testing.T) {
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
	if err := clirun.Graph(context.Background(), deps, []string{runID}, &stdout, &stderr); err != nil {
		t.Fatalf("Graph: %v, stderr=%s", err, stderr.String())
	}
	var result clirun.GraphResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode GraphResult: %v\nstdout=%s", err, stdout.String())
	}
	if len(result.Nodes) != 2 {
		t.Fatalf("len(Nodes) = %d, want 2 (start, end)", len(result.Nodes))
	}
	if len(result.Activations) != 1 {
		t.Fatalf("len(Activations) = %d, want 1 (the START activation)", len(result.Activations))
	}
	if result.NextCursor != "" {
		t.Fatalf("NextCursor = %q, want empty (everything fit on one page)", result.NextCursor)
	}
}

// TestRunGraph_PagesAndCursorContinues is this task's own "paging" Verify
// bullet: with --limit smaller than the real activation count, `run graph`
// must expose cursor continuation rather than silently truncating —
// mirrors internal/delivery/httpapi/rundetail's own
// TestGetRunGraph_HTTP_PagesAndStaysStableAcrossConcurrentWrite (minus the
// concurrent-write half, out of this task's own narrower scope) using the
// identical routerChainDocument fixture to produce more than one real
// NodeRun activation via real runtime.AdvanceRun hops.
func TestRunGraph_PagesAndCursorContinues(t *testing.T) {
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
	// Now: start (SUCCEEDED), router1 (SUCCEEDED), router2 (RUNNING) — 3
	// activations total.

	var page1Out, page1Err bytes.Buffer
	if err := clirun.Graph(context.Background(), deps, []string{"--limit", "2", runID}, &page1Out, &page1Err); err != nil {
		t.Fatalf("Graph (page 1): %v, stderr=%s", err, page1Err.String())
	}
	var page1 clirun.GraphResult
	if err := json.Unmarshal(page1Out.Bytes(), &page1); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if len(page1.Activations) != 2 {
		t.Fatalf("page 1 len(Activations) = %d, want 2", len(page1.Activations))
	}
	if page1.NextCursor == "" {
		t.Fatal("page 1 NextCursor is empty, want a continuation cursor (3 activations exist, limit=2)")
	}

	var page2Out, page2Err bytes.Buffer
	if err := clirun.Graph(context.Background(), deps, []string{"--limit", "2", "--cursor", page1.NextCursor, runID}, &page2Out, &page2Err); err != nil {
		t.Fatalf("Graph (page 2): %v, stderr=%s", err, page2Err.String())
	}
	var page2 clirun.GraphResult
	if err := json.Unmarshal(page2Out.Bytes(), &page2); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if len(page2.Activations) != 1 {
		t.Fatalf("page 2 len(Activations) = %d, want 1 (the remaining activation)", len(page2.Activations))
	}
	if page2.NextCursor != "" {
		t.Fatalf("page 2 NextCursor = %q, want empty (fully paged)", page2.NextCursor)
	}

	// The two pages together must cover every activation exactly once, no
	// duplicate and no silent gap.
	seen := map[string]bool{}
	for _, a := range page1.Activations {
		seen[a.NodeRunID] = true
	}
	for _, a := range page2.Activations {
		if seen[a.NodeRunID] {
			t.Fatalf("NodeRunID %s appeared on both pages", a.NodeRunID)
		}
		seen[a.NodeRunID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("total distinct activations across both pages = %d, want 3", len(seen))
	}
}

// TestRunGraph_CursorForDifferentRunIsRejected proves the resync half of
// this package's own local cursor contract (cursor.go's own doc comment):
// a cursor minted for one Run must never be silently accepted for a
// different one.
func TestRunGraph_CursorForDifferentRunIsRejected(t *testing.T) {
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
	if _, err := runtime.AdvanceRun(context.Background(), deps.UOW, ids, runtime.AdvanceRunRequest{
		RunID: runID, NodeRunID: startEnvelope.Result.NodeRunID,
	}); err != nil {
		t.Fatalf("AdvanceRun (start->router1): %v", err)
	}

	root2 := readyWorkItemFixture(t, deps.UOW, ids, "project-2", "repo-2")
	version2 := publishTestWorkflowVersion(t, deps.UOW, "project-2", "wf-def-2", "wf-v-2", routerChainDocument())
	var startOut2, startErr2 bytes.Buffer
	if err := clirun.Start(context.Background(), deps, []string{
		"--workflow-version-id", string(version2.ID()), "--idempotency-key", "idem-2", root2.WorkItemID,
	}, &startOut2, &startErr2); err != nil {
		t.Fatalf("Start (run 2): %v, stderr=%s", err, startErr2.String())
	}
	runID2 := decodeStartRunID(t, startOut2.Bytes())

	// Mint a real NextCursor for runID (2 activations exist now, limit=1
	// leaves one more to page).
	var page1Out, page1Err bytes.Buffer
	if err := clirun.Graph(context.Background(), deps, []string{"--limit", "1", runID}, &page1Out, &page1Err); err != nil {
		t.Fatalf("Graph (run 1): %v, stderr=%s", err, page1Err.String())
	}
	var page1 clirun.GraphResult
	if err := json.Unmarshal(page1Out.Bytes(), &page1); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if page1.NextCursor == "" {
		t.Fatal("expected a real NextCursor for run 1 (2 activations, limit=1)")
	}

	// Reusing runID's own cursor against runID2 must be rejected, never
	// silently accepted or mixed with runID2's own activations.
	var stdout, stderr bytes.Buffer
	err := clirun.Graph(context.Background(), deps, []string{"--cursor", page1.NextCursor, runID2}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Graph accepted a cursor minted for a different run, want ErrCursorRunMismatch")
	}
	if !errors.Is(err, clirun.ErrCursorRunMismatch) {
		t.Fatalf("Graph error = %v, want errors.Is(..., clirun.ErrCursorRunMismatch)", err)
	}
}

func TestRunGraph_MalformedCursorIsRejected(t *testing.T) {
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
	err := clirun.Graph(context.Background(), deps, []string{"--cursor", "not-a-real-token", runID}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Graph with a malformed --cursor succeeded, want ErrCursorInvalid")
	}
	if !errors.Is(err, clirun.ErrCursorInvalid) {
		t.Fatalf("Graph error = %v, want errors.Is(..., clirun.ErrCursorInvalid)", err)
	}
}
