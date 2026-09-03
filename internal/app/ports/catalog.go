package ports

import (
	"errors"
	"time"
)

// ErrCrossProjectReference is returned when a catalog record's declared
// ProjectID does not match the actual project of the parent row it
// references (a Component referencing a Repository owned by a different
// Project; a ComponentPackAssignment referencing a Component owned by a
// different Project) — the Catalog-concern counterpart of
// ErrCrossProjectDependency, which is specific to a DefinitionKind's own
// dependency pins. The referenced row's actual project is always resolved
// by the repository itself, never trusted from the request, the same
// discipline ErrCrossProjectDependency's own doc comment describes.
var ErrCrossProjectReference = errors.New("ports: catalog record references a parent that belongs to a different project")

// CreateProjectRequest is what a caller supplies to
// CatalogRepository.CreateProject.
type CreateProjectRequest struct {
	ID   string
	Name string
}

// RegisterRepositoryRequest is what a caller supplies to
// CatalogRepository.RegisterRepository (persistence) and
// internal/app/catalog.RegisterRepository (the command). ID is the new
// Repository's own identity — caller-supplied, mirroring
// CreateDefinitionRequest.DefinitionID's own convention, since (unlike
// the probe job this same command also enqueues) a caller registering a
// repository already names the identity it wants, rather than the
// application layer minting one on its behalf.
type RegisterRepositoryRequest struct {
	ID            string
	ProjectID     string
	Name          string
	RemoteLocator string
	DefaultRef    string
}

// CreateComponentRequest is what a caller supplies to
// CatalogRepository.CreateComponent.
type CreateComponentRequest struct {
	ID           string
	ProjectID    string
	RepositoryID string
	Name         string
	Path         string
	Kind         string
}

// AssignComponentPackRequest is what a caller supplies to
// CatalogRepository.AssignComponentPack.
type AssignComponentPackRequest struct {
	ID            string
	ProjectID     string
	ComponentID   string
	PackVersionID string
	EffectiveAt   time.Time
	Actor         string
}
