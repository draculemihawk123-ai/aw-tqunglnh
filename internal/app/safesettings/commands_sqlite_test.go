package safesettings_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/safesettings"
)

func openSQLiteStore(t *testing.T, name string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

const validDesiredJSON = `{
	"managedWorkspaceRoot":"data/workspaces",
	"managedArtifactRoot":"data/artifacts",
	"evidenceRetention":"168h0m0s",
	"processOutputLimit":1048576,
	"providerExecutablePath":"C:/tools/claude/claude.exe",
	"providerDefaultModel":"claude-sonnet-4-5",
	"providerCredentialRef":"keychain:claude-api-key"
}`

func installationCommand(idempotencyKey, requestHash string, expectedVersion uint64) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.InstallationScope(), ExpectedVersion: expectedVersion,
		RequestedAt: time.Now().UTC(), Type: "UpdateSafeSettings", RequestHash: requestHash,
	}
}

// TestGetSafeSettings_FreshDatabase is this task's own "migration applies
// cleanly" Verify scenario, seen through the public application query: a
// freshly migrated database's GetSafeSettings reports Version 1, the
// zero-value ("never configured") desired document, and RestartRequired
// false (nothing has ever been set, so there is nothing a restart would
// newly apply).
func TestGetSafeSettings_FreshDatabase(t *testing.T) {
	uow := sqlite.NewUnitOfWork(openSQLiteStore(t, "get-fresh.db"))
	result, err := safesettings.GetSafeSettings(context.Background(), uow)
	if err != nil {
		t.Fatalf("GetSafeSettings: %v", err)
	}
	if result.Version != 1 {
		t.Fatalf("result.Version = %d, want 1", result.Version)
	}
	if !result.Desired.IsZero() {
		t.Fatalf("result.Desired = %+v, want IsZero()", result.Desired)
	}
	if result.RestartRequired {
		t.Fatal("result.RestartRequired = true for the never-configured document, want false")
	}
}

// TestUpdateSafeSettings_HappyPath is this task's own end-to-end command
// proof: a well-formed update persists, bumps version, appends a
// registered SAFE_SETTINGS_UPDATED v1 event, and records a receipt.
func TestUpdateSafeSettings_HappyPath(t *testing.T) {
	uow := sqlite.NewUnitOfWork(openSQLiteStore(t, "update-happy.db"))
	cmd := installationCommand("idem-1", "hash-1", 1)

	result, err := safesettings.UpdateSafeSettings(context.Background(), uow, idsource.Random{}, cmd,
		safesettings.UpdateSafeSettingsRequest{DesiredJSON: json.RawMessage(validDesiredJSON)})
	if err != nil {
		t.Fatalf("UpdateSafeSettings: %v", err)
	}
	if result.Version != 2 {
		t.Fatalf("result.Version = %d, want 2", result.Version)
	}
	if result.Desired.ManagedWorkspaceRoot != "data/workspaces" || result.Desired.ProviderDefaultModel != "claude-sonnet-4-5" {
		t.Fatalf("result.Desired = %+v, unexpected", result.Desired)
	}
	if !result.RestartRequired {
		t.Fatal("result.RestartRequired = false after a real configuration, want true")
	}

	// The SAFE_SETTINGS_UPDATED event itself is proven registered/decodable
	// by event_schema_test.go's own golden/real-payload pair; here the CAS
	// having actually landed (in the SAME transaction the event append used,
	// GC-INV-15) is what a fresh read proves.
	reread, err := safesettings.GetSafeSettings(context.Background(), uow)
	if err != nil {
		t.Fatalf("GetSafeSettings (reread): %v", err)
	}
	if reread.Version != 2 || reread.Desired != result.Desired {
		t.Fatalf("reread = %+v, want version 2 matching the just-updated document %+v", reread, result.Desired)
	}
}

