// Package doctor is V6-10A's own HTTP slice
// (docs/design/08-v6-api-projections.md V6-10A: "installation-scoped query
// combines typed component status and operation links; no credentials") —
// one thin GET /doctor aggregation over internal/app/doctor's own pure
// Run/Options (V1-11, ADR-022), plus two things Run alone cannot answer
// from an HTTP composition root:
//
//   - "isolation" (this task's own Phạm vi line names DB/roots/Git/
//     provider/adapter/ISOLATION readiness explicitly, but internal/app/
//     doctor.Run has no isolation check at all): isolationCheck below
//     calls the real ports.IsolationEnforcementChecker (ADR-023) the SAME
//     composition root already builds for
//     internal/delivery/httpapi/recovery's own RetryBlockedActivation —
//     never a second, differently-configured instance. VerifyEnforceable
//     is I/O-free and side-effect-free (see that interface's own doc
//     comment), so calling it here on every request is exactly as cheap
//     and safe as internal/app/doctor's own CheckGit/CheckRoot probes.
//   - "restartRequired" (mirrors V6-10H's own safesettings.SafeSettingsResult
//     field): reuses internal/app/safesettings.GetSafeSettings' own already-
//     computed, already-tested RestartRequired bool verbatim — Doctor never
//     recomputes that semantic itself, and never echoes any of the 7
//     safe-settings fields (in particular never ProviderCredentialRef) to
//     get it, only the one bool.
//
// "adapter" (this task's own dependency on V6-10J, PR #49): read literally,
// the design doc's own wording is "typed component status AND OPERATION
// LINKS" — a link, not embedded registry data. This package deliberately
// never calls appadapterbuild.ListAdapterBuilds or re-derives any adapter-
// build drift/registration fact inline: V6-10J's own GET /adapter-builds
// already IS that typed status, one HTTP hop away, and ADR-022's own
// "Doctor must never claim registry admission" caution (see internal/app/
// doctor's own package doc) is best honored by never restating registry
// state a second time in a second response shape that could quietly drift
// from the first. Links below is exactly that: a fixed map of operation
// name to installation-scoped path, for every route this task's own "Không
// làm" line permits Doctor to point at without re-implementing (repository
// onboarding/history/retry-probe stay owned by V6-03A; safe settings by
// V6-10H; adapter builds by V6-10J) — a first-run UI follows a link rather
// than this endpoint ever hand-rolling a second copy of that data.
//
// No credential ever reaches this package's response, structurally, not by
// convention: internal/app/doctor.Options/Run never touches
// safesettings.SafeSettings.ProviderCredentialRef or any other secret field
// (CheckSafeSettings only proves the persisted document DECODES, never
// echoes it — see that check's own doc comment), config.Config carries no
// credential field at all (DatabasePath/ArtifactRoot/ProviderExecutables
// are filesystem paths, not secrets), and this package's own extra
// restartRequired lookup forwards exactly one bool, nothing else, from
// SafeSettingsResult. A filesystem path DOES legitimately appear where the
// underlying check's own Detail already documents it as "explicitly safe to
// show" (artifact_root's own configured path, in CheckRoot's Detail) — that
// is the operator-configured location Doctor exists to help fix, not a
// secret. DatabasePath itself is never echoed by any check in this
// package's response (proven by TestDoctor_NeverLeaksDatabasePathOrCredential_HTTP).
package doctor

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// Dependencies is everything this package's one handler needs from the
// composition root (cmd/aw/serve.go). Deliberately narrower than
// appdoctor.Options: WorkerConfig/CheckWorker are omitted because `aw
// serve` never runs the lease/reaper worker pool itself (the same "a
// caller that only runs serve, never worker, has no lease/reaper settings
// to check" default appdoctor.Options' own doc comment already documents)
// — a future composition root that DOES need that facet extends this
// struct then, rather than this task speculatively wiring a check no
// running binary can ever exercise today.
type Dependencies struct {
	// Config is this process' own resolved, real internal/app/config.Config
	// (built once at boot by the composition root, the SAME value used to
	// open the database/artifact root in the first place — never a second,
	// re-derived copy). CheckAppConfig/CheckRoot/CheckProviderExecutable all
	// read directly from this.
	Config config.Config
	// Store is the real ports.QueryStore the composition root already
	// opened — CheckDatabase pings it, never opens a second connection.
	Store ports.QueryStore
	// UnitOfWork is the real ports.UnitOfWork the composition root already
	// opened — used for CheckSafeSettings (via appdoctor.Options.UnitOfWork)
	// AND, separately, this package's own restartRequired lookup
	// (safesettings.GetSafeSettings). Same instance, never a second one.
	UnitOfWork ports.UnitOfWork
	// Isolation is the real ports.IsolationEnforcementChecker (ADR-023) the
	// composition root already built for
	// internal/delivery/httpapi/recovery — reused here verbatim for
	// isolationCheck below, never a second, differently-configured
	// instance.
	Isolation ports.IsolationEnforcementChecker
}

// RegisterRoutes registers this package's own one route onto routes — a
// composition root (cmd/aw/serve.go) calls this once, alongside every
// sibling endpoint task's own RegisterRoutes.
func RegisterRoutes(routes *httpapi.RouteRegistry, deps Dependencies) {
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/doctor", OperationID: "doctor",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: responseDTO{},
		Handler: handleDoctor(deps),
	})
}

// GET /doctor is a plain query, never a ports.Command-enveloped mutation
// (this task's own "Thực hiện: an installation-scoped QUERY" line) — unlike
// internal/delivery/httpapi/safesettings or /adapterbuild, this package has
// no command-type constants and no receipt/idempotency handling at all.
