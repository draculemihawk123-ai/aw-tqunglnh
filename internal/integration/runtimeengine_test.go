// TestRuntimeEngineGate is V4-14's own closing gate for the whole V4
// "durable workflow runtime engine" phase (docs/design/06-v4-runtime-engine.md
// V4-14: "chạy graph chứa tất cả node types bằng fake executors qua
// restart"). It drives one WorkflowDocument covering every node type this
// codebase's authoring vocabulary declares (START, ROUTER, AGENT, WAIT,
// APPROVAL, FORK, COMMAND, MACHINE_GATE, JOIN, END) through the real,
// wired-together system — real internal/app/runtime application commands
// and job handlers, a real internal/app/workerpool.Pool, a real
// *sqlite.Store — the same "wire adapters and app-layer code together the
// way a real binary does" discipline internal/integration's own
// foundation_test.go/definitionplane_test.go/workplane_test.go already
// established. Unlike workplane_test.go's own real-git/gitworktree
// approach (appropriate for V3-12's own workspace-provisioning scope),
// this gate uses a scripted stub workspace provider (mirroring
// internal/app/runtime's own readyFixtureSQLite/commands_sqlite_test.go
// pattern) — V4's own scope is the runtime engine's node/job orchestration,
// not workspace provisioning, which V3-12 already gates exhaustively.
//
// Design (confirmed with the user before writing this file): two separate
// WorkflowRun lineages under one Project/WorkflowVersion, since a Run that
// is cancelled mid-flight and a Run that reaches its own real terminal
// candidate are mutually exclusive outcomes that can never share one
// lineage — Run A (WorkItem A) walks the full "golden path" (technical
// retry, rework/cycle, WAIT, APPROVAL, FORK/JOIN, scope-expansion BLOCKED
// -> reactivation) all the way to WorkflowRunVerifying (the real terminal
// CANDIDATE state — real completion/COMPLETED authority is V5-11's own
// job, deliberately out of V4-14's scope); Run B (WorkItem B, a separate
// TaskFamily) is driven to a deterministic WAIT barrier and then cancelled
// via the real V4-12B/V4-12C protocol, proving Run A is never affected.
//
// The identical scenario runs twice — once clean, once with a real crash
// (the pool's own context is cancelled while one ExecutionAttempt is
// genuinely claimed and RUNNING, mirroring V1-12/V2-12/V3-12's own
// "kill/restart" idiom rather than internal/adapters/sqlite/crashworker.go's
// heavier subprocess machinery, which V4-13's own StartupRecoveryScan
// already exists to make unnecessary here) followed by a real
// store.Close()/sqlite.Open() restart and V4-13's own recovery reaper
// resolving the orphaned attempt. Both runs make the exact same business
// decisions and must reach the exact same FinalDomainResult (self-
// comparison, no checked-in fixture — a crash-recovered run and a clean
// run cannot share one operational trace, since the crash run has real
// extra LOST/retry/recovery-decision events the clean run never produces);
// each run's own OperationalTrace is additionally compared byte-exact
// against its own checked-in golden fixture
// (testdata/golden/v4-runtime-clean.json,
// testdata/golden/v4-runtime-crash-recovery.json) — regenerated only by
// an explicit developer command (see regenerateRuntimeEngineGoldens
// below), never automatically by a test run.
package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	stdruntime "runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	appwork "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// --- graph fixture ---

// runtimeEngineDocument is the one WorkflowDocument this whole gate drives
// — every one of workflow.NodeType's ten values appears exactly once:
//
//	start(START) -> gate(ROUTER, single outcome, auto-resolves per ADR-026)
//	  -> implement(AGENT, outcomes rework/done, CyclePolicy MaxIterations=1)
//	       -- rework loops back to itself once (one rework round)
//	  -> wait_node(WAIT, SIGNAL) -> approval_node(APPROVAL)
//	  -> fork_node(FORK) -> test_a(COMMAND) \
//	                     -> gate_b(MACHINE_GATE) -> join_node(JOIN, ALL)
//	  -> scope_node(AGENT) -- first activation BLOCKED, requests repo-b;
//	     reactivated after approval+provisioning -> done -> end(END)
func runtimeEngineDocument(t *testing.T) workflow.WorkflowDocument {
	t.Helper()
	buildID := runtimeEngineAdapterBuild(t).ID()
	agent := func(key string, outcomes []string, cycle *workflow.CyclePolicy) workflow.Node {
		return workflow.Node{
			Key: key, Type: workflow.NodeAgent, Outcomes: outcomes, CyclePolicy: cycle,
			Agent: &workflow.AgentNodeConfig{
				ProfileRef: definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "re-agent-profile", VersionID: "re-agent-profile-v1"},
				PolicyRefs: []definition.DependencyPin{
					{Kind: definition.KindPolicy, DefinitionID: "re-attempt-policy", VersionID: "re-attempt-policy-v1"},
					{Kind: definition.KindPolicy, DefinitionID: "re-permission-policy", VersionID: "re-permission-policy-v1"},
				},
				AdapterBuildID: &buildID,
			},
		}
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "gate", Type: workflow.NodeRouter, Outcomes: []string{"continue"}},
			agent("implement", []string{"rework", "done"}, &workflow.CyclePolicy{MaxIterations: 1, EscalationOutcome: "done"}),
			{
				Key: "wait_node", Type: workflow.NodeWait, Outcomes: []string{"signaled"},
				Wait: &workflow.WaitNodeConfig{Mode: workflow.WaitModeSignal, SignalName: "re-signal"},
			},
			{
				Key: "approval_node", Type: workflow.NodeApproval, Outcomes: []string{"approved"},
				Approval: &workflow.ApprovalNodeConfig{
					AuthorizedRoles: []string{"operator"}, TimeoutSeconds: 3600, EscalationOutcome: "approved",
				},
			},
			{Key: "fork_node", Type: workflow.NodeFork, Outcomes: []string{"branch_a", "branch_b"}},
			{
				Key: "test_a", Type: workflow.NodeCommand, Outcomes: []string{"passed"},
				Command: &workflow.CommandNodeConfig{
					CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "re-command", VersionID: "re-command-v1"},
					PolicyRefs: []definition.DependencyPin{
						{Kind: definition.KindPolicy, DefinitionID: "re-attempt-policy", VersionID: "re-attempt-policy-v1"},
						{Kind: definition.KindPolicy, DefinitionID: "re-permission-policy", VersionID: "re-permission-policy-v1"},
					},
				},
			},
			{
				Key: "gate_b", Type: workflow.NodeMachineGate, Outcomes: []string{"passed"},
				MachineGate: &workflow.MachineGateNodeConfig{
					GateRef: definition.DependencyPin{Kind: definition.KindGate, DefinitionID: "re-gate", VersionID: "re-gate-v1"},
					PolicyRefs: []definition.DependencyPin{
						{Kind: definition.KindPolicy, DefinitionID: "re-attempt-policy", VersionID: "re-attempt-policy-v1"},
						{Kind: definition.KindPolicy, DefinitionID: "re-permission-policy", VersionID: "re-permission-policy-v1"},
					},
				},
			},
			{
				Key: "join_node", Type: workflow.NodeJoin, Outcomes: []string{"joined"},
				Join: &workflow.JoinNodeConfig{Mode: workflow.JoinModeAll},
			},
			agent("scope_node", []string{"done"}, nil),
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-gate", From: "start", Outcome: "next", To: "gate"},
			{Key: "gate-implement", From: "gate", Outcome: "continue", To: "implement"},
			{Key: "implement-rework", From: "implement", Outcome: "rework", To: "implement"},
			{Key: "implement-wait", From: "implement", Outcome: "done", To: "wait_node"},
			{Key: "wait-approval", From: "wait_node", Outcome: "signaled", To: "approval_node"},
			{Key: "approval-fork", From: "approval_node", Outcome: "approved", To: "fork_node"},
			{Key: "fork-a", From: "fork_node", Outcome: "branch_a", To: "test_a"},
			{Key: "fork-b", From: "fork_node", Outcome: "branch_b", To: "gate_b"},
			{Key: "a-join", From: "test_a", Outcome: "passed", To: "join_node"},
			{Key: "b-join", From: "gate_b", Outcome: "passed", To: "join_node"},
			{Key: "join-scope", From: "join_node", Outcome: "joined", To: "scope_node"},
			{Key: "scope-end", From: "scope_node", Outcome: "done", To: "end"},
		},
	}
}

// --- shared AdapterBuild fixture ---

// Audit finding (2026-09-08, V5-08 remediation): GC-INV-23 makes a pinned
// AdapterBuildVersion mandatory for an AGENT node —
// runAdmissionProbePhase (internal/app/runtime/admission.go) no longer
// treats a nil AdapterBuild as "nothing to verify, pass" for an AGENT
// executor. Both of this file's own tests dispatch "implement"/
// "scope_node" through real admission, so runtimeEngineDocument's own
// AGENT nodes must pin one, and ExecuteNodeHandler's own agentregistry
// must resolve a matching, non-drifting executor for it — mirroring
// internal/app/runtime's own sharedTestAdapterBuild
// (shared_admission_test.go): built once (a real temp executable, hashed,
// wrapped in a domainadapterbuild.Build) via os.MkdirTemp (never
// t.TempDir(), which would delete the file out from under a LATER test
// once the FIRST test that built it finishes and cleans up) no matter how
// many of this file's own tests call it.
var (
	runtimeEngineAdapterBuildOnce     sync.Once
	runtimeEngineAdapterBuildValue    domainadapterbuild.Build
	runtimeEngineAdapterRegistryValue *agentregistry.Registry
)

// runtimeEngineAdapterBuild returns the one real, non-drifting AdapterBuild
// this whole gate's own AGENT nodes pin.
func runtimeEngineAdapterBuild(t *testing.T) domainadapterbuild.Build {
	t.Helper()
	runtimeEngineAdapterBuildOnce.Do(func() { initRuntimeEngineAdapterBuild(t) })
	return runtimeEngineAdapterBuildValue
}

// runtimeEngineAgentRegistry returns the agentregistry.Registry whose own
// fake.AgentExecutor reports capabilities matching runtimeEngineAdapterBuild
// exactly — the replacement for every agentregistry.Empty() placeholder
// this file's own ExecuteNodeHandler wiring used before this fix.
func runtimeEngineAgentRegistry(t *testing.T) *agentregistry.Registry {
	t.Helper()
	runtimeEngineAdapterBuildOnce.Do(func() { initRuntimeEngineAdapterBuild(t) })
	return runtimeEngineAdapterRegistryValue
}

func initRuntimeEngineAdapterBuild(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("", "runtime-engine-adapter-build")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	executablePath := filepath.Join(dir, "runtime-engine-fixture-binary")
	if err := os.WriteFile(executablePath, []byte("runtime-engine-fixture-binary-v1"), 0o755); err != nil {
		t.Fatalf("write fixture executable: %v", err)
	}
	capabilities := ports.AgentCapabilities{
		Provider: ports.ProviderClaude, AdapterVersion: "claude-stream-json/v1", ProtocolVersion: "claude-stream-json/v1",
		SupportsStart: true, SupportsResume: true, SupportsCancel: true,
	}
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
	runtimeEngineAdapterBuildValue = build
	registry, err := agentregistry.New(context.Background(), &fake.AgentExecutor{CapabilitiesResult: capabilities})
	if err != nil {
		t.Fatalf("agentregistry.New: %v", err)
	}
	runtimeEngineAdapterRegistryValue = registry
}

// registerRuntimeEngineAdapterBuild inserts runtimeEngineAdapterBuild(t)
// into uow's own AdapterBuilds repository — schedule.go's own
// resolveExecutionProfile fails the whole scheduling transaction closed if
// a declared AdapterBuildID cannot be resolved. Safe to call more than once
// for the same uow (InsertIfAbsent is idempotent by ID).
func registerRuntimeEngineAdapterBuild(t *testing.T, uow ports.UnitOfWork) string {
	t.Helper()
	build := runtimeEngineAdapterBuild(t)
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, _, err := tx.AdapterBuilds().InsertIfAbsent(context.Background(), build)
		return err
	}); err != nil {
		t.Fatalf("register runtime engine adapter build: %v", err)
	}
	return build.ID()
}

