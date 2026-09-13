// Package catalog is V3-01/V3-02's application-command layer
// (docs/design/05-v3-project-workspace.md): RegisterRepository,
// AssignComponentPack and RetryRepositoryProbe are the three named public
// commands docs/architecture/04-go-core-spec.md §8's command table calls
// out for this task's own scope (RetryRepositoryProbe added by V3-02, on
// the exact same rung as the other two). All three follow V1-06's
// established idempotent envelope — a command-receipt idempotency check,
// the real work, and a domain event plus a fresh receipt, all inside one
// ports.UnitOfWork.WithSerializedWrite call — the same shape
// internal/app/definitions/commands.go's CreateDefinition/
// PublishDefinitionVersion already establish (see that file's own
// package doc comment).
//
// RegisterRepository's own "Thực hiện" line is the more demanding of the
// two: it must atomically create the new Repository row (always
// REGISTERING, project.NewRepository's own rule) AND enqueue the
// REPOSITORY_PROBE durable job in the exact same transaction
// (docs/architecture/04-go-core-spec.md §4.1's "RegisterRepository
// atomically tạo record REGISTERING và probe job/outbox nguyên tử") —
// this package composes ports.Tx's Catalog(), Jobs() and Events()
// accessors inside one WithSerializedWrite call to make that true by
// construction, never as a separate step a caller could forget. Neither
// this package nor any part of V3-01 ever claims or processes that job:
// executing the actual probe (REGISTERING -> PROBING -> ACTIVE|BLOCKED)
// is entirely V3-02's job, against real Git/toolchain evidence.
//
// CreateComponent is deliberately NOT one of V3-01's own named public
// commands: go-core-spec's §8 command table never lists it (Component
// discovery/creation is V3-02's onboarding-probe job, per
// docs/harness-engineering/06-lec-06-khoi-tao-la-phase-rieng.md
// HE-06-M05's "onboarding MUST tạo hoặc xác nhận topology Repository ->
// Component -> EngineeringPack"). It lives here anyway, as a thin
// non-enveloped helper (no Command/receipt/event — its own DB-level
// UNIQUE(repository_id, path) constraint is this task's only replay
// protection, which is sufficient for a helper nothing yet calls twice
// with intent to replay), only because AssignComponentPack needs an
// existing Component to pin a pack version to and V3-01 adds no CLI (so
// there is no other caller yet that would otherwise create one for a
// test or for V3-02 to build on).
//
// CreateProject/ListProjects/GetProject are V6-03's own addition
// (docs/design/08-v6-api-projections.md V6-03): the public,
// idempotent/scope-checked authority this package was always missing for
// the one catalog aggregate every other command in this file already
// depends on (RegisterRepository/CreateComponent both require an existing
// Project) — until V6-03, the only way to create one at all was the bare
// ports.CatalogRepository.CreateProject persistence method this task's own
// "Không làm" line now forbids delivery from calling directly. CreateProject
// follows RegisterRepository/AssignComponentPack's exact idempotent
// envelope; ListProjects/GetProject follow ListProjectRepositories'
// read-only shape below, with an explicit ports.CommandScope check neither
// of those two needs (both are always implicitly scoped by an already-
// resolved projectID/componentID parameter) because ADR-025 makes
// CreateProject/ListProjects installation-scoped while every other command
// and query in this file is project-scoped — see CreateProject/ListProjects/
// GetProject's own doc comments for the exact ADR-025 rule each enforces.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// RepositoryProbeJobKind is durable_jobs.kind's value for the job
// RegisterRepository (and RetryRepositoryProbe, below) enqueue.
// durable_jobs.kind is a plain TEXT NOT NULL column with no CHECK
// constraint (internal/adapters/sqlite/migrations/0001_initial_schema.sql),
// so introducing this new kind string needs no migration change. Exported
// (V3-02 renamed this from the original unexported repositoryProbeJobKind)
// because V3-02's own internal/app/repositoryprobe package is this job
// kind's real consumer — the workerpool.Registry.Register(kind, handler)
// call site needs the exact same string RegisterRepository/
// RetryRepositoryProbe already enqueue, and a second, independently
// maintained string literal would be exactly the kind of drift-prone
// duplication a single exported constant exists to prevent.
const RepositoryProbeJobKind = "REPOSITORY_PROBE"

