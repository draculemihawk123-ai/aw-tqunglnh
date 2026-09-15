package rundetail

import (
	"errors"
	"net/http"
	"strings"

	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// timelineQueryFingerprint mirrors graphQueryFingerprint (graph.go) — just
// RunID, no filter/sort parameter this route accepts.
type timelineQueryFingerprint struct {
	RunID string `json:"runId"`
}

// timelineSubOrder is one entry's own position within its owning NodeRun's
// own local sub-sequence: 0 for the NODE_RUN entry itself, AttemptNumber
// for each EXECUTION_ATTEMPT entry (AttemptNumber starts at 1, so a
// NodeRun's own entry always sorts first among its own children).
func timelineSubOrder(e runtimeapp.TimelineEntryView) uint32 {
	if e.Kind == runtimeapp.TimelineEntryExecutionAttempt {
		return e.AttemptNumber
	}
	return 0
}

// runTimelineResponse is GET /runs/{id}/timeline's own wire shape: one
// bounded page of runtimeapp.GetRunTimeline's own already-ordered,
// already-flattened Entries.
type runTimelineResponse struct {
	RunID      string                         `json:"runId"`
	Entries    []runtimeapp.TimelineEntryView `json:"entries"`
	Freshness  httpapi.Freshness              `json:"freshness"`
	NextCursor string                         `json:"nextCursor,omitempty"`
}

// handleGetRunTimeline implements GET /runs/{id}/timeline (operationId
// getRunTimeline): runtimeapp.GetRunTimeline's own full, already-ordered
// entry list, sliced into one bounded page here — the identical
// fingerprint/bind/candidate-filter/next-cursor shape handleGetRunGraph
// uses, keyed by the 3-part (ActivationSequence, NodeRunID, subOrder) total
// order cursorkeys.go's own encodeTimelineKey/afterTimelineKey establish
// instead of the 2-part activation key graph.go uses — a timeline entry
// needs the extra subOrder component to stay correctly ordered against its
// own sibling entries (one NodeRun's own NODE_RUN entry plus its own
// EXECUTION_ATTEMPT entries), which graph.go's own Activations-only page
// never has to represent.
func handleGetRunTimeline(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		runID := r.PathValue("id")
		if strings.TrimSpace(runID) == "" {
			writeValidationError(w, "id", "is required")
			return
		}
		limit, err := httpapi.ResolveLimit(r.URL.Query().Get("limit"))
		if err != nil {
			writeValidationError(w, "limit", err.Error())
			return
		}

		timeline, err := runtimeapp.GetRunTimeline(ctx, deps.UnitOfWork, deps.Matcher, runID)
		if err != nil {
			writeQueryError(w, err)
			return
		}

		fingerprint, err := httpapi.Fingerprint(timelineQueryFingerprint{RunID: runID})
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
			return
		}

		var afterSequence uint64
		var afterNodeRunID string
		var afterSubOrder uint32
		var haveAfter bool
		var upperWatermark int64
		if rawCursor := r.URL.Query().Get("cursor"); rawCursor != "" {
			state, decodeErr := deps.Cursor.Decode(rawCursor)
			if decodeErr != nil {
				httpapi.WriteCursorInvalid(w)
				return
			}
			want := httpapi.CursorState{QueryFingerprint: fingerprint, Generation: 0}
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
				seq, id, sub, ok := decodeTimelineKey(state.LastKey)
				if !ok {
					httpapi.WriteCursorInvalid(w)
					return
				}
				afterSequence, afterNodeRunID, afterSubOrder, haveAfter = seq, id, sub, true
			}
		} else {
			// First page of a fresh walk: pin UpperWatermark to the
			// greatest ActivationSequence visible right now — identical
			// stability guarantee to graph.go's own first-page branch,
			// applied to timeline entries instead of activations.
			for _, e := range timeline.Entries {
				if int64(e.ActivationSequence) > upperWatermark {
					upperWatermark = int64(e.ActivationSequence)
				}
			}
		}

		candidates := make([]runtimeapp.TimelineEntryView, 0, len(timeline.Entries))
		for _, e := range timeline.Entries {
			if int64(e.ActivationSequence) > upperWatermark {
				continue
			}
			if haveAfter && !afterTimelineKey(e.ActivationSequence, e.NodeRunID, timelineSubOrder(e), afterSequence, afterNodeRunID, afterSubOrder) {
				continue
			}
			candidates = append(candidates, e)
		}

		pageCount := limit
		if pageCount > len(candidates) {
			pageCount = len(candidates)
		}
		page := candidates[:pageCount]

		response := runTimelineResponse{
			RunID: timeline.RunID, Entries: page,
			Freshness: httpapi.Freshness{Generation: 0, AsOfJournalPosition: upperWatermark, Status: httpapi.FreshnessLive},
		}
		if len(candidates) > pageCount {
			last := page[len(page)-1]
			nextToken, encodeErr := deps.Cursor.Encode(httpapi.CursorState{
				QueryFingerprint: fingerprint, Generation: 0, UpperWatermark: upperWatermark,
				LastKey: encodeTimelineKey(last.ActivationSequence, last.NodeRunID, timelineSubOrder(last)),
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
