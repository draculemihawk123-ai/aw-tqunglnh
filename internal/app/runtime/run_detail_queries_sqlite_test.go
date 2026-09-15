package runtime_test

// V6-06B's own test suite (docs/design/08-v6-api-projections.md) for
// GetRunDetail/GetRunGraph/GetRunTimeline (run_detail_queries.go). Reuses
// this package's own already-established real-sqlite fixtures verbatim
// (readyFixtureSQLite, publishWorkflowVersionDocument, joinPolicyDocument/
// publishJoinPolicyFixtures/seedRunningAndFinalizeSQLite from
// join_sqlite_test.go, documentWithReworkEdge/requiredEvidenceCompletionPolicy
// from completion_policy_test.go) rather than inventing new ones — every
// fork/join/rework fixture below drives a REAL StartWorkflowRun/AdvanceRun/
// ScheduleExecutableNodeRun/FinalizeExecutionAttempt/EvaluateCompletionCandidate
// sequence, never a hand-seeded NodeRun/BranchToken/Amendment row (this
// task's own Verify-checklist line). The one exception is
// TestGetRunGraph_RedactsBlockReason, a narrow, isolated unit test of the
// BlockReason redaction path alone — hand-seeding ONE NodeRun there mirrors
// this package's own seedRunEvidence/seedPriorEndReach precedent for a
// targeted field-level assertion unrelated to fork/join/rework routing.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

func TestGetRunDetail_SQLite_UnknownRun_NotFound(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-rundetail-notfound.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	if _, err := runtime.GetRunDetail(ctx, uow, "unknown-run"); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetRunDetail(unknown) err = %v, want ErrPersistenceNotFound", err)
	}
	if _, err := runtime.GetRunGraph(ctx, uow, redact.Matcher{}, "unknown-run"); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetRunGraph(unknown) err = %v, want ErrPersistenceNotFound", err)
	}
	if _, err := runtime.GetRunTimeline(ctx, uow, redact.Matcher{}, "unknown-run"); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetRunTimeline(unknown) err = %v, want ErrPersistenceNotFound", err)
	}
}

// TestGetRunDetail_SQLite_ReturnsManifestAndCounts drives a plain
// start->end Run (workflowDocumentV1) all the way to VERIFYING and proves
// GetRunDetail's own manifest/amendment/count fields are real, not
// zero-valued placeholders.
func TestGetRunDetail_SQLite_ReturnsManifestAndCounts(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-rundetail-basic.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->end): %v", err)
	}
	if hop.NextNodeKey != "end" {
		t.Fatalf("hop = %+v, want NextNodeKey=end", hop)
	}

	detail, err := runtime.GetRunDetail(ctx, uow, startResult.RunID)
	if err != nil {
		t.Fatalf("GetRunDetail: %v", err)
	}
	if detail.RunID != startResult.RunID || detail.ProjectID != "project-1" || detail.WorkItemID != root.WorkItemID {
		t.Fatalf("detail identities = %+v, want RunID/ProjectID/WorkItemID to match the started run", detail)
	}
	if detail.State != string(runtimedomain.WorkflowRunVerifying) {
		t.Fatalf("detail.State = %s, want VERIFYING (END reached)", detail.State)
	}
	if detail.WorkflowVersionID != string(version.ID()) {
		t.Fatalf("detail.WorkflowVersionID = %s, want %s", detail.WorkflowVersionID, version.ID())
	}
	if detail.Manifest.WorkflowVersionID != string(version.ID()) || detail.Manifest.CompiledSnapshotHash == "" {
		t.Fatalf("detail.Manifest = %+v, want a real pinned WorkflowVersionID/CompiledSnapshotHash", detail.Manifest)
	}
	if len(detail.Amendments) != 0 {
		t.Fatalf("detail.Amendments = %+v, want none (no scope expansion ever approved)", detail.Amendments)
	}
	if detail.NodeRunCount != 2 {
		t.Fatalf("detail.NodeRunCount = %d, want 2 (start, end)", detail.NodeRunCount)
	}
	if detail.ExecutionAttemptCount != 0 {
		t.Fatalf("detail.ExecutionAttemptCount = %d, want 0 (no executable node in this document)", detail.ExecutionAttemptCount)
	}
}