func reCommand(idempotencyKey string, scope ports.CommandScope, commandType string) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "operator-1",
		CorrelationID: "corr-re", Scope: scope, RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type: commandType, RequestHash: "hash-" + idempotencyKey,
	}
}

func publishREAgentProfile(t *testing.T, uow ports.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	if _, err := definitions.CreateDefinition(ctx, uow, reCommand("re-def-agent", ports.InstallationScope(), "CreateDefinition"), definitions.CreateDefinitionRequest{
		DefinitionID: "re-agent-profile", Kind: definition.KindAgentProfile, Scope: definition.GlobalScope(), Name: "re agent profile",
	}); err != nil {
		t.Fatalf("CreateDefinition(agent profile): %v", err)
	}
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, reCommand("re-pub-agent", ports.InstallationScope(), "PublishDefinitionVersion"), definitions.PublishDefinitionVersionRequest{
		DefinitionID: "re-agent-profile", Kind: definition.KindAgentProfile,
		Compile: func() (definition.VersionFields, error) {
			return agentprofile.Compile(
				agentprofile.AgentProfileDefinition{ID: "re-agent-profile", Fields: definition.Fields{
					Kind: definition.KindAgentProfile, Scope: definition.GlobalScope(), Name: "re agent profile", Status: definition.StatusDraft, Version: 1,
				}},
				agentprofile.PublishRequest{
					VersionID: "re-agent-profile-v1", VersionNumber: 1, SchemaVersion: 1,
					Document: agentprofile.AgentProfileDocument{
						ProviderKey: "fake-provider", Model: "fake-model", ToolRefs: []string{"read_file"},
						ContextPolicyRef: definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: "re-context-policy", VersionID: "re-context-policy-v1"},
						Compatibility:    agentprofile.Compatibility{OS: []string{"linux"}},
						Budget:           agentprofile.Budget{MaxTokens: 4096},
					},
					PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			)
		},
	}); err != nil {
		t.Fatalf("PublishDefinitionVersion(agent profile): %v", err)
	}
}

func publishREPolicy(t *testing.T, uow ports.UnitOfWork, definitionID, versionID string, doc policy.PolicyDocument) {
	t.Helper()
	ctx := context.Background()
	if _, err := definitions.CreateDefinition(ctx, uow, reCommand("re-def-"+definitionID, ports.InstallationScope(), "CreateDefinition"), definitions.CreateDefinitionRequest{
		DefinitionID: definitionID, Kind: definition.KindPolicy, Scope: definition.GlobalScope(), Name: "re policy " + definitionID,
	}); err != nil {
		t.Fatalf("CreateDefinition(%s): %v", definitionID, err)
	}
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, reCommand("re-pub-"+versionID, ports.InstallationScope(), "PublishDefinitionVersion"), definitions.PublishDefinitionVersionRequest{
		DefinitionID: definitionID, Kind: definition.KindPolicy,
		Compile: func() (definition.VersionFields, error) {
			return policy.Compile(
				policy.PolicyDefinition{ID: policy.PolicyDefinitionID(definitionID), Fields: definition.Fields{
					Kind: definition.KindPolicy, Scope: definition.GlobalScope(), Name: "re policy", Status: definition.StatusDraft, Version: 1,
				}},
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

func publishRECommand(t *testing.T, uow ports.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	if _, err := definitions.CreateDefinition(ctx, uow, reCommand("re-def-command", ports.InstallationScope(), "CreateDefinition"), definitions.CreateDefinitionRequest{
		DefinitionID: "re-command", Kind: definition.KindCommand, Scope: definition.GlobalScope(), Name: "re command",
	}); err != nil {
		t.Fatalf("CreateDefinition(command): %v", err)
	}
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, reCommand("re-pub-command", ports.InstallationScope(), "PublishDefinitionVersion"), definitions.PublishDefinitionVersionRequest{
		DefinitionID: "re-command", Kind: definition.KindCommand,
		Compile: func() (definition.VersionFields, error) {
			return command.Compile(
				command.CommandDefinition{ID: "re-command", Fields: definition.Fields{
					Kind: definition.KindCommand, Scope: definition.GlobalScope(), Name: "re command", Status: definition.StatusDraft, Version: 1,
				}},
				command.PublishRequest{
					VersionID: "re-command-v1", VersionNumber: 1, SchemaVersion: 1,
					Document: command.CommandDocument{
						Executable:          command.ExecutableRef{OwnerVersionID: "re-skill-v1", ResourceKey: "scripts/run.sh", ContentHash: "sha256:re-command"},
						Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run.sh"}},
						CwdRepositoryTarget: "primary",
						Compatibility:       command.Compatibility{OS: []string{"linux"}},
						NetworkAccess:       command.NetworkAccessNone,
						TimeoutSeconds:      60,
						Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 20},
					},
					PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			)
		},
	}); err != nil {
		t.Fatalf("PublishDefinitionVersion(command): %v", err)
	}
}

func publishREGate(t *testing.T, uow ports.UnitOfWork) {
	t.Helper()
	ctx := context.Background()
	if _, err := definitions.CreateDefinition(ctx, uow, reCommand("re-def-gate", ports.InstallationScope(), "CreateDefinition"), definitions.CreateDefinitionRequest{
		DefinitionID: "re-gate", Kind: definition.KindGate, Scope: definition.GlobalScope(), Name: "re gate",
	}); err != nil {
		t.Fatalf("CreateDefinition(gate): %v", err)
	}
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, reCommand("re-pub-gate", ports.InstallationScope(), "PublishDefinitionVersion"), definitions.PublishDefinitionVersionRequest{
		DefinitionID: "re-gate", Kind: definition.KindGate,
		Compile: func() (definition.VersionFields, error) {
			return gate.Compile(
				gate.GateDefinition{ID: "re-gate", Fields: definition.Fields{
					Kind: definition.KindGate, Scope: definition.GlobalScope(), Name: "re gate", Status: definition.StatusDraft, Version: 1,
				}},
				gate.PublishRequest{
					VersionID: "re-gate-v1", VersionNumber: 1, SchemaVersion: 1,
					Document: gate.GateDocument{
						CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "re-command", VersionID: "re-command-v1"},
						Criteria:   []gate.Criterion{{Name: "exit-code-zero", EvidenceKey: "exitCode"}},
					},
					PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			)
		},
	}); err != nil {
		t.Fatalf("PublishDefinitionVersion(gate): %v", err)
	}
}

// publishREWorkflowVersion mirrors internal/app/runtime's own
// publishWorkflowVersionDocument test helper exactly (workflow.Compile +
// tx.Definitions().PublishWorkflowVersion — workflow versions bypass the
// generic definitions.PublishDefinitionVersion pipeline entirely, unlike
// every other definition kind this file publishes above).
func publishREWorkflowVersion(t *testing.T, uow ports.UnitOfWork, projectID string) workflow.WorkflowVersion {
	t.Helper()
	ctx := context.Background()
	registerRuntimeEngineAdapterBuild(t, uow)
	pid := project.ProjectID(projectID)
	def := workflow.WorkflowDefinition{
		ID: "re-workflow", ProjectID: &pid, Name: "re workflow", Status: workflow.DefinitionActive, Version: 1,
	}
	candidate, err := workflow.Compile(def, workflow.PublishRequest{
		VersionID: "re-workflow-v1", VersionNumber: 1, Document: runtimeEngineDocument(t),
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "1", Hash: "sha256:re-dependency-1"},
		}},
		PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("compile re-workflow-v1: %v", err)
	}
	var published workflow.WorkflowVersion
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		p, err := tx.Definitions().PublishWorkflowVersion(ctx, def, candidate)
		published = p
		return err
	}); err != nil {
		t.Fatalf("publish re-workflow-v1: %v", err)
	}
	return published
}

// publishREDefinitions publishes every definition runtimeEngineDocument's
// own DependencyPins reference: the AgentProfile+its ContextPolicy, the
// Command+the Gate node's own CommandRef (gate_b evaluates through the
// identical fake-executor envelope as AGENT/COMMAND — see this file's own
// top doc comment — so no Command/Gate node itself is ever actually
// invoked as a gate.Compile-style criterion check; the Gate document still
// has to compile and resolve like any other real DependencyPin), the
// Attempt/Permission policies, and finally the WorkflowVersion itself.
func publishREDefinitions(t *testing.T, uow ports.UnitOfWork, projectID string) workflow.WorkflowVersion {
	t.Helper()
	publishREPolicy(t, uow, "re-context-policy", "re-context-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context:  &policy.ContextRules{Selector: []string{"**"}, Budget: policy.ContextBudget{MaxTokens: 4096}},
	})
	publishREAgentProfile(t, uow)
	publishREPolicy(t, uow, "re-attempt-policy", "re-attempt-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryAttempt,
		Attempt:  &policy.AttemptRules{MaxAttempts: 3, BackoffSeconds: 1, TimeoutSeconds: 600},
	})
	publishREPolicy(t, uow, "re-permission-policy", "re-permission-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryPermission,
		Permission: &policy.PermissionRules{
			IsolationTier: policy.IsolationTierEnforcedIsolated, GrantedCapabilities: []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"},
		},
	})
	publishRECommand(t, uow)
	publishREGate(t, uow)
	return publishREWorkflowVersion(t, uow, projectID)
}

// --- scripted node executor ---

// scriptedNodeExecutor implements ports.NodeExecutor by looking up the
// real NodeRun a NodeExecutionRequest names (its own NodeKey/Iteration/
// ReactivationReason are all durable, real state — never guessed) and
// returning a fixed, deterministic outcome per node, mirroring
// runtimeEngineDocument's own doc comment. "implement" ends the graph's
// one rework round on Iteration 0 (rework loops back to itself once,
// Iteration becomes 1, then this returns "done"); "scope_node" reports
// BLOCKED requesting repo-b on its first activation (ReactivationReason
// empty) and "done" once V4-12A's own reactivation has run (a fresh
// NodeRun row, same Iteration, ReactivationReason="SCOPE_EXPANDED").
// blockNodeKey, when non-empty, is V4-14's own "inject crash" mechanism
// (docs/design/06-v4-runtime-engine.md V4-14, "chạy graph... qua
// restart"): the FIRST Execute call for that exact NodeKey blocks on ctx
// being cancelled instead of returning any scripted result at all,
// simulating a worker that genuinely crashed mid-execution — the pool's
// own outer context cancellation (this test's cancelPool) propagates into
// ExecuteNodeHandler's own derived-deadline context, so Execute returns
// ctx.Err() for a reason OTHER than its own deadline, which
// ExecuteNodeHandler's own doc comment says leaves the Attempt RUNNING,
// un-finalized — a real, durable orphaned attempt, never a fabricated one.
// A restart against the SAME store, followed by a FRESH scriptedNodeExecutor
// with blockNodeKey left empty, lets V4-13's own RecoveryReaperHandler (via
// StartupRecoveryScan) discover and retry it for real, exactly the
// "inject crash" + "restart" combination this task's own text names.
type scriptedNodeExecutor struct {
	uow          ports.UnitOfWork
	blockNodeKey string
}

