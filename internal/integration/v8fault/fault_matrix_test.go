// TestV8Fault_Boundary1..6 close V8-02's own primary deliverable (GC-ACC-16,
// docs/spikes/01-go-core-spike-plan.md §9): the same six standard crash
// transaction boundaries internal/spikeacceptance's own SPK-04 scenario
// already proves — but recovered here by a REAL `aw worker` process's own
// continuously-polling reaper loop, not a manual RecoverExpiredJobs/ClaimJob
// call. SPK-04 already established that the underlying SQLite transactions
// are individually safe to replay/reclaim; what these tests add is proof
// that the PRODUCTION worker binary's own real scheduling loop actually
// discovers and acts on that recoverable state end to end.
//
// Every arrange/seed step below is copied structurally from spk04_scenario.go
// (same fixture IDs, same seed calls) since that sequence is already proven
// correct — only the recovery half differs.
package v8fault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

const faultCrashedWorkerTTL = 900 * time.Millisecond

// faultFixture is one boundary's arranged installation: a fresh temp
// directory with its own SQLite database, artifact root and workspace root
// (the latter two exist only so a real `aw worker` has valid --artifact-root/
// --workspace-root paths — no boundary here ever touches an artifact or a
// real Git workspace) plus the compiled+published crash-resume workflow
// version and started run every boundary needs.
type faultFixture struct {
	Root          string
	DatabasePath  string
	ArtifactRoot  string
	WorkspaceRoot string
	Store         *sqlite.Store
	Version       workflow.WorkflowVersion
	Run           domainruntime.WorkflowRun
}

func arrangeFault(t *testing.T, tempDirPrefix string, runID domainruntime.WorkflowRunID, versionID workflow.WorkflowVersionID, moveToRunning bool) *faultFixture {
	t.Helper()
	root := t.TempDir()
	databasePath := filepath.Join(root, "agentkit.db")
	artifactRoot := filepath.Join(root, "artifacts")
	workspaceRoot := filepath.Join(root, "workspaces")
	for _, dir := range []string{artifactRoot, workspaceRoot} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("%s: mkdir %s: %v", tempDirPrefix, dir, err)
		}
	}
	ctx := context.Background()
	store, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("%s: open sqlite: %v", tempDirPrefix, err)
	}
	if err := sqlite.SeedCrashResumeOwners(ctx, store); err != nil {
		t.Fatalf("%s: seed owners: %v", tempDirPrefix, err)
	}
	definition := sqlite.CrashResumeWorkflowDefinition()
	version, err := sqlite.CompileCrashResumeWorkflowVersion(definition, versionID, 1, sqlite.CrashResumeWorkflowDocumentV1(), "skill-v1")
	if err != nil {
		t.Fatalf("%s: compile workflow version: %v", tempDirPrefix, err)
	}
	if _, err := store.PublishWorkflowVersion(ctx, definition, version); err != nil {
		t.Fatalf("%s: publish workflow version: %v", tempDirPrefix, err)
	}
	run, err := domainruntime.NewWorkflowRun(runID, "project-crash", "work-item-crash", version, "family-crash", 1, json.RawMessage(`{"checkpoint":"created"}`))
	if err != nil {
		t.Fatalf("%s: build workflow run: %v", tempDirPrefix, err)
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		t.Fatalf("%s: start workflow run: %v", tempDirPrefix, err)
	}
	if moveToRunning {
		if _, err := store.CompareAndSwapWorkflowRun(ctx, ports.WorkflowRunTransition{
			RunID: run.ID, ExpectedState: domainruntime.WorkflowRunCreated, ExpectedVersion: 1,
			NextState: domainruntime.WorkflowRunRunning, SharedState: json.RawMessage(`{"checkpoint":"worker-dispatched"}`),
			OccurredAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("%s: move run to RUNNING: %v", tempDirPrefix, err)
		}
	}
	return &faultFixture{
		Root: root, DatabasePath: databasePath, ArtifactRoot: artifactRoot, WorkspaceRoot: workspaceRoot,
		Store: store, Version: version, Run: run,
	}
}

