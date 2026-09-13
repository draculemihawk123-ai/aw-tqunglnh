// Package safesettings is V6-10G's own closed, versioned "desired
// document" (docs/design/08-v6-api-projections.md V6-10G, ADR-016,
// ADR-017, ADR-025, ADR-028): a small, hand-picked allowlist of Alpha
// startup settings an operator may change WHILE THE SERVER IS RUNNING —
// persisted in SQLite, versioned by optimistic-concurrency CAS — that only
// ever take effect on the NEXT process restart. This is a SECOND
// configuration layer that sits on top of, and never mutates,
// internal/app/config.Config: that package stays the immutable-per-process
// startup config (defaults < file < env < flags, V1-03); this package's
// own SafeSettings slots into that SAME precedence chain at one additional
// point — "defaults < config file < SQLite safe settings < environment <
// flags" — resolved fresh only at the next startup (internal/app/safesettings's
// own startup.go owns that resolution; this file owns only the persisted
// document's shape and validation).
//
// The allowlist is exactly these 7 fields, per V6-10G's own Thực hiện
// line — nothing else is ever safe-settings-mutable, and this is a closed
// set on purpose: DatabasePath, WorkerID, LocalPrincipal, any
// session/signing key and any raw secret value are all explicitly
// forbidden (V6-10G's own Không làm line) from ever appearing here, now or
// in a future field addition, without a new task explicitly re-opening
// this allowlist.
package safesettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// SafeSettings is the full desired document — V6-10G's own "Store full
// desired document + version": an update always replaces every field
// together (never a per-field patch), so there is no partial/mixed state
// to reason about between what SQLite persisted and what a caller last
// validated. The zero value (every field at its own zero value) is the
// legitimate "nothing has ever been configured" state the very first
// SQLite row is seeded with (see internal/adapters/sqlite/migrations/
// 0036_safe_settings.sql) — IsZero names that state explicitly rather than
// leaving callers to compare against SafeSettings{} by hand.
//
// ProviderCredentialRef is a REFERENCE id (e.g. an OS keychain entry name,
// a vault key, an env var name a provider adapter resolves at spawn time)
// — never the actual secret value. Nothing in this codebase resolves a
// credential reference to its real value inside this package or anywhere
// application logic can see it (V6-10G's own Không làm: "raw secret
// value" never appears here); ValidateCredentialRef below only checks the
// reference's own shape (a bounded, whitespace-free identifier), which is
// the one thing this layer can and must enforce — it cannot detect "is
// this actually a real secret" any more reliably than that.
type SafeSettings struct {
	ManagedWorkspaceRoot   string
	ManagedArtifactRoot    string
	EvidenceRetention      time.Duration
	ProcessOutputLimit     int
	ProviderExecutablePath string
	ProviderDefaultModel   string
	ProviderCredentialRef  string
}

// IsZero reports whether s is the "never configured" desired document —
// every field still at its own zero value, the state migration
// 0036_safe_settings.sql's own seed row starts in.
func (s SafeSettings) IsZero() bool { return s == SafeSettings{} }

// jsonSafeSettings is SafeSettings' own wire/storage shape: EvidenceRetention
// is a Go-syntax duration string ("168h0m0s"), not time.Duration's default
// opaque-nanosecond-integer JSON encoding — mirroring
// internal/app/config/sources.go's own rawFileConfig.LeaseTTL convention,
// for the identical reason (a human — an operator reading a persisted row
// or an event payload, or hand-editing a request body once V6-10H exists —
// should never have to do nanosecond arithmetic by hand).
type jsonSafeSettings struct {
	ManagedWorkspaceRoot   string `json:"managedWorkspaceRoot"`
	ManagedArtifactRoot    string `json:"managedArtifactRoot"`
	EvidenceRetention      string `json:"evidenceRetention"`
	ProcessOutputLimit     int    `json:"processOutputLimit"`
	ProviderExecutablePath string `json:"providerExecutablePath"`
	ProviderDefaultModel   string `json:"providerDefaultModel"`
	ProviderCredentialRef  string `json:"providerCredentialRef"`
}

// MarshalJSON implements json.Marshaler.
func (s SafeSettings) MarshalJSON() ([]byte, error) {
	return json.Marshal(jsonSafeSettings{
		ManagedWorkspaceRoot:   s.ManagedWorkspaceRoot,
		ManagedArtifactRoot:    s.ManagedArtifactRoot,
		EvidenceRetention:      s.EvidenceRetention.String(),
		ProcessOutputLimit:     s.ProcessOutputLimit,
		ProviderExecutablePath: s.ProviderExecutablePath,
		ProviderDefaultModel:   s.ProviderDefaultModel,
		ProviderCredentialRef:  s.ProviderCredentialRef,
	})
}

