// Package projection is V6-08's own frozen, generation-aware Kanban/
// task-detail projection schema and projector inventory
// (docs/design/08-v6-api-projections.md V6-08). It defines WHAT a
// (EventType, SchemaVersion) means for this projection — a Classification
// of APPLY(handlerVersion, Reducer) or IGNORE(reason) for every event this
// codebase's real eventschema registry currently registers — and provides
// pure, deterministic Reducer functions for every APPLY entry. It never
// live-consumes the journal, rebuilds a generation or serves an HTTP
// response: those are V6-08A/V6-09/V6-10's own later jobs, which this
// package's own "Hoàn thành khi" bar requires them to build WITHOUT any new
// design choice — every Reducer/Classification/EntityKey decision a live
// consumer or rebuild worker needs already lives here.
package projection

import (
	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
)

// ProjectionName is the one named projection this package defines — see
// migration 0039_projection_schema.sql's own doc comment for why a single
// row-per-WorkItem projection serves BOTH the Kanban card list (Screen 5)
// and the projected (non-authoritative) WorkItem detail sibling data
// (Screen 7's own authoritative detail is a SEPARATE, live query — V6-04 —
// never this projection).
const ProjectionName = "workitem"

// WorkItemCardRow is ProjectionName's own row payload shape — the frozen
// "Kanban/task-detail schema" V6-08's own Mục tiêu names. Every field here
// is either a direct WorkItem-lifecycle fact (Status/Title/family/scope
// linkage) or a coarse Run-summary fact a Kanban card needs to render its
// badge (ActiveRunID/ActiveRunStatus) — never a node/attempt/graph-level
// detail (Screen 8's own Run graph & timeline reads directly from
// authoritative runtime tables, V6-06B, with no projection dependency at
// all; see this package's own catalog.go doc comment for the full
// APPLY/IGNORE reasoning).
type WorkItemCardRow struct {
	WorkItemID string `json:"workItemId"`
	ProjectID  string `json:"projectId"`
	FamilyID   string `json:"familyId"`
	Title      string `json:"title"`
	// ParentWorkItemID is empty for a root WorkItem.
	ParentWorkItemID string `json:"parentWorkItemId,omitempty"`
	IsRoot           bool   `json:"isRoot"`
	// WorkspaceSetID is populated only for a root WorkItem's own row — a
	// child inherits its root's WorkspaceSet (ADR-028's own "Subtask kế
	// thừa TaskFamily/WorkspaceSet của root task") but this projection does
	// not itself resolve or duplicate that inheritance chain: V6-10's own
	// "multi-repo badge aggregation" (its own explicit Thực hiện bullet)
	// cross-references a child's FamilyID back to its root's own row for
	// this field, a deliberate scope boundary — the same "task-owned
	// design decision" latitude this doc's own framing grants every V6
	// task, documented here so a later reader does not mistake the empty
	// field on a child row for a data-loss bug.
	WorkspaceSetID string `json:"workspaceSetId,omitempty"`
	// Status is always one of work.WorkItemStatus's own six closed values
	// (BACKLOG/READY/ACTIVE/BLOCKED/DONE/CANCELLED — internal/domain/work's
	// own package, not imported here to keep this package's own "Không
	// làm: no domain leaf... import" boundary honest; Reducers assign the
	// exact same wire-string values by hand, verified in this package's
	// own golden tests against work.WorkItemStatus's real constants).
	Status string `json:"status"`
	// ActiveRunID/ActiveRunStatus are this card's own coarse Run-summary
	// badge — empty when no Run is currently associated. ActiveRunStatus
	// is never a runtime.RunStatus wire value verbatim: it is this
	// projection's OWN small vocabulary (ACTIVE/CANCELLING/COMPLETING/
	// FAILED/CANCELLED), chosen deliberately narrower than the full Run
	// domain's own state machine (a Kanban badge needs "what should I show
	// the operator right now," not the full authoritative state machine —
	// that stays Screen 7/8's own job).
	ActiveRunID     string `json:"activeRunId,omitempty"`
	ActiveRunStatus string `json:"activeRunStatus,omitempty"`
	// BlockerCount/TopBlockerType surface WORK_ITEM_BLOCKED's own
	// authority (never invented independently by this projection, per
	// contract rule 5 "Projection không là authority" and V6-10's own
	// "Không làm: projection không decide readiness/ValidAction").
	// TopBlockerType is the MOST RECENTLY applied blocker's own
	// BlockerType — a real simplification (a WorkItem can carry more than
	// one open blocker in the authoritative domain) this projection
	// accepts because the Kanban board only ever needs one representative
	// blocker badge per card; the FULL blocker list stays Screen 7/8's
	// own authoritative query, never this projection's job to enumerate.
	BlockerCount   int    `json:"blockerCount"`
	TopBlockerType string `json:"topBlockerType,omitempty"`
	// PendingScopeExpansionCount is the number of ScopeExpansionRequested
	// requests not yet Approved/Rejected/Withdrawn for this WorkItem's own
	// FamilyID — a coarse "has an open scope-expansion request" badge.
	PendingScopeExpansionCount int `json:"pendingScopeExpansionCount"`
}

