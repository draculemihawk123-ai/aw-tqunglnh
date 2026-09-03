// Package work is WorkItem/TaskFamily identity, contract and scope
// (docs/design/05-v3-project-workspace.md V3-01, V3-03;
// docs/architecture/04-go-core-spec.md §4.2). It has two separable
// layers that predate one another:
//
//   - TaskFamily/RepositoryScope/path-scope normalization (V3-01/earlier
//     V0-era work) — WHICH repositories/paths a task family may touch.
//     V3-03 does not touch any of this.
//   - WorkItem's own contract shape and readiness gate (V3-03, this
//     file's own newer half) — WHAT a WorkItem's root task actually
//     promises: schema version, behavior, acceptance criteria,
//     verification approach, risk, exclusions and an optional pinned
//     workflow version, plus ValidateReadinessGate, the pure
//     BACKLOG->READY completeness check
//     (docs/harness-engineering/01-lec-01-do-tin-cay-khong-den-tu-model.md
//     HE-01-M02, docs/harness-engineering/07-lec-07-kiem-soat-scope-va-wip.md
//     HE-07-M01/M02, docs/harness-engineering/08-lec-08-work-item-la-primitive.md
//     HE-08-M01/M06, docs/harness-engineering/10-lec-10-kiem-chung-full-pipeline.md
//     HE-10-M01, docs/harness-engineering/11-lec-11-observability-noi-tai.md
//     HE-11-M05). ValidateReadinessGate is pure and takes a plain
//     WorkItem value: it never touches a database or a UnitOfWork, never
//     persists anything and never performs the transition itself
//     (go-core-spec §8: "CreateRootWorkItem là public boundary duy nhất
//     để tạo root; contract validation có thể chạy trước transaction
//     nhưng không được persist WorkItem mồ côi") — the real
//     CreateRootWorkItem/CreateChildWorkItem commands that call this
//     validator from inside a real persisted transition are a later
//     task's job (V3-04), not this package's.
package work

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
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

// WorkItem is a task's identity, ownership and own contract
// (docs/architecture/04-go-core-spec.md §4.2's struct sketch). The first
// block of fields (ID..FamilyID) is V0/V1-era identity/ownership,
// unchanged since before V3-03. The second block (SchemaVersion..
// ApprovalException) is V3-03's own addition: the root task's contract —
// what it promises, not just who owns it. See ValidateReadinessGate for
// the completeness rule this contract exists to support.
type WorkItem struct {
	ID        WorkItemID
	ProjectID project.ProjectID
	Kind      WorkItemKind
	ParentID  *WorkItemID
	FamilyID  TaskFamilyID

	// SchemaVersion is this WorkItem contract's own schema version
	// (HE-08-M01: "WorkItem MUST có schema version"). No citation in this
	// task's scope defines what a migration between schema versions looks
	// like — ValidateReadinessGate only requires the field be positive.
	SchemaVersion int
	Title         string
	// Behavior is the observable behavior this WorkItem's root task is
	// expected to produce (HE-07-M01: "WorkItem MUST có ... observable
	// behavior"; HE-08's own contract sketch: "behavior + acceptance").
	Behavior string
	// AcceptanceCriteria is the checkable statements this WorkItem's
	// behavior must satisfy (HE-08-M01, HE-11-M05). See
	// AcceptanceCriterion.Executable and ValidateReadinessGate for the
	// "executable acceptance" completeness rule this task's own "Hoàn
	// thành khi" bar names: a WorkItem with no executable acceptance
	// criterion cannot reach READY unless ApprovalException is set.
	AcceptanceCriteria []AcceptanceCriterion
	// VerificationSpec is a whole-item description of how this WorkItem's
	// behavior is verified overall (HE-11-M05: "verification policy MUST
	// tồn tại trước BUILD"; HE-08-S01: "WorkItem publish/activation SHOULD
	// fail nếu behavior hoặc verification rỗng"). It is the item-level
	// verification narrative, distinct from each AcceptanceCriterion's own
	// optional, per-criterion VerificationRef.
	VerificationSpec string
	// RiskLevel is this WorkItem's self-declared risk rating, later
	// consumed by a VerificationPolicyVersion to resolve which assurance
	// levels apply (HE-10-M01). See RiskLevel's own doc comment for why
	// it stays a plain non-empty string rather than a closed enum.
	RiskLevel RiskLevel
	// Exclusions is the explicit out-of-scope list (HE-07-M01: "WorkItem
	// MUST có ... out-of-scope"; HE-11-M05: "exclusions ... MUST tồn tại
	// trước BUILD"). An empty list is a legitimate declaration — "nothing
	// is explicitly out of scope" — not a missing-field violation;
	// ValidateReadinessGate only rejects a blank entry inside a non-empty
	// list.
	Exclusions []string
	// WorkflowVersionID optionally pins the WorkflowVersion this WorkItem
	// is intended to run under (go-core-spec §4.2's "WorkflowVersionID?").
	// A pure validator has no registry/UnitOfWork to confirm the
	// reference actually resolves to a real, published, compiled
	// WorkflowVersion — see ValidateReadinessGate's own doc comment for
	// that boundary. This package only ever checks the reference is
	// well-formed (non-blank) when present.
	WorkflowVersionID *workflow.WorkflowVersionID
	// ApprovalException, when present, is the human sign-off that lets
	// this WorkItem reach READY despite having no executable acceptance
	// criterion (HE-01-M02: "ngoại lệ phải qua human approval"; HE-07-M02:
	// "verification recipe hoặc approval criterion trước activation").
	ApprovalException *ApprovalException

	Status  WorkItemStatus
	Version uint64
}

