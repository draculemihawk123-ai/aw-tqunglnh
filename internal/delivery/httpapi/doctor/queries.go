package doctor

import (
	"context"
	"net/http"

	appdoctor "github.com/taQuangLing/agent-workflow/internal/app/doctor"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
)

// handleDoctor implements GET /doctor (operationId doctor): a plain read,
// no Idempotency-Key/If-Match, always 200 — unlike /health/ready (a binary
// load-balancer-style gate that answers 503 when not ready), Doctor is a
// rich diagnostic report a first-run UI RENDERS; the severity lives in the
// typed body (Status/Checks), never in the HTTP status line, so a caller
// can always parse and display it.
func handleDoctor(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		report := appdoctor.Run(ctx, appdoctor.Options{
			Config: deps.Config, Store: deps.Store, UnitOfWork: deps.UnitOfWork,
		})
		checks := append([]appdoctor.CheckResult{}, report.Checks...)
		checks = append(checks, isolationCheck(ctx, deps.Isolation))

		response := responseDTO{
			Status:          string(aggregateStatus(checks)),
			Checks:          make([]checkResultWire, len(checks)),
			RestartRequired: restartRequired(ctx, deps.UnitOfWork),
			Links:           operationLinks(),
		}
		for i, c := range checks {
			response.Checks[i] = checkResultWireFrom(c)
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, response, "")
	}
}

// aggregateStatus mirrors internal/app/doctor's own unexported aggregate
// function byte-for-byte (worst-of-three: BLOCKED > DEGRADED > HEALTHY) —
// re-declared here (rather than exported from that package for reuse)
// because this package's own Checks slice is wider than any single
// appdoctor.Report.Checks (it also includes isolationCheck's own extra
// entry), so the aggregation has to run again over the COMBINED list; the
// worst-of-three RULE itself is exactly appdoctor.Run's own, never a
// separately-invented policy.
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

// isolationCheck reports what isolation tier(s) THIS environment can
// actually enforce right now — the "isolation" facet of this task's own
// Phạm vi line that internal/app/doctor.Run itself has no check for (see
// doctor.go's own top-of-file doc comment). checker.VerifyEnforceable is
// I/O-free and side-effect-free (ports.IsolationEnforcementChecker's own
// doc comment), so calling it twice, synchronously, on every request is as
// cheap as any other appdoctor check.
//
// OPERATOR_TRUSTED_LOCAL failing is BLOCKED: ADR-023/ADR-013 make it the
// one tier Alpha ever actually schedules a node under (see
// internal/adapters/process.IsolationChecker's own doc comment) — if even
// that fails, nothing in this installation can run a node at all, a
// genuinely broken environment, not a documented limitation. ENFORCED_ISOLATED
// failing on its own is NOT blocked or degraded: today's only production
// checker (internal/adapters/process.IsolationChecker) always rejects it
// honestly (Alpha has no real OS-level sandbox yet, ADR-023's own "không
// bao giờ auto-downgrade" — a fail-closed admission check elsewhere is what
// actually protects a node pinned to it, never Doctor), so reporting that
// as anything other than HEALTHY here would just be permanent, un-actionable
// noise for every operator on every install.
func isolationCheck(ctx context.Context, checker ports.IsolationEnforcementChecker) appdoctor.CheckResult {
	const name = "isolation_enforcement"
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

// restartRequired forwards internal/app/safesettings.GetSafeSettings' own
// already-computed RestartRequired bool verbatim (see doctor.go's own
// top-of-file doc comment) — never any other field of the result, in
// particular never Desired (which carries ProviderCredentialRef). A read
// failure (e.g. the same unreachable database appdoctor's own "database"
// check would already report BLOCKED for) defaults to false rather than
// failing this whole request: mirrors cmd/aw/serve.go's own identical
// "ignore the error, the safe zero value is already correct" choice for
// safeSettingsAtBoot at process boot — the "database"/"safe_settings"
// CheckResults in the SAME response already name the real underlying
// failure; this bool has nothing further, safe, to add on top of that.
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
