package safesettings

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/domain/safesettings"
)

// This file is V6-10G's own "startup merge": the precedence resolution
// that turns a persisted SafeSettingsRecord into what the NEXT restart
// would actually run with — "defaults < config file < SQLite safe settings
// < environment < flags" (V6-10G's own Thực hiện line), with per-field
// masking reported explicitly when environment/flags override what SQLite
// desired ("env/flag masking is returned explicitly").
//
// This is deliberately its own, independent precedence chain — it does NOT
// extend internal/app/config.Config/Overrides/Load, and it never touches
// that package's own source files. Two of the seven allowlisted fields
// (ManagedArtifactRoot, ProcessOutputLimit) already have a real Config
// concept (ArtifactRoot, ProcessOutputLimit); the other five
// (ManagedWorkspaceRoot, EvidenceRetention, the three provider fields) have
// no Config concept at all today. Config stays the immutable-per-process
// startup config V1-03 already built and ships unmodified by this task
// (V6-10G's own Không làm: "no ... live mutation of immutable process
// config" — read literally, as "this task must never risk that already-
// shipped, heavily depended-on package", not merely "the running process's
// resolved value must not change", which a brand new, additive type could
// never violate on its own). A composition root that wants this resolution
// wired into a real `aw serve`/`aw worker` startup sequence calls
// ResolveEffective itself, in the exact order this package's own tests
// exercise: open the database using ONLY the pre-existing
// defaults/file/env/flags precedence for DatabasePath (config.Load's own
// existing contract, untouched), THEN read the persisted SafeSettingsRecord
// (GetSafeSettings), THEN call ResolveEffective with that record plus
// whatever file/env/flag StartupOverrides it has for these 7 fields.

// FieldSource names which precedence layer ultimately won for one field.
type FieldSource string

const (
	SourceDefault FieldSource = "default"
	SourceFile    FieldSource = "file"
	SourceSQLite  FieldSource = "sqlite"
	SourceEnv     FieldSource = "environment"
	SourceFlag    FieldSource = "flag"
)

// StartupOverrides mirrors internal/app/config.Overrides' own shape — a
// nil field means "this layer did not set it" — but scoped to exactly the
// 7 safe-settings-allowlisted fields, one layer (file, environment, or
// flags) at a time. There is no "defaults" instance of this type: Defaults()
// below returns a plain safesettings.SafeSettings directly, since the
// default layer never has an "unset" field the way an optional override
// layer does.
type StartupOverrides struct {
	ManagedWorkspaceRoot   *string
	ManagedArtifactRoot    *string
	EvidenceRetention      *time.Duration
	ProcessOutputLimit     *int
	ProviderExecutablePath *string
	ProviderDefaultModel   *string
	ProviderCredentialRef  *string
}

// Defaults returns Alpha's own safe, always-valid-shape starting point for
// every field this package's own precedence chain resolves — the "defaults"
// layer, always present, never itself masked. ManagedArtifactRoot and
// ProcessOutputLimit reuse internal/app/config.Defaults()'s own values
// exactly (the same concept as Config.ArtifactRoot/Config.ProcessOutputLimit,
// just resolved through this SECOND, SQLite-overridable layer) rather than
// duplicating those literals — a future change to Config's own defaults is
// picked up here automatically. EvidenceRetention's own default (7 days) is
// ADR-017's own "TTL 7 ngày áp cho evidence payload/raw provider output mặc
// định" read directly. The remaining fields (ManagedWorkspaceRoot and the
// three provider fields) have no pre-existing Config concept to borrow from,
// so they default to Alpha's own safe, unconfigured starting point — an
// empty provider configuration is already how Config.ProviderExecutables'
// own zero value (an empty map) behaves today, so this is not a new kind of
// "unconfigured" state, only the same one restated per-field. This
// deliberately does NOT return a safesettings.SafeSettings that Validate
// would accept as-is (an empty ManagedWorkspaceRoot or provider field is
// blocked by internal/domain/safesettings.Validate) — the defaults layer is
// a resolution FLOOR an operator is expected to configure over via file/
// SQLite/env/flags, exactly like Config's own WorkerID default ("" on
// purpose, so Validate catches a missing one) already established.
func Defaults() safesettings.SafeSettings {
	appDefaults := config.Defaults()
	return safesettings.SafeSettings{
		ManagedWorkspaceRoot: "",
		ManagedArtifactRoot:  appDefaults.ArtifactRoot,
		EvidenceRetention:    7 * 24 * time.Hour,
		ProcessOutputLimit:   appDefaults.ProcessOutputLimit,
	}
}

