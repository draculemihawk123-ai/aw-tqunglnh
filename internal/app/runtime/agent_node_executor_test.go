package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// bridgeFakeWorkspaceProvider is AgentNodeExecutor's own E2E-test double for
// ports.WorkspaceProvider — proves the bridge's own orchestration (mount
// resolution, evidence staging) without a real git repository;
// gitworktree's own package tests and sink_sqlite_test.go's own
// TestSinkSQLite_DiffExceedsEffectiveScope already separately prove real
// Diff/WorkingDirectory correctness (the same "don't require the whole
// subprocess machinery for a concern one layer below it" precedent
// contract_test.go already established for provider adapters, extended
// here one layer up).
type bridgeFakeWorkspaceProvider struct {
	diff    ports.WorkspaceDiff
	diffErr error
	// captureRevision is consulted by handleMutatingCancellation
	// (agent_node_executor_cancellation.go) as the live "current on-disk
	// revision" a cancelled mutating attempt is reconciled against — tests
	// set this to fixtureFixtureRepo1PinnedRevision (clean) or any other
	// value (mutation observed) to control ReconcileMutatingAttempt's own
	// verdict.
	captureRevision workspace.Revision
}

func (f *bridgeFakeWorkspaceProvider) Provision(context.Context, ports.ProvisionSpec) (ports.WorkspaceHandle, error) {
	return ports.NewWorkspaceHandle("bridge-fixture-handle")
}
func (f *bridgeFakeWorkspaceProvider) Inspect(context.Context, ports.WorkspaceHandle) (ports.WorkspaceInspection, error) {
	return ports.WorkspaceInspection{}, nil
}
func (f *bridgeFakeWorkspaceProvider) CaptureRevision(context.Context, ports.WorkspaceHandle) (workspace.Revision, error) {
	return f.captureRevision, nil
}
func (f *bridgeFakeWorkspaceProvider) Diff(context.Context, ports.WorkspaceHandle, workspace.Revision) (ports.WorkspaceDiff, error) {
	return f.diff, f.diffErr
}
func (f *bridgeFakeWorkspaceProvider) Release(context.Context, ports.WorkspaceHandle) error {
	return nil
}
func (f *bridgeFakeWorkspaceProvider) WorkingDirectory(context.Context, ports.WorkspaceHandle) (string, error) {
	return "bridge-fixture-working-directory", nil
}

var _ ports.WorkspaceProvider = (*bridgeFakeWorkspaceProvider)(nil)

// bridgeFakeWriteLeaseManager is a minimal, in-memory ports.WriteLeaseManager
// — none exists in the shared ports/fake package (work.go's own doc
// comment: deep fencing edge cases are sqlite-only), but AgentNodeExecutor's
// own E2E test needs SOME AcquireWriteLeases implementation to exercise
// its own real call site (execute.go's own doc comment names the bridge as
// "the first caller with a genuine reason to... call AcquireWriteLeases").
type bridgeFakeWriteLeaseManager struct {
	// released records every ReleaseWriteLeases call — V5-08C's own
	// cancellation-reconciliation path (handleMutatingCancellation) is a
	// real, legitimate caller now (release only after reconciliation
	// completes), unlike every other pre-V5-08C bridge test which never
	// reaches this call at all.
	released [][]ports.WriteLeaseGrant
	// acquireCalls counts every AcquireWriteLeases call — V5-12's own
	// CHECKER-role tests assert this stays zero (forced read-only mounts
	// mean resolveExecutionResources never builds a non-empty writeTargets
	// list for a CHECKER, so it never calls this method at all).
	acquireCalls int
}

func (m *bridgeFakeWriteLeaseManager) AcquireWriteLeases(_ context.Context, req ports.AcquireWriteLeasesRequest) ([]ports.WriteLeaseGrant, error) {
	m.acquireCalls++
	grants := make([]ports.WriteLeaseGrant, 0, len(req.Targets))
	for i, target := range req.Targets {
		grants = append(grants, ports.WriteLeaseGrant{
			RepositoryID: target.RepositoryID, RepositoryWorkspaceID: target.RepositoryWorkspaceID, Generation: target.Generation,
			FenceToken: uint64(i + 1), HolderJobID: req.JobLease.JobID, HolderJobLeaseToken: req.JobLease.Token,
			HolderAttemptID: req.AttemptID, Owner: req.JobLease.Owner, LeaseUntil: time.Now().Add(req.TTL),
		})
	}
	return grants, nil
}
func (m *bridgeFakeWriteLeaseManager) HeartbeatWriteLeases(context.Context, []ports.WriteLeaseGrant, time.Duration) ([]ports.WriteLeaseGrant, error) {
	return nil, errors.New("bridgeFakeWriteLeaseManager: HeartbeatWriteLeases must not be called")
}
func (m *bridgeFakeWriteLeaseManager) ValidateWriteLease(context.Context, ports.WriteLeaseGrant) error {
	return errors.New("bridgeFakeWriteLeaseManager: ValidateWriteLease must not be called")
}
func (m *bridgeFakeWriteLeaseManager) ReleaseWriteLeases(_ context.Context, grants []ports.WriteLeaseGrant) error {
	m.released = append(m.released, grants)
	return nil
}

