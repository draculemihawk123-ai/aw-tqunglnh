// Package approvalrelease is the application layer HTTP and the CLI share for V11-07's
// "approve and commit in one action" (ADR-040): it turns what an approver chose on screen into the
// runtime.ApprovalCommit that ResolveApproval carries (reading the git author identity and the
// reviewed patch, both outside any transaction), answers the read-side questions the dialog asks
// (who would the author be, what is parked, how did the commit go), and restores parked files.
// It never commits anything itself; the commit is the background job of internal/app/releasesetcommit.
package approvalrelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/releasesetcommit"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

const (
	maxRepositories = 32
	maxFiles        = 5000
	maxPathBytes    = 4096
	maxMessageBytes = 8192
	// maxReviewedPatchBytes bounds the patch kept with a decision as evidence of what the approver saw.
	maxReviewedPatchBytes = 1 << 20
)

// ErrInvalidCommit is returned (wrapped) for a commit specification that is malformed.
var ErrInvalidCommit = errors.New("approvalrelease: invalid commit specification")

// FileInput is a file the approver chose, with what they saw of it.
type FileInput struct {
	Path        string
	Status      string
	ContentHash string
}

// RepositoryInput is the choice for one repository.
type RepositoryInput struct {
	RepositoryWorkspaceID    string
	ExpectedWorkspaceVersion uint64
	Files                    []FileInput
	// Unselected is KEEP or PARK; empty means KEEP.
	Unselected string
}

// CommitInput is the wire-independent commit specification.
type CommitInput struct {
	Message      string
	Repositories []RepositoryInput
}

// MissingIdentity names a repository whose git author identity is incomplete.
type MissingIdentity struct {
	RepositoryID          string
	RepositoryWorkspaceID string
	MissingName           bool
	MissingEmail          bool
}

// IdentityMissingError is returned by Prepare when any repository to commit has no complete git
// identity. It matches releasesetcommit.ErrCommitIdentityMissing.
type IdentityMissingError struct{ Missing []MissingIdentity }