// driveForkJoinRunSQLite drives a REAL fork(2 branches, ALL policy)/join Run
// through StartWorkflowRun/AdvanceRun/ScheduleExecutableNodeRun/
// FinalizeExecutionAttempt — both branches SUCCEEDED, the JOIN decided
// SUCCEEDED — exactly mirroring join_sqlite_test.go's own
// TestAdvanceRun_SQLite_Join_PersistsAcrossRestart fixture setup, factored
// out so both TestGetRunGraph_SQLite_ForkJoin and
// TestGetRunTimeline_SQLite_ForkJoin can drive the identical real Run.
func driveForkJoinRunSQLite(t *testing.T) (store *sqlite.Store, uow ports.UnitOfWork, runID string) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-rungraph-forkjoin.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow = sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	seedEffectiveScope(t, uow, root.WorkItemID, "repo-1")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-1", "wf-v-1",
		joinPolicyDocument(t, workflow.JoinModeAll, 0, []string{"a", "b"}))
	publishJoinPolicyFixtures(t, uow)

	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->fork): %v", err)
	}
	branchA := findForkedBranch(hop.ForkedBranches, "a")
	branchB := findForkedBranch(hop.ForkedBranches, "b")
	if branchA == nil || branchB == nil {
		t.Fatalf("ForkedBranches = %+v, want a and b", hop.ForkedBranches)
	}

	provider := fake.NewRuntimeExecutionConfigProvider()
	scheduledA, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, provider, runtime.ScheduleExecutableNodeRunRequest{
		RunID: startResult.RunID, NodeRunID: branchA.NodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun(a): %v", err)
	}
	resultA := seedRunningAndFinalizeSQLite(t, ctx, uow, store, ids, startResult.RunID, branchA.NodeRunID, scheduledA.AttemptID, true)
	if !resultA.AdvanceResult.ReachedJoin {
		t.Fatalf("branch A advance result = %+v, want ReachedJoin=true", resultA.AdvanceResult)
	}

	scheduledB, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, provider, runtime.ScheduleExecutableNodeRunRequest{
		RunID: startResult.RunID, NodeRunID: branchB.NodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun(b): %v", err)
	}
	resultB := seedRunningAndFinalizeSQLite(t, ctx, uow, store, ids, startResult.RunID, branchB.NodeRunID, scheduledB.AttemptID, true)
	if !resultB.AdvanceResult.JoinDecided || resultB.AdvanceResult.JoinVerdict != "SUCCEEDED" {
		t.Fatalf("branch B advance result = %+v, want JoinDecided=true/JoinVerdict=SUCCEEDED", resultB.AdvanceResult)
	}
	return store, uow, startResult.RunID
}

// TestGetRunGraph_SQLite_ForkJoin_ReturnsStructureAndActivations is this
// task's own "fork/join fixture" Verify-checklist proof for GetRunGraph:
// PossibleEdges/Nodes come from the compiled WorkflowVersion, Activations
// covers every real NodeRun this fork/join Run actually produced (in
// ActivationSequence order), and BranchTokens reports both branches
// SUCCEEDED.
func TestGetRunGraph_SQLite_ForkJoin_ReturnsStructureAndActivations(t *testing.T) {
	ctx := context.Background()
	_, uow, runID := driveForkJoinRunSQLite(t)

	graph, err := runtime.GetRunGraph(ctx, uow, redact.Matcher{}, runID)
	if err != nil {
		t.Fatalf("GetRunGraph: %v", err)
	}
	if graph.RunID != runID {
		t.Fatalf("graph.RunID = %s, want %s", graph.RunID, runID)
	}

	nodeKeys := map[string]bool{}
	for _, n := range graph.Nodes {
		nodeKeys[n.Key] = true
	}
	for _, want := range []string{"start", "fork", "a_step", "b_step", "join", "end"} {
		if !nodeKeys[want] {
			t.Fatalf("graph.Nodes = %+v, missing compiled node %q", graph.Nodes, want)
		}
	}

	foundJoinToEnd := false
	for _, e := range graph.PossibleEdges {
		if e.Key == "join-to-end" && e.From == "join" && e.To == "end" && e.Outcome == "joined" {
			foundJoinToEnd = true
		}
	}
	if !foundJoinToEnd {
		t.Fatalf("graph.PossibleEdges = %+v, missing the compiled join-to-end edge", graph.PossibleEdges)
	}

	if len(graph.BranchTokens) != 2 {
		t.Fatalf("graph.BranchTokens = %+v, want exactly 2 tokens", graph.BranchTokens)
	}
	for _, tok := range graph.BranchTokens {
		if tok.State != "SUCCEEDED" {
			t.Fatalf("branch token %+v, want SUCCEEDED (both branches completed)", tok)
		}
	}

	// Activations must be sorted non-decreasing by ActivationSequence, and
	// include every real activation this Run produced (start, fork, both
	// branch steps, join, end — at least 6). A tie IS expected here: JOIN's
	// own NodeRun is pinned to forkRun.ActivationSequence+1 (fixed relative
	// to the FORK, HE-14-M09), which legitimately coincides with whichever
	// branch's own first step happens to land on that same sequence — see
	// sortedNodeRuns' own doc comment (run_detail_queries.go).
	if len(graph.Activations) < 6 {
		t.Fatalf("graph.Activations = %+v, want at least 6 real activations", graph.Activations)
	}
	for i := 1; i < len(graph.Activations); i++ {
		if graph.Activations[i-1].ActivationSequence > graph.Activations[i].ActivationSequence {
			t.Fatalf("graph.Activations not sorted non-decreasing by ActivationSequence at index %d: %+v", i, graph.Activations)
		}
	}
	var joinActivation *runtime.NodeActivationView
	for i := range graph.Activations {
		if graph.Activations[i].NodeKey == "join" {
			joinActivation = &graph.Activations[i]
		}
	}
	if joinActivation == nil || joinActivation.SelectedOutcome != "joined" || joinActivation.State != "SUCCEEDED" {
		t.Fatalf("join activation = %+v, want State=SUCCEEDED SelectedOutcome=joined", joinActivation)
	}
}

