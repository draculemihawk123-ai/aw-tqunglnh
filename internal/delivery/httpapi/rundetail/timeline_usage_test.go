package rundetail_test

// V9-13a (live finding F4): GET /runs/{id}/timeline carries what the provider
// CLI reported each attempt used (usage) and the run's total, read back from the
// attempts' USAGE_REPORTED events. An attempt with no such event has no usage
// key at all, so every pre-V9-13a response stays byte-identical.

import (
	"context"
	"encoding/json"
	"io"
	"math"
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
)

func seedUsage(t *testing.T, uow ports.UnitOfWork, attemptID string, sequence uint64, usage ports.AgentUsage) {
	t.Helper()
	payload, err := json.Marshal(agentevents.Payload{ObservedAt: time.Now().UTC(), Usage: &usage})
	if err != nil {
		t.Fatal(err)
	}
	err = uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.AgentEvents().AppendBatch(context.Background(), []ports.AgentEventRecord{{
			ID: "usage-" + attemptID, AttemptID: attemptID, Sequence: sequence, Kind: string(ports.AgentEventUsageReported),
			SchemaVersion: 1, PayloadJSON: string(payload), CreatedAt: time.Now().UTC(),
		}})
	})
	if err != nil {
		t.Fatalf("seed usage %s: %v", attemptID, err)
	}
}

func TestGetRunTimeline_HTTP_AttemptsCarryReportedUsageAndTheRunItsTotal(t *testing.T) {
	ctx := context.Background()
	_, uow := openRunDetailTestStore(t, "timeline-usage.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	started, err := runtime.StartWorkflowRun(ctx, uow, ids, testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun"),
		runtime.StartWorkflowRunRequest{ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID())})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	seedFailedAttempt(t, uow, started.NodeRunID, "attempt-a", 1, errorcode.CodeProviderUnavailable, "")
	seedFailedAttempt(t, uow, started.NodeRunID, "attempt-b", 2, errorcode.CodeProviderUnavailable, "")
	seedFailedAttempt(t, uow, started.NodeRunID, "attempt-silent", 3, errorcode.CodeProviderUnavailable, "")
	seedUsage(t, uow, "attempt-a", 1, ports.AgentUsage{InputTokens: 1000, CachedInputTokens: 400, OutputTokens: 50, CostUSD: 0.2})
	seedUsage(t, uow, "attempt-b", 1, ports.AgentUsage{InputTokens: 10, OutputTokens: 5, CostUSD: 0.05})

	server := newTestServer(t, uow, redact.NewMatcher())
	resp, err := http.Get(server.URL + "/runs/" + started.RunID + "/timeline")
	if err != nil {
		t.Fatalf("GET timeline: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body=%s", resp.StatusCode, raw)
	}
	var body struct {
		Entries []runtime.TimelineEntryView `json:"entries"`
		Usage   *runtime.UsageView          `json:"usage"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}
	byAttempt := map[string]runtime.TimelineEntryView{}
	for _, entry := range body.Entries {
		if entry.Kind == runtime.TimelineEntryExecutionAttempt {
			byAttempt[entry.AttemptID] = entry
		}
	}
	if u := byAttempt["attempt-a"].Usage; u == nil || u.InputTokens != 1000 || u.CachedInputTokens != 400 || u.OutputTokens != 50 || math.Abs(u.CostUSD-0.2) > 1e-9 {
		t.Fatalf("attempt-a usage = %+v, want 1000/400/50 tokens and 0.2 USD", u)
	}
	if byAttempt["attempt-silent"].Usage != nil {
		t.Fatalf("an attempt with no USAGE_REPORTED event has usage %+v", byAttempt["attempt-silent"].Usage)
	}
	if body.Usage == nil || body.Usage.InputTokens != 1010 || body.Usage.OutputTokens != 55 || math.Abs(body.Usage.CostUSD-0.25) > 1e-9 {
		t.Fatalf("run usage = %+v, want the sum of attempt-a and attempt-b (1010 in, 55 out, 0.25 USD)", body.Usage)
	}
	// omitempty: the silent attempt contributes no key.
	if got := strings.Count(string(raw), `"costUsd"`); got != 3 {
		t.Fatalf("costUsd appears %d times, want 3 (two attempts and the run total): %s", got, raw)
	}
}
