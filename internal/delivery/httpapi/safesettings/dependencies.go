// Package safesettings is V6-10H's own HTTP slice
// (docs/design/08-v6-api-projections.md V6-10H: "expose GET/PUT
// /settings/safe over V6-10G"). It owns a dedicated subpackage under
// internal/delivery/httpapi — the same "one subpackage per endpoint task"
// discipline internal/delivery/httpapi/workitem's own doc comment already
// establishes — rather than adding flat files directly into
// internal/delivery/httpapi.
//
// Route inventory (both installation-scoped — ADR-025's own command-scope
// table names "safe-settings mutation"/"safe-settings read" as installation
// Command/Query, never project-scoped):
//
//	GET /settings/safe   getSafeSettings
//	PUT /settings/safe   updateSafeSettings
//
// Both handlers are thin transport over internal/app/safesettings'
// GetSafeSettings/UpdateSafeSettings — this package never touches a config
// file, an environment variable, a secret store or any setting outside
// that package's own closed 7-field allowlist (V6-10H's own "Không làm:
// handler no config/file/env/secret access and no extra setting"). The one
// piece of information this package needs that GetSafeSettings alone
// cannot produce — Effective, the per-field startup-precedence resolution
// (internal/app/safesettings/startup.go's own ResolveEffective) — is
// computed exactly ONCE by the composition root (cmd/aw/serve.go) at
// process boot, using the file/env/flag StartupOverrides that boot actually
// resolved, and handed to this package as a plain, read-only value via
// Dependencies.Effective: see that field's own doc comment for the full
// "why once, not per-request" reasoning, and baocaov6checklist.md's own
// V6-10H section for the wiring narrative.
package safesettings

import (
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
)

// Dependencies is everything RegisterRoutes' own handlers need — a
// composition root builds exactly one of these and passes it to
// RegisterRoutes, mirroring internal/delivery/httpapi/workitem.Dependencies'
// own "everything a handler needs travels through one explicit struct"
// shape.
type Dependencies struct {
	// UnitOfWork is the one real ports.UnitOfWork every handler in this
	// package dispatches through — this package never opens a second,
	// competing persistence path, and never writes a receipt of its own
	// (that stays internal/app/safesettings.UpdateSafeSettings' own
	// transaction).
	UnitOfWork ports.UnitOfWork
	// IDs mints the event ID UpdateSafeSettings' own transaction needs for
	// its SAFE_SETTINGS_UPDATED event append. A composition root supplies
	// idsource.Random{} in production; a test supplies idsource.Sequential
	// for deterministic assertions.
	IDs idsource.Source
	// Clock supplies cmd.RequestedAt for the one command envelope this
	// package ever builds (UpdateSafeSettings) — internal/app/clock's own
	// "no caller reaches for time.Now() directly" discipline.
	Clock clock.Clock
	// Matcher is the ONE shared, process-lifetime redactor this server
	// already builds (mirrors internal/delivery/httpapi/message.
	// Dependencies' own Matcher doc comment: "an HTTP caller never gets to
	// supply its own") — used here for exactly one purpose: masking
	// ProviderCredentialRef (a credential REFERENCE id — never the raw
	// secret value itself, see internal/domain/safesettings.SafeSettings'
	// own doc comment — but still never worth echoing back verbatim over
	// HTTP, this task's own "secret/redaction goldens" Verify line) via
	// Tagged(redact.Secret, ...) in every response this package ever
	// writes, GET, PUT, and a replayed PUT alike. This is a STRUCTURAL tag
	// (redact.Matcher.Tagged's own doc comment: "a caller that knows a
	// field's role... before any real secret value is known"), not a
	// content-based match against Matcher's own known-secrets set — an
	// empty redact.Matcher{} masks this field exactly as completely as one
	// seeded with real secrets.
	Matcher redact.Matcher
	// Effective is this RUNNING process' own startup-precedence resolution
	// (internal/app/safesettings/startup.go's own ResolveEffective) —
	// computed EXACTLY ONCE, by the composition root, at process boot, from
	// the file/env/flag StartupOverrides that boot actually had in scope,
	// combined with WHATEVER SafeSettingsRecord.Desired was persisted at
	// that same moment. It is deliberately never recomputed per-request:
	// Alpha has no hot-reload path at all (V6-10G's own "no live mutation
	// of immutable process config"), so a value captured once at boot
	// accurately represents what THIS running process is actually using
	// for its entire lifetime, whether or not GetSafeSettings' own live
	// Desired has since changed underneath it via a later PUT. That gap —
	// a live Desired that already differs from this frozen Effective — is
	// exactly what RestartRequired (SafeSettingsResult's own field,
	// forwarded unchanged in this package's response) tells a caller to
	// expect: "the next restart will use different settings than the ones
	// this response's own effective object describes right now." See
	// cmd/aw/serve.go's own composition-root comment for exactly how this
	// value is built, and baocaov6checklist.md's V6-10H section for why
	// file/env/flag StartupOverrides are all empty in `aw serve` today (no
	// prior task ever added flag/file/env parsing for these 7 fields).
	Effective safesettingsapp.Effective
}
