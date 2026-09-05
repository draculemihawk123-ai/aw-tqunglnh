package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// --- fixtures shared by every ScheduleExecutableNodeRun test below ---

// agentExecutableDocument is start -> implement(AGENT, real resolvable
// ProfileRef/PolicyRefs/AdapterBuildID) -> end — the fixture this file's
// own tests publish real AgentProfile/Policy definitions against, unlike
// advance_test.go's agentSingleOutcomeDocument (whose ProfileRef
// deliberately never resolves, since that file only needs AdvanceRun to
// leave the node PENDING, never to schedule it).
func agentExecutableDocument(profileVersionID string, policyRefs []definition.DependencyPin, adapterBuildID *string) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "implement", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef: definition.DependencyPin{
					Kind: definition.KindAgentProfile, DefinitionID: "agent-profile-def", VersionID: profileVersionID,
				},
				PolicyRefs:     policyRefs,
				AdapterBuildID: adapterBuildID,
			}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-implement", From: "start", Outcome: "next", To: "implement"},
			{Key: "implement-to-end", From: "implement", Outcome: "done", To: "end"},
		},
	}
}

func validAgentProfileDocument() agentprofile.AgentProfileDocument {
	return agentprofile.AgentProfileDocument{
		ProviderKey: "fake-provider", Model: "fake-model", ToolRefs: []string{"read_file"},
		ContextPolicyRef: definition.DependencyPin{
			Kind: definition.KindPolicy, DefinitionID: "context-policy-def", VersionID: "context-policy-v1",
		},
		Compatibility: agentprofile.Compatibility{OS: []string{"linux"}},
		Budget:        agentprofile.Budget{MaxTokens: 4096},
	}
}

func attemptPolicyDocument(timeoutSeconds uint32) policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryAttempt,
		Attempt:  &policy.AttemptRules{MaxAttempts: 3, BackoffSeconds: 30, TimeoutSeconds: timeoutSeconds},
	}
}

func permissionPolicyDocument() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryPermission,
		Permission: &policy.PermissionRules{
			IsolationTier: policy.IsolationTierEnforcedIsolated, GrantedCapabilities: []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"},
		},
	}
}

// publishAgentProfileVersion publishes a real AgentProfileVersion through
// the real V2-10 application command (definitions.PublishDefinitionVersion)
// — never a shortcut — so this file's tests resolve an ACTUAL compiled
// snapshot the same way ScheduleExecutableNodeRun's own resolver must
// unwrap it in production. Takes ports.UnitOfWork (not *fake.UnitOfWork) so
// this same helper backs both the fake and the sqlite tests in this
// package.
func publishAgentProfileVersion(t *testing.T, uow ports.UnitOfWork, definitionID, versionID string, doc agentprofile.AgentProfileDocument) {
	t.Helper()
	ctx := context.Background()
	createCmd := testCommand("idem-def-"+definitionID, "hash-def-"+definitionID, ports.InstallationScope(), "CreateDefinition")
	if _, err := definitions.CreateDefinition(ctx, uow, createCmd, definitions.CreateDefinitionRequest{
		DefinitionID: definitionID, Kind: definition.KindAgentProfile, Scope: definition.GlobalScope(), Name: "agent profile " + definitionID,
	}); err != nil {
		t.Fatalf("CreateDefinition(%s): %v", definitionID, err)
	}
	publishCmd := testCommand("idem-pub-"+versionID, "hash-pub-"+versionID, ports.InstallationScope(), "PublishDefinitionVersion")
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, publishCmd, definitions.PublishDefinitionVersionRequest{
		DefinitionID: definitionID, Kind: definition.KindAgentProfile,
		Compile: func() (definition.VersionFields, error) {
			return agentprofile.Compile(
				agentprofile.AgentProfileDefinition{
					ID: agentprofile.AgentProfileDefinitionID(definitionID),
					Fields: definition.Fields{
						Kind: definition.KindAgentProfile, Scope: definition.GlobalScope(),
						Name: "agent profile", Status: definition.StatusDraft, Version: 1,
					},
				},
				agentprofile.PublishRequest{
					VersionID: agentprofile.AgentProfileVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
					Document: doc, PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			)
		},
	}); err != nil {
		t.Fatalf("PublishDefinitionVersion(%s): %v", versionID, err)
	}
}

