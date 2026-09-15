package rundetail

import (
	"errors"
	"net/http"
	"strings"

	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// graphQueryFingerprint is the one value httpapi.Fingerprint hashes for
// this route's own cursor — just RunID, since GET /runs/{id}/graph accepts
// no filter/sort query parameter of its own (unlike, say, ListEvidence's
// own runId/kind filters). A cursor decoded for a different RunID than the
// one THIS request's own path names produces a QUERY_CHANGED resync,
// exactly the protection ProjectID would otherwise provide on a
// project-scoped route — see rundetail.go's own package doc comment for
// why CursorState.ProjectID is deliberately left blank on every route in
// this package.
type graphQueryFingerprint struct {
	RunID string `json:"runId"`
}

// takenEdgeView is one compiled edge a real activation's own SelectedOutcome
// actually resolved to — derived HERE, from graph.PossibleEdges plus the
// CURRENT PAGE of Activations, never inside internal/app/runtime
// (run_detail_queries.go's own package doc comment: "which activations
// belong to 'the current page' is a pagination concern that file
// deliberately knows nothing about"). EdgeKey/ToNodeKey are empty when no
// compiled FLOW edge matches (should not happen for a real, validated
// WorkflowVersion, but this task's own contract never assumes a domain
// invariant client-side rather than degrading gracefully).
type takenEdgeView struct {
	EdgeKey            string `json:"edgeKey,omitempty"`
	FromNodeRunID      string `json:"fromNodeRunId"`
	FromNodeKey        string `json:"fromNodeKey"`
	Outcome            string `json:"outcome"`
	ToNodeKey          string `json:"toNodeKey,omitempty"`
	ActivationSequence uint64 `json:"activationSequence"`
}

// deriveTakenEdges builds one takenEdgeView per activation in page whose
// own SelectedOutcome is non-blank (a FORK's own NodeRun, and every still-
// PENDING/QUEUED/RUNNING activation, never has one — this task's own
// "actually taken" scope, distinct from PossibleEdges' "could be taken").
func deriveTakenEdges(edges []runtimeapp.GraphEdgeView, page []runtimeapp.NodeActivationView) []takenEdgeView {
	var out []takenEdgeView
	for _, a := range page {
		if a.SelectedOutcome == "" {
			continue
		}
		view := takenEdgeView{
			FromNodeRunID: a.NodeRunID, FromNodeKey: a.NodeKey, Outcome: a.SelectedOutcome, ActivationSequence: a.ActivationSequence,
		}
		for _, e := range edges {
			if e.From == a.NodeKey && e.Outcome == a.SelectedOutcome && e.Kind != "COMPLETION_REWORK" {
				view.EdgeKey, view.ToNodeKey = e.Key, e.To
				break
			}
		}
		out = append(out, view)
	}
	return out
}

// runGraphResponse is GET /runs/{id}/graph's own wire shape: the compiled
// structural graph (Nodes/PossibleEdges — always the FULL set, never
// paginated) plus one bounded PAGE of real Activations, TakenEdges derived
// from exactly that page, and the full BranchTokens set (small by
// construction — see run_detail_queries.go's own BranchTokenView doc
// comment).
type runGraphResponse struct {
	RunID            string                          `json:"runId"`
	ManifestRevision uint64                          `json:"manifestRevision"`
	Nodes            []runtimeapp.GraphNodeView      `json:"nodes"`
	PossibleEdges    []runtimeapp.GraphEdgeView      `json:"possibleEdges"`
	TakenEdges       []takenEdgeView                 `json:"takenEdges,omitempty"`
	Activations      []runtimeapp.NodeActivationView `json:"activations"`
	BranchTokens     []runtimeapp.BranchTokenView    `json:"branchTokens,omitempty"`
	Freshness        httpapi.Freshness               `json:"freshness"`
	NextCursor       string                          `json:"nextCursor,omitempty"`
}

// handleGetRunGraph implements GET /runs/{id}/graph (operationId
// getRunGraph): runtimeapp.GetRunGraph's own full result, sliced into one
// bounded page here — see rundetail.go's own package doc comment for why
// pagination/freshness live entirely in this package, mirroring
// internal/delivery/httpapi/message's own handleListMessages exactly
// (fingerprint/bind/candidate-filter/next-cursor shape identical, applied
// to NodeActivationView keyed by (ActivationSequence, NodeRunID) instead of
// Message keyed by Sequence alone).
func handleGetRunGraph(deps Dependencies) http.HandlerFunc {
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

		graph, err := runtimeapp.GetRunGraph(ctx, deps.UnitOfWork, deps.Matcher, runID)
		if err != nil {
			writeQueryError(w, err)
			return
		}

		fingerprint, err := httpapi.Fingerprint(graphQueryFingerprint{RunID: runID})
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
			return
		}

		var afterSequence uint64
		var afterNodeRunID string
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
				seq, id, ok := decodeActivationKey(state.LastKey)
				if !ok {
					httpapi.WriteCursorInvalid(w)
					return
				}
				afterSequence, afterNodeRunID, haveAfter = seq, id, true
			}
		} else {
			// First page of a fresh walk: pin UpperWatermark to the
			// greatest ActivationSequence visible right now — every later
			// page of THIS SAME walk stays bounded by that same value, so a
			// NodeRun created after the walk began never appears partway
			// through it (cursor.go's own CursorState.UpperWatermark doc
			// comment; see graph_test.go's own stable-paging-across-a-
			// concurrent-write case for exactly what this prevents).
			for _, a := range graph.Activations {
				if int64(a.ActivationSequence) > upperWatermark {
					upperWatermark = int64(a.ActivationSequence)
				}
			}
		}

		candidates := make([]runtimeapp.NodeActivationView, 0, len(graph.Activations))
		for _, a := range graph.Activations {
			if int64(a.ActivationSequence) > upperWatermark {
				continue
			}
			if haveAfter && !afterActivation(a.ActivationSequence, a.NodeRunID, afterSequence, afterNodeRunID) {
				continue
			}
			candidates = append(candidates, a)
		}

		pageCount := limit
		if pageCount > len(candidates) {
			pageCount = len(candidates)
		}
		page := candidates[:pageCount]

		response := runGraphResponse{
			RunID: graph.RunID, ManifestRevision: graph.ManifestRevision,
			Nodes: graph.Nodes, PossibleEdges: graph.PossibleEdges, TakenEdges: deriveTakenEdges(graph.PossibleEdges, page),
			Activations: page, BranchTokens: graph.BranchTokens,
			Freshness: httpapi.Freshness{Generation: 0, AsOfJournalPosition: upperWatermark, Status: httpapi.FreshnessLive},
		}
		if len(candidates) > pageCount {
			nextToken, encodeErr := deps.Cursor.Encode(httpapi.CursorState{
				QueryFingerprint: fingerprint, Generation: 0, UpperWatermark: upperWatermark,
				LastKey: encodeActivationKey(page[len(page)-1].ActivationSequence, page[len(page)-1].NodeRunID),
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
