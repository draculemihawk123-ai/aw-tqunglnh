// Package forgerelease is V12-28 (docs/design/15-v12-ui-operations.md, UX-01, UX-39, ADR-060): pushing a family branch to origin,
// opening a pull request, reading its checks and merging it, with the machine's own git credential helper and GitHub CLI.
//
// The rules, all checked, none assumed:
//   - the repository workspace is READY, no attempt holds its write lease, and its worktree HEAD is the revision aw recorded;
//   - every commit of the branch that the base branch does not have went through an APPROVAL gate of aw (a COMMITTED local commit of
//     an approval decision) — a commit made outside aw, even adopted, is refused;
//   - the branch is pushed, never forced, to the name chosen at the first push (default: the family branch); never to the base branch;
//     a remote branch that already exists and was not created by aw is refused;
//   - a merge needs the pull request open, its head the commit aw pushed, not conflicting, and every check green — or, when the
//     repository has no check at all, the operator's explicit acknowledgment that the pull request has no CI.
package forgerelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// Remote is the remote aw pushes to.
const Remote = "origin"

// Merge methods.
const (
	MergeCommit = "merge"
	MergeSquash = "squash"
	MergeRebase = "rebase"
)

// Errors of the commands.
var (
	ErrNotReady           = errors.New("forgerelease: the repository workspace is not READY")
	ErrWriterActive       = errors.New("forgerelease: an attempt is writing in the worktree")
	ErrHeadNotRecorded    = errors.New("forgerelease: the worktree HEAD is not the revision aw recorded; adopt or sync first")
	ErrUnapprovedCommits  = errors.New("forgerelease: the branch has commits that did not go through an approval gate of aw")
	ErrBaseBranch         = errors.New("forgerelease: aw never pushes to the base branch")
	ErrBranchFixed        = errors.New("forgerelease: the remote branch is chosen at the first push and cannot change")
	ErrNotPushed          = errors.New("forgerelease: push the branch first (the remote branch does not have the worktree HEAD)")
	ErrPullRequestExists  = errors.New("forgerelease: this branch already has a pull request")
	ErrNoPullRequest      = errors.New("forgerelease: this branch has no pull request")
	ErrPullRequestNotOpen = errors.New("forgerelease: the pull request is not open")
	ErrHeadMoved          = errors.New("forgerelease: the pull request head is not the commit aw pushed")
	ErrChecksNotGreen     = errors.New("forgerelease: not every check of the pull request passed")
	ErrNoChecksNotAcked   = errors.New("forgerelease: the pull request has no CI; confirm that you merge it without CI")
	ErrConflicting        = errors.New("forgerelease: the pull request conflicts with its base branch")
	ErrInvalidMethod      = errors.New("forgerelease: merge method must be merge, squash or rebase")
	ErrNoTitle            = errors.New("forgerelease: a pull request needs a title")
)

// Deps is what the commands need.
type Deps struct {
	UnitOfWork ports.UnitOfWork
	Auditor    ports.WorkspaceHeadAuditor
	Git        ports.ForgeGit
	Forge      ports.ForgeClient
}

func (d Deps) ok() error {
	if d.UnitOfWork == nil || d.Auditor == nil || d.Git == nil || d.Forge == nil {
		return fmt.Errorf("%w: this server has no Git workspace provider or no forge client", ports.ErrForgeUnavailable)
	}
	return nil
}

// RemoteView is the remote state of a repository workspace.
type RemoteView struct {
	RepositoryWorkspaceID string             `json:"repositoryWorkspaceId"`
	BaseBranch            string             `json:"baseBranch"`
	RemoteURL             string             `json:"remoteUrl,omitempty"`
	RemoteBranch          string             `json:"remoteBranch"`
	SuggestedBranch       string             `json:"suggestedBranch"`
	Pushed                bool               `json:"pushed"`
	PushedRevision        string             `json:"pushedRevision,omitempty"`
	Head                  string             `json:"head"`
	UnapprovedCommits     []string           `json:"unapprovedCommits"`
	PullRequest           *ports.PullRequest `json:"pullRequest,omitempty"`
	NoChecks              bool               `json:"noChecks"`
	MergeBlockers         []string           `json:"mergeBlockers"`
	MergedRevision        string             `json:"mergedRevision,omitempty"`
	Version               uint64             `json:"version,omitempty"`
}

type loaded struct {
	rw         workspace.RepositoryWorkspace
	handle     ports.WorkspaceHandle
	baseBranch string
	record     *ports.RemoteBranchRecord
	approved   map[string]bool
}

