// ReleaseSetLocalCommit (V6-10E, docs/design/08-v6-api-projections.md
// V6-10E; ADR-014, AK-ARCH-015C, GC-INV-26) is the durable "intent" record
// for one RequestReleaseSetLocalCommit operation: a caller's own request to
// materialize a ReleaseSet entry's repository as a real, local Git commit —
// never a remote-authority record (this type has no push/PR/merge field;
// AK-ARCH-015C's own "no remote operation before adapter remote được gọi"
// rejection is structural, exactly like ports.LocalCommitCreator's own
// doc comment already establishes for CreateLocalCommit itself).
//
// Like ReleaseSet (release_set.go), this is a per-operation runtime
// aggregate, not a DefinitionKind schema. Its own three-state lifecycle
// (REQUESTED -> COMMITTED or FAILED) mirrors ReleaseSetState's own
// CREATED -> {SEALED, ABANDONED} shape: REQUESTED is the only state either
// terminal state may ever transition from, and once terminal this record is
// immutable. internal/app/releasesetcommit owns every state transition —
// this package only ever constructs a REQUESTED row.
package work

import (
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// ReleaseSetLocalCommitID identifies one durable RequestReleaseSetLocalCommit
// operation record.
type ReleaseSetLocalCommitID string

// ReleaseSetLocalCommitState is this operation's own three-state lifecycle.
type ReleaseSetLocalCommitState string

const (
	ReleaseSetLocalCommitRequested ReleaseSetLocalCommitState = "REQUESTED"
	ReleaseSetLocalCommitCommitted ReleaseSetLocalCommitState = "COMMITTED"
	ReleaseSetLocalCommitFailed    ReleaseSetLocalCommitState = "FAILED"
)

// ReleaseSetLocalCommitFailureReason is this operation's own closed set of
// typed, non-retryable terminal failure reasons (V6-10E's own Verify line:
// "mismatch blocks/quarantines"). "Stale" (the Verify line's other typed-
// conflict case: "a captured generation/fence that's since changed
// correctly triggers a typed conflict, not a wrong commit") is enforced at
// REQUEST time instead — RequestReleaseSetLocalCommit's own
// ExpectedReleaseSetVersion/ExpectedWorkspaceVersion checks (commands.go)
// reject a caller whose observed version has already moved with
// ports.ErrOptimisticConflict, before any intent row is even created. A
// worker-side "generation changed since request" re-check is not a
// distinct, separately reachable failure mode this schema can produce:
// ExpectedGeneration always names the SAME RepositoryWorkspace row this
// operation's own RepositoryWorkspaceID already fixes, and that row's own
// Generation column is immutable for its own lifetime (a RECREATE
// supersedes it with an entirely new row/ID, never edits this one) — the
// one real way a worker can observe that a fixed RepositoryWorkspace row
// is no longer usable is exactly FailureWorkspaceQuarantined below (a
// RECREATE always quarantines the row it supersedes first).
type ReleaseSetLocalCommitFailureReason string

const (
	// FailureWorkspaceQuarantined: the target RepositoryWorkspace became
	// QUARANTINED after this operation was requested.
	FailureWorkspaceQuarantined ReleaseSetLocalCommitFailureReason = "WORKSPACE_QUARANTINED"
	// FailureMarkerDrift: a commit was found at the workspace's own HEAD
	// carrying this operation's own exact marker, but its parent does not
	// match the parent this operation itself pinned before ever calling
	// LocalCommitCreator — an anomaly a fresh retry must never silently
	// paper over. The workspace is quarantined when this fires (see
	// internal/app/releasesetcommit's own doc comment).
	FailureMarkerDrift ReleaseSetLocalCommitFailureReason = "MARKER_DRIFT"
)

// IsValid reports whether reason is one of this type's own closed set.
func (r ReleaseSetLocalCommitFailureReason) IsValid() bool {
	switch r {
	case FailureWorkspaceQuarantined, FailureMarkerDrift:
		return true
	default:
		return false
	}
}

// ReleaseSetLocalCommit is one durable RequestReleaseSetLocalCommit
// operation's own full record.
type ReleaseSetLocalCommit struct {
	ID                        ReleaseSetLocalCommitID
	ProjectID                 project.ProjectID
	ReleaseSetID              ReleaseSetID
	ExpectedReleaseSetVersion uint64
	// RepositoryWorkspaceID is a plain string, not
	// workspace.RepositoryWorkspaceID: internal/domain/workspace already
	// imports internal/domain/work (workspace.go's own WorkspaceSet.FamilyID
	// field), so this package cannot import internal/domain/workspace back
	// without an import cycle — the identical reasoning
	// work.RepositoryRelease.RepositoryID already follows by using
	// project.RepositoryID (project has no such cycle) rather than any
	// workspace-package type.
	RepositoryWorkspaceID string
	RepositoryID          project.RepositoryID
	// ExpectedGeneration/ExpectedWorkspaceVersion are this request's own
	// pinned "workspace generation/fence" (V6-10E's own Thực hiện line):
	// the target RepositoryWorkspace's Generation and Version exactly as
	// observed when this request was accepted. ExpectedWorkspaceVersion is
	// the fence RequestReleaseSetLocalCommit itself checks against the
	// current row before ever creating this intent (a stale caller is
	// rejected with ports.ErrOptimisticConflict, never given an intent at
	// all — see ReleaseSetLocalCommitFailureReason's own doc comment for
	// why a worker never re-checks it). ExpectedGeneration identifies which
	// physical workspace this operation targets and feeds the deterministic
	// marker (internal/app/releasesetcommit's own doc comment) and the
	// local-commit write lease's own target.
	ExpectedGeneration       uint64
	ExpectedWorkspaceVersion uint64
	Actor                    string
	Message                  string
	AuthorName               string
	AuthorEmail              string
	// MessageHash is sha256("sha256:"-prefixed) of Message — part of this
	// operation's own deterministic marker input.
	MessageHash string
	// Marker is this operation's own deterministic operation marker,
	// embedded as a trailer in the real commit message a worker creates —
	// see internal/app/releasesetcommit's own doc comment for its exact
	// construction and how it is used for crash-recovery reconciliation.
	Marker        string
	State         ReleaseSetLocalCommitState
	FailureReason ReleaseSetLocalCommitFailureReason
	// ParentVCSObjectID is populated once a worker has pinned it (see
	// ports.WorkRepository.PinReleaseSetLocalCommitParent's own doc
	// comment) — the exact commit this operation's own local commit is
	// created on top of.
	ParentVCSObjectID string
	// ResultVCSObjectID is populated only once State is COMMITTED — the
	// real commit this operation either created or reconciled.
	ResultVCSObjectID string
	JobID             string
	CreatedAt         time.Time
	CompletedAt       *time.Time
	Version           uint64
}

// NewReleaseSetLocalCommit validates and builds a new, REQUESTED
// ReleaseSetLocalCommit — never COMMITTED or FAILED directly.
func NewReleaseSetLocalCommit(
	id ReleaseSetLocalCommitID, projectID project.ProjectID, releaseSetID ReleaseSetID, expectedReleaseSetVersion uint64,
	repositoryWorkspaceID string, repositoryID project.RepositoryID,
	expectedGeneration, expectedWorkspaceVersion uint64,
	actor, message, authorName, authorEmail, messageHash, marker string, createdAt time.Time,
) (ReleaseSetLocalCommit, error) {
	if id == "" || projectID == "" || releaseSetID == "" || repositoryWorkspaceID == "" || repositoryID == "" {
		return ReleaseSetLocalCommit{}, errors.New("release set local commit identities are required")
	}
	if expectedReleaseSetVersion == 0 || expectedGeneration == 0 || expectedWorkspaceVersion == 0 {
		return ReleaseSetLocalCommit{}, errors.New("release set local commit expected release set version, generation and workspace version must all be greater than zero")
	}
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(message) == "" ||
		strings.TrimSpace(authorName) == "" || strings.TrimSpace(authorEmail) == "" {
		return ReleaseSetLocalCommit{}, errors.New("release set local commit actor, message, author name and author email are required")
	}
	if strings.TrimSpace(messageHash) == "" || strings.TrimSpace(marker) == "" {
		return ReleaseSetLocalCommit{}, errors.New("release set local commit message hash and marker are required")
	}
	if createdAt.IsZero() {
		return ReleaseSetLocalCommit{}, errors.New("release set local commit created timestamp is required")
	}
	return ReleaseSetLocalCommit{
		ID: id, ProjectID: projectID, ReleaseSetID: releaseSetID, ExpectedReleaseSetVersion: expectedReleaseSetVersion,
		RepositoryWorkspaceID: repositoryWorkspaceID, RepositoryID: repositoryID,
		ExpectedGeneration: expectedGeneration, ExpectedWorkspaceVersion: expectedWorkspaceVersion,
		Actor: actor, Message: message, AuthorName: authorName, AuthorEmail: authorEmail,
		MessageHash: messageHash, Marker: marker, State: ReleaseSetLocalCommitRequested,
		CreatedAt: createdAt.UTC(), Version: 1,
	}, nil
}