// reopen closes f's own arrange-time Store handle (so the real `aw worker`
// this test starts next owns the only live connection, mirroring an
// operator's own real restart) and returns a fresh, test-owned read handle
// for verification after the worker has run.
func (f *faultFixture) reopen(t *testing.T) *sqlite.Store {
	t.Helper()
	if err := f.Store.Close(); err != nil {
		t.Fatalf("close arrange store: %v", err)
	}
	store, err := sqlite.Open(context.Background(), f.DatabasePath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// waitForRealWorkerReclaim polls jobID's own durable_jobs row (via the
// debug-only DebugListJobsByKind projection, the same "small, direct,
// test-support query" this package's own harness otherwise has no way to
// read a job's claim_count from) until a real `aw worker`'s own reaper loop
// has genuinely reclaimed it — ClaimCount strictly greater than
// crashedClaimCount, proving the reclaim was performed by the WORKER's own
// polling cycle, not by this test.
func waitForRealWorkerReclaim(t *testing.T, store *sqlite.Store, jobID string, crashedClaimCount int) {
	t.Helper()
	ctx := context.Background()
	waitFor(t, fmt.Sprintf("real aw worker to reclaim job %s (claim count > %d)", jobID, crashedClaimCount), 15*time.Second, 100*time.Millisecond, func() bool {
		rows, err := store.DebugListJobsByKind(ctx, "EXECUTE_NODE")
		if err != nil {
			t.Fatalf("DebugListJobsByKind: %v", err)
		}
		for _, row := range rows {
			if row.ID == jobID {
				return row.ClaimCount > crashedClaimCount
			}
		}
		return false
	})
}

// TestV8Fault_Boundary1_BeforeIntentJobCommit closes fault point 1/6: killed
// before the worker ever attempts DispatchNodeIntent. Nothing durable may
// exist afterward beyond the CREATED@1 workflow run, and a real `aw worker`
// started against this database has nothing to reclaim and must not error
// out or fabricate state.
func TestV8Fault_Boundary1_BeforeIntentJobCommit(t *testing.T) {
	requireAcceptance(t)
	bin := builtBinaries(t)
	fixture := arrangeFault(t, "v8fault-b1", sqlite.CrashIntentRunID, "workflow-version-v8fault-b1-v1", false)

	if _, err := spawnAndHardKillSpikeWorker(bin.spikeWorker, fixture.DatabasePath, faultCrashedWorkerTTL, sqlite.CrashModeBeforeIntentCommit); err != nil {
		t.Fatalf("spawn/kill crash worker: %v", err)
	}

	store := fixture.reopen(t)
	ctx := context.Background()

	// Give a real worker a real chance to run before asserting "nothing
	// happened" — the absence of a job/event is the actual claim under test,
	// not merely that no time passed.
	startRealWorker(t, bin.aw, fixture.DatabasePath, fixture.ArtifactRoot, fixture.WorkspaceRoot, "worker-after-crash-b1")
	time.Sleep(1 * time.Second)

	run, err := store.LoadWorkflowRun(ctx, sqlite.CrashIntentRunID)
	if err != nil {
		t.Fatalf("load workflow run: %v", err)
	}
	if run.State != domainruntime.WorkflowRunCreated || run.Version != 1 {
		t.Errorf("workflow run = state=%s version=%d, want CREATED@1 (untouched)", run.State, run.Version)
	}

	if _, _, err := store.LoadNodeRunState(ctx, sqlite.CrashIntentNodeRunID); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Errorf("load node run state = %v, want ErrPersistenceNotFound (no node run was ever committed)", err)
	}

	jobCount, err := store.CountDurableJobsByIdempotencyKey(ctx, sqlite.CrashIntentIdempotencyKey)
	if err != nil {
		t.Fatalf("count durable jobs: %v", err)
	}
	if jobCount != 0 {
		t.Errorf("durable job count = %d, want 0 (no job was ever committed for the real worker to fabricate)", jobCount)
	}
}

// TestV8Fault_Boundary2_AfterJobCommitBeforeClaim closes fault point 2/6: the
// crashed worker committed a real DispatchNodeIntent transaction, then was
// hard-killed before anyone claimed the job it created. A real `aw worker`
// started fresh must discover and claim that already-committed job on its
// own — it was never claimed in the first place, so no lease-expiry wait is
// needed.
func TestV8Fault_Boundary2_AfterJobCommitBeforeClaim(t *testing.T) {
	requireAcceptance(t)
	bin := builtBinaries(t)
	fixture := arrangeFault(t, "v8fault-b2", sqlite.CrashIntentRunID, "workflow-version-v8fault-b2-v1", false)

	ready, err := spawnAndHardKillSpikeWorker(bin.spikeWorker, fixture.DatabasePath, faultCrashedWorkerTTL, sqlite.CrashModeAfterIntentCommit)
	if err != nil {
		t.Fatalf("spawn/kill crash worker: %v", err)
	}
	if string(ready.JobID) != sqlite.CrashIntentJobID {
		t.Fatalf("crashed worker dispatched job %q, want %q", ready.JobID, sqlite.CrashIntentJobID)
	}

	store := fixture.reopen(t)
	ctx := context.Background()
	startRealWorker(t, bin.aw, fixture.DatabasePath, fixture.ArtifactRoot, fixture.WorkspaceRoot, "worker-after-crash-b2")
	// Never claimed yet (claim_count 0 at commit time), so a real worker's
	// very first successful claim (count 1) already proves the job was not
	// lost.
	waitForRealWorkerReclaim(t, store, sqlite.CrashIntentJobID, 0)

	run, err := store.LoadWorkflowRun(ctx, sqlite.CrashIntentRunID)
	if err != nil {
		t.Fatalf("load workflow run: %v", err)
	}
	if run.State != domainruntime.WorkflowRunRunning {
		t.Errorf("workflow run state = %s, want RUNNING", run.State)
	}
	eventCount, err := store.CountDomainEvents(ctx, "NodeRun", sqlite.CrashIntentNodeRunID)
	if err != nil {
		t.Fatalf("count domain events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("node-dispatch domain event count = %d, want exactly 1 (the real worker's claim must never re-dispatch it)", eventCount)
	}
}

// TestV8Fault_Boundary3_AfterJobClaimBeforeProcessSpawn closes fault point
// 3/6: killed right after the worker claims its job, before it spawns any
// process. A real `aw worker` must reclaim the SAME job once the crashed
// lease expires, with a strictly greater claim count.
func TestV8Fault_Boundary3_AfterJobClaimBeforeProcessSpawn(t *testing.T) {
	requireAcceptance(t)
	bin := builtBinaries(t)
	fixture := arrangeFault(t, "v8fault-b3", sqlite.CrashClaimRunID, "workflow-version-v8fault-b3-v1", true)
	ctx := context.Background()
	if _, err := fixture.Store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: sqlite.CrashClaimJobID, ProjectID: "project-crash", Kind: "EXECUTE_NODE", AggregateType: "WorkflowRun",
		AggregateID: string(fixture.Run.ID), Payload: json.RawMessage(`{"runId":"` + sqlite.CrashClaimRunID + `"}`),
		MaxClaims: 3, IdempotencyKey: "v8fault-b3-execute",
	}); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}

	ready, err := spawnAndHardKillSpikeWorker(bin.spikeWorker, fixture.DatabasePath, faultCrashedWorkerTTL, sqlite.CrashModeClaimAndHang)
	if err != nil {
		t.Fatalf("spawn/kill crash worker: %v", err)
	}
	if string(ready.JobID) != sqlite.CrashClaimJobID {
		t.Fatalf("crashed worker claimed job %q, want %q", ready.JobID, sqlite.CrashClaimJobID)
	}

	store := fixture.reopen(t)
	startRealWorker(t, bin.aw, fixture.DatabasePath, fixture.ArtifactRoot, fixture.WorkspaceRoot, "worker-after-crash-b3")
	waitForRealWorkerReclaim(t, store, sqlite.CrashClaimJobID, 1)
}

