package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	domain "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

func openAdapterBuildTestStore(t *testing.T, name string) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func writeAdapterBuildExecutable(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider-cli")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable fixture: %v", err)
	}
	return path
}

func adapterBuildManifest() domain.CapabilityManifest {
	return domain.CapabilityManifest{
		SupportsStart: true, SupportsResume: true, SupportsCancel: true,
		CanonicalEventKinds: []string{"TEXT_DELTA", "TOOL_CALL"},
	}
}

func adapterBuildProbeRequest(path string) adapterbuild.ProbeRequest {
	return adapterbuild.ProbeRequest{
		ProviderKey: "claude", ExecutablePath: path, ProtocolVersion: "claude-stream-json/v1",
		CapabilityManifest: adapterBuildManifest(), OS: "linux", Toolchain: "node-20", ConfigIdentity: "default",
	}
}

// adapterBuildTestCommand builds a minimal, valid installation-scoped
// ports.Command envelope for this file's Probe/Register call sites —
// mirroring internal/app/adapterbuild's own commands_test.go testCommand
// helper (this file is a different package, sqlite, so it needs its own
// copy rather than importing a _test.go symbol across packages).
func adapterBuildTestCommand(commandType, idempotencyKey, actor string) ports.Command {
	id := commandType + "-" + idempotencyKey
	return ports.Command{
		ID: id, IdempotencyKey: idempotencyKey, Actor: actor,
		CorrelationID: id, Scope: ports.InstallationScope(), RequestedAt: time.Now().UTC(),
		Type: commandType, RequestHash: "hash-" + idempotencyKey,
	}
}

// TestAdapterBuild_ProbeRegisterRoundTrip_RealSQLite exercises the full
// Probe->Register flow against the real sqlite adapter (not the fake) —
// the app-layer test suite (internal/app/adapterbuild) already covers
// this exhaustively against the in-memory fake; this test's own job is
// proving the real implementation satisfies the identical contract, not
// re-deriving every case.
func TestAdapterBuild_ProbeRegisterRoundTrip_RealSQLite(t *testing.T) {
	ctx := context.Background()
	store := openAdapterBuildTestStore(t, "agentkit-adapterbuild-roundtrip.db")
	uow := NewUnitOfWork(store)
	path := writeAdapterBuildExecutable(t, "binary-content-v1")

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, adapterBuildTestCommand("ProbeAdapterBuild", "probe-1", "operator-1"), adapterBuildProbeRequest(path))
	if err != nil {
		t.Fatalf("ProbeAdapterBuild: %v", err)
	}
	result, err := adapterbuild.RegisterAdapterBuild(ctx, uow, adapterBuildTestCommand("RegisterAdapterBuild", "register-1", "operator-1"), adapterbuild.RegisterRequest{
		Token: token, CapabilityManifest: adapterBuildManifest(),
	})
	if err != nil {
		t.Fatalf("RegisterAdapterBuild: %v", err)
	}
	if result.AlreadyExisted {
		t.Fatal("first registration should not report AlreadyExisted")
	}

	loaded, err := adapterbuild.GetAdapterBuild(ctx, uow, result.Build.ID())
	if err != nil {
		t.Fatalf("GetAdapterBuild: %v", err)
	}
	if loaded.Tuple().ExecutableContentHash != result.Build.Tuple().ExecutableContentHash {
		t.Fatal("round-tripped build content hash does not match what was registered")
	}
	if loaded.CapabilityManifest().SupportsCancel != true {
		t.Fatal("round-tripped capability manifest lost a field")
	}
}

// TestAdapterBuild_SigningKeySurvivesRestart proves the per-installation
// signing key ADR-022 requires persists across a process restart — a
// key that only lived in memory would invalidate every outstanding
// candidate token on every restart, which is not what ADR-022 asks for
// (only an explicit rotation should do that).
func TestAdapterBuild_SigningKeySurvivesRestart(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-adapterbuild-restart.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	uow := NewUnitOfWork(store)
	path := writeAdapterBuildExecutable(t, "binary-content-v1")

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, adapterBuildTestCommand("ProbeAdapterBuild", "probe-1", "operator-1"), adapterBuildProbeRequest(path))
	if err != nil {
		t.Fatalf("ProbeAdapterBuild: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before simulated restart: %v", err)
	}

	reopened, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("reopen after restart: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })
	reopenedUow := NewUnitOfWork(reopened)

	result, err := adapterbuild.RegisterAdapterBuild(ctx, reopenedUow, adapterBuildTestCommand("RegisterAdapterBuild", "register-1", "operator-1"), adapterbuild.RegisterRequest{
		Token: token, CapabilityManifest: adapterBuildManifest(),
	})
	if err != nil {
		t.Fatalf("RegisterAdapterBuild after restart with a token signed before restart: %v", err)
	}
	if result.Build.ID() == "" {
		t.Fatal("expected a successfully registered build")
	}
}

