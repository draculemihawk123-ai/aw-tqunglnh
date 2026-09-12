// Package work is V3-04/V3-05's application-command layer
// (docs/design/05-v3-project-workspace.md), mirroring the domain package it
// is built on, internal/domain/work — the same "app package named after the
// domain package it orchestrates" convention internal/app/catalog follows
// for internal/domain/project (Component/pack-assignment aside). It is a
// separate bounded concern from internal/app/catalog: Project/Repository/
// Component catalog management (V3-01/V3-02) is a different lifecycle from
// WorkItem/TaskFamily/WorkspaceSet task creation, even though both live
// under internal/app and both compose the same ports.UnitOfWork.
//
// This file has two public commands, at two very different points in a
// task family's own lifecycle: CreateRootWorkItem (V3-04, below) creates a
// family from nothing; CreateChildWorkItem (V3-05, this file's own newer
// half, see its own doc comment further down) decomposes an
// already-existing one. The two deliberately share almost nothing at the
// persistence level beyond ScopeGrantRequest's own shape and the
// receipt/idempotency envelope pattern — a child never creates a
// TaskFamily/WorkspaceSet of its own (AK-ARCH-012) and never enqueues a
// WORKSPACE_PROVISION job, so CreateChildWorkItem's own transaction body
// looks structurally different from CreateRootWorkItem's, not just
// shorter.
//
// CreateRootWorkItem is this package's original public command
// (docs/architecture/04-go-core-spec.md §8's command table: "CreateRootWorkItem
// | WorkItem + TaskFamily + WorkspaceSet + scope + provision jobs nguyên
// tử"; that same doc's own "CreateRootWorkItem là public boundary duy nhất
// để tạo root; contract validation có thể chạy trước transaction nhưng
// không được persist WorkItem mồ côi"). It atomically creates, inside one
// ports.UnitOfWork.WithSerializedWrite call:
//
//   - the root WorkItem itself (work.NewRootWorkItem — BACKLOG, generation
//     1);
//   - its owning TaskFamily (work.NewTaskFamily — ScopeVersion 1, ACTIVE);
//   - a WorkspaceSet intent (workspace.NewWorkspaceSet — REQUESTED; this
//     command never provisions a real RepositoryWorkspace, it only records
//     the intent to — V3-06 is the future task that actually provisions
//     against this WorkspaceSet, exactly the same "V3-01 enqueues the job,
//     a separate later task is the only consumer" relationship
//     RegisterRepository's own REPOSITORY_PROBE job already established);
//   - the initial RepositoryScope grants, normalized READ/WRITE + path
//     scopes, all AddedInScopeVersion=1 (work.NewRepositoryScope — add-only
//     from day one: nothing about this shape needs to change for a later
//     add-only expansion, V3-08's job, to append AddedInScopeVersion=2+
//     rows alongside these);
//   - one durable WORKSPACE_PROVISION job per repository named in the
//     initial scope (this command's own "provision jobs ... nguyên tử"
//     line) — never claimed or processed by anything in this task's scope,
//     the same "enqueue only, a separate later task is the only consumer"
//     relationship RegisterRepository's REPOSITORY_PROBE job already
//     established for V3-02;
//
// plus the domain event and command receipt every mutating command in this
// codebase already gets (RegisterRepository's own doc comment states the
// identical shape). All of it commits or rolls back as one atomic unit — a
// crash or validation failure at any point leaves zero rows behind: no
// WorkItem without its TaskFamily, no TaskFamily without its WorkspaceSet,
// no WorkspaceSet without its full set of provision-job intents (this
// task's own "Hoàn thành khi" bar).
//
// Same-project/ACTIVE-repository validation ("Thực hiện" line): every
// repository named in the initial scope must belong to req.ProjectID (its
// own stored project_id is always what is checked, resolved via
// tx.Catalog().GetRepository — never trusted from the request, the same
// discipline internal/app/catalog.CreateComponent already follows for its
// own cross-project checks) and must currently be project.RepositoryActive
// (V3-01/V3-02's own status machine) — a repository still REGISTERING/
// PROBING/BLOCKED/DISABLED cannot be granted scope on a brand-new task
// family. This check deliberately lives here, in the command handler, not
// inside work.ValidateFamilyScopes (the shared domain validator): extending
// ValidateFamilyScopes to require ACTIVE would break its own existing
// caller (internal/domain/work/work_test.go's own
// TestRepositoryScopeNormalizationAndFamilyInvariants fixture repository is
// deliberately REGISTERING, project.NewRepository's own rule — that test
// is about scope *shape* validity, not repository onboarding readiness,
// and the two concerns are deliberately kept separate). ValidateFamilyScopes
// itself is still called, once, over the full accumulated scope/repository
// set, as the shared same-project/registered-repository invariant every
// other scope caller in this codebase must also honor — redundant with
// this handler's own inline per-repository checks, kept anyway as a cheap
// (pure, no I/O) static safety net.
//
// Contract fields (SchemaVersion, Behavior, AcceptanceCriteria,
// VerificationSpec, RiskLevel, Exclusions, WorkflowVersionID,
// ApprovalException — V3-03's own addition to WorkItem) are deliberately
// NOT part of CreateRootWorkItemRequest: V3-03's own scope note says a
// caller needing a fully-contracted WorkItem "sets the exported fields
// directly ... and then calls ValidateReadinessGate itself", and this
// task's own "Thực hiện" line never mentions readiness/contract/BACKLOG->READY
// at all — only "normalized READ/WRITE/path scope, add-only version 1,
// same-project/ACTIVE-repository validation, provision jobs/outbox/event/
// receipt". A root WorkItem created via this command starts in BACKLOG
// with an empty contract; filling in the contract (and ever attempting
// BACKLOG->READY, which would call ValidateReadinessGate) is a separate,
// later step no V3 task in this repository's own doc set builds yet.
// ValidateReadinessGate is therefore never called from this file — there is
// no citation in this task's own scope requiring it, and V3-03's own scope
// note is explicit that persisting a WorkItem and validating its readiness
// are two separable concerns ("Validator MAY chạy trước V3-04 nhưng không
// persist WorkItem hoặc transition độc lập" describes V3-03's own validator
// running standalone, not this command wiring it in).
package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// WorkspaceProvisionJobKind is durable_jobs.kind's value for the job
// CreateRootWorkItem enqueues once per repository in the initial scope.
// durable_jobs.kind is a plain TEXT NOT NULL column with no CHECK
// constraint (internal/adapters/sqlite/migrations/0001_initial_schema.sql),
// so introducing this new kind string needs no migration change. Nothing in
// this task's own scope claims or processes this job — V3-06 is the future
// consumer, the same relationship RepositoryProbeJobKind (V3-01) has with
// V3-02's onboarding-probe worker.
const WorkspaceProvisionJobKind = "WORKSPACE_PROVISION"