// publishPolicyVersion is publishAgentProfileVersion's own sibling for
// PolicyVersion.
func publishPolicyVersion(t *testing.T, uow ports.UnitOfWork, definitionID, versionID string, doc policy.PolicyDocument) {
	t.Helper()
	ctx := context.Background()
	createCmd := testCommand("idem-def-"+definitionID, "hash-def-"+definitionID, ports.InstallationScope(), "CreateDefinition")
	if _, err := definitions.CreateDefinition(ctx, uow, createCmd, definitions.CreateDefinitionRequest{
		DefinitionID: definitionID, Kind: definition.KindPolicy, Scope: definition.GlobalScope(), Name: "policy " + definitionID,
	}); err != nil {
		t.Fatalf("CreateDefinition(%s): %v", definitionID, err)
	}
	publishCmd := testCommand("idem-pub-"+versionID, "hash-pub-"+versionID, ports.InstallationScope(), "PublishDefinitionVersion")
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, publishCmd, definitions.PublishDefinitionVersionRequest{
		DefinitionID: definitionID, Kind: definition.KindPolicy,
		Compile: func() (definition.VersionFields, error) {
			return policy.Compile(
				policy.PolicyDefinition{
					ID: policy.PolicyDefinitionID(definitionID),
					Fields: definition.Fields{
						Kind: definition.KindPolicy, Scope: definition.GlobalScope(),
						Name: "policy", Status: definition.StatusDraft, Version: 1,
					},
				},
				policy.PublishRequest{
					VersionID: policy.PolicyVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
					Document: doc, PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			)
		},
	}); err != nil {
		t.Fatalf("PublishDefinitionVersion(%s): %v", versionID, err)
	}
}

func fullyResolvablePolicyRefs() []definition.DependencyPin {
	return []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "attempt-policy-def", VersionID: "attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "permission-policy-def", VersionID: "permission-policy-v1"},
	}
}

// seedEffectiveScope directly calls tx.Work().AddEffectiveScope for
// workItemID — test setup standing in for internal/app/work.CreateChildWorkItem's
// own real caller (this file's fixture always uses a ROOT WorkItem via
// readyFixture, and CreateRootWorkItem itself never populates
// ListWorkItemEffectiveScopes — only CreateChildWorkItem does), so this
// file's own EffectiveScope-pinning assertions have real, non-empty content
// to prove ScheduleNodeRun's own CAS actually persists it, mirroring the
// same "poke the state a future command would otherwise reach" discipline
// seedRunningNodeRun (advance_test.go) already uses.
func seedEffectiveScope(t *testing.T, uow ports.UnitOfWork, workItemID, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		scope, err := workdomain.NewRepositoryScope(
			item.FamilyID, 1, project.RepositoryID(repositoryID), workdomain.RepositoryWrite,
			[]string{"**"}, "scheduling test", "actor-1", time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		_, err = tx.Work().AddEffectiveScope(ctx, workItemID, scope)
		return err
	})
	if err != nil {
		t.Fatalf("seed effective scope for work item %s: %v", workItemID, err)
	}
}