// TestGetRunTimeline_SQLite_ForkJoin_OrdersActivationsAndAttempts proves
// GetRunTimeline flattens the identical fork/join Run into one
// chronologically-ordered feed: every branch step's own NODE_RUN entry is
// immediately followed by its EXECUTION_ATTEMPT entry, and the whole feed
// stays sorted by ActivationSequence throughout.
func TestGetRunTimeline_SQLite_ForkJoin_OrdersActivationsAndAttempts(t *testing.T) {
	ctx := context.Background()
	_, uow, runID := driveForkJoinRunSQLite(t)

	timeline, err := runtime.GetRunTimeline(ctx, uow, redact.Matcher{}, runID)
	if err != nil {
		t.Fatalf("GetRunTimeline: %v", err)
	}
	if timeline.RunID != runID {
		t.Fatalf("timeline.RunID = %s, want %s", timeline.RunID, runID)
	}
	if len(timeline.Entries) < 8 {
		// 6 NODE_RUN entries (start/fork/a_step/b_step/join/end) + 2
		// EXECUTION_ATTEMPT entries (one per branch step).
		t.Fatalf("timeline.Entries = %+v, want at least 8 entries", timeline.Entries)
	}
	for i := 1; i < len(timeline.Entries); i++ {
		if timeline.Entries[i-1].ActivationSequence > timeline.Entries[i].ActivationSequence {
			t.Fatalf("timeline.Entries not non-decreasing by ActivationSequence at index %d: %+v", i, timeline.Entries)
		}
	}

	attemptCount, nodeRunCount := 0, 0
	lastNodeRunByKey := map[string]string{}
	for _, entry := range timeline.Entries {
		switch entry.Kind {
		case runtime.TimelineEntryNodeRun:
			nodeRunCount++
			lastNodeRunByKey[entry.NodeKey] = entry.NodeRunID
		case runtime.TimelineEntryExecutionAttempt:
			attemptCount++
			if entry.NodeRunID != lastNodeRunByKey[entry.NodeKey] {
				t.Fatalf("attempt entry %+v arrived before its own NODE_RUN entry", entry)
			}
			if entry.AttemptState != "SUCCEEDED" || entry.TerminationReason != "COMPLETED" {
				t.Fatalf("attempt entry %+v, want AttemptState=SUCCEEDED TerminationReason=COMPLETED", entry)
			}
			// StartedAt/FinishedAt are NOT asserted non-nil here:
			// seedRunningAndFinalizeSQLite (join_sqlite_test.go) CASes
			// QUEUED->RUNNING directly, bypassing the real claim-time path
			// that would populate StartedAt — a fixture-shape detail
			// unrelated to this task's own DTO-population correctness.
		default:
			t.Fatalf("unexpected timeline entry kind %q: %+v", entry.Kind, entry)
		}
	}
	if nodeRunCount < 6 {
		t.Fatalf("nodeRunCount = %d, want at least 6", nodeRunCount)
	}
	if attemptCount != 2 {
		t.Fatalf("attemptCount = %d, want exactly 2 (one per branch step)", attemptCount)
	}
}