// defaultProbeJobMaxClaims mirrors this codebase's own most common
// durable-job MaxClaims value for a single logical unit of work (see
// e.g. internal/adapters/sqlite/crashworker.go, spikeacceptance's own
// node-dispatch fixtures) — V3-02's own future probe handler owns
// deciding its real retry policy; this is only what makes the row a
// valid durable_jobs insert today.
const defaultProbeJobMaxClaims = 3

// CreateProjectRequest is what a caller supplies to CreateProject. There
// is deliberately no ID field: unlike RegisterRepository's own
// RepositoryID (a caller-chosen identity, ports.RegisterRepositoryRequest's
// own doc comment), a Project's ID is application-generated
// (internal/app/idsource's own "application tạo ID, không để persistence
// adapter tự sinh" convention) — the same rule CreateComponent already
// follows below (`id := ids.NewID()`), since nothing yet gives a caller a
// meaningful identity to name a brand-new Project by.
type CreateProjectRequest struct {
	Name string
}

// CreateProjectResult is what CreateProject returns (and what a replayed
// command-receipt reconstructs) — first execution's ProjectID is the one
// idsource minted; a replay returns that exact same ID, never a freshly
// minted one (see CreateProject's own doc comment).
type CreateProjectResult struct {
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Status    string `json:"status"`
}

// CreateProject is V6-03's own named public command
// (docs/design/08-v6-api-projections.md V6-03: "bổ sung public
// CreateProject ... còn thiếu trước HTTP/CLI"): the one authority a
// delivery layer (HTTP/CLI, not yet built) is allowed to call to create a
// Project — closing the exact gap this task's own "Không làm" line names
// ("không để delivery gọi Tx.Catalog().CreateProject"). It follows every
// other named command in this package's own idempotent shape: a
// command-receipt idempotency check, the real work, and a domain event
// plus a fresh receipt, all inside one ports.UnitOfWork.WithSerializedWrite
// call.
//
// ADR-025 (docs/architecture/02-architecture-decisions.md §27) lists
// CreateProject explicitly in the closed installation-scoped command set
// ("CreateProject, safe-settings mutation, ProbeAdapterBuild,
// RegisterAdapterBuild") — there is no existing Project yet for this
// command to be scoped to, so cmd.Scope MUST be ports.InstallationScope();
// a project-scoped cmd is rejected as ports.ErrScopeMismatch before any
// receipt lookup or write. This corrects, rather than follows,
// internal/adapters/sqlite/command_handler_example_test.go's own
// "handleCreateProject" illustrative handler (V1-06, predating ADR-025 by
// several tasks): that teaching example's own
// TestHandleCreateProject_InstallationScopedCommand_Rejected asserts the
// opposite (project-scoped, installation-scope rejected) — it was V1-06's
// own best guess before ADR-025 existed to settle the question, is never
// invoked by any real command dispatch (its own types and handler func are
// unexported to that one _test.go file), and is deliberately left
// unchanged here: rewriting a historical illustrative example to match a
// later ADR would misrepresent what V1-06 itself actually decided at the
// time, the same immutable-history discipline ADR-008/ADR-015 already
// apply to real persisted events.
//
// First execution mints a fresh ProjectID via idsource (never letting
// sqlite auto-generate one, idsource's own package doc) and creates the
// Project row — always ACTIVE, generation 1, project.NewProject's own
// rule. A retry with the same IdempotencyKey and RequestHash replays the
// first call's result, including that exact same ProjectID, without
// minting a second ID or creating a second row/event; the same key with a
// different RequestHash is rejected as ports.ErrReceiptConflict.
//
// The appended ProjectCreated event's own ProjectID field is set to the
// newly created Project's own ID — not left empty the way an
// installation-scoped event normally would be (ports.DomainEvent.ProjectID's
// own doc comment: "empty means installation-scoped") — because that
// field tracks which Project's own event stream/journal an event belongs
// to, not which CommandScope produced it: this Project's own future
// per-project journal (V6-08/V6-09's eventual projection) MUST include its
// own genesis event, the same reasoning RegisterRepository's own
// RepositoryRegistered event already applies by setting ProjectID to the
// Repository's actual owning project below.
func CreateProject(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req CreateProjectRequest) (CreateProjectResult, error) {
	if !cmd.Scope.IsInstallation() {
		return CreateProjectResult{}, fmt.Errorf("%w: CreateProject is installation-scoped (ADR-025), not %s", ports.ErrScopeMismatch, cmd.Scope.Key())
	}
	if strings.TrimSpace(req.Name) == "" {
		return CreateProjectResult{}, errors.New("catalog: Name is required")
	}

	var result CreateProjectResult
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

		projectID := ids.NewID()
		created, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: req.Name})
		if err != nil {
			return err
		}

		eventPayload, err := json.Marshal(projectCreatedEventPayload{
			ProjectID: string(created.ID), Name: created.Name, Status: string(created.Status),
		})
		if err != nil {
			return fmt.Errorf("marshal ProjectCreated payload: %w", err)
		}
		// AggregateID is the new Project's own ID; Sequence=1 is safe
		// because CreateProject is the only command that ever creates a
		// Project row, and the receipt check above guarantees this branch
		// runs at most once per distinct (Actor, Scope, IdempotencyKey,
		// Type) — the same reasoning RegisterRepository's own
		// RepositoryRegistered event already relies on.
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-created", ProjectID: string(created.ID),
			AggregateType: "Project", AggregateID: string(created.ID), Sequence: 1,
			EventType: ProjectCreatedEventType, SchemaVersion: ProjectCreatedSchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = CreateProjectResult{ProjectID: string(created.ID), Name: created.Name, Status: string(created.Status)}
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