func (e *scriptedNodeExecutor) Execute(ctx context.Context, req ports.NodeExecutionRequest) (ports.NodeExecutionResult, error) {
	var nodeRun runtimedomain.NodeRun
	if err := e.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		nodeRun, err = tx.Runtime().GetNodeRun(ctx, req.NodeRunID)
		return err
	}); err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("scriptedNodeExecutor: load node run %s: %w", req.NodeRunID, err)
	}
	if e.blockNodeKey != "" && nodeRun.NodeKey == e.blockNodeKey {
		<-ctx.Done()
		return ports.NodeExecutionResult{}, ctx.Err()
	}
	switch nodeRun.NodeKey {
	case "implement":
		if nodeRun.Iteration == 0 {
			return ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "rework"}, nil
		}
		return ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "done"}, nil
	case "test_a", "gate_b":
		return ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "passed"}, nil
	case "scope_node":
		if nodeRun.ReactivationReason != "" {
			return ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "done"}, nil
		}
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptBlocked, TerminationReason: runtimedomain.TerminationReasonScopeExpansionRequired,
			RequestedScopeExpansion: &runtimedomain.ScopeExpansionProposal{
				RequestedGrants: []runtimedomain.ScopeGrantProposal{{
					RepositoryID: "repo-b", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"**"}, Reason: "scope_node needs repo-b",
				}},
				Reason: "scope_node needs repo-b",
			},
		}, nil
	default:
		return ports.NodeExecutionResult{}, fmt.Errorf("scriptedNodeExecutor: no script for node %s", nodeRun.NodeKey)
	}
}

// --- pool wiring ---

// registerRuntimeEngineHandlers wires every job handler a full run of
// runtimeEngineDocument needs into one registry: the four runtime-engine
// job kinds every node type dispatches through (Scheduler/ADVANCE_RUN,
// NodeSchedulingHandler/SCHEDULE_NODE_RUN, ExecuteNodeHandler/EXECUTE_NODE
// driving the scripted executor above), WAIT/APPROVAL's own timeout
// handlers (registered even though this gate always resolves both by real
// signal/approval before any timeout fires, matching production wiring
// rather than omitting a handler this graph's own document could in
// principle dispatch), V4-12A's own scope-expansion pair
// (RequestScopeExpansionHandler enqueued by a real BLOCKED Attempt,
// ScopeExpansionReconcileHandler self-rescheduling until the expanded
// WorkspaceSet is READY), workspaceprovision's own handler (the real
// provisioning job RequestScopeExpansion's own approval enqueues — a
// scripted stub workspace.Provider, never real Git, per this file's own
// top doc comment), V4-12B's own CancelRunCoordinatorHandler, and V4-13's
// own RecoveryReaperHandler (the real interruptions/workspaces/recovery
// dependencies are the SAME *sqlite.Store passed in — it satisfies all
// three spike-era interfaces structurally, exactly RecoveryReaperHandler's
// own doc comment already establishes).
func registerRuntimeEngineHandlers(
	t *testing.T, store *sqlite.Store, uow ports.UnitOfWork, handlerIDs idsource.Source, executor ports.NodeExecutor,
) *workerpool.Registry {
	t.Helper()
	registry := workerpool.NewRegistry()
	registry.Register(runtime.AdvanceRunJobKind, runtime.NewScheduler(uow, handlerIDs))
	registry.Register(runtime.ScheduleNodeRunJobKind, runtime.NewNodeSchedulingHandler(uow, handlerIDs, fake.NewRuntimeExecutionConfigProvider()))
	registry.Register(runtime.ExecuteNodeJobKind, runtime.NewExecuteNodeHandler(uow, handlerIDs, executor, clock.System{}, fake.IsolationEnforcementChecker{}, runtimeEngineAgentRegistry(t)))
	registry.Register(runtime.WaitTimerJobKind, runtime.NewWaitTimeoutHandler(uow, handlerIDs))
	registry.Register(runtime.ApprovalTimerJobKind, runtime.NewApprovalTimeoutHandler(uow, handlerIDs))
	registry.Register(runtime.RequestScopeExpansionJobKind, runtime.NewRequestScopeExpansionHandler(uow, handlerIDs))
	registry.Register(appwork.ScopeExpansionReconcileJobKind, runtime.NewScopeExpansionReconcileHandler(uow, handlerIDs))
	registry.Register(appwork.WorkspaceProvisionJobKind, workspaceprovision.New(uow, handlerIDs, &scriptedWorkspaceProvider{}))
	registry.Register(runtime.CancelRunCoordinatorJobKind, runtime.NewCancelRunCoordinatorHandler(uow, handlerIDs))
	registry.Register(runtime.RecoveryReaperJobKind, runtime.NewRecoveryReaperHandler(uow, handlerIDs, clock.System{}, store, store, store))
	return registry
}

func newRuntimeEnginePool(t *testing.T, store *sqlite.Store, registry *workerpool.Registry) *workerpool.Pool {
	t.Helper()
	pool, err := workerpool.New(store, registry, workerpool.Config{
		Concurrency: 1, Owner: "runtime-engine-gate", LeaseTTL: 2 * time.Second,
		HeartbeatEvery: 200 * time.Millisecond, PollInterval: 10 * time.Millisecond,
		ShutdownGrace: 2 * time.Second, RecoveryInterval: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("workerpool.New: %v", err)
	}
	return pool
}

// scriptedWorkspaceProvider mirrors stubProvider
// (internal/app/runtime/commands_test.go) — this gate's own scope is the
// runtime engine's node/job orchestration, not workspace provisioning
// (V3-12 already gates that exhaustively against real Git), so every
// RepositoryWorkspace this scenario ever provisions gets an identical
// scripted handle/revision, keyed only by enough uniqueness
// (WorkspaceGeneration) to satisfy CaptureRevision's own real callers.
type scriptedWorkspaceProvider struct{}

func (scriptedWorkspaceProvider) Provision(_ context.Context, spec ports.ProvisionSpec) (ports.WorkspaceHandle, error) {
	handle, err := ports.NewWorkspaceHandle("handle-" + string(spec.RepositoryID))
	if err != nil {
		return ports.WorkspaceHandle{}, err
	}
	return handle, nil
}
func (scriptedWorkspaceProvider) Inspect(context.Context, ports.WorkspaceHandle) (ports.WorkspaceInspection, error) {
	return ports.WorkspaceInspection{}, errors.New("scriptedWorkspaceProvider: Inspect must not be called")
}
func (scriptedWorkspaceProvider) CaptureRevision(_ context.Context, handle ports.WorkspaceHandle) (workspace.Revision, error) {
	repositoryID := strings.TrimPrefix(handle.String(), "handle-")
	return workspace.Revision{
		RepositoryID: project.RepositoryID(repositoryID),
		VCSObjectID:  "cafebabecafebabecafebabecafebabecafebabe", WorkspaceGeneration: 1,
	}, nil
}
func (scriptedWorkspaceProvider) Diff(context.Context, ports.WorkspaceHandle, workspace.Revision) (ports.WorkspaceDiff, error) {
	return ports.WorkspaceDiff{}, errors.New("scriptedWorkspaceProvider: Diff must not be called")
}
func (scriptedWorkspaceProvider) Release(context.Context, ports.WorkspaceHandle) error {
	return errors.New("scriptedWorkspaceProvider: Release must not be called")
}

// --- Project/repository/WorkItem fixture ---

// seedREProject creates one Project and drives repositoryIDs all the way
// to ACTIVE (RegisterRepository, then a direct CAS through
// REGISTERING->PROBING->ACTIVE — no real repository probe executor exists
// in this codebase, mirroring internal/app/runtime's own identical
// seedActiveRepositorySQLite test helper).
func seedREProject(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID string, repositoryIDs ...string) {
	t.Helper()
	ctx := context.Background()
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID})
		return err
	}); err != nil {
		t.Fatalf("seed project %s: %v", projectID, err)
	}
	for _, repositoryID := range repositoryIDs {
		if _, err := catalog.RegisterRepository(ctx, uow, ids, reCommand("re-reg-"+repositoryID, ports.ProjectScope(projectID), "RegisterRepository"), catalog.RegisterRepositoryRequest{
			RepositoryID: repositoryID, ProjectID: projectID, Name: repositoryID,
			RemoteLocator: "https://example.invalid/" + repositoryID + ".git", DefaultRef: "main",
		}); err != nil {
			t.Fatalf("RegisterRepository(%s): %v", repositoryID, err)
		}
		if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			probing, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
				RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
				NextStatus: project.RepositoryProbing,
			})
			if err != nil {
				return err
			}
			_, err = tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
				RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: probing.Version,
				NextStatus: project.RepositoryActive,
			})
			return err
		}); err != nil {
			t.Fatalf("drive repository %s to ACTIVE: %v", repositoryID, err)
		}
	}
}

// createREWorkItem creates a real ROOT WorkItem (a separate TaskFamily each
// call, mirroring internal/app/work.CreateRootWorkItem's own contract),
// waits for its own initial WorkspaceSet to reach READY through the real
// registered workspaceprovision handler, then forces WorkItem
// BACKLOG->READY directly (no real command reaches READY yet — see
// internal/app/runtime's own readyFixture/readyFixtureSQLite doc comment
// for the identical, already-established reason).
func createREWorkItem(t *testing.T, store *sqlite.Store, uow ports.UnitOfWork, ids idsource.Source, projectID, title, repositoryID string) appwork.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	result, err := appwork.CreateRootWorkItem(ctx, uow, ids, reCommand("re-root-"+title, ports.ProjectScope(projectID), "CreateRootWorkItem"), appwork.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: title, InitialScope: []appwork.ScopeGrantRequest{{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite), PathScopes: []string{"**"}, Reason: "root task",
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem(%s): %v", title, err)
	}
	for _, provisioned := range result.ProvisionedRepositories {
		waitForRuntimeEngineJobState(t, store, provisioned.ProvisionJobID, ports.JobSucceeded)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: result.WorkItemID, ExpectedStatus: workdomain.WorkItemBacklog, ExpectedVersion: 1,
			NextStatus: workdomain.WorkItemReady,
		})
		return err
	}); err != nil {
		t.Fatalf("force work item %s READY: %v", result.WorkItemID, err)
	}
	return result
}

