package runtime_test

import (
	"context"
	"crypto/sha1" //nolint:gosec // content addressing for an in-memory test double, not a security use
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// treeFakeWorkspaces is a ports.WorkspaceProvider that also implements
// ports.WorkspaceTreeSnapshotter (V9-01, ADR-030) over an in-memory "working
// tree" (path -> content): SnapshotTree content-addresses the current files
// into a 40-hex tree ID and remembers the snapshot, DiffTrees compares two
// remembered snapshots (ports.ErrTreeNotFound for an unknown ID, as the git
// adapter reports a pruned object). Diff is the embedded scripted double — a
// test uses it to say "the diff against the pinned commit is non-empty", i.e.
// "an earlier maker left uncommitted changes".
type treeFakeWorkspaces struct {
	*bridgeFakeWorkspaceProvider

	mu    sync.Mutex
	files map[string]string
	trees map[string]map[string]string
	// snapshots counts SnapshotTree calls.
	snapshots int
	// onFirstSnapshot, if non-nil, runs once at the start of the first
	// SnapshotTree call, before it reads the files.
	onFirstSnapshot func()
	snapshotError   error
}

func newTreeFakeWorkspaces(diff ports.WorkspaceDiff, files map[string]string) *treeFakeWorkspaces {
	w := &treeFakeWorkspaces{
		bridgeFakeWorkspaceProvider: &bridgeFakeWorkspaceProvider{diff: diff},
		files:                       map[string]string{},
		trees:                       map[string]map[string]string{},
	}
	for path, content := range files {
		w.files[path] = content
	}
	return w
}

var _ ports.WorkspaceProvider = (*treeFakeWorkspaces)(nil)
var _ ports.WorkspaceTreeSnapshotter = (*treeFakeWorkspaces)(nil)

func (w *treeFakeWorkspaces) setFile(path, content string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.files[path] = content
}

func (w *treeFakeWorkspaces) removeFile(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.files, path)
}

func (w *treeFakeWorkspaces) forgetTree(treeID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.trees, treeID)
}

func (w *treeFakeWorkspaces) snapshotCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.snapshots
}

func (w *treeFakeWorkspaces) SnapshotTree(_ context.Context, _ ports.WorkspaceHandle) (string, error) {
	w.mu.Lock()
	hook := w.onFirstSnapshot
	if w.snapshots == 0 {
		w.onFirstSnapshot = nil
	} else {
		hook = nil
	}
	w.mu.Unlock()
	if hook != nil {
		hook()
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.snapshots++
	if w.snapshotError != nil {
		return "", w.snapshotError
	}
	return w.snapshotLocked(), nil
}

// snapshotLocked content-addresses the current files and remembers them.
func (w *treeFakeWorkspaces) snapshotLocked() string {
	paths := make([]string, 0, len(w.files))
	for path := range w.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha1.New() //nolint:gosec // see import
	snapshot := make(map[string]string, len(paths))
	for _, path := range paths {
		fmt.Fprintf(hash, "%d:%s%d:%s", len(path), path, len(w.files[path]), w.files[path])
		snapshot[path] = w.files[path]
	}
	treeID := hex.EncodeToString(hash.Sum(nil))
	w.trees[treeID] = snapshot
	return treeID
}

func (w *treeFakeWorkspaces) DiffTrees(_ context.Context, _ ports.WorkspaceHandle, a, b string) ([]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	left, ok := w.trees[a]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrTreeNotFound, a)
	}
	right, ok := w.trees[b]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrTreeNotFound, b)
	}
	changed := map[string]bool{}
	for path, content := range left {
		if other, present := right[path]; !present || other != content {
			changed[path] = true
		}
	}
	for path := range right {
		if _, present := left[path]; !present {
			changed[path] = true
		}
	}
	paths := make([]string, 0, len(changed))
	for path := range changed {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// checkerFixture builds a CHECKER-role bridge fixture over a tree-aware
// workspace double whose Diff against the pinned commit is non-empty (a maker
// left uncommitted changes) and whose working tree holds makerFiles.
type checkerFixture struct {
	executor   *runtime.AgentNodeExecutor
	req        ports.NodeExecutionRequest
	uow        *fake.UnitOfWork
	workspaces *treeFakeWorkspaces
	starts     *int
}

func newCheckerFixture(t *testing.T, role workflow.AgentRole, onAgentStart func(w *treeFakeWorkspaces)) checkerFixture {
	t.Helper()
	workspaces := newTreeFakeWorkspaces(defaultInScopeDiff(), map[string]string{
		"src/main.go": "package main // written by an earlier maker, still uncommitted",
		"README.md":   "docs",
	})
	starts := new(int)
	executor, req, uow, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		role:        role,
		workspaces:  workspaces,
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
		agentStarts: starts,
		onAgentStart: func() {
			if onAgentStart != nil {
				onAgentStart(workspaces)
			}
		},
	})
	return checkerFixture{executor: executor, req: req, uow: uow, workspaces: workspaces, starts: starts}
}

