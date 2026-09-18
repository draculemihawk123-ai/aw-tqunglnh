package apicontract

import "strings"

// GapStatus classifies one CheckUXGaps finding.
type GapStatus string

const (
	// GapImplementedDocStale means the proposed operationId IS now
	// registered, verbatim, in the real Contract, but
	// docs/design/11-v6-00-ux-artifact.md still carries the "[CHƯA CÓ]"
	// marker for it — the owning backend task closed the gap and nobody
	// went back to update that marker. Not a failure: V6-00's own doc
	// explicitly says zero-gap was never its own completion bar
	// (11-v6-00-ux-artifact.md §16.3: "Zero gap KHÔNG phải precondition để
	// hoàn thành V6-00"), and V6-12 does not own that document — only
	// reports the staleness.
	GapImplementedDocStale GapStatus = "IMPLEMENTED_DOC_STALE"
	// GapImplementedRenamed means the proposed operationId is not
	// registered under that EXACT name, but this package's own
	// knownRenamedProposals records — after manual, cited verification
	// (see that map's own doc comment) — which real, currently-registered
	// operationId(s) cover the identical concern. V6-00's own §1 is
	// explicit that a "Proposed operationId" is a non-binding starting
	// point, not a frozen value ("KHÔNG phải giá trị đã freeze... V6-12 là
	// nơi duy nhất compose/freeze operationId thật") — so a leaf task
	// choosing a different final name is expected, not a gap.
	GapImplementedRenamed GapStatus = "IMPLEMENTED_RENAMED"
	// GapOpenAcceptable means the proposed operationId is genuinely not
	// registered yet, AND its Owner Task ID names a task this package's
	// own knownOpenOwnerTasks records as not-yet-merged as of this
	// snapshot — a legitimate, still-open gap with a real owner, exactly
	// what V6-00's own "gap có owner" contract promises.
	GapOpenAcceptable GapStatus = "OPEN_GAP_ACCEPTABLE"
	// GapOpenAcknowledged means the proposed operationId is genuinely not
	// registered under any name, its Owner Task ID's own task IS already
	// merged (so no future task is coming to close it as originally
	// planned), and this package's own knownUnimplementedGaps records it
	// as a reviewed, real, currently-open gap in that already-merged
	// task's own deliverable — one V6-12 itself is not allowed to close
	// (this task's own "Không làm: no new endpoint/DTO/authority"). It is
	// reported, not silently hidden, but does not fail
	// uxgap_test.go's own hard gate the way GapOpenUnresolved does: the
	// gate instead asserts the ACKNOWLEDGED set matches this exact,
	// pinned list, so a brand new unacknowledged gap cannot hide behind
	// this mechanism.
	GapOpenAcknowledged GapStatus = "OPEN_GAP_ACKNOWLEDGED"
	// GapOpenUnresolved means none of the above — a proposed operationId
	// this package cannot account for under any of the three explicit,
	// reviewed mechanisms above. This is the one status
	// uxgap_test.go's own hard gate fails the build on.
	GapOpenUnresolved GapStatus = "OPEN_GAP_UNRESOLVED"
)

// UXGapFinding is one (row, proposed operationId) pair CheckUXGaps
// evaluated.
type UXGapFinding struct {
	OperationID string
	Section     string
	OwnerTaskID string
	Status      GapStatus
}