// RegisterRepositoryRequest is what a caller supplies to RegisterRepository.
type RegisterRepositoryRequest struct {
	RepositoryID  string
	ProjectID     string
	Name          string
	RemoteLocator string
	DefaultRef    string
}

// RegisterRepositoryResult is what RegisterRepository returns (and what a
// replayed command-receipt reconstructs).
type RegisterRepositoryResult struct {
	RepositoryID string `json:"repositoryId"`
	ProjectID    string `json:"projectId"`
	Status       string `json:"status"`
	ProbeJobID   string `json:"probeJobId"`
}

// RegisterRepository atomically creates a new Repository row (always
// RepositoryRegistering) and enqueues its REPOSITORY_PROBE durable job in
// one transaction, then appends a RepositoryRegistered domain event and
// records the command receipt — all composed inside a single
// ports.UnitOfWork.WithSerializedWrite call, so a crash between any two
// of those steps is impossible: either everything commits, or nothing
// does. It follows V1-06's idempotent-command shape: a retry with the
// same IdempotencyKey and RequestHash replays the first call's result
// (including the same ProbeJobID) without creating a second Repository
// row or a second probe job; the same key with a different RequestHash
// is rejected as ports.ErrReceiptConflict.
func RegisterRepository(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req RegisterRepositoryRequest) (RegisterRepositoryResult, error) {
	if strings.TrimSpace(req.RepositoryID) == "" {
		return RegisterRepositoryResult{}, errors.New("catalog: RepositoryID is required")
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return RegisterRepositoryResult{}, errors.New("catalog: ProjectID is required")
	}

	var result RegisterRepositoryResult
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

		created, err := tx.Catalog().RegisterRepository(ctx, ports.RegisterRepositoryRequest{
			ID: req.RepositoryID, ProjectID: req.ProjectID, Name: req.Name,
			RemoteLocator: req.RemoteLocator, DefaultRef: req.DefaultRef,
		})
		if err != nil {
			return err
		}

		jobPayload, err := json.Marshal(struct {
			RepositoryID string `json:"repositoryId"`
			ProjectID    string `json:"projectId"`
		}{RepositoryID: req.RepositoryID, ProjectID: req.ProjectID})
		if err != nil {
			return fmt.Errorf("marshal %s job payload: %w", RepositoryProbeJobKind, err)
		}
		jobID := ids.NewID()
		job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(jobID), ProjectID: project.ProjectID(req.ProjectID), Kind: RepositoryProbeJobKind,
			AggregateType: "Repository", AggregateID: req.RepositoryID, Payload: jobPayload,
			AvailableAt: cmd.RequestedAt, MaxClaims: defaultProbeJobMaxClaims,
			IdempotencyKey: cmd.IdempotencyKey + "-probe",
		})
		if err != nil {
			return err
		}

		eventPayload, err := json.Marshal(repositoryRegisteredEventPayload{
			RepositoryID: req.RepositoryID, ProjectID: req.ProjectID,
			Name: created.Name, Status: string(created.Status), ProbeJobID: string(job.ID),
		})
		if err != nil {
			return fmt.Errorf("marshal RepositoryRegistered payload: %w", err)
		}
		// AggregateID is the new Repository's own ID; Sequence=1 is safe
		// because RegisterRepository is the only command in this task's
		// scope that ever creates a Repository row, and the receipt check
		// above guarantees this branch runs at most once per distinct
		// (Actor, Scope, IdempotencyKey, Type) — the same reasoning
		// internal/app/definitions.CreateDefinition's own DefinitionCreated
		// event already relies on.
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-registered", ProjectID: req.ProjectID,
			AggregateType: "Repository", AggregateID: req.RepositoryID, Sequence: 1,
			EventType: RepositoryRegisteredEventType, SchemaVersion: RepositoryRegisteredSchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = RegisterRepositoryResult{
			RepositoryID: req.RepositoryID, ProjectID: req.ProjectID,
			Status: string(created.Status), ProbeJobID: string(job.ID),
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

// RetryRepositoryProbeRequest is what a caller supplies to
// RetryRepositoryProbe. The caller is expected to have already loaded the
// Repository (e.g. via GetRepository) and pass the exact Version it
// observed as cmd.ExpectedVersion — this command is this codebase's first
// genuine call site for ports.Command.ExpectedVersion
// (docs/architecture/04-go-core-spec.md §3's "Mọi command mutation mang
// ... ExpectedVersion khi sửa aggregate đã tồn tại"), since RegisterRepository
// only ever creates a fresh row and V3-01 itself had no update path at all.
type RetryRepositoryProbeRequest struct {
	RepositoryID string
	ProjectID    string
}

// RetryRepositoryProbeResult is what RetryRepositoryProbe returns (and
// what a replayed command-receipt reconstructs).
type RetryRepositoryProbeResult struct {
	RepositoryID string `json:"repositoryId"`
	ProjectID    string `json:"projectId"`
	Status       string `json:"status"`
	ProbeJobID   string `json:"probeJobId"`
}

// RetryRepositoryProbe is V3-02's own named public command
// (docs/architecture/04-go-core-spec.md §8's command table: "RetryRepositoryProbe
// | Probe lại repository BLOCKED"): it CAS-transitions a BLOCKED
// Repository back to PROBING (project.CanTransitionRepositoryStatus's own
// closed BLOCKED->PROBING edge) against cmd.ExpectedVersion, clears any
// stale LastProbeErrorCode (a fresh probe is about to run — see
// ports.TransitionRepositoryStatusRequest's own doc comment for why this
// is not a third "leave unchanged" mode), and enqueues a brand new
// REPOSITORY_PROBE durable job — never the old failed job's row.
// internal/adapters/sqlite/crashworker.go's own crash-worker fault points
// are this codebase's established precedent for "mint a fresh job/attempt
// rather than resurrect a dead one" (a crashed attempt is never revived in
// place); RetryBlockedActivation's own go-core-spec §8 line ("tạo
// activation/Attempt mới sau admission blocker; không hồi sinh Attempt
// cũ") states the identical principle for a different aggregate. Both the
// Repository-row CAS and the fresh job enqueue happen inside the same
// WithSerializedWrite call, exactly like RegisterRepository's own atomic
// row+job creation above — a crash between the two is impossible.
//
// Follows V1-06's idempotent-command shape like RegisterRepository:  a
// retry with the same IdempotencyKey and RequestHash replays the first
// call's result (including the same ProbeJobID) without transitioning the
// Repository or enqueuing a second job; the same key with a different
// RequestHash is rejected as ports.ErrReceiptConflict. Rejects a mismatched
// ProjectID as ports.ErrCrossProjectReference, resolving the Repository's
// actual project from its own stored row rather than trusting the
// request — the same discipline CreateComponent/AssignComponentPack
// already follow for their own parent references.
func RetryRepositoryProbe(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req RetryRepositoryProbeRequest) (RetryRepositoryProbeResult, error) {
	if strings.TrimSpace(req.RepositoryID) == "" {
		return RetryRepositoryProbeResult{}, errors.New("catalog: RepositoryID is required")
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return RetryRepositoryProbeResult{}, errors.New("catalog: ProjectID is required")
	}
	if cmd.ExpectedVersion == 0 {
		return RetryRepositoryProbeResult{}, errors.New("catalog: RetryRepositoryProbe requires cmd.ExpectedVersion (the Repository version the caller observed)")
	}

	var result RetryRepositoryProbeResult
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

		current, err := tx.Catalog().GetRepository(ctx, req.RepositoryID)
		if err != nil {
			return err
		}
		if string(current.ProjectID) != req.ProjectID {
			return fmt.Errorf("%w: repository %s belongs to project %s, not %s",
				ports.ErrCrossProjectReference, req.RepositoryID, current.ProjectID, req.ProjectID)
		}

		updated, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: req.RepositoryID, ExpectedStatus: project.RepositoryBlocked, ExpectedVersion: cmd.ExpectedVersion,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		})
		if err != nil {
			return err
		}

		jobPayload, err := json.Marshal(struct {
			RepositoryID string `json:"repositoryId"`
			ProjectID    string `json:"projectId"`
		}{RepositoryID: req.RepositoryID, ProjectID: req.ProjectID})
		if err != nil {
			return fmt.Errorf("marshal %s job payload: %w", RepositoryProbeJobKind, err)
		}
		jobID := ids.NewID()
		job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(jobID), ProjectID: project.ProjectID(req.ProjectID), Kind: RepositoryProbeJobKind,
			AggregateType: "Repository", AggregateID: req.RepositoryID, Payload: jobPayload,
			AvailableAt: cmd.RequestedAt, MaxClaims: defaultProbeJobMaxClaims,
			IdempotencyKey: cmd.IdempotencyKey + "-probe",
		})
		if err != nil {
			return err
		}

		eventPayload, err := json.Marshal(repositoryProbeRetriedEventPayload{
			RepositoryID: req.RepositoryID, ProjectID: req.ProjectID,
			Status: string(updated.Status), ProbeJobID: string(job.ID),
		})
		if err != nil {
			return fmt.Errorf("marshal RepositoryProbeRetried payload: %w", err)
		}
		// AggregateID is the freshly minted probe job's own ID, not the
		// Repository's — deliberately, the same technique
		// AssignComponentPack's own ComponentPackAssigned event uses (see
		// that function's doc comment): this package has no real
		// per-aggregate event-sequence allocator for "Repository" (whose
		// own event stream already claims Sequence=1 via
		// RegisterRepository's RepositoryRegistered, with no safe way from
		// here to know what the next number should be under concurrent
		// retries), and a freshly minted job ID is only ever assigned
		// once, ever, so Sequence=1 on its own aggregate identity can
		// never collide with any other event.
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-retried", ProjectID: req.ProjectID,
			AggregateType: "RepositoryProbeRetry", AggregateID: string(job.ID), Sequence: 1,
			EventType: RepositoryProbeRetriedEventType, SchemaVersion: RepositoryProbeRetriedSchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = RetryRepositoryProbeResult{
			RepositoryID: req.RepositoryID, ProjectID: req.ProjectID,
			Status: string(updated.Status), ProbeJobID: string(job.ID),
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

// CreateComponentRequest is what a caller supplies to CreateComponent.
type CreateComponentRequest struct {
	ProjectID    string
	RepositoryID string
	Name         string
	Path         string
	Kind         string
}

// CreateComponent mints a fresh ComponentID via idsource (application
// code, never internal/domain/project itself, mints new identity — see
// internal/app/idsource's own package doc) and persists a new Component
// row. See this package's own doc comment for why this is not a full
// idempotent Command — it is not one of V3-01's cited public commands,
// and its own UNIQUE(repository_id, path) constraint already prevents
// a genuine duplicate.
func CreateComponent(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, req CreateComponentRequest) (project.Component, error) {
	id := ids.NewID()
	var result project.Component
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		created, err := tx.Catalog().CreateComponent(ctx, ports.CreateComponentRequest{
			ID: id, ProjectID: req.ProjectID, RepositoryID: req.RepositoryID,
			Name: req.Name, Path: req.Path, Kind: req.Kind,
		})
		result = created
		return err
	})
	return result, err
}