func (c checkerFixture) execute(t *testing.T) ports.NodeExecutionResult {
	t.Helper()
	result, err := c.executor.Execute(context.Background(), c.req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return result
}

func (c checkerFixture) attempt(t *testing.T) domainruntime.ExecutionAttempt {
	t.Helper()
	var attempt domainruntime.ExecutionAttempt
	if err := c.uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		attempt, err = tx.Runtime().GetExecutionAttempt(context.Background(), c.req.AttemptID)
		return err
	}); err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	return attempt
}

func requireScopeViolation(t *testing.T, result ports.NodeExecutionResult, why string) {
	t.Helper()
	if result.State != domainruntime.ExecutionAttemptFailed || result.TerminationReason != domainruntime.TerminationReasonScopeViolation ||
		result.ErrorCode != errorcode.CodeScopeViolation {
		t.Fatalf("result = %+v, want FAILED/SCOPE_VIOLATION/SCOPE_VIOLATION (%s)", result, why)
	}
}

const checkerRepository = project.RepositoryID("repo-1")

// A CHECKER after a MAKER that left uncommitted changes: the diff against the
// pinned commit is non-empty, but the checker itself changed nothing, so it
// passes (ADR-030). This is the unit-level shape of G1; on a build that
// measures against the pinned commit it is FAILED/SCOPE_VIOLATION.
func TestAgentNodeExecutor_CheckerAfterMaker_UncommittedMakerChangeIsNotAViolation(t *testing.T) {
	c := newCheckerFixture(t, workflow.AgentRoleChecker, nil)

	result := c.execute(t)
	if result.State != domainruntime.ExecutionAttemptSucceeded {
		t.Fatalf("result = %+v, want SUCCEEDED (the maker's uncommitted change is part of the checker's INPUT, not something it did)", result)
	}
	attempt := c.attempt(t)
	if attempt.InputTrees[checkerRepository] == "" {
		t.Fatalf("attempt.InputTrees = %v, want a tree recorded for %s before the checker ran", attempt.InputTrees, checkerRepository)
	}
	if got := c.workspaces.snapshotCount(); got != 2 {
		t.Fatalf("SnapshotTree calls = %d, want 2 (input before spawn, output after quiescence)", got)
	}

	// V9-01: the AGENT attempt's evidence now carries one RECORDED row whose
	// references are its diff manifests, so a later CHECKER can find them.
	entries := result.Evidence.EvidenceEntries
	if len(entries) != 1 || entries[0].Kind != domainruntime.EvidenceKindAgentExecution || entries[0].Verdict != domainruntime.EvidenceVerdictRecorded {
		t.Fatalf("evidence entries = %+v, want exactly one %s/%s entry", entries, domainruntime.EvidenceKindAgentExecution, domainruntime.EvidenceVerdictRecorded)
	}
	var manifestIDs []string
	for _, ref := range result.Evidence.DiffManifestArtifacts {
		manifestIDs = append(manifestIDs, ref.ArtifactID)
	}
	if strings.Join(entries[0].ArtifactReferences, ",") != strings.Join(manifestIDs, ",") || len(manifestIDs) == 0 {
		t.Fatalf("evidence artifact references = %v, want the diff manifests %v", entries[0].ArtifactReferences, manifestIDs)
	}
}

