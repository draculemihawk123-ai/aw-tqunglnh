package ports

import (
	"context"
	"errors"
	"time"
)

// V12-28 (ADR-060): pushing a family branch, opening a pull request and merging it, with the machine's own git credential helper and
// GitHub CLI. aw keeps no forge token.

// RemoteBranchRecord is the remote branch aw pushed a repository workspace to, and its pull request.
type RemoteBranchRecord struct {
	RepositoryWorkspaceID string
	ProjectID             string
	RepositoryID          string
	Remote                string
	RemoteBranch          string
	PushedRevision        string
	PullRequestNumber     int
	PullRequestURL        string
	BaseBranch            string
	MergedRevision        string
	Version               uint64
	UpdatedBy             string
	UpdatedAt             time.Time
}

// ForgeRepository is the Tx accessor of the remote branches aw created.
type ForgeRepository interface {
	GetRemoteBranch(ctx context.Context, repositoryWorkspaceID string) (RemoteBranchRecord, error)
	// PutRemoteBranch inserts (expectedVersion 0) or replaces (the stored version; ErrOptimisticConflict otherwise) the record.
	PutRemoteBranch(ctx context.Context, record RemoteBranchRecord, expectedVersion uint64) (RemoteBranchRecord, error)
	// ApprovedCommitRevisions returns the commits aw created in the repository workspace for an approval decision (COMMITTED local
	// commits whose selection names an approval request): the commits that "went through an APPROVAL gate".
	ApprovedCommitRevisions(ctx context.Context, repositoryWorkspaceID string) ([]string, error)
}

// ErrRemoteBranchExists: the remote branch exists and aw did not create it.
var ErrRemoteBranchExists = errors.New("ports: the remote branch already exists and was not created by aw")

// ForgeGit is the git side of ADR-060, run in a repository workspace's worktree.
type ForgeGit interface {
	// RemoteURL returns the URL of remote ("origin"), or ErrNoRemote.
	RemoteURL(ctx context.Context, handle WorkspaceHandle, remote string) (string, error)
	// RemoteBranchHead returns the commit the remote branch points at (exists false when there is none).
	RemoteBranchHead(ctx context.Context, handle WorkspaceHandle, remote, branch string) (sha string, exists bool, err error)
	// PushHead pushes HEAD to refs/heads/<branch> of remote. Never forced: a remote branch that moved is refused by git itself.
	PushHead(ctx context.Context, handle WorkspaceHandle, remote, branch string) (pushed string, err error)
	// CommitsAhead lists the commits of HEAD not in base (a ref such as "origin/main"), oldest first.
	CommitsAhead(ctx context.Context, handle WorkspaceHandle, base string) ([]string, error)
	// CurrentBranch is the worktree's checked-out branch (the family branch, agentkit/w-…).
	CurrentBranch(ctx context.Context, handle WorkspaceHandle) (string, error)
	// WorkingDirectory is the worktree's path (where the forge CLI runs).
	WorkingDirectory(ctx context.Context, handle WorkspaceHandle) (string, error)
}

// ErrNoRemote: the repository has no such remote.
var ErrNoRemote = errors.New("ports: the repository has no such remote")

// PullRequest is what the forge says about a pull request.
type PullRequest struct {
	Number    int          `json:"number"`
	URL       string       `json:"url"`
	State     string       `json:"state"` // OPEN, CLOSED, MERGED
	Draft     bool         `json:"draft"`
	HeadSHA   string       `json:"headSha"`
	Base      string       `json:"base"`
	Head      string       `json:"head"`
	Title     string       `json:"title"`
	Mergeable string       `json:"mergeable,omitempty"` // MERGEABLE, CONFLICTING, UNKNOWN
	Checks    []ForgeCheck `json:"checks"`
	MergedAt  *time.Time   `json:"mergedAt,omitempty"`
}

// ForgeCheck is one CI check of a pull request.
type ForgeCheck struct {
	Name       string `json:"name"`
	Status     string `json:"status"`     // COMPLETED, IN_PROGRESS, QUEUED, PENDING
	Conclusion string `json:"conclusion"` // SUCCESS, FAILURE, NEUTRAL, SKIPPED, CANCELLED, TIMED_OUT, ACTION_REQUIRED, ""
}

// CreatePullRequestRequest is what ForgeClient.CreatePullRequest needs.
type CreatePullRequestRequest struct {
	Directory string // a checkout whose remote is the repository (the worktree)
	Head      string
	Base      string
	Title     string
	Body      string
	Draft     bool
}

// ForgeClient is the forge side of ADR-060 (GitHub through `gh`; another forge is another adapter).
type ForgeClient interface {
	CreatePullRequest(ctx context.Context, req CreatePullRequestRequest) (PullRequest, error)
	GetPullRequest(ctx context.Context, directory string, number int) (PullRequest, error)
	// MergePullRequest merges number with method (merge, squash, rebase) only if its head is still expectedHeadSHA.
	MergePullRequest(ctx context.Context, directory string, number int, method, expectedHeadSHA string) error
}

// ErrForgeUnavailable: the forge CLI is missing or not signed in; the message names the command to run.
var ErrForgeUnavailable = errors.New("ports: the forge CLI is not available or not signed in")