var _ ports.WriteLeaseManager = (*bridgeFakeWriteLeaseManager)(nil)

// bridgeFakeAgentExecutor is a minimal, scripted ports.AgentExecutor —
// internal/adapters/providers' own contract_test.go already proves a real
// AgentExecutor.Start correctly translates raw provider output into
// ports.AgentEvent calls; AgentNodeExecutor's own tests only need SOME
// executor emitting a plausible terminal event sequence through the sink
// it is handed and returning a scripted ports.AgentExecutionResult.
type bridgeFakeAgentExecutor struct {
	result ports.AgentExecutionResult
	err    error
	events []ports.AgentEventKind
}

func (f *bridgeFakeAgentExecutor) Capabilities(context.Context) (ports.AgentCapabilities, error) {
	return ports.AgentCapabilities{Provider: "fake-provider", AdapterVersion: "v1", ProtocolVersion: "v1", SupportsStart: true}, nil
}

func (f *bridgeFakeAgentExecutor) Start(ctx context.Context, request ports.AgentExecutionRequest, sink ports.AgentEventSink) (ports.AgentExecutionResult, error) {
	var sequence uint64
	for _, kind := range f.events {
		sequence++
		if err := sink.Accept(ctx, ports.AgentEvent{AttemptID: request.AttemptID, Sequence: sequence, Kind: kind, ObservedAt: time.Now().UTC()}); err != nil {
			return ports.AgentExecutionResult{}, err
		}
	}
	result := f.result
	result.AttemptID = request.AttemptID
	return result, f.err
}

func (f *bridgeFakeAgentExecutor) Resume(context.Context, ports.AgentExecutionRequest, ports.ProviderSessionRef, ports.AgentEventSink) (ports.AgentExecutionResult, error) {
	return ports.AgentExecutionResult{}, errors.New("bridgeFakeAgentExecutor: Resume must not be called (ADR-005: every Attempt always Starts fresh)")
}

func (f *bridgeFakeAgentExecutor) Cancel(context.Context, ports.ExecutionAttemptID) error { return nil }

var _ ports.AgentExecutor = (*bridgeFakeAgentExecutor)(nil)

// bridgeFakeCheckpointStore is a trivial agentevents.CheckpointStore —
// NewSink requires one, but this fixture's own scripted event sequences
// never emit CHECKPOINT_PROPOSED, so it is never actually called.
type bridgeFakeCheckpointStore struct{}

func (bridgeFakeCheckpointStore) StoreCheckpoint(_ context.Context, checkpoint domainruntime.Checkpoint) (domainruntime.Checkpoint, error) {
	return checkpoint, nil
}

// fixtureRepo1PinnedRevision is the exact VCSObjectID readyFixture's own
// stubProvider pins repo-1 to (commands_test.go) — assembleRequestFixture's
// whole chain (scheduleFixture -> readyFixture -> workspaceprovision ->
// ContextSnapshot.Revisions -> AgentExecutionRequest.WorkspaceMounts) never
// changes this value, so a cancellation test reporting exactly this same
// string back from CaptureRevision proves "clean, no mutation observed";
// any other value proves the opposite.
const fixtureRepo1PinnedRevision = "cafebabecafebabecafebabecafebabecafebabe"

// bridgeFakeInterruptionStore is a minimal worker.InterruptionRecoveryStore
// backed by the SAME fake.UnitOfWork the rest of this fixture already uses
// (reusing its own already-tested TransitionExecutionAttempt CAS rather
// than hand-rolling a parallel state machine) — none exists in the shared
// ports/fake package (recovery_reaper_sqlite_test.go's own real-sqlite-only
// precedent), but AgentNodeExecutor's own V5-08C cancellation tests need
// SOME TerminateInterruptedAttempt implementation.
type bridgeFakeInterruptionStore struct {
	uow          *fake.UnitOfWork
	terminations []ports.AttemptTerminationUpdate
}

func (s *bridgeFakeInterruptionStore) AttemptHeldAnyWriteLease(context.Context, domainruntime.ExecutionAttemptID) (bool, error) {
	return false, errors.New("bridgeFakeInterruptionStore: AttemptHeldAnyWriteLease must not be called — the bridge already knows")
}

func (s *bridgeFakeInterruptionStore) TerminateInterruptedAttempt(ctx context.Context, update ports.AttemptTerminationUpdate) error {
	if err := s.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: string(update.AttemptID), ExpectedState: domainruntime.ExecutionAttemptRunning, ExpectedVersion: update.ExpectedVersion,
			NextState: update.NextState, TerminationReason: update.Reason,
		})
		return err
	}); err != nil {
		return err
	}
	s.terminations = append(s.terminations, update)
	return nil
}

