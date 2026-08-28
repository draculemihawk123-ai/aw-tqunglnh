package work

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

type WorkItemID string
type TaskFamilyID string

type WorkItemKind string

const (
	WorkItemRoot  WorkItemKind = "ROOT"
	WorkItemChild WorkItemKind = "CHILD"
)

type WorkItemStatus string

const (
	WorkItemBacklog   WorkItemStatus = "BACKLOG"
	WorkItemReady     WorkItemStatus = "READY"
	WorkItemActive    WorkItemStatus = "ACTIVE"
	WorkItemBlocked   WorkItemStatus = "BLOCKED"
	WorkItemDone      WorkItemStatus = "DONE"
	WorkItemCancelled WorkItemStatus = "CANCELLED"
)

type WorkItem struct {
	ID        WorkItemID
	ProjectID project.ProjectID
	Kind      WorkItemKind
	ParentID  *WorkItemID
	FamilyID  TaskFamilyID
	Title     string
	Status    WorkItemStatus
	Version   uint64
}

func NewRootWorkItem(
	id WorkItemID,
	projectID project.ProjectID,
	familyID TaskFamilyID,
	title string,
) (WorkItem, error) {
	if id == "" || projectID == "" || familyID == "" {
		return WorkItem{}, errors.New("root work item id, project id and family id are required")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return WorkItem{}, errors.New("work item title is required")
	}
	return WorkItem{
		ID:        id,
		ProjectID: projectID,
		Kind:      WorkItemRoot,
		FamilyID:  familyID,
		Title:     title,
		Status:    WorkItemBacklog,
		Version:   1,
	}, nil
}

func NewChildWorkItem(id WorkItemID, parent WorkItem, title string) (WorkItem, error) {
	if id == "" {
		return WorkItem{}, errors.New("child work item id is required")
	}
	if parent.ID == "" || parent.ProjectID == "" || parent.FamilyID == "" {
		return WorkItem{}, errors.New("parent work item is invalid")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return WorkItem{}, errors.New("work item title is required")
	}
	parentID := parent.ID
	return WorkItem{
		ID:        id,
		ProjectID: parent.ProjectID,
		Kind:      WorkItemChild,
		ParentID:  &parentID,
		FamilyID:  parent.FamilyID,
		Title:     title,
		Status:    WorkItemBacklog,
		Version:   1,
	}, nil
}

type TaskFamilyStatus string

const (
	TaskFamilyActive    TaskFamilyStatus = "ACTIVE"
	TaskFamilyBlocked   TaskFamilyStatus = "BLOCKED"
	TaskFamilyCompleted TaskFamilyStatus = "COMPLETED"
	TaskFamilyCancelled TaskFamilyStatus = "CANCELLED"
)

type TaskFamily struct {
	ID             TaskFamilyID
	ProjectID      project.ProjectID
	RootWorkItemID WorkItemID
	ScopeVersion   uint64
	Status         TaskFamilyStatus
	Version        uint64
}

func NewTaskFamily(id TaskFamilyID, root WorkItem) (TaskFamily, error) {
	if id == "" {
		return TaskFamily{}, errors.New("task family id is required")
	}
	if root.Kind != WorkItemRoot || root.ParentID != nil {
		return TaskFamily{}, errors.New("task family root must be a root work item")
	}
	if root.FamilyID != id {
		return TaskFamily{}, errors.New("root work item family id does not match task family id")
	}
	return TaskFamily{
		ID:             id,
		ProjectID:      root.ProjectID,
		RootWorkItemID: root.ID,
		ScopeVersion:   1,
		Status:         TaskFamilyActive,
		Version:        1,
	}, nil
}

type RepositoryAccess string

const (
	RepositoryRead  RepositoryAccess = "READ"
	RepositoryWrite RepositoryAccess = "WRITE"
)

type RepositoryScope struct {
	familyID            TaskFamilyID
	addedInScopeVersion uint64
	repositoryID        project.RepositoryID
	access              RepositoryAccess
	pathScopes          []string
	reason              string
	addedBy             string
	addedAt             time.Time
}

func NewRepositoryScope(
	familyID TaskFamilyID,
	addedInScopeVersion uint64,
	repositoryID project.RepositoryID,
	access RepositoryAccess,
	pathScopes []string,
	reason string,
	addedBy string,
	addedAt time.Time,
) (RepositoryScope, error) {
	if familyID == "" || repositoryID == "" {
		return RepositoryScope{}, errors.New("scope family id and repository id are required")
	}
	if addedInScopeVersion == 0 {
		return RepositoryScope{}, errors.New("scope version must be greater than zero")
	}
	if access != RepositoryRead && access != RepositoryWrite {
		return RepositoryScope{}, fmt.Errorf("unsupported repository access %q", access)
	}
	normalizedPaths, err := normalizePathScopes(pathScopes)
	if err != nil {
		return RepositoryScope{}, err
	}
	reason = strings.TrimSpace(reason)
	addedBy = strings.TrimSpace(addedBy)
	if reason == "" || addedBy == "" || addedAt.IsZero() {
		return RepositoryScope{}, errors.New("scope reason, actor and timestamp are required")
	}

	return RepositoryScope{
		familyID:            familyID,
		addedInScopeVersion: addedInScopeVersion,
		repositoryID:        repositoryID,
		access:              access,
		pathScopes:          normalizedPaths,
		reason:              reason,
		addedBy:             addedBy,
		addedAt:             addedAt.UTC(),
	}, nil
}

