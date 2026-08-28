package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

type WorkspaceSetID string
type RepositoryWorkspaceID string

type WorkspaceSetState string

const (
	WorkspaceSetRequested    WorkspaceSetState = "REQUESTED"
	WorkspaceSetProvisioning WorkspaceSetState = "PROVISIONING"
	WorkspaceSetReady        WorkspaceSetState = "READY"
	WorkspaceSetBlocked      WorkspaceSetState = "BLOCKED"
	WorkspaceSetReleasing    WorkspaceSetState = "RELEASING"
	WorkspaceSetReleased     WorkspaceSetState = "RELEASED"
	WorkspaceSetFailed       WorkspaceSetState = "FAILED"
)

type WorkspaceSet struct {
	ID        WorkspaceSetID
	ProjectID project.ProjectID
	FamilyID  work.TaskFamilyID
	State     WorkspaceSetState
	Version   uint64
}

func NewWorkspaceSet(id WorkspaceSetID, family work.TaskFamily) (WorkspaceSet, error) {
	if id == "" {
		return WorkspaceSet{}, errors.New("workspace set id is required")
	}
	if family.ID == "" || family.ProjectID == "" {
		return WorkspaceSet{}, errors.New("task family is invalid")
	}
	return WorkspaceSet{
		ID:        id,
		ProjectID: family.ProjectID,
		FamilyID:  family.ID,
		State:     WorkspaceSetRequested,
		Version:   1,
	}, nil
}

type RepositoryWorkspaceState string

const (
	RepositoryWorkspaceProvisioning RepositoryWorkspaceState = "PROVISIONING"
	RepositoryWorkspaceReady        RepositoryWorkspaceState = "READY"
	RepositoryWorkspaceQuarantined  RepositoryWorkspaceState = "QUARANTINED"
	RepositoryWorkspaceReleasing    RepositoryWorkspaceState = "RELEASING"
	RepositoryWorkspaceReleased     RepositoryWorkspaceState = "RELEASED"
	RepositoryWorkspaceFailed       RepositoryWorkspaceState = "FAILED"
)

type RepositoryWorkspace struct {
	ID              RepositoryWorkspaceID
	WorkspaceSetID  WorkspaceSetID
	RepositoryID    project.RepositoryID
	Generation      uint64
	Locator         string
	BranchRef       string
	BaseRevision    string
	CurrentRevision string
	State           RepositoryWorkspaceState
	Version         uint64
}

func NewRepositoryWorkspace(
	id RepositoryWorkspaceID,
	set WorkspaceSet,
	repository project.Repository,
	generation uint64,
	locator string,
	branchRef string,
	baseRevision string,
) (RepositoryWorkspace, error) {
	if id == "" {
		return RepositoryWorkspace{}, errors.New("repository workspace id is required")
	}
	if set.ID == "" || set.ProjectID == "" || set.FamilyID == "" {
		return RepositoryWorkspace{}, errors.New("workspace set is invalid")
	}
	if repository.ID == "" || repository.ProjectID != set.ProjectID {
		return RepositoryWorkspace{}, errors.New("repository must belong to the workspace set project")
	}
	if generation == 0 {
		return RepositoryWorkspace{}, errors.New("workspace generation must be greater than zero")
	}
	locator = strings.TrimSpace(locator)
	baseRevision = strings.TrimSpace(baseRevision)
	if locator == "" || baseRevision == "" {
		return RepositoryWorkspace{}, errors.New("workspace locator and base revision are required")
	}

	return RepositoryWorkspace{
		ID:             id,
		WorkspaceSetID: set.ID,
		RepositoryID:   repository.ID,
		Generation:     generation,
		Locator:        locator,
		BranchRef:      strings.TrimSpace(branchRef),
		BaseRevision:   baseRevision,
		State:          RepositoryWorkspaceProvisioning,
		Version:        1,
	}, nil
}

func ValidateRepositoryWorkspaces(
	set WorkspaceSet,
	family work.TaskFamily,
	familyScopes []work.RepositoryScope,
	workspaces []RepositoryWorkspace,
) error {
	if set.FamilyID != family.ID || set.ProjectID != family.ProjectID {
		return errors.New("workspace set does not belong to the task family")
	}
	allowedRepositories := make(map[project.RepositoryID]struct{}, len(familyScopes))
	for _, scope := range familyScopes {
		if scope.FamilyID() != family.ID {
			return fmt.Errorf("scope for repository %q belongs to another task family", scope.RepositoryID())
		}
		allowedRepositories[scope.RepositoryID()] = struct{}{}
	}

	type workspaceKey struct {
		repositoryID project.RepositoryID
		generation   uint64
	}
	seen := make(map[workspaceKey]struct{}, len(workspaces))
	for _, repositoryWorkspace := range workspaces {
		if repositoryWorkspace.WorkspaceSetID != set.ID {
			return fmt.Errorf("repository workspace %q belongs to another workspace set", repositoryWorkspace.ID)
		}
		if _, allowed := allowedRepositories[repositoryWorkspace.RepositoryID]; !allowed {
			return fmt.Errorf("repository %q is outside task family scope", repositoryWorkspace.RepositoryID)
		}
		key := workspaceKey{
			repositoryID: repositoryWorkspace.RepositoryID,
			generation:   repositoryWorkspace.Generation,
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf(
				"duplicate repository workspace for repository %q generation %d",
				repositoryWorkspace.RepositoryID,
				repositoryWorkspace.Generation,
			)
		}
		seen[key] = struct{}{}
	}
	return nil
}

type Revision struct {
	RepositoryID        project.RepositoryID `json:"repositoryId"`
	VCSObjectID         string               `json:"vcsObjectId"`
	WorkspaceGeneration uint64               `json:"workspaceGeneration"`
}

type RevisionSet struct {
	entries     []Revision
	contentHash string
}

func NewRevisionSet(entries []Revision) (RevisionSet, error) {
	normalized := append([]Revision(nil), entries...)
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].RepositoryID < normalized[j].RepositoryID
	})

	for index := range normalized {
		normalized[index].VCSObjectID = strings.TrimSpace(normalized[index].VCSObjectID)
		if normalized[index].RepositoryID == "" || normalized[index].VCSObjectID == "" {
			return RevisionSet{}, errors.New("revision repository id and VCS object id are required")
		}
		if normalized[index].WorkspaceGeneration == 0 {
			return RevisionSet{}, fmt.Errorf(
				"revision for repository %q has invalid workspace generation",
				normalized[index].RepositoryID,
			)
		}
		if index > 0 && normalized[index-1].RepositoryID == normalized[index].RepositoryID {
			return RevisionSet{}, fmt.Errorf("duplicate revision for repository %q", normalized[index].RepositoryID)
		}
	}

	canonical, err := json.Marshal(normalized)
	if err != nil {
		return RevisionSet{}, fmt.Errorf("marshal revision set: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return RevisionSet{
		entries:     normalized,
		contentHash: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func (s RevisionSet) Entries() []Revision {
	return append([]Revision(nil), s.entries...)
}

func (s RevisionSet) ContentHash() string {
	return s.contentHash
}

func (s RevisionSet) RevisionFor(repositoryID project.RepositoryID) (Revision, bool) {
	index := sort.Search(len(s.entries), func(i int) bool {
		return s.entries[i].RepositoryID >= repositoryID
	})
	if index >= len(s.entries) || s.entries[index].RepositoryID != repositoryID {
		return Revision{}, false
	}
	return s.entries[index], true
}
