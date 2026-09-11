// Package v5accept is V5-15's own acceptance-gate test package
// (docs/design/07-v5-execution-evidence.md V5-15) — the first place this
// codebase ever composes a real NodeExecutorRouter, real
// CommandNodeExecutor/GateNodeExecutor, real EvaluateCompletionCandidate
// (V5-11) and a real ReleaseSet (V5-10A) together, driving a real,
// multi-node WorkflowRun through a real *sqlite.Store, a real
// workerpool.Pool and a real gitworktree.Provider (real git subprocess),
// never internal/integration/runtimeengine_test.go's own
// scriptedNodeExecutor/scriptedWorkspaceProvider stand-ins (that file's
// own V4-14 scope deliberately stops at VERIFYING and never touches real
// Git — see its own package doc comment).
//
// User's own binding contract for V5-15 (2026-09-11, full text in
// baocaov5checklist.md's own "V5-15" section and memory
// agent-kit-v5-15-acceptance-gate-contract.md): every PR must exercise
// the real production command/router/repository path — never mutate the
// database directly to fabricate the result being verified. This file is
// PR A's own "reusable scenario harness" the contract asks for: every
// later V5-15 PR (B: completion integrity, C: recovery/availability, D:
// isolation/fencing, E: full conformance matrix) builds its own fault
// scenario on top of v5AcceptFixture rather than re-deriving this wiring.
package v5accept

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	processadapter "github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/secretenv"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	appwork "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// v5AcceptProjectID/v5AcceptRepositoryID are this package's own fixed
// fixture identities — every scenario in this package acts on the same
// one project/repository shape, mirroring internal/integration's own
// "re-project"/"repo-a" convention (runtimeengine_test.go).
const (
	v5AcceptProjectID    = "v5a-project"
	v5AcceptRepositoryID = "repo-a"
)

// v5AcceptFixture bundles one real, on-disk-git-backed acceptance
// scenario: a real *sqlite.Store, a real gitworktree.Provider (real git
// subprocess), a real filesystem ArtifactStore, a real ProcessSupervisor
// and SecretResolver — every adapter a real CommandNodeExecutor/
// GateNodeExecutor composition needs, none of them faked. restart below
// is the one sanctioned way this fixture's own store is ever closed and
// reopened — every V5-15 scenario that needs a real crash/restart proof
// (PR A's own happy path, PR C's own recovery scenarios) calls it rather
// than reimplementing store.Close()/sqlite.Open() inline.
type v5AcceptFixture struct {
	dbPath      string
	fixtureRoot string
	repoPath    string

	store      *sqlite.Store
	uow        ports.UnitOfWork
	ids        idsource.Source
	provider   *gitworktree.Provider
	artifacts  ports.ArtifactStore
	supervisor ports.ProcessSupervisor
	secrets    ports.SecretResolver
}

// newV5AcceptFixture builds one real git fixture repository, a real
// sqlite store, and drives project-1/repo-a to ACTIVE — everything every
// V5-15 scenario needs before it can ever create a WorkItem. Requires a
// real `git` executable on PATH (this codebase's own established
// end-to-end-test posture — see internal/app/workspacereconcile/
// handler_sqlite_test.go's own createReconcileFixtureGitRepository).
func newV5AcceptFixture(t *testing.T) *v5AcceptFixture {
	t.Helper()
	ctx := context.Background()

	fixtureRoot := t.TempDir()
	repoPath := createV5AcceptGitRepository(t, filepath.Join(fixtureRoot, "origin-repo-a"))
	dbPath := filepath.Join(fixtureRoot, "agentkit-v5accept.db")

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("v5a")

	provider, err := gitworktree.New(gitworktree.Config{Root: filepath.Join(fixtureRoot, "workspaces")})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}
	artifacts, err := artifactstore.New(filepath.Join(fixtureRoot, "artifacts"))
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}

	f := &v5AcceptFixture{
		dbPath: dbPath, fixtureRoot: fixtureRoot, repoPath: repoPath,
		store: store, uow: uow, ids: ids, provider: provider, artifacts: artifacts,
		supervisor: processadapter.NewSupervisor(), secrets: secretenv.NewResolver(),
	}
	// Closes over f (a pointer), never the local store variable above —
	// f.restart replaces f.store in place after a real close/reopen, so
	// this must always close whichever store is CURRENT when the test
	// ends, not the one that existed when this fixture was constructed.
	t.Cleanup(func() { _ = f.store.Close() })
	f.seedActiveProjectAndRepository(t)
	return f
}

// restart closes f's own store and reopens the SAME on-disk database,
// replacing f.store/f.uow in place — the one real "crash/restart" proof
// every V5-15 scenario that claims persistence survives it must go
// through, mirroring internal/integration/runtimeengine_test.go's own
// inline store.Close()/sqlite.Open() restart sequence.
func (f *v5AcceptFixture) restart(t *testing.T) {
	t.Helper()
	if err := f.store.Close(); err != nil {
		t.Fatalf("close store before restart: %v", err)
	}
	store, err := sqlite.Open(context.Background(), f.dbPath)
	if err != nil {
		t.Fatalf("reopen store after restart: %v", err)
	}
	f.store = store
	f.uow = sqlite.NewUnitOfWork(store)
}

