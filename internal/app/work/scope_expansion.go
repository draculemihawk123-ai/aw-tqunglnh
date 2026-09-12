// Scope expansion request/approval (V3-08,
// docs/design/05-v3-project-workspace.md; docs/architecture/04-go-core-spec.md
// §8's command table). This file's own four public commands are this
// package's third structural shape, alongside CreateRootWorkItem/
// CreateChildWorkItem in commands.go: those two create WorkItems;
// RequestScopeExpansion/ApproveScopeExpansion/RejectScopeExpansion/
// WithdrawScopeExpansion instead manage a TaskFamily's own add-only scope
// growth, entirely after a family already exists.
//
// AK-ARCH-015A is this whole file's own governing invariant: "Scope được
// duyệt thêm không đổi quyền của Attempt cũ; activation mới pin đúng
// [scope version mới]" — an approved scope expansion NEVER reaches into an
// already-running NodeRun/Attempt's own live permissions; it only ever
// creates state (a new RepositoryScope grant at a new AddedInScopeVersion, a
// ScopeExpansionApproved domain event) that a FUTURE activation — entirely
// V4's job, no runtime engine exists yet in this codebase — will pin
// against. This file's own closing bar, go-core-spec's "provider/prompt
// không thể tự mở scope", is exactly this: scope expansion is ALWAYS an
// explicit, separate, human/operator-approved command (this file's own
// four-command surface), never something a provider/agent does mid-run to
// silently widen its own permitted scope.
//
// ApproveScopeExpansion's own ScopeVersion-bump-then-grant-write ordering
// (documented on that function itself, below) is this file's single most
// important design decision — read it before touching either half.
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

// ScopeExpansionReconcileJobKind is the durable job ApproveScopeExpansion
// enqueues (V4-12A, AK-ARCH-015A) exactly once per request that has a real
// runtime origin — internal/app/runtime owns the ONE consumer/Handler for
// it (this package only ever enqueues, the same "producer names the job
// kind it enqueues, the consumer package imports it" convention
// WorkspaceProvisionJobKind already established for
// internal/app/workspaceprovision's own Handler).
const ScopeExpansionReconcileJobKind = "SCOPE_EXPANSION_RECONCILE"

const defaultScopeExpansionReconcileJobMaxClaims = 3

// ScopeExpansionReconcileJobPayload is the exact JSON shape
// ApproveScopeExpansion marshals for a ScopeExpansionReconcileJobKind job
// (and every successor that job's own handler enqueues for itself) —
// defined once here so producer and every consumer always unmarshal the
// exact same shape.
type ScopeExpansionReconcileJobPayload struct {
	AttemptID      string `json:"attemptId"`
	PollGeneration uint64 `json:"pollGeneration"`
}

// ErrCrossFamilyReference is returned when RequestScopeExpansion's own
// optional ReferencedWorkItemID names a real WorkItem that belongs to a
// DIFFERENT TaskFamily than the one the request itself targets — a "block
// WorkItem/run reference nếu có" reference only ever makes sense within the
// same family (GC-INV-01's own family-ownership discipline, applied here to
// a reference rather than a create).
var ErrCrossFamilyReference = errors.New("work: referenced work item belongs to another task family")

// ErrScopeExpansionNotPending is returned by ApproveScopeExpansion/
// RejectScopeExpansion, and by WithdrawScopeExpansion for a request that is
// already APPROVED or REJECTED (never for one already WITHDRAWN — see
// WithdrawScopeExpansion's own doc comment for that command's explicit
// idempotency), when the targeted ScopeExpansionRequest's own Status is not
// PENDING. This is this task's own explicit "duplicate approval" Verify-line
// requirement made concrete: a second ApproveScopeExpansion (or
// RejectScopeExpansion) call against an already-decided request must fail
// cleanly, never silently re-provision or double-increment ScopeVersion.
var ErrScopeExpansionNotPending = errors.New("work: scope expansion request is not PENDING")

// --- RequestScopeExpansion ---