// AssignComponentPackRequest is what a caller supplies to
// AssignComponentPack.
type AssignComponentPackRequest struct {
	ProjectID     string
	ComponentID   string
	PackVersionID string
	EffectiveAt   time.Time
	Actor         string
}

// AssignComponentPackResult is what AssignComponentPack returns (and what
// a replayed command-receipt reconstructs).
type AssignComponentPackResult struct {
	AssignmentID  string    `json:"assignmentId"`
	ComponentID   string    `json:"componentId"`
	PackVersionID string    `json:"packVersionId"`
	EffectiveAt   time.Time `json:"effectiveAt"`
	Actor         string    `json:"actor"`
}

// AssignComponentPack pins a new EngineeringPackVersion to a Component
// with an effective time and acting actor, then appends a
// ComponentPackAssigned domain event and records the command receipt —
// composed inside one WithSerializedWrite call, following the same
// idempotent-command shape RegisterRepository above and
// internal/app/definitions.CreateDefinition already establish. This is a
// deliberate consistency choice, not a default applied without thought:
// AssignComponentPack has a real downstream consumer (whatever later
// resolves "the effective pack configuration" needs to know this exact
// assignment was durably recorded and audited, matching every other
// mutating command in this codebase — GC-INV-35's own idempotency ledger
// requirement), and nothing about pinning a pack version is so trivial
// that it could safely skip the receipt/event a caller might rely on for
// audit or replay-safety; unlike CreateComponent above (which has no
// cited public command at all and only this task's own DB-level
// UNIQUE(repository_id, path) constraint standing in for replay
// protection), AssignComponentPack IS one of go-core-spec's own named
// public commands, so it gets the full envelope every other named
// command gets. It never mutates or replaces an existing assignment: a
// second call (a genuinely new command, not a replay) always creates a
// new row with its own EffectiveAt (project.ComponentPackAssignment's own
// doc comment).
func AssignComponentPack(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req AssignComponentPackRequest) (AssignComponentPackResult, error) {
	if strings.TrimSpace(req.ComponentID) == "" {
		return AssignComponentPackResult{}, errors.New("catalog: ComponentID is required")
	}

	var result AssignComponentPackResult
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

		assignmentID := ids.NewID()
		created, err := tx.Catalog().AssignComponentPack(ctx, ports.AssignComponentPackRequest{
			ID: assignmentID, ProjectID: req.ProjectID, ComponentID: req.ComponentID,
			PackVersionID: req.PackVersionID, EffectiveAt: req.EffectiveAt, Actor: req.Actor,
		})
		if err != nil {
			return err
		}

		eventPayload, err := json.Marshal(componentPackAssignedEventPayload{
			AssignmentID: string(created.ID), ComponentID: req.ComponentID,
			PackVersionID: req.PackVersionID, EffectiveAt: created.EffectiveAt, Actor: created.Actor,
		})
		if err != nil {
			return fmt.Errorf("marshal ComponentPackAssigned payload: %w", err)
		}
		// AggregateID is the new assignment's own immutable ID, not the
		// Component's — deliberately, the same technique
		// internal/app/definitions.PublishDefinitionVersion's own
		// DefinitionVersionPublished event uses (see that function's doc
		// comment): this package has no real per-aggregate event-sequence
		// allocator (ports.EventsRepository's own doc comment defers that
		// to whichever later task first needs it), and a Component may
		// already have prior ComponentPackAssignment rows (append-only,
		// V3-01's own "provenance" requirement) whose own events already
		// claim Sequence=1 on the Component aggregate. A freshly minted
		// assignmentID is only ever assigned once, ever, so Sequence=1 on
		// its own aggregate identity can never collide with any other
		// assignment's event or the Component's own event stream.
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-assigned", ProjectID: req.ProjectID,
			AggregateType: "ComponentPackAssignment", AggregateID: assignmentID, Sequence: 1,
			EventType: ComponentPackAssignedEventType, SchemaVersion: ComponentPackAssignedSchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = AssignComponentPackResult{
			AssignmentID: string(created.ID), ComponentID: req.ComponentID,
			PackVersionID: string(created.PackVersionID), EffectiveAt: created.EffectiveAt, Actor: created.Actor,
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

// ListProjects returns every Project row, ID order — a read-only query,
// never a Command. ADR-025 lists ListProjects itself as one of the closed
// installation-scoped queries (docs/architecture/02-architecture-decisions.md
// §27's own table), so scope MUST be ports.InstallationScope(); anything
// else (a project scope naming one specific Project) is rejected as
// ports.ErrScopeMismatch — there is no "list scoped to a project" query,
// since a Project is itself the scope unit everything else scopes by.
func ListProjects(ctx context.Context, uow ports.UnitOfWork, scope ports.CommandScope) ([]project.Project, error) {
	if !scope.IsInstallation() {
		return nil, fmt.Errorf("%w: ListProjects is installation-scoped (ADR-025), not %s", ports.ErrScopeMismatch, scope.Key())
	}
	var result []project.Project
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		projects, err := tx.Catalog().ListProjects(ctx)
		result = projects
		return err
	})
	return result, err
}