func shortBranch(ref string) string { return strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/") }

func load(ctx context.Context, d Deps, projectID, repositoryWorkspaceID string, forWrite bool) (loaded, error) {
	var l loaded
	err := d.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		record, err := tx.Work().GetRepositoryWorkspaceByID(ctx, repositoryWorkspaceID)
		if err != nil {
			return err
		}
		l.rw = record.Workspace
		repo, err := tx.Catalog().GetRepository(ctx, string(l.rw.RepositoryID))
		if err != nil {
			return err
		}
		if string(repo.ProjectID) != projectID {
			return fmt.Errorf("%w: repository workspace %s", ports.ErrPersistenceNotFound, repositoryWorkspaceID)
		}
		l.baseBranch = shortBranch(repo.DefaultRef)
		if l.rw.State != workspace.RepositoryWorkspaceReady {
			return fmt.Errorf("%w: %s is %s", ErrNotReady, repositoryWorkspaceID, l.rw.State)
		}
		if forWrite {
			active, err := tx.Work().HasActiveWriteLease(ctx, []string{repositoryWorkspaceID})
			if err != nil {
				return err
			}
			if active {
				return ErrWriterActive
			}
		}
		if rec, err := tx.Forge().GetRemoteBranch(ctx, repositoryWorkspaceID); err == nil {
			l.record = &rec
		} else if !errors.Is(err, ports.ErrPersistenceNotFound) {
			return err
		}
		approved, err := tx.Forge().ApprovedCommitRevisions(ctx, repositoryWorkspaceID)
		if err != nil {
			return err
		}
		l.approved = map[string]bool{}
		for _, sha := range approved {
			l.approved[sha] = true
		}
		return nil
	})
	if err != nil {
		return loaded{}, err
	}
	l.handle, err = ports.NewWorkspaceHandle(l.rw.Locator)
	return l, err
}

func (l loaded) recorded() string {
	if l.rw.CurrentRevision != "" {
		return l.rw.CurrentRevision
	}
	return l.rw.BaseRevision
}

// unapproved lists the commits of HEAD the base branch does not have that did not go through an approval gate.
func unapproved(ctx context.Context, d Deps, l loaded) ([]string, error) {
	commits, err := d.Git.CommitsAhead(ctx, l.handle, Remote+"/"+l.baseBranch)
	if err != nil {
		// No remote-tracking ref yet (never fetched): compare with the local base branch.
		if commits, err = d.Git.CommitsAhead(ctx, l.handle, l.baseBranch); err != nil {
			return nil, err
		}
	}
	out := []string{}
	for _, c := range commits {
		if !l.approved[c] {
			out = append(out, c)
		}
	}
	return out, nil
}

func checkHead(ctx context.Context, d Deps, l loaded) (string, error) {
	audit, err := d.Auditor.AuditHead(ctx, ports.AuditHeadRequest{Handle: l.handle, PinnedRevision: l.recorded(), CommitLimit: 1})
	if err != nil {
		return "", err
	}
	if audit.Dirty || audit.Head != l.recorded() {
		return "", fmt.Errorf("%w (HEAD %s, recorded %s)", ErrHeadNotRecorded, audit.Head, l.recorded())
	}
	return audit.Head, nil
}

// mergeBlockers says why the pull request cannot be merged now (empty: it can, after the no-CI acknowledgment when NoChecks).
func mergeBlockers(pr *ports.PullRequest, l loaded, head string, unapprovedCommits []string) []string {
	var out []string
	if pr == nil {
		return []string{ErrNoPullRequest.Error()}
	}
	if pr.State != "OPEN" {
		out = append(out, ErrPullRequestNotOpen.Error())
	}
	if l.record == nil || pr.HeadSHA != l.record.PushedRevision || l.record.PushedRevision != head {
		out = append(out, ErrHeadMoved.Error())
	}
	if len(unapprovedCommits) > 0 {
		out = append(out, ErrUnapprovedCommits.Error())
	}
	if pr.Mergeable == "CONFLICTING" {
		out = append(out, ErrConflicting.Error())
	}
	if pr.Draft {
		out = append(out, "the pull request is a draft; mark it ready on GitHub first")
	}
	for _, c := range pr.Checks {
		if c.Status != "COMPLETED" || !(c.Conclusion == "SUCCESS" || c.Conclusion == "NEUTRAL" || c.Conclusion == "SKIPPED") {
			out = append(out, fmt.Sprintf("%s: check %q is %s %s", ErrChecksNotGreen.Error(), c.Name, c.Status, c.Conclusion))
		}
	}
	return out
}

