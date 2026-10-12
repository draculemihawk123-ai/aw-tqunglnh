// Package workitemredo is the application layer HTTP and the CLI share for V11-10 (ADR-041): running a failed task again and redoing
// it with the newest workflow version, in one action each.
//
// The two commands themselves (internal/app/runtime RetryWorkItem and RecreateWorkItem) do every database write in one transaction.
// What cannot live there is Git: a failed run can leave files outside the WorkItem's scope (a stray docs/needs-info.md), and a new run
// that starts beside them fails again with SCOPE_VIOLATION. This package reads the worktree, tells the operator which changed files are
// out of scope, and, when asked, parks exactly those files (Git ref under refs/agentkit/parked/, restorable, never deleted) BEFORE the
// command runs, because the command's job starts a run that a worker can claim at once. The command only records what was done.
//
// It also answers the question the confirmation dialog asks (Preview): what would a retry or a redo do, why would one refuse, what
// has repeating this task cost so far.
package workitemredo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// What to do with a previous run's files that are outside the WorkItem's scope.
const (
	// Keep leaves them where they are; the next run will very likely fail with SCOPE_VIOLATION again.
	Keep = "KEEP"
	// Park moves exactly those files out of the worktree into a hidden Git ref.
	Park = "PARK"
)

var (
	// ErrInvalidDisposition is returned for an out-of-scope choice that is neither KEEP nor PARK.
	ErrInvalidDisposition = errors.New("workitemredo: outOfScope must be KEEP or PARK")
	// ErrParkUnavailable is returned when PARK is asked for but this process has no Git workspace provider to do it with.
	ErrParkUnavailable = errors.New("workitemredo: this process has no Git workspace provider, so it cannot park files")
	// ErrWorktreeNotReadable is returned when PARK is asked for but the worktree could not be read (a writer is active, too many changes).
	ErrWorktreeNotReadable = errors.New("workitemredo: the worktree cannot be read, so files cannot be parked")
)

// OutOfScopeDecisionError is returned when a previous run left files outside the WorkItem's scope and the caller did not say what to do
// with them. Nothing was changed.
type OutOfScopeDecisionError struct{ Files []LeftoverFile }

func (e *OutOfScopeDecisionError) Error() string {
	paths := make([]string, 0, len(e.Files))
	for _, f := range e.Files {
		paths = append(paths, f.RepositoryID+":"+f.Path)
	}
	if len(paths) > 5 {
		paths = append(paths[:5], fmt.Sprintf("... (%d files)", len(e.Files)))
	}
	return "workitemredo: the worktree holds files outside the work item's scope (" + strings.Join(paths, ", ") + "); choose KEEP or PARK"
}

// LeftoverFile is one uncommitted change in a worktree of the WorkItem's family.
type LeftoverFile struct {
	RepositoryID          string `json:"repositoryId"`
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	Path                  string `json:"path"`
	Status                string `json:"status"`
	// OutOfScope reports that no WRITE scope of the WorkItem covers the path: a run that finds it fails with SCOPE_VIOLATION.
	OutOfScope bool `json:"outOfScope"`
}

// VersionView names a workflow version.
type VersionView struct {
	WorkflowVersionID string `json:"workflowVersionId"`
	VersionNumber     uint64 `json:"versionNumber"`
}

// WorkItemRef names a WorkItem a redo links to.
type WorkItemRef struct {
	WorkItemID string `json:"workItemId"`
	Title      string `json:"title,omitempty"`
}