// seedActiveProjectAndRepository mirrors internal/integration's own
// seedREProject: create the Project, RegisterRepository against the real
// on-disk origin-repo-a fixture, then drive it directly
// REGISTERING->PROBING->ACTIVE (no real repository-probe executor exists
// in this codebase yet).
func (f *v5AcceptFixture) seedActiveProjectAndRepository(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: v5AcceptProjectID, Name: "V5-15 acceptance project"})
		return err
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := catalog.RegisterRepository(ctx, f.uow, f.ids, testCmd("v5a-reg-"+v5AcceptRepositoryID, ports.ProjectScope(v5AcceptProjectID), "RegisterRepository"), catalog.RegisterRepositoryRequest{
		RepositoryID: v5AcceptRepositoryID, ProjectID: v5AcceptProjectID, Name: v5AcceptRepositoryID,
		RemoteLocator: f.repoPath, DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		probing, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: v5AcceptRepositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing,
		})
		if err != nil {
			return err
		}
		_, err = tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: v5AcceptRepositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: probing.Version,
			NextStatus: project.RepositoryActive,
		})
		return err
	}); err != nil {
		t.Fatalf("drive repository %s to ACTIVE: %v", v5AcceptRepositoryID, err)
	}
}

// --- real Git fixture ---

// createV5AcceptGitRepository mirrors
// internal/app/workspacereconcile/handler_sqlite_test.go's own
// createReconcileFixtureGitRepository exactly: one real git repository,
// one committed file, ready for gitworktree.Provider to provision real
// worktrees from.
func createV5AcceptGitRepository(t *testing.T, repositoryPath string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required for this end-to-end test: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(repositoryPath), 0o755); err != nil {
		t.Fatalf("create repository parent: %v", err)
	}
	runV5AcceptGit(t, "", "init", "--initial-branch=main", repositoryPath)
	runV5AcceptGit(t, repositoryPath, "config", "user.name", "Agent Kit Test")
	runV5AcceptGit(t, repositoryPath, "config", "user.email", "agent-kit@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "service.txt"), []byte("v0\n"), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	runV5AcceptGit(t, repositoryPath, "add", "--", "service.txt")
	runV5AcceptGit(t, repositoryPath, "commit", "-m", "initial fixture")
	return repositoryPath
}

func runV5AcceptGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	commandArguments := arguments
	if directory != "" {
		commandArguments = append([]string{"-C", directory}, arguments...)
	}
	cmd := exec.Command("git", commandArguments...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", commandArguments, err, output)
	}
	return string(output)
}

// --- WorkItem / workspace ---

