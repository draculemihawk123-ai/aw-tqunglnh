// Package definitions is V2-10's application-command layer
// (docs/design/04-v2-definition-plane.md V2-10): the one contract
// CLI/API code uses for every DefinitionKind's create/validate/publish/
// list/load, instead of calling internal/adapters/sqlite or a kind's own
// domain Compile function directly ("CLI/API sau này dùng một contract,
// không gọi compiler/repository trực tiếp"). Every mutating command
// follows V1-06's own established shape (see
// internal/adapters/sqlite/command_handler_example_test.go's
// handleCreateProject): a command-receipt idempotency check, the real
// work, and a domain event plus a fresh receipt, all inside one
// ports.UnitOfWork.WithSerializedWrite call — so GC-INV-15 ("Mọi state
// transition tạo domain event trong cùng database transaction") holds by
// construction, never as a separate step a caller could forget.
//
// Compiling a candidate Version is deliberately NOT this package's own
// job for the eight shared DefinitionKinds: each kind's own Compile
// function is pure (no I/O, no ctx) and lives in that kind's own domain
// package (internal/domain/block, .../skill, ...), with its own concrete
// Definition/Document/PublishRequest types this package would otherwise
// have to import eight of just to dispatch by Kind — a wide fan-out this
// package has no other reason to carry. Instead, the caller — who
// already holds the concrete typed Document (e.g. a CLI command that
// just decoded a BlockDocument from a file) — closes over that kind's
// own Compile call in a PublishDefinitionVersionRequest.Compile (or
// ValidateDraftRequest.Compile) func value, and this package's own code
// is what actually invokes it, at the right point in its own
// validate/idempotency/persist/event/receipt sequence. CLI/API code
// therefore still never manually sequences "compile, then check
// idempotency, then persist, then emit an event, then record a receipt"
// itself — it calls exactly one function here that does all of that
// atomically — while this package stays free of a compile-time
// dependency on all eight leaf compiler packages.
//
// Kind == definition.KindWorkflow is the one case with a different
// shape: its own compiler (internal/app/workflowcompiler.CompileAndResolve)
// needs a UnitOfWork of its own to resolve dependency pins against the
// real registry, so a Workflow candidate is supplied as
// WorkflowDefinition/WorkflowRequest instead of a Compile func value —
// see PublishDefinitionVersion's own doc comment for exactly how and why
// its sequencing differs from the eight shared kinds.
package definitions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workflowcompiler"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// versionFieldsDTO is the JSON-serializable mirror of
// definition.VersionFields (whose own fields are all unexported —
// V2-01's own "không có update/delete public trên Version" immutability
// discipline extends to giving it no exported constructor a bare
// json.Unmarshal could drive either) — used only for the command-receipt
// ResultJSON a replay reconstructs its result from.
type versionFieldsDTO struct {
	ID               string                        `json:"id"`
	DefinitionID     string                        `json:"definitionId"`
	Kind             definition.Kind               `json:"kind"`
	VersionNumber    uint64                        `json:"versionNumber"`
	SchemaVersion    int                           `json:"schemaVersion"`
	CanonicalSource  string                        `json:"canonicalSource"`
	SourceHash       string                        `json:"sourceHash"`
	CompiledSnapshot string                        `json:"compiledSnapshot"`
	CompiledHash     string                        `json:"compiledHash"`
	Dependencies     definition.DependencyManifest `json:"dependencies"`
	PublishedBy      string                        `json:"publishedBy"`
	PublishedAt      time.Time                     `json:"publishedAt"`
}

func toVersionFieldsDTO(v definition.VersionFields) versionFieldsDTO {
	return versionFieldsDTO{
		ID: v.ID(), DefinitionID: v.DefinitionID(), Kind: v.Kind(),
		VersionNumber: v.VersionNumber(), SchemaVersion: v.SchemaVersion(),
		CanonicalSource: v.CanonicalSource(), SourceHash: v.SourceHash(),
		CompiledSnapshot: v.CompiledSnapshot(), CompiledHash: v.CompiledHash(),
		Dependencies: v.Dependencies(), PublishedBy: v.PublishedBy(), PublishedAt: v.PublishedAt(),
	}
}