// What the checker itself changes is still a violation, whatever the kind of
// change: edit a tracked file, create an untracked one, delete one.
func TestAgentNodeExecutor_CheckerRole_OwnChangeIsScopeViolation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(w *treeFakeWorkspaces)
	}{
		{"modifies a tracked file", func(w *treeFakeWorkspaces) { w.setFile("src/main.go", "tampered by the checker") }},
		{"creates an untracked file", func(w *treeFakeWorkspaces) { w.setFile("notes/untracked.txt", "new") }},
		{"deletes a file", func(w *treeFakeWorkspaces) { w.removeFile("README.md") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCheckerFixture(t, workflow.AgentRoleChecker, tc.mutate)

			requireScopeViolation(t, c.execute(t), "the checker changed the worktree after its InputTree was taken")
		})
	}
}

// A MAKER (non-strict) snapshots nothing: its scope check is unchanged.
func TestAgentNodeExecutor_MakerRole_RecordsNoInputTree(t *testing.T) {
	c := newCheckerFixture(t, workflow.AgentRoleMaker, func(w *treeFakeWorkspaces) { w.setFile("src/main.go", "the maker's own edit") })

	result := c.execute(t)
	if result.State != domainruntime.ExecutionAttemptSucceeded {
		t.Fatalf("result = %+v, want SUCCEEDED (a maker may change files within scope)", result)
	}
	if got := c.workspaces.snapshotCount(); got != 0 {
		t.Fatalf("SnapshotTree calls = %d, want 0 for a MAKER", got)
	}
	if trees := c.attempt(t).InputTrees; len(trees) != 0 {
		t.Fatalf("maker attempt InputTrees = %v, want none", trees)
	}
}

// A provider without tree support leaves a CHECKER on the pre-V9-01 rule and
// records nothing — fail-closed, never more permissive.
func TestAgentNodeExecutor_CheckerRole_ProviderWithoutTreeSupport_FallsBackAndRecordsNothing(t *testing.T) {
	executor, req, uow, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		role:        workflow.AgentRoleChecker,
		diff:        defaultReadOnlyDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
	})

	result, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != domainruntime.ExecutionAttemptSucceeded {
		t.Fatalf("result = %+v, want SUCCEEDED (empty diff, old rule)", result)
	}
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(context.Background(), req.AttemptID)
		if err != nil {
			return err
		}
		if len(attempt.InputTrees) != 0 {
			t.Fatalf("InputTrees = %v, want none (the provider cannot snapshot trees)", attempt.InputTrees)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// recordInputTree snapshots the fixture's current working tree and records it
// as the attempt's InputTree — the state of an attempt that inherited its
// predecessor's trees (retry/recovery creates it that way) or whose earlier
// run crashed after recording.
func (c checkerFixture) recordInputTree(t *testing.T) string {
	t.Helper()
	treeID, err := c.workspaces.SnapshotTree(context.Background(), ports.WorkspaceHandle{})
	if err != nil {
		t.Fatalf("SnapshotTree: %v", err)
	}
	if err := c.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		applied, err := tx.Runtime().RecordAttemptInputTrees(context.Background(), c.req.AttemptID, map[project.RepositoryID]string{checkerRepository: treeID})
		if err == nil && !applied {
			t.Fatal("RecordAttemptInputTrees was not applied to a fresh attempt")
		}
		return err
	}); err != nil {
		t.Fatalf("RecordAttemptInputTrees: %v", err)
	}
	return treeID
}