// TestGetRunGraph_SQLite_Rework_CreatesNewActivationOnReworkTarget drives a
// REAL COMPLETION_REWORK round through the production
// EvaluateCompletionCandidate command (no evidence seeded, so the ladder is
// unsatisfied but a valid, under-budget rework edge exists — the identical
// fixture shape TestEvaluateCompletionCandidate_Rework_ValidReworkEdgeUnderBudget
// already proves against a fake UnitOfWork; this test proves the SAME
// production code path against real SQLite, and that GetRunGraph/
// GetRunTimeline correctly surface the resulting SECOND "implement"
// activation (Iteration 1) alongside the first (Iteration 0) — this task's
// own "rework fixture" Verify-checklist line.
func TestGetRunGraph_SQLite_Rework_CreatesNewActivationOnReworkTarget(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-rungraph-rework.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	publishPolicyVersion(t, uow, "completion-policy-def", "completion-policy-v1", requiredEvidenceCompletionPolicy())
	// resolveCompletionPolicy (completion_policy.go) cross-checks
	// CompletionPolicyRef against this Run's own pinned
	// ExecutionManifest.DependencyManifest ("đối chiếu ID/version/hash...
	// trước khi load policy thật") — unlike publishWorkflowVersionDocument's
	// own hardcoded unrelated "skill" pin (fine for every OTHER test in this
	// package, none of which pin a CompletionPolicyRef), this document needs
	// a REAL matching pin, so this test compiles+publishes inline rather
	// than reusing that helper — mirrors completionCandidateFixture's own
	// identical pattern (completion_policy_test.go), against real sqlite
	// instead of a fake UnitOfWork.
	var policyHash string
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		fields, err := tx.Definitions().LoadVersion(ctx, "completion-policy-v1")
		if err != nil {
			return err
		}
		policyHash = fields.CompiledHash()
		return nil
	}); err != nil {
		t.Fatalf("load completion policy version: %v", err)
	}

	doc := documentWithReworkEdge(3)
	doc.CompletionPolicyRef = &definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: "completion-policy-def", VersionID: "completion-policy-v1"}
	pid := project.ProjectID("project-1")
	wfDef := workflow.WorkflowDefinition{ID: "wf-def-1", ProjectID: &pid, Name: "workflow wf-def-1", Status: workflow.DefinitionActive, Version: 1}
	candidate, err := workflow.Compile(wfDef, workflow.PublishRequest{
		VersionID: "wf-v-1", VersionNumber: 1, Document: doc,
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: string(definition.KindPolicy), Key: "completion-policy-def", Version: "completion-policy-v1", Hash: policyHash},
		}},
		PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("compile workflow: %v", err)
	}
	var version workflow.WorkflowVersion
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		v, err := tx.Definitions().PublishWorkflowVersion(ctx, wfDef, candidate)
		version = v
		return err
	}); err != nil {
		t.Fatalf("publish workflow version: %v", err)
	}

	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	nodeRunID := startResult.NodeRunID
	var run runtimedomain.WorkflowRun
	for {
		if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			run, err = tx.Runtime().GetWorkflowRun(ctx, startResult.RunID)
			return err
		}); err != nil {
			t.Fatalf("GetWorkflowRun: %v", err)
		}
		if run.State == runtimedomain.WorkflowRunVerifying {
			break
		}
		hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: nodeRunID})
		if err != nil {
			t.Fatalf("AdvanceRun: %v", err)
		}
		if !hop.Advanced {
			t.Fatalf("AdvanceRun did not advance (nodeRunID=%s) — would loop forever", nodeRunID)
		}
		nodeRunID = hop.NextNodeRunID
	}

	evalCmd := testCommand("idem-eval-1", "hash-eval-1", ports.ProjectScope("project-1"), "EvaluateCompletionCandidate")
	evalCmd.ExpectedVersion = run.Version
	result, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, evalCmd, runtime.EvaluateCompletionCandidateRequest{RunID: startResult.RunID})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	if result.Outcome != runtime.CompletionOutcomeRework || result.ReworkNodeKey != "implement" || result.ReworkNodeRunID == "" {
		t.Fatalf("result = %+v, want REWORK on implement", result)
	}

	graph, err := runtime.GetRunGraph(ctx, uow, redact.Matcher{}, startResult.RunID)
	if err != nil {
		t.Fatalf("GetRunGraph: %v", err)
	}
	var implementActivations []runtime.NodeActivationView
	for _, a := range graph.Activations {
		if a.NodeKey == "implement" {
			implementActivations = append(implementActivations, a)
		}
	}
	if len(implementActivations) != 2 {
		t.Fatalf("implement activations = %+v, want exactly 2 (original + rework)", implementActivations)
	}
	if implementActivations[0].Iteration != 0 || implementActivations[1].Iteration != 1 {
		t.Fatalf("implement activation iterations = [%d, %d], want [0, 1]", implementActivations[0].Iteration, implementActivations[1].Iteration)
	}
	if implementActivations[1].NodeRunID != result.ReworkNodeRunID {
		t.Fatalf("second implement activation NodeRunID = %s, want %s (result.ReworkNodeRunID)", implementActivations[1].NodeRunID, result.ReworkNodeRunID)
	}
	if implementActivations[0].ActivationSequence >= implementActivations[1].ActivationSequence {
		t.Fatalf("rework activation sequence not increasing: %+v", implementActivations)
	}

	timeline, err := runtime.GetRunTimeline(ctx, uow, redact.Matcher{}, startResult.RunID)
	if err != nil {
		t.Fatalf("GetRunTimeline: %v", err)
	}
	implementEntries := 0
	for _, e := range timeline.Entries {
		if e.Kind == runtime.TimelineEntryNodeRun && e.NodeKey == "implement" {
			implementEntries++
		}
	}
	if implementEntries != 2 {
		t.Fatalf("timeline implement NODE_RUN entries = %d, want 2", implementEntries)
	}
}