// RiskLevel is a WorkItem's self-declared risk rating. No citation this
// task reads (HE-01/07/08/10/11) enumerates a closed set of risk level
// names — lecture 09's assurance ladder mentions "rủi ro cao" (high risk)
// only informally, never as a fixed vocabulary the way WorkItemStatus or
// RepositoryAccess are. Per this codebase's own established discipline
// for a loosely-specified vocabulary (see workflow/node_config.go's
// repeated "minimal, defensible reading" comments), RiskLevel stays a
// plain non-empty string: the real VerificationPolicyVersion resolver
// that actually interprets this value (HE-10-M01) is a later,
// I/O-capable task's job — HE-10-M01 is explicit that "Skill/Engineering
// Pack chỉ cung cấp tri thức đầu vào, không cấu hình gate", and this
// validator is even further upstream than a Pack. Inventing level names
// now risks being wrong about what that resolver expects; leaving it a
// string lets a later task enrich it into an enum without a breaking
// migration.
type RiskLevel string

// AcceptanceCriterion is one checkable statement of what must be true
// for a WorkItem's behavior to be accepted. Description is the criterion
// itself; VerificationRef, when non-empty, names the verification
// recipe/command/check that proves it — exactly the distinction
// HE-01-M02 draws between merely listing "acceptance criteria" and
// having an "Executable DoD" ("WorkItem MUST có acceptance criteria và
// ít nhất một verification recipe"). VerificationRef is deliberately a
// bare string identifier here, not a resolved pin against a real
// CommandDefinition/GateDefinition version: resolving it needs a real
// registry lookup (docs/harness-engineering/10-lec-10-kiem-chung-full-pipeline.md:
// "Command, selector có thể thực thi, gate definition... là các object
// versioned riêng"), which is I/O this package's pure validator does not
// have — see ValidateReadinessGate's own doc comment.
type AcceptanceCriterion struct {
	Description     string
	VerificationRef string
}

// Executable reports whether c is backed by a concrete verification
// recipe reference rather than being descriptive-only text (HE-01-M02's
// "Executable DoD").
func (c AcceptanceCriterion) Executable() bool {
	return strings.TrimSpace(c.VerificationRef) != ""
}