// TestUpdateSafeSettings_ReplayReturnsFirstResult is this task's own
// "replay (idempotency key)" Verify scenario: an identical retry (same
// IdempotencyKey, same RequestHash) never re-applies the CAS — it returns
// the FIRST call's own stored result, and the stored version never
// advances a second time.
func TestUpdateSafeSettings_ReplayReturnsFirstResult(t *testing.T) {
	uow := sqlite.NewUnitOfWork(openSQLiteStore(t, "update-replay.db"))
	cmd := installationCommand("idem-replay", "hash-replay", 1)
	req := safesettings.UpdateSafeSettingsRequest{DesiredJSON: json.RawMessage(validDesiredJSON)}

	first, err := safesettings.UpdateSafeSettings(context.Background(), uow, idsource.Random{}, cmd, req)
	if err != nil {
		t.Fatalf("first UpdateSafeSettings: %v", err)
	}
	second, err := safesettings.UpdateSafeSettings(context.Background(), uow, idsource.Random{}, cmd, req)
	if err != nil {
		t.Fatalf("replayed UpdateSafeSettings: %v", err)
	}
	if second != first {
		t.Fatalf("replayed result = %+v, want identical to first result %+v", second, first)
	}

	current, err := safesettings.GetSafeSettings(context.Background(), uow)
	if err != nil {
		t.Fatalf("GetSafeSettings: %v", err)
	}
	if current.Version != 2 {
		t.Fatalf("current.Version = %d after replay, want 2 (replay must never re-apply the CAS)", current.Version)
	}
}

// TestUpdateSafeSettings_ReplayWithDifferentHashConflicts proves the same
// IdempotencyKey reused for a genuinely different payload is rejected, not
// silently treated as a replay.
func TestUpdateSafeSettings_ReplayWithDifferentHashConflicts(t *testing.T) {
	uow := sqlite.NewUnitOfWork(openSQLiteStore(t, "update-replay-conflict.db"))
	cmd1 := installationCommand("idem-conflict", "hash-A", 1)
	_, err := safesettings.UpdateSafeSettings(context.Background(), uow, idsource.Random{}, cmd1,
		safesettings.UpdateSafeSettingsRequest{DesiredJSON: json.RawMessage(validDesiredJSON)})
	if err != nil {
		t.Fatalf("first UpdateSafeSettings: %v", err)
	}

	cmd2 := installationCommand("idem-conflict", "hash-B", 1)
	_, err = safesettings.UpdateSafeSettings(context.Background(), uow, idsource.Random{}, cmd2,
		safesettings.UpdateSafeSettingsRequest{DesiredJSON: json.RawMessage(validDesiredJSON)})
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("second UpdateSafeSettings (same key, different hash) err = %v, want ports.ErrReceiptConflict", err)
	}
}

// TestUpdateSafeSettings_ConcurrentUpdateCASConflict is this task's own
// "concurrency" Verify scenario at the application-command layer: two
// distinct commands (different idempotency keys — genuinely different
// requests, not a replay) both observing ExpectedVersion 1 — only the
// first may win.
func TestUpdateSafeSettings_ConcurrentUpdateCASConflict(t *testing.T) {
	uow := sqlite.NewUnitOfWork(openSQLiteStore(t, "update-cas-conflict.db"))
	cmd1 := installationCommand("idem-first", "hash-first", 1)
	_, err := safesettings.UpdateSafeSettings(context.Background(), uow, idsource.Random{}, cmd1,
		safesettings.UpdateSafeSettingsRequest{DesiredJSON: json.RawMessage(validDesiredJSON)})
	if err != nil {
		t.Fatalf("first UpdateSafeSettings: %v", err)
	}

	cmd2 := installationCommand("idem-second", "hash-second", 1) // stale: still claims version 1
	_, err = safesettings.UpdateSafeSettings(context.Background(), uow, idsource.Random{}, cmd2,
		safesettings.UpdateSafeSettingsRequest{DesiredJSON: json.RawMessage(validDesiredJSON)})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("second UpdateSafeSettings (stale ExpectedVersion) err = %v, want ports.ErrOptimisticConflict", err)
	}
}

// TestUpdateSafeSettings_NonInstallationScopeRejected is ADR-025's own
// "Handler MUST validate scope khớp command type" for this specific
// installation-only command.
func TestUpdateSafeSettings_NonInstallationScopeRejected(t *testing.T) {
	uow := sqlite.NewUnitOfWork(openSQLiteStore(t, "update-wrong-scope.db"))
	cmd := installationCommand("idem-scope", "hash-scope", 1)
	cmd.Scope = ports.ProjectScope("some-project")

	_, err := safesettings.UpdateSafeSettings(context.Background(), uow, idsource.Random{}, cmd,
		safesettings.UpdateSafeSettingsRequest{DesiredJSON: json.RawMessage(validDesiredJSON)})
	if !errors.Is(err, safesettings.ErrNotInstallationScoped) {
		t.Fatalf("err = %v, want safesettings.ErrNotInstallationScoped", err)
	}
}

