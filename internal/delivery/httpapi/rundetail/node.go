package rundetail

import (
	"net/http"
	"strconv"
	"strings"

	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// handleGetNodeRunDetail implements GET /node-runs/{nodeRunId} (operationId getNodeRunDetail, V11-11, ADR-042): what the node was asked
// to do, what it is doing, what came of it and, if it failed, why and what next. Read-only; the project is derived from the node run's
// own row, never from the request.
func handleGetNodeRunDetail(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeRunID := r.PathValue("nodeRunId")
		if strings.TrimSpace(nodeRunID) == "" {
			writeValidationError(w, "nodeRunId", "is required")
			return
		}
		detail, err := runtimeapp.GetNodeRunDetail(r.Context(), deps.UnitOfWork, deps.ArtifactStore, deps.Matcher, nodeRunID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, detail, "")
	}
}

// handleListAttemptEvents implements GET /execution-attempts/{attemptId}/events (operationId listAttemptEvents, V11-11, ADR-042): one
// page of the attempt's redacted event stream. cursor is the nextSequence of the previous page (absent or 0 for the first page); limit
// defaults to 100 and is capped at 500. A client follows a running attempt by asking again with the last nextSequence until the page says
// the attempt is finished and has no more.
func handleListAttemptEvents(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		attemptID := r.PathValue("attemptId")
		if strings.TrimSpace(attemptID) == "" {
			writeValidationError(w, "attemptId", "is required")
			return
		}
		var after uint64
		if raw := r.URL.Query().Get("cursor"); raw != "" {
			parsed, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				writeValidationError(w, "cursor", "must be a sequence number returned by a previous page")
				return
			}
			after = parsed
		}
		limit := 0
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 {
				writeValidationError(w, "limit", "must be a positive integer")
				return
			}
			limit = parsed
		}
		page, err := runtimeapp.ListAttemptEvents(r.Context(), deps.UnitOfWork, deps.Matcher, attemptID, after, limit)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, page, "")
	}
}