func (e *IdentityMissingError) Error() string {
	parts := make([]string, 0, len(e.Missing))
	for _, m := range e.Missing {
		what := "user.name and user.email"
		switch {
		case m.MissingName && !m.MissingEmail:
			what = "user.name"
		case m.MissingEmail && !m.MissingName:
			what = "user.email"
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", m.RepositoryID, what))
	}
	return "COMMIT_IDENTITY_MISSING: git is not configured with an author for " + strings.Join(parts, ", ") +
		`; run git config --global user.name "Name" and git config --global user.email "email" on the machine that runs aw`
}

func (e *IdentityMissingError) Unwrap() error { return releasesetcommit.ErrCommitIdentityMissing }

// Preparer builds a runtime.ApprovalCommit from a CommitInput.
type Preparer struct {
	UOW        ports.UnitOfWork
	Identities ports.GitIdentityReader
	Inspection ports.WorkspaceInspectionReader
}

// Prepare validates input, reads each repository's git identity and the reviewed patch (outside any
// transaction) and returns the commit ResolveApproval carries. A specification with no repositories is
// valid: it is the "there is nothing to commit" answer.
func (p Preparer) Prepare(ctx context.Context, input CommitInput) (*runtime.ApprovalCommit, error) {
	message := strings.TrimSpace(input.Message)
	if len(input.Repositories) > maxRepositories {
		return nil, fmt.Errorf("%w: more than %d repositories", ErrInvalidCommit, maxRepositories)
	}
	if len(input.Repositories) > 0 && message == "" {
		return nil, fmt.Errorf("%w: a commit message is required", ErrInvalidCommit)
	}
	if len(message) > maxMessageBytes || strings.ContainsRune(message, 0) {
		return nil, fmt.Errorf("%w: the commit message is too long or contains NUL", ErrInvalidCommit)
	}
	commit := &runtime.ApprovalCommit{Message: message}
	var missing []MissingIdentity
	for _, repository := range input.Repositories {
		if len(repository.Files) == 0 {
			continue
		}
		if len(repository.Files) > maxFiles {
			return nil, fmt.Errorf("%w: more than %d files in one repository", ErrInvalidCommit, maxFiles)
		}
		unselected := workdomain.UnselectedHandling(strings.ToUpper(strings.TrimSpace(repository.Unselected)))
		if unselected == "" {
			unselected = workdomain.UnselectedKeep
		}
		if !unselected.IsValid() {
			return nil, fmt.Errorf("%w: unselected must be KEEP or PARK", ErrInvalidCommit)
		}
		files := make([]workdomain.LocalCommitFile, 0, len(repository.Files))
		for _, file := range repository.Files {
			if strings.TrimSpace(file.Path) == "" || len(file.Path) > maxPathBytes || strings.ContainsRune(file.Path, 0) {
				return nil, fmt.Errorf("%w: a file path is empty, too long or contains NUL", ErrInvalidCommit)
			}
			files = append(files, workdomain.LocalCommitFile{Path: file.Path, Status: file.Status, ContentHash: file.ContentHash})
		}

		var record ports.RepositoryWorkspaceRecord
		if err := p.UOW.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			record, err = tx.Work().GetRepositoryWorkspaceByID(ctx, repository.RepositoryWorkspaceID)
			return err
		}); err != nil {
			return nil, err
		}
		handle, err := ports.NewWorkspaceHandle(record.Workspace.Locator)
		if err != nil {
			return nil, fmt.Errorf("approvalrelease: repository workspace %s has an invalid locator: %w", record.Workspace.ID, err)
		}
		identity, err := p.Identities.ReadGitIdentity(ctx, handle)
		if err != nil {
			return nil, fmt.Errorf("approvalrelease: read the git identity of %s: %w", record.Workspace.RepositoryID, err)
		}
		if !identity.Complete() {
			missing = append(missing, MissingIdentity{
				RepositoryID: string(record.Workspace.RepositoryID), RepositoryWorkspaceID: string(record.Workspace.ID),
				MissingName: identity.Name == "", MissingEmail: identity.Email == "",
			})
			continue
		}
		var patch []byte
		var patchTruncated bool
		if p.Inspection != nil && record.Workspace.CurrentRevision != "" {
			diff, err := p.Inspection.ReadWorkingTreeDiff(ctx, ports.ReadWorkingTreeDiffRequest{
				Handle: handle,
				BaseRevision: workspace.Revision{
					RepositoryID: record.Workspace.RepositoryID, VCSObjectID: record.Workspace.CurrentRevision,
					WorkspaceGeneration: record.Workspace.Generation,
				},
				ByteLimit: maxReviewedPatchBytes, FileLimit: 1,
			})
			if err != nil {
				return nil, fmt.Errorf("approvalrelease: read the reviewed changes of %s: %w", record.Workspace.RepositoryID, err)
			}
			patch, patchTruncated = diff.Patch, diff.PatchTruncated
		}
		commit.Repositories = append(commit.Repositories, runtime.ApprovalCommitRepository{
			RepositoryWorkspaceID: repository.RepositoryWorkspaceID, ExpectedWorkspaceVersion: repository.ExpectedWorkspaceVersion,
			AuthorName: identity.Name, AuthorEmail: identity.Email, Files: files, Unselected: unselected,
			Patch: patch, PatchTruncated: patchTruncated,
		})
	}
	if len(missing) > 0 {
		return nil, &IdentityMissingError{Missing: missing}
	}
	return commit, nil
}

// ---- read side

// IdentityView is one repository's author identity as the dialog shows it.
type IdentityView struct {
	Name        string `json:"name,omitempty"`
	Email       string `json:"email,omitempty"`
	NameSource  string `json:"nameSource,omitempty"`
	EmailSource string `json:"emailSource,omitempty"`
	Ready       bool   `json:"ready"`
	Problem     string `json:"problem,omitempty"`
}

// RepositoryContext is one repository of a family with its author identity.
type RepositoryContext struct {
	RepositoryID          string       `json:"repositoryId"`
	RepositoryWorkspaceID string       `json:"repositoryWorkspaceId"`
	Version               uint64       `json:"version"`
	State                 string       `json:"state"`
	CurrentRevision       string       `json:"currentRevision,omitempty"`
	Identity              IdentityView `json:"identity"`
}

// ParkedSet is a set of files moved out of a worktree by a PARK commit and not restored yet.
type ParkedSet struct {
	ReleaseSetLocalCommitID string   `json:"releaseSetLocalCommitId"`
	RepositoryID            string   `json:"repositoryId"`
	RepositoryWorkspaceID   string   `json:"repositoryWorkspaceId"`
	Ref                     string   `json:"ref"`
	Paths                   []string `json:"paths"`
	ParkedAt                string   `json:"parkedAt"`
}

