package securitymatrix

// Scope half of V6-13's own route×scope×role matrix: ADR-025's
// INSTALLATION|PROJECT boundary, nested-identifier ownership, and V6-02A's
// leakage-normalization policy — proven automatically for every route the
// real production composition registers, not for a hand-picked sample.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// hiddenResourceBody is the EXACT wire bytes httpapi.WriteResourceHidden
// produces (errors.go). Restated here as a literal rather than called
// through that helper on purpose: this suite must fail if the
// leakage-normalized response ever changes shape, including a change made
// inside the helper itself.
const hiddenResourceBody = `{"error":{"code":"NOT_FOUND","message":"the requested resource was not found"}}`

// pathSegments splits a registration-time path pattern into its segments,
// dropping the empty leading one.
func pathSegments(pattern string) []string {
	return strings.Split(strings.TrimPrefix(pattern, "/"), "/")
}

// isParam reports whether a path segment is a `{name}` placeholder, and
// returns the bare name.
func isParam(segment string) (string, bool) {
	if len(segment) >= 2 && segment[0] == '{' && segment[len(segment)-1] == '}' {
		return segment[1 : len(segment)-1], true
	}
	return "", false
}

// entityKind classifies what a path parameter actually identifies, from the
// parameter's own name plus the literal collection segment immediately
// before it — the same two clues a human reads the route table with. A
// generic `{id}` is meaningless on its own ("/projects/{id}" and
// "/runs/{id}" both use it), so the preceding collection segment is what
// disambiguates it.
//
// Returning kindUnknown is not a failure: it simply means this suite seeds
// no real instance of that entity, so the route is covered by the
// fabricated-identifier matrix only (see this package's own doc.go for why
// the two are deliberately complementary).
type entityKind int

const (
	kindUnknown entityKind = iota
	kindProject
	kindWorkItem
	kindRun
	kindFamily
	kindReleaseSet
	kindRepositoryWorkspace
	kindEvidence
	kindArtifact
	kindBlocker
	kindNodeRun
	kindRepository
)

func classifyParam(prevSegment, name string) entityKind {
	switch name {
	case "projectId":
		return kindProject
	case "workItemId":
		return kindWorkItem
	case "runId":
		return kindRun
	case "familyId":
		return kindFamily
	case "releaseSetId":
		return kindReleaseSet
	case "repositoryWorkspaceId":
		return kindRepositoryWorkspace
	case "evidenceId":
		return kindEvidence
	case "artifactId":
		return kindArtifact
	case "blockerId":
		return kindBlocker
	case "nodeRunId":
		return kindNodeRun
	}
	if name != "id" {
		return kindUnknown
	}
	switch prevSegment {
	case "projects":
		return kindProject
	case "runs":
		return kindRun
	case "repositories":
		return kindRepository
	default:
		// components, adapter-builds, definitions/{kind}/{id}, ... — real
		// entity kinds this suite deliberately does not seed.
		return kindUnknown
	}
}

// valueFor returns fx's own real, seeded identifier for kind, or "" when
// this suite seeds no instance of it.
func valueFor(kind entityKind, fx projectFixture) string {
	switch kind {
	case kindProject:
		return fx.ProjectID
	case kindWorkItem:
		return fx.WorkItemID
	case kindRun:
		return fx.RunID
	case kindFamily:
		return fx.FamilyID
	case kindReleaseSet:
		return fx.ReleaseSetID
	case kindRepositoryWorkspace:
		return fx.RepositoryWorkspaceID
	case kindEvidence:
		return fx.EvidenceID
	case kindArtifact:
		return fx.ArtifactID
	case kindBlocker:
		return fx.BlockerID
	case kindNodeRun:
		return fx.NodeRunID
	case kindRepository:
		return fx.RepositoryID
	default:
		return ""
	}
}