// defaultProvisionJobMaxClaims mirrors catalog.defaultProbeJobMaxClaims —
// this codebase's own most common durable-job MaxClaims value for a single
// logical unit of work; V3-06's own future provision handler owns deciding
// its real retry policy, this is only what makes the row a valid
// durable_jobs insert today.
const defaultProvisionJobMaxClaims = 3

// ErrRepositoryNotActive is returned when a repository named in
// CreateRootWorkItemRequest.InitialScope is not currently
// project.RepositoryActive — a repository still REGISTERING/PROBING/
// BLOCKED/DISABLED cannot be granted scope on a brand-new task family (this
// task's own "same-project/ACTIVE-repository validation" line).
var ErrRepositoryNotActive = errors.New("work: repository is not ACTIVE")

// ScopeGrantRequest is one repository's initial scope grant
// (work.NewRepositoryScope's own Access/PathScopes/Reason parameters).
// AddedBy and AddedAt are deliberately not part of this request — they come
// from cmd.Actor/cmd.RequestedAt, the same "actor/time come from the
// command envelope, not the request payload" discipline every other
// audited actor field in this codebase already follows (e.g.
// AssignComponentPackRequest.Actor is the one documented exception, and
// only because AssignComponentPack's own Actor names who assigned the
// pack, a business fact distinct from cmd.Actor who issued the command).
type ScopeGrantRequest struct {
	RepositoryID string
	Access       string // workdomain.RepositoryRead | workdomain.RepositoryWrite
	PathScopes   []string
	Reason       string
}

