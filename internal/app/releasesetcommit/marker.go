package releasesetcommit

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// hashHex sha256-hashes value and returns it "sha256:"-prefixed, hex
// encoded — the identical convention workspace.RevisionSet.ContentHash and
// work.ReleaseSet.ContentHash already establish.
func hashHex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// computeMessageHash is part of this operation's own deterministic marker
// input (V6-10E's own Thực hiện line).
func computeMessageHash(message string) string {
	return hashHex(message)
}

// computeOperationMarker builds this operation's own deterministic
// operation marker — "derived from the pinned ReleaseSet version +
// workspace generation + actor + message hash" (V6-10E's own Thực hiện
// line, unpacked). Every field that participates in the marker is exactly
// one this task's own request pins at REQUEST time (ReleaseSetID/its
// version, RepositoryWorkspaceID/its generation, Actor, MessageHash) —
// deliberately never the exact parent commit (that is resolved fresh by a
// worker, immediately before the real Git call, and would make two
// otherwise-identical requests racing a stale vs. fresh parent produce two
// different markers for what should be recognized as the same operation).
// A \x00-joined input (never a value any of these fields could itself
// contain) keeps two different field boundaries from ever colliding into
// the same hash input.
func computeOperationMarker(releaseSetID string, releaseSetVersion uint64, repositoryWorkspaceID string, generation uint64, actor, messageHash string) string {
	input := strings.Join([]string{
		releaseSetID,
		strconv.FormatUint(releaseSetVersion, 10),
		repositoryWorkspaceID,
		strconv.FormatUint(generation, 10),
		actor,
		messageHash,
	}, "\x00")
	return hashHex(input)
}

// withAttemptOrdinal (V9-09) folds a retry ordinal into base for the
// operator's "fix the worktree and request the same local commit again"
// path: attempt is how many earlier operations with the very same
// request-time pins already closed FAILED/NO_CHANGES (they provably never
// created a commit), so attempt 0 — every first request, and every request
// with no such predecessor — returns base UNCHANGED (existing markers and
// the V6-10E marker-collision guarantee are untouched), while attempt n>0
// derives a distinct, equally deterministic marker from base. A genuine
// duplicate of a still-live (REQUESTED/COMMITTED) operation therefore still
// derives the colliding marker and is rejected with
// ports.ErrLocalCommitMarkerCollision.
func withAttemptOrdinal(base string, attempt int) string {
	if attempt <= 0 {
		return base
	}
	return hashHex(base + "\x00attempt\x00" + strconv.Itoa(attempt))
}

// markerTrailer is the exact line CreateLocalCommit's own Message embeds,
// and LocalCommitMarkerReader's own real adapter searches HEAD's commit
// body for.
func markerTrailer(marker string) string {
	return ports.LocalCommitMarkerTrailerKey + ": " + marker
}

// commitMessageWithMarker appends markerTrailer(marker) to message on the
// SAME line, never as a separate trailing paragraph: gitworktree.Provider.
// CreateLocalCommit's own validCommitField rejects any message containing
// "\r" or "\n" (single-line commit messages only, this port's own
// established contract — V5-10A's own localcommit_test.go asserts exactly
// this: `{Message: "bad\nmessage", ...}` is rejected). Still trivially
// found by LocalCommitMarkerReader's own plain substring search over the
// full raw commit body, regardless of where in that single line it sits.
func commitMessageWithMarker(message, marker string) string {
	return message + " [" + markerTrailer(marker) + "]"
}