// createRootWorkItem creates a real ROOT WorkItem via the real
// appwork.CreateRootWorkItem command, waits for its own real
// WORKSPACE_PROVISION job (dispatched to the running pool's own
// registered workspaceprovision.Handler, real gitworktree.Provider) to
// reach SUCCEEDED, then forces WorkItem BACKLOG->READY directly — no real
// command reaches READY yet anywhere in this codebase (mirrors
// internal/app/runtime's own readyFixtureSQLite doc comment).
func (f *v5AcceptFixture) createRootWorkItem(t *testing.T, title string, access workdomain.RepositoryAccess) appwork.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	result, err := appwork.CreateRootWorkItem(ctx, f.uow, f.ids, testCmd("v5a-root-"+title, ports.ProjectScope(v5AcceptProjectID), "CreateRootWorkItem"), appwork.CreateRootWorkItemRequest{
		ProjectID: v5AcceptProjectID, Title: title, InitialScope: []appwork.ScopeGrantRequest{{
			// PathScopes is deliberately nil, not []string{"**"}: scopeguard's
			// own isAllowed (internal/app/scopeguard/guard.go) matches a
			// PathScopes entry as a literal path PREFIX, never a glob — "**"
			// only ever matches a changed path that itself literally starts
			// with "**/" (existing fixtures that use "**" get away with it by
			// also hand-crafting a fake diff path like "**/src/main.go" — see
			// agent_node_executor_test.go's own defaultInScopeDiff doc
			// comment). This package's own real git diffs report ordinary
			// paths (e.g. "output.txt"), so an empty PathScopes list — the
			// real "no path restriction, whole repository" convention
			// normalizePathScopes itself documents (len==0 -> nil, nil, never
			// an error) — is what a real, unrestricted WRITE grant needs here.
			RepositoryID: v5AcceptRepositoryID, Access: string(access), Reason: "v5-15 acceptance",
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem(%s): %v", title, err)
	}
	for _, provisioned := range result.ProvisionedRepositories {
		f.waitForJobState(t, provisioned.ProvisionJobID, ports.JobSucceeded)
	}
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
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

// createChildWorkItem creates a real CHILD WorkItem via the real
// appwork.CreateChildWorkItem command — the ONLY real production path
// that ever populates a WorkItem's own real EffectiveScope directly at
// creation time (confirmed by reading the code: appwork.CreateRootWorkItem
// never calls tx.Work().AddEffectiveScope at all — internal/app/work/
// commands.go's own doc comment: "CreateRootWorkItem itself never
// populates ListWorkItemEffectiveScopes — only CreateChildWorkItem does";
// the ONLY other real caller is runtime/scope_expansion.go's own
// ApproveScopeExpansion reactivation path, which needs a real AGENT node
// to request it first — out of this package's own COMMAND+MACHINE_GATE-
// only happy-path scope). Reuses the parent's own WorkspaceSet outright
// (no new WORKSPACE_PROVISION job — CreateChildWorkItem's own doc comment:
// "Child cùng family reuse WorkspaceSet"), so no job-state polling is
// needed here the way createRootWorkItem needs for its own provisioning.
func (f *v5AcceptFixture) createChildWorkItem(t *testing.T, parentWorkItemID, title string, access workdomain.RepositoryAccess) appwork.CreateChildWorkItemResult {
	t.Helper()
	// PathScopes is deliberately nil, not []string{"**"}: scopeguard's own
	// isAllowed (internal/app/scopeguard/guard.go) matches a PathScopes
	// entry as a literal path PREFIX, never a glob — "**" only ever
	// matches a changed path that itself literally starts with "**/"
	// (existing fixtures that use "**" get away with it by also
	// hand-crafting a fake diff path like "**/src/main.go" — see
	// agent_node_executor_test.go's own defaultInScopeDiff doc comment).
	// This package's own real git diffs report ordinary paths (e.g.
	// "output.txt"), so an empty PathScopes list — the real "no path
	// restriction, whole repository" convention normalizePathScopes
	// itself documents (len==0 -> nil, nil, never an error) — is what a
	// real, unrestricted WRITE grant needs here. A scenario that DOES need
	// a real, narrower literal-prefix restriction (V5-15D's own "scope
	// violation") calls createChildWorkItemWithPathScopes directly.
	return f.createChildWorkItemWithPathScopes(t, parentWorkItemID, title, access, nil)
}

// createChildWorkItemWithPathScopes is createChildWorkItem with an explicit
// PathScopes grant — V5-15D's own "scope violation" scenario needs a real,
// narrower-than-whole-repository WRITE grant (scopeguard.isAllowed's own
// literal-prefix match, see createChildWorkItem's own doc comment) so a
// real diff outside it genuinely violates, rather than trivially passing
// the unrestricted default every earlier V5-15 scenario uses.
func (f *v5AcceptFixture) createChildWorkItemWithPathScopes(t *testing.T, parentWorkItemID, title string, access workdomain.RepositoryAccess, pathScopes []string) appwork.CreateChildWorkItemResult {
	t.Helper()
	ctx := context.Background()
	result, err := appwork.CreateChildWorkItem(ctx, f.uow, f.ids, testCmd("v5a-child-"+title, ports.ProjectScope(v5AcceptProjectID), "CreateChildWorkItem"), appwork.CreateChildWorkItemRequest{
		ParentWorkItemID: parentWorkItemID, Title: title, ParentJoinPolicy: "v5-accept-child",
		EffectiveScope: []appwork.ScopeGrantRequest{{
			RepositoryID: v5AcceptRepositoryID, Access: string(access), PathScopes: pathScopes, Reason: "v5-15 acceptance",
		}},
	})
	if err != nil {
		t.Fatalf("CreateChildWorkItem(%s): %v", title, err)
	}
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
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

// repositoryWorkspaceHandle resolves repositoryID's own real
// ports.WorkspaceHandle at generation for workspaceSetID — the one
// sanctioned bridge from the opaque RepositoryWorkspace row to something
// f.provider/CreateLocalCommit can act on directly.
func (f *v5AcceptFixture) repositoryWorkspaceHandle(t *testing.T, workspaceSetID, repositoryID string, generation uint64) ports.WorkspaceHandle {
	t.Helper()
	ctx := context.Background()
	var locator string
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		repoWorkspace, err := tx.Work().GetRepositoryWorkspace(ctx, workspaceSetID, repositoryID, generation)
		if err != nil {
			return err
		}
		locator = repoWorkspace.Locator
		return nil
	}); err != nil {
		t.Fatalf("GetRepositoryWorkspace(%s, %s, %d): %v", workspaceSetID, repositoryID, generation, err)
	}
	handle, err := ports.NewWorkspaceHandle(locator)
	if err != nil {
		t.Fatalf("NewWorkspaceHandle(%s): %v", locator, err)
	}
	return handle
}

// artifactObjectPath reconstructs the real, on-disk path
// internal/adapters/artifactstore.Store itself would resolve locator to —
// "<root>/objects/<hex[0:2]>/<hex[2:4]>/<hex>", mirroring that package's
// own private objectPath exactly (confirmed by reading
// filesystem.go — a locator is always "sha256:" plus 64 lowercase hex
// characters). f.artifacts is only ever a ports.ArtifactStore interface
// value (never the concrete *artifactstore.Store), so a scenario that
// needs to corrupt real bytes on disk — proving Verify/Open really detect
// tampering, V5-15B's own "artifact tamper" injection — has no other real
// way to reach the exact file a real Put call wrote.
func (f *v5AcceptFixture) artifactObjectPath(t *testing.T, locator string) string {
	t.Helper()
	const prefix = "sha256:"
	if len(locator) != len(prefix)+64 || locator[:len(prefix)] != prefix {
		t.Fatalf("artifactObjectPath: locator %q is not a well-formed sha256 locator", locator)
	}
	hexDigest := locator[len(prefix):]
	return filepath.Join(f.fixtureRoot, "artifacts", "objects", hexDigest[0:2], hexDigest[2:4], hexDigest)
}

// --- job/worker pool wiring ---

// registerHandlers wires every real V4/V5 job handler this package's own
// scenarios need into one workerpool.Registry — mirrors
// internal/integration's own registerRuntimeEngineHandlers, but with a
// REAL workspaceprovision.Handler (f.provider, real git) rather than
// runtimeengine_test.go's own scriptedWorkspaceProvider, and executor
// plugged in by the caller (a *runtime.NodeExecutorRouter for every real
// scenario in this package — see this file's own package doc comment for
// why a fake single-kind executor would violate the user's own "no DB
// shortcut" contract). idPrefix must be fresh (never reused) across a
// real restart within the same test, exactly like runtimeengine_test.go's
// own "h"/"h2" convention — reusing one would collide with ids that
// prefix already durably minted in this same database.
func (f *v5AcceptFixture) registerHandlers(executor ports.NodeExecutor, idPrefix string) *workerpool.Registry {
	return f.registerHandlersWithAgents(executor, idPrefix, agentregistry.Empty())
}

// registerHandlersWithAgents is registerHandlers with an explicit
// *agentregistry.Registry — needed by any real scenario with a real AGENT
// node: ExecuteNodeHandler's own admission phase (admission.go's own
// runAdmissionProbePhase, GC-INV-23's drift check) resolves the pinned
// AdapterBuild's own provider against THIS SAME registry, not against
// whatever internal registry an AgentNodeExecutor happens to hold — the
// two must be the identical instance (wrapping the identical real
// claude.Adapter) or a real Attempt would see its own drift check resolve
// a DIFFERENT executor than the one that actually ran. Always wires the
// permissive fake.IsolationEnforcementChecker{} (Err: nil) — every V5-15A/B
// scenario needs isolation to pass so it can reach its own real node
// executor; a scenario that needs isolation to genuinely FAIL (V5-15C's
// own "isolation unavailable") uses registerHandlersWithIsolation directly.
func (f *v5AcceptFixture) registerHandlersWithAgents(executor ports.NodeExecutor, idPrefix string, agents *agentregistry.Registry) *workerpool.Registry {
	return f.registerHandlersWithIsolation(executor, idPrefix, agents, fake.IsolationEnforcementChecker{})
}

// registerHandlersWithIsolation is registerHandlersWithAgents with an
// explicit ports.IsolationEnforcementChecker — V5-15C's own "isolation
// unavailable" scenario needs the REAL, non-fake
// process.IsolationChecker{} (internal/adapters/process/isolation.go),
// which structurally always returns ErrIsolationEnforcementUnavailable for
// policy.IsolationTierEnforcedIsolated — a real production rejection, not
// a fake configured to simulate one.
func (f *v5AcceptFixture) registerHandlersWithIsolation(executor ports.NodeExecutor, idPrefix string, agents *agentregistry.Registry, isolation ports.IsolationEnforcementChecker) *workerpool.Registry {
	handlerIDs := idsource.NewSequential(idPrefix)
	registry := workerpool.NewRegistry()
	registry.Register(runtime.AdvanceRunJobKind, runtime.NewScheduler(f.uow, handlerIDs))
	registry.Register(runtime.ScheduleNodeRunJobKind, runtime.NewNodeSchedulingHandler(f.uow, handlerIDs, fake.NewRuntimeExecutionConfigProvider()))
	registry.Register(runtime.ExecuteNodeJobKind, runtime.NewExecuteNodeHandler(f.uow, handlerIDs, executor, clock.System{}, isolation, agents))
	registry.Register(runtime.WaitTimerJobKind, runtime.NewWaitTimeoutHandler(f.uow, handlerIDs))
	registry.Register(runtime.ApprovalTimerJobKind, runtime.NewApprovalTimeoutHandler(f.uow, handlerIDs))
	registry.Register(runtime.RequestScopeExpansionJobKind, runtime.NewRequestScopeExpansionHandler(f.uow, handlerIDs))
	registry.Register(appwork.ScopeExpansionReconcileJobKind, runtime.NewScopeExpansionReconcileHandler(f.uow, handlerIDs))
	registry.Register(appwork.WorkspaceProvisionJobKind, workspaceprovision.New(f.uow, handlerIDs, f.provider))
	registry.Register(runtime.CancelRunCoordinatorJobKind, runtime.NewCancelRunCoordinatorHandler(f.uow, handlerIDs))
	registry.Register(runtime.RecoveryReaperJobKind, runtime.NewRecoveryReaperHandler(f.uow, handlerIDs, clock.System{}, f.store, f.store, f.store))
	return registry
}

// newCommandExecutor/newGateExecutor build real CommandNodeExecutor/
// GateNodeExecutor instances against f's own real adapters — a single
// real *sqlite.Store structurally satisfies WriteLeaseManager,
// agentevents.CheckpointStore, worker.InterruptionRecoveryStore AND
// worker.WorkspaceReconciler all at once (confirmed by reading
// internal/app/runtime/recovery_reaper.go's own identical "store, store,
// store" wiring for the equivalent three spike-era interfaces).
func (f *v5AcceptFixture) newCommandExecutor() *runtime.CommandNodeExecutor {
	return runtime.NewCommandNodeExecutor(
		f.uow, f.ids, f.artifacts, f.provider, f.store, f.supervisor, f.secrets,
		v5AcceptEventRegistry(), redact.NewMatcher(), f.store, clock.System{}, f.store, f.store,
	)
}

func (f *v5AcceptFixture) newGateExecutor() *runtime.GateNodeExecutor {
	return runtime.NewGateNodeExecutor(
		f.uow, f.ids, f.artifacts, f.provider, f.supervisor, f.secrets,
		v5AcceptEventRegistry(), redact.NewMatcher(), f.store, clock.System{}, f.store, f.store,
	)
}

func v5AcceptEventRegistry() *eventschema.Registry {
	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	return registry
}

// startPool starts a real workerpool.Pool (Concurrency: 1, matching
// internal/integration's own runtimeengine_test.go convention so a
// deterministic tie-break never depends on true concurrency) against
// f.store/registry, returning a stop func the caller MUST call exactly
// once (directly, or via defer) before ever restarting f's own store —
// a pool still polling a store that gets closed underneath it is a bug
// this fixture never wants to hide.
func (f *v5AcceptFixture) startPool(t *testing.T, registry *workerpool.Registry) (pool *workerpool.Pool, stop func()) {
	t.Helper()
	return f.startPoolWithConfig(t, registry, workerpool.Config{
		Concurrency: 1, Owner: "v5-accept", LeaseTTL: 2 * time.Second,
		HeartbeatEvery: 200 * time.Millisecond, PollInterval: 10 * time.Millisecond,
		ShutdownGrace: 2 * time.Second, RecoveryInterval: 200 * time.Millisecond,
	})
}

// startPoolWithConfig is startPool with an explicit workerpool.Config —
// V5-15D's own "cancel during a mutating attempt" scenario needs a real,
// wider LeaseTTL/HeartbeatEvery than this fixture's own 2s/200ms default:
// empirically confirmed (not guessed) that this fixture's own default
// LeaseTTL leaves too little real margin for a real Attempt's own driving
// job to survive real heartbeat-write contention while a real, genuinely
// long-running process (git commit + sleep) is in flight, causing its
// still-in-flight lease to expire and the job to be reclaimed out from
// under itself before V5-08C's own real cancellation poller (a fixed,
// non-configurable 500ms interval, execute.go's own cancellationPollInterval)
// ever gets a chance to act. Every other real timing behavior this
// package's own scenarios depend on (the 500ms cancellation poll itself,
// ProcessSupervisor's own kill/grace escalation) is unaffected by this —
// only the pool's own lease bookkeeping gets more real headroom.
func (f *v5AcceptFixture) startPoolWithConfig(t *testing.T, registry *workerpool.Registry, config workerpool.Config) (pool *workerpool.Pool, stop func()) {
	t.Helper()
	pool, err := workerpool.New(f.store, registry, config)
	if err != nil {
		t.Fatalf("workerpool.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- pool.Run(ctx) }()
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		<-errCh
	}
	return pool, stop
}

// --- polling ---

func (f *v5AcceptFixture) waitForJobState(t *testing.T, jobID string, want ports.JobState) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(8 * time.Second)
	var last ports.JobState
	for time.Now().Before(deadline) {
		state, err := f.store.LoadDurableJobState(ctx, ports.JobID(jobID))
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

func (f *v5AcceptFixture) waitForNodeRunState(t *testing.T, runID, nodeKey string, want runtimedomain.NodeRunState) runtimedomain.NodeRun {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(8 * time.Second)
	var last runtimedomain.NodeRun
	var found bool
	for time.Now().Before(deadline) {
		var nodeRuns []runtimedomain.NodeRun
		if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
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

// waitForRunState polls until runID reaches want, or fails with the full
// real domain-event trace for this fixture's own project — a scenario
// this package drives fails deep inside a real router/executor/job-
// handler chain the test itself never touches directly, so a bare
// timeout with no trace is rarely actionable; every later V5-15 scenario
// (B/C/D/E, each with its own fault-injection paths) benefits from this
// same diagnostic, not just PR A's own happy path.
func (f *v5AcceptFixture) waitForRunState(t *testing.T, runID string, want runtimedomain.WorkflowRunState) runtimedomain.WorkflowRun {
	t.Helper()
	ctx := context.Background()
	// 20s, not 8s like the other two waitFor* helpers below: a real AGENT
	// scenario's own admission phase re-probes a real process
	// (adapterbuild.VerifyNoDrift spawning `fake-claude --version` again)
	// on top of the real task spawn itself, needing more real wall-clock
	// headroom than a COMMAND/MACHINE_GATE-only scenario ever does.
	deadline := time.Now().Add(20 * time.Second)
	var last runtimedomain.WorkflowRun
	for time.Now().Before(deadline) {
		var run runtimedomain.WorkflowRun
		if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			run, err = tx.Runtime().GetWorkflowRun(ctx, runID)
			return err
		}); err == nil {
			last = run
			if run.State == want {
				return run
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if events, err := f.store.ListDomainEventsForProject(context.Background(), v5AcceptProjectID); err == nil {
		for _, e := range events {
			t.Logf("event trace: type=%s aggregate=%s/%s payload=%s", e.EventType, e.AggregateType, e.AggregateID, e.PayloadJSON)
		}
	}
	t.Fatalf("run %s did not reach state %s within the deadline; last observed = %+v", runID, want, last)
	return runtimedomain.WorkflowRun{}
}

// --- command construction ---

// testCmd builds a minimal, valid ports.Command for one idempotency key
// — mirrors internal/integration's own reCommand exactly.
func testCmd(idempotencyKey string, scope ports.CommandScope, commandType string) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "operator-1",
		CorrelationID: "corr-v5accept", Scope: scope, RequestedAt: time.Now().UTC(),
		Type: commandType, RequestHash: "hash-" + idempotencyKey,
	}
}

// --- definition publishing helpers ---

func publishPolicyVersion(t *testing.T, uow ports.UnitOfWork, definitionID, versionID string, doc policy.PolicyDocument) definition.VersionFields {
	t.Helper()
	ctx := context.Background()
	if _, err := definitions.CreateDefinition(ctx, uow, testCmd("v5a-def-"+definitionID, ports.InstallationScope(), "CreateDefinition"), definitions.CreateDefinitionRequest{
		DefinitionID: definitionID, Kind: definition.KindPolicy, Scope: definition.GlobalScope(), Name: "policy " + definitionID,
	}); err != nil {
		t.Fatalf("CreateDefinition(%s): %v", definitionID, err)
	}
	fields, err := definitions.PublishDefinitionVersion(ctx, uow, testCmd("v5a-pub-"+versionID, ports.InstallationScope(), "PublishDefinitionVersion"), definitions.PublishDefinitionVersionRequest{
		DefinitionID: definitionID, Kind: definition.KindPolicy,
		Compile: func() (definition.VersionFields, error) {
			return policy.Compile(
				policy.PolicyDefinition{ID: policy.PolicyDefinitionID(definitionID), Fields: definition.Fields{
					Kind: definition.KindPolicy, Scope: definition.GlobalScope(), Name: "policy", Status: definition.StatusDraft, Version: 1,
				}},
				policy.PublishRequest{
					VersionID: policy.PolicyVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
					Document: doc, PublishedBy: "operator-1", PublishedAt: time.Now().UTC(),
				},
			)
		},
	})
	if err != nil {
		t.Fatalf("PublishDefinitionVersion(%s): %v", versionID, err)
	}
	return fields
}

func publishSkillVersion(t *testing.T, uow ports.UnitOfWork, definitionID, versionID string, doc skill.SkillDocument) {
	t.Helper()
	ctx := context.Background()
	if _, err := definitions.CreateDefinition(ctx, uow, testCmd("v5a-def-"+definitionID, ports.InstallationScope(), "CreateDefinition"), definitions.CreateDefinitionRequest{
		DefinitionID: definitionID, Kind: definition.KindSkill, Scope: definition.GlobalScope(), Name: "skill " + definitionID,
	}); err != nil {
		t.Fatalf("CreateDefinition(%s): %v", definitionID, err)
	}
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, testCmd("v5a-pub-"+versionID, ports.InstallationScope(), "PublishDefinitionVersion"), definitions.PublishDefinitionVersionRequest{
		DefinitionID: definitionID, Kind: definition.KindSkill,
		Compile: func() (definition.VersionFields, error) {
			return skill.Compile(
				skill.SkillDefinition{ID: skill.SkillDefinitionID(definitionID), Fields: definition.Fields{
					Kind: definition.KindSkill, Scope: definition.GlobalScope(), Name: "skill", Status: definition.StatusDraft, Version: 1,
				}},
				skill.PublishRequest{
					VersionID: skill.SkillVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
					Document: doc, PublishedBy: "operator-1", PublishedAt: time.Now().UTC(),
				},
			)
		},
	}); err != nil {
		t.Fatalf("PublishDefinitionVersion(%s): %v", versionID, err)
	}
}

func publishCommandVersion(t *testing.T, uow ports.UnitOfWork, definitionID, versionID string, doc command.CommandDocument) {
	t.Helper()
	ctx := context.Background()
	if _, err := definitions.CreateDefinition(ctx, uow, testCmd("v5a-def-"+definitionID, ports.InstallationScope(), "CreateDefinition"), definitions.CreateDefinitionRequest{
		DefinitionID: definitionID, Kind: definition.KindCommand, Scope: definition.GlobalScope(), Name: "command " + definitionID,
	}); err != nil {
		t.Fatalf("CreateDefinition(%s): %v", definitionID, err)
	}
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, testCmd("v5a-pub-"+versionID, ports.InstallationScope(), "PublishDefinitionVersion"), definitions.PublishDefinitionVersionRequest{
		DefinitionID: definitionID, Kind: definition.KindCommand,
		Compile: func() (definition.VersionFields, error) {
			return command.Compile(
				command.CommandDefinition{ID: command.CommandDefinitionID(definitionID), Fields: definition.Fields{
					Kind: definition.KindCommand, Scope: definition.GlobalScope(), Name: "command", Status: definition.StatusDraft, Version: 1,
				}},
				command.PublishRequest{
					VersionID: command.CommandVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
					Document: doc, PublishedBy: "operator-1", PublishedAt: time.Now().UTC(),
				},
			)
		},
	}); err != nil {
		t.Fatalf("PublishDefinitionVersion(%s): %v", versionID, err)
	}
}

func publishGateVersion(t *testing.T, uow ports.UnitOfWork, definitionID, versionID string, doc gate.GateDocument) {
	t.Helper()
	ctx := context.Background()
	if _, err := definitions.CreateDefinition(ctx, uow, testCmd("v5a-def-"+definitionID, ports.InstallationScope(), "CreateDefinition"), definitions.CreateDefinitionRequest{
		DefinitionID: definitionID, Kind: definition.KindGate, Scope: definition.GlobalScope(), Name: "gate " + definitionID,
	}); err != nil {
		t.Fatalf("CreateDefinition(%s): %v", definitionID, err)
	}
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, testCmd("v5a-pub-"+versionID, ports.InstallationScope(), "PublishDefinitionVersion"), definitions.PublishDefinitionVersionRequest{
		DefinitionID: definitionID, Kind: definition.KindGate,
		Compile: func() (definition.VersionFields, error) {
			return gate.Compile(
				gate.GateDefinition{ID: gate.GateDefinitionID(definitionID), Fields: definition.Fields{
					Kind: definition.KindGate, Scope: definition.GlobalScope(), Name: "gate", Status: definition.StatusDraft, Version: 1,
				}},
				gate.PublishRequest{
					VersionID: gate.GateVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
					Document: doc, PublishedBy: "operator-1", PublishedAt: time.Now().UTC(),
				},
			)
		},
	}); err != nil {
		t.Fatalf("PublishDefinitionVersion(%s): %v", versionID, err)
	}
}

func publishWorkflowVersion(t *testing.T, uow ports.UnitOfWork, projectID, definitionID, versionID string, doc workflow.WorkflowDocument, dependencies workflow.DependencyManifest) workflow.WorkflowVersion {
	t.Helper()
	ctx := context.Background()
	pid := project.ProjectID(projectID)
	def := workflow.WorkflowDefinition{ID: workflow.WorkflowDefinitionID(definitionID), ProjectID: &pid, Name: "workflow " + definitionID, Status: workflow.DefinitionActive, Version: 1}
	candidate, err := workflow.Compile(def, workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID(versionID), VersionNumber: 1, Document: doc, Dependencies: dependencies,
		PublishedBy: "operator-1", PublishedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("compile workflow %s: %v", versionID, err)
	}
	var published workflow.WorkflowVersion
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		p, err := tx.Definitions().PublishWorkflowVersion(ctx, def, candidate)
		published = p
		return err
	}); err != nil {
		t.Fatalf("publish workflow %s: %v", versionID, err)
	}
	return published
}