// TestV8Fault_Boundary4_AfterCheckpointBeforeProcessExit closes fault point
// 4/6: killed after a real fake-provider grandchild signalled a
// checkpoint-worthy event and the crashed worker persisted a real
// Checkpoint+ContextSnapshot — but before that provider process would
// itself have exited. The checkpoint must survive the hard kill untouched,
// and a real `aw worker` must still reclaim the stale job afterward.
func TestV8Fault_Boundary4_AfterCheckpointBeforeProcessExit(t *testing.T) {
	requireAcceptance(t)
	bin := builtBinaries(t)
	fixture := arrangeFault(t, "v8fault-b4", sqlite.CrashCheckpointRunID, "workflow-version-v8fault-b4-v1", true)
	ctx := context.Background()
	const jobID = "v8fault-b4-job"
	if _, err := fixture.Store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: jobID, ProjectID: "project-crash", Kind: "EXECUTE_NODE", AggregateType: "WorkflowRun",
		AggregateID: string(fixture.Run.ID), Payload: json.RawMessage(`{"runId":"` + sqlite.CrashCheckpointRunID + `"}`),
		MaxClaims: 3, IdempotencyKey: "v8fault-b4-execute",
	}); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	if err := sqlite.SeedCrashCheckpointNodeRunAndAttempt(ctx, fixture.Store, fixture.Run.ID); err != nil {
		t.Fatalf("seed node run/attempt: %v", err)
	}

	ready, err := spawnAndHardKillSpikeWorker(bin.spikeWorker, fixture.DatabasePath, faultCrashedWorkerTTL, sqlite.CrashModeCheckpointThenHang)
	if err != nil {
		t.Fatalf("spawn/kill crash worker: %v", err)
	}
	if string(ready.JobID) != jobID {
		t.Fatalf("crashed worker claimed job %q, want %q", ready.JobID, jobID)
	}

	store := fixture.reopen(t)
	checkpoint, err := store.LoadLatestCheckpoint(ctx, sqlite.CrashCheckpointInterruptedAttempt)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if checkpoint.ID != sqlite.CrashCheckpointID || checkpoint.Sequence != 1 || checkpoint.ContextSnapshotID != sqlite.CrashCheckpointContextSnapshotID {
		t.Errorf("checkpoint = %+v, did not survive the hard kill intact", checkpoint)
	}
	if _, err := store.LoadContextSnapshot(ctx, checkpoint.ContextSnapshotID); err != nil {
		t.Errorf("load context snapshot: %v (must survive the hard kill)", err)
	}

	startRealWorker(t, bin.aw, fixture.DatabasePath, fixture.ArtifactRoot, fixture.WorkspaceRoot, "worker-after-crash-b4")
	waitForRealWorkerReclaim(t, store, jobID, 1)
}