// scheduleFixture publishes agent-profile-v1 plus both policy versions,
// starts a run over document, and advances it to the PENDING "implement"
// NodeRun — everything every happy-path/fail-closed test below needs
// before calling ScheduleExecutableNodeRun itself.
func scheduleFixture(t *testing.T, document workflow.WorkflowDocument) (uow *fake.UnitOfWork, ids idsource.Source, runID, pendingNodeRunID string) {
	t.Helper()
	ctx := context.Background()
	u := fake.New()
	seq := idsource.NewSequential("id")
	root := readyFixture(t, u, seq, "project-1", "repo-1")
	seedEffectiveScope(t, u, root.WorkItemID, "repo-1")
	version := publishWorkflowVersionDocument(t, u, "project-1", "wf-def-1", "wf-v-1", document)

	cmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, u, seq, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->implement): %v", err)
	}
	if hop.NextNodeKey != "implement" {
		t.Fatalf("hop.NextNodeKey = %s, want implement", hop.NextNodeKey)
	}
	return u, seq, startResult.RunID, hop.NextNodeRunID
}

// --- ScheduleExecutableNodeRun: happy path ---

func TestScheduleExecutableNodeRun_Agent_SchedulesAttemptAndJob(t *testing.T) {
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	ctx := context.Background()
	provider := fake.NewRuntimeExecutionConfigProvider()
	result, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, provider, runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1", JobID: "job-schedule-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	if !result.Scheduled || result.AttemptID == "" || result.ExecuteJobID == "" {
		t.Fatalf("result = %+v, want Scheduled=true with a minted AttemptID and ExecuteJobID", result)
	}
	if !strings.HasPrefix(result.ExecutionProfileHash, "sha256:") {
		t.Fatalf("result.ExecutionProfileHash = %s, want a sha256: hash", result.ExecutionProfileHash)
	}

	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunQueued {
		t.Fatalf("node run state = %s, want QUEUED", nodeRun.State)
	}
	if nodeRun.ExecutionProfileHash != result.ExecutionProfileHash {
		t.Fatalf("node run ExecutionProfileHash = %s, want %s (must match the pinned attempt)", nodeRun.ExecutionProfileHash, result.ExecutionProfileHash)
	}
	if len(nodeRun.EffectiveScope) != 1 || string(nodeRun.EffectiveScope[0].RepositoryID()) != "repo-1" {
		t.Fatalf("node run EffectiveScope = %+v, want exactly one entry for repo-1", nodeRun.EffectiveScope)
	}
	if nodeRun.ManifestRevision != 0 {
		t.Fatalf("node run ManifestRevision = %d, want 0 (no amendment ever appended)", nodeRun.ManifestRevision)
	}

	attempts := uow.Snapshot.Runtime().(*fake.RuntimeRepository).Attempts()
	attempt, ok := attempts[result.AttemptID]
	if !ok {
		t.Fatalf("attempt %s was not recorded; attempts = %+v", result.AttemptID, attempts)
	}
	if attempt.State != runtimedomain.ExecutionAttemptQueued || attempt.AttemptNumber != 1 {
		t.Fatalf("attempt = %+v, want QUEUED AttemptNumber=1", attempt)
	}
	if attempt.ExecutionProfileHash != result.ExecutionProfileHash || attempt.ProviderKey != "fake-provider" {
		t.Fatalf("attempt = %+v, want ExecutionProfileHash=%s ProviderKey=fake-provider", attempt, result.ExecutionProfileHash)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	var executeJob *ports.EnqueueJobRequest
	for i := range jobs {
		if jobs[i].Kind == runtime.ExecuteNodeJobKind {
			executeJob = &jobs[i]
		}
	}
	if executeJob == nil {
		t.Fatalf("no %s job found among %+v", runtime.ExecuteNodeJobKind, jobs)
	}
	if executeJob.IdempotencyKey != "execute-"+result.AttemptID {
		t.Fatalf("EXECUTE_NODE job idempotency key = %s, want execute-%s", executeJob.IdempotencyKey, result.AttemptID)
	}
	if executeJob.AggregateType != "ExecutionAttempt" || executeJob.AggregateID != result.AttemptID {
		t.Fatalf("EXECUTE_NODE job aggregate = (%s, %s), want (ExecutionAttempt, %s)", executeJob.AggregateType, executeJob.AggregateID, result.AttemptID)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	found := false
	for _, e := range events {
		if e.EventType == runtime.NodeScheduledEventType && e.AggregateID == nodeRunID {
			found = true
			if e.CorrelationID != "corr-1" {
				t.Fatalf("NODE_SCHEDULED event CorrelationID = %s, want corr-1", e.CorrelationID)
			}
		}
	}
	if !found {
		t.Fatalf("no %s event found for node run %s among %+v", runtime.NodeScheduledEventType, nodeRunID, events)
	}

	decisions := uow.Snapshot.Runtime().(*fake.RuntimeRepository).Decisions()
	decisionFound := false
	for _, d := range decisions {
		if d.Kind == "EXECUTION_PROFILE_V1" {
			decisionFound = true
		}
	}
	if !decisionFound {
		t.Fatalf("no EXECUTION_PROFILE_V1 decision artifact recorded among %+v", decisions)
	}
}

// --- ScheduleExecutableNodeRun: idempotent replay ---

func TestScheduleExecutableNodeRun_ReplayAfterAlreadyScheduled_IsNoOp(t *testing.T) {
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	ctx := context.Background()
	provider := fake.NewRuntimeExecutionConfigProvider()
	req := runtime.ScheduleExecutableNodeRunRequest{RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1"}

	first, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, provider, req)
	if err != nil {
		t.Fatalf("first ScheduleExecutableNodeRun: %v", err)
	}
	second, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, provider, req)
	if err != nil {
		t.Fatalf("replayed ScheduleExecutableNodeRun: %v", err)
	}
	if second.Scheduled {
		t.Fatalf("replayed result = %+v, want Scheduled=false", second)
	}

	attempts := uow.Snapshot.Runtime().(*fake.RuntimeRepository).Attempts()
	if len(attempts) != 1 {
		t.Fatalf("attempt count = %d, want exactly 1 (replay must not create a second attempt)", len(attempts))
	}
	if _, ok := attempts[first.AttemptID]; !ok {
		t.Fatalf("original attempt %s missing after replay", first.AttemptID)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	count := 0
	for _, j := range jobs {
		if j.Kind == runtime.ExecuteNodeJobKind {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%s job count = %d, want exactly 1 (replay must not enqueue a duplicate)", runtime.ExecuteNodeJobKind, count)
	}
}

// --- ScheduleExecutableNodeRun: fail-closed paths ---

func TestScheduleExecutableNodeRun_NilProvider_FailsClosed(t *testing.T) {
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	_, err := runtime.ScheduleExecutableNodeRun(context.Background(), uow, ids, nil, runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID,
	})
	if !errors.Is(err, runtime.ErrRuntimeExecutionConfigProviderRequired) {
		t.Fatalf("err = %v, want ErrRuntimeExecutionConfigProviderRequired", err)
	}

	nodeRun, getErr := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if getErr != nil {
		t.Fatalf("GetNodeRun: %v", getErr)
	}
	if nodeRun.State != runtimedomain.NodeRunPending {
		t.Fatalf("node run state = %s, want unchanged PENDING", nodeRun.State)
	}
}

func TestScheduleExecutableNodeRun_ProviderResolveFails_FailsClosed(t *testing.T) {
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	provider := &fake.RuntimeExecutionConfigProvider{Err: fake.ErrRuntimeExecutionConfigUnavailable}
	_, err := runtime.ScheduleExecutableNodeRun(context.Background(), uow, ids, provider, runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID,
	})
	if !errors.Is(err, runtime.ErrRuntimeExecutionConfigUnavailable) {
		t.Fatalf("err = %v, want ErrRuntimeExecutionConfigUnavailable", err)
	}
}

func TestScheduleExecutableNodeRun_NoAttemptPolicy_FailsClosed(t *testing.T) {
	refs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "permission-policy-def", VersionID: "permission-policy-v1"},
	}
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", refs, nil))
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	_, err := runtime.ScheduleExecutableNodeRun(context.Background(), uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID,
	})
	if !errors.Is(err, runtime.ErrAttemptPolicyRequired) {
		t.Fatalf("err = %v, want ErrAttemptPolicyRequired", err)
	}
}