// knownOpenOwnerTasks is V6-12's own explicit, hand-verified record of
// which Owner Task IDs referenced anywhere in
// docs/design/11-v6-00-ux-artifact.md are NOT yet merged as of this task's
// own snapshot (2026-09, branch feat/v6-12-api-contract-router).
//
// V6-12's own dependency list (docs/design/08-v6-api-projections.md:571-573)
// requires V6-00, V6-03A, V6-04, V6-04A, V6-05, V6-06, V6-06A, V6-06B,
// V6-06C, V6-06D, V6-07, V6-07A, V6-07B, V6-09B, V6-10, V6-10A, V6-10B,
// V6-10D, V6-10F, V6-10H, V6-10J and V6-11 to already be merged before this
// task can even begin — and V6-09B's own transitive dependency chain
// (P3 in that same doc: V6-08 -> V6-08A -> V6-09 -> V6-09A -> V6-09B) means
// V6-08/V6-08A/V6-09/V6-09A must already be merged too, and V6-10F's own
// {V6-10B, V6-10E} dependency plus V6-10D's own {V6-10B, V6-10C}
// dependency mean V6-10C/V6-10E are also already in. uxgap_test.go's own
// TestEveryOwnerTaskIDIsInTheKnownMergedSet enumerates every distinct
// "V6-NN" token this package's own ParseUXDoc ever returns as an
// OwnerTaskID and asserts each one is a member of exactly that
// already-merged set — proving, mechanically, that this map is allowed to
// be empty today rather than the checker simply not having looked hard
// enough. It stays a named, explicit value (never a silently-assumed
// "everything not found is fine" default) so a future run of this checker
// — against a possibly-updated UX doc, or from a point in the roadmap
// before V6-11 exists — has one obvious place to record a genuinely open
// Owner Task ID instead of this checker papering over a real gap as
// acceptable.
var knownOpenOwnerTasks = map[string]bool{}

// knownRenamedProposals is V6-12's own reviewed record of every UX-doc
// "[CHƯA CÓ]" row whose proposed operationId is not registered verbatim,
// but whose OWN Owner Task ID package DOES register a same-concept
// operation under a different final name (or, in one case, split across
// two operations). Each entry was verified by reading the owning leaf
// package's own route registration (Method+Path) and, where present, its
// own doc comment — never inferred from name similarity alone. A route's
// Method+Path is the strongest evidence available: the UX doc row's own
// prose ("Trạng thái workspace/repository-workspace", "Danh sách
// ReleaseSet", ...) names the resource, and the actual registered Path
// names the identical resource, just under an operationId the leaf task
// chose independently — exactly what V6-00's own §1 anticipates
// ("Proposed operationId... KHÔNG phải giá trị đã freeze").
//
// uxgap_test.go's own TestKnownRenamedProposalsAreActuallyRegistered
// re-verifies every value here is a real, currently-registered
// operationId (so this map itself can never silently rot into pointing at
// something that no longer exists).
var knownRenamedProposals = map[string][]string{
	// Screen 1 — Doctor: doctor.go's own package doc: "one thin GET
	// /doctor aggregation" over the exact installation Doctor-report
	// concern UX doc row 1 names.
	"getInstallationDoctorReport": {"doctor"},

	// Screen 2 — Project/Repository/Component: catalog.go registers every
	// one of these under a "<resource><Verb>" convention
	// (projectsList/projectsGet/...) rather than the UX doc's own
	// "<verb><Resource>" proposal; same GET /projects,
	// GET /projects/{id}, GET /projects/{id}/repositories,
	// GET /repositories/{id}, GET /projects/{id}/components,
	// GET /components/{id}/pack-assignments paths in both cases.
	"listProjects":        {"projectsList"},
	"getProject":          {"projectsGet"},
	"listRepositories":    {"projectRepositoriesList"},
	"getRepository":       {"repositoriesGet"},
	"listComponents":      {"projectComponentsList"},
	"listPackAssignments": {"componentPackAssignmentsList"},

	// Screen 5 — Kanban board: kanban/routes.go's own
	// GET /projects/{projectId}/work-items/kanban.
	"listKanbanCards": {"listWorkItemKanban"},

	// Screen 7 — Task/WorkItem detail: the UX doc's own row 2 text already
	// anticipates this ("có thể là field lồng trong GetWorkItem, không bắt
	// buộc operationId riêng") — workitem/routes.go instead gave it its
	// own operationId, listChildWorkItems, over
	// GET /projects/{projectId}/work-items/{workItemId}/children.
	"listWorkItemFamily": {"listChildWorkItems"},

	// Screen 8 — Run graph & timeline: rundetail.go's own GET /runs/{id}.
	"getRun": {"getRunDetail"},

	// Screen 9 — Workspace/source-diff-log viewer: this UX doc row's own
	// text names BOTH "workspace" and "repository-workspace" state in one
	// row ("Trạng thái workspace/repository-workspace") — V6-10B
	// registered these as two distinct operations
	// (workspaceroutes.go: GET /projects/{id}/workspace-sets/{familyId}
	// and GET /projects/{id}/repository-workspaces/{id}), never merged
	// into one "getWorkspaceState".
	"getWorkspaceState": {"getWorkspaceSetState", "getRepositoryWorkspaceState"},
	// workspaceinspection/routes.go prefixes all three with "Workspace":
	// GET .../repository-workspaces/{id}/source|diff|repository-log.
	"getSource":        {"getWorkspaceSource"},
	"getDiff":          {"getWorkspaceDiff"},
	"getRepositoryLog": {"getWorkspaceRepositoryLog"},

	// Screen 10 — ReleaseSet & local commit: releaseset/routes.go's own
	// GET /projects/{id}/task-families/{familyId}/release-sets (scoped to
	// one TaskFamily, hence "ForFamily") and
	// GET .../release-sets/{id}/local-commits/{id}.
	"listReleaseSets":               {"listReleaseSetsForFamily"},
	"getLocalCommitOperationStatus": {"getReleaseSetLocalCommitStatus"},

	// Cross-cutting §15 — Projection rebuild status: projectionrebuild.go's
	// own GET /projects/{id}/projection/rebuild-operations/{operationId}.
	"getProjectionRebuildStatus": {"getProjectionRebuildOperationStatus"},
}