// GetRemote reads the remote state; the pull request is read from the forge when there is one.
func GetRemote(ctx context.Context, d Deps, projectID, repositoryWorkspaceID string) (RemoteView, error) {
	l, err := load(ctx, d, projectID, repositoryWorkspaceID, false)
	if err != nil {
		return RemoteView{}, err
	}
	if err := d.ok(); err != nil {
		return RemoteView{}, err
	}
	view := RemoteView{RepositoryWorkspaceID: repositoryWorkspaceID, BaseBranch: l.baseBranch, Head: l.recorded(), MergeBlockers: []string{}}
	view.SuggestedBranch, _ = d.Git.CurrentBranch(ctx, l.handle)
	view.RemoteURL, _ = d.Git.RemoteURL(ctx, l.handle, Remote)
	if view.UnapprovedCommits, err = unapproved(ctx, d, l); err != nil {
		return RemoteView{}, err
	}
	if l.record != nil {
		view.RemoteBranch, view.PushedRevision, view.MergedRevision, view.Version = l.record.RemoteBranch, l.record.PushedRevision, l.record.MergedRevision, l.record.Version
		view.Pushed = l.record.PushedRevision == l.recorded()
		if l.record.PullRequestNumber != 0 {
			dir, err := d.Git.WorkingDirectory(ctx, l.handle)
			if err != nil {
				return RemoteView{}, err
			}
			pr, err := d.Forge.GetPullRequest(ctx, dir, l.record.PullRequestNumber)
			if err != nil {
				return RemoteView{}, err
			}
			view.PullRequest = &pr
			view.NoChecks = len(pr.Checks) == 0
		}
	}
	view.MergeBlockers = mergeBlockers(view.PullRequest, l, l.recorded(), view.UnapprovedCommits)
	if view.MergeBlockers == nil {
		view.MergeBlockers = []string{}
	}
	return view, nil
}

// replay returns the receipt's result when cmd was already done.
func replay(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, out any) (bool, error) {
	found := false
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		receipt, ok, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil || !ok {
			return err
		}
		if receipt.RequestHash != cmd.RequestHash {
			return ports.ErrReceiptConflict
		}
		found = true
		return json.Unmarshal([]byte(receipt.ResultJSON), out)
	})
	return found, err
}

// save writes the record (CAS on the version load saw), the audit event (appendEvent, which names its own event type constant) and
// the receipt.
func save(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, l loaded, rec ports.RemoteBranchRecord, appendEvent func(tx ports.Tx, payload []byte) error, payload, result any) error {
	return uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		var expected uint64
		if l.record != nil {
			expected = l.record.Version
		}
		rec.UpdatedBy, rec.UpdatedAt = cmd.Actor, cmd.RequestedAt
		if _, err := tx.Forge().PutRemoteBranch(ctx, rec, expected); err != nil {
			return err
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		if err := appendEvent(tx, body); err != nil {
			return err
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey, CommandType: cmd.Type,
			RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON), CreatedAt: cmd.RequestedAt,
		})
	})
}

func baseRecord(l loaded, projectID string) ports.RemoteBranchRecord {
	if l.record != nil {
		return *l.record
	}
	return ports.RemoteBranchRecord{RepositoryWorkspaceID: string(l.rw.ID), ProjectID: projectID, RepositoryID: string(l.rw.RepositoryID), Remote: Remote}
}

// PushRequest pushes a repository workspace's branch.
type PushRequest struct {
	ProjectID             string
	RepositoryWorkspaceID string
	// RemoteBranch is the name on origin; only at the first push (default: the family branch).
	RemoteBranch string
}

// PushResult is what the push did.
type PushResult struct {
	RemoteBranch string `json:"remoteBranch"`
	Pushed       string `json:"pushed"`
	Created      bool   `json:"created"`
}