// GetProject reloads the authoritative Project with the given ID directly
// from persistence — never a projection — a read-only query, never a
// Command. GetProject is not one of ADR-025's closed installation-scoped
// queries (§27's own table only names ListProjects, not GetProject), so
// per that ADR's own "Mọi query project-scoped vẫn MUST scope bằng
// ProjectID" rule, scope MUST be ports.ProjectScope(projectID) — the exact
// Project being requested, never the installation scope and never a scope
// naming a different Project. This mirrors RetryRepositoryProbe's own
// discipline above (resolving a referenced row's actual project from the
// stored row, never trusting the request alone) applied to authorization
// rather than referential integrity: a caller already scoped to one
// Project can never read a different Project's detail by ID alone.
func GetProject(ctx context.Context, uow ports.UnitOfWork, scope ports.CommandScope, projectID string) (project.Project, error) {
	if strings.TrimSpace(projectID) == "" {
		return project.Project{}, errors.New("catalog: ProjectID is required")
	}
	scopedProjectID, isProjectScoped := scope.ProjectID()
	if !isProjectScoped || scopedProjectID != projectID {
		return project.Project{}, fmt.Errorf("%w: GetProject requires project scope %q, got %q", ports.ErrScopeMismatch, projectID, scope.Key())
	}
	var result project.Project
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, err := tx.Catalog().GetProject(ctx, projectID)
		result = loaded
		return err
	})
	return result, err
}