// knownUnimplementedGaps is V6-12's own reviewed record of every UX-doc
// "[CHƯA CÓ]" row whose proposed operationId is NOT registered under any
// name (verbatim or renamed — see knownRenamedProposals above) even though
// its own Owner Task ID's package IS already merged. Each entry is a real,
// currently-open gap in that ALREADY-MERGED task's own deliverable — V6-12
// itself must not close it (this task's own "Không làm: no new
// endpoint/DTO/authority"), only report it honestly. uxgap_test.go's own
// gate asserts the set of GapOpenAcknowledged findings equals exactly this
// map's own key set — proving no OTHER, undiscovered gap is hiding behind
// this mechanism, and forcing a conscious edit here (with a fresh review)
// the day a follow-up task finally closes one of these.
var knownUnimplementedGaps = map[string]string{
	// Screen 3 — Definition catalog: docs/design/11-v6-00-ux-artifact.md
	// §4 row 1 proposes `listDefinitions` ("Danh sách definition (global +
	// /projects/{id}/definitions)"), Owner Task ID V6-05. Grepping every
	// OperationID internal/delivery/httpapi/definitions/routes.go
	// registers (createDefinition, validateDefinitionDraft,
	// publishDefinitionVersion, getDefinition, listDefinitionVersions,
	// getDefinitionVersion, diffDefinitionVersions, and their
	// project-scoped mirrors) confirms there is no
	// GET /definitions/{kind} (or GET /definitions) route at all — V6-05
	// implemented create/validate/publish/get-one/list-versions/diff, but
	// never "list every definition of a kind". This is a real, standalone
	// gap in V6-05's own already-merged deliverable, not a V6-12 scope
	// item; flagged here for a follow-up task rather than silently
	// invented by this generator.
	"listDefinitions": "internal/delivery/httpapi/definitions has no GET /definitions/{kind} " +
		"(or GET /definitions) list route — V6-05 implemented create/validate/publish/get-one/" +
		"list-versions/diff but never a definitions-of-a-kind list query. Needs a follow-up task; " +
		"V6-12 does not add it (no new endpoint is in this task's own scope).",
}