func waitForRuntimeEngineJobState(t *testing.T, store *sqlite.Store, jobID string, want ports.JobState) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(8 * time.Second)
	var last ports.JobState
	for time.Now().Before(deadline) {
		state, err := store.LoadDurableJobState(ctx, ports.JobID(jobID))
		if err == nil {
			last = state
			if state == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("durable job %s did not reach state %s within the deadline; last observed = %s", jobID, want, last)
}

func waitForRuntimeEngineNodeRunState(t *testing.T, uow ports.UnitOfWork, runID, nodeKey string, want runtimedomain.NodeRunState) runtimedomain.NodeRun {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(8 * time.Second)
	var last runtimedomain.NodeRun
	var found bool
	for time.Now().Before(deadline) {
		var nodeRuns []runtimedomain.NodeRun
		if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			nodeRuns, err = tx.Runtime().ListNodeRunsForRun(ctx, runID)
			return err
		}); err == nil {
			for _, nr := range nodeRuns {
				if nr.NodeKey == nodeKey && (nr.State == want || !found) {
					last, found = nr, true
					if nr.State == want {
						return last
					}
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("node %s on run %s did not reach state %s within the deadline; last observed = %+v", nodeKey, runID, want, last)
	return runtimedomain.NodeRun{}
}

// driveRERunToScopeExpansionApproval polls until nodeKey's own NodeRun for
// runID reaches BLOCKED, finds the real ScopeExpansionOrigin
// RequestScopeExpansionHandler created for its own BLOCKED Attempt, then
// retries ApproveScopeExpansion until it succeeds (the real
// WorkItemScopeExpansionRequest row RequestScopeExpansionHandler's own
// real appwork.RequestScopeExpansion call creates might not exist the
// instant the NodeRun itself flips BLOCKED — the job dispatch is a
// separate, asynchronous durable job).
func driveRERunToScopeExpansionApproval(t *testing.T, store *sqlite.Store, uow ports.UnitOfWork, ids idsource.Source, runID, nodeKey string) appwork.ApproveScopeExpansionResult {
	t.Helper()
	ctx := context.Background()
	waitForRuntimeEngineNodeRunState(t, uow, runID, nodeKey, runtimedomain.NodeRunBlocked)

	var attemptID string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var attempts []runtimedomain.ExecutionAttempt
		if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
			return err
		}); err == nil {
			for _, a := range attempts {
				if a.State == runtimedomain.ExecutionAttemptBlocked {
					var nr runtimedomain.NodeRun
					if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
						var err error
						nr, err = tx.Runtime().GetNodeRun(ctx, string(a.NodeRunID))
						return err
					}); err == nil && nr.NodeKey == nodeKey {
						attemptID = string(a.ID)
					}
				}
			}
		}
		if attemptID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if attemptID == "" {
		t.Fatalf("no BLOCKED ExecutionAttempt found for node %s on run %s within the deadline", nodeKey, runID)
	}

	var requestID string
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var origin runtimedomain.ScopeExpansionOrigin
		err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			origin, err = tx.Runtime().GetScopeExpansionOriginByAttemptID(ctx, attemptID)
			return err
		})
		if err == nil && origin.RequestID != "" {
			requestID = origin.RequestID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requestID == "" {
		t.Fatalf("no ScopeExpansionOrigin found for attempt %s within the deadline", attemptID)
	}

	deadline = time.Now().Add(8 * time.Second)
	var lastErr error
	var result appwork.ApproveScopeExpansionResult
	var approved bool
	for time.Now().Before(deadline) {
		result, lastErr = appwork.ApproveScopeExpansion(ctx, uow, ids, reCommand("re-expand-approve-"+requestID, ports.InstallationScope(), "ApproveScopeExpansion"), appwork.ApproveScopeExpansionRequest{RequestID: requestID})
		if lastErr == nil {
			approved = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !approved {
		t.Fatalf("ApproveScopeExpansion(%s) never succeeded within the deadline, last error = %v", requestID, lastErr)
	}
	for _, provisioned := range result.ProvisionedRepositories {
		waitForRuntimeEngineJobState(t, store, provisioned.ProvisionJobID, ports.JobSucceeded)
	}
	return result
}

// driveRERunPastWait polls until nodeKey's own NodeRun for runID reaches
// WAITING, finds the real WaitRegistration it produced, then signals it.
func driveRERunPastWait(t *testing.T, store *sqlite.Store, uow ports.UnitOfWork, ids idsource.Source, runID, nodeKey, signalKey string) {
	t.Helper()
	ctx := context.Background()
	nodeRun := waitForRuntimeEngineNodeRunState(t, uow, runID, nodeKey, runtimedomain.NodeRunWaiting)
	registration, err := store.GetWaitRegistrationByNodeRunID(ctx, string(nodeRun.ID))
	if err != nil {
		t.Fatalf("GetWaitRegistrationByNodeRunID(%s): %v", nodeRun.ID, err)
	}
	if _, err := runtime.SignalWait(ctx, uow, ids, reCommand("re-signal-"+runID+"-"+nodeKey, ports.InstallationScope(), "SignalWait"), runtime.SignalWaitRequest{
		RunID: runID, WaitRegistrationID: string(registration.ID), SignalKey: signalKey,
	}); err != nil {
		t.Fatalf("SignalWait(%s): %v", registration.ID, err)
	}
}

// driveRERunPastApproval polls until nodeKey's own NodeRun for runID
// reaches WAITING, finds the real ApprovalRequest it produced, then
// resolves it "approved".
func driveRERunPastApproval(t *testing.T, store *sqlite.Store, uow ports.UnitOfWork, ids idsource.Source, runID, nodeKey string) {
	t.Helper()
	ctx := context.Background()
	nodeRun := waitForRuntimeEngineNodeRunState(t, uow, runID, nodeKey, runtimedomain.NodeRunWaiting)
	request, err := store.GetApprovalRequestByNodeRunID(ctx, string(nodeRun.ID))
	if err != nil {
		t.Fatalf("GetApprovalRequestByNodeRunID(%s): %v", nodeRun.ID, err)
	}
	cmd := reCommand("re-approve-"+runID+"-"+nodeKey, ports.InstallationScope(), "ResolveApproval")
	cmd.ActorRoles = []string{"operator"}
	if _, err := runtime.ResolveApproval(ctx, uow, ids, cmd, runtime.ResolveApprovalRequest{
		RunID: runID, ApprovalRequestID: string(request.ID), Outcome: "approved", Reason: "gate approval",
	}); err != nil {
		t.Fatalf("ResolveApproval(%s): %v", request.ID, err)
	}
}

// runtimeEngineReconcileDeadline covers scope-expansion reconciliation's
// own real self-reschedule backoff (scopeExpansionReconcileBaseBackoffSeconds
// = 5s, internal/app/runtime/scope_expansion.go) — a NodeRun waiting on
// reactivation genuinely needs to survive at least one such cycle for the
// real ScopeExpansionReconcileHandler (registered in the real Pool this
// gate always runs) to notice the WorkspaceSet reached READY.
const runtimeEngineReconcileDeadline = 15 * time.Second

func waitForRuntimeEngineRunState(t *testing.T, uow ports.UnitOfWork, runID string, want runtimedomain.WorkflowRunState) runtimedomain.WorkflowRun {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(runtimeEngineReconcileDeadline)
	var last runtimedomain.WorkflowRun
	for time.Now().Before(deadline) {
		if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			last, err = tx.Runtime().GetWorkflowRun(ctx, runID)
			return err
		}); err == nil && last.State == want {
			return last
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach state %s within the deadline; last observed = %+v", runID, want, last)
	return runtimedomain.WorkflowRun{}
}

// --- top-level scenario ---

// runtimeEngineFinalResult is V4-14's own FinalDomainResult (confirmed
// with the user): the semantic business outcome of one full scenario run —
// deliberately NEVER Attempt count or any recovery-specific signal (a
// legitimate crash+restart produces a real extra retry Attempt the clean
// run never does), so a clean run and a crash-recovered run of the exact
// same fixture/decisions can be asserted equal on this alone.
type runtimeEngineFinalResult struct {
	RunAState            string
	WorkItemAStatus      string
	RunASharedStateJSON  string
	RunAManifestRevision uint64
	RunAOutcomes         map[string]string // "nodeKey@iteration" -> selectedOutcome
	RunAJoinVerdict      string
	RunAOpenBlockerTypes []string

	RunBState            string
	WorkItemBStatus      string
	RunBOpenBlockerTypes []string
}

func diffRuntimeEngineFinalResult(clean, recovered runtimeEngineFinalResult) string {
	var diffs []string
	add := func(field string, a, b any) {
		if fmt.Sprint(a) != fmt.Sprint(b) {
			diffs = append(diffs, fmt.Sprintf("%s: clean=%v recovered=%v", field, a, b))
		}
	}
	add("RunAState", clean.RunAState, recovered.RunAState)
	add("WorkItemAStatus", clean.WorkItemAStatus, recovered.WorkItemAStatus)
	add("RunASharedStateJSON", clean.RunASharedStateJSON, recovered.RunASharedStateJSON)
	add("RunAManifestRevision", clean.RunAManifestRevision, recovered.RunAManifestRevision)
	add("RunAJoinVerdict", clean.RunAJoinVerdict, recovered.RunAJoinVerdict)
	add("RunAOutcomes", fmt.Sprint(clean.RunAOutcomes), fmt.Sprint(recovered.RunAOutcomes))
	add("RunAOpenBlockerTypes", fmt.Sprint(clean.RunAOpenBlockerTypes), fmt.Sprint(recovered.RunAOpenBlockerTypes))
	add("RunBState", clean.RunBState, recovered.RunBState)
	add("WorkItemBStatus", clean.WorkItemBStatus, recovered.WorkItemBStatus)
	add("RunBOpenBlockerTypes", fmt.Sprint(clean.RunBOpenBlockerTypes), fmt.Sprint(recovered.RunBOpenBlockerTypes))
	return strings.Join(diffs, "\n")
}

// runRuntimeEngineScenario drives runtimeEngineDocument's own Run A (golden
// path to VERIFYING) and Run B (cancellation path) under a real
// workerpool.Pool against a real *sqlite.Store — see this file's own top
// doc comment for the full design. When injectCrash is true, Run A's own
// "test_a" fork branch is deliberately left RUNNING and orphaned by
// cancelling the pool mid-execution, followed by a real store close/reopen
// restart and V4-13's own StartupRecoveryScan/RecoveryReaperHandler
// resolving it for real — both variants make the exact same business
// decisions and must return an equal runtimeEngineFinalResult.
// runtimeEngineScenarioResult bundles the two comparisons V4-14's own
// hybrid determinism design (confirmed with the user) needs: Final is
// compared BETWEEN the clean and crash-recovered runs (self-comparison, no
// checked-in fixture — see runtimeEngineFinalResult's own doc comment);
// Trace is compared, byte-exact, against EACH variant's own separate
// checked-in golden file (a crash-recovered run's own trace necessarily
// carries real extra LOST/retry/recovery-decision events the clean run
// never produces, so the two traces are never compared to each other).
type runtimeEngineScenarioResult struct {
	Final runtimeEngineFinalResult
	Trace []canonicalTraceEvent
}

func runRuntimeEngineScenario(t *testing.T, injectCrash bool) runtimeEngineScenarioResult {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-runtime-engine.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	uow := sqlite.NewUnitOfWork(store)
	testIDs := idsource.NewSequential("t")

	seedREProject(t, uow, testIDs, "re-project", "repo-a", "repo-b")
	version := publishREDefinitions(t, uow, "re-project")

	executor := &scriptedNodeExecutor{uow: uow}
	if injectCrash {
		executor.blockNodeKey = "test_a"
	}
	// handlerIDs is its own Sequential source, deliberately SEPARATE from
	// testIDs above (never shared — idsource.Sequential's own doc comment:
	// "not safe for concurrent use") — but still fully deterministic, not
	// idsource.Random{}. A real CI run of this exact test caught why that
	// matters: dispatchForkBranches (V4-10) creates BOTH fork branches' own
	// SCHEDULE_NODE_RUN jobs in the SAME transaction (identical
	// created_at), so ClaimJob's own tie-break falls to raw id — with
	// Random ids that tie-break, and therefore which branch this gate's
	// own Concurrency:1 pool claims (and, for injectCrash, blocks on)
	// first, was a real per-run coin flip that happened to land the same
	// way every local rerun but not on a different CI runner, breaking the
	// golden trace's own byte-exact reproducibility. Two independent
	// Sequential sources (Concurrency:1 means the pool's own worker
	// goroutine is never concurrent with itself, only ever with this
	// test's own foreground goroutine — a distinct variable, so no shared
	// mutable state race) make that tie-break deterministic instead.
	handlerIDs := idsource.NewSequential("h")
	registry := registerRuntimeEngineHandlers(t, store, uow, handlerIDs, executor)
	pool := newRuntimeEnginePool(t, store, registry)
	poolCtx, cancelPool := context.WithCancel(ctx)
	poolErr := make(chan error, 1)
	go func() { poolErr <- pool.Run(poolCtx) }()
	poolStopped := false
	stopPool := func() {
		if poolStopped {
			return
		}
		poolStopped = true
		cancelPool()
		<-poolErr
	}
	// A closure over stopPool/store (not a snapshot of their current
	// values) — if injectCrash reassigns both mid-function for the
	// restart, this still stops/closes whichever pool/store is current at
	// return time, not the original pair.
	defer func() {
		stopPool()
		_ = store.Close()
	}()

	workItemA := createREWorkItem(t, store, uow, testIDs, "re-project", "Run A golden path", "repo-a")
	startA, err := runtime.StartWorkflowRun(ctx, uow, testIDs, reCommand("re-start-a", ports.ProjectScope("re-project"), "StartWorkflowRun"), runtime.StartWorkflowRunRequest{
		ProjectID: "re-project", WorkItemID: workItemA.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun(A): %v", err)
	}
	runA := startA.RunID

	driveRERunPastWait(t, store, uow, testIDs, runA, "wait_node", "re-signal")

	// Run B's ENTIRE setup, including reaching its own deterministic
	// barrier, must complete BEFORE Run A's own approval resolves below:
	// resolving approval_node dispatches fork_node's branches, and when
	// injectCrash is true test_a's own scripted executor then hangs
	// forever — with this gate's own Concurrency:1 pool (a single worker
	// goroutine), a hang started any earlier would permanently starve
	// Run B's own ADVANCE_RUN/SCHEDULE_NODE_RUN jobs, since nothing else
	// could ever get a turn until the crash/restart below.
	workItemB := createREWorkItem(t, store, uow, testIDs, "re-project", "Run B cancellation path", "repo-a")
	startB, err := runtime.StartWorkflowRun(ctx, uow, testIDs, reCommand("re-start-b", ports.ProjectScope("re-project"), "StartWorkflowRun"), runtime.StartWorkflowRunRequest{
		ProjectID: "re-project", WorkItemID: workItemB.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun(B): %v", err)
	}
	runB := startB.RunID
	// Deterministic barrier: wait_node genuinely reaches WAITING (a real
	// WaitRegistration exists) — never signalled, so Run B is provably
	// still mid-flight, not coincidentally already finished, at the exact
	// moment CancelRun commits (below, after the shared restart point).
	waitForRuntimeEngineNodeRunState(t, uow, runB, "wait_node", runtimedomain.NodeRunWaiting)

	driveRERunPastApproval(t, store, uow, testIDs, runA, "approval_node")

	if injectCrash {
		// fork_node has already dispatched both branches by the time
		// approval_node resolved above; test_a's own scripted executor is
		// blocked on ctx.Done() (never returns a result), so its own
		// ExecutionAttempt is genuinely RUNNING and stuck the moment this
		// observes it.
		waitForRuntimeEngineAttemptRunning(t, uow, runA, "test_a")

		stopPool() // cancels the pool's own context — the blocked Execute
		// call returns ctx.Err() for a reason OTHER than its own deadline,
		// so ExecuteNodeHandler leaves the Attempt RUNNING, never
		// finalized (see scriptedNodeExecutor's own blockNodeKey doc
		// comment) — a real, durable orphaned attempt.

		// Let the claimed EXECUTE_NODE job's own lease genuinely pass its
		// LeaseTTL (newRuntimeEnginePool's own 2s) before restarting, so
		// V4-13's own ListOrphanedRunningExecutionAttempts finds it
		// orphaned by a real expired lease, not a fabricated one.
		time.Sleep(2500 * time.Millisecond)

		if err := store.Close(); err != nil {
			t.Fatalf("Close (simulated crash restart): %v", err)
		}
		store2, err := sqlite.Open(ctx, dbPath)
		if err != nil {
			t.Fatalf("Open (restart): %v", err)
		}
		store = store2
		uow = sqlite.NewUnitOfWork(store2)

		if err := runtime.StartupRecoveryScan(ctx, uow, testIDs); err != nil {
			t.Fatalf("StartupRecoveryScan: %v", err)
		}

		recoveredExecutor := &scriptedNodeExecutor{uow: uow}
		// A fresh prefix ("h2"), not "h" again: pool1's own handlerIDs
		// already minted real "h-N" ids durably persisted in this SAME
		// database — restarting "h" from 1 here would collide.
		registry2 := registerRuntimeEngineHandlers(t, store, uow, idsource.NewSequential("h2"), recoveredExecutor)
		pool2 := newRuntimeEnginePool(t, store, registry2)
		poolCtx2, cancelPool2 := context.WithCancel(ctx)
		poolErr2 := make(chan error, 1)
		go func() { poolErr2 <- pool2.Run(poolCtx2) }()
		poolStopped = false
		stopPool = func() {
			if poolStopped {
				return
			}
			poolStopped = true
			cancelPool2()
			<-poolErr2
		}
		// No new defer here: the single outer defer (above, before Run A
		// starts) closes over stopPool/store by reference, so it already
		// stops/closes whichever pair is current when this function
		// returns.
	}

	driveRERunToScopeExpansionApproval(t, store, uow, testIDs, runA, "scope_node")
	finalA := waitForRuntimeEngineRunState(t, uow, runA, runtimedomain.WorkflowRunVerifying)
	if finalA.State != runtimedomain.WorkflowRunVerifying {
		t.Fatalf("run A final state = %s, want VERIFYING", finalA.State)
	}

	if _, err := runtime.CancelRun(ctx, uow, testIDs, runtime.CancelRunRequest{
		RunID: runB, Actor: "operator-1", Reason: "runtime engine gate: cancel mid-flight",
	}); err != nil {
		t.Fatalf("CancelRun(B): %v", err)
	}
	finalB := waitForRuntimeEngineRunState(t, uow, runB, runtimedomain.WorkflowRunCancelled)
	if finalB.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run B final state = %s, want CANCELLED", finalB.State)
	}

	final := extractRuntimeEngineFinalResult(t, uow, runA, workItemA.WorkItemID, runB, workItemB.WorkItemID)
	events, err := store.ListDomainEventsForProject(ctx, "re-project")
	if err != nil {
		t.Fatalf("ListDomainEventsForProject: %v", err)
	}
	return runtimeEngineScenarioResult{Final: final, Trace: canonicalizeRuntimeEngineTrace(events)}
}

// waitForRuntimeEngineAttemptRunning polls until SOME ExecutionAttempt for
// nodeKey on runID reaches RUNNING.
func waitForRuntimeEngineAttemptRunning(t *testing.T, uow ports.UnitOfWork, runID, nodeKey string) runtimedomain.ExecutionAttempt {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(8 * time.Second)
	var found runtimedomain.ExecutionAttempt
	for time.Now().Before(deadline) {
		var attempts []runtimedomain.ExecutionAttempt
		if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
			return err
		}); err == nil {
			for _, a := range attempts {
				if a.State != runtimedomain.ExecutionAttemptRunning {
					continue
				}
				var nr runtimedomain.NodeRun
				if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
					var err error
					nr, err = tx.Runtime().GetNodeRun(ctx, string(a.NodeRunID))
					return err
				}); err == nil && nr.NodeKey == nodeKey {
					return a
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no RUNNING ExecutionAttempt found for node %s on run %s within the deadline", nodeKey, runID)
	return found
}

// extractRuntimeEngineFinalResult reads back the FinalDomainResult (see
// runtimeEngineFinalResult's own doc comment) — every field a real
// business decision, nothing recovery-specific.
func extractRuntimeEngineFinalResult(t *testing.T, uow ports.UnitOfWork, runA, workItemAID, runB, workItemBID string) runtimeEngineFinalResult {
	t.Helper()
	ctx := context.Background()

	runAState, err := uowGetWorkflowRun(ctx, uow, runA)
	if err != nil {
		t.Fatalf("GetWorkflowRun(A): %v", err)
	}
	workItemAState, err := uowGetWorkItem(ctx, uow, workItemAID)
	if err != nil {
		t.Fatalf("GetWorkItem(A): %v", err)
	}
	var nodeRunsA []runtimedomain.NodeRun
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		nodeRunsA, err = tx.Runtime().ListNodeRunsForRun(ctx, runA)
		return err
	}); err != nil {
		t.Fatalf("ListNodeRunsForRun(A): %v", err)
	}
	outcomes := map[string]string{}
	joinVerdict := ""
	for _, nr := range nodeRunsA {
		if nr.State != runtimedomain.NodeRunSucceeded && nr.State != runtimedomain.NodeRunFailed {
			continue
		}
		key := fmt.Sprintf("%s@%d", nr.NodeKey, nr.Iteration)
		outcomes[key] = nr.SelectedOutcome
		if nr.NodeKey == "join_node" {
			joinVerdict = nr.SelectedOutcome
		}
	}
	var amendments []runtimedomain.RunManifestAmendment
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		amendments, err = tx.Runtime().ListRunManifestAmendments(ctx, runA)
		return err
	}); err != nil {
		t.Fatalf("ListRunManifestAmendments(A): %v", err)
	}
	var manifestRevision uint64
	for _, a := range amendments {
		if a.Revision > manifestRevision {
			manifestRevision = a.Revision
		}
	}

	blockersA, err := uowListWorkItemBlockers(ctx, uow, workItemAID)
	if err != nil {
		t.Fatalf("ListWorkItemBlockersForWorkItem(A): %v", err)
	}
	var openBlockersA []string
	for _, b := range blockersA {
		if b.State == workdomain.BlockerOpen {
			openBlockersA = append(openBlockersA, string(b.Type))
		}
	}
	sort.Strings(openBlockersA)

	runBState, err := uowGetWorkflowRun(ctx, uow, runB)
	if err != nil {
		t.Fatalf("GetWorkflowRun(B): %v", err)
	}
	workItemBState, err := uowGetWorkItem(ctx, uow, workItemBID)
	if err != nil {
		t.Fatalf("GetWorkItem(B): %v", err)
	}
	blockersB, err := uowListWorkItemBlockers(ctx, uow, workItemBID)
	if err != nil {
		t.Fatalf("ListWorkItemBlockersForWorkItem(B): %v", err)
	}
	var openBlockersB []string
	for _, b := range blockersB {
		if b.State == workdomain.BlockerOpen {
			openBlockersB = append(openBlockersB, string(b.Type))
		}
	}
	sort.Strings(openBlockersB)

	return runtimeEngineFinalResult{
		RunAState: string(runAState.State), WorkItemAStatus: string(workItemAState.Status),
		RunASharedStateJSON: string(runAState.SharedState), RunAManifestRevision: manifestRevision,
		RunAOutcomes: outcomes, RunAJoinVerdict: joinVerdict, RunAOpenBlockerTypes: openBlockersA,
		RunBState: string(runBState.State), WorkItemBStatus: string(workItemBState.Status),
		RunBOpenBlockerTypes: openBlockersB,
	}
}