func (dto versionFieldsDTO) toVersionFields() (definition.VersionFields, error) {
	return definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID: dto.ID, DefinitionID: dto.DefinitionID, Kind: dto.Kind,
		VersionNumber: dto.VersionNumber, SchemaVersion: dto.SchemaVersion,
		CanonicalSource: dto.CanonicalSource, SourceHash: dto.SourceHash,
		CompiledSnapshot: dto.CompiledSnapshot, CompiledHash: dto.CompiledHash,
		Dependencies: dto.Dependencies, PublishedBy: dto.PublishedBy, PublishedAt: dto.PublishedAt,
	})
}

// CreateDefinitionRequest is what a caller supplies to CreateDefinition.
type CreateDefinitionRequest struct {
	DefinitionID string
	Kind         definition.Kind
	Scope        definition.Scope
	Name         string
}

// CreateDefinitionResult is what CreateDefinition returns (and what a
// replayed command-receipt reconstructs).
type CreateDefinitionResult struct {
	DefinitionID string          `json:"definitionId"`
	Kind         definition.Kind `json:"kind"`
}

// CreateDefinition creates a new Definition — DRAFT, generation 1 — for
// any of the nine DefinitionKinds, Workflow included (its own row lives
// in workflow_definitions rather than the shared definitions table, but
// DefinitionsRepository.CreateDefinition hides that routing). It follows
// V1-06's own idempotent-command shape: a retry with the same
// IdempotencyKey and RequestHash replays the first call's result without
// creating anything twice; the same key with a different RequestHash is
// rejected as ports.ErrReceiptConflict.
func CreateDefinition(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req CreateDefinitionRequest) (CreateDefinitionResult, error) {
	if strings.TrimSpace(req.DefinitionID) == "" {
		return CreateDefinitionResult{}, errors.New("definitions: DefinitionID is required")
	}

	var result CreateDefinitionResult
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

		if err := tx.Definitions().CreateDefinition(ctx, req.DefinitionID, req.Kind, req.Scope, req.Name, cmd.RequestedAt); err != nil {
			return err
		}

		projectID := ""
		if !req.Scope.IsGlobal() {
			projectID = string(*req.Scope.ProjectID)
		}
		payload, err := json.Marshal(struct {
			DefinitionID string `json:"definitionId"`
			Kind         string `json:"kind"`
			Name         string `json:"name"`
		}{DefinitionID: req.DefinitionID, Kind: string(req.Kind), Name: req.Name})
		if err != nil {
			return fmt.Errorf("marshal DefinitionCreated payload: %w", err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-created", ProjectID: projectID,
			AggregateType: "Definition", AggregateID: req.DefinitionID, Sequence: 1,
			EventType: "DefinitionCreated", SchemaVersion: 1, PayloadJSON: string(payload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = CreateDefinitionResult{DefinitionID: req.DefinitionID, Kind: req.Kind}
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

// ValidateDraftRequest is what a caller supplies to ValidateDraft.
type ValidateDraftRequest struct {
	Kind definition.Kind

	// Compile is required when Kind != definition.KindWorkflow: the
	// caller's own closure over that kind's pure, no-I/O Compile call
	// (e.g. func() (definition.VersionFields, error) { return
	// block.Compile(def, req) }).
	Compile func() (definition.VersionFields, error)

	// WorkflowDefinition/WorkflowRequest are required when Kind ==
	// definition.KindWorkflow: workflowcompiler.CompileAndResolve's own
	// two inputs.
	WorkflowDefinition workflow.WorkflowDefinition
	WorkflowRequest    workflow.PublishRequest
}

// ValidateDraft is a read-only dry run: it compiles (and, for Workflow,
// resolves every dependency pin against the real registry) a candidate
// Version without publishing it — no Command envelope, because a dry run
// has no side effect to make idempotent. The caller gets back either a
// valid compiled candidate or the exact error ValidateDocument/Compile/
// CompileAndResolve would have raised at real publish time.
func ValidateDraft(ctx context.Context, uow ports.UnitOfWork, req ValidateDraftRequest) (definition.VersionFields, error) {
	if req.Kind == definition.KindWorkflow {
		version, err := workflowcompiler.CompileAndResolve(ctx, uow, req.WorkflowDefinition, req.WorkflowRequest)
		if err != nil {
			return definition.VersionFields{}, err
		}
		return workflowVersionToVersionFields(version)
	}
	if req.Compile == nil {
		return definition.VersionFields{}, errors.New("definitions: ValidateDraft requires Compile for a non-workflow kind")
	}
	return req.Compile()
}

// PublishDefinitionVersionRequest is what a caller supplies to
// PublishDefinitionVersion. Exactly one of Compile or
// WorkflowDefinition/WorkflowRequest applies, selected by Kind — see
// PublishDefinitionVersion's own doc comment for why Workflow's shape
// differs from the other eight kinds'.
type PublishDefinitionVersionRequest struct {
	DefinitionID string
	Kind         definition.Kind

	// Compile is required when Kind != definition.KindWorkflow.
	Compile func() (definition.VersionFields, error)

	// WorkflowDefinition/WorkflowRequest are required when Kind ==
	// definition.KindWorkflow.
	WorkflowDefinition workflow.WorkflowDefinition
	WorkflowRequest    workflow.PublishRequest
}

// PublishDefinitionVersion is V2-10's own "Hoàn thành khi" bar: publish
// event + version + receipt atomic. It follows V1-06's idempotent-command
// shape (receipt-check, real work, event+receipt, all inside one
// WithSerializedWrite) with one necessary variation for Kind ==
// definition.KindWorkflow: workflowcompiler.CompileAndResolve needs its
// own read-only UnitOfWork snapshot to resolve dependency pins against
// the real registry, and calling WithReadOnly (or WithSerializedWrite)
// again from inside an already-open WithSerializedWrite call is a
// forbidden nested transaction (fake.ErrNestedTransaction; real sqlite's
// RunSerializedWrite/BeginTx do not support nesting either) — so for
// Workflow specifically, compiling happens first, entirely outside the
// write transaction, and only the already-resolved candidate is carried
// into it. For the other eight kinds, Compile is pure (no I/O) and runs
// directly inside the write transaction closure, exactly where the
// brief-equivalent design calls for it.
//
// Idempotent-republish-vs-event: this function distinguishes two
// entirely different kinds of "duplicate" and answers each one
// deliberately, not by accident of code structure:
//
//  1. Same IdempotencyKey + same RequestHash (a literal retry — e.g. a
//     network retry of the exact same command): the receipt-check at the
//     top of the transaction replays the stored ResultJSON and returns
//     immediately. It never reaches PublishVersion, never reaches
//     Events().Append. Zero new version rows, zero new events, on the
//     second call — proven by TestPublishDefinitionVersion_DuplicatePublish_SameIdempotencyKey_ReplaysWithoutNewEventOrVersion.
//
//  2. Same compiled content (DefinitionID + CompiledSnapshotHash) but a
//     genuinely different command (a different IdempotencyKey — e.g. two
//     independent callers happen to publish byte-identical content, or
//     the same caller intentionally republishes unchanged content under
//     a fresh command): the receipt-check does NOT short-circuit (it is
//     a different receipt key), so this function actually calls
//     PublishVersion/PublishWorkflowVersion — which itself deduplicates
//     by (DefinitionID, CompiledSnapshotHash) and returns the
//     already-published Version rather than inserting a new row
//     (AK-ARCH-005B: dedup by CompiledSnapshotHash, never SourceHash).
//     But this function goes one step further than the repository layer
//     alone would: before calling publish, it checks (via
//     Definitions().ListVersions) whether a version with this exact
//     CompiledHash already exists for this Definition, and only appends
//     a DefinitionVersionPublished event when it does not. A second
//     command publishing identical content therefore still gets its own
//     receipt (proving that command was processed and audited), but
//     fires zero new domain events and creates zero new version rows —
//     proven by TestPublishDefinitionVersion_DifferentIdempotencyKey_SameContent_DedupesVersionAndEvent.
//     This also sidesteps a real correctness hazard: this package has no
//     per-aggregate event-sequence allocator of its own
//     (ports.EventsRepository's own doc comment defers real sequence
//     allocation to whichever later task first needs it, same as V1-06
//     left it), so the DefinitionVersionPublished event below is keyed
//     to the published Version's own immutable ID as its AggregateID
//     (never the Definition's — DefinitionCreated already claims
//     Sequence=1 on that aggregate) with a constant Sequence=1: safe
//     because a given VersionID is only ever published once, ever — the
//     isNewVersion check below guarantees this branch runs at most once
//     per distinct compiled content, so Sequence=1 on that Version's own
//     aggregate identity can never collide with anything.
func PublishDefinitionVersion(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req PublishDefinitionVersionRequest) (definition.VersionFields, error) {
	if strings.TrimSpace(req.DefinitionID) == "" {
		return definition.VersionFields{}, errors.New("definitions: DefinitionID is required")
	}

	var workflowCandidate *workflow.WorkflowVersion
	if req.Kind == definition.KindWorkflow {
		version, err := workflowcompiler.CompileAndResolve(ctx, uow, req.WorkflowDefinition, req.WorkflowRequest)
		if err != nil {
			return definition.VersionFields{}, err
		}
		workflowCandidate = &version
	} else if req.Compile == nil {
		return definition.VersionFields{}, errors.New("definitions: PublishDefinitionVersion requires Compile for a non-workflow kind")
	}

	var result definition.VersionFields
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		existingReceipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if found {
			if existingReceipt.RequestHash != cmd.RequestHash {
				return ports.ErrReceiptConflict
			}
			var dto versionFieldsDTO
			if err := json.Unmarshal([]byte(existingReceipt.ResultJSON), &dto); err != nil {
				return fmt.Errorf("decode replayed receipt result: %w", err)
			}
			result, err = dto.toVersionFields()
			return err
		}

		var candidateFields definition.VersionFields
		var compiledHash string
		if req.Kind == definition.KindWorkflow {
			compiledHash = workflowCandidate.ContentHash()
		} else {
			candidate, err := req.Compile()
			if err != nil {
				return err
			}
			if candidate.DefinitionID() != req.DefinitionID {
				return fmt.Errorf("definitions: compiled candidate belongs to definition %q, want %q", candidate.DefinitionID(), req.DefinitionID)
			}
			candidateFields = candidate
			compiledHash = candidate.CompiledHash()
		}

		existingVersions, err := tx.Definitions().ListVersions(ctx, req.Kind, req.DefinitionID)
		if err != nil {
			return err
		}
		isNewVersion := true
		for _, existing := range existingVersions {
			if existing.CompiledHash() == compiledHash {
				isNewVersion = false
				break
			}
		}

		var published definition.VersionFields
		if req.Kind == definition.KindWorkflow {
			publishedWorkflow, err := tx.Definitions().PublishWorkflowVersion(ctx, req.WorkflowDefinition, *workflowCandidate)
			if err != nil {
				return err
			}
			published, err = workflowVersionToVersionFields(publishedWorkflow)
			if err != nil {
				return err
			}
		} else {
			published, err = tx.Definitions().PublishVersion(ctx, publishVersionRequestFromFields(req.DefinitionID, candidateFields))
			if err != nil {
				return err
			}
		}

		if isNewVersion {
			payload, err := json.Marshal(struct {
				DefinitionID  string `json:"definitionId"`
				VersionID     string `json:"versionId"`
				Kind          string `json:"kind"`
				VersionNumber uint64 `json:"versionNumber"`
				CompiledHash  string `json:"compiledHash"`
			}{req.DefinitionID, published.ID(), string(req.Kind), published.VersionNumber(), published.CompiledHash()})
			if err != nil {
				return fmt.Errorf("marshal DefinitionVersionPublished payload: %w", err)
			}
			// AggregateID is the published Version's own immutable ID, not
			// the Definition's — deliberately, since this package has no
			// real per-aggregate event-sequence allocator of its own
			// (ports.EventsRepository's own doc comment defers that to a
			// later task, same as V1-06 left it) and DefinitionCreated
			// above already claims Sequence=1 on the Definition aggregate.
			// A given VersionID is only ever published once, ever (the
			// CompiledHash dedup above guarantees isNewVersion is true at
			// most once per distinct compiled content), so Sequence=1 on
			// the Version's own aggregate identity can never collide —
			// with the Definition's own event stream or with any other
			// Version's.
			if err := tx.Events().Append(ctx, ports.DomainEvent{
				ID: cmd.ID + "-published", AggregateType: "DefinitionVersion", AggregateID: published.ID(),
				Sequence: 1, EventType: "DefinitionVersionPublished",
				SchemaVersion: 1, PayloadJSON: string(payload),
				CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
			}); err != nil {
				return err
			}
		}

		result = published
		resultJSON, err := json.Marshal(toVersionFieldsDTO(published))
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

// ListVersions returns every Version definitionID has published, oldest
// first — a read-only query, never a Command.
func ListVersions(ctx context.Context, uow ports.UnitOfWork, kind definition.Kind, definitionID string) ([]definition.VersionFields, error) {
	var result []definition.VersionFields
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		versions, err := tx.Definitions().ListVersions(ctx, kind, definitionID)
		result = versions
		return err
	})
	return result, err
}

// LoadVersion returns the Version with the given ID, or
// ports.ErrDefinitionVersionNotFound — a read-only query, never a
// Command. It only ever resolves a shared-kind Version (see
// ports.DefinitionsRepository.LoadVersion's own doc comment).
func LoadVersion(ctx context.Context, uow ports.UnitOfWork, versionID string) (definition.VersionFields, error) {
	var result definition.VersionFields
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		fields, err := tx.Definitions().LoadVersion(ctx, versionID)
		result = fields
		return err
	})
	return result, err
}

func publishVersionRequestFromFields(definitionID string, fields definition.VersionFields) ports.PublishVersionRequest {
	return ports.PublishVersionRequest{
		DefinitionID: definitionID, Kind: fields.Kind(), VersionID: fields.ID(),
		SchemaVersion: fields.SchemaVersion(), CanonicalSource: fields.CanonicalSource(),
		SourceHash: fields.SourceHash(), CompiledSnapshot: fields.CompiledSnapshot(),
		CompiledHash: fields.CompiledHash(), Dependencies: fields.Dependencies(),
		PublishedBy: fields.PublishedBy(), PublishedAt: fields.PublishedAt(),
	}
}

// workflowVersionToVersionFields converts a compiled workflow.WorkflowVersion
// into the kind-agnostic definition.VersionFields shape this package
// returns uniformly for every Kind. It duplicates
// internal/adapters/sqlite's own equivalent (definitions.go's
// workflowVersionToDefinitionFields) and fake's
// (fakeWorkflowVersionToDefinitionFields) rather than sharing code with
// either: this package must never import the sqlite adapter (V1-05's own
// "app service có thể test không SQLite" boundary), and importing the
// fake test-double package from real application code would be
// backwards.
func workflowVersionToVersionFields(v workflow.WorkflowVersion) (definition.VersionFields, error) {
	var dependencies definition.DependencyManifest
	for _, pin := range v.Dependencies().Pins {
		dependencies.Pins = append(dependencies.Pins, definition.DependencyPin{
			Kind: definition.Kind(pin.Kind), DefinitionID: pin.Key, VersionID: pin.Version,
		})
	}
	schemaVersion, _ := strconv.Atoi(v.SchemaVersion())
	return definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID: string(v.ID()), DefinitionID: string(v.DefinitionID()), Kind: definition.KindWorkflow,
		VersionNumber: v.VersionNumber(), SchemaVersion: schemaVersion,
		CanonicalSource: string(v.CanonicalContent()), SourceHash: v.ContentHash(),
		CompiledSnapshot: string(v.CanonicalContent()), CompiledHash: v.ContentHash(),
		Dependencies: dependencies, PublishedBy: v.PublishedBy(), PublishedAt: v.PublishedAt(),
	})
}