// bridgeFakeWorkspaceReconciler is a minimal, in-memory
// worker.WorkspaceReconciler — see bridgeFakeInterruptionStore's own doc
// comment for why no shared fake exists yet.
type bridgeFakeWorkspaceReconciler struct {
	quarantined []ports.QuarantineRepositoryWorkspaceUpdate
}

func (r *bridgeFakeWorkspaceReconciler) LoadRepositoryWorkspaceRevision(context.Context, string) (string, error) {
	return "", errors.New("bridgeFakeWorkspaceReconciler: LoadRepositoryWorkspaceRevision must not be called — the bridge uses a live CaptureRevision instead")
}

func (r *bridgeFakeWorkspaceReconciler) QuarantineRepositoryWorkspace(_ context.Context, update ports.QuarantineRepositoryWorkspaceUpdate) error {
	r.quarantined = append(r.quarantined, update)
	return nil
}

// bridgeFixtureOptions lets each test override just the pieces it cares
// about; bridgeFixture below fills in golden-path defaults for the rest.
type bridgeFixtureOptions struct {
	diff            ports.WorkspaceDiff
	captureRevision workspace.Revision
	agentResult     ports.AgentExecutionResult
	agentErr        error
	agentEvents     []ports.AgentEventKind
	// role is V5-12 contract 3's own addition (2026-09-10) — empty (the
	// zero value, every existing test's own default) resolves to MAKER via
	// workflow.AgentNodeConfig.EffectiveRole(), identical to every pre-
	// V5-12 test's own unchanged behavior. Only the new CHECKER-role tests
	// below set this to workflow.AgentRoleChecker.
	role workflow.AgentRole
}

// bridgeFixture builds one fully-admitted, RUNNING ExecutionAttempt (reusing
// assembleRequestFixture's own real message/skill/AdapterBuild chain) plus
// the claimed EXECUTE_NODE JobLease a real ExecuteNodeHandler would have
// passed into Execute, and returns a ready-to-drive AgentNodeExecutor along
// with the fake interruption store/workspace reconciler V5-08C's own
// cancellation tests assert against.
func bridgeFixture(t *testing.T, opts bridgeFixtureOptions) (
	executor *runtime.AgentNodeExecutor, req ports.NodeExecutionRequest, uow *fake.UnitOfWork, ids idsource.Source,
	interruptions *bridgeFakeInterruptionStore, reconciler *bridgeFakeWorkspaceReconciler, writeLeases *bridgeFakeWriteLeaseManager,
) {
	t.Helper()
	ctx := context.Background()
	u, ids, store, runID, nodeRunID, attemptID := assembleRequestFixtureWithRole(t, opts.role)
	markAttemptRunning(t, u, runID, nodeRunID, attemptID)

	jobID := ports.JobID("job-" + attemptID)
	if err := u.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: jobID, ProjectID: "project-1", Kind: "EXECUTE_NODE",
			AggregateType: "ExecutionAttempt", AggregateID: attemptID, IdempotencyKey: "idem-" + attemptID,
		})
		return err
	}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	lease := ports.JobLease{JobID: jobID, Owner: "worker-1", Token: 1, LeaseUntil: time.Now().Add(time.Hour)}
	u.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(jobID), lease)

	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	agents, err := agentregistry.New(ctx, &bridgeFakeAgentExecutor{result: opts.agentResult, err: opts.agentErr, events: opts.agentEvents})
	if err != nil {
		t.Fatalf("agentregistry.New: %v", err)
	}

	interruptions = &bridgeFakeInterruptionStore{uow: u}
	reconciler = &bridgeFakeWorkspaceReconciler{}
	writeLeases = &bridgeFakeWriteLeaseManager{}
	executor = runtime.NewAgentNodeExecutor(
		u, ids, store, &bridgeFakeWorkspaceProvider{diff: opts.diff, captureRevision: opts.captureRevision}, writeLeases,
		agents, registry, redact.NewMatcher(), bridgeFakeCheckpointStore{}, clock.System{},
		interruptions, reconciler,
	)
	req = ports.NodeExecutionRequest{AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: lease}
	return executor, req, u, ids, interruptions, reconciler, writeLeases
}

// defaultInScopeDiff is the fixture's own default WorkspaceDiff for repo-1.
// seedEffectiveScope (schedule_test.go) grants repo-1 WRITE with
// PathScopes []string{"**"} — scopeguard.ValidateDiffs's own isAllowed
// matches this literally (equal to "**", or prefixed by "**/"), not as a
// glob wildcard, so the changed path itself must carry that exact prefix
// to land inside this fixture's own grant.
func defaultInScopeDiff() ports.WorkspaceDiff {
	return ports.WorkspaceDiff{
		RepositoryID:    "repo-1",
		CurrentRevision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", WorkspaceGeneration: 1},
		Files:           []ports.FileStatus{{Code: "M", Path: "**/src/main.go"}},
		Patch:           []byte("--- a/src/main.go\n+++ b/src/main.go\n"),
	}
}