// --- operational trace (golden) ---

// canonicalTraceEvent is one domain_events row with every generated
// identity replaced by a stable alias (see canonicalizeRuntimeEngineTrace)
// and its own real wall-clock timestamp dropped — the "deterministic
// event/activation golden" this task's own Verify line names.
type canonicalTraceEvent struct {
	AggregateType string `json:"aggregateType"`
	AggregateID   string `json:"aggregateId"`
	Sequence      int64  `json:"sequence"`
	EventType     string `json:"eventType"`
	SchemaVersion int    `json:"schemaVersion"`
	PayloadJSON   string `json:"payload"`
}

// runtimeEngineGeneratedIDPattern matches any bare generated-ID-shaped
// string this gate's own ID sources can produce: a canonical (8-4-4-4-12
// hex) UUID (idsource.Random{}, used by the race test's own registries —
// see TestRuntimeEngineGate_ConcurrentPoolsNoDuplicateExecution), or one
// of this file's own three closed idsource.Sequential prefixes
// ("t-N"/"h-N"/"h2-N" — runRuntimeEngineScenario's own testIDs/handlerIDs
// doc comment). Not every generated ID ever becomes a domain_events
// AggregateID (a real run of this exact test caught the concrete case:
// the "end" node's own NodeRunID is only ever REFERENCED as a payload
// field — nothing routes away FROM end, so it never gets its own
// NODE_ROUTED/AggregateID row — so the AggregateID-driven alias pass below
// never learns it), so this second, broader pattern is what catches
// everything the first pass misses.
var runtimeEngineGeneratedIDPattern = regexp.MustCompile(`^(?:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|(?:t|h|h2)-\d+)$`)