// TestV8Fault_Boundary5_AfterProcessExitBeforeOutcomeCommit_ReadOnly and its
// _Mutating twin close fault point 5/6: killed after a real external
// process already exited cleanly, before any outcome is durably committed.
// A real `aw worker` must reclaim the job; this test itself still performs
// the classify/reconcile step (worker.ClassifyInterruptedAttempt), matching
// SPK-04's own proof that recovery is driven from durable WriteLease
// evidence, never the crashed worker's own observed exit code.
func testBoundary5(t *testing.T, mutating bool) {
	t.Helper()
	requireAcceptance(t)
	bin := builtBinaries(t)

	label := "readonly"
	mode := sqlite.CrashModeProcessExitReadonly
	jobID := "v8fault-b5-job-readonly"
	versionID := workflow.WorkflowVersionID("workflow-version-v8fault-b5-readonly-v1")
	if mutating {
		label = "mutating"
		mode = sqlite.CrashModeProcessExitMutating
		jobID = "v8fault-b5-job-mutating"
		versionID = "workflow-version-v8fault-b5-mutating-v1"
	}

	fixture := arrangeFault(t, "v8fault-b5-"+label, sqlite.CrashAttemptTermRunID, versionID, true)
	ctx := context.Background()
	if _, err := fixture.Store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(jobID), ProjectID: "project-crash", Kind: "EXECUTE_NODE", AggregateType: "WorkflowRun",
		AggregateID: string(fixture.Run.ID), Payload: json.RawMessage(`{"runId":"` + sqlite.CrashAttemptTermRunID + `"}`),
		MaxClaims: 3, IdempotencyKey: "v8fault-b5-execute-" + label,
	}); err != nil {
		t.Fatalf("%s: enqueue job: %v", label, err)
	}
	if err := sqlite.SeedCrashAttemptTermNodeRunAndAttempt(ctx, fixture.Store, fixture.Run.ID); err != nil {
		t.Fatalf("%s: seed node run/attempt: %v", label, err)
	}
	if mutating {
		if err := sqlite.SeedCrashAttemptTermWorkspace(ctx, fixture.Store); err != nil {
			t.Fatalf("%s: seed workspace: %v", label, err)
		}
	}

	ready, err := spawnAndHardKillSpikeWorker(bin.spikeWorker, fixture.DatabasePath, faultCrashedWorkerTTL, mode)
	if err != nil {
		t.Fatalf("%s: spawn/kill crash worker: %v", label, err)
	}
	if string(ready.JobID) != jobID {
		t.Fatalf("%s: crashed worker claimed job %q, want %q", label, ready.JobID, jobID)
	}

	store := fixture.reopen(t)
	startRealWorker(t, bin.aw, fixture.DatabasePath, fixture.ArtifactRoot, fixture.WorkspaceRoot, "worker-after-crash-b5-"+label)
	waitForRealWorkerReclaim(t, store, jobID, 1)

	// classification must come from durable WriteLease evidence, never the
	// crashed worker's own clean exit code (SPK-04's own established proof,
	// reused verbatim here).
	nextState, reason, err := worker.ClassifyInterruptedAttempt(ctx, store, sqlite.CrashAttemptTermAttemptID)
	if err != nil {
		t.Fatalf("%s: classify interrupted attempt: %v", label, err)
	}
	wantState := domainruntime.ExecutionAttemptLost
	wantReason := domainruntime.TerminationReasonLeaseLost
	if mutating {
		wantState = domainruntime.ExecutionAttemptIndeterminate
		wantReason = domainruntime.TerminationReasonOwnershipLostMutating
	}
	if reason != wantReason || nextState != wantState {
		t.Errorf("%s: classification = state=%s reason=%s, want state=%s reason=%s", label, nextState, reason, wantState, wantReason)
	}
}

