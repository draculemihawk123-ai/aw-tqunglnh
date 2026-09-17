package kanban

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// kanbanQueryFilter is the small struct httpapi.Fingerprint hashes into
// every listWorkItemKanban cursor's own QueryFingerprint — cursor.go's own
// "pass a struct, not a map" contract. normalizeKanbanQueryFilter
// canonicalizes it (trimmed/upper-cased/deduped/sorted Statuses) BEFORE
// fingerprinting, so two requests naming the identical filter set in a
// different query-string order (or letter case) still fingerprint
// identically — an equivalence Fingerprint's own struct-field-order
// determinism alone would not give for a caller-ordered query string.
type kanbanQueryFilter struct {
	Statuses []string `json:"statuses"`
	FamilyID string   `json:"familyId"`
}

func normalizeKanbanQueryFilter(rawStatuses []string, rawFamilyID string) kanbanQueryFilter {
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
	return kanbanQueryFilter{Statuses: statuses, FamilyID: strings.TrimSpace(rawFamilyID)}
}

func (f kanbanQueryFilter) matches(card projection.WorkItemCardRow) bool {
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

// decodedCard bundles one ports.ProjectionRow together with its own decoded
// projection.WorkItemCardRow payload — kept side by side rather than
// re-decoding, since this handler needs both the row's own wrapper fields
// (EntityKey, LastAppliedJournalPosition) and the decoded payload's own
// fields (Status, FamilyID, IsRoot, WorkspaceSetID, ...) throughout.
type decodedCard struct {
	row  ports.ProjectionRow
	card projection.WorkItemCardRow
}

// handleListKanban implements GET /projects/{projectId}/work-items/kanban
// (operationId listWorkItemKanban): the project's own bounded, filtered,
// stably-paginated Kanban card list — see routes.go's own doc comment for
// the full package-level contract.
//
// # Keyset pagination mechanics
//
// Rows are already EntityKey (WorkItemID) ascending
// (ports.ProjectionRepository.ListProjectionRows' own documented order,
// re-sorted here defensively — the identical "in case that contract ever
// drifts" idiom internal/delivery/httpapi/message's own handleListMessages
// already uses for its analogous Sequence order). A page resumes strictly
// after CursorState.LastKey (the last WorkItemID the previous page
// returned).
//
// Unlike handleListMessages' own numeric Sequence (which conveniently
// serves as BOTH the sort key and a monotonic write-time watermark in one
// field), a WorkItemID is a random UUID (idsource.Random) with no relation
// to creation or update order — sorting by it alone could let a WorkItem
// created (or projected) AFTER this walk began land, by pure lexicographic
// luck, into a page this walk has not reached yet. This handler instead
// bounds CursorState.UpperWatermark against each row's own
// LastAppliedJournalPosition (ports.ProjectionRow's own wrapper field, the
// real "Cursor is greatest scanned global JournalPosition" quantity V6-08
// already tracks per row): the first page of a walk pins UpperWatermark to
// the greatest LastAppliedJournalPosition observed across every row this
// project's own generation currently has, and every later page of the SAME
// walk stays bounded by that same pinned value — a row that is CREATED, or
// whose Status/badges CHANGE (either one bumps its own
// LastAppliedJournalPosition), after the walk began is excluded from this
// walk entirely, exactly like message.go's own "stable paging across a
// concurrent write" guarantee, adapted from a numeric sort key to an
// unordered one.
func handleListKanban(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		projectID := r.PathValue("projectId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}

		limit, err := httpapi.ResolveLimit(r.URL.Query().Get("limit"))
		if err != nil {
			writeValidationError(w, "limit", err.Error())
			return
		}

		filter := normalizeKanbanQueryFilter(r.URL.Query()["status"], r.URL.Query().Get("familyId"))
		fingerprint, err := httpapi.Fingerprint(filter)
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
			return
		}

		var response kanbanListResponse
		err = deps.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
			generation, freshness, stateErr := resolveProjectionState(ctx, tx, projectID)
			if stateErr != nil {
				return stateErr
			}
			response.Freshness = freshness

			rawRows, listErr := tx.Projections().ListProjectionRows(ctx, projectID, projection.ProjectionName, generation)
			if listErr != nil {
				return listErr
			}
			sort.Slice(rawRows, func(i, j int) bool { return rawRows[i].EntityKey < rawRows[j].EntityKey })

			all := make([]decodedCard, 0, len(rawRows))
			rootByFamily := map[string]projection.WorkItemCardRow{}
			var upperWatermark int64
			for _, row := range rawRows {
				card, decodeErr := decodeCardRow(row)
				if decodeErr != nil {
					return decodeErr
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
			if rawCursor := r.URL.Query().Get("cursor"); rawCursor != "" {
				state, decodeErr := deps.Cursor.Decode(rawCursor)
				if decodeErr != nil {
					return httpapi.ErrCursorInvalid
				}
				want := httpapi.CursorState{ProjectID: projectID, QueryFingerprint: fingerprint, Generation: int(generation)}
				if bindErr := httpapi.Bind(state, want); bindErr != nil {
					return bindErr
				}
				afterKey = state.LastKey
				boundWatermark = state.UpperWatermark
			}

			candidates := make([]decodedCard, 0, len(all))
			for _, entry := range all {
				if entry.row.EntityKey <= afterKey {
					continue
				}
				if int64(entry.row.LastAppliedJournalPosition) > boundWatermark {
					continue
				}
				if !filter.matches(entry.card) {
					continue
				}
				candidates = append(candidates, entry)
			}

			pageCount := limit
			if pageCount > len(candidates) {
				pageCount = len(candidates)
			}
			page := candidates[:pageCount]

			badges := newBadgeLookup(tx)
			items := make([]KanbanCardDTO, 0, len(page))
			for _, entry := range page {
				workspaceSetID := resolveWorkspaceSetID(entry.card, rootByFamily)
				cardBadges, badgeErr := badges.Badges(ctx, workspaceSetID)
				if badgeErr != nil {
					return badgeErr
				}
				items = append(items, cardToDTO(entry.row.EntityKey, projectID, entry.card, workspaceSetID, cardBadges))
			}
			response.Items = items

			if len(candidates) > pageCount {
				nextToken, encodeErr := deps.Cursor.Encode(httpapi.CursorState{
					ProjectID: projectID, QueryFingerprint: fingerprint, Generation: int(generation),
					UpperWatermark: boundWatermark, LastKey: page[len(page)-1].row.EntityKey,
				})
				if encodeErr != nil {
					return encodeErr
				}
				response.NextCursor = nextToken
			}
			return nil
		})
		if err != nil {
			if errors.Is(err, httpapi.ErrCursorInvalid) {
				httpapi.WriteCursorInvalid(w)
				return
			}
			var resyncErr *httpapi.ResyncError
			if errors.As(err, &resyncErr) {
				httpapi.WriteResyncRequired(w, resyncErr.Reason)
				return
			}
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, response, "")
	}
}

// resolveWorkspaceSetID returns card's own effective WorkspaceSetID: its
// own value directly when card IS the root (WorkItemCardRow.WorkspaceSetID
// is populated for a root row already), otherwise the cross-referenced root
// row's own value for the SAME FamilyID (row.go's own documented scope
// boundary: a child row never carries this field itself) — empty when no
// root row for that family exists in this generation's own row set at all
// (an honest "not resolvable yet" rather than a guess).
func resolveWorkspaceSetID(card projection.WorkItemCardRow, rootByFamily map[string]projection.WorkItemCardRow) string {
	if card.IsRoot {
		return card.WorkspaceSetID
	}
	return rootByFamily[card.FamilyID].WorkspaceSetID
}