// CreateRootWorkItemRequest is what a caller supplies to CreateRootWorkItem.
// WorkItemID/FamilyID/WorkspaceSetID are minted internally via idsource,
// not caller-supplied: unlike RegisterRepositoryRequest.RepositoryID (a
// caller-chosen identity for something the caller already names, e.g. by
// remote locator), a root WorkItem/TaskFamily/WorkspaceSet has no
// caller-meaningful identity of its own before this command creates it —
// the same reasoning CreateComponentRequest/AssignComponentPackRequest
// already follow (both mint their own ID via idsource rather than taking
// one from the caller).
type CreateRootWorkItemRequest struct {
	ProjectID    string
	Title        string
	InitialScope []ScopeGrantRequest
}

// ProvisionedRepository is one repository CreateRootWorkItem granted
// initial scope to, and the WORKSPACE_PROVISION job minted for it.
type ProvisionedRepository struct {
	RepositoryID   string `json:"repositoryId"`
	ProvisionJobID string `json:"provisionJobId"`
}

// CreateRootWorkItemResult is what CreateRootWorkItem returns (and what a
// replayed command-receipt reconstructs).
type CreateRootWorkItemResult struct {
	WorkItemID              string                  `json:"workItemId"`
	ProjectID               string                  `json:"projectId"`
	FamilyID                string                  `json:"familyId"`
	WorkspaceSetID          string                  `json:"workspaceSetId"`
	Status                  string                  `json:"status"`
	ProvisionedRepositories []ProvisionedRepository `json:"provisionedRepositories"`
}