// --- policy document builders ---

func v5AcceptAttemptPolicyDocument(timeoutSeconds uint32) policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryAttempt,
		Attempt:  &policy.AttemptRules{MaxAttempts: 3, BackoffSeconds: 1, TimeoutSeconds: timeoutSeconds},
	}
}

func v5AcceptPermissionPolicyDocument() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryPermission,
		Permission: &policy.PermissionRules{
			IsolationTier: policy.IsolationTierEnforcedIsolated, GrantedCapabilities: []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"},
		},
	}
}

// --- skill resource / script fixtures ---

// v5AcceptTwoResourceSkillDocument builds a SkillDocument with exactly
// two Global resources (so no Selector is required for either to apply)
// — one per real script this package's real COMMAND/MACHINE_GATE nodes
// spawn.
func v5AcceptTwoResourceSkillDocument(key1, instruction1, key2, instruction2 string) skill.SkillDocument {
	provenance := skill.Provenance{Owner: "v5accept", Source: "fixture", Revision: "v1"}
	return skill.SkillDocument{Resources: []skill.Resource{
		{Key: key1, Instruction: instruction1, Priority: definition.PriorityGuidance, Global: true, Provenance: provenance},
		{Key: key2, Instruction: instruction2, Priority: definition.PriorityGuidance, Global: true, Provenance: provenance},
	}}
}