// defaultReadOnlyDiff is GateNodeExecutor's own fixture default (2026-09-10,
// buildEvidence's strictReadOnly parameter) — unlike defaultInScopeDiff,
// Files is empty: a real, correctly-behaving Gate evaluator never changes
// anything in any of its own mounts (GC-INV-25's own "scratch output nằm
// ngoài source workspace"), so the fixture's own default diff must reflect
// that, not defaultInScopeDiff's "a file changed but stayed within write
// scope" shape every AGENT/COMMAND test fixture legitimately still wants.
func defaultReadOnlyDiff() ports.WorkspaceDiff {
	return ports.WorkspaceDiff{
		RepositoryID:    "repo-1",
		CurrentRevision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", WorkspaceGeneration: 1},
	}
}

func loadAttemptVersion(t *testing.T, uow *fake.UnitOfWork, attemptID string) uint64 {
	t.Helper()
	var version uint64
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(context.Background(), attemptID)
		if err != nil {
			return err
		}
		version = attempt.Version
		return nil
	}); err != nil {
		t.Fatalf("load attempt version: %v", err)
	}
	return version
}

// TestAgentNodeExecutor_Success_BuildsEvidenceAndFinalizesEndToEnd is
// V5-08B's own golden-path E2E test: real request assembly (V5-08B0), real
// mount/write-lease resolution, a real agentevents.Sink fenced with a real
// JobLease, a scripted successful AgentExecutor, real post-quiescence diff/
// scope validation, a real ORPHAN artifact insert, and — driving
// FinalizeExecutionAttempt with the exact NodeExecutionResult the bridge
// proposed — real ORPHAN->ATTACHED promotion, a real completion Checkpoint,
// and a real NodeRun advance. Proves every new V5-08B piece composes
// correctly, not just in isolation.
func TestAgentNodeExecutor_Success_BuildsEvidenceAndFinalizesEndToEnd(t *testing.T) {
	executor, req, uow, ids, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
	})
	ctx := context.Background()

	result, err := executor.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != domainruntime.ExecutionAttemptSucceeded {
		t.Fatalf("result = %+v, want SUCCEEDED", result)
	}
	// agentExecutableDocument's own "implement" node declares a single
	// outcome ("done") — the bridge must derive it itself; the scripted
	// executor above never set ProposedOutcome.
	if result.SelectedOutcome != "done" {
		t.Fatalf("result.SelectedOutcome = %q, want done", result.SelectedOutcome)
	}
	if result.Evidence == nil {
		t.Fatal("result.Evidence is nil, want a populated finalization evidence bundle")
	}
	if len(result.Evidence.DiffManifestArtifacts) != 1 || result.Evidence.DiffManifestArtifacts[0].RepositoryID != "repo-1" {
		t.Fatalf("result.Evidence.DiffManifestArtifacts = %+v, want exactly one entry for repo-1", result.Evidence.DiffManifestArtifacts)
	}
	if result.Evidence.CompletionCheckpointID == "" || result.Evidence.TerminalEventSequence == 0 {
		t.Fatalf("result.Evidence = %+v, incomplete", result.Evidence)
	}
	if result.Evidence.ProposedOutcome == nil || result.Evidence.ProposedOutcome.Value != "done" ||
		result.Evidence.ProposedOutcome.Source != ports.AgentOutcomeDerivedSingleAllowed {
		t.Fatalf("result.Evidence.ProposedOutcome = %+v, want a derived done proposal", result.Evidence.ProposedOutcome)
	}

	// The diff manifest artifact must exist as ORPHAN before finalize ever
	// runs — proving the bridge's own short prep transaction (protocol
	// phase 2) really committed independently of finalize's own later
	// transaction.
	artifactID := result.Evidence.DiffManifestArtifacts[0].ArtifactID
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		record, err := tx.Artifacts().GetArtifact(ctx, artifactID)
		if err != nil {
			return err
		}
		if record.AttachState != "ORPHAN" {
			t.Fatalf("diff manifest artifact AttachState = %s, want ORPHAN before finalize", record.AttachState)
		}
		return nil
	}); err != nil {
		t.Fatalf("load diff manifest artifact before finalize: %v", err)
	}

	version := loadAttemptVersion(t, uow, req.AttemptID)
	finalizeResult, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: req.RunID, NodeRunID: req.NodeRunID, AttemptID: req.AttemptID, ExpectedVersion: version,
		NextState: result.State, TerminationReason: result.TerminationReason, SelectedOutcome: result.SelectedOutcome,
		JobLease: req.JobLease, CorrelationID: "corr-1", Evidence: result.Evidence,
	})
	if err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}
	if !finalizeResult.Advanced {
		t.Fatal("FinalizeExecutionAttempt did not advance the NodeRun")
	}

	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		record, err := tx.Artifacts().GetArtifact(ctx, artifactID)
		if err != nil {
			return err
		}
		if record.AttachState != "ATTACHED" {
			t.Fatalf("diff manifest artifact AttachState after finalize = %s, want ATTACHED", record.AttachState)
		}
		return nil
	}); err != nil {
		t.Fatalf("load diff manifest artifact after finalize: %v", err)
	}
}