// UnmarshalJSON implements json.Unmarshaler with a strict decoder
// (DisallowUnknownFields): a request body naming any field outside this
// package's own closed 7-field allowlist — "databasePath", "workerId",
// "localPrincipal", a session/signing key, or simply a typo — is rejected
// here, at the one shared decode path every caller (the SQLite adapter
// reading back a persisted row, and internal/app/safesettings.UpdateSafeSettings
// decoding an incoming desired document) goes through. This is deliberately
// the SAME method used for both directions: a persisted row this package
// itself wrote is always clean and decodes trivially, so reusing the strict
// path there costs nothing and doubles as an extra corruption signal (a
// truncated/bit-rotted row is more likely to fail this decode) — exactly
// the failure ports.ErrSafeSettingsCorrupt exists to name.
func (s *SafeSettings) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var raw jsonSafeSettings
	if err := decoder.Decode(&raw); err != nil {
		return fmt.Errorf("safesettings: decode desired document: %w", err)
	}
	var retention time.Duration
	if raw.EvidenceRetention != "" {
		parsed, err := time.ParseDuration(raw.EvidenceRetention)
		if err != nil {
			return fmt.Errorf("safesettings: evidenceRetention: %w", err)
		}
		retention = parsed
	}
	*s = SafeSettings{
		ManagedWorkspaceRoot:   raw.ManagedWorkspaceRoot,
		ManagedArtifactRoot:    raw.ManagedArtifactRoot,
		EvidenceRetention:      retention,
		ProcessOutputLimit:     raw.ProcessOutputLimit,
		ProviderExecutablePath: raw.ProviderExecutablePath,
		ProviderDefaultModel:   raw.ProviderDefaultModel,
		ProviderCredentialRef:  raw.ProviderCredentialRef,
	}
	return nil
}

// Validate checks every field's own shape constraint plus the one
// cross-field invariant this allowlist has (workspace/artifact root
// overlap), returning the FIRST problem found — mirroring this codebase's
// domain-constructor convention (e.g. internal/domain/readiness.NewProfile,
// internal/domain/work.NewReleaseSet), not internal/app/config.Validate's
// own collect-every-problem discipline: that package is a whole-process
// startup gate where an operator wants every problem at once, while this
// is a single-document update a caller re-submits after fixing one field,
// the same shape every other CAS-guarded domain constructor in this
// codebase already uses.
func Validate(s SafeSettings) error {
	workspaceRoot, err := normalizeRoot(s.ManagedWorkspaceRoot)
	if err != nil {
		return fmt.Errorf("safesettings: managedWorkspaceRoot: %w", err)
	}
	artifactRoot, err := normalizeRoot(s.ManagedArtifactRoot)
	if err != nil {
		return fmt.Errorf("safesettings: managedArtifactRoot: %w", err)
	}
	if rootsOverlap(workspaceRoot, artifactRoot) {
		return fmt.Errorf(
			"safesettings: managedWorkspaceRoot %q and managedArtifactRoot %q must not contain each other",
			s.ManagedWorkspaceRoot, s.ManagedArtifactRoot,
		)
	}
	if s.EvidenceRetention <= 0 {
		return fmt.Errorf("safesettings: evidenceRetention must be a positive duration, got %s", s.EvidenceRetention)
	}
	if s.ProcessOutputLimit <= 0 {
		return fmt.Errorf("safesettings: processOutputLimit must be a positive byte count, got %d", s.ProcessOutputLimit)
	}
	if _, err := normalizeRoot(s.ProviderExecutablePath); err != nil {
		return fmt.Errorf("safesettings: providerExecutablePath: %w", err)
	}
	if err := validateModel(s.ProviderDefaultModel); err != nil {
		return fmt.Errorf("safesettings: providerDefaultModel: %w", err)
	}
	if err := validateCredentialRef(s.ProviderCredentialRef); err != nil {
		return fmt.Errorf("safesettings: providerCredentialRef: %w", err)
	}
	return nil
}