// executionProfileHashPattern is the exact shape
// ResolvedExecutionProfileV1's own ExecutionProfileHash always takes (a
// lowercase-hex SHA-256 digest with its "sha256:" prefix) — validated
// before aliasing so a genuinely malformed/missing value fails loudly
// (canonicalizeRuntimeEngineTrace's own established discipline for every
// other unexpected shape) rather than silently passing through as an
// un-canonicalized, never-stable literal.
var executionProfileHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// canonicalizeRuntimeEngineTrace walks events in their own real journal
// order and aliases every distinct (AggregateType, AggregateID) pair to a
// stable "<lowercase_type>-N" name, plus every distinct bare
// generated-ID-shaped string value found anywhere in a payload (see
// runtimeEngineGeneratedIDPattern's own doc comment) to a stable "id-N",
// both in first-appearance order — the closed allow-list canonicalization
// confirmed with the user: only generated IDs/timestamps are ever
// touched, never event ordering, never a field dropped because two runs
// happened to differ (that would hide the exact regression this golden
// exists to catch). Two deliberate, narrowly-scoped exceptions to that
// rule live in the map-walking branch below, both keyed by FIELD NAME
// rather than value shape (so they can never accidentally swallow an
// unrelated hash-shaped field like contentHash/revisionHash/policyHash,
// which must stay byte-exact):
//
//  1. "jobId" is dropped to a fixed placeholder, never aliased: it is
//     purely a causal/debugging reference (which durable job produced this
//     event), not business content, and a real run of this exact test
//     proved its own concrete value is not even a stable FUNCTION of the
//     business decisions made (V4-13's own recovery reaper self-reschedules
//     a genuinely variable number of times before the single Concurrency:1
//     worker gets back to the retried Attempt, shifting every id minted
//     afterward).
//  2. "executionProfileHash" is aliased (same value -> same alias, in
//     first-appearance order, via its own dedicated alias map — never
//     folded into the generic aliasForGeneratedID's own UUID/t-N/h-N
//     namespace) rather than left byte-exact. Audit finding (2026-09-09,
//     V5-08 remediation): once this gate's own AGENT nodes pin a real
//     AdapterBuild (GC-INV-23), ResolvedExecutionProfileV1's own hash
//     folds in that build's CandidateTuple — which embeds the fixture
//     executable's real OS path and runtime.GOOS/runtime.Version() — so
//     its concrete value is a function of the MACHINE running the test,
//     not of any business decision this golden exists to protect. Confirmed
//     empirically: two separate local `go test` runs on the same source
//     produced two different executionProfileHash values for the identical
//     scenario, and CI's own contract job runs this exact test on BOTH
//     windows-latest and ubuntu-latest, so a byte-exact assertion on this
//     field could never pass on both platforms at once. Aliasing (rather
//     than the broader "alias every sha256:-shaped string" the user
//     explicitly rejected) preserves the one thing this golden actually
//     needs from the field: whether an AGENT node's profile hash is the
//     SAME or DIFFERENT across two events/nodes — GC-INV-23's own
//     "Attempt pin đúng immutable build" property is asserted directly
//     instead, in TestRuntimeEngineGate_AdapterBuildPinning below, which
//     reads the durable execution-profile-v1 DecisionArtifact and checks
//     its own AdapterBuild.BuildID rather than relying on this golden's
//     now-aliased hash text.
//
// Substitution walks each payload as PARSED JSON (json.Unmarshal into
// any, recurse, re-marshal — encoding/json's own map key sort order is
// already deterministic) and replaces a STRING VALUE only when it matches
// a known raw ID EXACTLY — never raw substring scanning against the whole
// payload text. A real run of this exact test caught why that distinction
// matters: a short sequential test ID like RunID "t-7" is also a trailing
// SUBSTRING of an unrelated UUID's own text purely by coincidence, so
// scanning-and-replacing raw substrings across the whole payload string
// corrupted OTHER fields' own values wherever they happened to end in the
// same characters — exact-value-only substitution over parsed JSON cannot
// do that, since "t-7" and "some-uuid-ending-int-7" are two entirely
// distinct JSON string values, never a substring relationship once parsed.
func canonicalizeRuntimeEngineTrace(events []sqlite.DomainEventRecord) []canonicalTraceEvent {
	aliasOf := map[string]string{}
	aggregateCounts := map[string]int{}
	genericIDCount := 0
	aliasForAggregate := func(aggregateType, aggregateID string) string {
		if alias, ok := aliasOf[aggregateID]; ok {
			return alias
		}
		aggregateCounts[aggregateType]++
		alias := fmt.Sprintf("%s-%d", strings.ToLower(aggregateType), aggregateCounts[aggregateType])
		aliasOf[aggregateID] = alias
		return alias
	}
	aliasForGeneratedID := func(id string) string {
		if alias, ok := aliasOf[id]; ok {
			return alias
		}
		genericIDCount++
		alias := fmt.Sprintf("id-%d", genericIDCount)
		aliasOf[id] = alias
		return alias
	}
	executionProfileHashAliasOf := map[string]string{}
	executionProfileHashCount := 0
	aliasForExecutionProfileHash := func(v any) any {
		s, ok := v.(string)
		if !ok || !executionProfileHashPattern.MatchString(s) {
			panic(fmt.Sprintf("canonicalizeRuntimeEngineTrace: executionProfileHash has unexpected shape %#v, want sha256:<64 lowercase hex>", v))
		}
		if alias, ok := executionProfileHashAliasOf[s]; ok {
			return alias
		}
		executionProfileHashCount++
		alias := fmt.Sprintf("<execution-profile-hash-%d>", executionProfileHashCount)
		executionProfileHashAliasOf[s] = alias
		return alias
	}
	var canonicalizeValue func(v any) any
	canonicalizeValue = func(v any) any {
		switch val := v.(type) {
		case string:
			if alias, ok := aliasOf[val]; ok {
				return alias
			}
			if runtimeEngineGeneratedIDPattern.MatchString(val) {
				return aliasForGeneratedID(val)
			}
			return val
		case map[string]any:
			// Sorted key order, not Go's own random map iteration order:
			// a lazily-assigned "uuid-N" alias is numbered in the order
			// this walk FIRST encounters each distinct UUID, so that
			// order must itself be deterministic run to run, not just the
			// final alias SET.
			keys := make([]string, 0, len(val))
			for k := range val {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out := make(map[string]any, len(val))
			for _, k := range keys {
				if k == "executionProfileHash" {
					out[k] = aliasForExecutionProfileHash(val[k])
					continue
				}
				if k == "jobId" {
					// jobId is dropped to a fixed placeholder, never
					// aliased: it is purely a causal/debugging reference
					// (which durable job produced this event), not
					// business content, and a REAL run of this exact test
					// proved its own concrete value is not even a
					// deterministic FUNCTION of the business decisions
					// made — V4-13's own RecoveryReaperHandler
					// self-reschedules with no fixed cycle count before
					// the retried Attempt's own EXECUTE_NODE job happens
					// to win the single Concurrency:1 worker's attention,
					// so every id-sequence number minted AFTER that point
					// legitimately drifts run to run even though every
					// business field stays byte-identical. Aliasing (as
					// opposed to dropping) would not fix this either: the
					// same jobId value appears in multiple, unrelated
					// events' own payloads only by accident of which
					// job happened to cause them, never as a genuine
					// cross-reference this golden needs to preserve.
					out[k] = "<job>"
					continue
				}
				out[k] = canonicalizeValue(val[k])
			}
			return out
		case []any:
			out := make([]any, len(val))
			for i, v := range val {
				out[i] = canonicalizeValue(v)
			}
			return out
		default:
			return v
		}
	}
	canonicalizePayload := func(payloadJSON string) string {
		var parsed any
		if err := json.Unmarshal([]byte(payloadJSON), &parsed); err != nil {
			// Never expected for a real, schema-registered event payload —
			// fail loudly rather than silently emit raw, non-canonical text
			// a golden comparison could never stabilize on.
			panic(fmt.Sprintf("canonicalizeRuntimeEngineTrace: invalid payload JSON: %v", err))
		}
		canonical, err := json.Marshal(canonicalizeValue(parsed))
		if err != nil {
			panic(fmt.Sprintf("canonicalizeRuntimeEngineTrace: re-marshal payload: %v", err))
		}
		return string(canonical)
	}

	// First pass: assign every AGGREGATE alias, in real journal
	// (first-appearance) order, before any payload substitution happens —
	// so a payload referencing an aggregate not yet otherwise seen (a
	// forward reference) still resolves to the SAME alias its own later
	// domain_events row gets, never a different one assigned mid-walk.
	for _, e := range events {
		aliasForAggregate(e.AggregateType, e.AggregateID)
	}

	out := make([]canonicalTraceEvent, 0, len(events))
	for _, e := range events {
		out = append(out, canonicalTraceEvent{
			AggregateType: e.AggregateType, AggregateID: aliasForAggregate(e.AggregateType, e.AggregateID),
			Sequence: e.Sequence, EventType: e.EventType, SchemaVersion: e.SchemaVersion,
			PayloadJSON: canonicalizePayload(e.PayloadJSON),
		})
	}
	return out
}

// TestCanonicalizeRuntimeEngineTrace_ExecutionProfileHashFieldAware proves
// the user's own explicit design for the golden-trace fix (2026-09-09,
// V5-08 remediation): "executionProfileHash" is aliased BY FIELD NAME
// (same value -> same alias, different value -> different alias — the
// golden still detects a real business divergence in WHICH nodes share a
// profile), while an unrelated hash-shaped field is left completely
// untouched — the field-aware exception must never widen into "alias
// every sha256:-shaped string" (a broader scope the user explicitly
// rejected, since many hashes — contentHash, revisionHash, policyHash —
// ARE genuine business/provenance content this golden must still catch a
// regression in).
func TestCanonicalizeRuntimeEngineTrace_ExecutionProfileHashFieldAware(t *testing.T) {
	hashA := "sha256:" + strings.Repeat("a", 64)
	hashB := "sha256:" + strings.Repeat("b", 64)
	contentHashValue := "sha256:" + strings.Repeat("c", 64)

	events := []sqlite.DomainEventRecord{
		{
			AggregateType: "ExecutionAttempt", AggregateID: "attempt-1", Sequence: 1,
			EventType: "NODE_SCHEDULED", SchemaVersion: 1,
			PayloadJSON: fmt.Sprintf(`{"executionProfileHash":%q,"contentHash":%q}`, hashA, contentHashValue),
		},
		{
			AggregateType: "ExecutionAttempt", AggregateID: "attempt-2", Sequence: 1,
			EventType: "NODE_SCHEDULED", SchemaVersion: 1,
			PayloadJSON: fmt.Sprintf(`{"executionProfileHash":%q,"contentHash":%q}`, hashA, contentHashValue),
		},
		{
			AggregateType: "ExecutionAttempt", AggregateID: "attempt-3", Sequence: 1,
			EventType: "NODE_SCHEDULED", SchemaVersion: 1,
			PayloadJSON: fmt.Sprintf(`{"executionProfileHash":%q,"contentHash":%q}`, hashB, contentHashValue),
		},
	}

	canonical := canonicalizeRuntimeEngineTrace(events)
	if len(canonical) != 3 {
		t.Fatalf("canonical trace = %d events, want 3", len(canonical))
	}
	var payloads [3]map[string]any
	for i, e := range canonical {
		if err := json.Unmarshal([]byte(e.PayloadJSON), &payloads[i]); err != nil {
			t.Fatalf("unmarshal event %d payload: %v", i, err)
		}
	}

	if payloads[0]["executionProfileHash"] != payloads[1]["executionProfileHash"] {
		t.Fatalf("same executionProfileHash value got different aliases: %v vs %v", payloads[0]["executionProfileHash"], payloads[1]["executionProfileHash"])
	}
	if payloads[0]["executionProfileHash"] == payloads[2]["executionProfileHash"] {
		t.Fatalf("different executionProfileHash values got the SAME alias: %v", payloads[0]["executionProfileHash"])
	}
	if got, ok := payloads[0]["executionProfileHash"].(string); !ok || got == hashA {
		t.Fatalf("executionProfileHash = %v, want an aliased placeholder, not the raw hash", payloads[0]["executionProfileHash"])
	}

	for i, p := range payloads {
		if got := p["contentHash"]; got != contentHashValue {
			t.Fatalf("event %d contentHash = %v, want untouched %q (an unrelated hash field must stay byte-exact)", i, got, contentHashValue)
		}
	}
}

// TestCanonicalizeRuntimeEngineTrace_MalformedExecutionProfileHash_PanicsFailClosed
// proves the fail-closed half of the same fix: a value under the
// "executionProfileHash" key that does NOT match the real
// ResolvedExecutionProfileV1 shape (sha256:<64 lowercase hex>) panics
// rather than silently passing through un-canonicalized — the same "fail
// loudly on an unexpected shape" discipline canonicalizePayload's own
// invalid-JSON branch already establishes.
func TestCanonicalizeRuntimeEngineTrace_MalformedExecutionProfileHash_PanicsFailClosed(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("canonicalizeRuntimeEngineTrace did not panic on a malformed executionProfileHash")
		}
	}()
	canonicalizeRuntimeEngineTrace([]sqlite.DomainEventRecord{{
		AggregateType: "ExecutionAttempt", AggregateID: "attempt-1", Sequence: 1,
		EventType: "NODE_SCHEDULED", SchemaVersion: 1,
		PayloadJSON: `{"executionProfileHash":"not-a-real-hash"}`,
	}})
}