// An attempt that already has InputTrees reuses them: it never snapshots a
// replacement. Whatever changed in the worktree since they were recorded —
// here a leftover write of a crashed predecessor — is charged against the
// RECORDED baseline, so a crash can never launder a change into the next
// attempt's input.
func TestAgentNodeExecutor_CheckerReusesRecordedInputTree_NeverResnapshots(t *testing.T) {
	c := newCheckerFixture(t, workflow.AgentRoleChecker, nil)
	recorded := c.recordInputTree(t)
	c.workspaces.setFile("leftover.tmp", "written before the crash")
	snapshotsBefore := c.workspaces.snapshotCount()

	requireScopeViolation(t, c.execute(t), "the leftover must be charged against the RECORDED InputTree, not a fresh snapshot that would absorb it")
	if got := c.attempt(t).InputTrees[checkerRepository]; got != recorded {
		t.Fatalf("InputTrees[%s] = %s after the run, want unchanged %s (set-once)", checkerRepository, got, recorded)
	}
	if got := c.workspaces.snapshotCount() - snapshotsBefore; got != 1 {
		t.Fatalf("the run took %d snapshots, want exactly 1 (the output tree only — the input tree is reused)", got)
	}
}

// With a recorded InputTree and nothing changed since, the checker passes.
func TestAgentNodeExecutor_CheckerWithInheritedInputTree_UnchangedWorktreeSucceeds(t *testing.T) {
	c := newCheckerFixture(t, workflow.AgentRoleChecker, nil)
	recorded := c.recordInputTree(t)

	if result := c.execute(t); result.State != domainruntime.ExecutionAttemptSucceeded {
		t.Fatalf("result = %+v, want SUCCEEDED", result)
	}
	if got := c.attempt(t).InputTrees[checkerRepository]; got != recorded {
		t.Fatalf("InputTrees[%s] = %s, want %s", checkerRepository, got, recorded)
	}
}

// ADR-030 "Hệ quả": a recorded InputTree whose object was pruned is a
// technical failure with an existing error code, and the engine neither
// re-snapshots nor spawns.
func TestAgentNodeExecutor_CheckerRecordedInputTreePruned_FailsPreconditionWithoutResnapshotOrSpawn(t *testing.T) {
	c := newCheckerFixture(t, workflow.AgentRoleChecker, nil)
	recorded := c.recordInputTree(t)
	c.workspaces.forgetTree(recorded)
	snapshotsBefore := c.workspaces.snapshotCount()

	result := c.execute(t)
	if result.State != domainruntime.ExecutionAttemptFailed || result.ErrorCode != errorcode.CodePreconditionFailed {
		t.Fatalf("result = %+v, want FAILED/PRECONDITION_FAILED", result)
	}
	if result.TerminationReason == domainruntime.TerminationReasonScopeViolation {
		t.Fatalf("a pruned tree must not be reported as a scope violation: %+v", result)
	}
	if got := c.workspaces.snapshotCount(); got != snapshotsBefore {
		t.Fatalf("SnapshotTree calls went from %d to %d: the engine must never re-snapshot to replace a recorded InputTree", snapshotsBefore, got)
	}
	if *c.starts != 0 {
		t.Fatal("the agent was spawned although its InputTree is unavailable")
	}
	if got := c.attempt(t).InputTrees[checkerRepository]; got != recorded {
		t.Fatalf("InputTrees[%s] = %s, want the recorded %s untouched", checkerRepository, got, recorded)
	}
}

// If the output-time probe finds the InputTree gone (pruned while the checker
// ran), the verdict is the same technical failure, not a scope violation.
func TestAgentNodeExecutor_CheckerInputTreePrunedWhileRunning_FailsPrecondition(t *testing.T) {
	c := newCheckerFixture(t, workflow.AgentRoleChecker, func(w *treeFakeWorkspaces) {
		// the checker changed something AND its input tree got pruned meanwhile
		w.setFile("src/main.go", "changed")
		w.mu.Lock()
		for id := range w.trees {
			delete(w.trees, id)
		}
		w.mu.Unlock()
	})

	result := c.execute(t)
	if result.State != domainruntime.ExecutionAttemptFailed || result.ErrorCode != errorcode.CodePreconditionFailed {
		t.Fatalf("result = %+v, want FAILED/PRECONDITION_FAILED", result)
	}
}

