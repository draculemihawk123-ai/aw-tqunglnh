package safesettings

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/safesettings"
)

func strPtr(s string) *string               { return &s }
func durPtr(d time.Duration) *time.Duration { return &d }
func intPtr(i int) *int                     { return &i }

// TestResolveEffective_DefaultsOnly proves that with no file/sqlite/env/
// flags layer set at all, every field resolves to Defaults() at
// SourceDefault, never masked.
func TestResolveEffective_DefaultsOnly(t *testing.T) {
	defaults := Defaults()
	effective := ResolveEffective(defaults, StartupOverrides{}, safesettings.SafeSettings{}, StartupOverrides{}, StartupOverrides{})

	if effective.ManagedArtifactRoot.Effective != defaults.ManagedArtifactRoot || effective.ManagedArtifactRoot.Source != SourceDefault {
		t.Fatalf("ManagedArtifactRoot = %+v, want default %q", effective.ManagedArtifactRoot, defaults.ManagedArtifactRoot)
	}
	if effective.EvidenceRetention.Effective != defaults.EvidenceRetention || effective.EvidenceRetention.Source != SourceDefault {
		t.Fatalf("EvidenceRetention = %+v, want default %s", effective.EvidenceRetention, defaults.EvidenceRetention)
	}
	if effective.ManagedWorkspaceRoot.MaskedByStartupSource != "" {
		t.Fatalf("ManagedWorkspaceRoot.MaskedByStartupSource = %q, want empty (nothing to mask with no SQLite configuration)", effective.ManagedWorkspaceRoot.MaskedByStartupSource)
	}
}

// TestResolveEffective_FileOverridesDefaults proves the file layer beats
// defaults when SQLite has never been configured.
func TestResolveEffective_FileOverridesDefaults(t *testing.T) {
	file := StartupOverrides{ManagedArtifactRoot: strPtr("from-file/artifacts")}
	effective := ResolveEffective(Defaults(), file, safesettings.SafeSettings{}, StartupOverrides{}, StartupOverrides{})
	if effective.ManagedArtifactRoot.Effective != "from-file/artifacts" || effective.ManagedArtifactRoot.Source != SourceFile {
		t.Fatalf("ManagedArtifactRoot = %+v, want file value at SourceFile", effective.ManagedArtifactRoot)
	}
}

// TestResolveEffective_SQLiteOverridesFile proves a real, non-zero SQLite
// desired document beats both defaults and file for every field together
// (V6-10G's own "Store full desired document": SQLite either configures
// every field or none).
func TestResolveEffective_SQLiteOverridesFile(t *testing.T) {
	file := StartupOverrides{ManagedArtifactRoot: strPtr("from-file/artifacts")}
	sqliteDesired := safesettings.SafeSettings{
		ManagedWorkspaceRoot: "data/workspaces", ManagedArtifactRoot: "data/artifacts",
		EvidenceRetention: 3 * 24 * time.Hour, ProcessOutputLimit: 2048,
		ProviderExecutablePath: "C:/tools/claude.exe", ProviderDefaultModel: "claude-sonnet-4-5",
		ProviderCredentialRef: "keychain:ref",
	}
	effective := ResolveEffective(Defaults(), file, sqliteDesired, StartupOverrides{}, StartupOverrides{})

	if effective.ManagedArtifactRoot.Effective != "data/artifacts" || effective.ManagedArtifactRoot.Source != SourceSQLite {
		t.Fatalf("ManagedArtifactRoot = %+v, want SQLite value at SourceSQLite", effective.ManagedArtifactRoot)
	}
	if effective.ManagedWorkspaceRoot.Effective != "data/workspaces" || effective.ManagedWorkspaceRoot.Source != SourceSQLite {
		t.Fatalf("ManagedWorkspaceRoot = %+v, want SQLite value at SourceSQLite", effective.ManagedWorkspaceRoot)
	}
	if effective.ManagedArtifactRoot.MaskedByStartupSource != "" {
		t.Fatalf("ManagedArtifactRoot.MaskedByStartupSource = %q, want empty (SQLite is winning, not masked)", effective.ManagedArtifactRoot.MaskedByStartupSource)
	}
}

