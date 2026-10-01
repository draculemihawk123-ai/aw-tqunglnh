package kanban

import (
	"errors"
	"net/http"
	"strings"

	kanbanapp "github.com/taQuangLing/agent-workflow/internal/app/kanban"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// handleListKanban implements GET /projects/{projectId}/work-items/kanban
// (operationId listWorkItemKanban): the project's own bounded, filtered,
// stably-paginated Kanban card list — see routes.go's own doc comment for the
// full package-level contract.
//
// The query itself — filtering, keyset pagination bounded by a pinned journal
// watermark, badge aggregation, freshness — is internal/app/kanban's
// ListWorkItemKanban (its own doc comment has the mechanics), the same
// operation `aw work-item kanban` runs. This handler only owns what is HTTP:
// the limit/filter query parameters, and the signed opaque cursor. The cursor
// is checked in the same order httpapi.Bind documents (project, then query,
// then generation); the first two are decided here, the generation — which
// can only be known inside the read — by the application operation, which
// reports kanbanapp.ErrGenerationChanged.
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

		filter := kanbanapp.NormalizeFilter(r.URL.Query()["status"], r.URL.Query().Get("familyId"))
		fingerprint, err := httpapi.Fingerprint(filter)
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
			return
		}

		var resume *kanbanapp.ResumePoint
		if rawCursor := r.URL.Query().Get("cursor"); rawCursor != "" {
			state, decodeErr := deps.Cursor.Decode(rawCursor)
			if decodeErr != nil {
				httpapi.WriteCursorInvalid(w)
				return
			}
			// Project and query only: the cursor's own generation is passed
			// through as the want, because the active generation is read inside
			// the application operation, which checks it.
			want := httpapi.CursorState{ProjectID: projectID, QueryFingerprint: fingerprint, Generation: state.Generation}
			if bindErr := httpapi.Bind(state, want); bindErr != nil {
				var resyncErr *httpapi.ResyncError
				if errors.As(bindErr, &resyncErr) {
					httpapi.WriteResyncRequired(w, resyncErr.Reason)
					return
				}
				writeQueryError(w, bindErr)
				return
			}
			resume = &kanbanapp.ResumePoint{
				Generation: uint64(state.Generation), UpperWatermark: state.UpperWatermark, LastKey: state.LastKey,
			}
		}

		page, err := kanbanapp.ListWorkItemKanban(ctx, deps.UnitOfWork, kanbanapp.ListRequest{
			ProjectID: projectID, Filter: filter, Limit: limit, Resume: resume,
		})
		if err != nil {
			if errors.Is(err, kanbanapp.ErrGenerationChanged) {
				httpapi.WriteResyncRequired(w, httpapi.ResyncReasonGenerationChanged)
				return
			}
			writeQueryError(w, err)
			return
		}

		response := kanbanListResponse{Items: cardsToDTOs(page.Items), Freshness: freshnessToHTTP(page.Freshness)}
		if page.Next != nil {
			nextToken, encodeErr := deps.Cursor.Encode(httpapi.CursorState{
				ProjectID: projectID, QueryFingerprint: fingerprint, Generation: int(page.Next.Generation),
				UpperWatermark: page.Next.UpperWatermark, LastKey: page.Next.LastKey,
			})
			if encodeErr != nil {
				writeQueryError(w, encodeErr)
				return
			}
			response.NextCursor = nextToken
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, response, "")
	}
}