// buildPath instantiates a route pattern by asking resolve for each
// parameter's value in turn.
func buildPath(pattern string, resolve func(prevSegment, name string) string) string {
	segments := pathSegments(pattern)
	out := make([]string, 0, len(segments))
	prev := ""
	for _, segment := range segments {
		if name, ok := isParam(segment); ok {
			out = append(out, resolve(prev, name))
		} else {
			out = append(out, segment)
		}
		prev = segment
	}
	return "/" + strings.Join(out, "/")
}

// hasParams reports whether a route addresses something by identifier at
// all — a collection route such as `GET /projects` or `GET /doctor`
// legitimately answers 2xx with no identifier involved, so it is not part
// of the "never 2xx for an unknown identifier" claim.
func hasParams(pattern string) bool {
	for _, segment := range pathSegments(pattern) {
		if _, ok := isParam(segment); ok {
			return true
		}
	}
	return false
}

// crossProjectPath instantiates pattern with the FIRST project parameter
// bound to owner's own real ProjectID and every other resolvable parameter
// bound to foreign's own real identifier — the exact "I know a real id from
// another project, let me address it under mine" probe V6-13's own "Include
// cross-project guessed Run/WorkItem/blocker/workspace/ReleaseSet/artifact
// IDs" line asks for. Returns ok=false when the route has no project
// parameter or no foreign-resolvable parameter to swap, i.e. when there is
// no cross-project pairing to make.
func crossProjectPath(pattern string, owner, foreign projectFixture) (string, bool) {
	sawProject, sawForeign := false, false
	path := buildPath(pattern, func(prev, name string) string {
		kind := classifyParam(prev, name)
		if kind == kindProject && !sawProject {
			sawProject = true
			return owner.ProjectID
		}
		if v := valueFor(kind, foreign); v != "" {
			sawForeign = true
			return v
		}
		return fabricatedID
	})
	return path, sawProject && sawForeign
}

// ownerFabricatedPath instantiates pattern with the same owner ProjectID
// but EVERY other parameter fabricated — the "this identifier does not
// exist anywhere" control every cross-project response is compared against.
func ownerFabricatedPath(pattern string, owner projectFixture) string {
	sawProject := false
	return buildPath(pattern, func(prev, name string) string {
		if classifyParam(prev, name) == kindProject && !sawProject {
			sawProject = true
			return owner.ProjectID
		}
		return fabricatedID
	})
}

// fabricatedFixture is a projectFixture whose every identifier is the
// never-issued fabricatedID — the "foreign" side of the control probe, so
// the control and the real cross-project probe go through the IDENTICAL
// code path and differ in exactly one thing: whether the non-project
// identifiers are real.
func fabricatedFixture(projectID string) projectFixture {
	return projectFixture{
		ProjectID: projectID, RepositoryID: fabricatedID, WorkItemID: fabricatedID, FamilyID: fabricatedID,
		WorkspaceSetID: fabricatedID, RepositoryWorkspaceID: fabricatedID, RunID: fabricatedID,
		NodeRunID: fabricatedID, AttemptID: fabricatedID, EvidenceID: fabricatedID, ArtifactID: fabricatedID,
		ReleaseSetID: fabricatedID, BlockerID: fabricatedID,
	}
}

// routePrecondition is the non-identifier input a handful of routes
// validate BEFORE they ever reload the authoritative target named in the
// URL — a required `If-Match` precondition, or a required query parameter.
// Without it those routes answer 400 and the ownership reload this matrix
// exists to exercise never runs at all, so the matrix would be silently
// asserting nothing for them. Supplying it is not "helping the route pass":
// every value below is still bound to the FOREIGN project's own data (or a
// fabricated id for the control), so the route still has every reason to
// refuse — it just has to refuse for the right reason.
type routePrecondition struct {
	Query   func(foreign projectFixture) string
	Headers map[string]string
}

