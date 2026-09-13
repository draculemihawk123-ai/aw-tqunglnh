package gitworktree

import (
	"context"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// Provider also satisfies ports.LocalCommitMarkerReader (V6-10E,
// ports/releasesetlocalcommit.go's own doc comment) — Go's structural
// typing needs no change to Provider's own ports.WorkspaceProvider/
// ports.LocalCommitCreator assertions.
var _ ports.LocalCommitMarkerReader = (*Provider)(nil)

// FindLocalCommitByMarker implements ports.LocalCommitMarkerReader: reads
// req.Handle's own workspace's current HEAD commit — via `git log -1
// --format=... HEAD`, one bounded, read-only invocation — and reports
// whether its own raw message body contains marker as an exact
// ports.LocalCommitMarkerTrailerKey trailer line. Never inspects any commit
// other than HEAD (see that port method's own doc comment for why HEAD
// alone is sufficient) and never mutates anything: this method's own git
// invocation is `log`, joining CreateLocalCommit's own {add, status,
// commit, rev-parse} as one more entry in this adapter's fixed, read-or-
// commit-only vocabulary — never a remote operation (AK-ARCH-015C).
func (p *Provider) FindLocalCommitByMarker(ctx context.Context, handle ports.WorkspaceHandle, marker string) (bool, workspace.Revision, string, error) {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		return false, workspace.Revision{}, "", fmt.Errorf("%w: marker is required", ErrInvalidSpec)
	}

	inspection, err := p.Inspect(ctx, handle)
	if err != nil {
		return false, workspace.Revision{}, "", err
	}
	workspacePath, err := p.workspacePath(handle)
	if err != nil {
		return false, workspace.Revision{}, "", err
	}

	// %x1f (unit separator) can never appear in a well-formed commit
	// message, so SplitN below cannot be confused by anything %B itself
	// might legitimately contain — %B is read last, greedily, with N=3.
	output, err := p.runGit(ctx, workspacePath, "log", "-1", "--format=%H%x1f%P%x1f%B", "HEAD")
	if err != nil {
		return false, workspace.Revision{}, "", err
	}
	parts := strings.SplitN(string(output), "\x1f", 3)
	if len(parts) < 3 {
		return false, workspace.Revision{}, "", fmt.Errorf("%w: unexpected git log output reading HEAD", ErrGit)
	}
	commitID := strings.TrimSpace(parts[0])
	parentField := strings.TrimSpace(parts[1])
	body := parts[2]

	var parentVCSObjectID string
	if fields := strings.Fields(parentField); len(fields) > 0 {
		parentVCSObjectID = fields[0]
	}
	headRevision := workspace.Revision{
		RepositoryID: inspection.RepositoryID, VCSObjectID: commitID, WorkspaceGeneration: inspection.Generation,
	}

	trailer := ports.LocalCommitMarkerTrailerKey + ": " + marker
	found := strings.Contains(body, trailer)
	return found, headRevision, parentVCSObjectID, nil
}