// CheckUXGaps cross-references every UXRow in rows against contract's own
// registered operationIds, and reports a GapStatus finding for each
// (row, proposed operationId) pair whose row was marked ChuaCoGap in the
// UX doc — see GapStatus' own doc comments for exactly what each status
// means. A row not marked ChuaCoGap (i.e. the doc already says "[ĐÃ CÓ]"
// or carries no gap marker at all) produces no finding: this function's
// whole job is auditing the doc's OWN "[CHƯA CÓ]" claims, not re-deriving
// ground truth for rows the doc already believes are settled.
func CheckUXGaps(rows []UXRow, contract Contract) []UXGapFinding {
	registered := make(map[string]bool, len(contract.Operations))
	for _, op := range contract.Operations {
		registered[op.OperationID] = true
	}

	var findings []UXGapFinding
	for _, row := range rows {
		if !row.ChuaCoGap {
			continue
		}
		for _, opID := range row.OperationIDs {
			findings = append(findings, UXGapFinding{
				OperationID: opID, Section: row.Section, OwnerTaskID: row.OwnerTaskID,
				Status: classifyGap(opID, row.OwnerTaskID, registered),
			})
		}
	}
	return findings
}

func classifyGap(opID, ownerCell string, registered map[string]bool) GapStatus {
	if registered[opID] {
		return GapImplementedDocStale
	}
	if renamedTo, isRenamed := knownRenamedProposals[opID]; isRenamed && allRegistered(renamedTo, registered) {
		return GapImplementedRenamed
	}
	if ownerTaskIsKnownOpen(ownerCell) {
		return GapOpenAcceptable
	}
	if _, acknowledged := knownUnimplementedGaps[opID]; acknowledged {
		return GapOpenAcknowledged
	}
	return GapOpenUnresolved
}

func allRegistered(operationIDs []string, registered map[string]bool) bool {
	for _, id := range operationIDs {
		if !registered[id] {
			return false
		}
	}
	return true
}

func ownerTaskIsKnownOpen(ownerCell string) bool {
	for task := range knownOpenOwnerTasks {
		if strings.Contains(ownerCell, task) {
			return true
		}
	}
	return false
}

// CheckUndocumentedOperations reports every operationId registered in
// contract whose EXACT string does not appear as a proposed operationId
// in ANY row (regardless of that row's own ChuaCoGap marker), and is not
// one of knownRenamedProposals' own target operationIds (the renamed
// counterpart of a documented proposal is, transitively, documented too).
//
// This is intentionally a coarser, best-effort signal than CheckUXGaps,
// not a second hard per-operation gate: docs/design/11-v6-00-ux-artifact.md
// was authored at the CONCEPT/screen level (one row per user-facing
// action/query), while several leaf tasks reasonably added companion
// routes the doc never enumerated as separate rows — a project-scoped
// mirror of a global definition endpoint, a getXDetail alongside a listX,
// and similar. Individually re-justifying each one would be exactly the
// "retrofitting metadata a leaf task should have already supplied" V6-12's
// own "Không làm" line excludes. routeinventory_test.go's own
// TestUndocumentedOperationsSnapshot instead pins the current, reviewed
// result as an explicit, sorted list — any CHANGE to that list (a brand
// new operationId appearing, or one disappearing) fails the test and
// forces a conscious review, exactly like a golden file, without
// requiring line-by-line justification of routes that were never the
// subject of an individual UX doc row to begin with.
func CheckUndocumentedOperations(rows []UXRow, contract Contract) []string {
	documented := make(map[string]bool)
	for _, row := range rows {
		for _, id := range row.OperationIDs {
			documented[id] = true
		}
	}
	for _, targets := range knownRenamedProposals {
		for _, id := range targets {
			documented[id] = true
		}
	}
	for id := range knownRenamedOrImplicitOperations {
		documented[id] = true
	}

	var undocumented []string
	for _, op := range contract.Operations {
		if !documented[op.OperationID] {
			undocumented = append(undocumented, op.OperationID)
		}
	}
	return undocumented
}