// CreateRootWorkItem is the single public command that atomically creates
// a root WorkItem, its owning TaskFamily, a WorkspaceSet intent, the
// initial RepositoryScope grants and one WORKSPACE_PROVISION job per
// repository — see this package's own doc comment for the full contract.
// It follows V1-06's idempotent-command shape exactly like
// internal/app/catalog.RegisterRepository: a retry with the same
// IdempotencyKey and RequestHash replays the first call's result without
// creating any duplicate row or job; the same key with a different
// RequestHash is rejected as ports.ErrReceiptConflict.
func CreateRootWorkItem(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req CreateRootWorkItemRequest) (CreateRootWorkItemResult, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return CreateRootWorkItemResult{}, errors.New("work: ProjectID is required")
	}
	if strings.TrimSpace(req.Title) == "" {
		return CreateRootWorkItemResult{}, errors.New("work: Title is required")
	}
	if len(req.InitialScope) == 0 {
		return CreateRootWorkItemResult{}, errors.New("work: at least one initial scope grant is required")
	}

	var result CreateRootWorkItemResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		existingReceipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if found {
			if existingReceipt.RequestHash != cmd.RequestHash {
				return ports.ErrReceiptConflict
			}
			return json.Unmarshal([]byte(existingReceipt.ResultJSON), &result)
		}

		workItemID := ids.NewID()
		familyID := ids.NewID()
		workspaceSetID := ids.NewID()

		root, err := workdomain.NewRootWorkItem(
			workdomain.WorkItemID(workItemID), project.ProjectID(req.ProjectID), workdomain.TaskFamilyID(familyID), req.Title,
		)
		if err != nil {
			return err
		}
		family, err := workdomain.NewTaskFamily(workdomain.TaskFamilyID(familyID), root)
		if err != nil {
			return err
		}
		set, err := workspace.NewWorkspaceSet(workspace.WorkspaceSetID(workspaceSetID), family)
		if err != nil {
			return err
		}

		// Persist in FK-safe order: task_families row must exist before
		// work_items (work_items.family_id references task_families(id)) —
		// see ports.WorkRepository's own doc comment for why this order is
		// the reverse of the aggregates' conceptual creation order.
		if _, err := tx.Work().CreateTaskFamily(ctx, family); err != nil {
			return err
		}
		if _, err := tx.Work().CreateWorkItem(ctx, root); err != nil {
			return err
		}
		if _, err := tx.Work().CreateWorkspaceSet(ctx, set); err != nil {
			return err
		}

		seenRepositories := make(map[string]bool, len(req.InitialScope))
		scopes := make([]workdomain.RepositoryScope, 0, len(req.InitialScope))
		repositories := make([]project.Repository, 0, len(req.InitialScope))
		provisioned := make([]ProvisionedRepository, 0, len(req.InitialScope))

		for _, grant := range req.InitialScope {
			if seenRepositories[grant.RepositoryID] {
				return fmt.Errorf("work: duplicate repository %q in initial scope", grant.RepositoryID)
			}
			seenRepositories[grant.RepositoryID] = true

			scope, err := workdomain.NewRepositoryScope(
				workdomain.TaskFamilyID(familyID), family.ScopeVersion, project.RepositoryID(grant.RepositoryID),
				workdomain.RepositoryAccess(grant.Access), grant.PathScopes, grant.Reason, cmd.Actor, cmd.RequestedAt,
			)
			if err != nil {
				return err
			}

			// Same-project/ACTIVE-repository validation: the repository's
			// own stored row is always what is checked, never trusted from
			// the request (the same discipline
			// internal/app/catalog.CreateComponent already follows for its
			// own Repository/Component cross-project check).
			repo, err := tx.Catalog().GetRepository(ctx, grant.RepositoryID)
			if err != nil {
				return err
			}
			if string(repo.ProjectID) != req.ProjectID {
				return fmt.Errorf("%w: repository %s belongs to project %s, not %s",
					ports.ErrCrossProjectReference, grant.RepositoryID, repo.ProjectID, req.ProjectID)
			}
			if repo.Status != project.RepositoryActive {
				return fmt.Errorf("%w: repository %s is %s", ErrRepositoryNotActive, grant.RepositoryID, repo.Status)
			}

			if _, err := tx.Work().AddRepositoryScope(ctx, scope); err != nil {
				return err
			}

			jobPayload, err := json.Marshal(struct {
				WorkItemID     string `json:"workItemId"`
				ProjectID      string `json:"projectId"`
				FamilyID       string `json:"familyId"`
				WorkspaceSetID string `json:"workspaceSetId"`
				RepositoryID   string `json:"repositoryId"`
			}{
				WorkItemID: workItemID, ProjectID: req.ProjectID, FamilyID: familyID,
				WorkspaceSetID: workspaceSetID, RepositoryID: grant.RepositoryID,
			})
			if err != nil {
				return fmt.Errorf("marshal %s job payload: %w", WorkspaceProvisionJobKind, err)
			}
			jobID := ids.NewID()
			job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
				ID: ports.JobID(jobID), ProjectID: project.ProjectID(req.ProjectID), Kind: WorkspaceProvisionJobKind,
				AggregateType: "WorkspaceSet", AggregateID: workspaceSetID, Payload: jobPayload,
				AvailableAt: cmd.RequestedAt, MaxClaims: defaultProvisionJobMaxClaims,
				IdempotencyKey: fmt.Sprintf("%s-provision-%s", cmd.IdempotencyKey, grant.RepositoryID),
			})
			if err != nil {
				return err
			}

			scopes = append(scopes, scope)
			repositories = append(repositories, repo)
			provisioned = append(provisioned, ProvisionedRepository{
				RepositoryID: grant.RepositoryID, ProvisionJobID: string(job.ID),
			})
		}

		// Shared same-project/registered-repository invariant every scope
		// caller in this codebase must honor (see this package's own doc
		// comment for why the ACTIVE check above stays out of this shared
		// validator instead of being folded into it).
		if err := workdomain.ValidateFamilyScopes(family, repositories, scopes); err != nil {
			return err
		}

		eventPayload, err := json.Marshal(rootWorkItemCreatedEventPayload{
			WorkItemID: workItemID, ProjectID: req.ProjectID, FamilyID: familyID,
			WorkspaceSetID: workspaceSetID, Title: root.Title, ScopeCount: len(scopes),
		})
		if err != nil {
			return fmt.Errorf("marshal RootWorkItemCreated payload: %w", err)
		}
		// AggregateID is the new WorkItem's own ID; Sequence=1 is safe
		// because this is the WorkItem's very first and only event from
		// this command, and the receipt check above guarantees this branch
		// runs at most once per distinct (Actor, Scope, IdempotencyKey,
		// Type) — the same reasoning
		// internal/app/catalog.RegisterRepository's own RepositoryRegistered
		// event already relies on.
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-created", ProjectID: req.ProjectID,
			AggregateType: "WorkItem", AggregateID: workItemID, Sequence: 1,
			EventType: RootWorkItemCreatedEventType, SchemaVersion: RootWorkItemCreatedSchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = CreateRootWorkItemResult{
			WorkItemID: workItemID, ProjectID: req.ProjectID, FamilyID: familyID, WorkspaceSetID: workspaceSetID,
			Status: string(root.Status), ProvisionedRepositories: provisioned,
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal receipt result: %w", err)
		}
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
			CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON),
			CreatedAt: cmd.RequestedAt,
		})
	})
	return result, err
}