// TestGetRunGraph_RedactsBlockReason is a narrow, isolated unit test of the
// BlockReason redaction path (AK-ARCH-024) — hand-seeds ONE extra NodeRun
// whose BlockReason exactly matches a known secret (redact.Matcher.String's
// own exact-equality contract, redact.go's own doc comment: "Matching is
// always exact equality, never a pattern/regex"), unrelated to fork/join/
// rework routing, so hand-seeding here mirrors this package's own
// seedRunEvidence/seedPriorEndReach precedent for a single targeted
// field-level assertion. Uses fake.UnitOfWork (no real sqlite I/O needed to
// prove a pure string-substitution concern).
func TestGetRunGraph_RedactsBlockReason(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")

	root := readyFixture(t, uow, ids, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	const secret = "super-secret-attempt-token"
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		nodeRun, err := runtimedomain.NewNodeRun(
			runtimedomain.NodeRunID("nr-blocked"), runtimedomain.WorkflowRunID(startResult.RunID),
			"blocked-node", 99, 0, nil, "input-hash", "",
		)
		if err != nil {
			return err
		}
		nodeRun.State = runtimedomain.NodeRunBlocked
		nodeRun.BlockReason = secret
		_, err = tx.Runtime().CreateNodeRun(ctx, nodeRun)
		return err
	})
	if err != nil {
		t.Fatalf("seed blocked node run: %v", err)
	}

	matcher := redact.NewMatcher(secret)
	graph, err := runtime.GetRunGraph(ctx, uow, matcher, startResult.RunID)
	if err != nil {
		t.Fatalf("GetRunGraph: %v", err)
	}
	var blocked *runtime.NodeActivationView
	for i := range graph.Activations {
		if graph.Activations[i].NodeRunID == "nr-blocked" {
			blocked = &graph.Activations[i]
		}
	}
	if blocked == nil {
		t.Fatalf("graph.Activations = %+v, missing the hand-seeded blocked node run", graph.Activations)
	}
	if strings.Contains(blocked.BlockReason, secret) {
		t.Fatalf("BlockReason = %q, want the known secret redacted", blocked.BlockReason)
	}
	if blocked.BlockReason != "[REDACTED]" {
		t.Fatalf("BlockReason = %q, want the redaction placeholder", blocked.BlockReason)
	}

	// GetRunTimeline's own NODE_RUN entry shares the identical conversion
	// (nodeRunToTimelineEntry -> toNodeActivationView) — proving it here too
	// guards against the two ever silently diverging.
	timeline, err := runtime.GetRunTimeline(ctx, uow, matcher, startResult.RunID)
	if err != nil {
		t.Fatalf("GetRunTimeline: %v", err)
	}
	found := false
	for _, e := range timeline.Entries {
		if e.NodeRunID == "nr-blocked" {
			found = true
			if strings.Contains(e.BlockReason, secret) {
				t.Fatalf("timeline entry leaked secret: %+v", e)
			}
			if e.BlockReason != "[REDACTED]" {
				t.Fatalf("timeline entry BlockReason = %q, want the redaction placeholder", e.BlockReason)
			}
		}
	}
	if !found {
		t.Fatalf("timeline.Entries missing the hand-seeded blocked node run")
	}
}
