package rundetail

import (
	"strconv"
	"strings"
)

// encodeActivationKey/decodeActivationKey encode graph.go's own paging
// position as a single opaque CursorState.LastKey string:
// "<activationSequence>:<nodeRunId>" — a composite key because
// ActivationSequence alone is NOT unique across NodeRuns (a real, expected
// tie for fork/join topology — see run_detail_queries.go's own
// sortedNodeRuns doc comment); NodeRunID is the deterministic tiebreak that
// same function already sorts by.
func encodeActivationKey(sequence uint64, nodeRunID string) string {
	return strconv.FormatUint(sequence, 10) + ":" + nodeRunID
}

func decodeActivationKey(raw string) (sequence uint64, nodeRunID string, ok bool) {
	idx := strings.IndexByte(raw, ':')
	if idx < 0 {
		return 0, "", false
	}
	n, err := strconv.ParseUint(raw[:idx], 10, 64)
	if err != nil {
		return 0, "", false
	}
	return n, raw[idx+1:], true
}

// afterActivation reports whether (sequence, nodeRunID) sorts strictly
// after (afterSequence, afterNodeRunID) in the SAME total order
// run_detail_queries.go's own sortedNodeRuns already produces
// (ActivationSequence, then NodeRunID).
func afterActivation(sequence uint64, nodeRunID string, afterSequence uint64, afterNodeRunID string) bool {
	if sequence != afterSequence {
		return sequence > afterSequence
	}
	return nodeRunID > afterNodeRunID
}

// encodeTimelineKey/decodeTimelineKey are timeline.go's own sibling of the
// activation key above, extended with a THIRD component — subOrder — since
// one NodeRun's own timeline entries (the NODE_RUN entry itself, subOrder
// 0, plus one EXECUTION_ATTEMPT entry per attempt, subOrder ==
// AttemptNumber) must stay internally ordered too, on top of the
// (ActivationSequence, NodeRunID) tie the activation key alone already
// resolves. Encoded as "<activationSequence>:<nodeRunId>:<subOrder>".
func encodeTimelineKey(sequence uint64, nodeRunID string, subOrder uint32) string {
	return strconv.FormatUint(sequence, 10) + ":" + nodeRunID + ":" + strconv.FormatUint(uint64(subOrder), 10)
}

func decodeTimelineKey(raw string) (sequence uint64, nodeRunID string, subOrder uint32, ok bool) {
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 {
		return 0, "", 0, false
	}
	seq, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, "", 0, false
	}
	sub, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil {
		return 0, "", 0, false
	}
	return seq, parts[1], uint32(sub), true
}

// afterTimelineKey mirrors afterActivation, extended with the subOrder
// tiebreak.
func afterTimelineKey(sequence uint64, nodeRunID string, subOrder uint32, afterSequence uint64, afterNodeRunID string, afterSubOrder uint32) bool {
	if sequence != afterSequence {
		return sequence > afterSequence
	}
	if nodeRunID != afterNodeRunID {
		return nodeRunID > afterNodeRunID
	}
	return subOrder > afterSubOrder
}