// Set-once under a race: if another writer records InputTrees between this
// executor's read and its write, the winner's value stays and IS the
// baseline (the loser's snapshot is not written).
func TestAgentNodeExecutor_CheckerInputTree_ConcurrentWriterWins(t *testing.T) {
	c := newCheckerFixture(t, workflow.AgentRoleChecker, nil)

	var winner string
	c.workspaces.onFirstSnapshot = func() {
		// The "other writer": snapshot a DIFFERENT working tree, record it,
		// then put the files back so this executor's own snapshot differs.
		c.workspaces.setFile("other-writer.txt", "x")
		c.workspaces.mu.Lock()
		winner = c.workspaces.snapshotLocked()
		c.workspaces.mu.Unlock()
		c.workspaces.removeFile("other-writer.txt")
		if err := c.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
			applied, err := tx.Runtime().RecordAttemptInputTrees(context.Background(), c.req.AttemptID, map[project.RepositoryID]string{checkerRepository: winner})
			if err == nil && !applied {
				t.Errorf("the other writer's record was not applied")
			}
			return err
		}); err != nil {
			t.Errorf("other writer: %v", err)
		}
	}

	result := c.execute(t)
	if got := c.attempt(t).InputTrees[checkerRepository]; got != winner || winner == "" {
		t.Fatalf("InputTrees[%s] = %q, want the first writer's %q (set-once, the second writer must not overwrite)", checkerRepository, got, winner)
	}
	requireScopeViolation(t, result, "the winner's tree (with other-writer.txt) is the baseline, and the worktree no longer matches it")
}

// A worker that lost its job lease must not record, and must not spawn.
func TestAgentNodeExecutor_CheckerInputTree_LostJobLease_IsIndeterminateAndRecordsNothing(t *testing.T) {
	c := newCheckerFixture(t, workflow.AgentRoleChecker, nil)
	c.req.JobLease.Token++ // a stale fencing token

	_, err := c.executor.Execute(context.Background(), c.req)
	if !errors.Is(err, runtime.ErrIndeterminateExecution) {
		t.Fatalf("Execute error = %v, want ErrIndeterminateExecution (the lease is lost; crash recovery owns the attempt)", err)
	}
	if trees := c.attempt(t).InputTrees; len(trees) != 0 {
		t.Fatalf("InputTrees = %v, want none recorded under a lost lease", trees)
	}
	if *c.starts != 0 {
		t.Fatal("the agent was spawned under a lost lease")
	}
}

// GateNodeExecutor: a MACHINE_GATE after a maker (the diff against the pinned
// commit is non-empty) passes when the evaluator changes nothing, and still
// fails with SCOPE_VIOLATION when the evaluator changes the worktree.
func TestGateNodeExecutor_AfterMaker_UncommittedMakerChangeIsNotAViolation(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{})
	workspaces := newTreeFakeWorkspaces(defaultInScopeDiff(), map[string]string{"src/main.go": "written by an earlier maker"})
	workspaces.captureRevision = workspace.Revision{RepositoryID: "repo-1", VCSObjectID: fixtureRepo1PinnedRevision, WorkspaceGeneration: 1}
	supervisor := &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
		Stdout: string(gateStdout(t, map[string]map[string]string{"lint": {"verdict": "PASS"}})),
	}
	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	executor := runtime.NewGateNodeExecutor(
		uow, ids, store, workspaces, supervisor, fake.SecretResolver{}, registry, redact.NewMatcher(), bridgeFakeCheckpointStore{}, clock.System{},
		&bridgeFakeInterruptionStore{uow: uow}, &bridgeFakeWorkspaceReconciler{},
	)

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != domainruntime.ExecutionAttemptSucceeded {
		t.Fatalf("result = %+v, want SUCCEEDED (the maker's uncommitted change is the gate's input)", result)
	}
	if len(supervisor.Calls) != 1 {
		t.Fatalf("supervisor.Calls = %d, want the evaluator to run once", len(supervisor.Calls))
	}
	var recorded map[project.RepositoryID]string
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(context.Background(), attemptID)
		recorded = attempt.InputTrees
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if recorded[checkerRepository] == "" {
		t.Fatalf("gate attempt InputTrees = %v, want a tree recorded before the evaluator was spawned", recorded)
	}
}

