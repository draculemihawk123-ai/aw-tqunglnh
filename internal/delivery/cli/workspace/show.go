package workspace

import (
	"context"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/workspacestate"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"workspace-set", "show"}, Scope: cli.ScopeProject,
		AppOperation: "GetWorkspaceSetState", HTTPOperationID: "getWorkspaceSetState",
	})
}

// repositoryWorkspaceStateResult is this package's own delivery-owned wire
// DTO for one workspacestate.RepositoryWorkspaceState — a fresh, camelCase-
// tagged shape (workspacestate's own type carries no json tags of its own),
// mirroring internal/delivery/httpapi/workspacestate.go's own
// repositoryWorkspaceStateResponse minus its ValidActions field (an
// HTTP-specific advisory-action concept — see this package's own doc.go for
// why this package returns state/lease/fence/quarantine only, never an
// advisory action list of its own).
type repositoryWorkspaceStateResult struct {
	RepositoryWorkspaceID  string  `json:"repositoryWorkspaceId"`
	WorkspaceSetID         string  `json:"workspaceSetId"`
	RepositoryID           string  `json:"repositoryId"`
	Generation             uint64  `json:"generation"`
	State                  string  `json:"state"`
	Version                uint64  `json:"version"`
	BranchRef              string  `json:"branchRef,omitempty"`
	CurrentRevision        string  `json:"currentRevision,omitempty"`
	LastProvisionErrorCode *string `json:"lastProvisionErrorCode,omitempty"`
	HasActiveWriteLease    bool    `json:"hasActiveWriteLease"`
}

func toRepositoryWorkspaceStateResult(rw workspacestate.RepositoryWorkspaceState) repositoryWorkspaceStateResult {
	return repositoryWorkspaceStateResult{
		RepositoryWorkspaceID: rw.RepositoryWorkspaceID, WorkspaceSetID: rw.WorkspaceSetID, RepositoryID: rw.RepositoryID,
		Generation: rw.Generation, State: string(rw.State), Version: rw.Version, BranchRef: rw.BranchRef,
		CurrentRevision: rw.CurrentRevision, LastProvisionErrorCode: rw.LastProvisionErrorCode, HasActiveWriteLease: rw.HasActiveWriteLease,
	}
}

// workspaceSetStateResult is this package's own delivery-owned wire DTO for
// a whole workspacestate.WorkspaceSetState, mirroring
// internal/delivery/httpapi/workspacestate.go's own workspaceSetStateResponse
// minus ValidActions.
type workspaceSetStateResult struct {
	WorkspaceSetID       string                           `json:"workspaceSetId"`
	FamilyID             string                           `json:"familyId"`
	ProjectID            string                           `json:"projectId"`
	State                string                           `json:"state"`
	Version              uint64                           `json:"version"`
	HasBaseRevisionSet   bool                             `json:"hasBaseRevisionSet"`
	RepositoryWorkspaces []repositoryWorkspaceStateResult `json:"repositoryWorkspaces"`
}

func toWorkspaceSetStateResult(state workspacestate.WorkspaceSetState) workspaceSetStateResult {
	repos := make([]repositoryWorkspaceStateResult, 0, len(state.RepositoryWorkspaces))
	for _, rw := range state.RepositoryWorkspaces {
		repos = append(repos, toRepositoryWorkspaceStateResult(rw))
	}
	return workspaceSetStateResult{
		WorkspaceSetID: state.WorkspaceSetID, FamilyID: state.FamilyID, ProjectID: state.ProjectID,
		State: string(state.State), Version: state.Version, HasBaseRevisionSet: state.HasBaseRevisionSet,
		RepositoryWorkspaces: repos,
	}
}

// RunWorkspaceSetShow implements `aw workspace-set show <familyId>
// --project-id <projectId>` — a read-only query over
// workspacestate.GetWorkspaceSetState: the WorkspaceSet's own state/
// version/HasBaseRevisionSet plus every one of its own RepositoryWorkspace
// children's own state/lease/fence/quarantine (V6-10B's own "state/lease/
// fence/quarantine" vocabulary — see workspacestate's own package doc
// comment). Errors (ports.ErrPersistenceNotFound, workspacestate.ErrScopeMismatch)
// are returned verbatim, unmapped — this package's own opacity policy
// (mapInspectionQueryError, helpers.go) applies only to the three
// Git-content-inspection queries below (source/diff/log), never to this
// plain state read, which never touches Git/the filesystem and so has
// nothing adapter-internal left to hide.
func RunWorkspaceSetShow(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("workspace-set show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw workspace-set show <familyId> --project-id <projectId>")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}
	familyID := positional[0]

	state, err := workspacestate.GetWorkspaceSetState(ctx, deps.UOW, workspacestate.GetWorkspaceSetStateRequest{
		ProjectID: *projectID, FamilyID: familyID,
	})
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, toWorkspaceSetStateResult(state))
}