// routePreconditions is keyed by operationId. Each entry was added because
// the generic matrix observed that route short-circuiting on a precondition
// before its own scope check — never speculatively.
var routePreconditions = map[string]routePrecondition{
	// V6-10B: both commands require a strong If-Match naming the target's
	// current version (workspacerelease.go/workspacereconcile.go's own
	// RequireIfMatch). Version 1 is a plausible guess a probing caller
	// would make; the point is only to get past the precondition check.
	"requestWorkspaceSetRelease":     {Headers: map[string]string{httpapi.IfMatchHeader: httpapi.ETagFromVersion(1)}},
	"requestWorkspaceReconciliation": {Headers: map[string]string{httpapi.IfMatchHeader: httpapi.ETagFromVersion(1)}},
	// V6-10D: the three bounded inspection routes require the full
	// WorkspaceScope quadruple plus a revision/anchor, as query parameters
	// (workspaceinspection/params.go's own requireQueryParam discipline).
	"getWorkspaceSource": {Query: func(f projectFixture) string {
		return "?repositoryId=" + f.RepositoryID + "&workspaceSetId=" + f.WorkspaceSetID +
			"&path=README.md&revision=" + fixtureVCSObjectID + "&generation=1"
	}},
	"getWorkspaceDiff": {Query: func(f projectFixture) string {
		return "?repositoryId=" + f.RepositoryID + "&workspaceSetId=" + f.WorkspaceSetID +
			"&base=" + fixtureVCSObjectID + "&baseGeneration=1&result=" + fixtureVCSObjectID + "&resultGeneration=1"
	}},
	"getWorkspaceRepositoryLog": {Query: func(f projectFixture) string {
		return "?repositoryId=" + f.RepositoryID + "&workspaceSetId=" + f.WorkspaceSetID +
			"&anchor=" + fixtureVCSObjectID + "&anchorGeneration=1"
	}},
}

// applyPrecondition returns the query suffix and extra headers (if any)
// operationID needs, with every identifier bound to foreign's own data.
func applyPrecondition(operationID string, foreign projectFixture) (string, map[string]string) {
	pre, ok := routePreconditions[operationID]
	if !ok {
		return "", nil
	}
	query := ""
	if pre.Query != nil {
		query = pre.Query(foreign)
	}
	return query, pre.Headers
}

// emptyCollectionOnUnknownProject is the reviewed, closed set of
// project-scoped COLLECTION routes that legitimately answer 200 with an
// empty collection — rather than 404 — for a project identifier this
// server never issued.
//
// Both were checked by hand against their own real query
// (internal/app/work.ListWorkItems -> ports.WorkRepository.ListWorkItemsByProject,
// and internal/delivery/httpapi/kanban's own projection read, both of which
// filter strictly by the URL's own ProjectID): an unknown project produces
// the byte-identical response a real but EMPTY project produces, so this is
// leakage-normalized by construction in the other direction — a caller
// learns nothing about whether the project exists, and no other project's
// rows can ever appear. TestUnknownProjectCollectionsAreEmptyAndIdentical
// below asserts exactly that rather than taking it on trust, and any route
// ADDED to this list has to be reviewed the same way first.
var emptyCollectionOnUnknownProject = map[string]bool{
	"listWorkItems":      true,
	"listWorkItemKanban": true,
}