// ListProjectRepositories returns every Repository whose stored
// project_id column equals projectID — a read-only query, never a
// Command (mirroring internal/app/definitions.ListVersions's own shape).
func ListProjectRepositories(ctx context.Context, uow ports.UnitOfWork, projectID string) ([]project.Repository, error) {
	var result []project.Repository
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		repos, err := tx.Catalog().ListProjectRepositories(ctx, projectID)
		result = repos
		return err
	})
	return result, err
}

// ListComponentPackAssignments returns every ComponentPackAssignment for
// componentID, oldest-EffectiveAt-first — a read-only query, never a
// Command.
func ListComponentPackAssignments(ctx context.Context, uow ports.UnitOfWork, componentID string) ([]project.ComponentPackAssignment, error) {
	var result []project.ComponentPackAssignment
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		assignments, err := tx.Catalog().ListComponentPackAssignments(ctx, componentID)
		result = assignments
		return err
	})
	return result, err
}

// GetEffectiveComponentPackAssignment returns the ComponentPackAssignment
// effective at time `at` for componentID — a read-only query, never a
// Command.
func GetEffectiveComponentPackAssignment(ctx context.Context, uow ports.UnitOfWork, componentID string, at time.Time) (project.ComponentPackAssignment, error) {
	var result project.ComponentPackAssignment
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		assignment, err := tx.Catalog().GetEffectiveComponentPackAssignment(ctx, componentID, at)
		result = assignment
		return err
	})
	return result, err
}

