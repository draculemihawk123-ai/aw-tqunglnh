package doctor

import (
	appdoctor "github.com/taQuangLing/agent-workflow/internal/app/doctor"
)

// checkResultWire is one appdoctor.CheckResult's own wire shape — declared
// separately from that exported type (rather than reused directly) purely
// to match every sibling endpoint package's own "every route defines its
// own wire DTO" convention (see internal/delivery/httpapi/adapterbuild/dto.go's
// own identical rationale for capabilityManifestBody), even though today
// the field set and JSON tags are identical.
type checkResultWire struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Status      string `json:"status"`
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

func checkResultWireFrom(c appdoctor.CheckResult) checkResultWire {
	return checkResultWire{
		Name: c.Name, Category: string(c.Category), Status: string(c.Status),
		Detail: c.Detail, Remediation: c.Remediation,
	}
}

// responseDTO is GET /doctor's own exact wire shape.
//
//   - Status/Checks are appdoctor.Report's own two fields, widened to this
//     package's own checkResultWire (isolationCheck's own extra
//     CheckResult, computed in queries.go, is folded into Checks and
//     Status exactly like every check appdoctor.Run itself produces —
//     a caller cannot tell which of the two aggregations produced any
//     given entry, by design).
//   - RestartRequired mirrors V6-10H's own SafeSettingsResult.RestartRequired
//     verbatim (see doctor.go's own top-of-file doc comment) — the one
//     "needs a restart to take effect" signal this installation has today.
//   - Links is a fixed map of operation name to installation-scoped path
//     this task's own "operation links" line asks for — never embedded
//     registry/query data, see doctor.go's own doc comment for why.
type responseDTO struct {
	Status          string            `json:"status"`
	Checks          []checkResultWire `json:"checks"`
	RestartRequired bool              `json:"restartRequired"`
	Links           map[string]string `json:"links"`
}

// operationLinks is the fixed set of installation-scoped routes a first-run
// UI can follow from Doctor's own response — every path here is a REAL
// route already registered by another endpoint task's own RegisterRoutes in
// cmd/aw/serve.go (never a placeholder/aspirational path): adapterBuilds
// (V6-10J), safeSettings (V6-10H), projects (V6-03A), healthLive/healthReady
// (V6-01). Deliberately excludes any project-scoped route (repositories,
// onboarding, retry-probe) — Doctor itself is installation-scoped and knows
// no project id to fill into such a path; a first-run UI reaches those via
// GET /projects first, per this task's own "Không làm: no duplicate
// repository onboarding/history/retry routes" line.
func operationLinks() map[string]string {
	return map[string]string{
		"adapterBuilds": "/adapter-builds",
		"safeSettings":  "/settings/safe",
		"projects":      "/projects",
		"healthLive":    "/health/live",
		"healthReady":   "/health/ready",
	}
}