// TestAgentNodeExecutor_LeaseLostMidExecution_ReturnsIndeterminate proves
// V5-08B's own locked provider-loss mapping (mapping #4): a JobLease lost
// mid-execution — simulated here by another worker re-claiming the exact
// job the Sink is fenced against — must never be classified as a definite
// FAILED; it must return ErrIndeterminateExecution instead, leaving the
// Attempt for the crash-recovery path to resolve (isFinalizableExecutionAttemptState
// accepts neither LOST nor INDETERMINATE).
func TestAgentNodeExecutor_LeaseLostMidExecution_ReturnsIndeterminate(t *testing.T) {
	executor, req, uow, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
		// Sink.Flush is a safe no-op on an empty buffer (its own doc
		// comment) — at least one buffered event is required so the fake
		// executor's own Start->Flush call path actually attempts a real
		// write and observes the stolen lease below, rather than never
		// touching the database at all.
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted},
	})
	ctx := context.Background()

	// Steal the job lease before Execute ever runs — the Sink's own
	// validateFencingLocked (flushLocked, called when the buffered event
	// above is flushed) will observe a stale lease exactly like a real
	// worker heartbeat/re-claim race would produce.
	stolen := ports.JobLease{JobID: req.JobLease.JobID, Owner: "worker-2", Token: 2, LeaseUntil: time.Now().Add(time.Hour)}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(req.JobLease.JobID), stolen)

	_, err := executor.Execute(ctx, req)
	if !errors.Is(err, runtime.ErrIndeterminateExecution) {
		t.Fatalf("Execute error = %v, want ErrIndeterminateExecution", err)
	}
}

// TestAgentNodeExecutor_UnconfirmedQuiescenceOnMutatingAttempt_ReturnsIndeterminate
// proves the other half of V5-08B's own locked decision #3: a mutating
// attempt (this fixture's own repo-1 mount is WRITE) whose own
// AgentExecutionResult.TreeQuiesced is false must never be trusted as a
// definite terminal disposition, success OR failure — this is the
// scripted executor reporting a provider-level failure (Status: FAILED)
// with quiescence unconfirmed.
func TestAgentNodeExecutor_UnconfirmedQuiescenceOnMutatingAttempt_ReturnsIndeterminate(t *testing.T) {
	executor, req, _, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionFailed, TreeQuiesced: false},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
	})
	ctx := context.Background()

	_, err := executor.Execute(ctx, req)
	if !errors.Is(err, runtime.ErrIndeterminateExecution) {
		t.Fatalf("Execute error = %v, want ErrIndeterminateExecution", err)
	}
}

// TestAgentNodeExecutor_OutOfScopeDiff_RejectsAsScopeViolation proves the
// bridge itself — not just agentevents.Sink's own mid-run checkpoint check
// — validates the FINAL, post-quiescence diff against EffectiveScope
// (V5-08B's own locked decision #2) before ever proposing SUCCEEDED.
func TestAgentNodeExecutor_OutOfScopeDiff_RejectsAsScopeViolation(t *testing.T) {
	executor, req, _, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff: ports.WorkspaceDiff{
			RepositoryID: "repo-2", // not in EffectiveScope at all
			Files:        []ports.FileStatus{{Code: "M", Path: "unauthorized.txt"}},
		},
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
	})
	ctx := context.Background()

	result, err := executor.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != domainruntime.ExecutionAttemptFailed || result.TerminationReason != domainruntime.TerminationReasonScopeViolation ||
		result.ErrorCode != errorcode.CodeScopeViolation {
		t.Fatalf("result = %+v, want FAILED/SCOPE_VIOLATION/SCOPE_VIOLATION", result)
	}
}

// TestAgentNodeExecutor_CheckerRole_MutatingDiff_RejectsAsScopeViolation is
// V5-12 contract 3's own proof (2026-09-10): a CHECKER-role Attempt whose
// real post-quiescence diff is non-empty is rejected, even though the
// SAME diff (defaultInScopeDiff) would PASS for a MAKER at this identical
// node/EffectiveScope (see TestAgentNodeExecutor_Success_BuildsEvidenceAndFinalizesEndToEnd,
// which uses it as its own golden-path diff). Mirrors
// TestGateNodeExecutor_MountChangedDespiteReadOnly_FailsWithScopeViolation
// exactly, reusing the identical buildEvidence strictReadOnly mechanism
// for a CHECKER-role AGENT instead of a MACHINE_GATE. Also proves
// contract 3's own "no WriteLease" bar: writeLeases.acquireCalls stays
// zero — forceReadOnlyMounts (assemble_execution_request.go) already
// downgraded this Attempt's own mount to READ_ONLY before
// resolveExecutionResources ever built a writeTargets list from it.
func TestAgentNodeExecutor_CheckerRole_MutatingDiff_RejectsAsScopeViolation(t *testing.T) {
	executor, req, _, _, _, _, writeLeases := bridgeFixture(t, bridgeFixtureOptions{
		role:        workflow.AgentRoleChecker,
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
	})
	ctx := context.Background()

	result, err := executor.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != domainruntime.ExecutionAttemptFailed || result.TerminationReason != domainruntime.TerminationReasonScopeViolation ||
		result.ErrorCode != errorcode.CodeScopeViolation {
		t.Fatalf("result = %+v, want FAILED/SCOPE_VIOLATION/SCOPE_VIOLATION (a CHECKER must never accept a non-empty diff, even one in-scope for a MAKER)", result)
	}
	if writeLeases.acquireCalls != 0 {
		t.Fatalf("writeLeases.acquireCalls = %d, want 0 (a CHECKER's own mounts are always read-only, so no write lease is ever sought)", writeLeases.acquireCalls)
	}
}

