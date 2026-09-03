package project

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// ComponentID identifies a Component — a routable, path-scoped unit
// within one Repository (docs/harness-engineering/03-lec-03-repository-va-nguon-su-that.md
// HE-03-M04: "component MUST nằm gần component hoặc được selector định
// tuyến chính xác; không chỉ nằm trong global blob").
type ComponentID string

// Component is a named, path-scoped subdivision of one Repository —
// never a loose tag, always resolvable to an exact relative path within
// that Repository's tree. ProjectID is carried directly (not merely
// derivable by joining through RepositoryID) so cross-project validation
// never has to trust a caller's claim about which project a Component
// belongs to without checking it against the Repository it actually
// references (see ports.CatalogRepository.CreateComponent's own doc
// comment for exactly how that check runs). Kind is deliberately a plain
// string, not a closed Go enum: no citation in this task's own scope
// (docs/design/05-v3-project-workspace.md V3-01,
// docs/harness-engineering/03-lec-03-repository-va-nguon-su-that.md,
// docs/harness-engineering/06-lec-06-khoi-tao-la-phase-rieng.md) specifies
// a fixed set of Component kinds, and inventing one here would be a
// speculative design decision this package has no citation to ground —
// the same reasoning durable_jobs.kind (internal/adapters/sqlite
// migrations/0001_initial_schema.sql) already applies to an open-ended
// classification string.
type Component struct {
	ID           ComponentID
	ProjectID    ProjectID
	RepositoryID RepositoryID
	Name         string
	Path         string
	Kind         string
	Version      uint64
}

// NewComponent validates and constructs a new Component at generation 1.
// Path is normalized and validated by normalizeComponentPath — the same
// relative-path rules internal/app/scopeguard's own normalizePath and
// internal/domain/work's own normalizePathScopes already enforce
// (backslash normalized to forward slash, no leading "/" or Windows drive
// letter, no ".." segment, cleaned via path.Clean), reproduced here rather
// than imported because a domain package must never import
// internal/app/scopeguard (domain must never import app — enforced by
// internal/archtest's TestDomainAppNeverImportAdapters-style boundary
// tests) and internal/domain/work already sets the precedent of each
// domain package keeping its own small copy rather than inventing a
// shared import cycle to avoid it.
func NewComponent(id ComponentID, projectID ProjectID, repositoryID RepositoryID, name, rawPath, kind string) (Component, error) {
	if id == "" {
		return Component{}, errors.New("component id is required")
	}
	if projectID == "" {
		return Component{}, errors.New("component project id is required")
	}
	if repositoryID == "" {
		return Component{}, errors.New("component repository id is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Component{}, errors.New("component name is required")
	}
	normalizedPath, err := normalizeComponentPath(rawPath)
	if err != nil {
		return Component{}, err
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return Component{}, errors.New("component kind is required")
	}
	return Component{
		ID: id, ProjectID: projectID, RepositoryID: repositoryID,
		Name: name, Path: normalizedPath, Kind: kind, Version: 1,
	}, nil
}

// normalizeComponentPath applies this codebase's established relative-path
// validation rules to a single Component path — the same rules
// internal/app/scopeguard's normalizePath and internal/domain/work's
// normalizePathScopes already express for a WorkItem's path scopes: reject
// backslash-only paths by normalizing to forward slash, reject a leading
// "/" or a Windows drive letter, reject any ".." segment, clean via
// path.Clean, and reject a "." or empty result.
func normalizeComponentPath(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("component path cannot be empty")
	}
	normalized := strings.ReplaceAll(raw, "\\", "/")
	if strings.HasPrefix(normalized, "/") || (len(normalized) >= 2 && normalized[1] == ':') {
		return "", fmt.Errorf("component path %q must be relative", raw)
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return "", fmt.Errorf("component path %q cannot contain parent traversal", raw)
		}
	}
	normalized = path.Clean(normalized)
	if normalized == "." || normalized == "" {
		return "", fmt.Errorf("component path %q is invalid", raw)
	}
	return normalized, nil
}

// ComponentPackAssignmentID identifies one ComponentPackAssignment row.
// Assignments are append-only (a new pack version creates a new row, it
// never mutates an existing one — see ComponentPackAssignment's own doc
// comment), so this ID never needs a companion Version/generation field.
type ComponentPackAssignmentID string

// PackVersionID names an EngineeringPackVersion by its own opaque
// identity string — reproduced here rather than importing
// internal/domain/engineeringpack's own EngineeringPackVersionID type,
// because internal/domain/project cannot depend on it without creating an
// import cycle: engineeringpack already imports internal/domain/definition,
// which itself imports internal/domain/project (for definition.Scope's
// ProjectID) — the reverse import here would cycle. This is the same
// cross-kind-reference-by-value convention already established elsewhere
// in this codebase (internal/domain/policy's ResourceRef reproduces a
// resource identity tuple rather than importing Skill/Layer/Pack;
// internal/domain/block references Command/Gate/Policy only via
// definition.DependencyPin, never by importing either package). A caller
// assigning a pack version supplies the exact string
// engineeringpack.EngineeringPackVersionID produces; this package only
// ever treats it as an opaque identity — it never resolves or validates
// that the pack version actually exists (a later task/handler, which
// already holds a real UnitOfWork and the engineeringpack package itself,
// owns that check if one is ever needed).
type PackVersionID string

// ComponentPackAssignment pins an exact EngineeringPackVersion to a
// Component with an effective time and the actor who made the assignment
// (docs/design/01-system-design.md §6.2: "component_pack_assignments lưu
// component, pack version, effective time và actor để resolved
// configuration không phải suy ngầm từ UI"). It has no Version/generation
// field and no update operation: assigning a new pack version to the same
// Component always creates a new row (a new EffectiveAt), the same
// immutable-once-created discipline definition.VersionFields already
// established for a published Version — never an in-place mutation of a
// prior assignment, so the full assignment history (and "what pack
// version was in effect at time T") stays queryable rather than being
// overwritten.
type ComponentPackAssignment struct {
	ID            ComponentPackAssignmentID
	ProjectID     ProjectID
	ComponentID   ComponentID
	PackVersionID PackVersionID
	EffectiveAt   time.Time
	Actor         string
}

// NewComponentPackAssignment validates and constructs a new
// ComponentPackAssignment.
func NewComponentPackAssignment(
	id ComponentPackAssignmentID,
	projectID ProjectID,
	componentID ComponentID,
	packVersionID PackVersionID,
	effectiveAt time.Time,
	actor string,
) (ComponentPackAssignment, error) {
	if id == "" {
		return ComponentPackAssignment{}, errors.New("component pack assignment id is required")
	}
	if projectID == "" {
		return ComponentPackAssignment{}, errors.New("component pack assignment project id is required")
	}
	if componentID == "" {
		return ComponentPackAssignment{}, errors.New("component pack assignment component id is required")
	}
	if strings.TrimSpace(string(packVersionID)) == "" {
		return ComponentPackAssignment{}, errors.New("component pack assignment pack version id is required")
	}
	if effectiveAt.IsZero() {
		return ComponentPackAssignment{}, errors.New("component pack assignment effective time is required")
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return ComponentPackAssignment{}, errors.New("component pack assignment actor is required")
	}
	return ComponentPackAssignment{
		ID: id, ProjectID: projectID, ComponentID: componentID,
		PackVersionID: packVersionID, EffectiveAt: effectiveAt, Actor: actor,
	}, nil
}
