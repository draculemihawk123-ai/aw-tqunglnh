// V8-04B — Process, executable and isolation abuse suite
// (docs/design/10-v8-alpha-hardening.md V8-04B, ADR-013/ADR-023/
// AK-ARCH-015B/AK-ARCH-020A): the "multi-repo write thiếu capability" half
// of that task's own Thực hiện line, proven the way V5-15C's own
// isolation_unavailable_test.go/adapter_drift_test.go already proved their
// two admission-priority siblings — a real production rejection, not a
// simulated one.
//
// checkMultiRepositoryWriteGrant (internal/app/runtime/admission.go, GC-INV-24:
// "Mutating attempt có đúng một repository READ_WRITE, trừ khi capability
// INTEGRATION_MULTI_REPOSITORY_WRITE được pin và cấp") already has its own
// admission.go-level unit test
// (TestAdmission_MultiRepositoryWriteWithoutGrant_BlocksBeforeSpawn), but
// that test drives evaluateAdmission directly against hand-built values —
// it never proves a NodeRun whose EffectiveScope genuinely grants WRITE on
// two real, independently-provisioned repositories reaches admission this
// way through the real production path (CreateRootWorkItem/
// CreateChildWorkItem -> real WORKSPACE_PROVISION jobs -> real
// ExecuteNodeHandler). This file closes that gap, reusing v5AcceptFixture
// exactly as isolation_unavailable_test.go/adapter_drift_test.go do.
package v5accept

import (
	"context"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	appwork "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

const v5AcceptSecondRepositoryID = "repo-b"

// registerSecondV5AcceptRepository registers and activates a real, SECOND
// git repository (repo-b) alongside f's own default repo-a — mirrors
// seedActiveProjectAndRepository exactly (same synchronous
// REGISTERING->PROBING->ACTIVE drive, since no real repository-probe
// executor exists in this codebase yet), generalized to a caller-chosen
// repositoryID/path pair the way golden_workload_test.go's own
// registerGoldenRepository generalizes journey.projectAndRepository
// (internal/integration/v6accept) for the identical reason: this scenario
// is the first in this package that needs more than one repository.
func (f *v5AcceptFixture) registerSecondV5AcceptRepository(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	repositoryPath := createV5AcceptGitRepository(t, filepath.Join(f.fixtureRoot, "origin-repo-b"))
	if _, err := catalog.RegisterRepository(ctx, f.uow, f.ids, testCmd("v5a-reg-"+v5AcceptSecondRepositoryID, ports.ProjectScope(v5AcceptProjectID), "RegisterRepository"), catalog.RegisterRepositoryRequest{
		RepositoryID: v5AcceptSecondRepositoryID, ProjectID: v5AcceptProjectID, Name: v5AcceptSecondRepositoryID,
		RemoteLocator: repositoryPath, DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository(%s): %v", v5AcceptSecondRepositoryID, err)
	}
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		probing, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: v5AcceptSecondRepositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing,
		})
		if err != nil {
			return err
		}
		_, err = tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: v5AcceptSecondRepositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: probing.Version,
			NextStatus: project.RepositoryActive,
		})
		return err
	}); err != nil {
		t.Fatalf("drive repository %s to ACTIVE: %v", v5AcceptSecondRepositoryID, err)
	}
	return repositoryPath
}

// v5AcceptMultiRepoNoGrantPermissionPolicyDocument pins OperatorTrustedLocal
// (never EnforcedIsolated — admissionPriority checks isolation BEFORE
// multi-repository-write, admission.go, and this scenario is about the
// write-grant check specifically, never about tripping isolation first and
// masking it, exactly the same reasoning
// v5AcceptDriftPermissionPolicyDocument's own doc comment already gives for
// adapter drift) and deliberately grants NO capabilities at all — in
// particular, never INTEGRATION_MULTI_REPOSITORY_WRITE.
func v5AcceptMultiRepoNoGrantPermissionPolicyDocument() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryPermission,
		Permission: &policy.PermissionRules{
			IsolationTier: policy.IsolationTierOperatorTrustedLocal,
		},
	}
}