func TestV8Fault_Boundary5_AfterProcessExitBeforeOutcomeCommit_ReadOnly(t *testing.T) {
	testBoundary5(t, false)
}

func TestV8Fault_Boundary5_AfterProcessExitBeforeOutcomeCommit_Mutating(t *testing.T) {
	testBoundary5(t, true)
}

// TestV8Fault_Boundary6_AfterOutcomeCommitBeforeNextDispatch closes fault
// point 6/6: node completion, job acknowledgement and downstream-job
// dispatch already committed in one transaction before the crash. A real
// `aw worker` reclaiming the ALREADY-SUCCEEDED current job must never
// duplicate the downstream dispatch — the real proof here is that the
// downstream job it already created stays exactly one, genuinely claimable.
func TestV8Fault_Boundary6_AfterOutcomeCommitBeforeNextDispatch(t *testing.T) {
	requireAcceptance(t)
	bin := builtBinaries(t)
	fixture := arrangeFault(t, "v8fault-b6", sqlite.CrashNodeDispatchRunID, "workflow-version-v8fault-b6-v1", true)
	ctx := context.Background()
	if _, err := fixture.Store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: sqlite.CrashNodeDispatchJobID, ProjectID: "project-crash", Kind: "EXECUTE_NODE", AggregateType: "WorkflowRun",
		AggregateID: string(fixture.Run.ID), Payload: json.RawMessage(`{"runId":"` + sqlite.CrashNodeDispatchRunID + `"}`),
		MaxClaims: 3, IdempotencyKey: "execute-" + sqlite.CrashNodeDispatchJobID,
	}); err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	if err := sqlite.SeedCrashNodeDispatchNodeRun(ctx, fixture.Store, fixture.Run.ID); err != nil {
		t.Fatalf("seed node run: %v", err)
	}

	ready, err := spawnAndHardKillSpikeWorker(bin.spikeWorker, fixture.DatabasePath, faultCrashedWorkerTTL, sqlite.CrashModeNodeDispatch)
	if err != nil {
		t.Fatalf("spawn/kill crash worker: %v", err)
	}
	if string(ready.JobID) != sqlite.CrashNodeDispatchJobID {
		t.Fatalf("crashed worker claimed job %q, want %q", ready.JobID, sqlite.CrashNodeDispatchJobID)
	}

	store := fixture.reopen(t)
	nodeState, nodeVersion, err := store.LoadNodeRunState(ctx, sqlite.CrashNodeDispatchNodeRunID)
	if err != nil {
		t.Fatalf("load node run: %v", err)
	}
	if nodeState != domainruntime.NodeRunSucceeded || nodeVersion != 2 {
		t.Errorf("node run = state=%s version=%d, want SUCCEEDED@2 (already committed before the crash)", nodeState, nodeVersion)
	}
	currentJobState, err := store.LoadDurableJobState(ctx, ports.JobID(sqlite.CrashNodeDispatchJobID))
	if err != nil {
		t.Fatalf("load current job state: %v", err)
	}
	if currentJobState != ports.JobSucceeded {
		t.Errorf("current job state = %s, want SUCCEEDED (already committed before the crash)", currentJobState)
	}

	// The downstream job was already dispatched in the SAME transaction —
	// a real worker reclaiming/retrying the (already-terminal) current job
	// must never re-run CompleteNodeAndDispatchNext and duplicate it.
	startRealWorker(t, bin.aw, fixture.DatabasePath, fixture.ArtifactRoot, fixture.WorkspaceRoot, "worker-after-crash-b6")
	time.Sleep(2 * time.Second)

	eventCount, err := store.CountDomainEvents(ctx, "NodeRun", sqlite.CrashNodeDispatchNodeRunID)
	if err != nil {
		t.Fatalf("count domain events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("node-completion domain event count = %d, want exactly 1 (the real worker must never duplicate an already-committed transition)", eventCount)
	}
	// Read the downstream job's own row rather than claiming it: the real
	// worker started above is itself polling and would genuinely race this
	// test for that exact claim (it is a real, available EXECUTE_NODE job)
	// — a manual claim here would either lose that race with a confusing
	// "no job available" or steal the job out from under the worker this
	// test is trying to observe. Presence of exactly one row under the
	// expected ID is the real, non-racy proof that the downstream dispatch
	// was never duplicated.
	rows, err := store.DebugListJobsByKind(ctx, "EXECUTE_NODE")
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	var downstreamCount int
	for _, row := range rows {
		if row.ID == sqlite.CrashNodeDispatchNextJobID {
			downstreamCount++
		}
	}
	if downstreamCount != 1 {
		t.Errorf("downstream job %s appears %d time(s) among EXECUTE_NODE jobs, want exactly 1 (never duplicated)", sqlite.CrashNodeDispatchNextJobID, downstreamCount)
	}
}