func TestScheduleExecutableNodeRun_NoPermissionPolicy_FailsClosed(t *testing.T) {
	refs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "attempt-policy-def", VersionID: "attempt-policy-v1"},
	}
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", refs, nil))
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))

	_, err := runtime.ScheduleExecutableNodeRun(context.Background(), uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID,
	})
	if !errors.Is(err, runtime.ErrPermissionPolicyRequired) {
		t.Fatalf("err = %v, want ErrPermissionPolicyRequired", err)
	}
}

func TestScheduleExecutableNodeRun_UnresolvableAdapterBuild_FailsClosed(t *testing.T) {
	bogusBuildID := "build-does-not-exist"
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), &bogusBuildID))
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	_, err := runtime.ScheduleExecutableNodeRun(context.Background(), uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID,
	})
	if !errors.Is(err, runtime.ErrAdapterBuildUnresolved) {
		t.Fatalf("err = %v, want ErrAdapterBuildUnresolved", err)
	}
}

// TestScheduleExecutableNodeRun_UnresolvableAgentProfile_FailsClosed reuses
// advance_test.go's own agentSingleOutcomeDocument, whose ProfileRef
// ("agent-default-v1") is deliberately never published by that file's own
// tests — exactly the unresolvable-pin case this function must fail closed
// on rather than schedule a half-resolved profile.
func TestScheduleExecutableNodeRun_UnresolvableAgentProfile_FailsClosed(t *testing.T) {
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentSingleOutcomeDocument())

	_, err := runtime.ScheduleExecutableNodeRun(context.Background(), uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID,
	})
	if !errors.Is(err, ports.ErrDefinitionVersionNotFound) {
		t.Fatalf("err = %v, want ports.ErrDefinitionVersionNotFound", err)
	}
}

