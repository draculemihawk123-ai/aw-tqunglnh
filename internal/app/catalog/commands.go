// Package catalog is V3-01's application-command layer
// (docs/design/05-v3-project-workspace.md): RegisterRepository and
// AssignComponentPack, the two named public commands
// docs/architecture/04-go-core-spec.md §8's command table calls out for
// this task's own scope. Both follow V1-06's established idempotent
// envelope — a command-receipt idempotency check, the real work, and a
// domain event plus a fresh receipt, all inside one
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

// repositoryProbeJobKind is durable_jobs.kind's value for the job
// RegisterRepository enqueues. durable_jobs.kind is a plain TEXT NOT NULL
// column with no CHECK constraint (internal/adapters/sqlite/migrations/0001_initial_schema.sql),
// so introducing this new kind string needs no migration change. Nothing
// in this task claims or processes a job of this kind — that is entirely
// V3-02's job.
const repositoryProbeJobKind = "REPOSITORY_PROBE"

// defaultProbeJobMaxClaims mirrors this codebase's own most common
// durable-job MaxClaims value for a single logical unit of work (see
// e.g. internal/adapters/sqlite/crashworker.go, spikeacceptance's own
// node-dispatch fixtures) — V3-02's own future probe handler owns
// deciding its real retry policy; this is only what makes the row a
// valid durable_jobs insert today.
const defaultProbeJobMaxClaims = 3

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
			return fmt.Errorf("marshal %s job payload: %w", repositoryProbeJobKind, err)
		}
		jobID := ids.NewID()
		job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(jobID), ProjectID: project.ProjectID(req.ProjectID), Kind: repositoryProbeJobKind,
			AggregateType: "Repository", AggregateID: req.RepositoryID, Payload: jobPayload,
			AvailableAt: cmd.RequestedAt, MaxClaims: defaultProbeJobMaxClaims,
			IdempotencyKey: cmd.IdempotencyKey + "-probe",
		})
		if err != nil {
			return err
		}

		eventPayload, err := json.Marshal(struct {
			RepositoryID string `json:"repositoryId"`
			ProjectID    string `json:"projectId"`
			Name         string `json:"name"`
			Status       string `json:"status"`
			ProbeJobID   string `json:"probeJobId"`
		}{
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
			EventType: "RepositoryRegistered", SchemaVersion: 1, PayloadJSON: string(eventPayload),
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

		eventPayload, err := json.Marshal(struct {
			AssignmentID  string    `json:"assignmentId"`
			ComponentID   string    `json:"componentId"`
			PackVersionID string    `json:"packVersionId"`
			EffectiveAt   time.Time `json:"effectiveAt"`
			Actor         string    `json:"actor"`
		}{
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
			EventType: "ComponentPackAssigned", SchemaVersion: 1, PayloadJSON: string(eventPayload),
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