const runtimeEngineRegenerateGoldenEnv = "AGENTKIT_REGENERATE_RUNTIME_ENGINE_GOLDEN"

// assertRuntimeEngineTraceGolden compares trace against goldenPath,
// byte-exact after canonicalization — regenerated ONLY when
// AGENTKIT_REGENERATE_RUNTIME_ENGINE_GOLDEN=1 is set in the environment
// (never automatically by a plain test run); a real behavior change's own
// diff against the previous golden is exactly the review artifact the user
// confirmed this mechanism should produce.
func assertRuntimeEngineTraceGolden(t *testing.T, trace []canonicalTraceEvent, goldenPath string) {
	t.Helper()
	got, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		t.Fatalf("marshal canonical trace: %v", err)
	}
	got = append(got, '\n')
	if os.Getenv(runtimeEngineRegenerateGoldenEnv) == "1" {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("regenerate golden %s: %v", goldenPath, err)
		}
		t.Logf("regenerated golden %s (%s=1)", goldenPath, runtimeEngineRegenerateGoldenEnv)
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with %s=1 to generate it)", goldenPath, err, runtimeEngineRegenerateGoldenEnv)
	}
	if string(got) != string(want) {
		t.Fatalf("operational trace does not match golden %s (run with %s=1 to inspect/regenerate; diff the two files to review)", goldenPath, runtimeEngineRegenerateGoldenEnv)
	}
}

func TestRuntimeEngineGate(t *testing.T) {
	clean := runRuntimeEngineScenario(t, false)
	recovered := runRuntimeEngineScenario(t, true)

	assertRuntimeEngineTraceGolden(t, clean.Trace, filepath.Join("testdata", "golden", "v4-runtime-clean.json"))
	assertRuntimeEngineTraceGolden(t, recovered.Trace, filepath.Join("testdata", "golden", "v4-runtime-crash-recovery.json"))

	if diff := diffRuntimeEngineFinalResult(clean.Final, recovered.Final); diff != "" {
		t.Fatalf("clean vs crash-recovered FinalDomainResult differ:\n%s", diff)
	}
	if clean.Final.RunAState != string(runtimedomain.WorkflowRunVerifying) {
		t.Fatalf("clean run A state = %s, want VERIFYING", clean.Final.RunAState)
	}
	if clean.Final.RunBState != string(runtimedomain.WorkflowRunCancelled) {
		t.Fatalf("clean run B state = %s, want CANCELLED", clean.Final.RunBState)
	}
	if got := clean.Final.RunAOutcomes["implement@0"]; got != "rework" {
		t.Fatalf(`RunAOutcomes["implement@0"] = %q, want "rework"`, got)
	}
	if got := clean.Final.RunAOutcomes["implement@1"]; got != "done" {
		t.Fatalf(`RunAOutcomes["implement@1"] = %q, want "done"`, got)
	}
	if got := clean.Final.RunAOutcomes["scope_node@0"]; got != "done" {
		t.Fatalf(`RunAOutcomes["scope_node@0"] = %q, want "done" (the reactivated activation)`, got)
	}
	if clean.Final.RunAJoinVerdict != "joined" {
		t.Fatalf("clean RunAJoinVerdict = %q, want %q", clean.Final.RunAJoinVerdict, "joined")
	}
	if clean.Final.RunAManifestRevision != 1 {
		t.Fatalf("clean RunAManifestRevision = %d, want 1 (scope-expansion's own single amendment)", clean.Final.RunAManifestRevision)
	}
	if len(clean.Final.RunAOpenBlockerTypes) != 0 {
		t.Fatalf("clean RunA open blockers = %v, want none (scope-expansion blocker resolved by reactivation)", clean.Final.RunAOpenBlockerTypes)
	}
	if want := []string{string(workdomain.BlockerRunCancelled)}; !reflect.DeepEqual(clean.Final.RunBOpenBlockerTypes, want) {
		t.Fatalf("clean RunB open blockers = %v, want %v", clean.Final.RunBOpenBlockerTypes, want)
	}
}

// --- race test ---

// TestRuntimeEngineGate_ConcurrentPoolsNoDuplicateExecution is this task's
// own "race test" (Verify line, confirmed with the user's own broader
// design): two REAL workerpool.Pool instances, registered against the
// SAME real *sqlite.Store and the SAME EXECUTE_NODE job for "implement"'s
// own real, fully-scheduled NodeRun, race to claim and process it —
// mirroring internal/app/workerpool's own TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing
// pattern, but through this gate's own real ExecuteNodeHandler/
// FinalizeExecutionAttempt chain rather than a bare synthetic handler, so
// this proves the SAME "exactly one claim wins" guarantee holds all the
// way through a real node execution, not just the underlying ClaimJob
// primitive V4-05/workerpool already prove independently on their own.
func TestRuntimeEngineGate_ConcurrentPoolsNoDuplicateExecution(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-runtime-engine-race.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)
	testIDs := idsource.NewSequential("t")

	seedREProject(t, uow, testIDs, "re-project", "repo-a", "repo-b")
	version := publishREDefinitions(t, uow, "re-project")

	// Setup phase: drives START -> ROUTER -> "implement" all the way to a
	// real QUEUED EXECUTE_NODE job via DIRECT calls to AdvanceRun/
	// ScheduleExecutableNodeRun — deliberately NO pool runs at all once
	// createREWorkItem's own initial WorkspaceSet provisioning is done. A
	// workerpool.Pool claims ANY available job kind-agnostically at the DB
	// level (workerpool.Pool.runWorker's own "no handler registered for
	// kind %q" error path proves a claim happens BEFORE the handler lookup
	// ever runs) — a pool with only Scheduler/NodeSchedulingHandler
	// registered would still race to claim the fresh EXECUTE_NODE job the
	// instant it appears, the exact accidental-double-claim risk this test
	// exists to rule out for the REAL race below. A short-lived
	// provisioning-only pool is safe here specifically because no
	// EXECUTE_NODE job can possibly exist yet at this point in the test —
	// createREWorkItem's own WorkspaceSet provisioning is the ONLY durable
	// job this phase ever produces.
	provisionRegistry := workerpool.NewRegistry()
	provisionRegistry.Register(appwork.WorkspaceProvisionJobKind, workspaceprovision.New(uow, idsource.Random{}, &scriptedWorkspaceProvider{}))
	provisionPool := newRuntimeEnginePool(t, store, provisionRegistry)
	provisionCtx, cancelProvision := context.WithCancel(ctx)
	provisionErr := make(chan error, 1)
	go func() { provisionErr <- provisionPool.Run(provisionCtx) }()

	workItem := createREWorkItem(t, store, uow, testIDs, "re-project", "Race gate work item", "repo-a")

	cancelProvision()
	if err := <-provisionErr; err != nil {
		t.Fatalf("provision pool.Run: %v", err)
	}
	startResult, err := runtime.StartWorkflowRun(ctx, uow, testIDs, reCommand("re-start-race", ports.ProjectScope("re-project"), "StartWorkflowRun"), runtime.StartWorkflowRunRequest{
		ProjectID: "re-project", WorkItemID: workItem.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := startResult.RunID

	hop, err := runtime.AdvanceRun(ctx, uow, testIDs, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun(start->gate): %v", err)
	}
	for hop.NextAutoAdvanced {
		hop, err = runtime.AdvanceRun(ctx, uow, testIDs, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: hop.NextNodeRunID})
		if err != nil {
			t.Fatalf("AdvanceRun(auto-advance chain): %v", err)
		}
	}
	if hop.NextNodeKey != "implement" {
		t.Fatalf("AdvanceRun chain reached node %q, want %q", hop.NextNodeKey, "implement")
	}
	if _, err := runtime.ScheduleExecutableNodeRun(ctx, uow, testIDs, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: hop.NextNodeRunID,
	}); err != nil {
		t.Fatalf("ScheduleExecutableNodeRun(implement): %v", err)
	}
	waitForRuntimeEngineNodeRunState(t, uow, runID, "implement", runtimedomain.NodeRunQueued)

	var executions atomic.Int32
	countingExecutor := &countingNodeExecutor{inner: &scriptedNodeExecutor{uow: uow}, count: &executions}
	raceRegistry := workerpool.NewRegistry()
	raceRegistry.Register(runtime.ExecuteNodeJobKind, runtime.NewExecuteNodeHandler(uow, idsource.Random{}, countingExecutor, clock.System{}, fake.IsolationEnforcementChecker{}, runtimeEngineAgentRegistry(t)))

	poolA := newRuntimeEnginePool(t, store, raceRegistry)
	poolB := newRuntimeEnginePool(t, store, raceRegistry)
	raceCtx, cancelRace := context.WithCancel(ctx)
	errA := make(chan error, 1)
	errB := make(chan error, 1)
	go func() { errA <- poolA.Run(raceCtx) }()
	go func() { errB <- poolB.Run(raceCtx) }()

	waitForRuntimeEngineNodeRunState(t, uow, runID, "implement", runtimedomain.NodeRunSucceeded)
	time.Sleep(200 * time.Millisecond) // give a would-be duplicate claim a real chance to also fire
	cancelRace()
	if err := <-errA; err != nil {
		t.Fatalf("Run (A): %v", err)
	}
	if err := <-errB; err != nil {
		t.Fatalf("Run (B): %v", err)
	}

	if got := executions.Load(); got != 1 {
		t.Fatalf("executor.Execute call count = %d, want exactly 1 (two real pools racing over the same EXECUTE_NODE job must never both run it)", got)
	}
	var attempts []runtimedomain.ExecutionAttempt
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("ListExecutionAttemptsForRun: %v", err)
	}
	succeeded := 0
	for _, a := range attempts {
		if a.State == runtimedomain.ExecutionAttemptSucceeded {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("SUCCEEDED ExecutionAttempt count = %d, want exactly 1 (attempts: %+v)", succeeded, attempts)
	}
}

// countingNodeExecutor wraps another ports.NodeExecutor, counting real
// Execute calls — the direct evidence TestRuntimeEngineGate_ConcurrentPoolsNoDuplicateExecution
// needs that only ONE of the two racing pools ever actually ran the node,
// not just that only one FINALIZED (which alone would not rule out both
// having raced into Execute concurrently before ExecuteNodeHandler's own
// claimRunning CAS decided a winner).
type countingNodeExecutor struct {
	inner ports.NodeExecutor
	count *atomic.Int32
}

func (e *countingNodeExecutor) Execute(ctx context.Context, req ports.NodeExecutionRequest) (ports.NodeExecutionResult, error) {
	e.count.Add(1)
	return e.inner.Execute(ctx, req)
}

func uowGetWorkItem(ctx context.Context, uow ports.UnitOfWork, workItemID string) (workdomain.WorkItem, error) {
	var item workdomain.WorkItem
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		item, err = tx.Work().GetWorkItem(ctx, workItemID)
		return err
	})
	return item, err
}

func uowListWorkItemBlockers(ctx context.Context, uow ports.UnitOfWork, workItemID string) ([]workdomain.WorkItemBlocker, error) {
	var blockers []workdomain.WorkItemBlocker
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		blockers, err = tx.Work().ListWorkItemBlockersForWorkItem(ctx, workItemID)
		return err
	})
	return blockers, err
}

func uowGetWorkflowRun(ctx context.Context, uow ports.UnitOfWork, runID string) (runtimedomain.WorkflowRun, error) {
	var run runtimedomain.WorkflowRun
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		run, err = tx.Runtime().GetWorkflowRun(ctx, runID)
		return err
	})
	return run, err
}