// normalizeRoot applies this codebase's established path-shape validation
// (see internal/domain/readiness's own normalizeRelativeDirectory doc
// comment for why every domain package keeps its own small copy rather
// than importing another domain package's helper): backslash normalized to
// forward slash, no ".." segment (traversal), cleaned via path.Clean.
// Unlike readiness.normalizeRelativeDirectory this package's own roots are
// legitimately either absolute or relative (an operator-configured
// filesystem root, the same shape config.Config.ArtifactRoot's own default
// "artifacts" already uses), so — unlike that sibling helper — an absolute
// path is never rejected here, only traversal. This is pure string
// validation: the domain layer has no filesystem access of its own
// (mirrors readiness.CommandSpec's own doc comment), so existence/
// writability is a later, adapter-level concern (internal/app/doctor's own
// CheckRoot precedent), never this package's job.
func normalizeRoot(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("must not be empty")
	}
	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return "", fmt.Errorf("must not contain parent traversal (%q): %q", "..", raw)
		}
	}
	normalized = cleanSlashPath(normalized)
	if normalized == "" {
		return "", fmt.Errorf("must not be empty after normalization: %q", raw)
	}
	return normalized, nil
}

// cleanSlashPath is a minimal, filesystem-free "collapse ./ and duplicate
// slashes" cleaner over an already forward-slash-normalized, traversal-free
// path — deliberately not path.Clean (whose own leading-".."-collapsing
// behavior only matters for a traversal path, which normalizeRoot's own
// caller has already rejected above; using it anyway would be reaching for
// a tool built for a check this function does not need).
func cleanSlashPath(normalized string) string {
	segments := strings.Split(normalized, "/")
	kept := make([]string, 0, len(segments))
	for i, segment := range segments {
		if segment == "" && i != 0 {
			continue // collapse duplicate/trailing slash, but keep a leading "" (absolute Unix path)
		}
		if segment == "." {
			continue
		}
		kept = append(kept, segment)
	}
	cleaned := strings.Join(kept, "/")
	if cleaned == "" && strings.HasPrefix(normalized, "/") {
		return "/"
	}
	return cleaned
}

// rootsOverlap reports whether a and b (both already normalizeRoot-cleaned)
// name the same path, or one is a strict subdirectory of the other —
// mirroring internal/adapters/gitworktree's own isWithin/ensureLexicallyWithin
// precedent ("source repository and workspace root must not contain each
// other"), reimplemented here as pure string comparison since a domain
// package has no filesystem access to resolve a real relative path with.
// Case-sensitive: a cross-platform case-insensitive comparison is a real
// filesystem's own concern (see gitworktree.samePath's own OS-conditional
// EqualFold), not something this pure-string domain check can or should
// decide.
func rootsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	if strings.HasPrefix(a, b+"/") {
		return true
	}
	if strings.HasPrefix(b, a+"/") {
		return true
	}
	return false
}

// validateModel checks ProviderDefaultModel's own shape: non-empty once
// trimmed, no control characters, and a bounded length — deliberately not
// an enum (this codebase never hardcodes a fixed provider/model list;
// internal/app/ports' own ProviderClaude/ProviderCodex identifiers name
// providers, never their models).
func validateModel(model string) error {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return errors.New("must not be empty")
	}
	if trimmed != model {
		return errors.New("must not have leading/trailing whitespace")
	}
	if len(model) > 128 {
		return fmt.Errorf("must be at most 128 characters, got %d", len(model))
	}
	for _, r := range model {
		if unicode.IsControl(r) {
			return fmt.Errorf("must not contain control character %q", r)
		}
	}
	return nil
}

// validateCredentialRef checks ProviderCredentialRef's own shape: a
// bounded, whitespace-free identifier built only from characters a real
// reference id (a keychain entry name, a vault key, an env var name) would
// plausibly use — never accepting whitespace or an unbounded length, which
// is the one shape-level signal this layer can use to keep a caller from
// accidentally pasting an actual secret blob into this field instead of a
// reference to one (see this file's own SafeSettings doc comment for why
// this package can never fully verify "is this a real secret").
func validateCredentialRef(ref string) error {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return errors.New("must not be empty")
	}
	if trimmed != ref {
		return errors.New("must not have leading/trailing whitespace")
	}
	if len(ref) > 256 {
		return fmt.Errorf("must be at most 256 characters (a reference id, never a raw secret value), got %d", len(ref))
	}
	for _, r := range ref {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			continue
		case strings.ContainsRune("-_.:/@", r):
			continue
		default:
			return fmt.Errorf("contains disallowed character %q — must look like a reference id (letters, digits, and -_.:/@ only), never a raw secret value", r)
		}
	}
	return nil
}