// Preview is what the dialogs show before an operator commits to a retry or a redo.
type Preview struct {
	WorkItemID string `json:"workItemId"`
	ProjectID  string `json:"projectId"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	IsChild    bool   `json:"isChild"`

	CanRetry        bool                 `json:"canRetry"`
	RetryRefusal    *runtime.RedoRefusal `json:"retryRefusal,omitempty"`
	CanRecreate     bool                 `json:"canRecreate"`
	RecreateRefusal *runtime.RedoRefusal `json:"recreateRefusal,omitempty"`

	PinnedWorkflowVersion VersionView `json:"pinnedWorkflowVersion"`
	LatestWorkflowVersion VersionView `json:"latestWorkflowVersion"`
	// NewerVersionAvailable is true when the definition has a version newer than the one the WorkItem is pinned to.
	NewerVersionAvailable bool `json:"newerVersionAvailable"`

	BlockersToResolve    []runtime.RedoBlocker `json:"blockersToResolve"`
	OperatorMessageCount int                   `json:"operatorMessageCount"`
	// RedoNumber is the number the next redo would carry (2 for the first); a client warns from 3.
	RedoNumber int `json:"redoNumber"`
	// PriorRunCount and PriorCostUsd cover this WorkItem and every WorkItem it replaced. CostKnown is false when no run reported a cost.
	PriorRunCount int     `json:"priorRunCount"`
	PriorCostUsd  float64 `json:"priorCostUsd"`
	CostKnown     bool    `json:"costKnown"`

	Supersedes   *WorkItemRef `json:"supersedes,omitempty"`
	SupersededBy *WorkItemRef `json:"supersededBy,omitempty"`

	Files           []LeftoverFile `json:"files"`
	OutOfScopeCount int            `json:"outOfScopeCount"`
	// FilesUnavailableReason says why Files could not be read (an attempt is writing, too many changes, no Git provider). Empty when read.
	FilesUnavailableReason string `json:"filesUnavailableReason,omitempty"`
}

// Service is the composition of the redo commands with the worktree handling. Inspection and Parker may be nil (a process without a
// Git workspace provider): files are then not inspected and PARK is refused.
type Service struct {
	runtime.RedoDeps
	Inspection *workspaceinspection.Queries
	Parker     ports.SelectedChangeParker
}

// RetryRequest is what a caller supplies to Retry.
type RetryRequest struct {
	ProjectID  string
	WorkItemID string
	Note       string
	// OutOfScope is KEEP or PARK, required when a previous run left files outside the WorkItem's scope.
	OutOfScope string
}

// RecreateRequest is what a caller supplies to Recreate.
type RecreateRequest struct {
	ProjectID     string
	WorkItemID    string
	Reason        string
	Note          string
	CarryMessages bool
	OutOfScope    string
}

// Retry settles files outside the scope, then runs runtime.RetryWorkItem.
func (s Service) Retry(ctx context.Context, cmd ports.Command, req RetryRequest) (runtime.RetryWorkItemResult, error) {
	settlement, err := s.settleBefore(ctx, cmd, req.ProjectID, req.WorkItemID, req.OutOfScope, func(a runtime.RedoAssessment) *runtime.RedoRefusal { return a.Retry })
	if err != nil {
		return runtime.RetryWorkItemResult{}, err
	}
	return runtime.RetryWorkItem(ctx, s.RedoDeps, cmd, runtime.RetryWorkItemRequest{
		ProjectID: req.ProjectID, WorkItemID: req.WorkItemID, Note: req.Note, OutOfScope: settlement.outOfScope, Parked: settlement.parked,
	})
}

// Recreate settles files outside the scope, then runs runtime.RecreateWorkItem.
func (s Service) Recreate(ctx context.Context, cmd ports.Command, req RecreateRequest) (runtime.RecreateWorkItemResult, error) {
	settlement, err := s.settleBefore(ctx, cmd, req.ProjectID, req.WorkItemID, req.OutOfScope, func(a runtime.RedoAssessment) *runtime.RedoRefusal { return a.Recreate })
	if err != nil {
		return runtime.RecreateWorkItemResult{}, err
	}
	return runtime.RecreateWorkItem(ctx, s.RedoDeps, cmd, runtime.RecreateWorkItemRequest{
		ProjectID: req.ProjectID, WorkItemID: req.WorkItemID, Reason: req.Reason, Note: req.Note, CarryMessages: req.CarryMessages,
		OutOfScope: settlement.outOfScope, Parked: settlement.parked,
	})
}

type settlement struct {
	outOfScope string
	parked     []runtime.RedoParkedSet
}

// settleBefore does what must happen before the command: nothing on a replay (the command returns its first result), nothing when the
// command would refuse anyway (it reports the refusal itself, and no file must move for a command that does not run), otherwise it reads
// the worktree and applies the caller's choice for files outside the scope.
func (s Service) settleBefore(ctx context.Context, cmd ports.Command, projectID, workItemID, disposition string, refusal func(runtime.RedoAssessment) *runtime.RedoRefusal) (settlement, error) {
	if disposition != "" && disposition != Keep && disposition != Park {
		return settlement{}, ErrInvalidDisposition
	}
	if replay, err := runtime.RedoReceiptExists(ctx, s.UnitOfWork, cmd); err != nil || replay {
		return settlement{outOfScope: disposition}, err
	}
	assessment, err := runtime.AssessRedo(ctx, s.UnitOfWork, projectID, workItemID)
	if err != nil {
		return settlement{}, err
	}
	if refusal(assessment) != nil {
		return settlement{outOfScope: disposition}, nil
	}

	files, workspaces, unavailable, err := s.leftovers(ctx, assessment)
	if err != nil {
		return settlement{}, err
	}
	var outside []LeftoverFile
	for _, f := range files {
		if f.OutOfScope {
			outside = append(outside, f)
		}
	}
	if len(outside) == 0 {
		if disposition == Park && unavailable != "" {
			return settlement{}, fmt.Errorf("%w: %s", ErrWorktreeNotReadable, unavailable)
		}
		return settlement{}, nil
	}
	switch disposition {
	case "":
		return settlement{}, &OutOfScopeDecisionError{Files: outside}
	case Keep:
		return settlement{outOfScope: Keep}, nil
	}
	if s.Parker == nil {
		return settlement{}, ErrParkUnavailable
	}
	byWorkspace := map[string][]string{}
	for _, f := range outside {
		byWorkspace[f.RepositoryWorkspaceID] = append(byWorkspace[f.RepositoryWorkspaceID], f.Path)
	}
	ids := make([]string, 0, len(byWorkspace))
	for id := range byWorkspace {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := settlement{outOfScope: Park}
	for _, id := range ids {
		w := workspaces[id]
		handle, err := ports.NewWorkspaceHandle(w.Locator)
		if err != nil {
			return settlement{}, fmt.Errorf("workitemredo: repository workspace %s has no usable locator: %w", id, err)
		}
		parked, err := s.Parker.ParkChangedPaths(ctx, handle, parkRef(cmd.IdempotencyKey, id), byWorkspace[id])
		if err != nil {
			return settlement{}, fmt.Errorf("workitemredo: park files of repository workspace %s: %w", id, err)
		}
		paths := append([]string(nil), parked.Paths...)
		sort.Strings(paths)
		result.parked = append(result.parked, runtime.RedoParkedSet{
			RepositoryID: string(w.RepositoryID), RepositoryWorkspaceID: id, Ref: parked.Ref, Paths: paths,
		})
	}
	return result, nil
}

// parkRef is the hidden ref a redo parks under: derived from the idempotency key so a retried request parks to the same ref (the
// adapter then reports what is already there instead of parking twice).
func parkRef(idempotencyKey, repositoryWorkspaceID string) string {
	sum := sha256.Sum256([]byte(idempotencyKey + "|" + repositoryWorkspaceID))
	return "refs/agentkit/parked/redo-" + hex.EncodeToString(sum[:8])
}

// leftovers reads the uncommitted changes of every current READY repository workspace of the WorkItem's family and marks those no
// WRITE scope of the WorkItem covers. unavailable is non-empty when they could not be read; the list is then incomplete.
func (s Service) leftovers(ctx context.Context, a runtime.RedoAssessment) (files []LeftoverFile, workspaces map[string]workspace.RepositoryWorkspace, unavailable string, err error) {
	workspaces = map[string]workspace.RepositoryWorkspace{}
	if s.Inspection == nil {
		return nil, workspaces, "this process cannot inspect worktrees", nil
	}
	var all []workspace.RepositoryWorkspace
	if err := s.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, a.FamilyID)
		if err != nil {
			return err
		}
		all, err = tx.Work().ListWorkspaceSetRepositoryWorkspaces(ctx, string(set.ID))
		return err
	}); err != nil {
		return nil, nil, "", err
	}
	current := map[project.RepositoryID]workspace.RepositoryWorkspace{}
	for _, w := range all {
		if w.State != workspace.RepositoryWorkspaceReady {
			continue
		}
		if best, ok := current[w.RepositoryID]; !ok || w.Generation > best.Generation {
			current[w.RepositoryID] = w
		}
	}
	repositories := make([]string, 0, len(current))
	for id := range current {
		repositories = append(repositories, string(id))
	}
	sort.Strings(repositories)
	for _, repositoryID := range repositories {
		w := current[project.RepositoryID(repositoryID)]
		revision := w.CurrentRevision
		if revision == "" {
			revision = w.BaseRevision
		}
		diff, err := s.Inspection.GetWorkingTreeDiff(ctx, workspaceinspection.GetWorkingTreeDiffRequest{
			Scope: workspaceinspection.WorkspaceScope{
				ProjectID: a.ProjectID, RepositoryID: repositoryID, WorkspaceSetID: string(w.WorkspaceSetID), RepositoryWorkspaceID: string(w.ID),
			},
			BaseRevision: workspace.Revision{RepositoryID: w.RepositoryID, VCSObjectID: revision, WorkspaceGeneration: w.Generation},
			ByteLimit:    1, FileLimit: 5000,
		})
		switch {
		case errors.Is(err, workspaceinspection.ErrWorkspaceWriterActive):
			unavailable = "an attempt is writing to the worktree of " + repositoryID
			continue
		case errors.Is(err, ports.ErrWorkingTreeTooLarge):
			unavailable = "the worktree of " + repositoryID + " has too many changed files to list"
			continue
		case err != nil:
			unavailable = "the worktree of " + repositoryID + " cannot be read: " + err.Error()
			continue
		}
		if diff.FilesTruncated || diff.TotalFiles > len(diff.Files) {
			unavailable = "the worktree of " + repositoryID + " has more changed files than can be listed"
		}
		workspaces[string(w.ID)] = w
		for _, f := range diff.Files {
			files = append(files, LeftoverFile{
				RepositoryID: repositoryID, RepositoryWorkspaceID: string(w.ID), Path: f.Path, Status: string(f.Status),
				OutOfScope: !scopeguard.PathAllowed(a.Scopes, w.RepositoryID, f.Path),
			})
		}
	}
	return files, workspaces, unavailable, nil
}

// Preview assembles what the retry and redo dialogs show. It never changes anything.
func (s Service) Preview(ctx context.Context, projectID, workItemID string) (Preview, error) {
	a, err := runtime.AssessRedo(ctx, s.UnitOfWork, projectID, workItemID)
	if err != nil {
		return Preview{}, err
	}
	out := Preview{
		WorkItemID: a.WorkItemID, ProjectID: a.ProjectID, Title: a.Title, Status: a.Status, IsChild: a.IsChild,
		CanRetry: a.Retry == nil, RetryRefusal: a.Retry, CanRecreate: a.Recreate == nil, RecreateRefusal: a.Recreate,
		PinnedWorkflowVersion: VersionView{WorkflowVersionID: a.PinnedWorkflowVersionID, VersionNumber: a.PinnedVersionNumber},
		LatestWorkflowVersion: VersionView{WorkflowVersionID: a.LatestWorkflowVersionID, VersionNumber: a.LatestVersionNumber},
		NewerVersionAvailable: a.LatestVersionNumber > a.PinnedVersionNumber,
		BlockersToResolve:     a.BlockersToResolve, OperatorMessageCount: a.OperatorMessageCount, RedoNumber: a.RedoNumber,
		PriorRunCount: len(a.LineageRunIDs), Files: []LeftoverFile{},
	}
	if out.BlockersToResolve == nil {
		out.BlockersToResolve = []runtime.RedoBlocker{}
	}
	for _, runID := range a.LineageRunIDs {
		timeline, err := runtime.GetRunTimeline(ctx, s.UnitOfWork, s.Matcher, runID)
		if err != nil {
			return Preview{}, err
		}
		if timeline.Usage != nil {
			out.PriorCostUsd += timeline.Usage.CostUSD
			out.CostKnown = true
		}
	}
	if err := s.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
		ref := func(id string) (*WorkItemRef, error) {
			if id == "" {
				return nil, nil
			}
			item, err := tx.Work().GetWorkItem(ctx, id)
			if err != nil {
				return nil, err
			}
			return &WorkItemRef{WorkItemID: id, Title: item.Title}, nil
		}
		var err error
		if out.Supersedes, err = ref(a.SupersedesWorkItemID); err != nil {
			return err
		}
		out.SupersededBy, err = ref(a.SupersededByWorkItemID)
		return err
	}); err != nil {
		return Preview{}, err
	}

	// Only a WorkItem that can run again has leftovers worth showing.
	if a.Retry == nil || a.Recreate == nil {
		files, _, unavailable, err := s.leftovers(ctx, a)
		if err != nil {
			return Preview{}, err
		}
		out.FilesUnavailableReason = unavailable
		for _, f := range files {
			out.Files = append(out.Files, f)
			if f.OutOfScope {
				out.OutOfScopeCount++
			}
		}
	}
	return out, nil
}