// knownRenamedOrImplicitOperations documents every registered operationId
// CheckUndocumentedOperations would otherwise flag purely because the
// FINAL frozen name differs from what docs/design/11-v6-00-ux-artifact.md
// happened to propose, for a row that was ALREADY marked "[ĐÃ CÓ]" (not a
// "[CHƯA CÓ]" gap) at doc-authoring time — such a row never reaches
// CheckUXGaps/knownRenamedProposals at all (that pair only ever looks at
// ChuaCoGap==true rows), yet the row still named a specific proposed
// operationId that, like every "[CHƯA CÓ]" one, was never binding (V6-00's
// own §1: "Proposed operationId... KHÔNG phải giá trị đã freeze"). Three
// further entries cover the §15 Cross-cutting concerns table specifically
// (healthLive/healthReady/bootstrap), where the same non-binding-name
// reasoning applies even though those rows carry no gap marker at all.
// Each entry names the exact UX doc row that already covers the concern,
// so this is a reviewed exception list, never a silent skip.
var knownRenamedOrImplicitOperations = map[string]string{
	// Screen 2 — Project/Repository/Component: these four rows were
	// already marked "[ĐÃ CÓ: ...]" (an underlying internal/app/ports type
	// existed, just not yet a CommandEnvelope/HTTP route) rather than
	// "[CHƯA CÓ]", so knownRenamedProposals above never evaluates them —
	// but catalog.go still registered each under its own
	// "<resource><Verb>" convention rather than the row's own proposed
	// name.
	"projectsCreate": "docs/design/11-v6-00-ux-artifact.md §3 Screen 2 row 2 proposes `createProject` " +
		"(marked [ĐÃ CÓ], not a gap) — catalog.go registered it as `projectsCreate`.",
	"projectRepositoriesRegister": "docs/design/11-v6-00-ux-artifact.md §3 Screen 2 row 4 proposes " +
		"`registerRepository` (marked [ĐÃ CÓ]) — catalog.go registered it as `projectRepositoriesRegister`.",
	"repositoriesRetryProbe": "docs/design/11-v6-00-ux-artifact.md §3 Screen 2 row 8 proposes " +
		"`retryRepositoryProbe` (marked [ĐÃ CÓ]) — catalog.go registered it as `repositoriesRetryProbe`, " +
		"matching V6-03A's own \"leaf canonical là retry-probe\" note in that same row.",
	"componentPackAssignmentsAssign": "docs/design/11-v6-00-ux-artifact.md §3 Screen 2 row 10 proposes " +
		"`assignComponentPack` (marked [ĐÃ CÓ], and V6-00's own §16.2 discusses this exact mapping) — " +
		"catalog.go registered it as `componentPackAssignmentsAssign`.",

	"healthLive": "docs/design/11-v6-00-ux-artifact.md §15 Cross-cutting concerns proposes " +
		"`getHealthLive` for this same concern (\"Health liveness\") — V6-01 shipped it as " +
		"`healthLive` before this task ever froze a name; same concern, non-binding proposed name.",
	"healthReady": "docs/design/11-v6-00-ux-artifact.md §15 Cross-cutting concerns proposes " +
		"`getHealthReady` for this same concern (\"Health readiness\") — V6-01 shipped it as " +
		"`healthReady`; same reasoning as healthLive above.",
	"bootstrap": "docs/design/11-v6-00-ux-artifact.md §15 Cross-cutting concerns has a row for " +
		"\"Bootstrap per-start token (không phải action người dùng thấy)\" but its own Proposed " +
		"operationId cell is explicitly \"*(không áp dụng — bootstrap HTML no-store/CSP)*\": the " +
		"doc itself says bootstrap is HTML/CSP delivery, not a JSON API operation, and deliberately " +
		"proposes no operationId for it — this package's own ParseUXDoc correctly extracts no " +
		"identifier from that cell, so the concern is documented even though no operationId string " +
		"matches.",
}