// hookedSupervisor runs onRun inside Run — the stand-in for "what the
// evaluator process did to the worktree while it ran".
type hookedSupervisor struct {
	*fake.ProcessSupervisor
	onRun func()
}

func (s *hookedSupervisor) Run(ctx context.Context, spec ports.ProcessSpec, stdout, stderr io.Writer) (ports.ProcessResult, error) {
	if s.onRun != nil {
		s.onRun()
	}
	return s.ProcessSupervisor.Run(ctx, spec, stdout, stderr)
}

func TestGateNodeExecutor_EvaluatorChangesWorktree_FailsWithScopeViolation(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{})
	workspaces := newTreeFakeWorkspaces(defaultInScopeDiff(), map[string]string{"src/main.go": "written by an earlier maker"})
	workspaces.captureRevision = workspace.Revision{RepositoryID: "repo-1", VCSObjectID: fixtureRepo1PinnedRevision, WorkspaceGeneration: 1}
	supervisor := &hookedSupervisor{
		ProcessSupervisor: &fake.ProcessSupervisor{
			Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
			Stdout: string(gateStdout(t, map[string]map[string]string{"lint": {"verdict": "PASS"}})),
		},
		onRun: func() { workspaces.setFile("src/main.go", "rewritten by the evaluator") },
	}
	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	executor := runtime.NewGateNodeExecutor(
		uow, ids, store, workspaces, supervisor, fake.SecretResolver{}, registry, redact.NewMatcher(), bridgeFakeCheckpointStore{}, clock.System{},
		&bridgeFakeInterruptionStore{uow: uow}, &bridgeFakeWorkspaceReconciler{},
	)

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	requireScopeViolation(t, result, "the evaluator rewrote a file in a read-only mount")
}

// RecordAttemptInputTrees is set-once on every backing store, never bumps the
// attempt's Version (so the executor's later terminal CAS is unaffected),
// and survives a reload.
func TestRecordAttemptInputTrees_SetOnce_FakeAndSQLite(t *testing.T) {
	ctx := context.Background()
	first := map[project.RepositoryID]string{"repo-1": strings.Repeat("a1", 20)}
	second := map[project.RepositoryID]string{"repo-1": strings.Repeat("b2", 20), "repo-2": strings.Repeat("c3", 20)}

	check := func(t *testing.T, uow ports.UnitOfWork, attemptID string) {
		t.Helper()
		before := uowAttempt(t, uow, attemptID)
		if len(before.InputTrees) != 0 {
			t.Fatalf("fresh attempt InputTrees = %v, want none (a first attempt records at execution time)", before.InputTrees)
		}
		applied := func(trees map[project.RepositoryID]string) bool {
			var applied bool
			if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
				var err error
				applied, err = tx.Runtime().RecordAttemptInputTrees(ctx, attemptID, trees)
				return err
			}); err != nil {
				t.Fatalf("RecordAttemptInputTrees: %v", err)
			}
			return applied
		}
		if !applied(first) {
			t.Fatal("first RecordAttemptInputTrees was not applied")
		}
		if applied(second) {
			t.Fatal("second RecordAttemptInputTrees was applied: a recorded InputTree must never be overwritten")
		}
		after := uowAttempt(t, uow, attemptID)
		if len(after.InputTrees) != 1 || after.InputTrees["repo-1"] != first["repo-1"] {
			t.Fatalf("InputTrees = %v, want exactly the first writer's %v", after.InputTrees, first)
		}
		if after.Version != before.Version {
			t.Fatalf("attempt Version %d -> %d: recording InputTrees must not bump it", before.Version, after.Version)
		}
		if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			_, err := tx.Runtime().RecordAttemptInputTrees(ctx, "no-such-attempt", first)
			if err == nil {
				t.Error("RecordAttemptInputTrees(unknown attempt) = nil error, want ErrPersistenceNotFound")
			}
			_, err = tx.Runtime().RecordAttemptInputTrees(ctx, attemptID, nil)
			if err == nil {
				t.Error("RecordAttemptInputTrees(empty) = nil error, want an error")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("fake", func(t *testing.T) {
		_, req, uow, _, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{})
		check(t, uow, req.AttemptID)
	})
	t.Run("sqlite", func(t *testing.T) {
		uow, _, _, _, _, attemptID := sqliteExecutionFixture(t)
		check(t, uow, attemptID)
	})
}