// TestScopeMatrix_UnknownIdentifierIsNeverServed proves the universal, no-
// exceptions half of the scope matrix: for EVERY registered route that
// addresses something by identifier, a request naming an identifier this
// server never issued must never answer 2xx — no route silently falls back
// to "any project", "the first row" or "all rows" when its own scope
// lookup fails — and no response may ever contain a real, seeded identifier
// belonging to either project.
func TestScopeMatrix_UnknownIdentifierIsNeverServed(t *testing.T) {
	e := newEnv(t)
	covered := 0
	for _, d := range e.routes.Descriptors() {
		if !hasParams(d.Path) {
			continue
		}
		if d.OperationID == "uiShell" {
			// GET /ui/{path...} is the SPA client-side router's own server-
			// side fallback (httpcompose/compose.go's own doc comment) — its
			// `{path...}` segment is not an identifier lookup at all, it is a
			// wildcard that MUST answer 200 for literally any path (that is
			// the whole point: any client-side route the browser navigates
			// or refreshes to needs the same HTML shell). The "never serve an
			// unknown identifier" rule this test proves has no meaning here.
			continue
		}
		covered++
		t.Run(d.OperationID, func(t *testing.T) {
			query, headers := applyPrecondition(d.OperationID, fabricatedFixture(fabricatedID))
			got := e.do(t, req{
				Method: d.Method, Path: fabricatedPath(d.Path) + query, Headers: headers,
				IdempotencyKey: "sm-unknown-" + d.OperationID, Body: bodyForMethod(d.Method),
			})
			if got.Status >= 200 && got.Status < 300 && !emptyCollectionOnUnknownProject[d.OperationID] {
				t.Errorf("%s %s (scope %s) answered %d for a never-issued identifier: %s",
					d.Method, d.Path, d.ScopeKind, got.Status, truncate(got.Body))
			}
			if got.Status >= 500 {
				t.Errorf("%s %s answered %d for a never-issued identifier — an unknown id is a client condition, never an internal error: %s",
					d.Method, d.Path, got.Status, truncate(got.Body))
			}
			assertNoSeededIdentifier(t, e, d.Method+" "+d.Path, got.Body)
		})
	}
	if covered == 0 {
		t.Fatal("no identifier-addressing routes were checked — the matrix is not reading the real composed registry")
	}
	t.Logf("unknown-identifier matrix covered %d identifier-addressing routes", covered)
}

// TestUnknownProjectCollectionsAreEmptyAndIdentical is the explicit proof
// behind every entry in emptyCollectionOnUnknownProject: answering 200 for
// an unknown project is only acceptable while the answer is indistinguishable
// from a real, empty project's own answer and contains nothing from any
// other project. Two DIFFERENT never-issued project identifiers must produce
// byte-identical responses, and neither may mention a seeded identifier.
func TestUnknownProjectCollectionsAreEmptyAndIdentical(t *testing.T) {
	e := newEnv(t)
	checked := 0
	for _, d := range e.routes.Descriptors() {
		if !emptyCollectionOnUnknownProject[d.OperationID] {
			continue
		}
		checked++
		t.Run(d.OperationID, func(t *testing.T) {
			first := e.do(t, req{Method: d.Method, Path: buildPath(d.Path, func(string, string) string { return fabricatedID })})
			second := e.do(t, req{Method: d.Method, Path: buildPath(d.Path, func(string, string) string { return fabricatedID + "-other" })})
			if first.Status != second.Status || first.Body != second.Body {
				t.Errorf("%s %s: two different never-issued project ids produced different answers (%d %s vs %d %s) — "+
					"the difference is an existence oracle", d.Method, d.Path, first.Status, truncate(first.Body), second.Status, truncate(second.Body))
			}
			assertNoSeededIdentifier(t, e, d.Method+" "+d.Path, first.Body)
			if strings.Contains(first.Body, `"items":[]`) == false {
				t.Errorf("%s %s: expected an empty collection for a never-issued project, got %s", d.Method, d.Path, truncate(first.Body))
			}
		})
	}
	if checked != len(emptyCollectionOnUnknownProject) {
		t.Errorf("emptyCollectionOnUnknownProject names %d routes but only %d were found in the real composed registry — "+
			"remove the stale entry", len(emptyCollectionOnUnknownProject), checked)
	}
}

