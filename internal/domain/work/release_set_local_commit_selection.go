package work

import (
	"errors"
	"strings"
	"time"
)

// LocalCommitFile is one file a selective local commit (V11-07) covers, with what the
// approver saw of it: Status (ADDED/MODIFIED/DELETED/TYPE_CHANGED) and the sha256 of its
// content. Both may be empty for a manual commit whose caller saw no hashes; the commit is
// then not drift-checked for that file.
type LocalCommitFile struct {
	Path        string `json:"path"`
	Status      string `json:"status,omitempty"`
	ContentHash string `json:"contentHash,omitempty"`
}

// UnselectedHandling is what happens to the worktree's other changes after a selective
// commit. KEEP leaves them uncommitted; PARK moves them into a hidden ref.
type UnselectedHandling string

const (
	UnselectedKeep UnselectedHandling = "KEEP"
	UnselectedPark UnselectedHandling = "PARK"
)

// IsValid reports whether h is one of the two closed values.
func (h UnselectedHandling) IsValid() bool { return h == UnselectedKeep || h == UnselectedPark }

// ReleaseSetLocalCommitSelection is the selection record of one ReleaseSetLocalCommit
// operation that commits only some of the worktree's changes. An operation without one
// commits the whole worktree, as before V11-07.
type ReleaseSetLocalCommitSelection struct {
	ReleaseSetLocalCommitID ReleaseSetLocalCommitID
	// ApprovalRequestID/WorkItemID/RunID are set when a decision created the operation;
	// empty for a manual selective commit. WorkItemID is where RELEASE_FAILED opens.
	ApprovalRequestID string
	WorkItemID        string
	RunID             string
	Files             []LocalCommitFile
	// ChangeSetDigest is sha256 over the selected files' status, path and hash.
	ChangeSetDigest string
	Unselected      UnselectedHandling
	// ParkedRef/ParkedPaths are set after the leftover changes were parked.
	ParkedRef   string
	ParkedPaths []string
	// Patch is the bounded unified diff of the selected files as the approver saw it.
	Patch          []byte
	PatchTruncated bool
	// RestoredAt is set when the parked files were put back into the worktree.
	RestoredAt *time.Time
	CreatedAt  time.Time
}

// NewReleaseSetLocalCommitSelection validates and builds a selection record.
func NewReleaseSetLocalCommitSelection(
	id ReleaseSetLocalCommitID, approvalRequestID, workItemID, runID string, files []LocalCommitFile,
	digest string, unselected UnselectedHandling, patch []byte, patchTruncated bool, createdAt time.Time,
) (ReleaseSetLocalCommitSelection, error) {
	if id == "" {
		return ReleaseSetLocalCommitSelection{}, errors.New("release set local commit selection needs the operation id")
	}
	if len(files) == 0 {
		return ReleaseSetLocalCommitSelection{}, errors.New("release set local commit selection needs at least one file")
	}
	if !unselected.IsValid() {
		return ReleaseSetLocalCommitSelection{}, errors.New("release set local commit selection: unselected handling must be KEEP or PARK")
	}
	if strings.TrimSpace(digest) == "" {
		return ReleaseSetLocalCommitSelection{}, errors.New("release set local commit selection needs the change set digest")
	}
	if createdAt.IsZero() {
		return ReleaseSetLocalCommitSelection{}, errors.New("release set local commit selection needs a created timestamp")
	}
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if strings.TrimSpace(file.Path) == "" {
			return ReleaseSetLocalCommitSelection{}, errors.New("release set local commit selection has a file with no path")
		}
		if _, dup := seen[file.Path]; dup {
			return ReleaseSetLocalCommitSelection{}, errors.New("release set local commit selection lists a path twice")
		}
		seen[file.Path] = struct{}{}
	}
	return ReleaseSetLocalCommitSelection{
		ReleaseSetLocalCommitID: id, ApprovalRequestID: approvalRequestID, WorkItemID: workItemID, RunID: runID,
		Files: append([]LocalCommitFile(nil), files...), ChangeSetDigest: digest, Unselected: unselected,
		Patch: append([]byte(nil), patch...), PatchTruncated: patchTruncated, CreatedAt: createdAt.UTC(),
	}, nil
}

// ParkedChange names a parked set of a family's repository that has not been restored yet.
type ParkedChange struct {
	ReleaseSetLocalCommitID ReleaseSetLocalCommitID
	ReleaseSetID            ReleaseSetID
	RepositoryWorkspaceID   string
	RepositoryID            string
	Ref                     string
	Paths                   []string
	ParkedAt                time.Time
}