// ReleaseContext is what the approval dialog needs before it offers "Commit".
type ReleaseContext struct {
	FamilyID     string              `json:"familyId"`
	Repositories []RepositoryContext `json:"repositories"`
	Parked       []ParkedSet         `json:"parked"`
}

// Queries answers the read-side questions.
type Queries struct {
	UOW        ports.UnitOfWork
	Identities ports.GitIdentityReader
}

// FamilyReleaseContext returns, for every READY repository workspace of the family, the git author
// identity a commit would carry, plus the parked sets not restored yet. projectID must own the family.
func (q Queries) FamilyReleaseContext(ctx context.Context, projectID, familyID string) (ReleaseContext, error) {
	result := ReleaseContext{FamilyID: familyID, Repositories: []RepositoryContext{}, Parked: []ParkedSet{}}
	var workspaces []workspace.RepositoryWorkspace
	var parked []workdomain.ParkedChange
	if err := q.UOW.WithReadOnly(ctx, func(tx ports.Tx) error {
		family, err := tx.Work().GetTaskFamily(ctx, familyID)
		if err != nil {
			return err
		}
		if string(family.ProjectID) != projectID {
			return fmt.Errorf("%w: task family %s belongs to project %s, not %s", ports.ErrCrossProjectReference, familyID, family.ProjectID, projectID)
		}
		set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, familyID)
		if err != nil {
			return err
		}
		if workspaces, err = tx.Work().ListWorkspaceSetRepositoryWorkspaces(ctx, string(set.ID)); err != nil {
			return err
		}
		parked, err = tx.Work().ListParkedChanges(ctx, familyID)
		return err
	}); err != nil {
		return ReleaseContext{}, err
	}

	byRepository := map[string]workspace.RepositoryWorkspace{}
	for _, w := range workspaces {
		if w.State != workspace.RepositoryWorkspaceReady {
			continue
		}
		if current, ok := byRepository[string(w.RepositoryID)]; !ok || w.Generation > current.Generation {
			byRepository[string(w.RepositoryID)] = w
		}
	}
	repositoryIDs := make([]string, 0, len(byRepository))
	for id := range byRepository {
		repositoryIDs = append(repositoryIDs, id)
	}
	sort.Strings(repositoryIDs)
	repositoryOfWorkspace := map[string]string{}
	for _, id := range repositoryIDs {
		w := byRepository[id]
		repositoryOfWorkspace[string(w.ID)] = id
		view := RepositoryContext{
			RepositoryID: id, RepositoryWorkspaceID: string(w.ID), Version: w.Version, State: string(w.State), CurrentRevision: w.CurrentRevision,
		}
		view.Identity = q.identityView(ctx, w)
		result.Repositories = append(result.Repositories, view)
	}
	for _, p := range parked {
		result.Parked = append(result.Parked, ParkedSet{
			ReleaseSetLocalCommitID: string(p.ReleaseSetLocalCommitID), RepositoryID: p.RepositoryID, RepositoryWorkspaceID: p.RepositoryWorkspaceID,
			Ref: p.Ref, Paths: p.Paths, ParkedAt: p.ParkedAt.UTC().Format(time.RFC3339),
		})
	}
	return result, nil
}

func (q Queries) identityView(ctx context.Context, w workspace.RepositoryWorkspace) IdentityView {
	handle, err := ports.NewWorkspaceHandle(w.Locator)
	if err != nil || q.Identities == nil {
		return IdentityView{Problem: "the git identity cannot be read for this workspace"}
	}
	identity, err := q.Identities.ReadGitIdentity(ctx, handle)
	if err != nil {
		return IdentityView{Problem: "the git identity cannot be read: " + err.Error()}
	}
	view := IdentityView{
		Name: identity.Name, Email: identity.Email, NameSource: identity.NameSource, EmailSource: identity.EmailSource, Ready: identity.Complete(),
	}
	switch {
	case identity.Name == "" && identity.Email == "":
		view.Problem = "git user.name and user.email are not set"
	case identity.Name == "":
		view.Problem = "git user.name is not set"
	case identity.Email == "":
		view.Problem = "git user.email is not set"
	}
	return view
}