// assertNoSeededIdentifier fails if body mentions any real identifier this
// suite seeded in either project — the blunt, no-false-negatives form of
// "a probe with an identifier this caller made up must never come back
// carrying one this server actually issued".
func assertNoSeededIdentifier(t *testing.T, e *env, label, body string) {
	t.Helper()
	for _, fx := range []projectFixture{e.alpha, e.beta} {
		for name, value := range map[string]string{
			"workItemId": fx.WorkItemID, "runId": fx.RunID, "familyId": fx.FamilyID,
			"releaseSetId": fx.ReleaseSetID, "repositoryWorkspaceId": fx.RepositoryWorkspaceID,
			"evidenceId": fx.EvidenceID, "artifactId": fx.ArtifactID, "blockerId": fx.BlockerID,
			"nodeRunId": fx.NodeRunID, "repositoryId": fx.RepositoryID,
		} {
			if value != "" && strings.Contains(body, value) {
				t.Errorf("%s: response for a never-issued identifier leaked project %s's real %s (%q): %s",
					label, fx.ProjectID, name, value, truncate(body))
			}
		}
	}
}

// TestScopeMatrix_CrossProjectIdentifierIsNeverServed is the strong,
// seeded half: a REAL identifier belonging to project beta, addressed under
// project alpha's own URL, must never be served — contract point 3's "Mọi
// item route reload authoritative target để suy Project/scope và
// authorize; không tin ID shape, payload hoặc projection" as a live,
// per-route assertion.
func TestScopeMatrix_CrossProjectIdentifierIsNeverServed(t *testing.T) {
	e := newEnv(t)
	covered := 0
	for _, d := range e.routes.Descriptors() {
		path, ok := crossProjectPath(d.Path, e.alpha, e.beta)
		if !ok {
			continue
		}
		covered++
		t.Run(d.OperationID, func(t *testing.T) {
			query, headers := applyPrecondition(d.OperationID, e.beta)
			got := e.do(t, req{
				Method: d.Method, Path: path + query, Headers: headers,
				IdempotencyKey: "sm-cross-" + d.OperationID, Body: bodyForMethod(d.Method),
			})
			if got.Status >= 200 && got.Status < 300 {
				t.Errorf("%s %s served project %s's own data under project %s's URL (status %d): %s",
					d.Method, d.Path, e.beta.ProjectID, e.alpha.ProjectID, got.Status, truncate(got.Body))
			}
			// A cross-project probe must never be answered from the
			// foreign project's data in any form — including an error
			// message that names it (which would confirm the id is real).
			if strings.Contains(got.Body, e.beta.ProjectID) {
				t.Errorf("%s %s: response body names the foreign project %q — %s",
					d.Method, d.Path, e.beta.ProjectID, truncate(got.Body))
			}
		})
	}
	if covered == 0 {
		t.Fatal("no cross-project pairing could be built — the fixture seeded no comparable entity in both projects")
	}
	t.Logf("cross-project matrix covered %d routes pairing project alpha's URL with project beta's real identifiers", covered)
}

