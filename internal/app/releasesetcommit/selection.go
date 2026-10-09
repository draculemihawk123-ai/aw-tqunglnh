package releasesetcommit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// SelectionDigest is the sha256 over a selective commit's files — status, path and content hash
// of each, in path order, with NUL separators — the same construction the working-tree diff of
// V11-06 uses for its change set, so the digest the approver saw and the one stored with the
// operation are comparable. It changes iff the selection does.
func SelectionDigest(files []workdomain.LocalCommitFile) string {
	sorted := append([]workdomain.LocalCommitFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	hash := sha256.New()
	for _, file := range sorted {
		fmt.Fprintf(hash, "%s\x00%s\x00%s\x00", file.Status, file.Path, file.ContentHash)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// ParkedRef is the hidden ref the leftover changes of a PARK operation are stored under — one per
// ReleaseSet, so it is unique to the decision that created it.
func ParkedRef(releaseSetID workdomain.ReleaseSetID) string {
	return "refs/agentkit/parked/" + string(releaseSetID)
}

// portsSelection converts the stored selection into the adapter's request type.
func portsSelection(selection workdomain.ReleaseSetLocalCommitSelection) *ports.LocalCommitSelection {
	out := &ports.LocalCommitSelection{Files: make([]ports.LocalCommitFile, 0, len(selection.Files))}
	for _, file := range selection.Files {
		out.Files = append(out.Files, ports.LocalCommitFile{
			Path: file.Path, Status: ports.WorkingTreeFileStatus(file.Status), ContentHash: file.ContentHash,
		})
	}
	return out
}
