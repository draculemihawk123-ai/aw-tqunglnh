package workspacereconcile

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// ErrWorkspaceHeadDrift is what CheckFamilyWorkspaceHeads returns when a
// worktree's HEAD is not the revision aw recorded for it (V11-01): someone
// committed in the worktree outside aw. The error's text names the repository,
// both revisions, the commits and the commands that resolve it.
var ErrWorkspaceHeadDrift = errors.New("workspacereconcile: a worktree HEAD is not the revision aw recorded")

// WorkspaceHeadDrift is one repository whose worktree HEAD drifted.
type WorkspaceHeadDrift struct {
	RepositoryID          string
	RepositoryWorkspaceID string
	WorkspaceVersion      uint64
	Recorded              string
	Head                  string
	FastForward           bool
	TotalCommits          int
	Commits               []ports.HeadAuditCommit
}

// WorkspaceHeadDriftError is the typed form of ErrWorkspaceHeadDrift.
type WorkspaceHeadDriftError struct {
	ProjectID string
	Drifts    []WorkspaceHeadDrift
}

func (e *WorkspaceHeadDriftError) Unwrap() error { return ErrWorkspaceHeadDrift }

func (e *WorkspaceHeadDriftError) Error() string {
	var b strings.Builder
	b.WriteString("WORKSPACE_HEAD_DRIFT: a worktree HEAD is not the revision aw recorded, so a run started now would fail at its first machine gate.")
	for _, drift := range e.Drifts {
		fmt.Fprintf(&b, " Repository %s (workspace %s): HEAD %s, recorded %s", drift.RepositoryID, drift.RepositoryWorkspaceID,
			shortRevision(drift.Head), shortRevision(drift.Recorded))
		if drift.TotalCommits > 0 {
			fmt.Fprintf(&b, ", %d commit(s) made outside aw", drift.TotalCommits)
			if len(drift.Commits) > 0 {
				last := drift.Commits[len(drift.Commits)-1]
				fmt.Fprintf(&b, " (newest %s by %s)", shortRevision(last.CommitID), printable(last.AuthorName))
			}
		}
		if drift.FastForward {
			fmt.Fprintf(&b, ". To keep them: aw repository-workspace adopt-head %s --project-id %s --expected-version %d.",
				drift.RepositoryWorkspaceID, e.ProjectID, drift.WorkspaceVersion)
		} else {
			fmt.Fprintf(&b, ". HEAD no longer descends from the recorded revision (a reset or rebase): put the worktree back on %s, or reconcile the workspace.",
				shortRevision(drift.Recorded))
		}
	}
	return b.String()
}

func shortRevision(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func printable(text string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, text)
}

// CheckFamilyWorkspaceHeads is the preflight `aw run start` and POST
// /work-items/{id}/runs run before StartWorkflowRun (V11-01): it compares, for
// every repository of the work item's family, the live HEAD of the worktree
// with the CurrentRevision aw recorded — the revision the new run would pin.
// A mismatch means an IDE or a terminal committed in the worktree; the run
// would only discover it at its first MACHINE_GATE, after spending agent
// nodes. It reads only (one read-only transaction, then read-only Git), never
// adopts anything, and fails closed on a mismatch with *WorkspaceHeadDriftError.
//
// It is best effort by design: a repository whose worktree cannot be audited
// (released, unreadable) is skipped, because StartWorkflowRun and the gates
// keep their own checks — this preflight only turns a late, opaque failure into
// an early, explained one.
func CheckFamilyWorkspaceHeads(ctx context.Context, uow ports.UnitOfWork, auditor ports.WorkspaceHeadAuditor, workItemID string) error {
	if auditor == nil {
		return nil
	}
	type target struct {
		rw workspace.RepositoryWorkspace
	}
	var projectID string
	var targets []target
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		projectID = string(item.ProjectID)
		set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, string(item.FamilyID))
		if err != nil {
			return err
		}
		all, err := tx.Work().ListWorkspaceSetRepositoryWorkspaces(ctx, string(set.ID))
		if err != nil {
			return err
		}
		newest := map[string]workspace.RepositoryWorkspace{}
		for _, candidate := range all {
			if candidate.State != workspace.RepositoryWorkspaceReady {
				continue
			}
			key := string(candidate.RepositoryID)
			if held, found := newest[key]; !found || candidate.Generation > held.Generation {
				newest[key] = candidate
			}
		}
		for _, rw := range newest {
			targets = append(targets, target{rw: rw})
		}
		return nil
	})
	if err != nil {
		// Not found / not ready families are StartWorkflowRun's own errors to report.
		return nil
	}

	sort.Slice(targets, func(i, j int) bool { return targets[i].rw.RepositoryID < targets[j].rw.RepositoryID })
	drifted := &WorkspaceHeadDriftError{ProjectID: projectID}
	for _, t := range targets {
		recorded := t.rw.CurrentRevision
		if recorded == "" {
			continue
		}
		handle, err := ports.NewWorkspaceHandle(t.rw.Locator)
		if err != nil {
			continue
		}
		audit, err := auditor.AuditHead(ctx, ports.AuditHeadRequest{Handle: handle, PinnedRevision: recorded})
		if err != nil || audit.Head == recorded {
			continue
		}
		drifted.Drifts = append(drifted.Drifts, WorkspaceHeadDrift{
			RepositoryID: string(t.rw.RepositoryID), RepositoryWorkspaceID: string(t.rw.ID), WorkspaceVersion: t.rw.Version,
			Recorded: recorded, Head: audit.Head, FastForward: audit.FastForward, TotalCommits: audit.TotalCommits, Commits: audit.Commits,
		})
	}
	if len(drifted.Drifts) == 0 {
		return nil
	}
	return drifted
}
