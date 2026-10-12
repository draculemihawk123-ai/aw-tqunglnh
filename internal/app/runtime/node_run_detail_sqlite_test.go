package runtime_test

// V11-11 (ADR-042): reading what a node was asked, did and produced — GetNodeRunDetail — and following an attempt's stream by sequence —
// ListAttemptEvents — against a real sqlite store.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

type failedAgentNode struct {
	f         *rerunSQLite
	store     ports.ArtifactStore
	runID     string
	nodeRunID string
	attemptID string
}

// failedAgentNodeFixture runs the fixture's workflow into a FAILED agent attempt and returns the ids of its node run and attempt.
func failedAgentNodeFixture(t *testing.T) failedAgentNode {
	t.Helper()
	f := newRerunSQLite(t)
	started := f.mustStart("idem-start-1")
	f.failRun(started)
	store, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}
	out := failedAgentNode{f: f, store: store, runID: started.RunID}
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		attempts, err := tx.Runtime().ListExecutionAttemptsForRun(f.ctx, started.RunID)
		if err != nil || len(attempts) != 1 {
			t.Fatalf("attempts = %v, %v; want exactly one", attempts, err)
		}
		out.attemptID, out.nodeRunID = string(attempts[0].ID), string(attempts[0].NodeRunID)
		return nil
	}); err != nil {
		t.Fatalf("load attempt: %v", err)
	}
	return out
}

// putArtifact stores content and records an ATTACHED artifact row for it.
func (n failedAgentNode) putArtifact(id, mediaType string, content []byte) {
	n.f.t.Helper()
	ref, err := n.store.Put(n.f.ctx, ports.ArtifactMetadata{ContentType: mediaType, Sensitivity: redact.Sensitive, Redacted: true}, bytes.NewReader(content))
	if err != nil {
		n.f.t.Fatalf("Put: %v", err)
	}
	record, err := artifact.NewArtifact(artifact.ID(id), "project-1", ref.Locator, ref.SHA256, ref.Size, ref.ContentType, ref.Sensitivity, ref.Redacted,
		artifact.RetentionCanonicalContext, artifact.Attached, false, nil, time.Now().UTC(), 1)
	if err != nil {
		n.f.t.Fatalf("NewArtifact: %v", err)
	}
	if err := n.f.uow.WithSerializedWrite(n.f.ctx, func(tx ports.Tx) error {
		_, err := tx.Artifacts().InsertArtifact(n.f.ctx, record)
		return err
	}); err != nil {
		n.f.t.Fatalf("InsertArtifact: %v", err)
	}
}

func (n failedAgentNode) addEvidence(kind, verdict string, artifactIDs ...string) {
	n.f.t.Helper()
	if err := n.f.uow.WithSerializedWrite(n.f.ctx, func(tx ports.Tx) error {
		row, err := domainruntime.NewEvidence(domainruntime.EvidenceID(n.attemptID+":"+kind), "project-1", workdomain.WorkItemID(n.f.workItemID),
			domainruntime.WorkflowRunID(n.runID), domainruntime.NodeRunID(n.nodeRunID), domainruntime.ExecutionAttemptID(n.attemptID),
			kind, verdict, artifactIDs, workspace.RevisionSet{}, "policy-1", time.Now().UTC())
		if err != nil {
			return err
		}
		_, err = tx.Runtime().CreateEvidence(n.f.ctx, row)
		return err
	}); err != nil {
		n.f.t.Fatalf("CreateEvidence(%s): %v", kind, err)
	}
}

// appendEvents writes agent_events rows for the attempt in the order given, sequences 1..n.
func (n failedAgentNode) appendEvents(events ...[2]any) {
	n.f.t.Helper()
	var records []ports.AgentEventRecord
	for i, e := range events {
		body, err := json.Marshal(e[1])
		if err != nil {
			n.f.t.Fatalf("marshal: %v", err)
		}
		records = append(records, ports.AgentEventRecord{
			ID: n.attemptID + "-ev-" + string(rune('a'+i)), AttemptID: n.attemptID, Sequence: uint64(i + 1), Kind: e[0].(string),
			SchemaVersion: 1, PayloadJSON: string(body), ArtifactRefs: []string{}, CreatedAt: time.Date(2026, 10, 11, 0, 0, i, 0, time.UTC),
		})
	}
	if err := n.f.uow.WithSerializedWrite(n.f.ctx, func(tx ports.Tx) error { return tx.AgentEvents().AppendBatch(n.f.ctx, records) }); err != nil {
		n.f.t.Fatalf("AppendBatch: %v", err)
	}
}