// TestUpdateSafeSettings_UnknownFieldRejected is this task's own
// "unknown/startup-security field" Verify scenario at the command layer —
// including an attempt to smuggle in one of the explicitly forbidden
// fields (databasePath).
func TestUpdateSafeSettings_UnknownFieldRejected(t *testing.T) {
	uow := sqlite.NewUnitOfWork(openSQLiteStore(t, "update-unknown-field.db"))
	cmd := installationCommand("idem-unknown", "hash-unknown", 1)
	payload := `{"managedWorkspaceRoot":"w","managedArtifactRoot":"a","evidenceRetention":"1h","processOutputLimit":1,"providerExecutablePath":"p","providerDefaultModel":"m","providerCredentialRef":"r","databasePath":"/tmp/evil.db"}`

	_, err := safesettings.UpdateSafeSettings(context.Background(), uow, idsource.Random{}, cmd,
		safesettings.UpdateSafeSettingsRequest{DesiredJSON: json.RawMessage(payload)})
	if err == nil {
		t.Fatal("UpdateSafeSettings(unknown field databasePath) = nil error, want rejection")
	}

	current, getErr := safesettings.GetSafeSettings(context.Background(), uow)
	if getErr != nil {
		t.Fatalf("GetSafeSettings: %v", getErr)
	}
	if current.Version != 1 {
		t.Fatalf("current.Version = %d after rejected update, want unchanged 1", current.Version)
	}
}

// TestUpdateSafeSettings_InvalidValueRejected is this task's own "invalid
// retention/model/provider ref" Verify scenario, exercised through the
// public command (domain Validate is unit-tested exhaustively in
// internal/domain/safesettings; this proves the command layer actually
// calls it before ever touching storage).
func TestUpdateSafeSettings_InvalidValueRejected(t *testing.T) {
	uow := sqlite.NewUnitOfWork(openSQLiteStore(t, "update-invalid-value.db"))
	cmd := installationCommand("idem-invalid", "hash-invalid", 1)
	payload := `{"managedWorkspaceRoot":"w","managedArtifactRoot":"a","evidenceRetention":"0s","processOutputLimit":1,"providerExecutablePath":"p","providerDefaultModel":"m","providerCredentialRef":"r"}`

	_, err := safesettings.UpdateSafeSettings(context.Background(), uow, idsource.Random{}, cmd,
		safesettings.UpdateSafeSettingsRequest{DesiredJSON: json.RawMessage(payload)})
	if err == nil {
		t.Fatal("UpdateSafeSettings(evidenceRetention=0s) = nil error, want validation rejection")
	}

	current, getErr := safesettings.GetSafeSettings(context.Background(), uow)
	if getErr != nil {
		t.Fatalf("GetSafeSettings: %v", getErr)
	}
	if current.Version != 1 {
		t.Fatalf("current.Version = %d after rejected update, want unchanged 1", current.Version)
	}
}

// TestUpdateSafeSettings_RootOverlapRejected is this task's own "root
// overlap" Verify scenario at the command layer.
func TestUpdateSafeSettings_RootOverlapRejected(t *testing.T) {
	uow := sqlite.NewUnitOfWork(openSQLiteStore(t, "update-root-overlap.db"))
	cmd := installationCommand("idem-overlap", "hash-overlap", 1)
	payload := `{"managedWorkspaceRoot":"data/managed","managedArtifactRoot":"data/managed/artifacts","evidenceRetention":"1h","processOutputLimit":1,"providerExecutablePath":"p","providerDefaultModel":"m","providerCredentialRef":"r"}`

	_, err := safesettings.UpdateSafeSettings(context.Background(), uow, idsource.Random{}, cmd,
		safesettings.UpdateSafeSettingsRequest{DesiredJSON: json.RawMessage(payload)})
	if err == nil {
		t.Fatal("UpdateSafeSettings(overlapping roots) = nil error, want validation rejection")
	}
}

// TestUpdateSafeSettings_TraversalRejected is this task's own "traversal"
// Verify scenario at the command layer.
func TestUpdateSafeSettings_TraversalRejected(t *testing.T) {
	uow := sqlite.NewUnitOfWork(openSQLiteStore(t, "update-traversal.db"))
	cmd := installationCommand("idem-traversal", "hash-traversal", 1)
	payload := `{"managedWorkspaceRoot":"data/../../etc","managedArtifactRoot":"a","evidenceRetention":"1h","processOutputLimit":1,"providerExecutablePath":"p","providerDefaultModel":"m","providerCredentialRef":"r"}`

	_, err := safesettings.UpdateSafeSettings(context.Background(), uow, idsource.Random{}, cmd,
		safesettings.UpdateSafeSettingsRequest{DesiredJSON: json.RawMessage(payload)})
	if err == nil {
		t.Fatal("UpdateSafeSettings(traversal) = nil error, want validation rejection")
	}
}