// TestResolveEffective_EnvMasksSQLite is this task's own "env ... mask"
// Verify scenario: an environment override wins over a real SQLite desired
// value, and this is reported explicitly via MaskedByStartupSource.
func TestResolveEffective_EnvMasksSQLite(t *testing.T) {
	sqliteDesired := safesettings.SafeSettings{
		ManagedWorkspaceRoot: "data/workspaces", ManagedArtifactRoot: "data/artifacts",
		EvidenceRetention: 3 * 24 * time.Hour, ProcessOutputLimit: 2048,
		ProviderExecutablePath: "C:/tools/claude.exe", ProviderDefaultModel: "claude-sonnet-4-5",
		ProviderCredentialRef: "keychain:ref",
	}
	env := StartupOverrides{ManagedArtifactRoot: strPtr("from-env/artifacts")}
	effective := ResolveEffective(Defaults(), StartupOverrides{}, sqliteDesired, env, StartupOverrides{})

	if effective.ManagedArtifactRoot.Effective != "from-env/artifacts" || effective.ManagedArtifactRoot.Source != SourceEnv {
		t.Fatalf("ManagedArtifactRoot = %+v, want env value at SourceEnv", effective.ManagedArtifactRoot)
	}
	if effective.ManagedArtifactRoot.Desired != "data/artifacts" {
		t.Fatalf("ManagedArtifactRoot.Desired = %q, want the SQLite-persisted value preserved even while masked", effective.ManagedArtifactRoot.Desired)
	}
	if effective.ManagedArtifactRoot.MaskedByStartupSource != SourceEnv {
		t.Fatalf("ManagedArtifactRoot.MaskedByStartupSource = %q, want %q", effective.ManagedArtifactRoot.MaskedByStartupSource, SourceEnv)
	}
	// A field env did NOT touch must still resolve to SQLite, unmasked.
	if effective.ManagedWorkspaceRoot.Source != SourceSQLite || effective.ManagedWorkspaceRoot.MaskedByStartupSource != "" {
		t.Fatalf("ManagedWorkspaceRoot = %+v, want SQLite-sourced and unmasked", effective.ManagedWorkspaceRoot)
	}
}

// TestResolveEffective_FlagMasksEnvAndSQLite is this task's own "flag ...
// mask" Verify scenario: flags outrank both env and SQLite.
func TestResolveEffective_FlagMasksEnvAndSQLite(t *testing.T) {
	sqliteDesired := safesettings.SafeSettings{
		ManagedWorkspaceRoot: "data/workspaces", ManagedArtifactRoot: "data/artifacts",
		EvidenceRetention: 3 * 24 * time.Hour, ProcessOutputLimit: 2048,
		ProviderExecutablePath: "C:/tools/claude.exe", ProviderDefaultModel: "claude-sonnet-4-5",
		ProviderCredentialRef: "keychain:ref",
	}
	env := StartupOverrides{ProcessOutputLimit: intPtr(4096)}
	flags := StartupOverrides{ProcessOutputLimit: intPtr(8192)}
	effective := ResolveEffective(Defaults(), StartupOverrides{}, sqliteDesired, env, flags)

	if effective.ProcessOutputLimit.Effective != 8192 || effective.ProcessOutputLimit.Source != SourceFlag {
		t.Fatalf("ProcessOutputLimit = %+v, want flag value 8192 at SourceFlag", effective.ProcessOutputLimit)
	}
	if effective.ProcessOutputLimit.Desired != 2048 {
		t.Fatalf("ProcessOutputLimit.Desired = %d, want the SQLite-persisted value 2048 preserved", effective.ProcessOutputLimit.Desired)
	}
	if effective.ProcessOutputLimit.MaskedByStartupSource != SourceFlag {
		t.Fatalf("ProcessOutputLimit.MaskedByStartupSource = %q, want %q", effective.ProcessOutputLimit.MaskedByStartupSource, SourceFlag)
	}
}

// TestResolveEffective_DurationField exercises the Duration-typed
// resolver's own masking path directly (EvidenceRetention), since every
// other test above only exercises string/int fields.
func TestResolveEffective_DurationField(t *testing.T) {
	sqliteDesired := safesettings.SafeSettings{
		ManagedWorkspaceRoot: "w", ManagedArtifactRoot: "a", EvidenceRetention: 3 * 24 * time.Hour,
		ProcessOutputLimit: 1, ProviderExecutablePath: "p", ProviderDefaultModel: "m", ProviderCredentialRef: "r",
	}
	flags := StartupOverrides{EvidenceRetention: durPtr(24 * time.Hour)}
	effective := ResolveEffective(Defaults(), StartupOverrides{}, sqliteDesired, StartupOverrides{}, flags)
	if effective.EvidenceRetention.Effective != 24*time.Hour || effective.EvidenceRetention.Source != SourceFlag {
		t.Fatalf("EvidenceRetention = %+v, want flag value 24h", effective.EvidenceRetention)
	}
	if effective.EvidenceRetention.MaskedByStartupSource != SourceFlag {
		t.Fatalf("EvidenceRetention.MaskedByStartupSource = %q, want %q", effective.EvidenceRetention.MaskedByStartupSource, SourceFlag)
	}
}

