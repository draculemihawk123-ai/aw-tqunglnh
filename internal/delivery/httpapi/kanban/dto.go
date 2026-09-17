package kanban

import (
	"encoding/json"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
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
// state — see badges.go's own doc comment for the dedup rule.
type RepositoryBadgeDTO struct {
	RepositoryID string `json:"repositoryId"`
	State        string `json:"state"`
}

// KanbanCardDTO is this package's own delivery-owned wire shape for one
// projection.WorkItemCardRow — deliberately a separate type from that
// app-layer struct (mirroring internal/delivery/httpapi's own
// repositoryWorkspaceStateResponse precedent, workspacestate.go), for two
// reasons: (1) WorkspaceSetID here is the CORRECTED value after this
// package's own root-row cross-reference for a child WorkItem (row.go's own
// doc comment names this exact scope boundary — a raw WorkItemCardRow
// leaves it empty on every child row), and (2) RepositoryBadges is this
// package's own addition, not part of the frozen V6-08 projection schema at
// all.
//
// Every field below except WorkspaceSetID/RepositoryBadges is copied
// verbatim from the underlying WorkItemCardRow — Status/ActiveRunStatus/
// BlockerCount/TopBlockerType/PendingScopeExpansionCount are always the
// PROJECTED (possibly stale — see the sibling Freshness envelope) values,
// never re-derived or authorized by this package (this task's own "Không
// làm: projection không decide readiness/ValidAction").
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

// decodeCardRow decodes row's own PayloadJSON into a projection.WorkItemCardRow
// — the exact canonical shape every Reducer in internal/app/projection wrote
// (row.go's own CanonicalJSON), never re-derived or hand-parsed here.
func decodeCardRow(row ports.ProjectionRow) (projection.WorkItemCardRow, error) {
	var card projection.WorkItemCardRow
	if err := json.Unmarshal([]byte(row.PayloadJSON), &card); err != nil {
		return projection.WorkItemCardRow{}, fmt.Errorf("kanban: decode projection row %s: %w", row.EntityKey, err)
	}
	return card, nil
}

// cardToDTO converts one decoded WorkItemCardRow (identified by
// workItemID/projectID — EntityKey/ProjectID from the wrapping
// ports.ProjectionRow, never re-read off the payload itself, matching how
// every other query in this codebase treats the STORED key as authoritative
// over anything a payload might also happen to repeat) into the wire DTO.
// workspaceSetID is the CALLER's own already-resolved value (row.WorkspaceSetID
// for a root row, or the cross-referenced root row's own value for a child —
// see list.go/detail.go's own resolveWorkspaceSetID), and badges is whatever
// badgeLookup already resolved for that workspaceSetID (nil when
// workspaceSetID is empty).
func cardToDTO(workItemID, projectID string, card projection.WorkItemCardRow, workspaceSetID string, badges []RepositoryBadgeDTO) KanbanCardDTO {
	return KanbanCardDTO{
		WorkItemID: workItemID, ProjectID: projectID, FamilyID: card.FamilyID, Title: card.Title,
		ParentWorkItemID: card.ParentWorkItemID, IsRoot: card.IsRoot, WorkspaceSetID: workspaceSetID,
		Status: card.Status, ActiveRunID: card.ActiveRunID, ActiveRunStatus: card.ActiveRunStatus,
		BlockerCount: card.BlockerCount, TopBlockerType: card.TopBlockerType,
		PendingScopeExpansionCount: card.PendingScopeExpansionCount, RepositoryBadges: badges,
	}
}

// cardFromAuthoritative builds a fallback KanbanCardDTO straight from the
// REAL, authoritative WorkItem row — used only by detail.go when no
// projection row exists yet for this WorkItem (a genuine, honestly reported
// projection-lag edge case: the WorkItem was just created and the live
// consumer, V6-08A, has not applied its own RootWorkItemCreated/
// ChildWorkItemCreated event yet). Every field this fallback cannot know
// (ActiveRun*/BlockerCount/TopBlockerType/PendingScopeExpansionCount/
// WorkspaceSetID/RepositoryBadges) stays at its honest zero value rather
// than a fabricated guess — this is still display-only, exactly like a real
// projected card, never fed into readiness/ValidAction authority.
func cardFromAuthoritative(item workdomain.WorkItem) KanbanCardDTO {
	card := KanbanCardDTO{
		WorkItemID: string(item.ID), ProjectID: string(item.ProjectID), FamilyID: string(item.FamilyID),
		Title: item.Title, IsRoot: item.ParentID == nil, Status: string(item.Status),
	}
	if item.ParentID != nil {
		card.ParentWorkItemID = string(*item.ParentID)
	}
	return card
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
// projected Card plus the FRESH authoritative Readiness this package's own
// detail.go computes AFTER the card is built (see routes.go's own doc
// comment for why this ordering is what makes the "action race" Verify
// bullet hold), plus the advisory ValidActions this task's own "authoritative
// service recomputes valid actions with target version before response"
// line asks for — never derived from Card's own (possibly stale) Status,
// always from Readiness.
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
