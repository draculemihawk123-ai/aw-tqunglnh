package agentevents_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// This file is V5-08A's own real, full-pipeline verification (this
// session's own established "only a full pipeline run counts as real
// verification" lesson): a real sqlite.Store (real foreign keys, real
// UNIQUE constraints) and, for the diff tests, a real
// internal/adapters/gitworktree.Provider against a real temporary Git
// repository — never fakes standing in for either. It deliberately does
// NOT also spin up a real Claude/Codex fake-CLI subprocess
// (internal/adapters/providers' own contract_test.go already proves that
// half — a real AgentExecutor.Start correctly TRANSLATES raw provider
// output into ports.AgentEvent calls): these tests instead drive Sink.Accept
// directly with ports.AgentEvent sequences shaped exactly like claude.go's
// own normalizer.emit calls (internal/adapters/providers/claude/claude.go),
// which is what the V5-08 Q1 answer's "gọi trực tiếp Start/Resume để kiểm
// tra adapter–sink contract" permits without requiring the whole subprocess
// machinery for a concern one layer below it.

const sqliteFixtureAttemptID = "attempt-1"

// sqliteSinkFixture opens a real sqlite database, seeds the minimal
// owners/execution-attempt fixture chain agent_events/checkpoints' own real
// foreign keys require (sqlite.SeedFixtureOwners/SeedFixtureExecutionAttempt
// — exported by that package specifically for cross-package acceptance
// tests like this one), and returns everything a real Sink needs.
func sqliteSinkFixture(t *testing.T) (store *sqlite.Store, uow ports.UnitOfWork, runID, nodeRunID, attemptID string) {
	t.Helper()
	ctx := context.Background()
	dbStore, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { dbStore.Close() })

	if err := sqlite.SeedFixtureOwners(ctx, dbStore, "proj-1", "family-1", "work-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := sqlite.SeedFixtureExecutionAttempt(ctx, dbStore, "proj-1", "family-1", "work-1", sqliteFixtureAttemptID); err != nil {
		t.Fatalf("SeedFixtureExecutionAttempt: %v", err)
	}
	// Mirrors SeedFixtureExecutionAttempt's own internal, documented
	// "wf-run-"/"node-run-" suffix derivation exactly (fixtures.go) — it has
	// no return value for these, so a caller outside that package
	// reconstructs them the same deterministic way.
	return dbStore, sqlite.NewUnitOfWork(dbStore), "wf-run-" + sqliteFixtureAttemptID, "node-run-" + sqliteFixtureAttemptID, sqliteFixtureAttemptID
}

func newRealRegistry() *eventschema.Registry {
	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	return registry
}

