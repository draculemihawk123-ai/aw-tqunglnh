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
}

func (f *bridgeFakeWorkspaceProvider) Provision(context.Context, ports.ProvisionSpec) (ports.WorkspaceHandle, error) {
	return ports.NewWorkspaceHandle("bridge-fixture-handle")
}
func (f *bridgeFakeWorkspaceProvider) Inspect(context.Context, ports.WorkspaceHandle) (ports.WorkspaceInspection, error) {
	return ports.WorkspaceInspection{}, nil
}
func (f *bridgeFakeWorkspaceProvider) CaptureRevision(context.Context, ports.WorkspaceHandle) (workspace.Revision, error) {
	return workspace.Revision{}, nil
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
type bridgeFakeWriteLeaseManager struct{}

func (bridgeFakeWriteLeaseManager) AcquireWriteLeases(_ context.Context, req ports.AcquireWriteLeasesRequest) ([]ports.WriteLeaseGrant, error) {
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
func (bridgeFakeWriteLeaseManager) HeartbeatWriteLeases(context.Context, []ports.WriteLeaseGrant, time.Duration) ([]ports.WriteLeaseGrant, error) {
	return nil, errors.New("bridgeFakeWriteLeaseManager: HeartbeatWriteLeases must not be called")
}
func (bridgeFakeWriteLeaseManager) ValidateWriteLease(context.Context, ports.WriteLeaseGrant) error {
	return errors.New("bridgeFakeWriteLeaseManager: ValidateWriteLease must not be called")
}
func (bridgeFakeWriteLeaseManager) ReleaseWriteLeases(context.Context, []ports.WriteLeaseGrant) error {
	return errors.New("bridgeFakeWriteLeaseManager: ReleaseWriteLeases must not be called")
}

var _ ports.WriteLeaseManager = bridgeFakeWriteLeaseManager{}

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

// bridgeFixtureOptions lets each test override just the pieces it cares
// about; bridgeFixture below fills in golden-path defaults for the rest.
type bridgeFixtureOptions struct {
	diff        ports.WorkspaceDiff
	agentResult ports.AgentExecutionResult
	agentErr    error
	agentEvents []ports.AgentEventKind
}

// bridgeFixture builds one fully-admitted, RUNNING ExecutionAttempt (reusing
// assembleRequestFixture's own real message/skill/AdapterBuild chain) plus
// the claimed EXECUTE_NODE JobLease a real ExecuteNodeHandler would have
// passed into Execute, and returns a ready-to-drive AgentNodeExecutor.
func bridgeFixture(t *testing.T, opts bridgeFixtureOptions) (executor *runtime.AgentNodeExecutor, req ports.NodeExecutionRequest, uow *fake.UnitOfWork, ids idsource.Source) {
	t.Helper()
	ctx := context.Background()
	u, ids, store, runID, nodeRunID, attemptID := assembleRequestFixture(t)
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

	executor = runtime.NewAgentNodeExecutor(
		u, ids, store, &bridgeFakeWorkspaceProvider{diff: opts.diff}, bridgeFakeWriteLeaseManager{},
		agents, registry, redact.NewMatcher(), bridgeFakeCheckpointStore{}, clock.System{},
	)
	req = ports.NodeExecutionRequest{AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: lease}
	return executor, req, u, ids
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
	executor, req, uow, ids := bridgeFixture(t, bridgeFixtureOptions{
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
	finalizeResult, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, runtime.FinalizeExecutionAttemptRequest{
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
	executor, req, uow, _ := bridgeFixture(t, bridgeFixtureOptions{
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
	executor, req, _, _ := bridgeFixture(t, bridgeFixtureOptions{
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
	executor, req, _, _ := bridgeFixture(t, bridgeFixtureOptions{
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