// StringFieldEffective is one string-typed field's own desired/effective/
// source/masking picture.
type StringFieldEffective struct {
	// Desired is the SQLite-persisted value for this field specifically —
	// the empty string if the safe settings document has never been
	// configured at all (SafeSettingsRecord.Desired.IsZero()).
	Desired string
	// Effective is what the NEXT restart would actually resolve this field
	// to, once every layer is applied.
	Effective string
	// Source names which layer produced Effective.
	Source FieldSource
	// MaskedByStartupSource is non-empty exactly when SQLite has ever been
	// configured (the desired document is not the zero value) AND Source is
	// SourceEnv or SourceFlag — i.e. the operator's own SQLite-persisted
	// desired value for this field is NOT what is actually effective right
	// now, because a higher-precedence startup source overrides it. Empty
	// otherwise (including when SQLite has never been configured at all:
	// there is nothing to mask in that case, only an ordinary default/file
	// value winning).
	MaskedByStartupSource FieldSource
}

// DurationFieldEffective mirrors StringFieldEffective for EvidenceRetention.
type DurationFieldEffective struct {
	Desired               time.Duration
	Effective             time.Duration
	Source                FieldSource
	MaskedByStartupSource FieldSource
}

// IntFieldEffective mirrors StringFieldEffective for ProcessOutputLimit.
type IntFieldEffective struct {
	Desired               int
	Effective             int
	Source                FieldSource
	MaskedByStartupSource FieldSource
}

// Effective is GetSafeSettings' own richer sibling picture — V6-10G's own
// "Response has desired/effective/version/restartRequired/
// maskedByStartupSource" line, resolved per field.
type Effective struct {
	ManagedWorkspaceRoot   StringFieldEffective
	ManagedArtifactRoot    StringFieldEffective
	EvidenceRetention      DurationFieldEffective
	ProcessOutputLimit     IntFieldEffective
	ProviderExecutablePath StringFieldEffective
	ProviderDefaultModel   StringFieldEffective
	ProviderCredentialRef  StringFieldEffective
}

