package recovery_test

// This file duplicates internal/app/runtime's own AGENT-node scheduling/
// admission fixture helpers (schedule_test.go's agentExecutableDocument/
// validAgentProfileDocument/attemptPolicyDocument/permissionPolicyDocument/
// publishAgentProfileVersion(Only)/publishPolicyVersion/fullyResolvablePolicyRefs/
// seedEffectiveScope, advance_test.go's publishWorkflowVersionDocument,
// commands_test.go's testCommand, admission_test.go's writeAdmissionExecutable/
// admissionPinnedBuild) rather than importing them — the same "Go test
// helpers are not exported across packages, duplicated locally" discipline
// fixture_test.go's own doc comment already documents for this package's
// lighter fixtures. Every helper drives a REAL *sqlite.Store through the REAL
// application commands (definitions.CreateDefinition/PublishDefinitionVersion,
// workflow.Compile/PublishWorkflowVersion, runtime.StartWorkflowRun/AdvanceRun/
// ScheduleExecutableNodeRun, runtime.ExecuteNodeHandler.Handle) — never a
// hand-seeded row.
//
// blockedAdmissionFixture is this file's own addition (no equivalent exists
// anywhere in this codebase yet — not even at the application layer:
// internal/app/runtime's own admission tests are fake-UnitOfWork-only,
// confirmed by grepping for a sqlite-backed admission test before writing
// this file): it drives a real AGENT node all the way to a genuinely
// admission-BLOCKED NodeRun/Attempt/WorkItemBlocker against real sqlite, the
// one precondition every RetryBlockedActivation HTTP test in this package
// needs. It deliberately blocks on the ISOLATION axis (via a small
// stateful toggleIsolationChecker this file also defines) rather than
// adapter-build-drift or capability/multi-repo-write: isolation is the only
// one of the four admission checks whose own real dependency
// (ports.IsolationEnforcementChecker) this fixture fully controls and can
// make behave differently between the original block and a later retry —
// every other axis depends only on immutable pins (the resolved execution
// profile, the pinned AdapterBuildVersion, the NodeRun's own frozen
// EffectiveScope) that can never legitimately flip from failing to passing
// for the SAME NodeRun, by RetryBlockedActivation's own explicit "không
// repin Run" design (retry_blocked_activation.go's own package doc
// comment) — so this fixture pins a real, valid, non-drifting build and a
// working agentregistry.Registry for every OTHER axis, leaving isolation as
// the one deliberately test-controlled variable.

import (
	"context"
	"errors"
	"os"
	stdruntime "runtime"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

func testCommand(idempotencyKey, requestHash string, scope ports.CommandScope, commandType string) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: scope, RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type: commandType, RequestHash: requestHash,
	}
}

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

func publishAgentProfileVersion(t *testing.T, uow ports.UnitOfWork, definitionID, versionID string, doc agentprofile.AgentProfileDocument) {
	t.Helper()
	publishAgentProfileVersionOnly(t, uow, definitionID, versionID, doc)
	if doc.ContextPolicyRef.VersionID != "" {
		publishPolicyVersion(t, uow, doc.ContextPolicyRef.DefinitionID, doc.ContextPolicyRef.VersionID, policy.PolicyDocument{
			Category: policy.CategoryContext,
			Context:  &policy.ContextRules{Selector: []string{"*"}, Budget: policy.ContextBudget{MaxTokens: doc.Budget.MaxTokens}},
		})
	}
}

func publishAgentProfileVersionOnly(t *testing.T, uow ports.UnitOfWork, definitionID, versionID string, doc agentprofile.AgentProfileDocument) {
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

func publishWorkflowVersionDocument(t *testing.T, uow ports.UnitOfWork, projectID, definitionID, versionID string, document workflow.WorkflowDocument) workflow.WorkflowVersion {
	t.Helper()
	pid := project.ProjectID(projectID)
	def := workflow.WorkflowDefinition{
		ID: workflow.WorkflowDefinitionID(definitionID), ProjectID: &pid,
		Name: "workflow " + definitionID, Status: workflow.DefinitionActive, Version: 1,
	}
	candidate, err := workflow.Compile(def, workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID(versionID), VersionNumber: 1, Document: document,
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "1", Hash: "sha256:dependency-1"},
		}},
		PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("compile workflow %s: %v", versionID, err)
	}
	var published workflow.WorkflowVersion
	ctx := context.Background()
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		p, err := tx.Definitions().PublishWorkflowVersion(ctx, def, candidate)
		published = p
		return err
	})
	if err != nil {
		t.Fatalf("publish workflow version %s: %v", versionID, err)
	}
	return published
}

func writeAdmissionExecutable(t *testing.T, content string) string {
	t.Helper()
	path := t.TempDir() + string(os.PathSeparator) + "provider-cli"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable fixture: %v", err)
	}
	return path
}