func TestListAttemptEvents_PagesBySequenceRedactsAndBounds(t *testing.T) {
	n := failedAgentNodeFixture(t)
	n.appendEvents(
		[2]any{"EXECUTION_STARTED", agentevents.Payload{}},
		[2]any{"ASSISTANT_MESSAGE", agentevents.Payload{Message: "token is hunter2-secret, continuing"}},
		[2]any{"TOOL_CALL_STARTED", agentevents.Payload{Tool: &ports.AgentToolEvent{CallID: "c1", Name: "Edit", Input: json.RawMessage(`{ "file" : "src/App.tsx" }`)}}},
		[2]any{"TOOL_CALL_FINISHED", agentevents.Payload{Tool: &ports.AgentToolEvent{CallID: "c1", Name: "Edit", Output: strings.Repeat("x", 6000), IsError: true}}},
		[2]any{"DIAGNOSTIC", agentevents.Payload{Diagnostic: &ports.AgentDiagnostic{Code: "PROVIDER_REPORTED_FAILURE", Message: "limit reached"}}},
		[2]any{"EXECUTION_FINISHED", agentevents.Payload{}},
	)
	matcher := redact.NewMatcher("hunter2-secret")

	first, err := runtime.ListAttemptEvents(n.f.ctx, n.f.uow, matcher, n.attemptID, 0, 4)
	if err != nil {
		t.Fatalf("ListAttemptEvents: %v", err)
	}
	if len(first.Events) != 4 || !first.HasMore || first.NextSequence != 4 || !first.Finished || first.AttemptState != "FAILED" {
		t.Fatalf("first page = %+v, want 4 events, more to come, next 4, finished FAILED attempt", first)
	}
	if strings.Contains(first.Events[1].Message, "hunter2-secret") {
		t.Fatalf("a known secret reached the page: %q", first.Events[1].Message)
	}
	if tool := first.Events[2].Tool; tool == nil || tool.Name != "Edit" || tool.Input != `{"file":"src/App.tsx"}` {
		t.Fatalf("tool call = %+v, want a one-line input", first.Events[2].Tool)
	}
	finished := first.Events[3]
	if finished.Tool == nil || !finished.Tool.IsError || len(finished.Tool.Output) != 4<<10 || !finished.Truncated {
		t.Fatalf("tool output must be cut to 4 KiB and flagged: %+v", finished)
	}

	second, err := runtime.ListAttemptEvents(n.f.ctx, n.f.uow, matcher, n.attemptID, first.NextSequence, 4)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Events) != 2 || second.HasMore || second.NextSequence != 6 || second.Events[0].Diagnostic == nil || second.Events[0].Diagnostic.Code != "PROVIDER_REPORTED_FAILURE" {
		t.Fatalf("second page = %+v", second)
	}

	// following an attempt: asking again from the last sequence returns nothing new and the same cursor
	again, err := runtime.ListAttemptEvents(n.f.ctx, n.f.uow, matcher, n.attemptID, second.NextSequence, 4)
	if err != nil || len(again.Events) != 0 || again.NextSequence != 6 || again.HasMore {
		t.Fatalf("empty page = %+v, %v", again, err)
	}
	if _, err := runtime.ListAttemptEvents(n.f.ctx, n.f.uow, matcher, "no-such-attempt", 0, 10); err == nil {
		t.Fatal("an unknown attempt must be an error")
	}
}

func TestGetNodeRunDetail_AFailedAgentNodeShowsInputProgressResultAndWhy(t *testing.T) {
	n := failedAgentNodeFixture(t)
	n.appendEvents(
		[2]any{"EXECUTION_STARTED", agentevents.Payload{}},
		[2]any{"TOOL_CALL_STARTED", agentevents.Payload{Tool: &ports.AgentToolEvent{CallID: "c1", Name: "Read", Input: json.RawMessage(`{"path":"spec.md"}`)}}},
		[2]any{"ASSISTANT_MESSAGE", agentevents.Payload{Message: "I cannot find src/App.tsx, so I stopped."}},
		[2]any{"EXECUTION_FINISHED", agentevents.Payload{}},
	)
	n.putArtifact("art-output", "text/plain; charset=utf-8", []byte("I cannot find src/App.tsx, so I stopped."))
	n.putArtifact("art-diff", "application/vnd.agentkit.diff-manifest+json",
		[]byte(`{"RepositoryID":"repo-1","Files":[{"Code":"M","Path":"src/main.go"},{"Code":"??","Path":"docs/new.md"},{"Code":"D","Path":"old.txt"}]}`))
	n.addEvidence(domainruntime.EvidenceKindAgentOutput, domainruntime.EvidenceVerdictRecorded, "art-output")
	n.addEvidence(domainruntime.EvidenceKindAgentExecution, domainruntime.EvidenceVerdictRecorded, "art-diff")

	detail, err := runtime.GetNodeRunDetail(n.f.ctx, n.f.uow, n.store, redact.NewMatcher(), n.nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRunDetail: %v", err)
	}
	if detail.NodeKey != "implement" || detail.NodeType != "AGENT" || detail.RunID != n.runID || detail.State != "FAILED" {
		t.Fatalf("header = %+v", detail)
	}
	if len(detail.Definition.Pins) == 0 || detail.Definition.Pins[0].Kind != "agent_profile" && detail.Definition.Pins[0].Kind != "AGENT_PROFILE" && detail.Definition.Pins[0].DefinitionID != "agent-profile-def" {
		t.Fatalf("definition = %+v, want the pinned agent profile", detail.Definition)
	}
	if len(detail.Attempts) != 1 || detail.Attempts[0].AttemptID != n.attemptID || detail.Attempts[0].FailureCode != "EXECUTION_FAILED" {
		t.Fatalf("attempts = %+v", detail.Attempts)
	}
	if detail.Failure == nil || detail.Failure.Code != "EXECUTION_FAILED" || detail.Failure.Explanation == "" || detail.Failure.Suggestion == "" || detail.Failure.Action != "RETRY_WORK_ITEM" {
		t.Fatalf("failure = %+v, want an explanation and a next step", detail.Failure)
	}
	if detail.Progress == nil || detail.Progress.Running || detail.Progress.EventCount != 4 || detail.Progress.ToolCalls != 1 || detail.Progress.Messages != 1 {
		t.Fatalf("progress = %+v", detail.Progress)
	}
	if detail.Result.Kind != "AGENT" || detail.Result.FinalMessage == nil || !strings.HasPrefix(detail.Result.FinalMessage.Text, "I cannot find") || !detail.Result.FinalMessage.Partial {
		t.Fatalf("result = %+v, want the failed attempt's last words marked partial", detail.Result)
	}
	got := map[string]string{}
	for _, f := range detail.Result.ChangedFiles {
		got[f.Path] = f.Status
	}
	if got["src/main.go"] != "MODIFIED" || got["docs/new.md"] != "ADDED" || got["old.txt"] != "DELETED" {
		t.Fatalf("changed files = %+v", detail.Result.ChangedFiles)
	}
	if len(detail.Evidence) != 2 || len(detail.Evidence[0].Artifacts) != 1 || detail.Evidence[0].Artifacts[0].MediaType == "" {
		t.Fatalf("evidence = %+v", detail.Evidence)
	}

	// without an artifact store the structure is still there, only the summaries of artifact content are left out
	bare, err := runtime.GetNodeRunDetail(n.f.ctx, n.f.uow, nil, redact.NewMatcher(), n.nodeRunID)
	if err != nil || bare.Result.FinalMessage != nil || len(bare.Evidence) != 2 {
		t.Fatalf("detail without a store = %+v, %v", bare.Result, err)
	}
	if _, err := runtime.GetNodeRunDetail(n.f.ctx, n.f.uow, n.store, redact.NewMatcher(), "no-such-node-run"); err == nil {
		t.Fatal("an unknown node run must be an error")
	}
}

