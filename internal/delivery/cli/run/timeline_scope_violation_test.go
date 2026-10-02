package run_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	clirun "github.com/taQuangLing/agent-workflow/internal/delivery/cli/run"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// V9-09 / B3: `aw run timeline` — the CLI twin of GET /runs/{id}/timeline,
// over the same runtime.GetRunTimeline — shows failureDetail for an attempt
// that FAILED with SCOPE_VIOLATION, so an operator at a terminal learns
// WHICH paths broke the scope, not only the error code.
func TestRunTimeline_ScopeViolationAttemptShowsTheViolatingPaths(t *testing.T) {
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

	timelineOf := func() clirun.TimelineResult {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if err := clirun.Timeline(context.Background(), deps, []string{runID}, &stdout, &stderr); err != nil {
			t.Fatalf("Timeline: %v, stderr=%s", err, stderr.String())
		}
		var result clirun.TimelineResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatalf("decode TimelineResult: %v\nstdout=%s", err, stdout.String())
		}
		return result
	}
	nodeRunID := timelineOf().Entries[0].NodeRunID

	ctx := context.Background()
	err := deps.UOW.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		attempt, err := runtimedomain.NewExecutionAttempt("attempt-violation", runtimedomain.NodeRunID(nodeRunID), 1, "profile-hash", "fake-provider", nil)
		if err != nil {
			return err
		}
		created, err := tx.Runtime().CreateExecutionAttempt(ctx, attempt)
		if err != nil {
			return err
		}
		running, err := tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: "attempt-violation", ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: created.Version,
			NextState: runtimedomain.ExecutionAttemptRunning,
		})
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: "attempt-violation", ExpectedState: runtimedomain.ExecutionAttemptRunning, ExpectedVersion: running.Version,
			NextState: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonScopeViolation, FailureCode: "SCOPE_VIOLATION",
		}); err != nil {
			return err
		}
		payload, err := json.Marshal(agentevents.Payload{
			ObservedAt: time.Now().UTC(),
			Diagnostic: &ports.AgentDiagnostic{Code: agentevents.ScopeViolationDiagnosticCode, Message: "1 path(s) outside the granted scope: repo-1:leaked.txt"},
		})
		if err != nil {
			return err
		}
		return tx.AgentEvents().AppendBatch(ctx, []ports.AgentEventRecord{{
			ID: "evt-1", AttemptID: "attempt-violation", Sequence: 1, Kind: string(ports.AgentEventDiagnostic),
			SchemaVersion: 1, PayloadJSON: string(payload), CreatedAt: time.Now().UTC(),
		}})
	})
	if err != nil {
		t.Fatalf("seed scope-violation attempt: %v", err)
	}

	var attemptEntry *runtime.TimelineEntryView
	for _, entry := range timelineOf().Entries {
		entry := entry
		if entry.Kind == runtime.TimelineEntryExecutionAttempt {
			attemptEntry = &entry
		}
	}
	if attemptEntry == nil {
		t.Fatal("the timeline has no EXECUTION_ATTEMPT entry")
	}
	if attemptEntry.FailureCode != "SCOPE_VIOLATION" || !strings.Contains(attemptEntry.FailureDetail, "repo-1:leaked.txt") {
		t.Fatalf("attempt entry = %+v, want failureCode SCOPE_VIOLATION and a failureDetail naming repo-1:leaked.txt", *attemptEntry)
	}
}
