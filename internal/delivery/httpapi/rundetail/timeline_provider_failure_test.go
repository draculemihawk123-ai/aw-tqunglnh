package rundetail_test

// V9-20: GET /runs/{id}/timeline carries the provider CLI's own reason for an
// attempt that FAILED because the provider reported a failure (a session or
// usage limit, an auth problem) as failureDetail, read back from the
// PROVIDER_REPORTED_FAILURE DIAGNOSTIC event in the attempt's agent_events
// stream — redacted, and absent for an attempt that did not fail that way.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// seedProviderAttempt gives nodeRunID one attempt that ended in state, followed
// by a diagnostic with diagnosticCode and message in its agent_events stream.
func seedProviderAttempt(
	t *testing.T, uow ports.UnitOfWork, nodeRunID, attemptID string, attemptNumber uint32,
	state runtimedomain.ExecutionAttemptState, diagnosticCode, message string,
) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		attempt, err := runtimedomain.NewExecutionAttempt(
			runtimedomain.ExecutionAttemptID(attemptID), runtimedomain.NodeRunID(nodeRunID), attemptNumber, "profile-hash", "claude", nil)
		if err != nil {
			return err
		}
		created, err := tx.Runtime().CreateExecutionAttempt(ctx, attempt)
		if err != nil {
			return err
		}
		running, err := tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: attemptID, ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: created.Version,
			NextState: runtimedomain.ExecutionAttemptRunning,
		})
		if err != nil {
			return err
		}
		finish := ports.TransitionExecutionAttemptRequest{
			AttemptID: attemptID, ExpectedState: runtimedomain.ExecutionAttemptRunning, ExpectedVersion: running.Version, NextState: state,
		}
		if state == runtimedomain.ExecutionAttemptFailed {
			finish.TerminationReason = runtimedomain.TerminationReasonExecutionFailed
			finish.FailureCode = errorcode.CodeExecutionFailed
		} else {
			finish.TerminationReason = runtimedomain.TerminationReasonCompleted
		}
		if _, err := tx.Runtime().TransitionExecutionAttempt(ctx, finish); err != nil {
			return err
		}
		if diagnosticCode == "" {
			return nil
		}
		payload, err := json.Marshal(agentevents.Payload{
			ObservedAt: time.Now().UTC(),
			Diagnostic: &ports.AgentDiagnostic{Code: diagnosticCode, Message: message},
		})
		if err != nil {
			return err
		}
		return tx.AgentEvents().AppendBatch(ctx, []ports.AgentEventRecord{{
			ID: "evt-" + attemptID, AttemptID: attemptID, Sequence: 1, Kind: string(ports.AgentEventDiagnostic),
			SchemaVersion: 1, PayloadJSON: string(payload), CreatedAt: time.Now().UTC(),
		}})
	})
	if err != nil {
		t.Fatalf("seed attempt %s: %v", attemptID, err)
	}
}

func TestGetRunTimeline_HTTP_ProviderFailedAttemptShowsTheProvidersReason(t *testing.T) {
	ctx := context.Background()
	_, uow := openRunDetailTestStore(t, "timeline-provider-failure.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	started, err := runtime.StartWorkflowRun(ctx, uow, ids, testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun"),
		runtime.StartWorkflowRunRequest{ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID())})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	secret := "sk-ant-secret-value"
	seedProviderAttempt(t, uow, started.NodeRunID, "attempt-limit", 1, runtimedomain.ExecutionAttemptFailed,
		ports.ProviderFailureDiagnosticCode, "Claude reported a failed result: You've hit your limit · resets 5pm ("+secret+")")
	seedProviderAttempt(t, uow, started.NodeRunID, "attempt-silent", 2, runtimedomain.ExecutionAttemptFailed, "", "")
	seedProviderAttempt(t, uow, started.NodeRunID, "attempt-ok", 3, runtimedomain.ExecutionAttemptSucceeded,
		ports.ProviderFailureDiagnosticCode, "an old diagnostic on an attempt that succeeded")

	server := newTestServer(t, uow, redact.NewMatcher(secret))
	resp, err := http.Get(server.URL + "/runs/" + started.RunID + "/timeline")
	if err != nil {
		t.Fatalf("GET timeline: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, raw)
	}
	var timeline timelineHTTPResponse
	if err := json.Unmarshal(raw, &timeline); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	byAttempt := map[string]runtime.TimelineEntryView{}
	for _, entry := range timeline.Entries {
		if entry.Kind == runtime.TimelineEntryExecutionAttempt {
			byAttempt[entry.AttemptID] = entry
		}
	}
	limit, silent, ok := byAttempt["attempt-limit"], byAttempt["attempt-silent"], byAttempt["attempt-ok"]
	if limit.FailureCode != "EXECUTION_FAILED" || !strings.Contains(limit.FailureDetail, "hit your limit") {
		t.Fatalf("limit entry = %+v, want failureCode EXECUTION_FAILED and the provider's reason as failureDetail", limit)
	}
	if strings.Contains(string(raw), secret) || !strings.Contains(limit.FailureDetail, "[REDACTED]") {
		t.Fatalf("response must mask the known secret inside the provider's reason: %s", raw)
	}
	if silent.FailureDetail != "" {
		t.Fatalf("silent entry = %+v, want no failureDetail for a failure without a provider diagnostic", silent)
	}
	if ok.FailureDetail != "" {
		t.Fatalf("succeeded entry = %+v, want no failureDetail on an attempt that did not fail", ok)
	}
	if strings.Count(string(raw), `"failureDetail"`) != 1 {
		t.Fatalf("failureDetail key appears %d times, want exactly once: %s", strings.Count(string(raw), `"failureDetail"`), raw)
	}
}
