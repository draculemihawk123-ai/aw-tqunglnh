package releasesetcommit

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// This file is V11-07's approval-bound release (ADR-040, completing ADR-014): the approval decision
// and the request to commit are one transaction. RequestApprovalBoundCommitsTx is called by
// internal/app/runtime.ResolveApproval inside the decision's own transaction; it only writes
// durable intent rows — a ReleaseSet, sealed, and one REQUESTED ReleaseSetLocalCommit with its job
// per repository — and never touches Git (docs/architecture/04-go-core-spec.md §11.1). The commit
// itself is the same background job a manual commit uses (execute.go).

var (
	// ErrCommitIdentityMissing is returned when a repository to commit has no author name or email.
	// The author is the git identity configured on the machine that runs aw, read before the
	// transaction (ports.GitIdentityReader); a decision never commits as an invented identity.
	ErrCommitIdentityMissing = errors.New("releasesetcommit: the commit author (git user.name / user.email) is not configured")
	// ErrDuplicateRepository is returned when two entries name the same repository.
	ErrDuplicateRepository = errors.New("releasesetcommit: a repository appears twice in one commit request")
)

// ApprovalBoundRepository is one repository of an approval-bound commit.
type ApprovalBoundRepository struct {
	RepositoryWorkspaceID    string
	ExpectedWorkspaceVersion uint64
	AuthorName               string
	AuthorEmail              string
	Files                    []workdomain.LocalCommitFile
	Unselected               workdomain.UnselectedHandling
	// Patch is the bounded unified diff of Files as the approver saw it, kept with the operation.
	Patch          []byte
	PatchTruncated bool
}

// ApprovalBoundRequest is what ResolveApproval supplies once its decision won.
type ApprovalBoundRequest struct {
	ProjectID         string
	FamilyID          string
	WorkItemID        string
	RunID             string
	ApprovalRequestID string
	Message           string
	Repositories      []ApprovalBoundRepository
}

// ApprovalBoundOperation is one repository's REQUESTED operation.
type ApprovalBoundOperation struct {
	RepositoryWorkspaceID   string `json:"repositoryWorkspaceId"`
	RepositoryID            string `json:"repositoryId"`
	ReleaseSetLocalCommitID string `json:"releaseSetLocalCommitId"`
	JobID                   string `json:"jobId"`
	Marker                  string `json:"marker"`
}

// ApprovalBoundResult lists what was requested. Operations is empty only when Repositories was.
type ApprovalBoundResult struct {
	ReleaseSetID string                   `json:"releaseSetId,omitempty"`
	Operations   []ApprovalBoundOperation `json:"operations,omitempty"`
}

