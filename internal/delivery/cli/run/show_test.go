package run_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	clirun "github.com/taQuangLing/agent-workflow/internal/delivery/cli/run"
)

func TestRunShow_ReturnsRunDetail(t *testing.T) {
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
	if err := clirun.Show(context.Background(), deps, []string{runID}, &stdout, &stderr); err != nil {
		t.Fatalf("Show: %v, stderr=%s", err, stderr.String())
	}
	var detail runtime.RunDetail
	if err := json.Unmarshal(stdout.Bytes(), &detail); err != nil {
		t.Fatalf("decode RunDetail: %v\nstdout=%s", err, stdout.String())
	}
	if detail.RunID != runID {
		t.Fatalf("RunDetail.RunID = %q, want %q", detail.RunID, runID)
	}
	if detail.State != "RUNNING" {
		t.Fatalf("RunDetail.State = %q, want RUNNING", detail.State)
	}
}

func TestRunShow_UnknownRunID(t *testing.T) {
	deps, _ := newRunDeps(t)
	var stdout, stderr bytes.Buffer
	err := clirun.Show(context.Background(), deps, []string{"no-such-run"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Show with an unknown runId succeeded, want an error")
	}
}

func TestRunShow_MissingRunIDArgument(t *testing.T) {
	deps, _ := newRunDeps(t)
	var stdout, stderr bytes.Buffer
	err := clirun.Show(context.Background(), deps, nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("Show with no <runId> argument succeeded, want a usage error")
	}
}
