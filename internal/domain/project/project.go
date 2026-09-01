package project

import (
	"errors"
	"strings"
)

type ProjectID string
type RepositoryID string

type ProjectStatus string

const (
	ProjectActive   ProjectStatus = "ACTIVE"
	ProjectArchived ProjectStatus = "ARCHIVED"
)

type RepositoryStatus string

const (
	RepositoryActive   RepositoryStatus = "ACTIVE"
	RepositoryDisabled RepositoryStatus = "DISABLED"
)

type VCSKind string

const VCSGit VCSKind = "GIT"

type Project struct {
	ID      ProjectID
	Name    string
	Status  ProjectStatus
	Version uint64
}

func NewProject(id ProjectID, name string) (Project, error) {
	if id == "" {
		return Project{}, errors.New("project id is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Project{}, errors.New("project name is required")
	}
	return Project{ID: id, Name: name, Status: ProjectActive, Version: 1}, nil
}

type Repository struct {
	ID            RepositoryID
	ProjectID     ProjectID
	Name          string
	VCSKind       VCSKind
	RemoteLocator string
	DefaultRef    string
	Status        RepositoryStatus
	Version       uint64
}

func NewRepository(
	id RepositoryID,
	projectID ProjectID,
	name string,
	remoteLocator string,
	defaultRef string,
) (Repository, error) {
	if id == "" {
		return Repository{}, errors.New("repository id is required")
	}
	if projectID == "" {
		return Repository{}, errors.New("repository project id is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Repository{}, errors.New("repository name is required")
	}
	remoteLocator = strings.TrimSpace(remoteLocator)
	if remoteLocator == "" {
		return Repository{}, errors.New("repository remote locator is required")
	}
	defaultRef = strings.TrimSpace(defaultRef)
	if defaultRef == "" {
		return Repository{}, errors.New("repository default ref is required")
	}

	return Repository{
		ID:            id,
		ProjectID:     projectID,
		Name:          name,
		VCSKind:       VCSGit,
		RemoteLocator: remoteLocator,
		DefaultRef:    defaultRef,
		Status:        RepositoryActive,
		Version:       1,
	}, nil
}
