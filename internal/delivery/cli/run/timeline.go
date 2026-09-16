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
		Path: []string{"run", "timeline"}, Scope: cli.ScopeProject,
		AppOperation: "GetRunTimeline", HTTPOperationID: "getRunTimeline",
	})
}

// timelineSubOrder mirrors internal/delivery/httpapi/rundetail's own
// timelineSubOrder (timeline.go there): a timeline entry's own position
// within its owning NodeRun's own local sub-sequence — 0 for the NODE_RUN
// entry itself, AttemptNumber for each EXECUTION_ATTEMPT entry.
func timelineSubOrder(e runtime.TimelineEntryView) uint32 {
	if e.Kind == runtime.TimelineEntryExecutionAttempt {
		return e.AttemptNumber
	}
	return 0
}

// TimelineResult is `run timeline`'s own JSON result — one bounded page of
// runtime.GetRunTimeline's own already-ordered, already-flattened Entries.
type TimelineResult struct {
	RunID          string                      `json:"runId"`
	Entries        []runtime.TimelineEntryView `json:"entries"`
	UpperWatermark int64                       `json:"upperWatermark"`
	NextCursor     string                      `json:"nextCursor,omitempty"`
}

// Timeline implements `aw run timeline <runId>`: runtime.GetRunTimeline's
// own full, already-ordered entry list, paged the identical way
// internal/delivery/httpapi/rundetail.handleGetRunTimeline pages it —
// keyed by the 3-part (ActivationSequence, NodeRunID, subOrder) total order
// (cursor.go's own encodeTimelineKey/afterTimelineKey) rather than Graph's
// own 2-part key, since one NodeRun's own NODE_RUN entry plus its own
// EXECUTION_ATTEMPT entries must stay internally ordered too.
func Timeline(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run timeline", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limitFlag := fs.String("limit", "", "max entries per page (default 50, max 200)")
	cursorFlag := fs.String("cursor", "", "opaque continuation cursor from a previous page's nextCursor")
	if err := fs.Parse(args); err != nil {
		return err
	}

	runID := fs.Arg(0)
	if strings.TrimSpace(runID) == "" {
		return errors.New("cli: run timeline: <runId> argument is required")
	}
	limit, err := httpapi.ResolveLimit(*limitFlag)
	if err != nil {
		return err
	}

	timeline, err := runtime.GetRunTimeline(ctx, deps.UOW, deps.Matcher, runID)
	if err != nil {
		return err
	}

	var afterSequence uint64
	var afterNodeRunID string
	var afterSubOrder uint32
	var haveAfter bool
	var upperWatermark int64
	if raw := strings.TrimSpace(*cursorFlag); raw != "" {
		state, err := decodeCursor(raw, runID)
		if err != nil {
			return err
		}
		upperWatermark = state.UpperWatermark
		if state.LastKey != "" {
			seq, id, sub, ok := decodeTimelineKey(state.LastKey)
			if !ok {
				return ErrCursorInvalid
			}
			afterSequence, afterNodeRunID, afterSubOrder, haveAfter = seq, id, sub, true
		}
	} else {
		for _, e := range timeline.Entries {
			if int64(e.ActivationSequence) > upperWatermark {
				upperWatermark = int64(e.ActivationSequence)
			}
		}
	}

	candidates := make([]runtime.TimelineEntryView, 0, len(timeline.Entries))
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

	result := TimelineResult{RunID: timeline.RunID, Entries: page, UpperWatermark: upperWatermark}
	if len(candidates) > pageCount {
		last := page[len(page)-1]
		result.NextCursor = encodeCursor(localCursor{
			RunID: runID, UpperWatermark: upperWatermark,
			LastKey: encodeTimelineKey(last.ActivationSequence, last.NodeRunID, timelineSubOrder(last)),
		})
	}
	return cli.EncodeQueryResult(stdout, result)
}
