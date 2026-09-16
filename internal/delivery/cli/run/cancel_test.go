package run_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	clirun "github.com/taQuangLing/agent-workflow/internal/delivery/cli/run"
)

func TestRunCancel_FreshThenReplay(t *testing.T) {
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

	var stdout1, stderr1 bytes.Buffer
	if err := clirun.Cancel(context.Background(), deps, []string{"--reason", "operator requested", runID}, &stdout1, &stderr1); err != nil {
		t.Fatalf("first Cancel: %v, stderr=%s", err, stderr1.String())
	}
	var result1 clirun.CancelResult
	if err := json.Unmarshal(stdout1.Bytes(), &result1); err != nil {
		t.Fatalf("decode CancelResult: %v\nstdout=%s", err, stdout1.String())
	}
	if result1.AlreadyRequested {
		t.Fatal("first Cancel reported AlreadyRequested=true, want false")
	}
	if result1.State != "CANCELLING" {
		t.Fatalf("first Cancel State = %q, want CANCELLING", result1.State)
	}
	if result1.CoordinatorJobID == "" {
		t.Fatal("first Cancel: CoordinatorJobID is empty, want a real job id")
	}

	// A repeat `run cancel` call for the SAME run — idempotent by RunID,
	// never by an idempotency key (there is none): safe, reports
	// AlreadyRequested=true, and never enqueues a second coordinator job.
	var stdout2, stderr2 bytes.Buffer
	if err := clirun.Cancel(context.Background(), deps, []string{"--reason", "operator requested again", runID}, &stdout2, &stderr2); err != nil {
		t.Fatalf("second Cancel: %v, stderr=%s", err, stderr2.String())
	}
	var result2 clirun.CancelResult
	if err := json.Unmarshal(stdout2.Bytes(), &result2); err != nil {
		t.Fatalf("decode second CancelResult: %v", err)
	}
	if !result2.AlreadyRequested {
		t.Fatal("second Cancel reported AlreadyRequested=false, want true (RunID-idempotent repeat)")
	}
	if result2.CoordinatorJobID != "" {
		t.Fatalf("second (already-requested) Cancel CoordinatorJobID = %q, want empty (no second coordinator job)", result2.CoordinatorJobID)
	}
	if result2.RunID != result1.RunID {
		t.Fatalf("second Cancel RunID = %q, want %q", result2.RunID, result1.RunID)
	}
}

func TestRunCancel_MissingReason(t *testing.T) {
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
	err := clirun.Cancel(context.Background(), deps, []string{runID}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Cancel with no --reason succeeded, want a usage error")
	}
}

// decodeStartRunID decodes a `run start` stdout document down to just its
// own RunID — a small shared helper every test in this file/package that
// needs a real, already-started Run reaches for.
func decodeStartRunID(t *testing.T, stdout []byte) string {
	t.Helper()
	var envelope struct {
		Result clirun.StartResult `json:"result"`
	}
	if err := json.Unmarshal(stdout, &envelope); err != nil {
		t.Fatalf("decode start envelope: %v\nstdout=%s", err, string(stdout))
	}
	if envelope.Result.RunID == "" {
		t.Fatalf("decoded start envelope has empty RunID: %s", string(stdout))
	}
	return envelope.Result.RunID
}