// RequestScopeExpansionRequest is what a caller supplies to
// RequestScopeExpansion. Unlike CreateRootWorkItemRequest, it carries no
// ProjectID: a request's own ProjectID is always inherited from FamilyID's
// own already-persisted TaskFamily row (the same "the repository's own
// stored row is always what is checked, never trusted from the request"
// discipline CreateChildWorkItem's own parent lookup already follows,
// applied here to the family lookup).
type RequestScopeExpansionRequest struct {
	FamilyID string
	// RequestedGrants reuses ScopeGrantRequest — the exact same request-layer
	// shape CreateRootWorkItemRequest.InitialScope/
	// CreateChildWorkItemRequest.EffectiveScope already use — rather than a
	// third, near-identical DTO (this task's own judgment call: the
	// DOMAIN-level candidate shape, work.RequestedGrant, is deliberately its
	// own type — see that type's own doc comment — but the APPLICATION-level
	// request DTO a caller builds has no reason to differ from the two
	// existing scope-grant request shapes).
	RequestedGrants []ScopeGrantRequest
	Reason          string
	// ReferencedWorkItemID is this task's own "block WorkItem/run reference
	// nếu có" judgment call: optional; when non-blank it must name a real
	// WorkItem already persisted in the SAME family as FamilyID —
	// ErrPersistenceNotFound if it does not exist at all,
	// ErrCrossFamilyReference if it exists but belongs to a different
	// family. See work.ScopeExpansionRequest.ReferencedWorkItemID's own doc
	// comment for the explicit "no runtime engine exists yet, this never
	// itself drives a WorkItemStatus transition" boundary this field stays
	// inside of.
	ReferencedWorkItemID string
	// RequestID is populated now (V4-12A): internal-runtime-path-only —
	// left blank for every ordinary (UI/operator) caller, in which case
	// this command mints one via ids.NewID() exactly as it always has.
	// V4-12A's own REQUEST_SCOPE_EXPANSION job handler is the one caller
	// that ever supplies a non-blank value: the exact ScopeExpansionRequestID
	// FinalizeExecutionAttempt already RESERVED (and persisted into a
	// ScopeExpansionOrigin row) in the SAME fenced transaction that CASed
	// the originating Attempt/NodeRun to BLOCKED — before this command
	// ever runs, so the request this call creates lands under the exact
	// ID that origin row already names, and a crash/redelivery replays
	// idempotently by (Actor, Scope, IdempotencyKey) exactly like every
	// other command in this codebase, never by re-deriving a new ID. A
	// public/UI-facing caller must never be allowed to choose its own
	// RequestID (an internal identity a client has no business picking).
	RequestID string
}

// RequestScopeExpansionResult is what RequestScopeExpansion returns (and
// what a replayed command receipt reconstructs).
type RequestScopeExpansionResult struct {
	RequestID string `json:"requestId"`
	FamilyID  string `json:"familyId"`
	ProjectID string `json:"projectId"`
	Status    string `json:"status"`
}

