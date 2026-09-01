package ports

import (
	"context"
	"encoding"
	"errors"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// WorkspaceHandle is an opaque, path-free reference. Callers may persist and
// compare it, but only the WorkspaceProvider that issued it may interpret it.
type WorkspaceHandle struct {
	token string
}

var _ encoding.TextMarshaler = WorkspaceHandle{}
var _ encoding.TextUnmarshaler = (*WorkspaceHandle)(nil)

func NewWorkspaceHandle(token string) (WorkspaceHandle, error) {
	if token == "" || token != strings.TrimSpace(token) {
		return WorkspaceHandle{}, errors.New("workspace handle token is invalid")
	}
	return WorkspaceHandle{token: token}, nil
}

func (h WorkspaceHandle) String() string {
	return h.token
}

func (h WorkspaceHandle) IsZero() bool {
	return h.token == ""
}

func (h WorkspaceHandle) MarshalText() ([]byte, error) {
	if h.IsZero() {
		return nil, errors.New("workspace handle is empty")
	}
	return []byte(h.token), nil
}

func (h *WorkspaceHandle) UnmarshalText(text []byte) error {
	if h == nil {
		return errors.New("workspace handle target is nil")
	}
	parsed, err := NewWorkspaceHandle(string(text))
	if err != nil {
		return err
	}
	*h = parsed
	return nil
}

type ProvisionSpec struct {
	RepositoryID    project.RepositoryID
	LocalRepository string
	BaseRef         string
	FamilyID        work.TaskFamilyID
	WorkspaceSetID  workspace.WorkspaceSetID
	Generation      uint64
}

type WorkspaceState string

const (
	WorkspaceReady    WorkspaceState = "READY"
	WorkspaceReleased WorkspaceState = "RELEASED"
)

type WorkspaceInspection struct {
	Handle          WorkspaceHandle
	RepositoryID    project.RepositoryID
	FamilyID        work.TaskFamilyID
	WorkspaceSetID  workspace.WorkspaceSetID
	Generation      uint64
	BranchRef       string
	BaseRevision    workspace.Revision
	CurrentRevision workspace.Revision
	State           WorkspaceState
	Dirty           bool
}

type FileStatus struct {
	Code         string
	Path         string
	OriginalPath string
}

type WorkspaceDiff struct {
	RepositoryID    project.RepositoryID
	BaseRevision    workspace.Revision
	CurrentRevision workspace.Revision
	Files           []FileStatus
	Patch           []byte
}

// WorkspaceProvider owns all interpretation of WorkspaceHandle. In particular,
// application code must not derive an OS path from a handle.
type WorkspaceProvider interface {
	Provision(context.Context, ProvisionSpec) (WorkspaceHandle, error)
	Inspect(context.Context, WorkspaceHandle) (WorkspaceInspection, error)
	CaptureRevision(context.Context, WorkspaceHandle) (workspace.Revision, error)
	Diff(context.Context, WorkspaceHandle, workspace.Revision) (WorkspaceDiff, error)
	Release(context.Context, WorkspaceHandle) error
}