// TestAgentNodeExecutor_CheckerRole_EmptyDiff_Succeeds proves the
// strictReadOnly check does not itself break a well-behaved CHECKER: an
// empty diff — the only legitimate shape for a real, correctly-behaving
// checker whose own mounts are read-only — still succeeds end-to-end.
func TestAgentNodeExecutor_CheckerRole_EmptyDiff_Succeeds(t *testing.T) {
	executor, req, _, _, _, _, writeLeases := bridgeFixture(t, bridgeFixtureOptions{
		role:        workflow.AgentRoleChecker,
		diff:        defaultReadOnlyDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
	})
	ctx := context.Background()

	result, err := executor.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != domainruntime.ExecutionAttemptSucceeded {
		t.Fatalf("result = %+v, want SUCCEEDED (a real, correctly-behaving CHECKER with an empty diff must not be rejected)", result)
	}
	if writeLeases.acquireCalls != 0 {
		t.Fatalf("writeLeases.acquireCalls = %d, want 0", writeLeases.acquireCalls)
	}
}

// TestAgentNodeExecutor_ProviderDeclaredFailure_ReturnsExecutionFailed is
// V5-08B's own locked provider-loss mapping row 2: the provider itself
// reported a determinate non-success (AgentExecutionStatus != Succeeded,
// no error, quiescence confirmed) — a definite FAILED, never indeterminate.
func TestAgentNodeExecutor_ProviderDeclaredFailure_ReturnsExecutionFailed(t *testing.T) {
	executor, req, _, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionFailed, TreeQuiesced: true},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
	})
	ctx := context.Background()

	result, err := executor.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != domainruntime.ExecutionAttemptFailed || result.TerminationReason != domainruntime.TerminationReasonExecutionFailed ||
		result.ErrorCode != errorcode.CodeExecutionFailed {
		t.Fatalf("result = %+v, want FAILED/EXECUTION_FAILED/EXECUTION_FAILED", result)
	}
}

// TestAgentNodeExecutor_ProviderUnavailableBareError_ReturnsProviderUnavailable
// is V5-08B's own locked provider-loss mapping row 1: a bare Go error from
// AgentExecutor.Start itself (not a lease-lost sentinel, not an
// unconfirmed-quiescence case) is a definite FAILED, classified
// PROVIDER_UNAVAILABLE rather than the generic EXECUTION_FAILED row 2 uses
// for a provider-REPORTED failure.
func TestAgentNodeExecutor_ProviderUnavailableBareError_ReturnsProviderUnavailable(t *testing.T) {
	executor, req, _, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionFailed, TreeQuiesced: true},
		agentErr:    errors.New("spawn: executable not found"),
	})
	ctx := context.Background()

	result, err := executor.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != domainruntime.ExecutionAttemptFailed || result.TerminationReason != domainruntime.TerminationReasonExecutionFailed ||
		result.ErrorCode != errorcode.CodeProviderUnavailable {
		t.Fatalf("result = %+v, want FAILED/EXECUTION_FAILED/PROVIDER_UNAVAILABLE", result)
	}
}

