// Package project is Project/Repository/Component identity and lifecycle
// (docs/design/05-v3-project-workspace.md V3-01, docs/architecture/04-go-core-spec.md
// §4.1): a catalog authoritative where Repository identity is its own
// registered ID, never inferred from a path, slug, current working
// directory or remote URL ("Repository identity là ID đã đăng ký, MUST NOT
// suy từ path, slug, current directory hoặc remote URL"). Component is the
// routable, path-scoped unit within a Repository
// (docs/harness-engineering/03-lec-03-repository-va-nguon-su-that.md
// HE-03-M04) and the bridge to an EngineeringPack
// (docs/harness-engineering/06-lec-06-khoi-tao-la-phase-rieng.md HE-06-M05's
// "Repository -> Component -> EngineeringPack"), pinned by
// ComponentPackAssignment (component.go).
package project

import (
	"errors"
	"fmt"
	"strings"
)

type ProjectID string
type RepositoryID string

type ProjectStatus string

const (
	ProjectActive   ProjectStatus = "ACTIVE"
	ProjectArchived ProjectStatus = "ARCHIVED"
)

// RepositoryStatus is a Repository's own onboarding/health lifecycle
// (docs/design/05-v3-project-workspace.md V3-01, V3-02):
// REGISTERING -> PROBING -> ACTIVE|BLOCKED is driven by V3-02's future
// onboarding probe worker (not built by this package or by V3-01 at all —
// see CanTransitionRepositoryStatus's own doc comment for exactly which
// edges this task declares legal versus which it merely allows a later
// task to implement); DISABLED is reachable only as an explicit operator
// action, and only ever from ACTIVE.
type RepositoryStatus string

const (
	// RepositoryRegistering is every Repository's starting status
	// (NewRepository always returns this, never RepositoryActive): a
	// registered identity exists, but nothing has yet proven the remote
	// is reachable, its default ref resolves or its toolchain is usable.
	RepositoryRegistering RepositoryStatus = "REGISTERING"
	// RepositoryProbing means V3-02's onboarding probe has claimed the
	// repository and is running its read-only durable checks.
	RepositoryProbing RepositoryStatus = "PROBING"
	// RepositoryActive means the probe found real evidence (base commit,
	// clean/dirty baseline, toolchain) the repository is usable.
	RepositoryActive RepositoryStatus = "ACTIVE"
	// RepositoryBlocked means the probe failed and the repository is held
	// for a typed retry (RetryRepositoryProbe, V3-02) rather than silently
	// treated as usable.
	RepositoryBlocked RepositoryStatus = "BLOCKED"
	// RepositoryDisabled is a terminal operator action, reachable only
	// from RepositoryActive — a repository that has never been proven
	// ACTIVE has nothing meaningful to "disable".
	RepositoryDisabled RepositoryStatus = "DISABLED"
)

// ErrIllegalRepositoryTransition is returned by CanTransitionRepositoryStatus
// for any (from, to) pair that is not one of the lifecycle's explicitly
// allowed edges.
var ErrIllegalRepositoryTransition = errors.New("project: illegal repository status transition")

// legalRepositoryTransitions is the complete, closed set of
// RepositoryStatus transitions this task declares legal
// (docs/design/05-v3-project-workspace.md V3-01's own "status machine
// REGISTERING→PROBING→ACTIVE|BLOCKED, còn DISABLED chỉ là operator action
// sau khi từng ACTIVE"):
//
//   - REGISTERING -> PROBING: the only exit from REGISTERING (V3-02's
//     probe handler claims a freshly registered repository).
//   - PROBING -> ACTIVE | BLOCKED: the probe's two possible outcomes.
//   - BLOCKED -> PROBING: RetryRepositoryProbe (V3-02) re-runs the probe;
//     BLOCKED can never go anywhere else (in particular, never straight
//     to DISABLED — only a repository that reached ACTIVE at least once
//     may ever be disabled).
//   - ACTIVE -> DISABLED: the one operator-initiated edge; nothing else
//     leaves ACTIVE in this task's own scope (V3-01/V3-02 cite no path
//     back from ACTIVE to PROBING/BLOCKED, so none is declared here —
//     a later task that needs re-probing an already-ACTIVE repository
//     must add that edge deliberately, not rely on one silently already
//     existing).
//   - DISABLED: terminal, no outbound edge.
//
// This package only declares which transitions are legal; V3-01 itself
// never calls this to actually move a Repository (RegisterRepository only
// ever creates a fresh REGISTERING row) — executing REGISTERING->PROBING
// and beyond is entirely V3-02's job, against its own real probe evidence.
var legalRepositoryTransitions = map[RepositoryStatus]map[RepositoryStatus]bool{
	RepositoryRegistering: {RepositoryProbing: true},
	RepositoryProbing:     {RepositoryActive: true, RepositoryBlocked: true},
	RepositoryBlocked:     {RepositoryProbing: true},
	RepositoryActive:      {RepositoryDisabled: true},
	RepositoryDisabled:    {},
}

// CanTransitionRepositoryStatus reports whether from -> to is a legal
// RepositoryStatus transition per legalRepositoryTransitions.
func CanTransitionRepositoryStatus(from, to RepositoryStatus) error {
	edges, known := legalRepositoryTransitions[from]
	if !known {
		return fmt.Errorf("%w: unknown status %q", ErrIllegalRepositoryTransition, from)
	}
	if from == to {
		return fmt.Errorf("%w: %s -> %s is a no-op, not a transition", ErrIllegalRepositoryTransition, from, to)
	}
	if edges[to] {
		return nil
	}
	return fmt.Errorf("%w: %s -> %s", ErrIllegalRepositoryTransition, from, to)
}

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

// Repository is a registered repository identity, independent of any
// locator/path (V3-01's own "Hoàn thành khi": list/filter never infers
// identity from slug/cwd/remote). LastProbeErrorCode is nil until V3-02's
// probe first fails; V3-01 never sets it (RegisterRepository always
// starts a fresh Repository at RepositoryRegistering with no prior probe
// error to report).
type Repository struct {
	ID                 RepositoryID
	ProjectID          ProjectID
	Name               string
	VCSKind            VCSKind
	RemoteLocator      string
	DefaultRef         string
	Status             RepositoryStatus
	LastProbeErrorCode *string
	Version            uint64
}

// NewRepository validates and constructs a freshly registered Repository —
// always RepositoryRegistering, generation 1, never RepositoryActive
// directly (docs/architecture/04-go-core-spec.md §4.1's "RegisterRepository
// atomically tạo record REGISTERING"). Only V3-02's future onboarding
// probe worker ever moves a Repository past this status.
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
		Status:        RepositoryRegistering,
		Version:       1,
	}, nil
}
