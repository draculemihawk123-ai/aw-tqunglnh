// Package doctor is V6-15C's own CLI leaf over V6-10A's own installation
// diagnostic (docs/design/08-v6-api-projections.md V6-15C: "operate ...
// Doctor ... from the terminal with the same installation authority as
// HTTP"): `aw doctor` — the terminal-side mirror of GET /doctor
// (internal/delivery/httpapi/doctor). It composes internal/app/doctor.Run
// with the same two extra facets GET /doctor's own handleDoctor adds
// (docs/design/08-v6-api-projections.md V6-10A) — an isolation-enforcement
// check and the safe-settings restartRequired bool — rather than exposing
// appdoctor.Run's own narrower Report directly, because the design doc's
// own V6-15C line asks for "version, desired/effective/masking/restart
// output" parity with HTTP, and internal/app/doctor.Run alone has no
// isolation check or restartRequired field at all (see that package's own
// doc comment). internal/delivery/httpapi/doctor/queries.go's own
// composition (isolationCheck/restartRequired/operationLinks) is
// unexported, so this file restates the identical logic rather than
// importing it — "mirror the shape", not literally reuse an unexported
// function.
//
// This package never wires itself into cmd/aw (V6-15O's own job — "chỉ
// V6-15O compose CLI/parity registry", docs/design/08-v6-api-projections.md
// §1 rule 8): it only registers its own cli.Descriptor into cli.Default
// from its own init(), and exposes RunDoctor for a future composition root
// to call once that wiring exists.
package doctor

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	appdoctor "github.com/taQuangLing/agent-workflow/internal/app/doctor"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"doctor"}, Scope: cli.ScopeInstallation,
		AppOperation: "Doctor", HTTPOperationID: "doctor",
	})
}

// Dependencies is everything `aw doctor` needs from a composition root —
// mirrors internal/delivery/httpapi/doctor.Dependencies field-for-field
// (that package's own doc comment explains why WorkerConfig/CheckWorker
// are omitted: no composition root that builds this leaf runs the
// lease/reaper worker pool itself).
type Dependencies struct {
	// Config is this process' own resolved, real internal/app/config.Config
	// — the same value used to open the database/artifact root in the
	// first place, never a second, re-derived copy.
	Config config.Config
	// Store is the real ports.QueryStore the composition root already
	// opened — CheckDatabase pings it, never opens a second connection.
	Store ports.QueryStore
	// UnitOfWork is the real ports.UnitOfWork the composition root already
	// opened — used for appdoctor.Options.UnitOfWork (CheckSafeSettings)
	// AND, separately, this package's own restartRequired lookup, exactly
	// like httpapi/doctor.Dependencies' own UnitOfWork doc comment.
	UnitOfWork ports.UnitOfWork
	// Isolation is the real ports.IsolationEnforcementChecker (ADR-023) the
	// composition root already built — reused here verbatim for
	// isolationCheck below, never a second, differently-configured
	// instance.
	Isolation ports.IsolationEnforcementChecker
}

// CheckResult mirrors appdoctor.CheckResult's own wire shape — restated as
// its own type (rather than reusing appdoctor.CheckResult directly) purely
// to match internal/delivery/httpapi/doctor/dto.go's own identical
// "every route/leaf defines its own wire DTO" convention.
type CheckResult struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Status      string `json:"status"`
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

func checkResultFrom(c appdoctor.CheckResult) CheckResult {
	return CheckResult{
		Name: c.Name, Category: string(c.Category), Status: string(c.Status),
		Detail: c.Detail, Remediation: c.Remediation,
	}
}

// Report is `aw doctor`'s own output shape — mirrors GET /doctor's own
// responseDTO (internal/delivery/httpapi/doctor/dto.go) field-for-field,
// including Links: a first-run operator following this report by hand
// sees the exact same fixed operation-name-to-installation-scoped-path map
// a first-run UI would, per that package's own doc comment for why this is
// a link, never embedded registry/query data restated a second time.
type Report struct {
	Status          string            `json:"status"`
	Checks          []CheckResult     `json:"checks"`
	RestartRequired bool              `json:"restartRequired"`
	Links           map[string]string `json:"links"`
}

// BuildReport composes internal/app/doctor.Run with the same two extra
// facets GET /doctor's own handleDoctor adds (isolation check,
// restartRequired) — see this file's own top-of-file doc comment for why
// that unexported HTTP-side composition is restated here rather than
// imported.
func BuildReport(ctx context.Context, deps Dependencies) Report {
	report := appdoctor.Run(ctx, appdoctor.Options{Config: deps.Config, Store: deps.Store, UnitOfWork: deps.UnitOfWork})
	checks := append([]appdoctor.CheckResult{}, report.Checks...)
	checks = append(checks, isolationCheck(ctx, deps.Isolation))

	wire := make([]CheckResult, len(checks))
	for i, c := range checks {
		wire[i] = checkResultFrom(c)
	}
	return Report{
		Status:          string(aggregateStatus(checks)),
		Checks:          wire,
		RestartRequired: restartRequired(ctx, deps.UnitOfWork),
		Links:           operationLinks(),
	}
}

// aggregateStatus mirrors internal/app/doctor's own unexported aggregate
// function byte-for-byte (worst-of-three: BLOCKED > DEGRADED > HEALTHY),
// re-run here over the COMBINED checks slice (appdoctor.Run's own Checks
// plus isolationCheck's own extra entry) — the exact same duplication
// internal/delivery/httpapi/doctor/queries.go's own aggregateStatus
// carries, and for the identical reason (see that function's own doc
// comment).
func aggregateStatus(checks []appdoctor.CheckResult) appdoctor.Status {
	status := appdoctor.StatusHealthy
	for _, c := range checks {
		switch c.Status {
		case appdoctor.StatusBlocked:
			return appdoctor.StatusBlocked
		case appdoctor.StatusDegraded:
			status = appdoctor.StatusDegraded
		}
	}
	return status
}