func TestGetNodeRunDetail_StructuralNodeIsOneLine(t *testing.T) {
	f := newRerunSQLite(t)
	started := f.mustStart("idem-start-1")
	detail, err := runtime.GetNodeRunDetail(f.ctx, f.uow, nil, redact.NewMatcher(), started.NodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRunDetail(start): %v", err)
	}
	if detail.NodeType != "START" || detail.Result.Kind != "STRUCTURAL" || detail.Result.Note == "" || detail.Failure != nil || len(detail.Attempts) != 0 {
		t.Fatalf("start node = %+v", detail)
	}
}

func TestExplainFailure_EveryCodeSaysWhatToDoNext(t *testing.T) {
	codes := []string{"SCOPE_VIOLATION", "VALIDATION_FAILED", "EXECUTION_FAILED", "PROVIDER_UNAVAILABLE", "PROVIDER_QUOTA_EXCEEDED", "TIMEOUT", "CANCELLED",
		"WORKSPACE_QUARANTINED", "ADAPTER_BUILD_DRIFT", "ISOLATION_ENFORCEMENT_UNAVAILABLE", "LEASE_LOST", "RETRY_EXHAUSTED", "POLICY_DENIED", "CONFLICT", "SOMETHING_NEW"}
	for _, code := range codes {
		explained := runtime.ExplainFailure(code, "the detail")
		if explained.Code != code || explained.Title == "" || explained.Explanation == "" || explained.Suggestion == "" || explained.Detail != "the detail" {
			t.Errorf("%s: %+v is incomplete", code, explained)
		}
	}
	if drift := runtime.ExplainFailure("VALIDATION_FAILED", "WORKSPACE_HEAD_DRIFT: a commit outside aw"); drift.Action != "ADOPT_HEAD" {
		t.Errorf("a HEAD drift must point at adopt-head: %+v", drift)
	}
}


func TestListEvidenceForWorkItem_NamesTheNodeIterationAndAttempt(t *testing.T) {
	n := failedAgentNodeFixture(t)
	n.putArtifact("art-output", "text/plain", []byte("out"))
	n.addEvidence("AGENT_OUTPUT", "RECORDED", "art-output")
	items, err := runtime.ListEvidenceForWorkItem(n.f.ctx, n.f.uow, ports.ProjectScope("project-1"), n.f.workItemID, runtime.EvidenceFilter{})
	if err != nil {
		t.Fatalf("ListEvidenceForWorkItem: %v", err)
	}
	var found bool
	for _, item := range items {
		if item.Kind != "AGENT_OUTPUT" {
			continue
		}
		found = true
		if item.NodeKey != "implement" || item.Iteration != 0 || item.AttemptNumber != 1 {
			t.Fatalf("evidence = %+v; want its node key, iteration and attempt number", item)
		}
	}
	if !found {
		t.Fatalf("no AGENT_OUTPUT evidence in %+v", items)
	}
}
