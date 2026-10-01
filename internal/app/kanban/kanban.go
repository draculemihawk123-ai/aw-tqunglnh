// Package kanban holds the two public application queries behind the
// projected work board: ListWorkItemKanban (the project's Kanban cards) and
// GetWorkItemProjectedDetail (one card plus a fresh authoritative
// readiness). They exist so that every public query sits on a public
// APPLICATION operation (ADR-028) instead of in a delivery adapter: before
// V8-12R-01 internal/delivery/httpapi/kanban read tx.Projections() directly,
// which left the parity registry reporting both operations as MISSING_APP and
// left the CLI with no way to read the board at all. The HTTP handlers and the
// `aw work-item kanban` / `aw work-item detail` leaves are now thin shells
// over these functions.
//
// # What is projected and what is authoritative
//
// Every card returned here carries the projection row's own PROJECTED
// Status/ActiveRunStatus/BlockerCount (display-only, possibly stale — the
// sibling Freshness says how stale). Nothing in this package decides
// readiness or a valid action from them: GetWorkItemProjectedDetail
// additionally calls work.ExplainWorkItemReadiness, the one readiness
// authority in the codebase, fresh and in its OWN transaction opened after the
// card-building one has closed, so no matter how stale the projected card is,
// the readiness is always derived from the real WorkItem as it is when the
// function returns.
package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
)

// ErrGenerationChanged is returned by ListWorkItemKanban when a resumed walk's
// ResumePoint names a different active projection generation than the one
// now serving reads — V6-09A's fenced cutover swapped it out from under the
// walk, so continuing would silently mix two generations' rows. The caller
// restarts the walk from the first page.
var ErrGenerationChanged = errors.New("kanban: the active projection generation changed since this walk began")

// Filter is the canonical form of a board query's status/family filter. Its
// JSON shape is frozen: the HTTP layer fingerprints it into every pagination
// cursor, so changing a tag or the field order would invalidate every cursor
// minted by an older process. Build one with NormalizeFilter, never by hand —
// the fingerprint is only stable for a normalized value.
type Filter struct {
	Statuses []string `json:"statuses"`
	FamilyID string   `json:"familyId"`
}