// OperationView is one commit operation a decision requested.
type OperationView struct {
	ReleaseSetLocalCommitID string   `json:"releaseSetLocalCommitId"`
	ReleaseSetID            string   `json:"releaseSetId"`
	RepositoryID            string   `json:"repositoryId"`
	RepositoryWorkspaceID   string   `json:"repositoryWorkspaceId"`
	State                   string   `json:"state"`
	FailureReason           string   `json:"failureReason,omitempty"`
	ParentVCSObjectID       string   `json:"parentVcsObjectId,omitempty"`
	ResultVCSObjectID       string   `json:"resultVcsObjectId,omitempty"`
	Files                   []string `json:"files"`
	ChangeSetDigest         string   `json:"changeSetDigest"`
	Unselected              string   `json:"unselected"`
	ParkedRef               string   `json:"parkedRef,omitempty"`
	ParkedPaths             []string `json:"parkedPaths,omitempty"`
	Restored                bool     `json:"restored,omitempty"`
	PatchTruncated          bool     `json:"patchTruncated,omitempty"`
	// ReviewedPatch is the working-tree patch the approver saw (bounded), base64 on the wire.
	ReviewedPatch []byte `json:"reviewedPatch,omitempty"`
}

// ApprovalRelease is the status of the commits one decision requested.
type ApprovalRelease struct {
	ApprovalRequestID string          `json:"approvalRequestId"`
	ProjectID         string          `json:"projectId"`
	Operations        []OperationView `json:"operations"`
}

// ApprovalRelease returns the operations approvalRequestID requested, with what the approver reviewed.
func (q Queries) ApprovalRelease(ctx context.Context, runID, approvalRequestID string) (ApprovalRelease, error) {
	result := ApprovalRelease{ApprovalRequestID: approvalRequestID, Operations: []OperationView{}}
	err := q.UOW.WithReadOnly(ctx, func(tx ports.Tx) error {
		request, err := tx.Approvals().GetApprovalRequest(ctx, approvalRequestID)
		if err != nil {
			return err
		}
		if string(request.RunID) != runID {
			return fmt.Errorf("%w: approval request %s", ports.ErrPersistenceNotFound, approvalRequestID)
		}
		result.ProjectID = string(request.ProjectID)
		selections, err := tx.Work().ListReleaseSetLocalCommitSelectionsByApproval(ctx, approvalRequestID)
		if err != nil {
			return err
		}
		for _, selection := range selections {
			operation, err := tx.Work().GetReleaseSetLocalCommit(ctx, string(selection.ReleaseSetLocalCommitID))
			if err != nil {
				return err
			}
			files := make([]string, 0, len(selection.Files))
			for _, file := range selection.Files {
				files = append(files, file.Path)
			}
			result.Operations = append(result.Operations, OperationView{
				ReleaseSetLocalCommitID: string(operation.ID), ReleaseSetID: string(operation.ReleaseSetID),
				RepositoryID: string(operation.RepositoryID), RepositoryWorkspaceID: operation.RepositoryWorkspaceID,
				State: string(operation.State), FailureReason: string(operation.FailureReason),
				ParentVCSObjectID: operation.ParentVCSObjectID, ResultVCSObjectID: operation.ResultVCSObjectID,
				Files: files, ChangeSetDigest: selection.ChangeSetDigest, Unselected: string(selection.Unselected),
				ParkedRef: selection.ParkedRef, ParkedPaths: selection.ParkedPaths, Restored: selection.RestoredAt != nil,
				PatchTruncated: selection.PatchTruncated, ReviewedPatch: selection.Patch,
			})
		}
		return nil
	})
	return result, err
}

// ---- restore

// ErrNothingToRestore is returned when the operation parked nothing, or what it parked was restored.
var ErrNothingToRestore = errors.New("approvalrelease: nothing is parked for this commit operation")

// ErrOperationRequired is returned when the restore request names no commit operation.
var ErrOperationRequired = errors.New("approvalrelease: ReleaseSetLocalCommitID is required")

// ErrNoParker is returned when the process has no Git workspace provider to restore with.
var ErrNoParker = errors.New("approvalrelease: this process has no Git workspace provider, so it cannot restore parked changes")

// ErrWorkspaceBusy is returned when an attempt holds the workspace's write lease.
var ErrWorkspaceBusy = errors.New("approvalrelease: an attempt is writing to this repository workspace")

