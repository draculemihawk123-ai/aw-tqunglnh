package rundetail_test

// V9-09 / B3: GET /runs/{id}/timeline names WHICH paths broke the scope for
// an attempt that FAILED with SCOPE_VIOLATION (failureDetail), read back
// from the SCOPE_VIOLATION DIAGNOSTIC event the executor stored in the
// attempt's agent_events stream — bounded, redacted, and absent for every
// attempt that did not fail that way.

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

// seedFailedAttempt gives nodeRunID one attempt that ended FAILED with
// failureCode, optionally followed by a SCOPE_VIOLATION diagnostic carrying
// detail in its agent_events stream (written through the same payload shape
// agentevents.RecordScopeViolation produces).
func seedFailedAttempt(t *testing.T, uow ports.UnitOfWork, nodeRunID, attemptID string, attemptNumber uint32, failureCode errorcode.Code, detail string) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		attempt, err := runtimedomain.NewExecutionAttempt(
			runtimedomain.ExecutionAttemptID(attemptID), runtimedomain.NodeRunID(nodeRunID), attemptNumber, "profile-hash", "fake-provider", nil)
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
		if _, err := tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: attemptID, ExpectedState: runtimedomain.ExecutionAttemptRunning, ExpectedVersion: running.Version,
			NextState: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonScopeViolation, FailureCode: failureCode,
		}); err != nil {
			return err
		}
		if detail == "" {
			return nil
		}
		payload, err := json.Marshal(agentevents.Payload{
			ObservedAt: time.Now().UTC(),
			Diagnostic: &ports.AgentDiagnostic{Code: agentevents.ScopeViolationDiagnosticCode, Message: detail},
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
		t.Fatalf("seed failed attempt %s: %v", attemptID, err)
	}
}

func TestGetRunTimeline_HTTP_ScopeViolationAttemptNamesTheViolatingPaths(t *testing.T) {
	ctx := context.Background()
	_, uow := openRunDetailTestStore(t, "timeline-scope-violation.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	secret := "hunter2-secret-token"
	seedFailedAttempt(t, uow, started.NodeRunID, "attempt-violation", 1, errorcode.CodeScopeViolation,
		"2 path(s) outside the granted scope: repo-1:leaked.txt; repo-1:dir/"+secret+".txt")
	seedFailedAttempt(t, uow, started.NodeRunID, "attempt-other", 2, errorcode.CodeProviderUnavailable, "")

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
	violation, other := byAttempt["attempt-violation"], byAttempt["attempt-other"]
	if violation.FailureCode != "SCOPE_VIOLATION" || !strings.Contains(violation.FailureDetail, "repo-1:leaked.txt") {
		t.Fatalf("violation entry = %+v, want failureCode SCOPE_VIOLATION and a failureDetail naming repo-1:leaked.txt", violation)
	}
	if strings.Contains(string(raw), secret) || !strings.Contains(violation.FailureDetail, "[REDACTED]") {
		t.Fatalf("response must mask the known secret inside a reported path: %s", raw)
	}
	if other.FailureCode != "PROVIDER_UNAVAILABLE" || other.FailureDetail != "" {
		t.Fatalf("non-violation entry = %+v, want a failureCode and NO failureDetail", other)
	}
	// omitempty: the key itself is absent for an entry without a detail, so
	// pre-V9-09 responses stay byte-identical.
	if strings.Count(string(raw), `"failureDetail"`) != 1 {
		t.Fatalf("failureDetail key appears %d times, want exactly once (only the violation entry): %s", strings.Count(string(raw), `"failureDetail"`), raw)
	}
}