// v5AcceptMultiRepoDocument mirrors v5AcceptIsolationUnavailableDocument's
// own single-real-COMMAND-node shape exactly (isolation_unavailable_test.go)
// — no gate, no completion policy: admission never reaches a point where
// either would matter.
func v5AcceptMultiRepoDocument() workflow.WorkflowDocument {
	attemptPermissionRefs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v5c-multirepo-attempt-policy-def", VersionID: "v5c-multirepo-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v5c-multirepo-permission-policy-def", VersionID: "v5c-multirepo-permission-policy-v1"},
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{
				Key: "test_a", Type: workflow.NodeCommand, Outcomes: []string{"passed"},
				Command: &workflow.CommandNodeConfig{
					CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v5c-multirepo-maker-cmd-def", VersionID: "v5c-multirepo-maker-cmd-v1"},
					PolicyRefs: attemptPermissionRefs,
				},
			},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-test_a", From: "start", Outcome: "next", To: "test_a"},
			{Key: "test_a-end", From: "test_a", Outcome: "passed", To: "end"},
		},
	}
}

func TestV5AcceptMultiRepositoryWriteWithoutGrant_RealAdmissionRejectsBeforeSpawn(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	f.registerSecondV5AcceptRepository(t)

	// The marker path a real maker script would write to, if it ever ran —
	// this test's own proof (identical technique to
	// isolation_unavailable_test.go/adapter_drift_test.go) that admission
	// genuinely rejected the Attempt BEFORE any real process spawn.
	markerPath := filepath.Join(f.fixtureRoot, "v5c-multirepo-marker.txt")
	makerKey, makerScript, _, _ := v5AcceptScripts(markerPath)
	skillDoc := v5AcceptTwoResourceSkillDocument(makerKey, makerScript, "unused.txt", "unused")
	publishSkillVersion(t, f.uow, "v5c-multirepo-skill-def", "v5c-multirepo-skill-v1", skillDoc)
	makerHash := resourceContentHash(t, "v5c-multirepo-skill-v1", makerKey, skillDoc)

	publishCommandVersion(t, f.uow, "v5c-multirepo-maker-cmd-def", "v5c-multirepo-maker-cmd-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "v5c-multirepo-skill-v1", ResourceKey: makerKey, ContentHash: makerHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: v5AcceptRepositoryID,
		Compatibility:       command.Compatibility{OS: []string{stdruntime.GOOS}},
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      60,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})

	publishPolicyVersion(t, f.uow, "v5c-multirepo-attempt-policy-def", "v5c-multirepo-attempt-policy-v1", v5AcceptAttemptPolicyDocument(60))
	publishPolicyVersion(t, f.uow, "v5c-multirepo-permission-policy-def", "v5c-multirepo-permission-policy-v1", v5AcceptMultiRepoNoGrantPermissionPolicyDocument())

	doc := v5AcceptMultiRepoDocument()
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v5c-multirepo-workflow-def", "v5c-multirepo-workflow-v1", doc, workflow.DependencyManifest{})

	router := &runtime.NodeExecutorRouter{Command: f.newCommandExecutor()}
	registry := f.registerHandlers(router, "v5cmr")
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	// Real production path, not the single-repository fixture helpers:
	// InitialScope grants WRITE on BOTH real repositories, so both really
	// provision (two real WORKSPACE_PROVISION jobs), and EffectiveScope
	// carries both through to the child that actually runs — the exact
	// "more than one repository is write-scoped" shape
	// checkMultiRepositoryWriteGrant's own doc comment describes.
	bothRepositoriesWrite := []appwork.ScopeGrantRequest{
		{RepositoryID: v5AcceptRepositoryID, Access: string(workdomain.RepositoryWrite), Reason: "v8-04b multi-repo write acceptance"},
		{RepositoryID: v5AcceptSecondRepositoryID, Access: string(workdomain.RepositoryWrite), Reason: "v8-04b multi-repo write acceptance"},
	}
	root, err := appwork.CreateRootWorkItem(ctx, f.uow, f.ids, testCmd("v5a-multirepo-root", ports.ProjectScope(v5AcceptProjectID), "CreateRootWorkItem"), appwork.CreateRootWorkItemRequest{
		ProjectID: v5AcceptProjectID, Title: "multirepo-write-root", InitialScope: bothRepositoriesWrite,
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	for _, provisioned := range root.ProvisionedRepositories {
		f.waitForJobState(t, provisioned.ProvisionJobID, ports.JobSucceeded)
	}
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: root.WorkItemID, ExpectedStatus: workdomain.WorkItemBacklog, ExpectedVersion: 1,
			NextStatus: workdomain.WorkItemReady,
		})
		return err
	}); err != nil {
		t.Fatalf("force root work item ready: %v", err)
	}

	child, err := appwork.CreateChildWorkItem(ctx, f.uow, f.ids, testCmd("v5a-multirepo-child", ports.ProjectScope(v5AcceptProjectID), "CreateChildWorkItem"), appwork.CreateChildWorkItemRequest{
		ParentWorkItemID: root.WorkItemID, Title: "multirepo-write-child", ParentJoinPolicy: "v8-04b-multirepo-child",
		EffectiveScope: bothRepositoriesWrite,
	})
	if err != nil {
		t.Fatalf("CreateChildWorkItem: %v", err)
	}
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: child.WorkItemID, ExpectedStatus: workdomain.WorkItemBacklog, ExpectedVersion: 1,
			NextStatus: workdomain.WorkItemReady,
		})
		return err
	}); err != nil {
		t.Fatalf("force child work item ready: %v", err)
	}

	startCmd := testCmd("v5c-multirepo-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := started.RunID

	nodeRun := f.waitForNodeRunState(t, runID, "test_a", runtimedomain.NodeRunBlocked)

	var attempts []runtimedomain.ExecutionAttempt
	var blockers []workdomain.WorkItemBlocker
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		if err != nil {
			return err
		}
		blockers, err = tx.Work().ListWorkItemBlockersForWorkItem(ctx, child.WorkItemID)
		return err
	}); err != nil {
		t.Fatalf("read admission-blocked state: %v", err)
	}

	var blockedAttempt *runtimedomain.ExecutionAttempt
	for i := range attempts {
		if attempts[i].NodeRunID == nodeRun.ID {
			blockedAttempt = &attempts[i]
		}
	}
	if blockedAttempt == nil {
		t.Fatalf("no ExecutionAttempt found for node run %s", nodeRun.ID)
	}
	if blockedAttempt.State != runtimedomain.ExecutionAttemptBlocked {
		t.Fatalf("attempt.State = %s, want BLOCKED", blockedAttempt.State)
	}
	if blockedAttempt.TerminationReason != runtimedomain.TerminationReasonWriteCapabilityOrGrantMissing {
		t.Fatalf("attempt.TerminationReason = %s, want %s", blockedAttempt.TerminationReason, runtimedomain.TerminationReasonWriteCapabilityOrGrantMissing)
	}

	foundBlocker := false
	for _, blocker := range blockers {
		if blocker.Type == workdomain.BlockerWriteCapabilityOrGrantMissing {
			foundBlocker = true
			if blocker.State != workdomain.BlockerOpen {
				t.Fatalf("blocker.State = %s, want OPEN", blocker.State)
			}
			if blocker.SourceAttemptID != string(blockedAttempt.ID) {
				t.Fatalf("blocker.SourceAttemptID = %s, want %s", blocker.SourceAttemptID, blockedAttempt.ID)
			}
		}
	}
	if !foundBlocker {
		t.Fatalf("no WorkItemBlocker of type %s found for work item %s; blockers=%+v", workdomain.BlockerWriteCapabilityOrGrantMissing, child.WorkItemID, blockers)
	}

	// The real proof admission rejected this BEFORE any spawn: the real
	// maker script's own marker file was never written, because
	// ProcessSupervisor.Run was never called at all — "spawn count == 0 for
	// [the blocked] case", V8-04B's own Verify line, made concrete.
	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("marker file %s exists (err=%v) — the real maker process was spawned despite the missing multi-repository-write grant", markerPath, err)
	}
}