// RequestScopeExpansion is go-core-spec §8's "RequestScopeExpansion | Block
// node và tạo request add-only". Inside one
// ports.UnitOfWork.WithSerializedWrite call it:
//
//   - loads req.FamilyID's own already-persisted TaskFamily row — never
//     trusted from the request beyond its ID;
//   - when req.ReferencedWorkItemID is non-blank, loads and same-family-
//     validates it (this command's own "block ... reference" judgment call);
//   - resolves and same-project/ACTIVE-validates every repository named in
//     req.RequestedGrants via tx.Catalog().GetRepository — the identical
//     discipline CreateRootWorkItem's own doc comment already establishes,
//     applied here to a candidate request instead of an immediate grant;
//   - builds and persists a brand-new, PENDING work.ScopeExpansionRequest
//     (work.NewScopeExpansionRequest, the pure domain constructor);
//   - the domain event and command receipt every mutating command in this
//     codebase already gets.
//
// This command deliberately never touches family_repository_scopes,
// task_families.scope_version, or enqueues any WORKSPACE_PROVISION job —
// requesting is not granting (go-core-spec's own "provider/prompt không thể
// tự mở scope" bar starts exactly here: even a caller with every field
// filled in correctly gets nothing but a PENDING row until a separate
// ApproveScopeExpansion call, by a separate actor, decides otherwise).
func RequestScopeExpansion(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req RequestScopeExpansionRequest) (RequestScopeExpansionResult, error) {
	if strings.TrimSpace(req.FamilyID) == "" {
		return RequestScopeExpansionResult{}, errors.New("work: FamilyID is required")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return RequestScopeExpansionResult{}, errors.New("work: Reason is required")
	}
	if len(req.RequestedGrants) == 0 {
		return RequestScopeExpansionResult{}, errors.New("work: at least one requested grant is required")
	}

	var result RequestScopeExpansionResult
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

		family, err := tx.Work().GetTaskFamily(ctx, req.FamilyID)
		if err != nil {
			return err
		}

		var referencedWorkItemID *workdomain.WorkItemID
		if trimmed := strings.TrimSpace(req.ReferencedWorkItemID); trimmed != "" {
			referenced, err := tx.Work().GetWorkItem(ctx, trimmed)
			if err != nil {
				return err
			}
			if referenced.FamilyID != family.ID {
				return fmt.Errorf("%w: work item %s belongs to family %s, not %s",
					ErrCrossFamilyReference, trimmed, referenced.FamilyID, family.ID)
			}
			workItemID := workdomain.WorkItemID(trimmed)
			referencedWorkItemID = &workItemID
		}

		seenRepositories := make(map[string]bool, len(req.RequestedGrants))
		grants := make([]workdomain.RequestedGrant, 0, len(req.RequestedGrants))
		for _, grant := range req.RequestedGrants {
			if seenRepositories[grant.RepositoryID] {
				return fmt.Errorf("work: duplicate repository %q in requested grants", grant.RepositoryID)
			}
			seenRepositories[grant.RepositoryID] = true

			// Same-project/ACTIVE-repository validation, the identical
			// discipline CreateRootWorkItem's own doc comment establishes:
			// the repository's own stored row is always what is checked,
			// never trusted from the request.
			repo, err := tx.Catalog().GetRepository(ctx, grant.RepositoryID)
			if err != nil {
				return err
			}
			if repo.ProjectID != family.ProjectID {
				return fmt.Errorf("%w: repository %s belongs to project %s, not %s",
					ports.ErrCrossProjectReference, grant.RepositoryID, repo.ProjectID, family.ProjectID)
			}
			if repo.Status != project.RepositoryActive {
				return fmt.Errorf("%w: repository %s is %s", ErrRepositoryNotActive, grant.RepositoryID, repo.Status)
			}

			grants = append(grants, workdomain.RequestedGrant{
				RepositoryID: project.RepositoryID(grant.RepositoryID), Access: workdomain.RepositoryAccess(grant.Access),
				PathScopes: grant.PathScopes, Reason: grant.Reason,
			})
		}

		requestID := strings.TrimSpace(req.RequestID)
		if requestID == "" {
			requestID = ids.NewID()
		}
		request, err := workdomain.NewScopeExpansionRequest(
			workdomain.ScopeExpansionRequestID(requestID), family, grants, req.Reason,
			referencedWorkItemID, cmd.Actor, cmd.RequestedAt,
		)
		if err != nil {
			return err
		}
		if _, err := tx.Work().CreateScopeExpansionRequest(ctx, request); err != nil {
			return err
		}

		eventPayload, err := json.Marshal(scopeExpansionRequestedEventPayload{
			RequestID: requestID, FamilyID: req.FamilyID, ProjectID: string(family.ProjectID),
			ReferencedWorkItemID: req.ReferencedWorkItemID, GrantCount: len(grants),
		})
		if err != nil {
			return fmt.Errorf("marshal ScopeExpansionRequested payload: %w", err)
		}
		// AggregateID is the new request's own ID; Sequence=1 is safe
		// because requestID is minted fresh and this is its very first and
		// only event, the identical reasoning CreateRootWorkItem's own
		// RootWorkItemCreated event already relies on.
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-requested", ProjectID: string(family.ProjectID),
			AggregateType: "ScopeExpansionRequest", AggregateID: requestID, Sequence: 1,
			EventType: ScopeExpansionRequestedEventType, SchemaVersion: ScopeExpansionRequestedSchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = RequestScopeExpansionResult{
			RequestID: requestID, FamilyID: req.FamilyID, ProjectID: string(family.ProjectID),
			Status: string(request.Status),
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

// --- ApproveScopeExpansion ---

// ApproveScopeExpansionRequest is what a caller supplies to
// ApproveScopeExpansion.
type ApproveScopeExpansionRequest struct {
	RequestID string
}

// ApprovedGrant is one repository entry ApproveScopeExpansion turned into a
// real RepositoryScope grant.
type ApprovedGrant struct {
	RepositoryID string `json:"repositoryId"`
	Access       string `json:"access"`
}

// ApproveScopeExpansionResult is what ApproveScopeExpansion returns (and
// what a replayed command receipt reconstructs).
type ApproveScopeExpansionResult struct {
	RequestID               string                  `json:"requestId"`
	FamilyID                string                  `json:"familyId"`
	ProjectID               string                  `json:"projectId"`
	NewScopeVersion         uint64                  `json:"newScopeVersion"`
	ApprovedGrants          []ApprovedGrant         `json:"approvedGrants"`
	ProvisionedRepositories []ProvisionedRepository `json:"provisionedRepositories"`
}

// ApproveScopeExpansion is go-core-spec §8's "ApproveScopeExpansion | Tăng
// ScopeVersion, provision rồi append amendment/reactivate". Inside one
// ports.UnitOfWork.WithSerializedWrite call it:
//
//   - loads req.RequestID's own ScopeExpansionRequest — ErrScopeExpansionNotPending
//     unless it is currently PENDING (this task's own "duplicate approval"
//     Verify-line bar: a second approval of an already-decided request
//     fails cleanly here, before anything else runs);
//   - loads the request's own TaskFamily FRESH, inside this same
//     transaction (never a value passed in from outside it — this is what
//     makes the CAS bump below race-safe under real concurrent approvals,
//     see this function's own "ordering" paragraph);
//   - CAS-bumps TaskFamily.ScopeVersion by exactly one
//     (tx.Work().TransitionTaskFamilyScopeVersion);
//   - for each of the request's own RequestedGrants, re-validates same-
//     project/ACTIVE-repository (defense in depth against repository state
//     drifting between request and approval — the identical reasoning
//     workspaceprovision.Handler's own doc comment gives for its own
//     defensive re-check of the same condition) and persists a brand-new
//     RepositoryScope at the JUST-bumped ScopeVersion
//     (work.NewRepositoryScope + tx.Work().AddRepositoryScope — never an
//     edit to any existing row, AK-ARCH-015A/go-core-spec §4.2's own
//     add-only invariant);
//   - for each repository that is genuinely NEW to the family (never
//     granted at any earlier ScopeVersion — an access/path upgrade on an
//     already-known repository mints no new job, see this function's own
//     "newly-added vs. upgraded" paragraph), enqueues exactly one
//     WORKSPACE_PROVISION job, reusing WorkspaceProvisionJobKind/
//     defaultProvisionJobMaxClaims and CreateRootWorkItem's own job-payload
//     shape verbatim — the identical "an earlier task enqueues, V3-06's
//     workspaceprovision.Handler is the sole, unmodified consumer"
//     relationship CreateRootWorkItem's own initial-scope jobs already
//     establish;
//   - when at least one repository is genuinely new AND the family's
//     WorkspaceSet has already reached READY, CAS-reopens it back to
//     PROVISIONING — this function's own "READY reopen" paragraph explains
//     exactly why this is necessary and why it is safe;
//   - CAS-transitions the ScopeExpansionRequest PENDING -> APPROVED,
//     recording ApprovedScopeVersion;
//   - emits a ScopeExpansionApproved domain event — the "approved-scope
//     event" go-core-spec's own command-table line names, for a future V4
//     handler to consume and append a RunManifestAmendment / reactivate a
//     blocked node (AK-ARCH-015A). This command builds and persists that
//     event only; it NEVER itself touches any NodeRun/Attempt — no runtime
//     engine exists yet in this codebase for it to touch;
//   - the command receipt every mutating command in this codebase already
//     gets.
//
// ScopeVersion bump-then-grant-write ordering (this function's single most
// important design decision, explicitly left for this task to pick):
// TaskFamily.ScopeVersion is bumped to N+1 FIRST
// (TransitionTaskFamilyScopeVersion), and the newly-approved RepositoryScope
// grant(s) are then written at AddedInScopeVersion = N+1 — the already-
// bumped value, read back off the CAS call's own return value, never a
// value this function computed itself ahead of time. This mirrors
// CreateRootWorkItem's own identical ordering exactly: NewTaskFamily already
// sets ScopeVersion=1 before CreateRootWorkItem ever writes the initial
// grants at AddedInScopeVersion=family.ScopeVersion(=1) — bump (or, for the
// root case, construct-already-at-value) always precedes grant-write in
// this codebase's own established convention. Both statements commit or
// roll back together as one transaction, so no other transaction can ever
// observe the family at its new ScopeVersion with the corresponding grant
// still missing, or vice versa — the "atomic and consistent" bar this
// task's own instructions name is satisfied by transaction atomicity, not
// by statement ordering; the ordering choice itself only matters for which
// value AddedInScopeVersion legitimately reads.
//
// Concurrency: two concurrent ApproveScopeExpansion calls for two DIFFERENT
// PENDING requests on the SAME family are safe by construction, not by any
// retry loop this function itself implements. internal/adapters/sqlite's own
// Store opens every connection with _txlock=immediate (txrunner.go's own doc
// comment), so two concurrent WithSerializedWrite transactions against the
// same database fully serialize at the SQLite level — the second transaction
// literally cannot begin reading until the first commits or rolls back —
// and this function always re-reads TaskFamily fresh, inside its own
// transaction, immediately before its own CAS bump. There is no window in
// which two transactions can observe the identical stale ScopeVersion and
// both attempt to bump from it: the second transaction's own fresh read
// already sees the first transaction's committed bump. See
// commands_sqlite_test.go's own TestApproveScopeExpansion_ConcurrentApprovals_BothSucceedWithDistinctScopeVersions
// for the real-sqlite proof.
//
// Newly-added vs. upgraded repository, and the READY reopen: a repository
// this approval grants that the family has NEVER been granted before (at
// any earlier ScopeVersion) needs a real, brand-new git worktree — exactly
// what CreateRootWorkItem's own WORKSPACE_PROVISION job already provisions
// for the initial set, reused here verbatim. A repository the family
// already had SOME grant for (this approval only upgrading READ to WRITE,
// or widening PathScopes) needs no new worktree at all — its existing
// RepositoryWorkspace row is still physically valid, only the scope
// METADATA changed — so this function enqueues no job and never touches the
// WorkspaceSet for it. workspaceprovision.Handler's own aggregateWorkspaceSet
// (V3-06, unmodified by this task) derives "every required repository" from
// ListFamilyRepositoryScopes at query time, so it automatically picks up a
// genuinely-new repository's own grant once this function writes it —
// EXCEPT that aggregateWorkspaceSet also carries a hard "only ever advance a
// set that is still PROVISIONING" guard, with no path back out of READY (or
// BLOCKED) once it gets there. Tracing that guard was this task's own
// required investigation (see this repo's PR description for the full
// finding); the fix applied is exactly as narrow as go-core-spec's own
// framing invited: THIS function, not workspaceprovision.Handler (left
// completely unmodified), CAS-reopens the WorkspaceSet from READY back to
// PROVISIONING — the identical TransitionWorkspaceSetState CAS primitive
// beginProvisioning already uses for REQUESTED->PROVISIONING, just run from
// the approval side instead of the job-handler side — whenever at least one
// newly-added repository's own WORKSPACE_PROVISION job is about to be
// enqueued. Once reopened, aggregateWorkspaceSet's own unmodified logic
// takes over exactly as it already does for the initial set: BLOCKED the
// moment any required repository fails, READY again (with a freshly
// recomputed base RevisionSet spanning the EXPANDED required set — this
// function's own "compute a NEW base RevisionSet reflecting the expanded
// set" job, satisfied by simply letting the existing, unmodified aggregation
// run again) only once every required repository, including the new one, is
// READY. A REQUESTED or PROVISIONING set needs no such reopen — the
// unmodified aggregation already derives the expanded required set on its
// own next run, exactly as go-core-spec's own framing anticipated. A BLOCKED
// set is deliberately left un-reopened: an already-failed required
// repository is a genuine, unrelated problem newly-added scope does not
// fix, and (as traced through aggregateWorkspaceSet's own anyFailed check)
// reopening it would only ever re-derive BLOCKED again once the new job
// lands — a real but deliberately out-of-scope gap for a later task, not a
// silent inconsistency this one leaves behind.
func ApproveScopeExpansion(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req ApproveScopeExpansionRequest) (ApproveScopeExpansionResult, error) {
	if strings.TrimSpace(req.RequestID) == "" {
		return ApproveScopeExpansionResult{}, errors.New("work: RequestID is required")
	}

	var result ApproveScopeExpansionResult
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

		request, err := tx.Work().GetScopeExpansionRequest(ctx, req.RequestID)
		if err != nil {
			return err
		}
		if request.Status != workdomain.ScopeExpansionPending {
			return fmt.Errorf("%w: scope expansion request %s is %s", ErrScopeExpansionNotPending, req.RequestID, request.Status)
		}

		// Fresh read, inside this same transaction — see this function's own
		// "Concurrency" paragraph for why this is what makes the CAS bump
		// below race-safe.
		family, err := tx.Work().GetTaskFamily(ctx, string(request.FamilyID))
		if err != nil {
			return err
		}

		bumpedFamily, err := tx.Work().TransitionTaskFamilyScopeVersion(ctx, ports.TransitionTaskFamilyScopeVersionRequest{
			FamilyID: string(family.ID), ExpectedScopeVersion: family.ScopeVersion, ExpectedVersion: family.Version,
		})
		if err != nil {
			return err
		}
		newScopeVersion := bumpedFamily.ScopeVersion

		existingFamilyScopes, err := tx.Work().ListFamilyRepositoryScopes(ctx, string(family.ID))
		if err != nil {
			return err
		}
		alreadyGranted := make(map[project.RepositoryID]bool, len(existingFamilyScopes))
		for _, scope := range existingFamilyScopes {
			alreadyGranted[scope.RepositoryID()] = true
		}

		// Loaded once, outside the grant loop below: every newly-added
		// repository's own WORKSPACE_PROVISION job needs this WorkspaceSet's
		// own ID, and the "reopen from READY" step further down needs its
		// State/Version too.
		set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, string(family.ID))
		if err != nil {
			return err
		}

		scopes := make([]workdomain.RepositoryScope, 0, len(request.RequestedGrants))
		repositories := make([]project.Repository, 0, len(request.RequestedGrants))
		approvedGrants := make([]ApprovedGrant, 0, len(request.RequestedGrants))
		var provisioned []ProvisionedRepository

		for _, grant := range request.RequestedGrants {
			// Defense in depth: repository state can drift between request
			// and approval (the identical reasoning workspaceprovision.
			// Handler's own doc comment gives for its own defensive
			// re-check immediately before real I/O).
			repo, err := tx.Catalog().GetRepository(ctx, string(grant.RepositoryID))
			if err != nil {
				return err
			}
			if repo.ProjectID != family.ProjectID {
				return fmt.Errorf("%w: repository %s belongs to project %s, not %s",
					ports.ErrCrossProjectReference, grant.RepositoryID, repo.ProjectID, family.ProjectID)
			}
			if repo.Status != project.RepositoryActive {
				return fmt.Errorf("%w: repository %s is %s", ErrRepositoryNotActive, grant.RepositoryID, repo.Status)
			}

			scope, err := workdomain.NewRepositoryScope(
				family.ID, newScopeVersion, grant.RepositoryID, grant.Access, grant.PathScopes, grant.Reason,
				cmd.Actor, cmd.RequestedAt,
			)
			if err != nil {
				return err
			}
			if _, err := tx.Work().AddRepositoryScope(ctx, scope); err != nil {
				return err
			}
			scopes = append(scopes, scope)
			repositories = append(repositories, repo)
			approvedGrants = append(approvedGrants, ApprovedGrant{
				RepositoryID: string(grant.RepositoryID), Access: string(grant.Access),
			})

			if alreadyGranted[grant.RepositoryID] {
				// An access/path upgrade on an already-known repository —
				// no new worktree needed, see this function's own "newly-
				// added vs. upgraded" paragraph.
				continue
			}

			// Job payload shape mirrors CreateRootWorkItem's own
			// WORKSPACE_PROVISION payload verbatim (WorkItemID is left blank
			// here — this job was never caused by any one WorkItem, only by
			// the scope expansion request itself; workspaceprovision.Handler
			// never keys off WorkItemID regardless, per its own doc comment).
			jobPayload, err := json.Marshal(struct {
				WorkItemID     string `json:"workItemId"`
				ProjectID      string `json:"projectId"`
				FamilyID       string `json:"familyId"`
				WorkspaceSetID string `json:"workspaceSetId"`
				RepositoryID   string `json:"repositoryId"`
			}{
				ProjectID: string(family.ProjectID), FamilyID: string(family.ID),
				WorkspaceSetID: string(set.ID), RepositoryID: string(grant.RepositoryID),
			})
			if err != nil {
				return fmt.Errorf("marshal %s job payload: %w", WorkspaceProvisionJobKind, err)
			}
			jobID := ids.NewID()
			job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
				ID: ports.JobID(jobID), ProjectID: family.ProjectID, Kind: WorkspaceProvisionJobKind,
				AggregateType: "WorkspaceSet", AggregateID: string(set.ID), Payload: jobPayload,
				AvailableAt: cmd.RequestedAt, MaxClaims: defaultProvisionJobMaxClaims,
				IdempotencyKey: fmt.Sprintf("%s-provision-%s", cmd.IdempotencyKey, grant.RepositoryID),
			})
			if err != nil {
				return err
			}
			provisioned = append(provisioned, ProvisionedRepository{
				RepositoryID: string(grant.RepositoryID), ProvisionJobID: string(job.ID),
			})
			// Avoid a second provision job for a duplicate repository within
			// this same request's own grants (defensive — the domain
			// constructor already rejects a duplicate repository within one
			// request, so this only ever matters if that invariant is ever
			// relaxed).
			alreadyGranted[grant.RepositoryID] = true
		}

		// Shared same-project/registered-repository invariant every scope
		// caller in this codebase must honor — redundant with this
		// function's own inline per-repository checks above, kept anyway as
		// a cheap (pure, no I/O) static safety net, the identical reasoning
		// CreateRootWorkItem's own doc comment gives for its own call.
		if err := workdomain.ValidateFamilyScopes(bumpedFamily, repositories, scopes); err != nil {
			return err
		}

		if len(provisioned) > 0 {
			// set was loaded once, above, before the grant loop — nothing in
			// this transaction touches workspace_sets between that load and
			// here, so it is still fresh.
			if set.State == workspace.WorkspaceSetReady {
				// The narrow, well-justified fix this function's own doc
				// comment explains in full: reopen a READY WorkspaceSet so
				// V3-06's own unmodified aggregateWorkspaceSet can re-derive
				// it against the now-expanded required-repository set.
				if _, err := tx.Work().TransitionWorkspaceSetState(ctx, ports.TransitionWorkspaceSetStateRequest{
					WorkspaceSetID: string(set.ID), ExpectedState: workspace.WorkspaceSetReady, ExpectedVersion: set.Version,
					NextState: workspace.WorkspaceSetProvisioning,
				}); err != nil {
					return err
				}
			}
			// REQUESTED/PROVISIONING: no action needed, see this function's
			// own doc comment. BLOCKED: deliberately left un-reopened, same.
		}

		newScopeVersionCopy := newScopeVersion
		if _, err := tx.Work().TransitionScopeExpansionRequestStatus(ctx, ports.TransitionScopeExpansionRequestStatusRequest{
			RequestID: req.RequestID, ExpectedStatus: workdomain.ScopeExpansionPending, ExpectedVersion: request.Version,
			NextStatus: workdomain.ScopeExpansionApproved, DecidedBy: cmd.Actor, DecidedAt: cmd.RequestedAt,
			ApprovedScopeVersion: &newScopeVersionCopy,
		}); err != nil {
			return err
		}

		// V4-12A (docs/design/06-v4-runtime-engine.md, AK-ARCH-015A):
		// enqueue exactly one SCOPE_EXPANSION_RECONCILE job when — and
		// only when — this request has a real runtime origin (a BLOCKED
		// Attempt raised it via FinalizeExecutionAttempt's own
		// requestScopeExpansionTx, internal/app/runtime). A manually/
		// UI-created request (no ReferencedWorkItemID a runtime task ever
		// wired up, or simply never linked) has no origin row —
		// ErrPersistenceNotFound is the expected, common case there, not
		// an error this command surfaces. This command itself never
		// touches any NodeRun/Attempt (see this function's own doc
		// comment) — it only ever enqueues the job that will.
		if origin, err := tx.Runtime().GetScopeExpansionOriginByRequestID(ctx, req.RequestID); err != nil {
			if !errors.Is(err, ports.ErrPersistenceNotFound) {
				return err
			}
		} else {
			reconcilePayload, err := json.Marshal(ScopeExpansionReconcileJobPayload{
				AttemptID: string(origin.AttemptID), PollGeneration: origin.PollGeneration,
			})
			if err != nil {
				return fmt.Errorf("marshal %s job payload: %w", ScopeExpansionReconcileJobKind, err)
			}
			if _, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
				ID: ports.JobID(ids.NewID()), ProjectID: family.ProjectID, Kind: ScopeExpansionReconcileJobKind,
				AggregateType: "ExecutionAttempt", AggregateID: string(origin.AttemptID), Payload: reconcilePayload,
				AvailableAt: cmd.RequestedAt, MaxClaims: defaultScopeExpansionReconcileJobMaxClaims,
				IdempotencyKey: fmt.Sprintf("scope-expansion-reconcile:%s:%d", origin.AttemptID, origin.PollGeneration),
			}); err != nil {
				return err
			}
		}

		eventPayload, err := json.Marshal(scopeExpansionApprovedEventPayload{
			RequestID: req.RequestID, FamilyID: string(family.ID), ProjectID: string(family.ProjectID),
			NewScopeVersion: newScopeVersion, ApprovedGrants: approvedGrants,
			ReferencedWorkItemID: referencedWorkItemIDString(request.ReferencedWorkItemID), ApprovedBy: cmd.Actor,
		})
		if err != nil {
			return fmt.Errorf("marshal ScopeExpansionApproved payload: %w", err)
		}
		// AggregateType/AggregateID deliberately do NOT reuse
		// "ScopeExpansionRequest"/req.RequestID: this is the SECOND event on
		// that long-lived aggregate identity (the first being
		// RequestScopeExpansion's own ScopeExpansionRequested), and
		// Sequence=1 would collide with it. Instead this mints its own
		// decision-scoped aggregate identity keyed by cmd.ID — guaranteed
		// fresh per genuine (non-replayed) command invocation, the identical
		// "RepositoryProbeRetry"-style pattern
		// internal/app/catalog.RetryRepositoryProbe's own RepositoryProbeRetry
		// event already establishes for the same "second decision on an
		// already-existing aggregate" shape. PayloadJSON.requestId still
		// carries the real correlation back to the ScopeExpansionRequest for
		// any consumer (V4) that needs it.
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-approved", ProjectID: string(family.ProjectID),
			AggregateType: "ScopeExpansionApproval", AggregateID: cmd.ID, Sequence: 1,
			EventType: ScopeExpansionApprovedEventType, SchemaVersion: ScopeExpansionApprovedSchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = ApproveScopeExpansionResult{
			RequestID: req.RequestID, FamilyID: string(family.ID), ProjectID: string(family.ProjectID),
			NewScopeVersion: newScopeVersion, ApprovedGrants: approvedGrants, ProvisionedRepositories: provisioned,
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

func referencedWorkItemIDString(id *workdomain.WorkItemID) string {
	if id == nil {
		return ""
	}
	return string(*id)
}

// --- RejectScopeExpansion ---

// RejectScopeExpansionRequest is what a caller supplies to
// RejectScopeExpansion.
type RejectScopeExpansionRequest struct {
	RequestID    string
	DecisionNote string
}

// RejectScopeExpansionResult is what RejectScopeExpansion returns (and what
// a replayed command receipt reconstructs).
type RejectScopeExpansionResult struct {
	RequestID string `json:"requestId"`
	Status    string `json:"status"`
}

// RejectScopeExpansion is go-core-spec §8's "RejectScopeExpansion | Ghi
// decision, route block/escalation". This command's own scope is exactly
// the first half of that line: it records the decision (CAS-transitions the
// request PENDING -> REJECTED with DecisionNote/DecidedBy/DecidedAt) and
// emits a ScopeExpansionRejected domain event — no RepositoryScope is ever
// touched, no ScopeVersion bump, no WORKSPACE_PROVISION job. "route block/
// escalation" — actually routing this rejection to a blocker/escalation
// mechanism — is deliberately NOT this command's own job: that readiness/
// blocker evidence machinery belongs to a different, concurrently-built
// package entirely (V3-07), never internal/app/work; a future consumer of
// this event is where that routing would live.
func RejectScopeExpansion(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req RejectScopeExpansionRequest) (RejectScopeExpansionResult, error) {
	if strings.TrimSpace(req.RequestID) == "" {
		return RejectScopeExpansionResult{}, errors.New("work: RequestID is required")
	}
	if strings.TrimSpace(req.DecisionNote) == "" {
		return RejectScopeExpansionResult{}, errors.New("work: DecisionNote is required")
	}

	var result RejectScopeExpansionResult
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

		request, err := tx.Work().GetScopeExpansionRequest(ctx, req.RequestID)
		if err != nil {
			return err
		}
		if request.Status != workdomain.ScopeExpansionPending {
			return fmt.Errorf("%w: scope expansion request %s is %s", ErrScopeExpansionNotPending, req.RequestID, request.Status)
		}

		updated, err := tx.Work().TransitionScopeExpansionRequestStatus(ctx, ports.TransitionScopeExpansionRequestStatusRequest{
			RequestID: req.RequestID, ExpectedStatus: workdomain.ScopeExpansionPending, ExpectedVersion: request.Version,
			NextStatus: workdomain.ScopeExpansionRejected, DecidedBy: cmd.Actor, DecidedAt: cmd.RequestedAt,
			DecisionNote: req.DecisionNote,
		})
		if err != nil {
			return err
		}

		eventPayload, err := json.Marshal(scopeExpansionRejectedEventPayload{
			RequestID: req.RequestID, FamilyID: string(request.FamilyID), DecisionNote: req.DecisionNote, RejectedBy: cmd.Actor,
		})
		if err != nil {
			return fmt.Errorf("marshal ScopeExpansionRejected payload: %w", err)
		}
		// See ApproveScopeExpansion's own identical comment for why this
		// mints its own decision-scoped aggregate identity (cmd.ID) rather
		// than reusing "ScopeExpansionRequest"/req.RequestID.
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-rejected", ProjectID: string(request.ProjectID),
			AggregateType: "ScopeExpansionRejection", AggregateID: cmd.ID, Sequence: 1,
			EventType: ScopeExpansionRejectedEventType, SchemaVersion: ScopeExpansionRejectedSchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = RejectScopeExpansionResult{RequestID: req.RequestID, Status: string(updated.Status)}
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

// --- WithdrawScopeExpansion ---

// WithdrawScopeExpansionRequest is what a caller supplies to
// WithdrawScopeExpansion.
type WithdrawScopeExpansionRequest struct {
	RequestID string
}

// WithdrawScopeExpansionResult is what WithdrawScopeExpansion returns (and
// what a replayed command receipt reconstructs).
type WithdrawScopeExpansionResult struct {
	RequestID string `json:"requestId"`
	Status    string `json:"status"`
}

// WithdrawScopeExpansion is go-core-spec §8's "WithdrawScopeExpansion | Thu
// hồi request đang PENDING; idempotent, không tạo grant/amendment". Unlike
// Approve/Reject, this command's own contract explicitly names itself
// "idempotent" at the BUSINESS level, not merely at the command-envelope
// level every mutating command already gets for free from the receipt
// pattern (a byte-identical retry replaying the first call's result): a
// request already WITHDRAWN — reached via ANY prior successful withdrawal,
// regardless of that earlier call's own IdempotencyKey/Actor — makes a
// second WithdrawScopeExpansion call a harmless no-op that returns the
// SAME result, never ErrScopeExpansionNotPending. A request that is instead
// already APPROVED or REJECTED is a genuine misuse (there is nothing left
// to withdraw, and silently succeeding would mask a caller's confusion
// about the request's real state) and still returns
// ErrScopeExpansionNotPending, exactly like Approve/Reject. This command
// never touches family_repository_scopes/task_families.scope_version and
// never enqueues a WORKSPACE_PROVISION job in either branch — go-core-spec's
// own "không tạo grant/amendment" bar, trivially satisfied since withdrawal
// creates no scope grant of any kind, decided or not.
func WithdrawScopeExpansion(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req WithdrawScopeExpansionRequest) (WithdrawScopeExpansionResult, error) {
	if strings.TrimSpace(req.RequestID) == "" {
		return WithdrawScopeExpansionResult{}, errors.New("work: RequestID is required")
	}

	var result WithdrawScopeExpansionResult
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

		request, err := tx.Work().GetScopeExpansionRequest(ctx, req.RequestID)
		if err != nil {
			return err
		}

		if request.Status == workdomain.ScopeExpansionWithdrawn {
			// Semantic idempotency (this function's own doc comment): a
			// harmless no-op, never an error, and never a second event —
			// nothing changed.
			result = WithdrawScopeExpansionResult{RequestID: req.RequestID, Status: string(request.Status)}
		} else {
			if request.Status != workdomain.ScopeExpansionPending {
				return fmt.Errorf("%w: scope expansion request %s is %s", ErrScopeExpansionNotPending, req.RequestID, request.Status)
			}

			updated, err := tx.Work().TransitionScopeExpansionRequestStatus(ctx, ports.TransitionScopeExpansionRequestStatusRequest{
				RequestID: req.RequestID, ExpectedStatus: workdomain.ScopeExpansionPending, ExpectedVersion: request.Version,
				NextStatus: workdomain.ScopeExpansionWithdrawn, DecidedBy: cmd.Actor, DecidedAt: cmd.RequestedAt,
			})
			if err != nil {
				return err
			}

			eventPayload, err := json.Marshal(scopeExpansionWithdrawnEventPayload{
				RequestID: req.RequestID, FamilyID: string(request.FamilyID), WithdrawnBy: cmd.Actor,
			})
			if err != nil {
				return fmt.Errorf("marshal ScopeExpansionWithdrawn payload: %w", err)
			}
			// See ApproveScopeExpansion's own identical comment for why
			// this mints its own decision-scoped aggregate identity
			// (cmd.ID) rather than reusing
			// "ScopeExpansionRequest"/req.RequestID.
			if err := tx.Events().Append(ctx, ports.DomainEvent{
				ID: cmd.ID + "-withdrawn", ProjectID: string(request.ProjectID),
				AggregateType: "ScopeExpansionWithdrawal", AggregateID: cmd.ID, Sequence: 1,
				EventType: ScopeExpansionWithdrawnEventType, SchemaVersion: ScopeExpansionWithdrawnSchemaVersion, PayloadJSON: string(eventPayload),
				CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
			}); err != nil {
				return err
			}

			result = WithdrawScopeExpansionResult{RequestID: req.RequestID, Status: string(updated.Status)}
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