// TestStartupSequencing_CurrentProcessUnchangedThenRestartAppliesUnmasked
// is this task's own "current process unchanged; restart applies unmasked
// value" Verify scenario, exercised end to end against a real SQLite
// database — mirroring internal/integration/foundation_test.go's own
// "resolve -> mutate -> resolve again" restart-contract shape, scoped to
// this task's own safe-settings precedence instead of Doctor/worker
// recovery.
//
// Sequencing mirrors V6-10G's own "Startup resolves DatabasePath first,
// opens DB, then applies allowed precedence" contract: the database is
// already open (sqlite.Open, below) before either resolution happens.
func TestStartupSequencing_CurrentProcessUnchangedThenRestartAppliesUnmasked(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "startup-sequencing.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	// --- "current process" resolves once at its own startup ---
	firstRecord, err := GetSafeSettings(context.Background(), uow)
	if err != nil {
		t.Fatalf("GetSafeSettings (first): %v", err)
	}
	currentProcessEffective := ResolveEffective(Defaults(), StartupOverrides{}, firstRecord.Desired, StartupOverrides{}, StartupOverrides{})
	if currentProcessEffective.ManagedArtifactRoot.Source != SourceDefault {
		t.Fatalf("currentProcessEffective.ManagedArtifactRoot.Source = %s, want default (nothing configured yet)", currentProcessEffective.ManagedArtifactRoot.Source)
	}
	snapshot := currentProcessEffective // explicit copy, proving what "the running process already resolved" looked like

	// --- an operator updates safe settings WHILE this "process" keeps running ---
	cmd := ports.Command{
		ID: "cmd-1", IdempotencyKey: "idem-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.InstallationScope(), ExpectedVersion: firstRecord.Version, RequestedAt: time.Now().UTC(),
		Type: "UpdateSafeSettings", RequestHash: "hash-1",
	}
	desiredJSON, err := json.Marshal(safesettings.SafeSettings{
		ManagedWorkspaceRoot: "data/workspaces", ManagedArtifactRoot: "data/artifacts",
		EvidenceRetention: 24 * time.Hour, ProcessOutputLimit: 4096,
		ProviderExecutablePath: "C:/tools/claude.exe", ProviderDefaultModel: "claude-sonnet-4-5",
		ProviderCredentialRef: "keychain:ref",
	})
	if err != nil {
		t.Fatalf("marshal desired: %v", err)
	}
	if _, err := UpdateSafeSettings(context.Background(), uow, idsource.Random{}, cmd, UpdateSafeSettingsRequest{DesiredJSON: desiredJSON}); err != nil {
		t.Fatalf("UpdateSafeSettings: %v", err)
	}

	// --- the value the "current process" already resolved must be provably
	// unchanged: Go's own value semantics already guarantee this (Effective
	// is a plain struct, never a pointer into shared state), asserted here
	// explicitly rather than merely assumed.
	if snapshot != currentProcessEffective {
		t.Fatalf("currentProcessEffective mutated after UpdateSafeSettings: got %+v, want unchanged %+v", currentProcessEffective, snapshot)
	}
	if currentProcessEffective.ManagedArtifactRoot.Effective != Defaults().ManagedArtifactRoot {
		t.Fatalf("currentProcessEffective.ManagedArtifactRoot.Effective = %q after an update this process never re-resolved, want the original default (no hot reload)",
			currentProcessEffective.ManagedArtifactRoot.Effective)
	}

	// --- "restart": open a fresh resolution against the SAME database ---
	restartRecord, err := GetSafeSettings(context.Background(), uow)
	if err != nil {
		t.Fatalf("GetSafeSettings (restart): %v", err)
	}
	restartEffective := ResolveEffective(Defaults(), StartupOverrides{}, restartRecord.Desired, StartupOverrides{}, StartupOverrides{})
	if restartEffective.ManagedArtifactRoot.Effective != "data/artifacts" || restartEffective.ManagedArtifactRoot.Source != SourceSQLite {
		t.Fatalf("restartEffective.ManagedArtifactRoot = %+v, want the newly-persisted SQLite value, unmasked", restartEffective.ManagedArtifactRoot)
	}
	if restartEffective.ManagedArtifactRoot.MaskedByStartupSource != "" {
		t.Fatalf("restartEffective.ManagedArtifactRoot.MaskedByStartupSource = %q, want empty (no env/flag override at restart)", restartEffective.ManagedArtifactRoot.MaskedByStartupSource)
	}

	// --- a variant restart WITH an env override in effect: the newly
	// persisted value is masked, reported explicitly.
	restartWithEnv := ResolveEffective(Defaults(), StartupOverrides{}, restartRecord.Desired,
		StartupOverrides{ManagedArtifactRoot: strPtr("from-env/artifacts")}, StartupOverrides{})
	if restartWithEnv.ManagedArtifactRoot.Effective != "from-env/artifacts" || restartWithEnv.ManagedArtifactRoot.Source != SourceEnv {
		t.Fatalf("restartWithEnv.ManagedArtifactRoot = %+v, want the env value at SourceEnv", restartWithEnv.ManagedArtifactRoot)
	}
	if restartWithEnv.ManagedArtifactRoot.MaskedByStartupSource != SourceEnv {
		t.Fatalf("restartWithEnv.ManagedArtifactRoot.MaskedByStartupSource = %q, want %q", restartWithEnv.ManagedArtifactRoot.MaskedByStartupSource, SourceEnv)
	}
}