// Push pushes the worktree HEAD to origin (see the package comment).
func Push(ctx context.Context, d Deps, cmd ports.Command, req PushRequest) (PushResult, error) {
	var result PushResult
	if done, err := replay(ctx, d.UnitOfWork, cmd, &result); err != nil || done {
		return result, err
	}
	l, err := load(ctx, d, req.ProjectID, req.RepositoryWorkspaceID, true)
	if err != nil {
		return PushResult{}, err
	}
	if err := d.ok(); err != nil {
		return PushResult{}, err
	}
	head, err := checkHead(ctx, d, l)
	if err != nil {
		return PushResult{}, err
	}
	branch := shortBranch(req.RemoteBranch)
	switch {
	case l.record != nil && branch != "" && branch != l.record.RemoteBranch:
		return PushResult{}, fmt.Errorf("%w (it is %s)", ErrBranchFixed, l.record.RemoteBranch)
	case l.record != nil:
		branch = l.record.RemoteBranch
	case branch == "":
		if branch, err = d.Git.CurrentBranch(ctx, l.handle); err != nil {
			return PushResult{}, err
		}
	}
	if branch == l.baseBranch || branch == "" {
		return PushResult{}, fmt.Errorf("%w (%s)", ErrBaseBranch, l.baseBranch)
	}
	if _, err := d.Git.RemoteURL(ctx, l.handle, Remote); err != nil {
		return PushResult{}, err
	}
	bad, err := unapproved(ctx, d, l)
	if err != nil {
		return PushResult{}, err
	}
	if len(bad) > 0 {
		return PushResult{}, fmt.Errorf("%w: %s", ErrUnapprovedCommits, strings.Join(bad, ", "))
	}
	created := l.record == nil
	if created {
		if _, exists, err := d.Git.RemoteBranchHead(ctx, l.handle, Remote, branch); err != nil {
			return PushResult{}, err
		} else if exists {
			return PushResult{}, fmt.Errorf("%w: %s", ports.ErrRemoteBranchExists, branch)
		}
	}
	pushed, err := d.Git.PushHead(ctx, l.handle, Remote, branch)
	if err != nil {
		return PushResult{}, err
	}
	if pushed != head {
		return PushResult{}, fmt.Errorf("forgerelease: pushed %s, expected %s", pushed, head)
	}
	rec := baseRecord(l, req.ProjectID)
	rec.RemoteBranch, rec.PushedRevision, rec.BaseBranch = branch, pushed, l.baseBranch
	result = PushResult{RemoteBranch: branch, Pushed: pushed, Created: created}
	payload := familyBranchPushedPayload{RepositoryWorkspaceID: req.RepositoryWorkspaceID, Remote: Remote, RemoteBranch: branch, Revision: pushed, Created: created, Actor: cmd.Actor}
	if err := save(ctx, d.UnitOfWork, cmd, l, rec, func(tx ports.Tx, body []byte) error {
		return tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-pushed", ProjectID: req.ProjectID, AggregateType: "ForgeOperation", AggregateID: cmd.ID, Sequence: 1,
			EventType: FamilyBranchPushedEventType, SchemaVersion: FamilyBranchPushedSchemaVersion, PayloadJSON: string(body),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		})
	}, payload, result); err != nil {
		return PushResult{}, fmt.Errorf("the branch was pushed but recording it failed: %w", err)
	}
	return result, nil
}

// CreatePullRequestRequest opens a pull request for the pushed branch.
type CreatePullRequestRequest struct {
	ProjectID             string
	RepositoryWorkspaceID string
	Title                 string
	Body                  string
	Draft                 bool
}

// CreatePullRequest opens a pull request from the pushed branch to the base branch (ready unless Draft).
func CreatePullRequest(ctx context.Context, d Deps, cmd ports.Command, req CreatePullRequestRequest) (ports.PullRequest, error) {
	var result ports.PullRequest
	if done, err := replay(ctx, d.UnitOfWork, cmd, &result); err != nil || done {
		return result, err
	}
	l, err := load(ctx, d, req.ProjectID, req.RepositoryWorkspaceID, false)
	if err != nil {
		return ports.PullRequest{}, err
	}
	if strings.TrimSpace(req.Title) == "" {
		return ports.PullRequest{}, ErrNoTitle
	}
	if err := d.ok(); err != nil {
		return ports.PullRequest{}, err
	}
	if l.record == nil || l.record.PushedRevision != l.recorded() {
		return ports.PullRequest{}, ErrNotPushed
	}
	if l.record.PullRequestNumber != 0 {
		return ports.PullRequest{}, fmt.Errorf("%w: %s", ErrPullRequestExists, l.record.PullRequestURL)
	}
	dir, err := d.Git.WorkingDirectory(ctx, l.handle)
	if err != nil {
		return ports.PullRequest{}, err
	}
	pr, err := d.Forge.CreatePullRequest(ctx, ports.CreatePullRequestRequest{
		Directory: dir, Head: l.record.RemoteBranch, Base: l.baseBranch, Title: strings.TrimSpace(req.Title), Body: req.Body, Draft: req.Draft,
	})
	if err != nil {
		return ports.PullRequest{}, err
	}
	rec := *l.record
	rec.PullRequestNumber, rec.PullRequestURL = pr.Number, pr.URL
	payload := pullRequestOpenedPayload{RepositoryWorkspaceID: req.RepositoryWorkspaceID, Number: pr.Number, URL: pr.URL, Draft: pr.Draft, Base: l.baseBranch, Actor: cmd.Actor}
	if err := save(ctx, d.UnitOfWork, cmd, l, rec, func(tx ports.Tx, body []byte) error {
		return tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-opened", ProjectID: req.ProjectID, AggregateType: "ForgeOperation", AggregateID: cmd.ID, Sequence: 1,
			EventType: PullRequestOpenedEventType, SchemaVersion: PullRequestOpenedSchemaVersion, PayloadJSON: string(body),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		})
	}, payload, pr); err != nil {
		return ports.PullRequest{}, fmt.Errorf("pull request %s was opened but recording it failed: %w", pr.URL, err)
	}
	return pr, nil
}