// NormalizeFilter canonicalizes a raw filter (trimmed, upper-cased, deduped,
// sorted statuses; trimmed family id), so two requests naming the identical
// filter in a different order or letter case are the same query.
func NormalizeFilter(rawStatuses []string, rawFamilyID string) Filter {
	seen := map[string]bool{}
	statuses := make([]string, 0, len(rawStatuses))
	for _, raw := range rawStatuses {
		status := strings.ToUpper(strings.TrimSpace(raw))
		if status == "" || seen[status] {
			continue
		}
		seen[status] = true
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	return Filter{Statuses: statuses, FamilyID: strings.TrimSpace(rawFamilyID)}
}

func (f Filter) matches(card projection.WorkItemCardRow) bool {
	if f.FamilyID != "" && card.FamilyID != f.FamilyID {
		return false
	}
	if len(f.Statuses) == 0 {
		return true
	}
	for _, status := range f.Statuses {
		if card.Status == status {
			return true
		}
	}
	return false
}

// RepositoryBadge is one repository's coarse board badge: its most recent
// (highest Generation) RepositoryWorkspace state under the card's
// WorkspaceSet — see badges.go.
type RepositoryBadge struct {
	RepositoryID string `json:"repositoryId"`
	State        string `json:"state"`
}

// Card is one projected WorkItem card. WorkspaceSetID is the CORRECTED value
// (a child row never carries it itself; it is cross-referenced from the
// family's root row), and RepositoryBadges is this package's own addition to
// the frozen projection.WorkItemCardRow schema.
type Card struct {
	WorkItemID                 string            `json:"workItemId"`
	ProjectID                  string            `json:"projectId"`
	FamilyID                   string            `json:"familyId"`
	Title                      string            `json:"title"`
	ParentWorkItemID           string            `json:"parentWorkItemId,omitempty"`
	IsRoot                     bool              `json:"isRoot"`
	WorkspaceSetID             string            `json:"workspaceSetId,omitempty"`
	Status                     string            `json:"status"`
	ActiveRunID                string            `json:"activeRunId,omitempty"`
	ActiveRunStatus            string            `json:"activeRunStatus,omitempty"`
	BlockerCount               int               `json:"blockerCount"`
	TopBlockerType             string            `json:"topBlockerType,omitempty"`
	PendingScopeExpansionCount int               `json:"pendingScopeExpansionCount"`
	RepositoryBadges           []RepositoryBadge `json:"repositoryBadges,omitempty"`
}

// Freshness is how current the projection the cards were read from is:
// the active generation, the greatest journal position already reflected in
// it, and ports.ProjectionStatus's own LIVE/DEGRADED/STALE verdict. Both
// "no generation was ever created" and "a generation exists but its consumer
// never wrote a checkpoint" are honestly reported as STALE (never LIVE, never
// an error) — claiming a healthy projection no consumer ever confirmed would
// be false authority.
type Freshness struct {
	Generation          uint64 `json:"generation"`
	AsOfJournalPosition uint64 `json:"asOfJournalPosition"`
	Status              string `json:"status"`
}

// ResumePoint is where a paginated walk continues. A walk pins UpperWatermark
// on its first page and keeps it on every later one, so a card created or
// changed after the walk began (either bumps its own journal position) is
// excluded from that walk entirely — stable paging under concurrent writes.
type ResumePoint struct {
	Generation     uint64
	UpperWatermark int64
	LastKey        string
}

// ListRequest names one board read.
type ListRequest struct {
	ProjectID string
	Filter    Filter
	// Limit caps the page; <= 0 means every matching card.
	Limit int
	// Resume continues an earlier walk; nil starts one.
	Resume *ResumePoint
}

// Page is one board read's result. Next is non-nil exactly when more matching
// cards remain after Items; it is for a delivery adapter to encode into its
// own cursor and is never part of the serialized result.
type Page struct {
	Items     []Card       `json:"items"`
	Freshness Freshness    `json:"freshness"`
	Next      *ResumePoint `json:"-"`
}

// decodedCard bundles a projection row with its decoded card payload: the
// walk needs the row's wrapper fields (EntityKey, LastAppliedJournalPosition)
// and the payload's (Status, FamilyID, IsRoot, WorkspaceSetID) throughout.
type decodedCard struct {
	row  ports.ProjectionRow
	card projection.WorkItemCardRow
}

// ListWorkItemKanban returns ProjectID's board: its projected cards filtered by
// req.Filter, in ascending WorkItemID order, one bounded page at a time.
//
// # Keyset pagination mechanics
//
// Rows arrive EntityKey (WorkItemID) ascending (re-sorted defensively in case
// that port contract ever drifts) and a page resumes strictly after
// Resume.LastKey. A WorkItemID is a random UUID unrelated to creation or
// update order, so sorting by it alone could let a card projected AFTER the
// walk began land, by lexicographic luck, in a page not yet reached. The walk
// is therefore also bounded by each row's LastAppliedJournalPosition: the
// first page pins UpperWatermark to the greatest value across the project's
// current generation and every later page of the same walk stays bounded by
// that pinned value.
//
// Resume.Generation must equal the generation now active, else
// ErrGenerationChanged. A missing project is not an error: a project with no
// projection rows reads as an empty STALE board, exactly like one whose
// consumer has not run.
func ListWorkItemKanban(ctx context.Context, uow ports.UnitOfWork, req ListRequest) (Page, error) {
	projectID := strings.TrimSpace(req.ProjectID)
	if projectID == "" {
		return Page{}, errors.New("kanban: ProjectID is required")
	}

	var page Page
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		generation, freshness, err := resolveProjectionState(ctx, tx, projectID)
		if err != nil {
			return err
		}
		page.Freshness = freshness

		rawRows, err := tx.Projections().ListProjectionRows(ctx, projectID, projection.ProjectionName, generation)
		if err != nil {
			return err
		}
		sort.Slice(rawRows, func(i, j int) bool { return rawRows[i].EntityKey < rawRows[j].EntityKey })

		all := make([]decodedCard, 0, len(rawRows))
		rootByFamily := map[string]projection.WorkItemCardRow{}
		var upperWatermark int64
		for _, row := range rawRows {
			card, err := decodeCardRow(row)
			if err != nil {
				return err
			}
			all = append(all, decodedCard{row: row, card: card})
			if card.IsRoot {
				rootByFamily[card.FamilyID] = card
			}
			if pos := int64(row.LastAppliedJournalPosition); pos > upperWatermark {
				upperWatermark = pos
			}
		}

		var afterKey string
		boundWatermark := upperWatermark
		if req.Resume != nil {
			if req.Resume.Generation != generation {
				return ErrGenerationChanged
			}
			afterKey = req.Resume.LastKey
			boundWatermark = req.Resume.UpperWatermark
		}

		candidates := make([]decodedCard, 0, len(all))
		for _, entry := range all {
			if entry.row.EntityKey <= afterKey {
				continue
			}
			if int64(entry.row.LastAppliedJournalPosition) > boundWatermark {
				continue
			}
			if !req.Filter.matches(entry.card) {
				continue
			}
			candidates = append(candidates, entry)
		}

		pageCount := len(candidates)
		if req.Limit > 0 && req.Limit < pageCount {
			pageCount = req.Limit
		}
		window := candidates[:pageCount]

		badges := newBadgeLookup(tx)
		items := make([]Card, 0, len(window))
		for _, entry := range window {
			workspaceSetID := resolveWorkspaceSetID(entry.card, rootByFamily)
			cardBadges, err := badges.Badges(ctx, workspaceSetID)
			if err != nil {
				return err
			}
			items = append(items, newCard(entry.row.EntityKey, projectID, entry.card, workspaceSetID, cardBadges))
		}
		page.Items = items

		if len(candidates) > pageCount {
			page.Next = &ResumePoint{
				Generation: generation, UpperWatermark: boundWatermark, LastKey: window[len(window)-1].row.EntityKey,
			}
		}
		return nil
	})
	if err != nil {
		return Page{}, err
	}
	return page, nil
}