func (s RepositoryScope) FamilyID() TaskFamilyID             { return s.familyID }
func (s RepositoryScope) AddedInScopeVersion() uint64        { return s.addedInScopeVersion }
func (s RepositoryScope) RepositoryID() project.RepositoryID { return s.repositoryID }
func (s RepositoryScope) Access() RepositoryAccess           { return s.access }
func (s RepositoryScope) Reason() string                     { return s.reason }
func (s RepositoryScope) AddedBy() string                    { return s.addedBy }
func (s RepositoryScope) AddedAt() time.Time                 { return s.addedAt }
func (s RepositoryScope) PathScopes() []string               { return append([]string(nil), s.pathScopes...) }

func ValidateFamilyScopes(
	family TaskFamily,
	repositories []project.Repository,
	scopes []RepositoryScope,
) error {
	repositoryProjects := make(map[project.RepositoryID]project.ProjectID, len(repositories))
	for _, repository := range repositories {
		if repository.ID == "" {
			return errors.New("repository id is required")
		}
		if _, exists := repositoryProjects[repository.ID]; exists {
			return fmt.Errorf("duplicate repository %q", repository.ID)
		}
		repositoryProjects[repository.ID] = repository.ProjectID
	}

	for _, scope := range scopes {
		if scope.familyID != family.ID {
			return fmt.Errorf("repository %q scope belongs to another task family", scope.repositoryID)
		}
		if scope.addedInScopeVersion > family.ScopeVersion {
			return fmt.Errorf("repository %q scope version exceeds family scope version", scope.repositoryID)
		}
		projectID, exists := repositoryProjects[scope.repositoryID]
		if !exists {
			return fmt.Errorf("repository %q is not registered", scope.repositoryID)
		}
		if projectID != family.ProjectID {
			return fmt.Errorf("repository %q belongs to another project", scope.repositoryID)
		}
	}
	return nil
}

func ValidateEffectiveScopes(
	family TaskFamily,
	scopeVersion uint64,
	familyScopes []RepositoryScope,
	effectiveScopes []RepositoryScope,
) error {
	if scopeVersion == 0 || scopeVersion > family.ScopeVersion {
		return errors.New("effective scope version is outside the task family scope history")
	}
	for _, candidate := range effectiveScopes {
		if candidate.familyID != family.ID {
			return fmt.Errorf("effective repository %q belongs to another task family", candidate.repositoryID)
		}
		if !scopeCovered(candidate, scopeVersion, familyScopes) {
			return fmt.Errorf("effective scope for repository %q exceeds approved family scope", candidate.repositoryID)
		}
	}
	return nil
}

func scopeCovered(candidate RepositoryScope, scopeVersion uint64, grants []RepositoryScope) bool {
	paths := candidate.pathScopes
	if len(paths) == 0 {
		for _, grant := range grants {
			if grantCovers(candidate, "", scopeVersion, grant) && len(grant.pathScopes) == 0 {
				return true
			}
		}
		return false
	}

	for _, candidatePath := range paths {
		covered := false
		for _, grant := range grants {
			if grantCovers(candidate, candidatePath, scopeVersion, grant) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func grantCovers(candidate RepositoryScope, candidatePath string, scopeVersion uint64, grant RepositoryScope) bool {
	if grant.familyID != candidate.familyID || grant.repositoryID != candidate.repositoryID {
		return false
	}
	if grant.addedInScopeVersion > scopeVersion {
		return false
	}
	if candidate.access == RepositoryWrite && grant.access != RepositoryWrite {
		return false
	}
	if len(grant.pathScopes) == 0 {
		return true
	}
	for _, grantPath := range grant.pathScopes {
		if candidatePath == grantPath || strings.HasPrefix(candidatePath, grantPath+"/") {
			return true
		}
	}
	return false
}

func normalizePathScopes(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	unique := make(map[string]struct{}, len(paths))
	for _, raw := range paths {
		if raw == "" {
			return nil, errors.New("path scope cannot be empty")
		}
		normalized := strings.ReplaceAll(raw, "\\", "/")
		if strings.HasPrefix(normalized, "/") || (len(normalized) >= 2 && normalized[1] == ':') {
			return nil, fmt.Errorf("path scope %q must be relative", raw)
		}
		for _, segment := range strings.Split(normalized, "/") {
			if segment == ".." {
				return nil, fmt.Errorf("path scope %q cannot contain parent traversal", raw)
			}
		}
		normalized = path.Clean(normalized)
		if normalized == "." || normalized == "" {
			return nil, fmt.Errorf("path scope %q is invalid", raw)
		}
		unique[normalized] = struct{}{}
	}

	result := make([]string, 0, len(unique))
	for normalized := range unique {
		result = append(result, normalized)
	}
	sort.Strings(result)
	return result, nil
}
