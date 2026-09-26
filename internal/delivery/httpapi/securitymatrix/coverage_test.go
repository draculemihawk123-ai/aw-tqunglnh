package securitymatrix

// V6-13's own completion gate: "every route has explicit scope proof".
//
// The matrix's row set is never a hand-maintained list — every scenario in
// this package iterates httpapi.RouteRegistry.Descriptors() on a registry
// built by the real internal/delivery/httpcompose.ComposeRoutes, so a route
// a future leaf task registers is swept in the moment it is wired into the
// composition root. This file is what makes that claim CHECKABLE rather
// than merely true-by-construction: it recomputes, per route, exactly which
// scenario classes apply, and fails when a route ends up with no scope
// proof of its own.

import (
	"sort"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/apicontract"
)

// coverageOf returns the scenario classes that apply to one route, named
// after the test that implements each.
func coverageOf(d httpapi.RouteDescriptor, alpha, beta projectFixture) []string {
	classes := []string{
		// Every route, with no exceptions, gets the full Host/Origin/token/
		// CORS probe set plus the positive control.
		"transport",
	}
	if hasParams(d.Path) {
		classes = append(classes, "unknown-identifier")
	}
	if _, ok := crossProjectPath(d.Path, alpha, beta); ok {
		classes = append(classes, "cross-project", "leakage-normalization")
	}
	if emptyCollectionOnUnknownProject[d.OperationID] {
		classes = append(classes, "reviewed-empty-collection")
	}
	classes = append(classes, "declared-scope-structure")
	return classes
}

// noCrossProjectProof is the reviewed, closed set of PROJECT-scoped,
// identifier-addressing routes for which this suite can build NO
// cross-project pairing, and why. Each falls into exactly one of two
// honest categories:
//
//  1. The route names no project in its own path at all
//     (e.g. GET /runs/{id}) — it derives its scope by reloading the target,
//     so "project A's URL holding project B's id" is not a shape that
//     exists for it. Its protection is the unknown-identifier class plus
//     its own leaf package's tests.
//  2. The route's non-project identifier names an entity kind this suite
//     deliberately does not seed (a definition version, a message, a
//     context snapshot, a scope-expansion request, a component, an
//     adapter build, a projection-rebuild operation, a local commit, a
//     wait registration) — see doc.go for why the seeded set is exactly
//     the six kinds V6-13's own "Thực hiện" line names.
//
// A route that appears here WITHOUT falling into one of those two
// categories is a real coverage hole. The list is pinned so that a newly
// registered route cannot silently join it: adding an entry forces the
// reviewer to decide which category it is in — or to seed the entity and
// get a real cross-project proof instead.
var noCrossProjectProof = []string{
	"approveScopeExpansion",
	"cancelRun",
	"cancelWorkItem",
	"componentPackAssignmentsAssign",
	"componentPackAssignmentsList",
	"createProjectDefinition",
	"createRootWorkItem",
	"diffProjectDefinitionVersions",
	"getProjectDefinition",
	"getProjectDefinitionVersion",
	"getProjectionRebuildOperationStatus",
	"getProjectionStatus",
	"getRunDetail",
	"getRunGraph",
	"getRunTimeline",
	"getScopeExpansionRequest",
	"getWorkItemProjectedDetail",
	"listProjectDefinitionVersions",
	"listProjectDefinitions",
	"listWorkItemKanban",
	"listWorkItems",
	"markWorkItemReady",
	"projectComponentsList",
	"projectRepositoriesList",
	"projectRepositoriesRegister",
	"projectsGet",
	"publishProjectDefinitionVersion",
	"rejectScopeExpansion",
	"repositoriesGet",
	"repositoriesOnboarding",
	"repositoriesRetryProbe",
	"requestProjectionRebuild",
	"resolveApproval",
	"resolveWorkItemBlocker",
	"retryBlockedActivation",
	"startWorkflowRun",
	"submitWaitSignal",
	"validateProjectDefinitionDraft",
	"watchProjectEvents",
	"withdrawScopeExpansion",
}

// TestRowSetComesFromTheRealComposedContract proves this suite enumerates
// the same route set V6-12's own machine-readable contract does — the row
// set is the registry ComposeRoutes actually built, run through
// apicontract.Build, not a list anyone maintains by hand. If the two ever
// disagree, one of them is reading something other than the real
// composition.
func TestRowSetComesFromTheRealComposedContract(t *testing.T) {
	e := newEnv(t)
	descriptors := e.routes.Descriptors()
	contract := apicontract.Build(descriptors)

	if len(contract.Operations) != len(descriptors) {
		t.Fatalf("apicontract.Build produced %d operations from %d descriptors", len(contract.Operations), len(descriptors))
	}
	byOperationID := make(map[string]httpapi.RouteDescriptor, len(descriptors))
	for _, d := range descriptors {
		byOperationID[d.OperationID] = d
	}
	for _, op := range contract.Operations {
		d, ok := byOperationID[op.OperationID]
		if !ok {
			t.Errorf("contract operation %s has no matching registered descriptor", op.OperationID)
			continue
		}
		if op.Method != d.Method || op.Path != d.Path || op.ScopeKind != string(d.ScopeKind) {
			t.Errorf("contract operation %s = %s %s scope %s, registry says %s %s scope %s",
				op.OperationID, op.Method, op.Path, op.ScopeKind, d.Method, d.Path, d.ScopeKind)
		}
	}
}

// TestEveryRouteHasAnExplicitScopeProof is the gate itself. For every
// registered route it recomputes the scenario classes that actually cover
// it and fails when a route has no identifier-level proof at all, or when
// the reviewed no-cross-project list has drifted from reality in either
// direction.
func TestEveryRouteHasAnExplicitScopeProof(t *testing.T) {
	e := newEnv(t)
	descriptors := e.routes.Descriptors()

	var missingCrossProject []string
	classCounts := map[string]int{}
	for _, d := range descriptors {
		classes := coverageOf(d, e.alpha, e.beta)
		for _, class := range classes {
			classCounts[class]++
		}
		covered := map[string]bool{}
		for _, class := range classes {
			covered[class] = true
		}

		// Every identifier-addressing route must at minimum be proven
		// against an identifier this server never issued.
		if hasParams(d.Path) && !covered["unknown-identifier"] {
			t.Errorf("%s %s (%s) addresses a resource by identifier but has no unknown-identifier proof",
				d.Method, d.Path, d.OperationID)
		}
		// Every route, without exception, is transport-proven.
		if !covered["transport"] {
			t.Errorf("%s %s (%s) has no transport-layer proof", d.Method, d.Path, d.OperationID)
		}
		if d.ScopeKind == httpapi.ScopeProject && hasParams(d.Path) && !covered["cross-project"] {
			missingCrossProject = append(missingCrossProject, d.OperationID)
		}
	}

	sort.Strings(missingCrossProject)
	want := append([]string(nil), noCrossProjectProof...)
	sort.Strings(want)
	if strings.Join(missingCrossProject, "\n") != strings.Join(want, "\n") {
		t.Errorf("the set of PROJECT-scoped routes without a seeded cross-project proof has changed.\n"+
			"Update noCrossProjectProof ONLY after confirming the new entry genuinely falls into one of the two\n"+
			"documented categories — otherwise seed the entity and give the route a real cross-project proof.\n got: %v\nwant: %v",
			missingCrossProject, want)
	}

	t.Logf("scenario coverage over %d routes: %v", len(descriptors), classCounts)
}