// ApprovalException is the explicit human sign-off that lets a WorkItem
// reach READY despite having no executable acceptance criterion — the
// override HE-01-M02 ("ngoại lệ phải qua human approval") and HE-07-M02
// ("verification recipe hoặc approval criterion trước activation") both
// name. Its shape deliberately mirrors this package's own
// RepositoryScope: an actor, a reason and a timestamp behind private
// fields and a validating constructor, so a value can never exist
// half-populated — the same audit-record discipline, applied here to a
// readiness override instead of a scope grant.
type ApprovalException struct {
	reason     string
	approvedBy string
	approvedAt time.Time
}

// NewApprovalException validates and constructs an ApprovalException.
// reason and approvedBy must be non-blank and approvedAt must be set —
// an approval nobody can attribute to an actor, a reason or a time is
// not a real audit record.
func NewApprovalException(reason, approvedBy string, approvedAt time.Time) (ApprovalException, error) {
	reason = strings.TrimSpace(reason)
	approvedBy = strings.TrimSpace(approvedBy)
	if reason == "" || approvedBy == "" || approvedAt.IsZero() {
		return ApprovalException{}, errors.New("approval exception reason, approver and timestamp are required")
	}
	return ApprovalException{reason: reason, approvedBy: approvedBy, approvedAt: approvedAt.UTC()}, nil
}

func (a ApprovalException) Reason() string        { return a.reason }
func (a ApprovalException) ApprovedBy() string    { return a.approvedBy }
func (a ApprovalException) ApprovedAt() time.Time { return a.approvedAt }

// NewRootWorkItem and NewChildWorkItem remain the thin, identity-only
// constructors they were before V3-03 (their only two callers today are
// this package's own work_test.go and workspace/workspace_test.go — no
// production code path constructs a WorkItem yet). They deliberately do
// not populate SchemaVersion/Behavior/AcceptanceCriteria/VerificationSpec/
// RiskLevel/Exclusions/WorkflowVersionID/ApprovalException: filling in a
// WorkItem's full contract from real caller input is explicitly V3-04's
// CreateRootWorkItem/CreateChildWorkItem job
// (docs/architecture/04-go-core-spec.md §8), not this package's — adding
// a second, richer constructor here without a real caller to drive its
// shape would be guessing at V3-04's own request DTO. A caller that needs
// a fully-contracted WorkItem today sets the exported fields directly (a
// WorkItem's fields are all exported for exactly this reason) and then
// calls ValidateReadinessGate itself.
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

// ReadinessError lists every reason ValidateReadinessGate rejected a
// WorkItem, not just the first — the same "collect every problem" shape
// workflow.ValidationError already established for graph validation.
type ReadinessError struct {
	Problems []string
}

func (e *ReadinessError) Error() string {
	return "work item is not ready: " + strings.Join(e.Problems, "; ")
}