// GetRepository, ListRepositoryProbeAttempts, GetComponent and
// ListComponents below are V6-03A's own addition
// (docs/design/08-v6-api-projections.md V6-03A): the four read-only
// queries "repository ... detail/onboarding/probe-history" and "component
// query" need that this package was still missing before its own HTTP
// route task could wrap them, the same "delivery MUST NOT call
// Tx.Catalog() directly" gap CreateProject/GetProject closed for Project
// in V6-03 above.
//
// GetRepository and GetComponent are DELIBERATELY unlike GetProject: they
// take no ports.CommandScope parameter and run no scope check at all.
// GetProject's own caller already KNOWS which Project it is asking about
// (every GetProject call site is reached through a route already nested
// under /projects/{id}, or an equivalent already-resolved context) and
// uses scope to assert that claim against the authoritative row. A bare
// `GET /repositories/{id}` or `POST /components/{id}/pack-assignments`
// route has no such prior context — Repository ID and Component ID are
// each already a global, unique, caller-opaque identity (V3-01's own
// "Repository identity là ID đã đăng ký, MUST NOT suy từ path, slug,
// current directory hoặc remote URL"), so a caller reaches either by ID
// alone. These two queries are how a caller FIRST learns which Project a
// given Repository/Component belongs to — V6-03A's own task line "route
// reload authoritative target để suy Project/scope" (the general contract
// in §1.3 of the design doc) made concrete: an HTTP handler calls
// GetRepository/GetComponent first, then builds ports.ProjectScope from
// the RESULT's own ProjectID field for every scope-sensitive thing it
// does next (a receipt lookup's own scope key, a nested command's own
// cmd.Scope) — never the reverse. This is not a laxer authorization rule
// than GetProject's; it is the one query that necessarily runs BEFORE any
// scope is known at all, the same role RetryRepositoryProbe's own
// "resolve the Repository's actual project from its own stored row"
// already plays for referential integrity, applied here one step earlier
// in the flow.
func GetRepository(ctx context.Context, uow ports.UnitOfWork, repositoryID string) (project.Repository, error) {
	if strings.TrimSpace(repositoryID) == "" {
		return project.Repository{}, errors.New("catalog: RepositoryID is required")
	}
	var result project.Repository
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, err := tx.Catalog().GetRepository(ctx, repositoryID)
		result = loaded
		return err
	})
	return result, err
}