// TestRuntimeEngineGate_AdapterBuildPinning is the user's own explicit
// follow-up (2026-09-09, V5-08 remediation) to the golden-trace fix above:
// GC-INV-23's own "Attempt pin immutable AdapterBuildVersion; version khác
// bị từ chối trước dispatch" invariant is asserted DIRECTLY here —
// independent of the byte-exact golden comparison, which (after
// canonicalizeRuntimeEngineTrace's own executionProfileHash exception,
// documented above) no longer pins that field's literal value — through
// the real, wired-together system (a real *sqlite.Store, real
// ExecuteNodeHandler, a real workerpool.Pool), not internal/app/runtime's
// own unit-level admission_test.go (which already covers this identical
// invariant with fakes; this test proves the same thing holds through this
// gate's own full stack). Two parts, mirroring the user's own instruction:
//  1. a clean AGENT dispatch resolves the pinned build correctly and the
//     Attempt's own durable execution-profile-v1 DecisionArtifact records
//     that exact BuildID — "Build được resolve đúng" + "Attempt pin đúng
//     immutable build";
//  2. a real drift (the pinned executable's own bytes change on disk after
//     registration) blocks a FRESH Attempt before the executor is ever
//     spawned — "mismatch bị chặn trước dispatch", mirroring
//     internal/app/runtime's own TestAdmission_AdapterBuildDrift_BlocksBeforeSpawn.
func TestRuntimeEngineGate_AdapterBuildPinning(t *testing.T) {
	// Part 1/2 and Part 2/2 each get their OWN fresh *sqlite.Store/pool —
	// a real run of this exact test caught why sharing one store/pool
	// across both parts (stopping and restarting a pool mid-test, tamper
	// the shared fixture executable in between) is genuinely racy even
	// after the pool is fully stopped before tampering and handler ids are
	// shared rather than reset: an intermittent "execution attempt not
	// found" (blank AttemptID from ScheduleExecutableNodeRun's own
	// idempotent-replay guard) surfaced roughly 1 run in 25 under load,
	// most likely a residual timing edge in this project's own
	// already-documented class of Windows/SQLite concurrency flakes (see
	// db.go's own "_txlock=immediate" fix and the V0-11A diagnostic CI
	// step). Two fully independent scenarios (each publishing its own copy
	// of "re-project"'s definitions) removes the whole race by
	// construction rather than chasing one more manifestation of it.
	wantBuildID := runtimeEngineAdapterBuild(t).ID()

	// Part 1/2: clean dispatch resolves + pins the real build.
	func() {
		uow, implementNodeRunID, runID := runAdapterBuildPinningScenario(t, "adapterbuild-clean")
		ctx := context.Background()

		var recordedProfile runtimedomain.ResolvedExecutionProfileV1
		if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			artifact, err := tx.Runtime().GetDecisionArtifact(ctx, implementNodeRunID+"-execution-profile-v1")
			if err != nil {
				return err
			}
			return json.Unmarshal(artifact.Result, &recordedProfile)
		}); err != nil {
			t.Fatalf("load execution-profile-v1 decision artifact: %v", err)
		}
		if recordedProfile.AdapterBuild == nil || recordedProfile.AdapterBuild.BuildID != wantBuildID {
			t.Fatalf("recorded ResolvedExecutionProfileV1.AdapterBuild = %+v, want BuildID=%s", recordedProfile.AdapterBuild, wantBuildID)
		}
		if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			_, err := tx.AdapterBuilds().Get(ctx, wantBuildID)
			return err
		}); err != nil {
			t.Fatalf("AdapterBuilds().Get(%s): %v, want the real registered build to resolve", wantBuildID, err)
		}
		waitForRuntimeEngineNodeRunState(t, uow, runID, "implement", runtimedomain.NodeRunSucceeded)
	}()

	// Part 2/2: a real drift after registration blocks a FRESH Attempt
	// before the executor is ever spawned. The tampered bytes are restored
	// via t.Cleanup before this test returns — runtimeEngineAdapterBuild is
	// a package-wide sync.Once singleton every other test in this file also
	// depends on staying non-drifted.
	pinnedExecutablePath := runtimeEngineAdapterBuild(t).Tuple().ExecutablePath
	originalBytes, err := os.ReadFile(pinnedExecutablePath)
	if err != nil {
		t.Fatalf("read pinned executable before tampering: %v", err)
	}
	t.Cleanup(func() {
		if err := os.WriteFile(pinnedExecutablePath, originalBytes, 0o755); err != nil {
			t.Fatalf("restore pinned executable: %v", err)
		}
	})
	if err := os.WriteFile(pinnedExecutablePath, []byte("tampered-after-registration"), 0o755); err != nil {
		t.Fatalf("tamper pinned executable: %v", err)
	}

	uow, _, driftRunID := runAdapterBuildPinningScenario(t, "adapterbuild-drift")
	ctx := context.Background()

	blocked := waitForRuntimeEngineNodeRunState(t, uow, driftRunID, "implement", runtimedomain.NodeRunBlocked)
	var driftAttempt runtimedomain.ExecutionAttempt
	var driftBlockers []workdomain.WorkItemBlocker
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, driftRunID)
		if err != nil {
			return err
		}
		for _, a := range attempts {
			if a.NodeRunID == blocked.ID {
				driftAttempt = a
			}
		}
		run, err := tx.Runtime().GetWorkflowRun(ctx, driftRunID)
		if err != nil {
			return err
		}
		driftBlockers, err = tx.Work().ListWorkItemBlockersForWorkItem(ctx, string(run.WorkItemID))
		return err
	}); err != nil {
		t.Fatalf("reload drift attempt/blockers: %v", err)
	}
	if driftAttempt.ID == "" {
		t.Fatalf("no ExecutionAttempt found for blocked node run %s", blocked.ID)
	}
	if driftAttempt.State != runtimedomain.ExecutionAttemptBlocked {
		t.Fatalf("drift attempt.State = %s, want BLOCKED", driftAttempt.State)
	}
	if driftAttempt.TerminationReason != runtimedomain.TerminationReasonAdapterBuildDrift {
		t.Fatalf("drift attempt.TerminationReason = %s, want %s", driftAttempt.TerminationReason, runtimedomain.TerminationReasonAdapterBuildDrift)
	}
	if driftAttempt.StartedAt != nil {
		t.Fatalf("drift attempt.StartedAt = %v, want nil — a real drift must block BEFORE the executor is ever spawned", *driftAttempt.StartedAt)
	}
	foundBlocker := false
	for _, blocker := range driftBlockers {
		if blocker.Type == workdomain.BlockerAdapterBuildDrift && blocker.SourceAttemptID == string(driftAttempt.ID) {
			foundBlocker = true
			if blocker.State != workdomain.BlockerOpen {
				t.Fatalf("drift blocker.State = %s, want OPEN", blocker.State)
			}
		}
	}
	if !foundBlocker {
		t.Fatalf("no %s blocker found for drift attempt %s among %+v", workdomain.BlockerAdapterBuildDrift, driftAttempt.ID, driftBlockers)
	}
}

// runAdapterBuildPinningScenario sets up one fully independent
// "re-project" (fresh store, fresh pool, its own copy of every published
// definition) and drives it from START through "implement"'s own
// ScheduleExecutableNodeRun — the common setup
// TestRuntimeEngineGate_AdapterBuildPinning's own two parts each need,
// factored out so the two parts never share a store (see that test's own
// top doc comment for why). The pool keeps running until the test itself
// ends (t.Cleanup), since the caller still needs to observe the
// asynchronous EXECUTE_NODE dispatch this function's own
// ScheduleExecutableNodeRun call enqueues.
func runAdapterBuildPinningScenario(t *testing.T, title string) (uow ports.UnitOfWork, implementNodeRunID, runID string) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-runtime-engine-adapterbuild-"+title+".db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open(%s): %v", title, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	u := sqlite.NewUnitOfWork(store)
	testIDs := idsource.NewSequential("t")

	seedREProject(t, u, testIDs, "re-project", "repo-a")
	version := publishREDefinitions(t, u, "re-project")

	// Setup phase mirrors TestRuntimeEngineGate_ConcurrentPoolsNoDuplicateExecution's
	// own established pattern exactly (see its doc comment): drive
	// START -> ROUTER -> "implement" via DIRECT foreground calls to
	// AdvanceRun/ScheduleExecutableNodeRun, with NO full pool running
	// concurrently. A real run of this exact test caught why that matters:
	// starting registerRuntimeEngineHandlers' own full registry (Scheduler's
	// own AdvanceRunJobKind handler included) before this foreground chain
	// lets the pool's own background Scheduler race to advance the SAME
	// NodeRun this code is directly advancing — the loser (sometimes this
	// code's own foreground call) sees an idempotent-replay hop with a
	// blank NextNodeKey. A short-lived provisioning-only pool is safe here
	// specifically because no ADVANCE_RUN/EXECUTE_NODE job can race it —
	// createREWorkItem's own WorkspaceSet provisioning is the only durable
	// job this phase produces.
	provisionRegistry := workerpool.NewRegistry()
	provisionRegistry.Register(appwork.WorkspaceProvisionJobKind, workspaceprovision.New(u, idsource.Random{}, &scriptedWorkspaceProvider{}))
	provisionPool := newRuntimeEnginePool(t, store, provisionRegistry)
	provisionCtx, cancelProvision := context.WithCancel(ctx)
	provisionErr := make(chan error, 1)
	go func() { provisionErr <- provisionPool.Run(provisionCtx) }()

	workItem := createREWorkItem(t, store, u, testIDs, "re-project", title, "repo-a")

	cancelProvision()
	if err := <-provisionErr; err != nil {
		t.Fatalf("provision pool.Run(%s): %v", title, err)
	}

	start, err := runtime.StartWorkflowRun(ctx, u, testIDs, reCommand("re-start-"+title, ports.ProjectScope("re-project"), "StartWorkflowRun"), runtime.StartWorkflowRunRequest{
		ProjectID: "re-project", WorkItemID: workItem.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun(%s): %v", title, err)
	}
	hop, err := runtime.AdvanceRun(ctx, u, testIDs, runtime.AdvanceRunRequest{RunID: start.RunID, NodeRunID: start.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun(%s start->gate): %v", title, err)
	}
	for hop.NextAutoAdvanced {
		hop, err = runtime.AdvanceRun(ctx, u, testIDs, runtime.AdvanceRunRequest{RunID: start.RunID, NodeRunID: hop.NextNodeRunID})
		if err != nil {
			t.Fatalf("AdvanceRun(%s auto-advance chain): %v", title, err)
		}
	}
	if hop.NextNodeKey != "implement" {
		t.Fatalf("AdvanceRun(%s) chain reached node %q, want %q", title, hop.NextNodeKey, "implement")
	}
	if _, err := runtime.ScheduleExecutableNodeRun(ctx, u, testIDs, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: start.RunID, NodeRunID: hop.NextNodeRunID,
	}); err != nil {
		t.Fatalf("ScheduleExecutableNodeRun(%s implement): %v", title, err)
	}

	// Only now — after the foreground chain above has fully finished
	// advancing/scheduling, with no risk of racing this same pool's own
	// Scheduler against it — start the full pool so it can pick up and
	// process the real, already-QUEUED EXECUTE_NODE job asynchronously; the
	// caller waits on the resulting NodeRun state (SUCCEEDED or BLOCKED).
	registry := registerRuntimeEngineHandlers(t, store, u, idsource.NewSequential("h"), &scriptedNodeExecutor{uow: u})
	pool := newRuntimeEnginePool(t, store, registry)
	poolCtx, cancelPool := context.WithCancel(ctx)
	poolErr := make(chan error, 1)
	go func() { poolErr <- pool.Run(poolCtx) }()
	t.Cleanup(func() {
		cancelPool()
		if err := <-poolErr; err != nil {
			t.Fatalf("pool.Run(%s): %v", title, err)
		}
	})

	return u, hop.NextNodeRunID, start.RunID
}
