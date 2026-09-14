package message

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	messagedomain "github.com/taQuangLing/agent-workflow/internal/domain/message"
)

// handleListMessages implements
// GET /projects/{projectId}/work-items/{workItemId}/messages (operationId
// listMessages): workItemId's own full, canonical task chat
// (appmessage.ListMessages), bounded into pages via a real opaque
// httpapi.CursorCodec cursor — this task's own "Phạm vi: ... return bounded
// canonical references and pagination" line, using the shared
// cursor.go/page.go helpers from V6-02A (the same convention V6-08 onward
// projected lists are expected to also use).
//
// appmessage.ListMessages is not itself paginated at the storage layer —
// it returns the full, already-ordered (by Sequence) slice for one
// WorkItem, matching internal/app/message/commands.go's own doc comment
// framing of task chat as "bounded, typed content" (unlike V5-05's raw
// process output, which needs true streaming). This handler slices that
// already-bounded, already-sorted slice in memory rather than adding a new
// SQL query — a deliberate, documented scope choice, not a hidden
// shortcut: nothing in this task's own citations asks for a bounded SQL
// LIMIT/OFFSET query, and every real conversation this system produces is
// small enough (MaxContentSize-bounded messages, one human task at a time)
// for an in-memory slice to stay cheap.
//
// The WorkItem named by {workItemId} is reloaded via workapp.GetWorkItem
// FIRST, unconditionally — the identical scope-verification discipline
// append.go's own handleAppendMessage already establishes.
func handleListMessages(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		projectID := r.PathValue("projectId")
		workItemID := r.PathValue("workItemId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(workItemID) == "" {
			writeValidationError(w, "workItemId", "is required")
			return
		}
		scope := ports.ProjectScope(projectID)
		if _, err := workapp.GetWorkItem(ctx, deps.UnitOfWork, scope, workItemID); err != nil {
			writeQueryError(w, err)
			return
		}

		limit, err := httpapi.ResolveLimit(r.URL.Query().Get("limit"))
		if err != nil {
			writeValidationError(w, "limit", err.Error())
			return
		}

		all, err := appmessage.ListMessages(ctx, deps.UnitOfWork, workItemID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		// ports.MessageRepository.ListMessagesForWorkItem's own doc comment
		// already promises Sequence-ascending order — re-sorting here is
		// defense-in-depth against that contract ever silently drifting,
		// not a behavior change, and costs nothing on an already-sorted
		// slice.
		sort.Slice(all, func(i, j int) bool { return all[i].Sequence < all[j].Sequence })

		fingerprint, err := httpapi.Fingerprint(listQueryFingerprint{WorkItemID: workItemID})
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
			return
		}

		var afterSequence uint64
		var upperWatermark int64
		if rawCursor := r.URL.Query().Get("cursor"); rawCursor != "" {
			state, decodeErr := deps.Cursor.Decode(rawCursor)
			if decodeErr != nil {
				httpapi.WriteCursorInvalid(w)
				return
			}
			want := httpapi.CursorState{ProjectID: projectID, QueryFingerprint: fingerprint, Generation: 0}
			if bindErr := httpapi.Bind(state, want); bindErr != nil {
				var resyncErr *httpapi.ResyncError
				if errors.As(bindErr, &resyncErr) {
					httpapi.WriteResyncRequired(w, resyncErr.Reason)
					return
				}
				httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
				return
			}
			upperWatermark = state.UpperWatermark
			if state.LastKey != "" {
				parsed, parseErr := strconv.ParseUint(state.LastKey, 10, 64)
				if parseErr != nil {
					httpapi.WriteCursorInvalid(w)
					return
				}
				afterSequence = parsed
			}
		} else {
			// The FIRST page of a fresh walk pins UpperWatermark to the
			// greatest Sequence visible right now (cursor.go's own
			// CursorState.UpperWatermark doc comment) — every later page of
			// THIS SAME walk stays bounded by that same value, so a message
			// appended after the walk began never appears partway through
			// it (see list_test.go's own stable-paging-across-a-concurrent-
			// write case).
			for _, m := range all {
				if int64(m.Sequence) > upperWatermark {
					upperWatermark = int64(m.Sequence)
				}
			}
		}

		candidates := make([]messagedomain.Message, 0, len(all))
		for _, m := range all {
			if m.Sequence > afterSequence && int64(m.Sequence) <= upperWatermark {
				candidates = append(candidates, m)
			}
		}

		pageCount := limit
		if pageCount > len(candidates) {
			pageCount = len(candidates)
		}
		page := candidates[:pageCount]
		items := make([]messageRefDTO, 0, len(page))
		for _, m := range page {
			items = append(items, messageToRefDTO(m))
		}

		response := listMessagesResponse{Items: items}
		if len(candidates) > pageCount {
			nextToken, encodeErr := deps.Cursor.Encode(httpapi.CursorState{
				ProjectID: projectID, QueryFingerprint: fingerprint, Generation: 0,
				UpperWatermark: upperWatermark, LastKey: strconv.FormatUint(page[len(page)-1].Sequence, 10),
			})
			if encodeErr != nil {
				httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
				return
			}
			response.NextCursor = nextToken
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, response, "")
	}
}