// MergeRequest merges the pull request.
type MergeRequest struct {
	ProjectID             string
	RepositoryWorkspaceID string
	// Method is merge (default), squash or rebase.
	Method string
	// AcknowledgeNoChecks is the operator's "this pull request has no CI" confirmation; required when it has no check.
	AcknowledgeNoChecks bool
}

// MergeResult is the merge done.
type MergeResult struct {
	Number   int    `json:"number"`
	URL      string `json:"url"`
	Method   string `json:"method"`
	Head     string `json:"head"`
	NoChecks bool   `json:"noChecks"`
}

// Merge merges the pull request through the forge (see the package comment).
func Merge(ctx context.Context, d Deps, cmd ports.Command, req MergeRequest) (MergeResult, error) {
	var result MergeResult
	if done, err := replay(ctx, d.UnitOfWork, cmd, &result); err != nil || done {
		return result, err
	}
	method := req.Method
	if method == "" {
		method = MergeCommit
	}
	if method != MergeCommit && method != MergeSquash && method != MergeRebase {
		return MergeResult{}, ErrInvalidMethod
	}
	l, err := load(ctx, d, req.ProjectID, req.RepositoryWorkspaceID, true)
	if err != nil {
		return MergeResult{}, err
	}
	if err := d.ok(); err != nil {
		return MergeResult{}, err
	}
	if l.record == nil || l.record.PullRequestNumber == 0 {
		return MergeResult{}, ErrNoPullRequest
	}
	head, err := checkHead(ctx, d, l)
	if err != nil {
		return MergeResult{}, err
	}
	dir, err := d.Git.WorkingDirectory(ctx, l.handle)
	if err != nil {
		return MergeResult{}, err
	}
	pr, err := d.Forge.GetPullRequest(ctx, dir, l.record.PullRequestNumber)
	if err != nil {
		return MergeResult{}, err
	}
	bad, err := unapproved(ctx, d, l)
	if err != nil {
		return MergeResult{}, err
	}
	if blockers := mergeBlockers(&pr, l, head, bad); len(blockers) > 0 {
		return MergeResult{}, fmt.Errorf("%w: %s", errorFor(blockers), strings.Join(blockers, "; "))
	}
	noChecks := len(pr.Checks) == 0
	if noChecks && !req.AcknowledgeNoChecks {
		return MergeResult{}, ErrNoChecksNotAcked
	}
	if err := d.Forge.MergePullRequest(ctx, dir, pr.Number, method, pr.HeadSHA); err != nil {
		return MergeResult{}, err
	}
	rec := *l.record
	rec.MergedRevision = pr.HeadSHA
	result = MergeResult{Number: pr.Number, URL: pr.URL, Method: method, Head: pr.HeadSHA, NoChecks: noChecks}
	payload := pullRequestMergedPayload{RepositoryWorkspaceID: req.RepositoryWorkspaceID, Number: pr.Number, URL: pr.URL, Method: method,
		Head: pr.HeadSHA, NoChecks: noChecks, Actor: cmd.Actor}
	if err := save(ctx, d.UnitOfWork, cmd, l, rec, func(tx ports.Tx, body []byte) error {
		return tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-merged", ProjectID: req.ProjectID, AggregateType: "ForgeOperation", AggregateID: cmd.ID, Sequence: 1,
			EventType: PullRequestMergedEventType, SchemaVersion: PullRequestMergedSchemaVersion, PayloadJSON: string(body),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		})
	}, payload, result); err != nil {
		return MergeResult{}, fmt.Errorf("pull request %s was merged but recording it failed: %w", pr.URL, err)
	}
	return result, nil
}

// errorFor picks the typed error of the first blocker (so a caller can map it).
func errorFor(blockers []string) error {
	for _, e := range []error{ErrPullRequestNotOpen, ErrHeadMoved, ErrUnapprovedCommits, ErrConflicting, ErrChecksNotGreen} {
		if strings.HasPrefix(blockers[0], e.Error()) {
			return e
		}
	}
	return ErrChecksNotGreen
}
