package run

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"run", "graph"}, Scope: cli.ScopeProject,
		AppOperation: "GetRunGraph", HTTPOperationID: "getRunGraph",
	})
}

// GraphResult is `run graph`'s own JSON result: the compiled WorkflowVersion's
// own declared nodes/edges (always the FULL set — runtime.GetRunGraph's own
// doc comment), one bounded PAGE of real Activations, and the full
// BranchTokens set — mirrors internal/delivery/httpapi/rundetail's own
// runGraphResponse shape (graph.go there), minus TakenEdges/Freshness
// (out of this task's own scope; a future task can add them without
// touching this package's own descriptor).
type GraphResult struct {
	RunID            string                       `json:"runId"`
	ManifestRevision uint64                       `json:"manifestRevision"`
	Nodes            []runtime.GraphNodeView      `json:"nodes"`
	PossibleEdges    []runtime.GraphEdgeView      `json:"possibleEdges"`
	Activations      []runtime.NodeActivationView `json:"activations"`
	BranchTokens     []runtime.BranchTokenView    `json:"branchTokens,omitempty"`
	UpperWatermark   int64                        `json:"upperWatermark"`
	NextCursor       string                       `json:"nextCursor,omitempty"`
}

// Graph implements `aw run graph <runId>`: runtime.GetRunGraph's own full
// result, paged here exactly the way
// internal/delivery/httpapi/rundetail.handleGetRunGraph pages it (same
// upperWatermark-pins-the-walk, same keyset-pagination-by-
// (ActivationSequence, NodeRunID) algorithm) — see graph_test.go for the
// stable-paging-across-a-concurrent-write proof this mirrors. --cursor
// continuation is a real, required behavior here (this task's own Verify
// bullet: "Paging... must expose cursor continuation, not silently
// truncate") — see cursor.go's own doc comment for why this package's own
// cursor format is deliberately unsigned rather than httpapi.CursorCodec.
func Graph(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run graph", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limitFlag := fs.String("limit", "", "max activations per page (default 50, max 200)")
	cursorFlag := fs.String("cursor", "", "opaque continuation cursor from a previous page's nextCursor")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runID := fs.Arg(0)
	if strings.TrimSpace(runID) == "" {
		return errors.New("cli: run graph: <runId> argument is required")
	}
	limit, err := httpapi.ResolveLimit(*limitFlag)
	if err != nil {
		return err
	}

	graph, err := runtime.GetRunGraph(ctx, deps.UOW, deps.Matcher, runID)
	if err != nil {
		return err
	}

	var afterSequence uint64
	var afterNodeRunID string
	var haveAfter bool
	var upperWatermark int64
	if raw := strings.TrimSpace(*cursorFlag); raw != "" {
		state, err := decodeCursor(raw, runID)
		if err != nil {
			return err
		}
		upperWatermark = state.UpperWatermark
		if state.LastKey != "" {
			seq, id, ok := decodeActivationKey(state.LastKey)
			if !ok {
				return ErrCursorInvalid
			}
			afterSequence, afterNodeRunID, haveAfter = seq, id, true
		}
	} else {
		// First page of a fresh walk: pin UpperWatermark to the greatest
		// ActivationSequence visible right now — every later page of THIS
		// SAME walk stays bounded by that value, so a NodeRun created
		// after the walk began never appears partway through it (mirrors
		// httpapi's own graph.go identical first-page branch).
		for _, a := range graph.Activations {
			if int64(a.ActivationSequence) > upperWatermark {
				upperWatermark = int64(a.ActivationSequence)
			}
		}
	}

	candidates := make([]runtime.NodeActivationView, 0, len(graph.Activations))
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

	result := GraphResult{
		RunID: graph.RunID, ManifestRevision: graph.ManifestRevision,
		Nodes: graph.Nodes, PossibleEdges: graph.PossibleEdges,
		Activations: page, BranchTokens: graph.BranchTokens, UpperWatermark: upperWatermark,
	}
	if len(candidates) > pageCount {
		last := page[len(page)-1]
		result.NextCursor = encodeCursor(localCursor{
			RunID: runID, UpperWatermark: upperWatermark, LastKey: encodeActivationKey(last.ActivationSequence, last.NodeRunID),
		})
	}
	return cli.EncodeQueryResult(stdout, result)
}