func uowAttempt(t *testing.T, uow ports.UnitOfWork, attemptID string) domainruntime.ExecutionAttempt {
	t.Helper()
	attempt, err := uowGetExecutionAttempt(context.Background(), uow, attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt(%s): %v", attemptID, err)
	}
	return attempt
}

// The AGENT attempt's RECORDED evidence row survives FinalizeExecutionAttempt:
// the diff manifests it references were promoted once (through
// DiffManifestArtifacts, not OutputArtifactRefs), and the row is persisted
// where a later CHECKER's snapshot can find it. An entry naming an artifact
// the evidence does not carry is still rejected.
func TestFinalizeExecutionAttempt_AgentEvidenceEntryReferencesDiffManifests(t *testing.T) {
	ctx := context.Background()
	executor, req, uow, ids, _, _, _ := bridgeFixture(t, bridgeFixtureOptions{
		diff:        defaultInScopeDiff(),
		agentResult: ports.AgentExecutionResult{Status: ports.AgentExecutionSucceeded, TreeQuiesced: true},
		agentEvents: []ports.AgentEventKind{ports.AgentEventExecutionStarted, ports.AgentEventExecutionFinished},
	})
	result, err := executor.Execute(ctx, req)
	if err != nil || result.State != domainruntime.ExecutionAttemptSucceeded {
		t.Fatalf("Execute = %+v, %v; want SUCCEEDED", result, err)
	}
	manifestID := result.Evidence.DiffManifestArtifacts[0].ArtifactID

	tampered := *result.Evidence
	tampered.EvidenceEntries = []ports.EvidenceProposal{{
		Kind: domainruntime.EvidenceKindAgentExecution, Verdict: domainruntime.EvidenceVerdictRecorded,
		ArtifactReferences: []string{"artifact-never-staged"}, PolicyVersion: "policy-1",
	}}
	finalize := func(evidence *ports.AttemptFinalizationEvidence) error {
		_, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
			RunID: req.RunID, NodeRunID: req.NodeRunID, AttemptID: req.AttemptID, ExpectedVersion: loadAttemptVersion(t, uow, req.AttemptID),
			NextState: result.State, TerminationReason: result.TerminationReason, SelectedOutcome: result.SelectedOutcome,
			JobLease: req.JobLease, CorrelationID: "corr-1", Evidence: evidence,
		})
		return err
	}
	if err := finalize(&tampered); err == nil {
		t.Fatal("FinalizeExecutionAttempt accepted an evidence entry naming an artifact the evidence does not carry")
	}
	if state := mustGetArtifactAttachState(t, uow, manifestID); state != "ORPHAN" {
		t.Fatalf("diff manifest AttachState after a rejected finalize = %s, want still ORPHAN (all-or-nothing)", state)
	}

	if err := finalize(result.Evidence); err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}
	if state := mustGetArtifactAttachState(t, uow, manifestID); state != "ATTACHED" {
		t.Fatalf("diff manifest AttachState after finalize = %s, want ATTACHED", state)
	}
	var stored []domainruntime.Evidence
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		stored, err = tx.Runtime().ListEvidenceForAttempt(ctx, req.AttemptID)
		return err
	}); err != nil {
		t.Fatalf("ListEvidenceForAttempt: %v", err)
	}
	if len(stored) != 1 || stored[0].Kind != domainruntime.EvidenceKindAgentExecution || stored[0].Verdict != domainruntime.EvidenceVerdictRecorded ||
		len(stored[0].ArtifactReferences) != 1 || stored[0].ArtifactReferences[0] != manifestID {
		t.Fatalf("stored evidence = %+v, want one %s/%s row referencing diff manifest %s",
			stored, domainruntime.EvidenceKindAgentExecution, domainruntime.EvidenceVerdictRecorded, manifestID)
	}
}