// isolationCheck mirrors internal/delivery/httpapi/doctor/queries.go's own
// unexported isolationCheck byte-for-byte: what isolation tier(s) this
// environment can actually enforce right now, a facet
// internal/app/doctor.Run itself has no check for. A nil Isolation
// (a composition-root wiring gap this task's own tests exercise directly,
// unlike the HTTP package which always has one non-nil real checker
// wired) reports BLOCKED rather than panicking.
func isolationCheck(ctx context.Context, checker ports.IsolationEnforcementChecker) appdoctor.CheckResult {
	const name = "isolation_enforcement"
	if checker == nil {
		return appdoctor.CheckResult{
			Name: name, Category: appdoctor.CategoryCapability, Status: appdoctor.StatusBlocked,
			Detail:      "no isolation enforcement checker configured for this invocation",
			Remediation: "this is a composition-root wiring gap; supply a real ports.IsolationEnforcementChecker",
		}
	}
	if err := checker.VerifyEnforceable(ctx, policy.IsolationTierOperatorTrustedLocal); err != nil {
		return appdoctor.CheckResult{
			Name: name, Category: appdoctor.CategoryCapability, Status: appdoctor.StatusBlocked,
			Detail:      "this environment cannot enforce OPERATOR_TRUSTED_LOCAL, the only isolation tier Alpha ever schedules a node under",
			Remediation: "this is an unexpected environment fault; no documented configuration in this codebase produces it today",
		}
	}
	if err := checker.VerifyEnforceable(ctx, policy.IsolationTierEnforcedIsolated); err != nil {
		return appdoctor.CheckResult{
			Name: name, Category: appdoctor.CategoryCapability, Status: appdoctor.StatusHealthy,
			Detail: "OPERATOR_TRUSTED_LOCAL isolation is enforceable; ENFORCED_ISOLATED is not available in this environment (no real OS-level sandbox yet) and any node pinned to it fails closed rather than silently downgrading",
		}
	}
	return appdoctor.CheckResult{
		Name: name, Category: appdoctor.CategoryCapability, Status: appdoctor.StatusHealthy,
		Detail: "both OPERATOR_TRUSTED_LOCAL and ENFORCED_ISOLATED are enforceable in this environment",
	}
}

// restartRequired mirrors internal/delivery/httpapi/doctor/queries.go's
// own unexported restartRequired byte-for-byte: forwards
// internal/app/safesettings.GetSafeSettings' own already-computed
// RestartRequired bool verbatim, defaulting to false on a read failure
// (the "database"/"safe_settings" CheckResults already in the same report
// name the real underlying failure; this bool has nothing further, safe,
// to add on top of that) — never Desired, which carries
// ProviderCredentialRef.
func restartRequired(ctx context.Context, uow ports.UnitOfWork) bool {
	if uow == nil {
		return false
	}
	result, err := safesettingsapp.GetSafeSettings(ctx, uow)
	if err != nil {
		return false
	}
	return result.RestartRequired
}

// operationLinks mirrors internal/delivery/httpapi/doctor/dto.go's own
// unexported operationLinks byte-for-byte — the fixed set of
// installation-scoped HTTP routes GET /doctor's own response already
// names; restated identically here so an operator reading `aw doctor`
// output sees exactly the parity picture ADR-028's own eventual CLI/HTTP
// inventory (V6-15O) will later verify, rather than a second, drifted copy
// of this map.
func operationLinks() map[string]string {
	return map[string]string{
		"adapterBuilds": "/adapter-builds",
		"safeSettings":  "/settings/safe",
		"projects":      "/projects",
		"healthLive":    "/health/live",
		"healthReady":   "/health/ready",
	}
}

// RunDoctor implements `aw doctor`: parses its own (empty besides --json)
// flag set, builds a Report via BuildReport, and writes it to stdout as
// JSON (--json) or a human summary otherwise. Always returns nil,
// regardless of Report.Status — unlike `aw health ready` (a binary
// load-balancer-style gate that fails the command on "not ready"), Doctor
// is a rich diagnostic report whose severity lives entirely in the typed
// body, never in the command's own exit code (mirrors GET /doctor's own
// "always 200" contract exactly — internal/delivery/httpapi/doctor/
// queries.go's own handleDoctor doc comment).
func RunDoctor(ctx context.Context, deps Dependencies, arguments []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	jsonOutput := cli.BindJSONFlag(fs)
	if err := fs.Parse(arguments); err != nil {
		return cli.UsageError{Err: err}
	}
	report := BuildReport(ctx, deps)
	if *jsonOutput {
		return cli.EncodeQueryResult(stdout, report)
	}
	fmt.Fprintf(stdout, "status: %s\n", report.Status)
	fmt.Fprintf(stdout, "restartRequired: %t\n", report.RestartRequired)
	fmt.Fprintln(stdout, "checks:")
	for _, c := range report.Checks {
		fmt.Fprintf(stdout, "  - %s [%s] %s: %s\n", c.Name, c.Category, c.Status, c.Detail)
		if c.Remediation != "" {
			fmt.Fprintf(stdout, "      remediation: %s\n", c.Remediation)
		}
	}
	fmt.Fprintln(stdout, "links:")
	for name, path := range report.Links {
		fmt.Fprintf(stdout, "  %s: %s\n", name, path)
	}
	return nil
}