func admissionPinnedBuild(t *testing.T, executablePath string, capabilities ports.AgentCapabilities) domainadapterbuild.Build {
	t.Helper()
	contentHash, err := adapterbuild.HashExecutableFile(executablePath)
	if err != nil {
		t.Fatalf("hash fixture: %v", err)
	}
	manifest := domainadapterbuild.CapabilityManifest{
		SupportsStart: capabilities.SupportsStart, SupportsResume: capabilities.SupportsResume, SupportsCancel: capabilities.SupportsCancel,
	}
	_, manifestHash, err := domainadapterbuild.HashCapabilityManifest(manifest)
	if err != nil {
		t.Fatalf("hash manifest: %v", err)
	}
	tuple := domainadapterbuild.CandidateTuple{
		ProviderKey: string(capabilities.Provider), ExecutablePath: executablePath,
		ExecutableContentHash: contentHash, ProtocolVersion: capabilities.ProtocolVersion,
		CapabilityManifestHash: manifestHash, OS: stdruntime.GOOS, Toolchain: stdruntime.Version(),
		ConfigIdentity: "default",
	}
	build, err := domainadapterbuild.NewBuild(domainadapterbuild.NewBuildRequest{
		Tuple: tuple, CapabilityManifest: manifest, RegisteredBy: "operator-1", RegisteredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("construct pinned build: %v", err)
	}
	return build
}

// toggleIsolationChecker is a stateful, counting ports.IsolationEnforcementChecker:
// the first failCalls invocations return an error, every later one passes —
// see this file's own package doc comment for why isolation is the one
// admission axis this package's tests can legitimately flip between an
// original block and a later retry.
type toggleIsolationChecker struct {
	mu        sync.Mutex
	calls     int
	failCalls int
}

func (c *toggleIsolationChecker) VerifyEnforceable(context.Context, policy.IsolationTier) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls <= c.failCalls {
		return errors.New("test: isolation enforcement not available yet")
	}
	return nil
}

// blockedAdmissionFixture is this package's own shared precondition for
// every RetryBlockedActivation HTTP test: a real, sqlite-backed AGENT
// NodeRun admission-BLOCKED for ISOLATION_ENFORCEMENT_UNAVAILABLE, via the
// real scheduling pipeline (StartWorkflowRun -> AdvanceRun ->
// ScheduleExecutableNodeRun -> a REAL claimed EXECUTE_NODE job -> a real
// runtime.ExecuteNodeHandler.Handle call). failIsolationCalls controls the
// toggleIsolationChecker's own failure count (this fixture's own ONE
// Handle call always consumes exactly one) — 1 lets a later retry pass, a
// large number keeps every future call failing (the "revalidation still
// fails" case).
type blockedAdmissionFixtureResult struct {
	Store      *sqlite.Store
	UOW        ports.UnitOfWork
	IDs        idsource.Source
	ProjectID  string
	RunID      string
	NodeRunID  string
	AttemptID  string
	WorkItemID string
	BlockerID  string
	Registry   *agentregistry.Registry
	Isolation  *toggleIsolationChecker
}

func blockedAdmissionFixture(t *testing.T, dbName string, failIsolationCalls int) blockedAdmissionFixtureResult {
	t.Helper()
	store, uow := openRecoveryTestStore(t, dbName)
	ids := idsource.NewSequential("id")
	ctx := context.Background()
	const projectID, repositoryID = "project-1", "repo-1"

	root := readyWorkItemFixture(t, uow, ids, projectID, repositoryID)
	seedEffectiveScope(t, uow, root.WorkItemID, repositoryID)

	executablePath := writeAdmissionExecutable(t, "recovery-fixture-binary-v1")
	capabilities := ports.AgentCapabilities{
		Provider: ports.ProviderClaude, AdapterVersion: "claude-stream-json/v1", ProtocolVersion: "claude-stream-json/v1",
		SupportsStart: true, SupportsResume: true, SupportsCancel: true,
	}
	build := admissionPinnedBuild(t, executablePath, capabilities)
	buildID := build.ID()
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, _, err := tx.AdapterBuilds().InsertIfAbsent(ctx, build)
		return err
	}); err != nil {
		t.Fatalf("register pinned adapter build: %v", err)
	}
	registry, err := agentregistry.New(ctx, &fake.AgentExecutor{CapabilitiesResult: capabilities})
	if err != nil {
		t.Fatalf("agentregistry.New: %v", err)
	}

	version := publishWorkflowVersionDocument(t, uow, projectID, "wf-def-admission", "wf-v-admission",
		agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), &buildID))
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	startCmd := testCommand("idem-start-admission", "hash-start-admission", ports.ProjectScope(projectID), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: projectID, WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->implement): %v", err)
	}

	scheduled, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: startResult.RunID, NodeRunID: hop.NextNodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}

	job, _ := claimJobOfKind(t, store, runtime.ExecuteNodeJobKind, 5)

	isolation := &toggleIsolationChecker{failCalls: failIsolationCalls}
	handler := runtime.NewExecuteNodeHandler(uow, ids, &fake.NodeExecutor{}, clock.System{}, isolation, registry, nil)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("ExecuteNodeHandler.Handle (fixture setup, blocking admission): %v", err)
	}

	return blockedAdmissionFixtureResult{
		Store: store, UOW: uow, IDs: ids, ProjectID: projectID,
		RunID: startResult.RunID, NodeRunID: hop.NextNodeRunID, AttemptID: scheduled.AttemptID, WorkItemID: root.WorkItemID,
		BlockerID: scheduled.AttemptID + "-admission-blocker", Registry: registry, Isolation: isolation,
	}
}
