package kanban

import (
	kanbanapp "github.com/taQuangLing/agent-workflow/internal/app/kanban"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// RepositoryBadgeDTO is one repository's own coarse Kanban-card badge — the
// "multi-repo badge aggregation" this task's own Thực hiện line names,
// sourced from tx.Work().ListWorkspaceSetRepositoryWorkspaces (never a
// second, invented repository-membership list): one badge per
// distinct RepositoryID currently provisioned under a WorkItem family's own
// WorkspaceSet, its own MOST RECENT (highest Generation) RepositoryWorkspace
// state — see internal/app/kanban's badges.go for the dedup rule.
type RepositoryBadgeDTO struct {
	RepositoryID string `json:"repositoryId"`
	State        string `json:"state"`
}

// KanbanCardDTO is this package's own delivery-owned wire shape for one
// internal/app/kanban.Card — deliberately a separate type from that
// app-layer struct (mirroring internal/delivery/httpapi's own
// repositoryWorkspaceStateResponse precedent, workspacestate.go): the HTTP
// wire contract (and the OpenAPI schema generated from this type) is owned
// here and must not move because an application struct gains a field.
//
// Every field below is copied verbatim from the application Card — whose
// Status/ActiveRunStatus/BlockerCount/TopBlockerType/
// PendingScopeExpansionCount are always the PROJECTED (possibly stale — see
// the sibling Freshness envelope) values, never re-derived or authorized by
// this package (this task's own "Không làm: projection không decide
// readiness/ValidAction").
type KanbanCardDTO struct {
	WorkItemID                 string               `json:"workItemId"`
	ProjectID                  string               `json:"projectId"`
	FamilyID                   string               `json:"familyId"`
	Title                      string               `json:"title"`
	ParentWorkItemID           string               `json:"parentWorkItemId,omitempty"`
	IsRoot                     bool                 `json:"isRoot"`
	WorkspaceSetID             string               `json:"workspaceSetId,omitempty"`
	Status                     string               `json:"status"`
	ActiveRunID                string               `json:"activeRunId,omitempty"`
	ActiveRunStatus            string               `json:"activeRunStatus,omitempty"`
	BlockerCount               int                  `json:"blockerCount"`
	TopBlockerType             string               `json:"topBlockerType,omitempty"`
	PendingScopeExpansionCount int                  `json:"pendingScopeExpansionCount"`
	RepositoryBadges           []RepositoryBadgeDTO `json:"repositoryBadges,omitempty"`
}

// cardToDTO converts one application Card into the wire DTO.
func cardToDTO(card kanbanapp.Card) KanbanCardDTO {
	var badges []RepositoryBadgeDTO
	if card.RepositoryBadges != nil {
		badges = make([]RepositoryBadgeDTO, 0, len(card.RepositoryBadges))
		for _, b := range card.RepositoryBadges {
			badges = append(badges, RepositoryBadgeDTO{RepositoryID: b.RepositoryID, State: b.State})
		}
	}
	return KanbanCardDTO{
		WorkItemID: card.WorkItemID, ProjectID: card.ProjectID, FamilyID: card.FamilyID, Title: card.Title,
		ParentWorkItemID: card.ParentWorkItemID, IsRoot: card.IsRoot, WorkspaceSetID: card.WorkspaceSetID,
		Status: card.Status, ActiveRunID: card.ActiveRunID, ActiveRunStatus: card.ActiveRunStatus,
		BlockerCount: card.BlockerCount, TopBlockerType: card.TopBlockerType,
		PendingScopeExpansionCount: card.PendingScopeExpansionCount, RepositoryBadges: badges,
	}
}

// cardsToDTOs converts a page of application Cards, always returning a
// non-nil slice so an empty page serializes as [] rather than null.
func cardsToDTOs(cards []kanbanapp.Card) []KanbanCardDTO {
	items := make([]KanbanCardDTO, 0, len(cards))
	for _, c := range cards {
		items = append(items, cardToDTO(c))
	}
	return items
}

// freshnessToHTTP converts the application Freshness into the shared
// httpapi.Freshness envelope. ports.ProjectionStatus's own three values
// (LIVE/DEGRADED/STALE) are the IDENTICAL wire vocabulary
// httpapi.FreshnessStatus already freezes (freshness.go) — a direct string
// cast, never a translation table that could silently drift.
func freshnessToHTTP(f kanbanapp.Freshness) httpapi.Freshness {
	return httpapi.Freshness{
		Generation: int(f.Generation), AsOfJournalPosition: int64(f.AsOfJournalPosition), Status: httpapi.FreshnessStatus(f.Status),
	}
}

// kanbanListResponse wraps a page of KanbanCardDTO plus the shared
// NextCursor/Freshness envelope every paginated projection-backed route in
// this codebase reports (V6-02A's own "Phạm vi: page/limit ... Freshness"
// line).
type kanbanListResponse struct {
	Items      []KanbanCardDTO   `json:"items"`
	NextCursor string            `json:"nextCursor,omitempty"`
	Freshness  httpapi.Freshness `json:"freshness"`
}

// workItemDetailResponse is GET /work-items/{id}/detail's own response: the
// projected Card plus the FRESH authoritative Readiness internal/app/kanban
// computes AFTER the card is built (see routes.go's own doc comment for why
// this ordering is what makes the "action race" Verify bullet hold), plus the
// advisory ValidActions this task's own "authoritative service recomputes
// valid actions with target version before response" line asks for — never
// derived from Card's own (possibly stale) Status, always from Readiness.
type workItemDetailResponse struct {
	Card         KanbanCardDTO             `json:"card"`
	Readiness    workapp.WorkItemReadiness `json:"readiness"`
	Freshness    httpapi.Freshness         `json:"freshness"`
	ValidActions []httpapi.ValidAction     `json:"validActions"`
}

// validActionsForReadiness returns the advisory ValidAction list a client
// may act on next — mirroring httpapi's own workspacestate.go
// reconcileValidActions idiom (action.go's own doc comment: "never
// authority... a client uses TargetVersion only to populate its own next
// request's concurrency precondition"). The ONLY action this package ever
// advertises is markWorkItemReady, and ONLY when readiness (the fresh
// authoritative result, never the projected card) says the WorkItem is
// BACKLOG and Ready — exactly internal/app/work.MarkWorkItemReady's own
// real eligibility precondition, restated, never approximated from
// projected data.
func validActionsForReadiness(readiness workapp.WorkItemReadiness) []httpapi.ValidAction {
	if !readiness.Ready || readiness.Status != string(workdomain.WorkItemBacklog) {
		return []httpapi.ValidAction{}
	}
	return []httpapi.ValidAction{{
		OperationID: "markWorkItemReady", ScopeKind: httpapi.ScopeProject, TargetVersion: int64(readiness.Version),
	}}
}
