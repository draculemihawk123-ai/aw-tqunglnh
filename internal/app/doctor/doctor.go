// Package doctor is Alpha's health/diagnostic report (V1-11,
// docs/design/03-v1-alpha-foundation.md, ADR-022): it distinguishes
// three different questions, never conflates them into one bare pass/
// fail —
//
//   - Liveness: is this process running at all and able to respond?
//   - Readiness: can this instance actually do its job right now (DB
//     reachable and migrated, filesystem roots writable, Git available,
//     startup configuration valid, lease/reaper settings sane)?
//   - Capability: what optional capability did we OBSERVE (a configured
//     provider executable's fingerprint), as distinct from whether it is
//     admitted into a registry. ADR-022 is explicit that Doctor at V1
//     MUST NOT claim registry admission: the AdapterBuildVersion registry
//     itself does not exist until V2-07A, so every capability check here
//     is only ever "observed", never "registered".
//
// Every check that is not HEALTHY carries a Remediation string — Doctor
// exists to tell an operator what to do next, not just that something is
// wrong. No check ever mutates anything outside a temp file it creates
// and removes itself to probe writability, and no check ever echoes a
// raw OS/driver error string or environment dump into its output — only
// the specific, deliberately-chosen Detail/Remediation text this package
// writes.
package doctor

import (
	"context"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

// Status is one check's or the overall report's severity.
type Status string

const (
	StatusHealthy  Status = "HEALTHY"
	StatusDegraded Status = "DEGRADED"
	StatusBlocked  Status = "BLOCKED"
)

// Category is which of Doctor's three questions (see package doc) a
// CheckResult answers.
type Category string

const (
	CategoryLiveness   Category = "LIVENESS"
	CategoryReadiness  Category = "READINESS"
	CategoryCapability Category = "CAPABILITY"
)

// CheckResult is one diagnostic check's outcome.
type CheckResult struct {
	Name        string   `json:"name"`
	Category    Category `json:"category"`
	Status      Status   `json:"status"`
	Detail      string   `json:"detail,omitempty"`
	Remediation string   `json:"remediation,omitempty"`
}

// Report is Doctor's full output. Status is the worst status across
// every check (BLOCKED > DEGRADED > HEALTHY) — a single number an
// automated caller can branch on, with Checks giving a human the detail
// behind it.
type Report struct {
	Status Status        `json:"status"`
	Checks []CheckResult `json:"checks"`
}

// Options is everything Run needs to check. WorkerConfig is optional
// (its zero value skips the worker-config check) — a caller that only
// runs `serve`, never `worker`, has no lease/reaper settings to check.
// Store is a ports.QueryStore the caller already opened — see
// CheckDatabase's doc comment for why Doctor never opens one itself.
type Options struct {
	Config       config.Config
	Store        ports.QueryStore
	WorkerConfig workerpool.Config
	CheckWorker  bool
}

// Run executes every applicable check and aggregates them into a Report.
func Run(ctx context.Context, opts Options) Report {
	checks := []CheckResult{
		CheckLiveness(),
		CheckAppConfig(opts.Config),
		CheckDatabase(ctx, opts.Store),
		CheckRoot("artifact_root", opts.Config.ArtifactRoot),
		CheckGit(ctx),
	}
	if opts.CheckWorker {
		checks = append(checks, CheckWorkerConfig(opts.WorkerConfig))
	}

	providerNames := make([]string, 0, len(opts.Config.ProviderExecutables))
	for name := range opts.Config.ProviderExecutables {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)
	for _, name := range providerNames {
		checks = append(checks, CheckProviderExecutable(name, opts.Config.ProviderExecutables[name]))
	}

	return Report{Status: aggregate(checks), Checks: checks}
}

func aggregate(checks []CheckResult) Status {
	status := StatusHealthy
	for _, c := range checks {
		switch c.Status {
		case StatusBlocked:
			return StatusBlocked
		case StatusDegraded:
			status = StatusDegraded
		}
	}
	return status
}