// resourceContentHash re-derives skill.ResourceIdentities' own
// ContentHash for one resource — mirrors internal/app/runtime's own
// identical test helper (schedule_contextresourcerefs_test.go).
func resourceContentHash(t *testing.T, ownerVersionID, resourceKey string, doc skill.SkillDocument) string {
	t.Helper()
	identities, err := skill.ResourceIdentities(skill.SkillVersionID(ownerVersionID), doc)
	if err != nil {
		t.Fatalf("ResourceIdentities: %v", err)
	}
	for _, id := range identities {
		if id.Identity.ResourceKey == resourceKey {
			return id.Identity.ContentHash
		}
	}
	t.Fatalf("resource key %s not found in computed identities", resourceKey)
	return ""
}

// v5AcceptMakerResourceKey/v5AcceptGateResourceKey name the two real,
// cross-platform scripts this package's real COMMAND/MACHINE_GATE nodes
// spawn via a real ports.ProcessSupervisor — chosen per-OS at test time
// (each CI job builds/runs on its own native OS, matrix
// windows-latest/ubuntu-latest, .github/workflows/spike-gate.yml) rather
// than cross-compiled: a .bat is CreateProcess-launchable directly on
// Windows with no shell wrapper (empirically confirmed — Windows'
// CreateProcess falls back to COMSPEC for a .bat/.cmd target), a .sh
// with the executable bit (materializeExecutable's own 0o755) runs via
// the kernel's shebang support on Linux/Darwin.
//
// The marker file both scripts share lives OUTSIDE repo-a's own git
// worktree entirely (markerPath, an absolute path under this fixture's
// own root, passed to both scripts as a real ArgvLiteral) — a deliberate
// design forced by a real architectural constraint this file's own
// development actually ran into and confirmed by reading the code, not
// guessed: GateNodeExecutor's own buildEvidence(strictReadOnly=true)
// (agent_node_executor_resources.go's own validateStrictlyReadOnlyDiffs)
// requires the real git diff on EVERY mount an Attempt resolves — not
// just ones its own script happens to reference — to be COMPLETELY EMPTY
// relative to this WorkflowRun's own immutable base revision
// (schedule.go: `baseRevisionSet := manifest.BaseRevisionSet`, pinned
// ONCE at StartWorkflowRun and reused unchanged by every later NodeRun's
// own ContextSnapshot in the same Run — there is no "latest revision"
// that updates mid-run). A MACHINE_GATE's own mount set always covers
// every repository the owning WorkItem has ANY EffectiveScope grant
// over — so if test_a (COMMAND, same WorkItem) had actually written
// inside repo-a's own working tree, gate_b would ALWAYS see a non-empty
// diff there and ALWAYS fail strict-read-only, regardless of write scope
// or commit state; empirically confirmed both ways (an uncommitted write
// and a real `git commit` of that write were each tried and each still
// failed this exact check). Keeping the maker's own real output
// completely outside every repository mount sidesteps this cleanly: the
// gate's own real spawned process still reports PASS only by actually
// finding that file on disk — a real, end-to-end-verified "PASS verdict
// derived from the COMMAND's own real output," never a canned answer —
// while repo-a's own working tree stays genuinely untouched throughout
// the Run, satisfying GateNodeExecutor's own hard invariant for real
// rather than working around it.
func v5AcceptScripts(markerPath string) (makerKey, makerScript, gateKey, gateScript string) {
	if stdruntime.GOOS == "windows" {
		return "maker.bat", "@echo off\r\necho marker> \"" + markerPath + "\"\r\nexit /b 0\r\n",
			"gate.bat", "@echo off\r\nif exist \"" + markerPath + "\" (\r\n  echo {\"" + v5AcceptGateEvidenceKey + "\":{\"verdict\":\"PASS\"}}\r\n) else (\r\n  echo {\"" + v5AcceptGateEvidenceKey + "\":{\"verdict\":\"FAIL\"}}\r\n)\r\nexit /b 0\r\n"
	}
	return "maker.sh", "#!/bin/sh\necho marker > \"" + markerPath + "\"\nexit 0\n",
		"gate.sh", "#!/bin/sh\nif [ -f \"" + markerPath + "\" ]; then\n  echo '{\"" + v5AcceptGateEvidenceKey + "\":{\"verdict\":\"PASS\"}}'\nelse\n  echo '{\"" + v5AcceptGateEvidenceKey + "\":{\"verdict\":\"FAIL\"}}'\nfi\nexit 0\n"
}

// v5AcceptGateEvidenceKey is the one gate.Criterion.EvidenceKey this
// package's own gate script reports and policy.CompletionRules requires
// — a single shared constant so the script's own JSON key, the
// authoring-time Criterion, and the CompletionPolicy's own
// RequiredEvidenceKinds can never silently drift apart from each other.
const v5AcceptGateEvidenceKey = "OUTPUT_VERIFIED"