// ListRepositoryProbeAttempts returns every RepositoryProbeAttempt for
// repositoryID, oldest-CreatedAt-first — the "probe history" evidence
// docs/design/01-system-design.md §6.1's own API sketch names for `GET
// /repositories/{id}/onboarding` ("trạng thái/error/probe history có thể
// hành động"). A read-only query, never a Command; like GetRepository
// above, it takes no scope (an HTTP caller reaching this by RepositoryID
// alone reloads GetRepository first for that purpose — the two queries
// are meant to be called together by that one route, never a substitute
// for one another).
func ListRepositoryProbeAttempts(ctx context.Context, uow ports.UnitOfWork, repositoryID string) ([]ports.RepositoryProbeAttempt, error) {
	if strings.TrimSpace(repositoryID) == "" {
		return nil, errors.New("catalog: RepositoryID is required")
	}
	var result []ports.RepositoryProbeAttempt
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempts, err := tx.Catalog().ListRepositoryProbeAttempts(ctx, repositoryID)
		result = attempts
		return err
	})
	return result, err
}

// GetComponent reloads the authoritative Component with the given ID
// directly from persistence — never a projection — a read-only query,
// never a Command. See GetRepository's own doc comment above for exactly
// why this takes no ports.CommandScope: Component ID is itself already a
// caller-opaque global identity, and this is the one query an HTTP
// caller reaching `/components/{id}/...` by ID alone uses to first learn
// the Component's own ProjectID before building any scope.
func GetComponent(ctx context.Context, uow ports.UnitOfWork, componentID string) (project.Component, error) {
	if strings.TrimSpace(componentID) == "" {
		return project.Component{}, errors.New("catalog: ComponentID is required")
	}
	var result project.Component
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, err := tx.Catalog().GetComponent(ctx, componentID)
		result = loaded
		return err
	})
	return result, err
}

// ListComponents returns every Component whose stored ProjectID equals
// projectID, ID order — the "GET /projects/{id}/components | catalog
// component đã được repository onboarding/probe discover" query
// (docs/design/01-system-design.md §6's own API sketch). A read-only
// query, never a Command. Unlike GetRepository/GetComponent above, this
// IS reached from a route already nested under /projects/{id}
// (mirroring ListProjectRepositories above), so a caller already knows
// projectID going in — no separate scope parameter is needed here either,
// for the opposite reason: the caller already has it, the same shape
// ListProjectRepositories/ListComponentPackAssignments already establish
// for an unscoped list-by-foreign-key read.
func ListComponents(ctx context.Context, uow ports.UnitOfWork, projectID string) ([]project.Component, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, errors.New("catalog: ProjectID is required")
	}
	var result []project.Component
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		components, err := tx.Catalog().ListComponents(ctx, projectID)
		result = components
		return err
	})
	return result, err
}