func TestScheduleExecutableNodeRun_NodeRunBelongsToDifferentRun_Rejected(t *testing.T) {
	uow, ids, _, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	otherRunID, _ := startWorkflowRunFixtureSecondFamily(t, uow, ids)

	_, err := runtime.ScheduleExecutableNodeRun(context.Background(), uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: otherRunID, NodeRunID: nodeRunID,
	})
	if !errors.Is(err, runtime.ErrNodeRunMismatch) {
		t.Fatalf("err = %v, want ErrNodeRunMismatch", err)
	}
}

// --- AdvanceRun: enqueues SCHEDULE_NODE_RUN for an executable downstream node ---

func TestAdvanceRun_ExecutableDownstream_EnqueuesScheduleNodeRunJob(t *testing.T) {
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))

	hop, err := runtime.AdvanceRun(context.Background(), uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun: %v", err)
	}
	if hop.NextAutoAdvanced || hop.NextJobID != "" {
		t.Fatalf("hop = %+v, want NextAutoAdvanced=false and no NextJobID (AGENT is not structural)", hop)
	}
	if hop.NextScheduleJobID == "" {
		t.Fatalf("hop = %+v, want a non-empty NextScheduleJobID (AGENT is executable)", hop)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	found := false
	for _, j := range jobs {
		if j.Kind == runtime.ScheduleNodeRunJobKind && j.IdempotencyKey == "schedule-"+hop.NextNodeRunID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s job with idempotency key schedule-%s found among %+v", runtime.ScheduleNodeRunJobKind, hop.NextNodeRunID, jobs)
	}
}

// --- NodeSchedulingHandler ---

func TestNodeSchedulingHandler_Handle_SchedulesNodeRun(t *testing.T) {
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	handler := runtime.NewNodeSchedulingHandler(uow, ids, fake.NewRuntimeExecutionConfigProvider())
	payload, err := json.Marshal(runtime.ScheduleNodeRunJobPayload{RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := handler.Handle(context.Background(), ports.DurableJob{ID: "job-1", Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunQueued {
		t.Fatalf("node run state = %s, want QUEUED", nodeRun.State)
	}
}