// TestFinalizeExecutionAttempt_TamperedEvidence_RejectsBeforeCommitting
// proves FinalizeExecutionAttempt's own phase-3 revalidation
// (validateAndAttachFinalizationEvidenceTx) actually distrusts the proposal it is
// handed — a TerminalEventSequence naming no real agent_events row must be
// rejected, and nothing (not the Attempt CAS, not the NodeRun advance)
// must have committed as a side effect of the attempt.
func TestFinalizeExecutionAttempt_TamperedEvidence_RejectsBeforeCommitting(t *testing.T) {
	executor, req, uow, ids, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
	})
	ctx := context.Background()

	result, err := executor.Execute(ctx, req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	tampered := *result.Evidence
	tampered.TerminalEventSequence = result.Evidence.TerminalEventSequence + 1000

	version := loadAttemptVersion(t, uow, req.AttemptID)
	if _, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: req.RunID, NodeRunID: req.NodeRunID, AttemptID: req.AttemptID, ExpectedVersion: version,
		NextState: result.State, TerminationReason: result.TerminationReason, SelectedOutcome: result.SelectedOutcome,
		JobLease: req.JobLease, CorrelationID: "corr-1", Evidence: &tampered,
	}); err == nil {
		t.Fatal("FinalizeExecutionAttempt accepted evidence naming a nonexistent terminal event sequence, want a fail-closed error")
	}

	// The Attempt must still be exactly where it was (RUNNING, unchanged
	// version) — the whole transaction must have rolled back, not just the
	// evidence-specific part of it.
	if after := loadAttemptVersion(t, uow, req.AttemptID); after != version {
		t.Fatalf("attempt version after rejected finalize = %d, want unchanged %d", after, version)
	}

	// The diff manifest artifact must still be ORPHAN — never attached
	// alongside a finalize that itself did not commit.
	artifactID := result.Evidence.DiffManifestArtifacts[0].ArtifactID
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		record, err := tx.Artifacts().GetArtifact(ctx, artifactID)
		if err != nil {
			return err
		}
		if record.AttachState != "ORPHAN" {
			t.Fatalf("diff manifest artifact AttachState after rejected finalize = %s, want still ORPHAN", record.AttachState)
		}
		return nil
	}); err != nil {
		t.Fatalf("load diff manifest artifact after rejected finalize: %v", err)
	}
}

// TestAgentNodeExecutor_CancelledWithoutDurableIntent_ReturnsIndeterminateExecution
// proves classifyCancellation's own locked rule (V5-08C): a confirmed-
// stopped process (AgentExecutionStatus == Cancelled) whose owning Run has
// NO durable RunCancellationIntent (CancelRun was never called — this
// fixture never calls it) must never be assumed CANCELLED just because ctx
// happened to be cancelled for some other reason (e.g. workerpool.Pool's
// own shutdown-grace escalation) — it stays the same safe
// ErrIndeterminateExecution default every other ambiguous case in this
// bridge already uses.
func TestAgentNodeExecutor_CancelledWithoutDurableIntent_ReturnsIndeterminateExecution(t *testing.T) {
	executor, req, _, _, interruptions, reconciler, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:            defaultInScopeDiff(),
		captureRevision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: fixtureRepo1PinnedRevision},
		agentResult:     ports.AgentExecutionResult{Status: ports.AgentExecutionCancelled, TreeQuiesced: true},
	})
	ctx := context.Background()

	_, err := executor.Execute(ctx, req)
	if !errors.Is(err, runtime.ErrIndeterminateExecution) {
		t.Fatalf("Execute error = %v, want ErrIndeterminateExecution", err)
	}
	if len(interruptions.terminations) != 0 {
		t.Fatalf("terminations = %+v, want none — no durable cancellation intent existed", interruptions.terminations)
	}
	if len(reconciler.quarantined) != 0 {
		t.Fatalf("quarantined = %+v, want none", reconciler.quarantined)
	}
}

// TestAgentNodeExecutor_MutatingCancellation_CleanRevision_TerminatesIndeterminateWithoutQuarantine
// proves V5-08C's own locked requirement for the case a mutating attempt IS
// genuinely, durably cancelled (a real runtime.CancelRun call, exactly the
// production entry point) and the workspace's own current revision — read
// live via CaptureRevision, confirmed only after TreeQuiesced — turns out
// unchanged from what this attempt was pinned to: the Attempt still becomes
// INDETERMINATE (a cancelled mutating attempt is never assumed clean just
// because the revision happens to match; only NOT quarantined), and
// WriteLeases are released only after that whole reconciliation completes.
func TestAgentNodeExecutor_MutatingCancellation_CleanRevision_TerminatesIndeterminateWithoutQuarantine(t *testing.T) {
	executor, req, uow, ids, interruptions, reconciler, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:            defaultInScopeDiff(),
		captureRevision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: fixtureRepo1PinnedRevision},
		agentResult:     ports.AgentExecutionResult{Status: ports.AgentExecutionCancelled, TreeQuiesced: true},
	})
	ctx := context.Background()

	if _, err := runtime.CancelRun(ctx, uow, ids, runtime.CancelRunRequest{
		RunID: req.RunID, Actor: "actor-1", Reason: "test cancellation",
	}); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	_, err := executor.Execute(ctx, req)
	if !errors.Is(err, runtime.ErrAttemptAlreadyTerminated) {
		t.Fatalf("Execute error = %v, want ErrAttemptAlreadyTerminated", err)
	}
	// V5-15D: the real cancellation-finalization boundary
	// (finalizeMutatingCancellation, agent_node_executor_cancellation.go)
	// CASes the Attempt via the already-tx-scoped
	// ports.RuntimeRepository.TransitionExecutionAttempt directly — this
	// legacy fake spy (the OLD, non-atomic worker.InterruptionRecoveryStore/
	// WorkspaceReconciler path) is no longer called at all, by either
	// branch, so it must stay empty regardless of outcome.
	if len(interruptions.terminations) != 0 {
		t.Fatalf("interruptions.terminations = %+v, want none — the legacy path is no longer called", interruptions.terminations)
	}
	if len(reconciler.quarantined) != 0 {
		t.Fatalf("reconciler.quarantined = %+v, want none — the legacy path is no longer called", reconciler.quarantined)
	}

	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(ctx, req.AttemptID)
		if err != nil {
			return err
		}
		if attempt.State != domainruntime.ExecutionAttemptIndeterminate || attempt.TerminationReason != domainruntime.TerminationReasonOwnershipLostMutating {
			t.Fatalf("attempt = %+v, want INDETERMINATE/OWNERSHIP_LOST_MUTATING", attempt)
		}
		nodeRun, err := tx.Runtime().GetNodeRun(ctx, req.NodeRunID)
		if err != nil {
			return err
		}
		if nodeRun.State != domainruntime.NodeRunCancelled {
			t.Fatalf("nodeRun.State = %s, want CANCELLED", nodeRun.State)
		}
		run, err := tx.Runtime().GetWorkflowRun(ctx, req.RunID)
		if err != nil {
			return err
		}
		workspaceSet, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, string(run.FamilyID))
		if err != nil {
			return err
		}
		repoWorkspace, err := tx.Work().GetRepositoryWorkspace(ctx, string(workspaceSet.ID), "repo-1", 1)
		if err != nil {
			return err
		}
		if repoWorkspace.State != workspace.RepositoryWorkspaceReady {
			t.Fatalf("repositoryWorkspace.State = %s, want still READY (revision was unchanged, never quarantined)", repoWorkspace.State)
		}
		return nil
	}); err != nil {
		t.Fatalf("load attempt/node run/repository workspace: %v", err)
	}
}