// Exists reports whether row represents a real, already-created WorkItem
// row (Status is always non-empty for a real row — the two "create" events,
// RootWorkItemCreated/ChildWorkItemCreated, are the only reducers that ever
// set it from empty) — a Reducer's own prior parameter is the zero
// WorkItemCardRow{} exactly when V6-08A (not yet built) has no existing row
// for this EntityKey yet.
func (row WorkItemCardRow) Exists() bool { return row.Status != "" }

// isTerminal reports whether row.Status is one of the two closed-forever
// WorkItemStatus values (DONE/CANCELLED) — every Reducer in this package
// that could otherwise move Status uses this guard first, so a
// Run-level event that arrives AFTER a WorkItem-level terminal event
// (a real possibility: this codebase's own event application is at-least-
// once and not globally ordered across aggregates, V6-08's own "Cursor is
// greatest scanned global JournalPosition" plus per-row
// LastAppliedJournalPosition fencing only protects against a DUPLICATE of
// the SAME event, never against two DIFFERENT events for the same
// WorkItem arriving in an unexpected relative order) never regresses a
// card back out of its own terminal column.
func (row WorkItemCardRow) isTerminal() bool {
	return row.Status == statusDone || row.Status == statusCancelled
}

const (
	statusBacklog   = "BACKLOG"
	statusReady     = "READY"
	statusActive    = "ACTIVE"
	statusBlocked   = "BLOCKED"
	statusDone      = "DONE"
	statusCancelled = "CANCELLED"
)

const (
	runStatusActive     = "ACTIVE"
	runStatusCancelling = "CANCELLING"
	runStatusCompleting = "COMPLETING"
	runStatusFailed     = "FAILED"
	runStatusCancelled  = "CANCELLED"
)

// CanonicalJSON returns row's own canonical (stable key order, no
// non-deterministic whitespace) JSON encoding — the exact string this
// package's Reducers write into ports.ProjectionRow.PayloadJSON, and the
// exact input CanonicalRowHash hashes. Reuses
// internal/domain/authoring.Canonicalize (this codebase's own single
// "same semantic content -> same bytes/hash regardless of field order"
// convention, ADR-012) rather than a second, package-local canonicalizer.
func (row WorkItemCardRow) CanonicalJSON() ([]byte, error) {
	canonicalJSON, _, err := authoring.Canonicalize(row, authoring.CanonicalizeOptions{})
	return canonicalJSON, err
}

// CanonicalRowHash returns row's own "sha256:<hex>" content hash — the
// exact value V6-09A's own "canonical old/new diff" verify requirement
// (docs/design/08-v6-api-projections.md V6-09A) compares a rebuilt shadow
// row against its live-consumer-built counterpart with.
func (row WorkItemCardRow) CanonicalRowHash() (string, error) {
	_, hash, err := authoring.Canonicalize(row, authoring.CanonicalizeOptions{})
	return hash, err
}