// TestAdapterBuildVersionsTable_HasNoProjectIDColumn is V2-07A's own
// structural proof for its "cross-project" contract test: the registry
// is genuinely installation-scoped, not accidentally project-scoped like
// definitions/definition_versions — there is no project_id column at all
// for any query to (correctly or incorrectly) filter on.
func TestAdapterBuildVersionsTable_HasNoProjectIDColumn(t *testing.T) {
	store := openAdapterBuildTestStore(t, "agentkit-adapterbuild-schema.db")
	rows, err := store.db.QueryContext(context.Background(), `PRAGMA table_info(adapter_build_versions)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan table_info row: %v", err)
		}
		columns = append(columns, name)
	}
	for _, column := range columns {
		if column == "project_id" {
			t.Fatal("adapter_build_versions must never have a project_id column — the registry is installation-scoped")
		}
	}
}

// TestAdapterBuild_ListVisibleAcrossAnyCaller_NoProjectFiltering is the
// behavioral half of the cross-project contract test: a build registered
// is immediately visible to List/Get with no project context supplied at
// all (the application command signatures themselves take no project
// parameter), confirming the registry is genuinely global.
func TestAdapterBuild_ListVisibleAcrossAnyCaller_NoProjectFiltering(t *testing.T) {
	ctx := context.Background()
	store := openAdapterBuildTestStore(t, "agentkit-adapterbuild-crossproject.db")
	uow := NewUnitOfWork(store)
	path := writeAdapterBuildExecutable(t, "binary-content-v1")

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, adapterBuildTestCommand("ProbeAdapterBuild", "probe-1", "operator-1"), adapterBuildProbeRequest(path))
	if err != nil {
		t.Fatalf("ProbeAdapterBuild: %v", err)
	}
	if _, err := adapterbuild.RegisterAdapterBuild(ctx, uow, adapterBuildTestCommand("RegisterAdapterBuild", "register-1", "operator-1"), adapterbuild.RegisterRequest{
		Token: token, CapabilityManifest: adapterBuildManifest(),
	}); err != nil {
		t.Fatalf("RegisterAdapterBuild: %v", err)
	}

	// Two entirely independent UnitOfWork handles over the SAME store —
	// standing in for two different callers acting on behalf of two
	// different projects — must see the identical global list.
	firstView, err := adapterbuild.ListAdapterBuilds(ctx, NewUnitOfWork(store))
	if err != nil {
		t.Fatalf("ListAdapterBuilds (first caller): %v", err)
	}
	secondView, err := adapterbuild.ListAdapterBuilds(ctx, NewUnitOfWork(store))
	if err != nil {
		t.Fatalf("ListAdapterBuilds (second caller): %v", err)
	}
	if len(firstView) != 1 || len(secondView) != 1 {
		t.Fatalf("len(firstView)=%d len(secondView)=%d, want 1 each", len(firstView), len(secondView))
	}
	if firstView[0].ID() != secondView[0].ID() {
		t.Fatal("two independent callers must see the identical registered build")
	}
}

// TestAdapterBuild_RegisteringNewBuildNeverRepinsExistingWorkflowVersion
// is V2-07A's own "run đang chạy không bị repin khi build mới được đăng
// ký" bar: registering a new AdapterBuildVersion must have zero
// observable effect on any already-published WorkflowVersion's own
// dependency manifest (the thing a Run's pin ultimately traces back to)
// — proven directly, not just assumed true because no code path
// currently wires the two together.
func TestAdapterBuild_RegisteringNewBuildNeverRepinsExistingWorkflowVersion(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-adapterbuild-norepin.db")
	store := openWorkflowTestStore(t, ctx, databasePath)
	t.Cleanup(func() { store.Close() })
	seedWorkflowRunOwners(t, ctx, store)

	definition := testWorkflowDefinition()
	v1 := compileWorkflowVersion(t, definition, "workflow-v1", 1, workflowDocumentV1(), "1")
	if _, err := store.PublishWorkflowVersion(ctx, definition, v1); err != nil {
		t.Fatalf("publish workflow version: %v", err)
	}

	var manifestBefore string
	if err := store.db.QueryRowContext(ctx,
		`SELECT dependency_manifest FROM workflow_versions WHERE id = ?`, string(v1.ID()),
	).Scan(&manifestBefore); err != nil {
		t.Fatalf("read dependency_manifest before: %v", err)
	}

	uow := NewUnitOfWork(store)
	path := writeAdapterBuildExecutable(t, "binary-content-v1")
	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, adapterBuildTestCommand("ProbeAdapterBuild", "probe-1", "operator-1"), adapterBuildProbeRequest(path))
	if err != nil {
		t.Fatalf("ProbeAdapterBuild: %v", err)
	}
	if _, err := adapterbuild.RegisterAdapterBuild(ctx, uow, adapterBuildTestCommand("RegisterAdapterBuild", "register-1", "operator-1"), adapterbuild.RegisterRequest{
		Token: token, CapabilityManifest: adapterBuildManifest(),
	}); err != nil {
		t.Fatalf("RegisterAdapterBuild: %v", err)
	}

	var manifestAfter string
	if err := store.db.QueryRowContext(ctx,
		`SELECT dependency_manifest FROM workflow_versions WHERE id = ?`, string(v1.ID()),
	).Scan(&manifestAfter); err != nil {
		t.Fatalf("read dependency_manifest after: %v", err)
	}
	if manifestBefore != manifestAfter {
		t.Fatalf("registering a new adapter build changed an existing WorkflowVersion's dependency_manifest:\nbefore: %s\nafter:  %s",
			manifestBefore, manifestAfter)
	}

	var runCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_runs`).Scan(&runCount); err != nil {
		t.Fatalf("count workflow_runs: %v", err)
	}
	if runCount != 0 {
		t.Fatalf("test setup invariant broken: expected no workflow_runs rows, found %d", runCount)
	}
}