// RequestApprovalBoundCommitsTx creates a ReleaseSet for the repositories (base and result both the
// workspace's current revision, verdict PASS — the same shape the kit's commit-task.sh uses), seals
// it, and requests one selective local commit per repository. All writes go through tx, so they
// commit or roll back with the approval decision that called this.
func RequestApprovalBoundCommitsTx(
	ctx context.Context, tx ports.Tx, ids idsource.Source, cmd ports.Command, req ApprovalBoundRequest,
) (ApprovalBoundResult, error) {
	if strings.TrimSpace(req.ProjectID) == "" || strings.TrimSpace(req.FamilyID) == "" || strings.TrimSpace(req.ApprovalRequestID) == "" {
		return ApprovalBoundResult{}, errors.New("releasesetcommit: ProjectID, FamilyID and ApprovalRequestID are required")
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		return ApprovalBoundResult{}, errors.New("releasesetcommit: a commit message is required")
	}
	if len(req.Repositories) == 0 {
		return ApprovalBoundResult{}, nil
	}
	repositories := append([]ApprovalBoundRepository(nil), req.Repositories...)
	sort.Slice(repositories, func(i, j int) bool { return repositories[i].RepositoryWorkspaceID < repositories[j].RepositoryWorkspaceID })

	type target struct {
		repository ApprovalBoundRepository
		record     ports.RepositoryWorkspaceRecord
	}
	targets := make([]target, 0, len(repositories))
	entries := make([]workdomain.RepositoryRelease, 0, len(repositories))
	seen := map[project.RepositoryID]bool{}
	for _, repository := range repositories {
		if len(repository.Files) == 0 {
			return ApprovalBoundResult{}, fmt.Errorf("releasesetcommit: repository workspace %s has no files selected", repository.RepositoryWorkspaceID)
		}
		if strings.TrimSpace(repository.AuthorName) == "" || strings.TrimSpace(repository.AuthorEmail) == "" {
			return ApprovalBoundResult{}, fmt.Errorf("%w: repository workspace %s", ErrCommitIdentityMissing, repository.RepositoryWorkspaceID)
		}
		if !repository.Unselected.IsValid() {
			return ApprovalBoundResult{}, fmt.Errorf("releasesetcommit: repository workspace %s: unselected must be KEEP or PARK", repository.RepositoryWorkspaceID)
		}
		record, err := tx.Work().GetRepositoryWorkspaceByID(ctx, repository.RepositoryWorkspaceID)
		if err != nil {
			return ApprovalBoundResult{}, err
		}
		if record.FamilyID != req.FamilyID {
			return ApprovalBoundResult{}, fmt.Errorf("%w: repository workspace %s belongs to family %s, not %s",
				ports.ErrCrossProjectReference, repository.RepositoryWorkspaceID, record.FamilyID, req.FamilyID)
		}
		if record.Workspace.Version != repository.ExpectedWorkspaceVersion {
			return ApprovalBoundResult{}, fmt.Errorf("%w: repository workspace %s expected version %d, currently %d",
				ports.ErrOptimisticConflict, repository.RepositoryWorkspaceID, repository.ExpectedWorkspaceVersion, record.Workspace.Version)
		}
		if record.Workspace.State != workspace.RepositoryWorkspaceReady {
			return ApprovalBoundResult{}, fmt.Errorf("%w: repository workspace %s is %s", ErrWorkspaceNotReady, repository.RepositoryWorkspaceID, record.Workspace.State)
		}
		if record.Workspace.CurrentRevision == "" {
			return ApprovalBoundResult{}, fmt.Errorf("%w: repository workspace %s has no recorded revision", ErrWorkspaceNotReady, repository.RepositoryWorkspaceID)
		}
		if seen[record.Workspace.RepositoryID] {
			return ApprovalBoundResult{}, fmt.Errorf("%w: %s", ErrDuplicateRepository, record.Workspace.RepositoryID)
		}
		seen[record.Workspace.RepositoryID] = true
		entries = append(entries, workdomain.RepositoryRelease{
			RepositoryID: record.Workspace.RepositoryID, BaseVCSObjectID: record.Workspace.CurrentRevision,
			ResultVCSObjectID: record.Workspace.CurrentRevision, Verdict: gate.VerdictPass,
		})
		targets = append(targets, target{repository: repository, record: record})
	}

	releaseSet, err := workdomain.NewReleaseSet(
		workdomain.ReleaseSetID(ids.NewID()), project.ProjectID(req.ProjectID), workdomain.TaskFamilyID(req.FamilyID), entries, cmd.RequestedAt,
	)
	if err != nil {
		return ApprovalBoundResult{}, err
	}
	created, err := tx.Work().CreateReleaseSet(ctx, releaseSet)
	if err != nil {
		return ApprovalBoundResult{}, err
	}
	sealed, err := tx.Work().TransitionReleaseSetState(ctx, ports.TransitionReleaseSetStateRequest{
		ReleaseSetID: string(created.ID), ExpectedState: workdomain.ReleaseSetCreated, ExpectedVersion: created.Version,
		NextState: workdomain.ReleaseSetSealed, OccurredAt: cmd.RequestedAt,
	})
	if err != nil {
		return ApprovalBoundResult{}, err
	}

	result := ApprovalBoundResult{ReleaseSetID: string(sealed.ID)}
	for _, t := range targets {
		eventID := cmd.ID + "-requested-" + string(t.record.Workspace.ID)
		operation, err := requestLocalCommitTx(ctx, tx, ids, cmd, RequestReleaseSetLocalCommitRequest{
			ProjectID: req.ProjectID, ReleaseSetID: string(sealed.ID), ExpectedReleaseSetVersion: sealed.Version,
			RepositoryWorkspaceID: t.repository.RepositoryWorkspaceID, ExpectedWorkspaceVersion: t.repository.ExpectedWorkspaceVersion,
			Message: message, AuthorName: t.repository.AuthorName, AuthorEmail: t.repository.AuthorEmail,
			Selection: &LocalCommitSelectionInput{
				Files: t.repository.Files, Unselected: t.repository.Unselected, Patch: t.repository.Patch,
				PatchTruncated: t.repository.PatchTruncated, ApprovalRequestID: req.ApprovalRequestID,
				WorkItemID: req.WorkItemID, RunID: req.RunID,
			},
		}, eventID)
		if err != nil {
			return ApprovalBoundResult{}, err
		}
		result.Operations = append(result.Operations, ApprovalBoundOperation{
			RepositoryWorkspaceID: t.repository.RepositoryWorkspaceID, RepositoryID: string(t.record.Workspace.RepositoryID),
			ReleaseSetLocalCommitID: operation.ReleaseSetLocalCommitID, JobID: operation.JobID, Marker: operation.Marker,
		})
	}
	return result, nil
}