// ResolveEffective applies "defaults < file < SQLite safe settings <
// environment < flags" independently for each of the 7 allowlisted fields
// and reports, per field, which source won and whether environment/flags
// are masking a real SQLite-persisted value. sqlite is the CURRENT
// SafeSettingsRecord (from GetSafeSettings); when its own Desired is the
// zero/never-configured document, the SQLite layer contributes NOTHING to
// any field (there is no such thing as SQLite partially configuring one
// field while leaving the rest at the file layer — V6-10G's own "Store full
// desired document": either every field was validated and persisted
// together, or none was).
func ResolveEffective(defaults safesettings.SafeSettings, file StartupOverrides, sqlite safesettings.SafeSettings, env, flags StartupOverrides) Effective {
	var sqliteLayer StartupOverrides
	if !sqlite.IsZero() {
		sqliteLayer = StartupOverrides{
			ManagedWorkspaceRoot: &sqlite.ManagedWorkspaceRoot, ManagedArtifactRoot: &sqlite.ManagedArtifactRoot,
			EvidenceRetention: &sqlite.EvidenceRetention, ProcessOutputLimit: &sqlite.ProcessOutputLimit,
			ProviderExecutablePath: &sqlite.ProviderExecutablePath, ProviderDefaultModel: &sqlite.ProviderDefaultModel,
			ProviderCredentialRef: &sqlite.ProviderCredentialRef,
		}
	}

	return Effective{
		ManagedWorkspaceRoot: resolveString(
			defaults.ManagedWorkspaceRoot, file.ManagedWorkspaceRoot, sqliteLayer.ManagedWorkspaceRoot,
			env.ManagedWorkspaceRoot, flags.ManagedWorkspaceRoot,
		),
		ManagedArtifactRoot: resolveString(
			defaults.ManagedArtifactRoot, file.ManagedArtifactRoot, sqliteLayer.ManagedArtifactRoot,
			env.ManagedArtifactRoot, flags.ManagedArtifactRoot,
		),
		EvidenceRetention: resolveDuration(
			defaults.EvidenceRetention, file.EvidenceRetention, sqliteLayer.EvidenceRetention,
			env.EvidenceRetention, flags.EvidenceRetention,
		),
		ProcessOutputLimit: resolveInt(
			defaults.ProcessOutputLimit, file.ProcessOutputLimit, sqliteLayer.ProcessOutputLimit,
			env.ProcessOutputLimit, flags.ProcessOutputLimit,
		),
		ProviderExecutablePath: resolveString(
			defaults.ProviderExecutablePath, file.ProviderExecutablePath, sqliteLayer.ProviderExecutablePath,
			env.ProviderExecutablePath, flags.ProviderExecutablePath,
		),
		ProviderDefaultModel: resolveString(
			defaults.ProviderDefaultModel, file.ProviderDefaultModel, sqliteLayer.ProviderDefaultModel,
			env.ProviderDefaultModel, flags.ProviderDefaultModel,
		),
		ProviderCredentialRef: resolveString(
			defaults.ProviderCredentialRef, file.ProviderCredentialRef, sqliteLayer.ProviderCredentialRef,
			env.ProviderCredentialRef, flags.ProviderCredentialRef,
		),
	}
}

func maskedSource(source FieldSource, sqliteSet bool) FieldSource {
	if sqliteSet && (source == SourceEnv || source == SourceFlag) {
		return source
	}
	return ""
}

func resolveString(defaultValue string, file, sqlite, env, flags *string) StringFieldEffective {
	effective, source := defaultValue, SourceDefault
	var desired string
	var sqliteSet bool
	if file != nil {
		effective, source = *file, SourceFile
	}
	if sqlite != nil {
		effective, source = *sqlite, SourceSQLite
		desired = *sqlite
		sqliteSet = true
	}
	if env != nil {
		effective, source = *env, SourceEnv
	}
	if flags != nil {
		effective, source = *flags, SourceFlag
	}
	return StringFieldEffective{
		Desired: desired, Effective: effective, Source: source,
		MaskedByStartupSource: maskedSource(source, sqliteSet),
	}
}

func resolveDuration(defaultValue time.Duration, file, sqlite, env, flags *time.Duration) DurationFieldEffective {
	effective, source := defaultValue, SourceDefault
	var desired time.Duration
	var sqliteSet bool
	if file != nil {
		effective, source = *file, SourceFile
	}
	if sqlite != nil {
		effective, source = *sqlite, SourceSQLite
		desired = *sqlite
		sqliteSet = true
	}
	if env != nil {
		effective, source = *env, SourceEnv
	}
	if flags != nil {
		effective, source = *flags, SourceFlag
	}
	return DurationFieldEffective{
		Desired: desired, Effective: effective, Source: source,
		MaskedByStartupSource: maskedSource(source, sqliteSet),
	}
}

func resolveInt(defaultValue int, file, sqlite, env, flags *int) IntFieldEffective {
	effective, source := defaultValue, SourceDefault
	var desired int
	var sqliteSet bool
	if file != nil {
		effective, source = *file, SourceFile
	}
	if sqlite != nil {
		effective, source = *sqlite, SourceSQLite
		desired = *sqlite
		sqliteSet = true
	}
	if env != nil {
		effective, source = *env, SourceEnv
	}
	if flags != nil {
		effective, source = *flags, SourceFlag
	}
	return IntFieldEffective{
		Desired: desired, Effective: effective, Source: source,
		MaskedByStartupSource: maskedSource(source, sqliteSet),
	}
}