// resolveWorkspaceSetID returns card's effective WorkspaceSetID: its own when
// it IS the root, otherwise the root row's value for the SAME FamilyID —
// empty when no root row for that family exists in this generation (an honest
// "not resolvable yet", never a guess).
func resolveWorkspaceSetID(card projection.WorkItemCardRow, rootByFamily map[string]projection.WorkItemCardRow) string {
	if card.IsRoot {
		return card.WorkspaceSetID
	}
	return rootByFamily[card.FamilyID].WorkspaceSetID
}

// decodeCardRow decodes row's PayloadJSON into the exact canonical shape every
// Reducer in this package's sibling internal/app/projection wrote, never
// re-derived or hand-parsed.
func decodeCardRow(row ports.ProjectionRow) (projection.WorkItemCardRow, error) {
	var card projection.WorkItemCardRow
	if err := json.Unmarshal([]byte(row.PayloadJSON), &card); err != nil {
		return projection.WorkItemCardRow{}, fmt.Errorf("kanban: decode projection row %s: %w", row.EntityKey, err)
	}
	return card, nil
}

// newCard converts one decoded row into a Card. workItemID is the row's STORED
// EntityKey, authoritative over anything the payload might also repeat.
func newCard(workItemID, projectID string, card projection.WorkItemCardRow, workspaceSetID string, badges []RepositoryBadge) Card {
	return Card{
		WorkItemID: workItemID, ProjectID: projectID, FamilyID: card.FamilyID, Title: card.Title,
		ParentWorkItemID: card.ParentWorkItemID, IsRoot: card.IsRoot, WorkspaceSetID: workspaceSetID,
		Status: card.Status, ActiveRunID: card.ActiveRunID, ActiveRunStatus: card.ActiveRunStatus,
		BlockerCount: card.BlockerCount, TopBlockerType: card.TopBlockerType,
		PendingScopeExpansionCount: card.PendingScopeExpansionCount, RepositoryBadges: badges,
	}
}

// resolveProjectionState returns the active generation for
// (projectID, projection.ProjectionName) with its Freshness. See Freshness for
// the two early-lifecycle cases reported as STALE rather than as errors.
func resolveProjectionState(ctx context.Context, tx ports.Tx, projectID string) (uint64, Freshness, error) {
	generation, ok, err := tx.Projections().GetActiveGeneration(ctx, projectID, projection.ProjectionName)
	if err != nil {
		return 0, Freshness{}, err
	}
	if !ok {
		return 0, Freshness{Generation: 0, AsOfJournalPosition: 0, Status: string(ports.ProjectionStale)}, nil
	}
	checkpoint, err := tx.Projections().GetProjectionCheckpoint(ctx, projectID, projection.ProjectionName, generation)
	if err != nil {
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			return generation, Freshness{Generation: generation, AsOfJournalPosition: 0, Status: string(ports.ProjectionStale)}, nil
		}
		return 0, Freshness{}, err
	}
	return generation, Freshness{
		Generation: generation, AsOfJournalPosition: checkpoint.Cursor, Status: string(checkpoint.Status),
	}, nil
}