// ErrEffectiveScopeExceedsFamilyScope is returned when a requested child
// effective-scope entry is not covered by the parent's TaskFamily's own
// already-approved RepositoryScope grants at its current ScopeVersion —
// GC-INV-05's own "Child/node/attempt không được mở rộng quyền vượt scope
// family đã phê duyệt", concretely: a repository the family never granted
// at all, a path outside every grant's own PathScopes, or a WRITE request
// where the family only ever granted READ (this task's own explicit
// "READ→WRITE escalation rejection" Verify-line requirement). The
// underlying work.ValidateEffectiveScopes error (wrapped via %w below)
// names the specific repository.
var ErrEffectiveScopeExceedsFamilyScope = errors.New("work: child effective scope exceeds approved family scope")

// EffectiveScopeGrant is one repository entry CreateChildWorkItem recorded
// as part of a child's own effective scope.
type EffectiveScopeGrant struct {
	RepositoryID string `json:"repositoryId"`
	Access       string `json:"access"`
}

// CreateChildWorkItemRequest is what a caller supplies to
// CreateChildWorkItem. Unlike CreateRootWorkItemRequest, it carries no
// ProjectID: a child's ProjectID/FamilyID are always inherited from
// ParentWorkItemID's own already-persisted row (GC-INV-01, AK-ARCH-012),
// never re-declared by the caller — the same "the repository's own stored
// row is always what is checked, never trusted from the request"
// discipline CreateRootWorkItem's own same-project check already follows,
// applied here to the parent lookup instead of a Repository lookup.
type CreateChildWorkItemRequest struct {
	ParentWorkItemID string
	Title            string
	// ParentJoinPolicy is this child's own declared parent-completion/join
	// policy (HE-08-M07: "child WorkItem MUST gắn ... parent completion/
	// join policy"). Required — see work.WorkItem.ParentJoinPolicy's own
	// doc comment for why it stays a plain, open string rather than a
	// closed enum, and NewChildWorkItem for where the non-blank
	// requirement is actually enforced (this request field is just what
	// carries the caller's value there).
	ParentJoinPolicy string
	// SourceNodeRunID optionally names the runtime NodeRun that spawned
	// this child (HE-08-M07's other half: "child WorkItem MUST gắn source
	// NodeRun/parent"). Blank (the only case any real caller in this V3-era
	// codebase can supply — no runtime engine exists yet, V4's own future
	// job) means "created directly, not from a real NodeRun" — see
	// work.WorkItem.SourceNodeRunID's own doc comment for that boundary.
	SourceNodeRunID string
	// EffectiveScope is this child's own requested effective scope: every
	// entry must already be covered by the parent's TaskFamily's own
	// approved RepositoryScope grants at its current ScopeVersion
	// (GC-INV-05) — work.ValidateEffectiveScopes rejects anything wider (an
	// ungranted repository, a path outside every grant, or WRITE where the
	// family only ever granted READ). Reuses ScopeGrantRequest, the exact
	// same shape CreateRootWorkItemRequest.InitialScope already uses.
	EffectiveScope []ScopeGrantRequest
}