// RestoreDeps are RestoreParked's collaborators.
type RestoreDeps struct {
	UOW    ports.UnitOfWork
	Parker ports.UnselectedChangeParker
}

// RestoreRequest names the operation whose parked files come back.
type RestoreRequest struct {
	ProjectID               string
	RepositoryWorkspaceID   string
	ReleaseSetLocalCommitID string
}

// RestoreResult is RestoreParked's idempotent result.
type RestoreResult struct {
	ReleaseSetLocalCommitID string   `json:"releaseSetLocalCommitId"`
	Ref                     string   `json:"ref"`
	RestoredPaths           []string `json:"restoredPaths"`
}

// RestoreParked puts the files a PARK commit moved out of the worktree back, then records it. It
// refuses while an attempt writes to the workspace, and refuses (ports.ErrParkedRestoreConflict) to
// overwrite anything changed since. A crash between the Git restore and the record is repaired by a retry:
// the ref is then gone (ports.ErrNothingParked) and the operation is simply marked restored.
func RestoreParked(ctx context.Context, deps RestoreDeps, cmd ports.Command, req RestoreRequest) (RestoreResult, error) {
	if strings.TrimSpace(req.ProjectID) == "" || strings.TrimSpace(req.RepositoryWorkspaceID) == "" {
		return RestoreResult{}, errors.New("approvalrelease: ProjectID and RepositoryWorkspaceID are required")
	}
	var result RestoreResult
	var replayed bool
	var ref string
	var handle ports.WorkspaceHandle
	if err := deps.UOW.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		receipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if found {
			if receipt.RequestHash != cmd.RequestHash {
				return ports.ErrReceiptConflict
			}
			replayed = true
			return json.Unmarshal([]byte(receipt.ResultJSON), &result)
		}
		record, err := tx.Work().GetRepositoryWorkspaceByID(ctx, req.RepositoryWorkspaceID)
		if err != nil {
			return err
		}
		family, err := tx.Work().GetTaskFamily(ctx, record.FamilyID)
		if err != nil {
			return err
		}
		if string(family.ProjectID) != req.ProjectID {
			return fmt.Errorf("%w: repository workspace %s", ports.ErrCrossProjectReference, req.RepositoryWorkspaceID)
		}
		// Ownership first: a caller of another project learns nothing from a missing operation id.
		if strings.TrimSpace(req.ReleaseSetLocalCommitID) == "" {
			return ErrOperationRequired
		}
		operation, err := tx.Work().GetReleaseSetLocalCommit(ctx, req.ReleaseSetLocalCommitID)
		if err != nil {
			return err
		}
		if operation.RepositoryWorkspaceID != req.RepositoryWorkspaceID {
			return fmt.Errorf("%w: operation %s", ports.ErrPersistenceNotFound, req.ReleaseSetLocalCommitID)
		}
		selection, found, err := tx.Work().GetReleaseSetLocalCommitSelection(ctx, req.ReleaseSetLocalCommitID)
		if err != nil {
			return err
		}
		if !found || selection.ParkedRef == "" || selection.RestoredAt != nil {
			return ErrNothingToRestore
		}
		busy, err := tx.Work().HasActiveWriteLease(ctx, []string{req.RepositoryWorkspaceID})
		if err != nil {
			return err
		}
		if busy {
			return ErrWorkspaceBusy
		}
		ref = selection.ParkedRef
		handle, err = ports.NewWorkspaceHandle(record.Workspace.Locator)
		return err
	}); err != nil {
		return RestoreResult{}, err
	}
	if replayed {
		return result, nil
	}
	if deps.Parker == nil {
		return RestoreResult{}, ErrNoParker
	}

	restored, restoreErr := deps.Parker.RestoreParkedChanges(ctx, handle, ref)
	if restoreErr != nil && !errors.Is(restoreErr, ports.ErrNothingParked) {
		return RestoreResult{}, restoreErr
	}
	sort.Strings(restored)
	result = RestoreResult{ReleaseSetLocalCommitID: req.ReleaseSetLocalCommitID, Ref: ref, RestoredPaths: restored}
	if result.RestoredPaths == nil {
		result.RestoredPaths = []string{}
	}
	err := deps.UOW.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if err := tx.Work().MarkReleaseSetLocalCommitParkedRestored(ctx, req.ReleaseSetLocalCommitID, cmd.RequestedAt); err != nil {
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
	return result, err
}