func TestSinkSQLite_CheckpointRestart(t *testing.T) {
	store, uow, runID, nodeRunID, attemptID := sqliteSinkFixture(t)
	ctx := context.Background()

	sink, err := agentevents.NewSink(ctx, agentevents.Config{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ContextSnapshotID: "snapshot-1",
		// No Mounts: this test only exercises the event/checkpoint contract,
		// so Workspaces never has Diff/CaptureRevision called on it — a
		// trivial fake stands in (see sink_test.go's own fakeWorkspaceProvider,
		// same package).
		Workspaces: &fakeWorkspaceProvider{},
		Registry:   newRealRegistry(), Matcher: redact.NewMatcher(), UOW: uow, Checkpoints: store,
		IDs: idsource.NewSequential("evt"), Clock: clock.NewFixed(time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}
	if err := sink.Accept(ctx, ports.AgentEvent{Sequence: 1, Kind: ports.AgentEventExecutionStarted, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("accept EXECUTION_STARTED: %v", err)
	}
	if err := sink.Accept(ctx, ports.AgentEvent{
		Sequence: 2, Kind: ports.AgentEventCheckpointProposed, ObservedAt: time.Now().UTC(),
		Session: &ports.ProviderSessionRef{Provider: ports.ProviderClaude, SessionID: "sess-1"},
	}); err != nil {
		t.Fatalf("accept CHECKPOINT_PROPOSED: %v", err)
	}

	// "Checkpoint restart": a fresh read against the real durable store —
	// exactly the query worker.StartFreshFromLatestCheckpoint's own
	// recovery path performs — must resolve the checkpoint this Sink just
	// captured, byte-for-byte.
	restarted, err := store.LoadLatestCheckpoint(ctx, runtime.ExecutionAttemptID(attemptID))
	if err != nil {
		t.Fatalf("LoadLatestCheckpoint: %v", err)
	}
	if restarted.Sequence != 1 {
		t.Fatalf("restarted checkpoint sequence = %d, want 1", restarted.Sequence)
	}
	if restarted.CanonicalEventSequence != 2 {
		t.Fatalf("restarted checkpoint canonical event sequence = %d, want 2", restarted.CanonicalEventSequence)
	}
	if string(restarted.AttemptID) != attemptID || string(restarted.RunID) != runID || string(restarted.NodeRunID) != nodeRunID {
		t.Fatalf("restarted checkpoint identity = %+v, want attempt=%s run=%s nodeRun=%s", restarted, attemptID, runID, nodeRunID)
	}
	if restarted.SharedStateHash == "" {
		t.Fatal("restarted checkpoint has an empty shared state hash")
	}
}

func TestSinkSQLite_SecretFixtureSearchIsZero(t *testing.T) {
	// redact.Matcher matches by exact equality only (its own package doc
	// comment) — the fixture value must BE the secret, not merely contain
	// it, mirroring internal/app/message's own established convention.
	const secret = "hunter2-sk-live-9f8e7d"
	store, uow, runID, nodeRunID, attemptID := sqliteSinkFixture(t)
	ctx := context.Background()

	sink, err := agentevents.NewSink(ctx, agentevents.Config{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ContextSnapshotID: "snapshot-1",
		Workspaces: &fakeWorkspaceProvider{},
		Registry:   newRealRegistry(), Matcher: redact.NewMatcher(secret), UOW: uow, Checkpoints: store,
		IDs: idsource.NewSequential("evt"), Clock: clock.NewFixed(time.Now()),
	})
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}
	events := []ports.AgentEvent{
		{Sequence: 1, Kind: ports.AgentEventAssistantMessage, Message: secret, ObservedAt: time.Now().UTC()},
		{Sequence: 2, Kind: ports.AgentEventToolCallFinished, Tool: &ports.AgentToolEvent{CallID: "c1", Name: "shell", Output: secret}, ObservedAt: time.Now().UTC()},
		{Sequence: 3, Kind: ports.AgentEventDiagnostic, Diagnostic: &ports.AgentDiagnostic{Code: "X", Message: secret}, ObservedAt: time.Now().UTC()},
		{Sequence: 4, Kind: ports.AgentEventExecutionFinished, ProviderMetadata: map[string]string{"raw_type": secret}, ObservedAt: time.Now().UTC()},
	}
	for _, event := range events {
		if err := sink.Accept(ctx, event); err != nil {
			t.Fatalf("accept event %d: %v", event.Sequence, err)
		}
	}
	if err := sink.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	rows, err := readAgentEventRows(ctx, store, attemptID)
	if err != nil {
		t.Fatalf("read agent_events: %v", err)
	}
	if len(rows) != len(events) {
		t.Fatalf("agent_events rows scanned = %d, want %d", len(rows), len(events))
	}
	for _, row := range rows {
		if strings.Contains(row.PayloadJSON, secret) {
			t.Fatalf("agent_events.payload_json contains the raw secret fixture: %s", row.PayloadJSON)
		}
	}
}

func TestSinkSQLite_DiffExceedsEffectiveScope(t *testing.T) {
	ctx := context.Background()
	store, uow, runID, nodeRunID, attemptID := sqliteSinkFixture(t)

	fixtureRoot := t.TempDir()
	repositoryPath := createSinkFixtureGitRepository(t, filepath.Join(fixtureRoot, "repo"))
	provider, err := gitworktree.New(gitworktree.Config{Root: filepath.Join(fixtureRoot, "workspaces")})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}
	handle, err := provider.Provision(ctx, ports.ProvisionSpec{
		RepositoryID: project.RepositoryID("repo-1"), LocalRepository: repositoryPath, BaseRef: "main",
		FamilyID: work.TaskFamilyID("family-1"), WorkspaceSetID: "wset-1", Generation: 1,
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	workingDir, err := provider.WorkingDirectory(ctx, handle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}
	// A real, uncommitted change with NO corresponding WRITE grant at all
	// (EffectiveScope below is left empty) — scopeguard.ValidateDiffs
	// treats "no write scope for this repository" as "any change is a
	// violation," the same read-only-mount-then-any-write-is-a-violation
	// behavior a real unauthorized mutation would hit in production.
	if err := os.WriteFile(filepath.Join(workingDir, "unauthorized.txt"), []byte("mutated\n"), 0o600); err != nil {
		t.Fatalf("write unauthorized change: %v", err)
	}

	sink, err := agentevents.NewSink(ctx, agentevents.Config{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ContextSnapshotID: "snapshot-1",
		Mounts:     []agentevents.Mount{{RepositoryID: "repo-1", Handle: handle}},
		Workspaces: provider, Registry: newRealRegistry(), Matcher: redact.NewMatcher(),
		UOW: uow, Checkpoints: store, IDs: idsource.NewSequential("evt"), Clock: clock.NewFixed(time.Now()),
	})
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}
	if err := sink.Accept(ctx, ports.AgentEvent{Sequence: 1, Kind: ports.AgentEventExecutionStarted, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("accept EXECUTION_STARTED: %v", err)
	}
	err = sink.Accept(ctx, ports.AgentEvent{Sequence: 2, Kind: ports.AgentEventCheckpointProposed, ObservedAt: time.Now().UTC()})
	if !errors.Is(err, scopeguard.ErrScopeViolation) {
		t.Fatalf("accept checkpoint-proposed with out-of-scope diff error = %v, want scopeguard.ErrScopeViolation", err)
	}

	// The violation must block the Checkpoint from ever being recorded —
	// never a partially-committed evidence trail.
	if _, err := store.LoadLatestCheckpoint(ctx, runtime.ExecutionAttemptID(attemptID)); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("LoadLatestCheckpoint after scope violation = %v, want ErrPersistenceNotFound", err)
	}
	// The events flush (Accept's own flushLocked call) runs BEFORE diff
	// capture — both events up to and including the triggering one are
	// still durably recorded, exactly the ordering this package's own
	// captureCheckpointLocked doc comment describes.
	rows, err := readAgentEventRows(ctx, store, attemptID)
	if err != nil {
		t.Fatalf("read agent_events: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("agent_events rows after scope violation = %d, want 2 (both events still durably flushed)", len(rows))
	}
}

func TestSinkSQLite_RealisticEventSequenceEndToEnd(t *testing.T) {
	store, uow, runID, nodeRunID, attemptID := sqliteSinkFixture(t)
	ctx := context.Background()

	sink, err := agentevents.NewSink(ctx, agentevents.Config{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ContextSnapshotID: "snapshot-1",
		Workspaces: &fakeWorkspaceProvider{},
		Registry:   newRealRegistry(), Matcher: redact.NewMatcher(), UOW: uow, Checkpoints: store,
		IDs: idsource.NewSequential("evt"), Clock: clock.NewFixed(time.Now()),
	})
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}

	// Mirrors claude.go's own real emission order exactly: EXECUTION_STARTED,
	// assistant/tool traffic, USAGE_REPORTED, CHECKPOINT_PROPOSED, then (on a
	// reported provider failure) DIAGNOSTIC, and always EXECUTION_FINISHED
	// last — the CHECKPOINT_PROPOSED event is deliberately NOT the final
	// event in the stream.
	sequence := []ports.AgentEvent{
		{Sequence: 1, Kind: ports.AgentEventExecutionStarted},
		{Sequence: 2, Kind: ports.AgentEventAssistantMessage, Message: "working on it"},
		{Sequence: 3, Kind: ports.AgentEventToolCallStarted, Tool: &ports.AgentToolEvent{CallID: "c1", Name: "shell"}},
		{Sequence: 4, Kind: ports.AgentEventToolCallFinished, Tool: &ports.AgentToolEvent{CallID: "c1", Name: "shell", Output: "ok"}},
		{Sequence: 5, Kind: ports.AgentEventUsageReported, Usage: &ports.AgentUsage{InputTokens: 100, OutputTokens: 20}},
		{Sequence: 6, Kind: ports.AgentEventCheckpointProposed, Session: &ports.ProviderSessionRef{Provider: ports.ProviderClaude, SessionID: "sess-1"}},
		{Sequence: 7, Kind: ports.AgentEventDiagnostic, Diagnostic: &ports.AgentDiagnostic{Code: "PROVIDER_REPORTED_FAILURE", Message: "Claude reported a failed result"}},
		{Sequence: 8, Kind: ports.AgentEventExecutionFinished, Message: "FAILED"},
	}
	for i := range sequence {
		sequence[i].ObservedAt = time.Now().UTC()
		sequence[i].AttemptID = ports.ExecutionAttemptID(attemptID)
		if err := sink.Accept(ctx, sequence[i]); err != nil {
			t.Fatalf("accept event %d (%s): %v", sequence[i].Sequence, sequence[i].Kind, err)
		}
	}
	// Events after the checkpoint (7, 8) are still only buffered until an
	// explicit Flush — exactly the "caller must flush the tail" contract
	// Sink.Flush's own doc comment states.
	rows, err := readAgentEventRows(ctx, store, attemptID)
	if err != nil {
		t.Fatalf("read agent_events before flush: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("agent_events rows before final Flush = %d, want 6", len(rows))
	}
	if err := sink.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rows, err = readAgentEventRows(ctx, store, attemptID)
	if err != nil {
		t.Fatalf("read agent_events after flush: %v", err)
	}
	if len(rows) != len(sequence) {
		t.Fatalf("agent_events rows after Flush = %d, want %d", len(rows), len(sequence))
	}
	for i, row := range rows {
		if row.Sequence != uint64(i+1) {
			t.Fatalf("row %d sequence = %d, want %d (ordering preserved)", i, row.Sequence, i+1)
		}
	}

	checkpoint, err := store.LoadLatestCheckpoint(ctx, runtime.ExecutionAttemptID(attemptID))
	if err != nil {
		t.Fatalf("LoadLatestCheckpoint: %v", err)
	}
	if checkpoint.CanonicalEventSequence != 6 {
		t.Fatalf("checkpoint canonical event sequence = %d, want 6", checkpoint.CanonicalEventSequence)
	}
}

func readAgentEventRows(ctx context.Context, store *sqlite.Store, attemptID string) ([]ports.AgentEventRecord, error) {
	var records []ports.AgentEventRecord
	uow := sqlite.NewUnitOfWork(store)
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		records, err = tx.AgentEvents().ListByAttempt(ctx, attemptID)
		return err
	})
	return records, err
}

func createSinkFixtureGitRepository(t *testing.T, repositoryPath string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is required for this test: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(repositoryPath), 0o755); err != nil {
		t.Fatalf("create repository parent: %v", err)
	}
	runSinkFixtureGit(t, "", "init", "--initial-branch=main", repositoryPath)
	runSinkFixtureGit(t, repositoryPath, "config", "user.name", "Agent Kit Test")
	runSinkFixtureGit(t, repositoryPath, "config", "user.email", "agent-kit@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "service.txt"), []byte("v0\n"), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	runSinkFixtureGit(t, repositoryPath, "add", "--", "service.txt")
	runSinkFixtureGit(t, repositoryPath, "commit", "-m", "initial fixture")
	return repositoryPath
}

func runSinkFixtureGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	commandArguments := arguments
	if directory != "" {
		commandArguments = append([]string{"-C", directory}, arguments...)
	}
	command := exec.Command("git", commandArguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}