// ValidateReadinessGate is the pure, no-I/O BACKLOG->READY completeness
// check V3-03 is scoped to build
// (docs/design/05-v3-project-workspace.md V3-03: "schema
// migration/domain/contract validator; BACKLOG→READY completeness
// validation... Validator MAY chạy trước V3-04 nhưng không persist
// WorkItem hoặc transition độc lập"). It takes a plain WorkItem value —
// never a database row, never a UnitOfWork — and only ever reports
// whether item's own contract fields are complete enough for READY; it
// never mutates item, never persists anything and never performs the
// transition itself. Building the real CreateRootWorkItem/
// CreateChildWorkItem commands that call this from inside a persisted
// transition, and combining this result with an actual WorkItemStatus
// transition legality check, is V3-04's job
// (docs/architecture/04-go-core-spec.md §8's command table) — this
// function assumes nothing about item.Status and does not itself check
// that item is currently BACKLOG, since V3-03's own scope explicitly
// excludes building a general WorkItemStatus transition state machine
// (only this completeness gate is grounded by this task's citations).
//
// Pure/structural vs needs-I/O boundary — the single most important call
// this function makes: every check below is decidable from item's own
// fields alone. Three things this task's own Verify line names
// ("invalid workflow/project/scope") are deliberately NOT checked here,
// because deciding them needs a real lookup this pure validator has no
// access to:
//
//   - whether ProjectID/FamilyID/ParentID actually resolve to a real,
//     matching Project/TaskFamily/parent WorkItem row — needs
//     ports.CatalogRepository-style I/O this package cannot import
//     (domain packages depend on nothing but the Go standard library and
//     sibling domain packages);
//   - whether WorkflowVersionID actually resolves to a real, published,
//     compiled WorkflowVersion — needs a workflow version registry
//     lookup;
//   - whether each AcceptanceCriterion's VerificationRef actually
//     resolves to a real, executable CommandDefinition/GateDefinition
//     (HE-10-M01's own "Command... là các object versioned riêng").
//
// This function only ever checks the *structural* half of "invalid
// workflow/project/scope": that a WorkflowVersionID, when present, is
// non-blank; that ProjectID/FamilyID are non-blank; and that Kind and
// ParentID are mutually consistent (ROOT never carries a ParentID, CHILD
// always does — the same invariant NewChildWorkItem/NewTaskFamily already
// enforce at construction time, re-checked here because a WorkItem value
// handed to this validator need not have come through those
// constructors). A later I/O-capable caller (V3-04, or a later task) is
// expected to additionally run the three DB-backed checks above; this
// validator's job ends at what it can decide alone.
func ValidateReadinessGate(item WorkItem) error {
	var problems []string

	if strings.TrimSpace(string(item.ProjectID)) == "" {
		problems = append(problems, "project id is required")
	}
	if strings.TrimSpace(string(item.FamilyID)) == "" {
		problems = append(problems, "family id is required")
	}
	if strings.TrimSpace(item.Title) == "" {
		problems = append(problems, "title is required")
	}
	if item.SchemaVersion <= 0 {
		problems = append(problems, "schema version must be positive")
	}
	if strings.TrimSpace(item.Behavior) == "" {
		problems = append(problems, "behavior is required")
	}
	if strings.TrimSpace(item.VerificationSpec) == "" {
		problems = append(problems, "verification spec is required")
	}
	if strings.TrimSpace(string(item.RiskLevel)) == "" {
		problems = append(problems, "risk level is required")
	}

	switch item.Kind {
	case WorkItemRoot:
		if item.ParentID != nil {
			problems = append(problems, "root work item must not have a parent id")
		}
	case WorkItemChild:
		if item.ParentID == nil || strings.TrimSpace(string(*item.ParentID)) == "" {
			problems = append(problems, "child work item must have a parent id")
		}
	default:
		problems = append(problems, fmt.Sprintf("unknown work item kind %q", item.Kind))
	}

	if item.WorkflowVersionID != nil && strings.TrimSpace(string(*item.WorkflowVersionID)) == "" {
		problems = append(problems, "workflow version id is blank")
	}

	for i, exclusion := range item.Exclusions {
		if strings.TrimSpace(exclusion) == "" {
			problems = append(problems, fmt.Sprintf("exclusion at index %d is blank", i))
		}
	}

	// HE-01-M02's own "Executable DoD" bar: a WorkItem with no executable
	// acceptance criterion (none at all, or none whose VerificationRef is
	// set) cannot reach READY unless ApprovalException carries a real
	// human sign-off. This single check is what covers an empty
	// AcceptanceCriteria list too — there is no separate unconditional
	// "acceptance criteria must be non-empty" rule, because HE-01-M02's
	// own exception explicitly overrides that entire bar, not just the
	// "at least one is executable" half of it.
	if !hasExecutableAcceptance(item.AcceptanceCriteria) && item.ApprovalException == nil {
		problems = append(problems, "no executable acceptance criterion is present and no approval exception was granted")
	}

	if len(problems) > 0 {
		return &ReadinessError{Problems: problems}
	}
	return nil
}

func hasExecutableAcceptance(criteria []AcceptanceCriterion) bool {
	for _, c := range criteria {
		if c.Executable() {
			return true
		}
	}
	return false
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