// CreateChildWorkItemResult is what CreateChildWorkItem returns (and what a
// replayed command-receipt reconstructs).
type CreateChildWorkItemResult struct {
	WorkItemID       string                `json:"workItemId"`
	ProjectID        string                `json:"projectId"`
	FamilyID         string                `json:"familyId"`
	ParentWorkItemID string                `json:"parentWorkItemId"`
	Status           string                `json:"status"`
	EffectiveScope   []EffectiveScopeGrant `json:"effectiveScope"`
}

// CreateChildWorkItem is go-core-spec §8's other WorkItem-creating public
// command: "CreateChildWorkItem | Child cùng family, effective scope là tập
// con". Inside one ports.UnitOfWork.WithSerializedWrite call it:
//
//   - loads req.ParentWorkItemID's own already-persisted WorkItem row —
//     never trusted from the request beyond its ID, the parent's own
//     ProjectID/FamilyID are always what the child inherits (GC-INV-01);
//   - builds the child WorkItem itself (work.NewChildWorkItem — BACKLOG,
//     generation 1, ParentID set, ParentJoinPolicy/SourceNodeRunID
//     attached per HE-08-M07) and persists it;
//   - loads the parent's TaskFamily and its own full accumulated
//     RepositoryScope grant history (tx.Work().ListFamilyRepositoryScopes)
//     at the family's current ScopeVersion;
//   - builds one candidate RepositoryScope per req.EffectiveScope entry and
//     validates the whole set via work.ValidateEffectiveScopes — the
//     already-existing, already-tested subset algorithm (READ/WRITE
//     escalation and path-prefix subset checking) this task reuses rather
//     than reimplements — rejecting anything that is not a genuine subset
//     of the family's own approved grants (GC-INV-05);
//   - persists each validated candidate as a work_item_effective_scopes row
//     (tx.Work().AddEffectiveScope);
//   - the domain event and command receipt every mutating command in this
//     codebase already gets.
//
// What this command deliberately never does, per this task's own explicit
// "Hoàn thành khi: tạo child không enqueue provision workspace mới" bar and
// AK-ARCH-012 ("Child cùng family reuse WorkspaceSet"): it never calls
// work.NewTaskFamily or workspace.NewWorkspaceSet, and it never enqueues a
// WorkspaceProvisionJobKind job — the family's WorkspaceSet already exists
// (created by the family's own root CreateRootWorkItem call) and this
// command reuses it outright. There is nothing new to provision.
//
// Same-family/subset validation is the load-bearing check here, the direct
// counterpart of CreateRootWorkItem's own same-project/ACTIVE-repository
// validation: every repository named in req.EffectiveScope must already be
// covered by the family's own RepositoryScope history at its current
// ScopeVersion. This command deliberately does NOT separately re-check
// same-project or Repository ACTIVE status via tx.Catalog() the way
// CreateRootWorkItem does — those checks already ran once, for real,
// against every row ValidateEffectiveScopes's own familyScopes argument can
// possibly contain (CreateRootWorkItem is the only path that ever writes a
// family_repository_scopes row, and it already required same-project/
// ACTIVE before writing one) — re-deriving them here would duplicate a
// check the family's own grant history already encodes, not add any real
// safety.
func CreateChildWorkItem(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req CreateChildWorkItemRequest) (CreateChildWorkItemResult, error) {
	if strings.TrimSpace(req.ParentWorkItemID) == "" {
		return CreateChildWorkItemResult{}, errors.New("work: ParentWorkItemID is required")
	}
	if strings.TrimSpace(req.Title) == "" {
		return CreateChildWorkItemResult{}, errors.New("work: Title is required")
	}
	if strings.TrimSpace(req.ParentJoinPolicy) == "" {
		return CreateChildWorkItemResult{}, errors.New("work: ParentJoinPolicy is required")
	}
	if len(req.EffectiveScope) == 0 {
		return CreateChildWorkItemResult{}, errors.New("work: at least one effective scope entry is required")
	}

	var result CreateChildWorkItemResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		existingReceipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if found {
			if existingReceipt.RequestHash != cmd.RequestHash {
				return ports.ErrReceiptConflict
			}
			return json.Unmarshal([]byte(existingReceipt.ResultJSON), &result)
		}

		parent, err := tx.Work().GetWorkItem(ctx, req.ParentWorkItemID)
		if err != nil {
			return err
		}

		family, err := tx.Work().GetTaskFamily(ctx, string(parent.FamilyID))
		if err != nil {
			return err
		}

		var sourceNodeRunID *workdomain.SourceNodeRunID
		if trimmed := strings.TrimSpace(req.SourceNodeRunID); trimmed != "" {
			nodeRunID := workdomain.SourceNodeRunID(trimmed)
			sourceNodeRunID = &nodeRunID
		}

		childID := ids.NewID()
		child, err := workdomain.NewChildWorkItem(
			workdomain.WorkItemID(childID), parent, req.Title,
			workdomain.JoinPolicy(req.ParentJoinPolicy), sourceNodeRunID,
		)
		if err != nil {
			return err
		}
		if _, err := tx.Work().CreateWorkItem(ctx, child); err != nil {
			return err
		}

		familyScopes, err := tx.Work().ListFamilyRepositoryScopes(ctx, string(parent.FamilyID))
		if err != nil {
			return err
		}

		seenRepositories := make(map[string]bool, len(req.EffectiveScope))
		candidates := make([]workdomain.RepositoryScope, 0, len(req.EffectiveScope))
		for _, grant := range req.EffectiveScope {
			if seenRepositories[grant.RepositoryID] {
				return fmt.Errorf("work: duplicate repository %q in effective scope", grant.RepositoryID)
			}
			seenRepositories[grant.RepositoryID] = true

			candidate, err := workdomain.NewRepositoryScope(
				parent.FamilyID, family.ScopeVersion, project.RepositoryID(grant.RepositoryID),
				workdomain.RepositoryAccess(grant.Access), grant.PathScopes, grant.Reason, cmd.Actor, cmd.RequestedAt,
			)
			if err != nil {
				return err
			}
			candidates = append(candidates, candidate)
		}

		// The reused, already-tested subset algorithm (GC-INV-05,
		// AK-ARCH-012) — this command never reimplements READ/WRITE
		// escalation or path-prefix subset checking itself.
		if err := workdomain.ValidateEffectiveScopes(family, family.ScopeVersion, familyScopes, candidates); err != nil {
			return fmt.Errorf("%w: %v", ErrEffectiveScopeExceedsFamilyScope, err)
		}

		persisted := make([]EffectiveScopeGrant, 0, len(candidates))
		for _, candidate := range candidates {
			if _, err := tx.Work().AddEffectiveScope(ctx, childID, candidate); err != nil {
				return err
			}
			persisted = append(persisted, EffectiveScopeGrant{
				RepositoryID: string(candidate.RepositoryID()), Access: string(candidate.Access()),
			})
		}

		eventPayload, err := json.Marshal(childWorkItemCreatedEventPayload{
			WorkItemID: childID, ProjectID: string(child.ProjectID), FamilyID: string(child.FamilyID),
			ParentWorkItemID: req.ParentWorkItemID, Title: child.Title, ScopeCount: len(candidates),
		})
		if err != nil {
			return fmt.Errorf("marshal ChildWorkItemCreated payload: %w", err)
		}
		// AggregateID is the new child WorkItem's own ID; Sequence=1 is safe
		// for the identical reason CreateRootWorkItem's own
		// RootWorkItemCreated event gives: this is the child's very first
		// and only event from this command, and the receipt check above
		// guarantees this branch runs at most once per distinct (Actor,
		// Scope, IdempotencyKey, Type).
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-created", ProjectID: string(child.ProjectID),
			AggregateType: "WorkItem", AggregateID: childID, Sequence: 1,
			EventType: ChildWorkItemCreatedEventType, SchemaVersion: ChildWorkItemCreatedSchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = CreateChildWorkItemResult{
			WorkItemID: childID, ProjectID: string(child.ProjectID), FamilyID: string(child.FamilyID),
			ParentWorkItemID: req.ParentWorkItemID, Status: string(child.Status), EffectiveScope: persisted,
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal receipt result: %w", err)
		}
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
			CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON),
			CreatedAt: cmd.RequestedAt,
		})
	})
	return result, err
}
