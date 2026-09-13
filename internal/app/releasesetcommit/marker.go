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