// TestAgentNodeExecutor_MutatingCancellation_MutatedRevision_TerminatesIndeterminateAndQuarantines
// is the other half: the workspace's own live current revision no longer
// matches what this attempt was pinned to — a real, observed mutation the
// cancelled process could never confirm was complete or consistent. The
// Attempt still becomes INDETERMINATE, and the repository workspace it held
// a WriteLease against is quarantined.
func TestAgentNodeExecutor_MutatingCancellation_MutatedRevision_TerminatesIndeterminateAndQuarantines(t *testing.T) {
	executor, req, uow, ids, interruptions, reconciler, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:            defaultInScopeDiff(),
		captureRevision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
		agentResult:     ports.AgentExecutionResult{Status: ports.AgentExecutionCancelled, TreeQuiesced: true},
	})
	ctx := context.Background()

	if _, err := runtime.CancelRun(ctx, uow, ids, runtime.CancelRunRequest{
		RunID: req.RunID, Actor: "actor-1", Reason: "test cancellation",
	}); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	_, err := executor.Execute(ctx, req)
	if !errors.Is(err, runtime.ErrAttemptAlreadyTerminated) {
		t.Fatalf("Execute error = %v, want ErrAttemptAlreadyTerminated", err)
	}
	// V5-15D: see the sibling CleanRevision test's own identical comment —
	// the legacy fake spy is never called by either branch anymore.
	if len(interruptions.terminations) != 0 {
		t.Fatalf("interruptions.terminations = %+v, want none — the legacy path is no longer called", interruptions.terminations)
	}
	if len(reconciler.quarantined) != 0 {
		t.Fatalf("reconciler.quarantined = %+v, want none — the legacy path is no longer called", reconciler.quarantined)
	}

	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(ctx, req.AttemptID)
		if err != nil {
			return err
		}
		if attempt.State != domainruntime.ExecutionAttemptIndeterminate || attempt.TerminationReason != domainruntime.TerminationReasonOwnershipLostMutating {
			t.Fatalf("attempt = %+v, want INDETERMINATE/OWNERSHIP_LOST_MUTATING", attempt)
		}
		nodeRun, err := tx.Runtime().GetNodeRun(ctx, req.NodeRunID)
		if err != nil {
			return err
		}
		if nodeRun.State != domainruntime.NodeRunCancelled {
			t.Fatalf("nodeRun.State = %s, want CANCELLED", nodeRun.State)
		}
		run, err := tx.Runtime().GetWorkflowRun(ctx, req.RunID)
		if err != nil {
			return err
		}
		workspaceSet, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, string(run.FamilyID))
		if err != nil {
			return err
		}
		repoWorkspace, err := tx.Work().GetRepositoryWorkspace(ctx, string(workspaceSet.ID), "repo-1", 1)
		if err != nil {
			return err
		}
		if repoWorkspace.State != workspace.RepositoryWorkspaceQuarantined {
			t.Fatalf("repositoryWorkspace.State = %s, want QUARANTINED (revision was observed to change)", repoWorkspace.State)
		}
		return nil
	}); err != nil {
		t.Fatalf("load attempt/node run/repository workspace: %v", err)
	}
}
