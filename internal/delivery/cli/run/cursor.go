package run

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// ErrCursorInvalid is returned by `run graph`/`run timeline` for a --cursor
// value that cannot be decoded at all (bad base64/JSON) — the local
// counterpart of httpapi.ErrCursorInvalid (see localCursor's own doc
// comment for why this package does not reuse that type directly).
var ErrCursorInvalid = errors.New("cli: cursor is malformed")

// ErrCursorRunMismatch is returned when a decoded cursor's own RunID does
// not match the <runId> this invocation actually named — the local
// counterpart of httpapi's own ResyncReasonQueryChanged/ProjectMismatch:
// RunID is the one and only "query" dimension either paging subcommand
// accepts (no filter/sort flag of their own), so this is the one resync
// condition that can ever apply here.
var ErrCursorRunMismatch = errors.New("cli: cursor was issued for a different run; resync by omitting --cursor")

// localCursor is `run graph`/`run timeline`'s own opaque pagination-cursor
// payload — deliberately NOT httpapi.CursorCodec
// (internal/delivery/httpapi/cursor.go), even though that codec's Encode/
// Decode/Fingerprint/Bind machinery is exactly the shape this package's own
// paging otherwise mirrors line for line (see graph.go/timeline.go). Two
// reasons, confirmed by reading NewCursorCodec's own doc comment before
// making this choice:
//
//  1. httpapi.NewCursorCodec's own doc comment requires "a composition
//     root mints one per process... never a fixed compiled-in value" — that
//     is correct for a long-lived `aw serve` process, but unworkable for a
//     one-shot CLI invocation: an operator who pastes a --cursor token from
//     one `aw run graph <id>` invocation into a LATER, separate process
//     could never have that token verified against a secret the later
//     process never had (each CLI invocation is a fresh OS process with no
//     shared state). A CLI-local codec must therefore be either a fixed
//     compiled-in secret (exactly what that constructor's own doc comment
//     forbids) or genuinely unsigned.
//  2. There is no multi-tenant confidentiality boundary here to defend
//     with a signature in the first place: httpapi.CursorCodec's whole
//     point (that type's own doc comment) is stopping an HTTP client from
//     hand-editing a token to see another project's rows or replay a
//     stale generation — a local CLI operator already has full local read
//     access to whatever this cursor could ever encode (a RunID and a
//     paging position), so there is nothing a signature would actually
//     protect here.
//
// This cursor is therefore a plain, unsigned, base64-encoded JSON envelope
// bound to RunID alone — malformed or mismatched input is still rejected
// (ErrCursorInvalid/ErrCursorRunMismatch mirror httpapi.ErrCursorInvalid/
// ResyncError's own distinct-failure-mode split), it is just never
// HMAC-verified.
type localCursor struct {
	RunID          string `json:"runId"`
	UpperWatermark int64  `json:"upperWatermark"`
	LastKey        string `json:"lastKey"`
}

func encodeCursor(c localCursor) string {
	raw, err := json.Marshal(c)
	if err != nil {
		// c's own fields are all plain strings/ints — json.Marshal cannot
		// fail on this shape; this exists only so encodeCursor never needs
		// an error return its only two call sites (graph.go/timeline.go)
		// would otherwise have to thread through a "this cannot happen"
		// branch.
		panic("cli: run: marshal cursor: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeCursor(token, runID string) (localCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return localCursor{}, ErrCursorInvalid
	}
	var c localCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return localCursor{}, ErrCursorInvalid
	}
	if c.RunID != runID {
		return localCursor{}, ErrCursorRunMismatch
	}
	return c, nil
}

// encodeActivationKey/decodeActivationKey/afterActivation mirror
// internal/delivery/httpapi/rundetail's own cursorkeys.go
// (encodeActivationKey/decodeActivationKey/afterActivation) exactly — the
// identical "<activationSequence>:<nodeRunId>" composite total-order key
// `run graph` needs for the identical reason: ActivationSequence alone is
// not unique across NodeRuns (a real, expected fork/join tie —
// run_detail_queries.go's own sortedNodeRuns doc comment).
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

func afterActivation(sequence uint64, nodeRunID string, afterSequence uint64, afterNodeRunID string) bool {
	if sequence != afterSequence {
		return sequence > afterSequence
	}
	return nodeRunID > afterNodeRunID
}

// encodeTimelineKey/decodeTimelineKey/afterTimelineKey mirror
// internal/delivery/httpapi/rundetail's own cursorkeys.go identical
// functions, extended with the third subOrder component `run timeline`
// needs (see timeline.go's own doc comment).
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

func afterTimelineKey(sequence uint64, nodeRunID string, subOrder uint32, afterSequence uint64, afterNodeRunID string, afterSubOrder uint32) bool {
	if sequence != afterSequence {
		return sequence > afterSequence
	}
	if nodeRunID != afterNodeRunID {
		return nodeRunID > afterNodeRunID
	}
	return subOrder > afterSubOrder
}
