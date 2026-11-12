package gitworktree

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

var _ ports.WorkspaceBaseSyncer = (*Provider)(nil)

// BaseStatus implements ports.WorkspaceBaseSyncer (V12-20): read-only.
func (p *Provider) BaseStatus(ctx context.Context, req ports.BaseSyncRequest) (ports.BaseSyncResult, error) {
	req.Fetch = false
	return p.baseSync(ctx, req, false)
}

// SyncWithBase implements ports.WorkspaceBaseSyncer (V12-20): fetch from origin, then fast-forward only.
func (p *Provider) SyncWithBase(ctx context.Context, req ports.BaseSyncRequest) (ports.BaseSyncResult, error) {
	return p.baseSync(ctx, req, true)
}

func (p *Provider) baseSync(ctx context.Context, req ports.BaseSyncRequest, move bool) (ports.BaseSyncResult, error) {
	short := strings.TrimPrefix(strings.TrimSpace(req.BaseRef), "refs/heads/")
	if short == "" || !validRevisionInput(short) {
		return ports.BaseSyncResult{}, fmt.Errorf("%w: invalid base branch %q", ErrInvalidSpec, req.BaseRef)
	}
	metadata, err := p.readMetadata(req.Handle)
	if err != nil {
		return ports.BaseSyncResult{}, err
	}
	inspection, workspacePath, err := p.inspectForRead(ctx, req.Handle)
	if err != nil {
		return ports.BaseSyncResult{}, err
	}
	repositoryPath, err := p.validateLocalRepository(ctx, metadata.LocalRepository)
	if err != nil {
		return ports.BaseSyncResult{}, err
	}
	result := ports.BaseSyncResult{BaseRef: short, Head: inspection.CurrentRevision.VCSObjectID}

	hasOrigin := false
	if _, code, _ := p.runGitWithExitCode(ctx, repositoryPath, "remote", "get-url", "origin"); code == 0 {
		hasOrigin = true
	}
	if req.Fetch && hasOrigin {
		// Only the base branch, into its remote-tracking ref; never a push, never a change to a local branch.
		if _, err := p.runGit(ctx, repositoryPath, "fetch", "--quiet", "--no-tags", "origin",
			"+refs/heads/"+short+":refs/remotes/origin/"+short); err != nil {
			result.FetchProblem = boundText(err.Error())
		} else {
			result.Fetched = true
		}
	}

	local, localErr := p.resolveCommit(ctx, repositoryPath, "refs/heads/"+short)
	remote := ""
	if hasOrigin {
		remote, _ = p.resolveCommit(ctx, repositoryPath, "refs/remotes/origin/"+short)
	}
	switch {
	case remote == "" && localErr != nil:
		return ports.BaseSyncResult{}, fmt.Errorf("%w: base branch %q not found in the repository", ErrInvalidSpec, short)
	case remote == "":
		result.BaseRevision = local
	case localErr != nil:
		result.BaseRevision = remote
	default:
		// The newer of the two: origin's unless the local branch already contains it.
		result.BaseRevision = remote
		if ok, err := p.isAncestor(ctx, repositoryPath, remote, local); err == nil && ok {
			result.BaseRevision = local
		}
	}

	if err := p.countAheadBehind(ctx, workspacePath, &result); err != nil {
		return ports.BaseSyncResult{}, err
	}
	// Merged: the family made commits since its worktree was created, and the base branch contains every one of them.
	familyCommits := 0
	if metadata.BaseRevision != "" {
		if out, err := p.runGit(ctx, workspacePath, "rev-list", "--count", metadata.BaseRevision+".."+result.Head, "--"); err == nil {
			familyCommits, _ = strconv.Atoi(strings.TrimSpace(string(out)))
		}
	}
	merged := result.Ahead == 0 && familyCommits > 0
	switch {
	case result.Behind == 0:
		result.Outcome = ports.BaseSyncUpToDate
	case result.Ahead > 0:
		result.Outcome = ports.BaseSyncNeedsMerge
	case inspection.Dirty:
		result.Outcome = ports.BaseSyncDirty
	case !move:
		result.Outcome = ""
	default:
		if _, err := p.runGit(ctx, workspacePath, "merge", "--ff-only", "--quiet", result.BaseRevision); err != nil {
			return ports.BaseSyncResult{}, wrapGitContextErr(ctx, err)
		}
		head, err := p.resolveCommit(ctx, workspacePath, "HEAD")
		if err != nil {
			return ports.BaseSyncResult{}, err
		}
		result.Head = head
		result.Outcome = ports.BaseSyncFastForwarded
		if err := p.countAheadBehind(ctx, workspacePath, &result); err != nil {
			return ports.BaseSyncResult{}, err
		}
	}
	result.Merged = merged
	return result, nil
}

func (p *Provider) countAheadBehind(ctx context.Context, workspacePath string, result *ports.BaseSyncResult) error {
	out, err := p.runGit(ctx, workspacePath, "rev-list", "--left-right", "--count", result.Head+"..."+result.BaseRevision, "--")
	if err != nil {
		return wrapGitContextErr(ctx, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return fmt.Errorf("%w: unexpected rev-list output %q", ErrGit, out)
	}
	result.Ahead, _ = strconv.Atoi(fields[0])
	result.Behind, _ = strconv.Atoi(fields[1])
	return nil
}

func (p *Provider) isAncestor(ctx context.Context, directory, ancestor, descendant string) (bool, error) {
	_, code, err := p.runGitWithExitCode(ctx, directory, "merge-base", "--is-ancestor", ancestor, descendant)
	switch code {
	case 0:
		return true, nil
	case 1:
		return false, nil
	}
	if err == nil {
		err = fmt.Errorf("%w: merge-base exited with code %d", ErrGit, code)
	}
	return false, err
}

func boundText(s string) string {
	if len(s) > 500 {
		return s[:500] + "…"
	}
	return s
}