// TestScopeMatrix_ExistsElsewhereIsIndistinguishableFromNeverExisted is
// V6-02A's own leakage-normalization policy ("Normalize unauthorized/
// not-found theo leakage policy", httpapi.WriteResourceHidden's own doc
// comment) proven per route: the response to "a real id from another
// project" and the response to "an id that never existed" must be
// byte-identical, so a caller can never enumerate real identifiers by
// noticing which failure it gets.
func TestScopeMatrix_ExistsElsewhereIsIndistinguishableFromNeverExisted(t *testing.T) {
	e := newEnv(t)
	hiddenCount, covered := 0, 0
	for _, d := range e.routes.Descriptors() {
		foreignPath, ok := crossProjectPath(d.Path, e.alpha, e.beta)
		if !ok {
			continue
		}
		covered++
		t.Run(d.OperationID, func(t *testing.T) {
			// The same idempotency key for both halves would make the
			// second request a receipt replay of the first for a mutating
			// route, so each half gets its own.
			foreignQuery, foreignHeaders := applyPrecondition(d.OperationID, e.beta)
			absentQuery, absentHeaders := applyPrecondition(d.OperationID, fabricatedFixture(e.alpha.ProjectID))
			foreign := e.do(t, req{
				Method: d.Method, Path: foreignPath + foreignQuery, Headers: foreignHeaders,
				IdempotencyKey: "sm-leak-foreign-" + d.OperationID, Body: bodyForMethod(d.Method),
			})
			absent := e.do(t, req{
				Method: d.Method, Path: ownerFabricatedPath(d.Path, e.alpha) + absentQuery, Headers: absentHeaders,
				IdempotencyKey: "sm-leak-absent-" + d.OperationID, Body: bodyForMethod(d.Method),
			})
			if foreign.Status != absent.Status {
				t.Errorf("%s %s: foreign-project id answered %d but never-issued id answered %d — the difference tells a caller the foreign id is real",
					d.Method, d.Path, foreign.Status, absent.Status)
			}
			if foreign.Body != absent.Body {
				t.Errorf("%s %s: response bodies differ between a foreign-project id and a never-issued id\n foreign: %s\n absent:  %s",
					d.Method, d.Path, truncate(foreign.Body), truncate(absent.Body))
			}
			if foreign.Status == http.StatusNotFound && strings.TrimSpace(foreign.Body) == hiddenResourceBody {
				hiddenCount++
				return
			}
			// Every cross-project probe that reaches its route's own
			// ownership check answers with the exact WriteResourceHidden
			// envelope. A route that answers anything else has short-circuited
			// on some earlier precondition instead — which is not itself a
			// leak (the byte-identity assertions above already passed) but
			// DOES mean the ownership check was never exercised, so it must
			// be given its own entry in routePreconditions rather than
			// silently counting as covered.
			t.Errorf("%s %s: cross-project probe answered %d %s instead of the leakage-normalized 404 — "+
				"its ownership check was never reached; add the missing precondition to routePreconditions",
				d.Method, d.Path, foreign.Status, truncate(foreign.Body))
		})
	}
	t.Logf("leakage-normalization matrix covered %d routes; %d answered with the exact httpapi.WriteResourceHidden 404 envelope",
		covered, hiddenCount)
	if hiddenCount != covered {
		t.Errorf("%d of %d cross-project probes produced the leakage-normalized 404 envelope — every one of them must",
			hiddenCount, covered)
	}
}

// TestInstallationScopedRoutesCarryNoProjectSegment is the structural half
// of ADR-025's closed scope pair: a route declared INSTALLATION must not
// address a project at all (its authority is the whole installation), and a
// route declared PROJECT must be able to reach a project — either from a
// `{projectId}`/`{id}` segment under /projects, or by reloading the
// authoritative target named by some other identifier in its path.
func TestInstallationScopedRoutesCarryNoProjectSegment(t *testing.T) {
	e := newEnv(t)
	for _, d := range e.routes.Descriptors() {
		hasProjectParam := false
		prev := ""
		for _, segment := range pathSegments(d.Path) {
			if name, ok := isParam(segment); ok && classifyParam(prev, name) == kindProject {
				hasProjectParam = true
			}
			prev = segment
		}
		switch d.ScopeKind {
		case httpapi.ScopeInstallation:
			if hasProjectParam {
				t.Errorf("%s %s (%s) is declared INSTALLATION scope yet names a project in its own path — "+
					"ADR-025's installation scope is a closed list that does not include per-project resources",
					d.Method, d.Path, d.OperationID)
			}
		case httpapi.ScopeProject:
			if !hasProjectParam && !hasParams(d.Path) {
				t.Errorf("%s %s (%s) is declared PROJECT scope but names neither a project nor any identifier to "+
					"reload a project from — it has no way to derive its own scope",
					d.Method, d.Path, d.OperationID)
			}
		}
	}
}

func truncate(s string) string {
	const limit = 300
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
