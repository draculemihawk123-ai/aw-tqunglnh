// Package fake provides in-memory test doubles for internal/app/ports
// interfaces, so an application-layer test never has to import
// internal/adapters/sqlite (docs/design/03-v1-alpha-foundation.md V1-05's
// "app service có thể test không SQLite").
package fake

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// ErrNestedTransaction is returned when WithSerializedWrite or
// WithReadOnly is called again from inside an already-open transaction —
// nesting a transaction inside another is exactly what V1-05 forbids
// ("không nested transaction... trong UoW"), and a fake that silently
// allowed it would let a handler test pass against behavior a real
// adapter must reject.
var ErrNestedTransaction = errors.New("fake: nested UnitOfWork call (WithSerializedWrite/WithReadOnly called from inside an already-open transaction)")

// UnitOfWork is an in-memory ports.UnitOfWork. Each call works on a clone
// of the last committed Snapshot; the clone only replaces Snapshot if fn
// returns nil, giving the same commit/rollback semantics a real adapter
// has without needing separate undo logic.
type UnitOfWork struct {
	mu       sync.Mutex
	inTx     bool
	Snapshot Tx
}

// New returns a ready-to-use fake UnitOfWork with an empty Snapshot.
func New() *UnitOfWork {
	return &UnitOfWork{Snapshot: newTx()}
}

var _ ports.UnitOfWork = (*UnitOfWork)(nil)

func (u *UnitOfWork) WithSerializedWrite(_ context.Context, fn func(ports.Tx) error) error {
	return u.run(fn, true)
}

func (u *UnitOfWork) WithReadOnly(_ context.Context, fn func(ports.Tx) error) error {
	return u.run(fn, false)
}

func (u *UnitOfWork) run(fn func(ports.Tx) error, persistOnSuccess bool) error {
	u.mu.Lock()
	if u.inTx {
		u.mu.Unlock()
		return ErrNestedTransaction
	}
	u.inTx = true
	working := u.Snapshot.clone()
	u.mu.Unlock()

	err := fn(working)

	u.mu.Lock()
	u.inTx = false
	if err == nil && persistOnSuccess {
		u.Snapshot = working
	}
	u.mu.Unlock()
	return err
}

// Tx is the fake's in-memory ports.Tx. Every repository accessor
// currently returns a zero-method placeholder value (mirroring
// ports.Tx's own placeholder interfaces — see its doc comment), except
// Events and Receipts, which V1-06 gives real in-memory behavior; a
// concern gains real, stateful fields here in the same task that gives
// ports.<Concern>Repository real methods. Events/Receipts are held by
// pointer so a handler's writes inside fn are visible to that same fn
// call; clone() deep-copies both before each attempt so a failed or
// read-only attempt never mutates the committed Snapshot.
type Tx struct {
	catalog          *CatalogRepository
	work             *WorkRepository
	definitions      *DefinitionsRepository
	runtime          *RuntimeRepository
	jobs             *JobsRepository
	events           *EventsRepository
	receipts         *ReceiptsRepository
	adapterBuilds    *AdapterBuildRepository
	readiness        *ReadinessRepository
	wait             *WaitRepository
	approvals        *ApprovalRepository
	artifacts        *ArtifactRepository
	messages         *MessageRepository
	contextSnapshots *ContextSnapshotRepository
	agentEvents      *AgentEventsRepository
	checkpoints      *CheckpointsRepository
	safeSettings     *SafeSettingsRepository
}

func newTx() Tx {
	catalog := &CatalogRepository{}
	runtimeRepo := &RuntimeRepository{}
	jobsRepo := &JobsRepository{runtime: runtimeRepo}
	// V4-13: RuntimeRepository's own ListOrphanedRunningExecutionAttempts
	// needs to read JobsRepository's own lease state — the reverse
	// direction of the cross-reference JobsRepository.runtime already
	// established in V4-12B. Wired after both exist (both are pointers, so
	// this two-step construction is safe) rather than trying to construct
	// either first.
	runtimeRepo.jobs = jobsRepo
	// workRepo is pre-declared (rather than an inline literal) because
	// MessageRepository below needs the same pointer newTx assigns to
	// Tx.work — mirroring how catalog/runtimeRepo are already pre-declared
	// for the identical reason.
	workRepo := &WorkRepository{catalog: catalog}
	return Tx{
		events:           &EventsRepository{},
		receipts:         &ReceiptsRepository{},
		adapterBuilds:    &AdapterBuildRepository{},
		definitions:      &DefinitionsRepository{},
		catalog:          catalog,
		work:             workRepo,
		runtime:          runtimeRepo,
		jobs:             jobsRepo,
		readiness:        &ReadinessRepository{catalog: catalog},
		wait:             &WaitRepository{},
		approvals:        &ApprovalRepository{},
		artifacts:        &ArtifactRepository{catalog: catalog},
		messages:         &MessageRepository{work: workRepo, runtime: runtimeRepo},
		contextSnapshots: &ContextSnapshotRepository{runtime: runtimeRepo},
		agentEvents:      &AgentEventsRepository{},
		checkpoints:      &CheckpointsRepository{},
		// safeSettings is seeded at Version 1 with the zero-value ("never
		// configured") desired document — mirroring migration
		// 0036_safe_settings.sql's own seeded singleton row exactly, so a
		// handler test sees the identical starting state against either
		// implementation.
		safeSettings: &SafeSettingsRepository{record: ports.SafeSettingsRecord{Version: 1}},
	}
}

func (t Tx) clone() Tx {
	clone := t
	clone.events = t.events.clone()
	clone.receipts = t.receipts.clone()
	clone.adapterBuilds = t.adapterBuilds.clone()
	clone.definitions = t.definitions.clone()
	clone.catalog = t.catalog.clone()
	clone.work = t.work.cloneWith(clone.catalog)
	clone.runtime = t.runtime.clone()
	clone.jobs = t.jobs.cloneWith(clone.runtime)
	// See newTx's own doc comment on this same two-step wiring.
	clone.runtime.jobs = clone.jobs
	clone.readiness = t.readiness.cloneWith(clone.catalog)
	clone.wait = t.wait.clone()
	clone.approvals = t.approvals.clone()
	clone.artifacts = t.artifacts.cloneWith(clone.catalog)
	clone.messages = t.messages.cloneWith(clone.work, clone.runtime)
	clone.contextSnapshots = t.contextSnapshots.cloneWith(clone.runtime)
	clone.agentEvents = t.agentEvents.clone()
	clone.checkpoints = t.checkpoints.clone()
	clone.safeSettings = t.safeSettings.clone()
	return clone
}

var _ ports.Tx = Tx{}

func (t Tx) Catalog() ports.CatalogRepository                  { return t.catalog }
func (t Tx) Work() ports.WorkRepository                        { return t.work }
func (t Tx) Definitions() ports.DefinitionsRepository          { return t.definitions }
func (t Tx) Runtime() ports.RuntimeRepository                  { return t.runtime }
func (t Tx) Jobs() ports.JobsRepository                        { return t.jobs }
func (t Tx) Events() ports.EventsRepository                    { return t.events }
func (t Tx) Receipts() ports.ReceiptsRepository                { return t.receipts }
func (t Tx) AdapterBuilds() ports.AdapterBuildRepository       { return t.adapterBuilds }
func (t Tx) Readiness() ports.ReadinessRepository              { return t.readiness }
func (t Tx) Wait() ports.WaitRepository                        { return t.wait }
func (t Tx) Approvals() ports.ApprovalRepository               { return t.approvals }
func (t Tx) Artifacts() ports.ArtifactRepository               { return t.artifacts }
func (t Tx) Messages() ports.MessageRepository                 { return t.messages }
func (t Tx) ContextSnapshots() ports.ContextSnapshotRepository { return t.contextSnapshots }
func (t Tx) AgentEvents() ports.AgentEventsRepository          { return t.agentEvents }
func (t Tx) Checkpoints() ports.CheckpointsRepository          { return t.checkpoints }
func (t Tx) SafeSettings() ports.SafeSettingsRepository        { return t.safeSettings }

// EventsRepository is an in-memory ports.EventsRepository: Append rejects
// a duplicate (aggregate_type, aggregate_id, sequence) the same way the
// sqlite adapter's UNIQUE constraint does, so a handler test exercises
// the same conflict behavior against either implementation.
type EventsRepository struct {
	items []ports.DomainEvent
}

var _ ports.EventsRepository = (*EventsRepository)(nil)

func (e *EventsRepository) clone() *EventsRepository {
	items := make([]ports.DomainEvent, len(e.items))
	copy(items, e.items)
	return &EventsRepository{items: items}
}

func (e *EventsRepository) Append(_ context.Context, event ports.DomainEvent) error {
	for _, existing := range e.items {
		if existing.AggregateType == event.AggregateType &&
			existing.AggregateID == event.AggregateID &&
			existing.Sequence == event.Sequence {
			return fmt.Errorf("fake: duplicate domain event (aggregate_type=%q aggregate_id=%q sequence=%d)",
				event.AggregateType, event.AggregateID, event.Sequence)
		}
	}
	e.items = append(e.items, event)
	return nil
}

// Items returns a copy of every event appended so far, newest last — for
// test assertions.
func (e *EventsRepository) Items() []ports.DomainEvent {
	items := make([]ports.DomainEvent, len(e.items))
	copy(items, e.items)
	return items
}

// ReceiptsRepository is an in-memory ports.ReceiptsRepository, keyed the
// same way the sqlite adapter's command_receipts primary key is (actor,
// scope key, idempotency key, command type).
type ReceiptsRepository struct {
	records map[receiptKey]ports.Receipt
}

type receiptKey struct {
	actor          string
	scopeKey       string
	idempotencyKey string
	commandType    string
}

var _ ports.ReceiptsRepository = (*ReceiptsRepository)(nil)

func (r *ReceiptsRepository) clone() *ReceiptsRepository {
	records := make(map[receiptKey]ports.Receipt, len(r.records))
	for k, v := range r.records {
		records[k] = v
	}
	return &ReceiptsRepository{records: records}
}

func (r *ReceiptsRepository) key(actor string, scope ports.CommandScope, idempotencyKey, commandType string) receiptKey {
	return receiptKey{actor: actor, scopeKey: scope.Key(), idempotencyKey: idempotencyKey, commandType: commandType}
}

func (r *ReceiptsRepository) Load(_ context.Context, actor string, scope ports.CommandScope, idempotencyKey, commandType string) (ports.Receipt, bool, error) {
	receipt, ok := r.records[r.key(actor, scope, idempotencyKey, commandType)]
	return receipt, ok, nil
}

func (r *ReceiptsRepository) Record(_ context.Context, receipt ports.Receipt) error {
	key := r.key(receipt.Actor, receipt.Scope, receipt.IdempotencyKey, receipt.CommandType)
	if existing, ok := r.records[key]; ok {
		if existing.RequestHash != receipt.RequestHash {
			return ports.ErrReceiptConflict
		}
		return nil
	}
	if r.records == nil {
		r.records = map[receiptKey]ports.Receipt{}
	}
	r.records[key] = receipt
	return nil
}

// DefinitionsRepository is an in-memory ports.DefinitionsRepository —
// V2-09 gave this concern its first real behavior (LoadVersion); V2-10
// adds CreateDefinition/PublishVersion/PublishWorkflowVersion/ListVersions,
// so an application-command test never needs sqlite (the same V1-05
// discipline fake.AdapterBuildRepository already follows). A test can
// still seed known Version data directly via Seed, standing in for a
// publish that already happened before the code under test ever runs.
type DefinitionsRepository struct {
	versions            map[string]definition.VersionFields    // by Version ID (shared kinds)
	definitions         map[string]definitionRecord            // by Definition ID (shared kinds)
	workflowDefinitions map[string]workflow.WorkflowDefinition // by Definition ID
	workflowVersions    map[string][]workflow.WorkflowVersion  // by Definition ID, oldest first
}

// definitionRecord is the fake's in-memory stand-in for one row of the
// real definitions table: just enough of definition.Fields for
// PublishVersion's own CanPublish/cross-project checks to run against.
type definitionRecord struct {
	Kind   definition.Kind
	Scope  definition.Scope
	Name   string
	Status definition.Status
}

var _ ports.DefinitionsRepository = (*DefinitionsRepository)(nil)

func (d *DefinitionsRepository) clone() *DefinitionsRepository {
	versions := make(map[string]definition.VersionFields, len(d.versions))
	for k, v := range d.versions {
		versions[k] = v
	}
	definitions := make(map[string]definitionRecord, len(d.definitions))
	for k, v := range d.definitions {
		definitions[k] = v
	}
	workflowDefinitions := make(map[string]workflow.WorkflowDefinition, len(d.workflowDefinitions))
	for k, v := range d.workflowDefinitions {
		workflowDefinitions[k] = v
	}
	workflowVersions := make(map[string][]workflow.WorkflowVersion, len(d.workflowVersions))
	for k, v := range d.workflowVersions {
		workflowVersions[k] = append([]workflow.WorkflowVersion(nil), v...)
	}
	return &DefinitionsRepository{
		versions: versions, definitions: definitions,
		workflowDefinitions: workflowDefinitions, workflowVersions: workflowVersions,
	}
}

func (d *DefinitionsRepository) LoadVersion(_ context.Context, versionID string) (definition.VersionFields, error) {
	fields, ok := d.versions[versionID]
	if !ok {
		return definition.VersionFields{}, ports.ErrDefinitionVersionNotFound
	}
	return fields, nil
}

// Seed registers fields as resolvable by its own ID() — test setup
// standing in for a real publish that already happened before the code
// under test ever runs.
func (d *DefinitionsRepository) Seed(fields definition.VersionFields) {
	if d.versions == nil {
		d.versions = map[string]definition.VersionFields{}
	}
	d.versions[fields.ID()] = fields
}

// CreateDefinition mirrors sqlite's createSharedDefinitionTx/
// definitionsRepository.CreateDefinition: KindWorkflow upserts an
// in-memory workflow_definitions-equivalent record (a repeat call with
// the same identity is a safe no-op, mirroring ensureWorkflowDefinition's
// own ON CONFLICT DO NOTHING + identity-match behavior); every other kind
// starts a fresh Definition at DRAFT/generation 1 via definition.Create.
func (d *DefinitionsRepository) CreateDefinition(_ context.Context, id string, kind definition.Kind, scope definition.Scope, name string, _ time.Time) error {
	if kind == definition.KindWorkflow {
		if existing, ok := d.workflowDefinitions[id]; ok {
			if existing.Name != name {
				return fmt.Errorf("fake: workflow definition %s already exists with a different identity", id)
			}
			return nil
		}
		wfDefinition := workflow.WorkflowDefinition{
			ID: workflow.WorkflowDefinitionID(id), Name: name,
			Status: workflow.DefinitionStatus(definition.StatusDraft), Version: 1,
		}
		if !scope.IsGlobal() {
			pid := *scope.ProjectID
			wfDefinition.ProjectID = &pid
		}
		if d.workflowDefinitions == nil {
			d.workflowDefinitions = map[string]workflow.WorkflowDefinition{}
		}
		d.workflowDefinitions[id] = wfDefinition
		return nil
	}

	if _, exists := d.definitions[id]; exists {
		return fmt.Errorf("fake: definition %s already exists", id)
	}
	fields, err := definition.Create(definition.CreateRequest{Kind: kind, Scope: scope, Name: name})
	if err != nil {
		return err
	}
	if d.definitions == nil {
		d.definitions = map[string]definitionRecord{}
	}
	d.definitions[id] = definitionRecord{Kind: fields.Kind, Scope: fields.Scope, Name: fields.Name, Status: fields.Status}
	return nil
}

// PublishVersion mirrors sqlite's publishSharedDefinitionVersionTx: it
// serves the eight shared kinds only (never KindWorkflow — see
// ports.DefinitionsRepository's own doc comment on PublishVersion for
// why), validates the Definition can still publish, resolves each
// dependency pin's own project against this fake's records (never
// trusting the pin's own claim, the same real-repository discipline
// ports.ErrCrossProjectDependency documents), and deduplicates by
// (DefinitionID, CompiledHash) — never SourceHash (AK-ARCH-005B).
func (d *DefinitionsRepository) PublishVersion(_ context.Context, req ports.PublishVersionRequest) (definition.VersionFields, error) {
	if req.Kind == definition.KindWorkflow {
		return definition.VersionFields{}, errors.New("fake: DefinitionsRepository.PublishVersion does not support KindWorkflow — use PublishWorkflowVersion instead")
	}
	record, ok := d.definitions[req.DefinitionID]
	if !ok {
		return definition.VersionFields{}, fmt.Errorf("fake: %w: definition %s", ports.ErrPersistenceNotFound, req.DefinitionID)
	}
	if err := definition.CanPublish(record.Status); err != nil {
		return definition.VersionFields{}, err
	}

	for _, pin := range req.Dependencies.Pins {
		depRecord, ok := d.definitions[pin.DefinitionID]
		if !ok {
			return definition.VersionFields{}, fmt.Errorf("fake: %w: dependency %s not found", ports.ErrPersistenceNotFound, pin.DefinitionID)
		}
		if !sameDefinitionScope(depRecord.Scope, record.Scope) {
			return definition.VersionFields{}, fmt.Errorf("%w: dependency %s", ports.ErrCrossProjectDependency, pin.DefinitionID)
		}
	}

	// Idempotent republish: identical compiled content for this
	// definition already exists — return it rather than "insert" a
	// duplicate.
	var nextVersionNo uint64
	for _, existing := range d.versions {
		if existing.DefinitionID() != req.DefinitionID {
			continue
		}
		if existing.CompiledHash() == req.CompiledHash {
			return existing, nil
		}
		if existing.VersionNumber() > nextVersionNo {
			nextVersionNo = existing.VersionNumber()
		}
	}
	nextVersionNo++

	fields, err := definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID: req.VersionID, DefinitionID: req.DefinitionID, Kind: req.Kind,
		VersionNumber: nextVersionNo, SchemaVersion: req.SchemaVersion,
		CanonicalSource: req.CanonicalSource, SourceHash: req.SourceHash,
		CompiledSnapshot: req.CompiledSnapshot, CompiledHash: req.CompiledHash,
		Dependencies: req.Dependencies, PublishedBy: req.PublishedBy, PublishedAt: req.PublishedAt,
	})
	if err != nil {
		return definition.VersionFields{}, err
	}
	if d.versions == nil {
		d.versions = map[string]definition.VersionFields{}
	}
	d.versions[fields.ID()] = fields
	return fields, nil
}

// PublishWorkflowVersion mirrors sqlite's publishWorkflowVersionTx: an
// already-compiled candidate is idempotent by ContentHash, a candidate ID
// reused with different content is rejected, and VersionNumber must match
// the next number this fake would allocate.
func (d *DefinitionsRepository) PublishWorkflowVersion(_ context.Context, def workflow.WorkflowDefinition, candidate workflow.WorkflowVersion) (workflow.WorkflowVersion, error) {
	if def.ID == "" || candidate.ID() == "" || candidate.DefinitionID() != def.ID {
		return workflow.WorkflowVersion{}, errors.New("fake: PublishWorkflowVersion requires a definition and a matching candidate")
	}
	if existing, ok := d.workflowDefinitions[string(def.ID)]; ok {
		if existing.Name != def.Name || existing.Status != def.Status || existing.Version != def.Version {
			return workflow.WorkflowVersion{}, fmt.Errorf("fake: %w: workflow definition %s differs from its persisted identity", ports.ErrPersistenceAlreadyExists, def.ID)
		}
	} else {
		if d.workflowDefinitions == nil {
			d.workflowDefinitions = map[string]workflow.WorkflowDefinition{}
		}
		d.workflowDefinitions[string(def.ID)] = def
	}

	existingVersions := d.workflowVersions[string(def.ID)]
	for _, existing := range existingVersions {
		if existing.ContentHash() == candidate.ContentHash() {
			return existing, nil
		}
	}
	for _, versions := range d.workflowVersions {
		for _, existing := range versions {
			if existing.ID() == candidate.ID() {
				return workflow.WorkflowVersion{}, fmt.Errorf("fake: %w: workflow version %s already stores hash %s", ports.ErrImmutableVersionConflict, candidate.ID(), existing.ContentHash())
			}
		}
	}
	nextVersion := uint64(len(existingVersions)) + 1
	if candidate.VersionNumber() != nextVersion {
		return workflow.WorkflowVersion{}, fmt.Errorf("fake: %w: workflow definition %s expects version %d, got %d", ports.ErrOptimisticConflict, def.ID, nextVersion, candidate.VersionNumber())
	}

	if d.workflowVersions == nil {
		d.workflowVersions = map[string][]workflow.WorkflowVersion{}
	}
	d.workflowVersions[string(def.ID)] = append(existingVersions, candidate)
	return candidate, nil
}

// ListVersions mirrors sqlite's DefinitionsRepository.ListVersions,
// routed by Kind the same way CreateDefinition/PublishVersion are.
func (d *DefinitionsRepository) ListVersions(_ context.Context, kind definition.Kind, definitionID string) ([]definition.VersionFields, error) {
	if kind == definition.KindWorkflow {
		versions := d.workflowVersions[definitionID]
		result := make([]definition.VersionFields, 0, len(versions))
		for _, version := range versions {
			fields, err := fakeWorkflowVersionToDefinitionFields(version)
			if err != nil {
				return nil, err
			}
			result = append(result, fields)
		}
		return result, nil
	}
	var result []definition.VersionFields
	for _, fields := range d.versions {
		if fields.DefinitionID() == definitionID {
			result = append(result, fields)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].VersionNumber() < result[j].VersionNumber() })
	return result, nil
}

// GetWorkflowVersion mirrors sqlite's GetWorkflowVersion (V4-02): scans
// every definition's own version list for a matching VersionID, since
// workflowVersions is keyed by DefinitionID, not VersionID.
func (d *DefinitionsRepository) GetWorkflowVersion(_ context.Context, versionID string) (workflow.WorkflowVersion, error) {
	for _, versions := range d.workflowVersions {
		for _, version := range versions {
			if string(version.ID()) == versionID {
				return version, nil
			}
		}
	}
	return workflow.WorkflowVersion{}, fmt.Errorf("fake: %w: workflow version %s", ports.ErrPersistenceNotFound, versionID)
}

func sameDefinitionScope(a, b definition.Scope) bool {
	if a.IsGlobal() != b.IsGlobal() {
		return false
	}
	if a.IsGlobal() {
		return true
	}
	return *a.ProjectID == *b.ProjectID
}

// CatalogRepository is an in-memory ports.CatalogRepository — V3-01 gives
// this concern its first real behavior (Project/Repository/Component/
// ComponentPackAssignment), mirroring the same cross-project resolution
// discipline sqlite's own catalogRepository follows: a Component's
// Repository, and a ComponentPackAssignment's Component, are always
// resolved from the fake's own stored records, never trusted from the
// request.
type CatalogRepository struct {
	projects      map[string]project.Project
	repositories  map[string]project.Repository
	components    map[string]project.Component
	assignments   map[string][]project.ComponentPackAssignment // by ComponentID, oldest first
	probeAttempts map[string][]ports.RepositoryProbeAttempt    // by RepositoryID, oldest first
	probeJobIDs   map[string]bool                              // JobID -> already recorded, mirroring the sqlite adapter's UNIQUE(job_id)
}

var _ ports.CatalogRepository = (*CatalogRepository)(nil)

func (c *CatalogRepository) clone() *CatalogRepository {
	projects := make(map[string]project.Project, len(c.projects))
	for k, v := range c.projects {
		projects[k] = v
	}
	repositories := make(map[string]project.Repository, len(c.repositories))
	for k, v := range c.repositories {
		repositories[k] = v
	}
	components := make(map[string]project.Component, len(c.components))
	for k, v := range c.components {
		components[k] = v
	}
	assignments := make(map[string][]project.ComponentPackAssignment, len(c.assignments))
	for k, v := range c.assignments {
		assignments[k] = append([]project.ComponentPackAssignment(nil), v...)
	}
	probeAttempts := make(map[string][]ports.RepositoryProbeAttempt, len(c.probeAttempts))
	for k, v := range c.probeAttempts {
		probeAttempts[k] = append([]ports.RepositoryProbeAttempt(nil), v...)
	}
	probeJobIDs := make(map[string]bool, len(c.probeJobIDs))
	for k, v := range c.probeJobIDs {
		probeJobIDs[k] = v
	}
	return &CatalogRepository{
		projects: projects, repositories: repositories, components: components,
		assignments: assignments, probeAttempts: probeAttempts, probeJobIDs: probeJobIDs,
	}
}

func (c *CatalogRepository) CreateProject(_ context.Context, req ports.CreateProjectRequest) (project.Project, error) {
	created, err := project.NewProject(project.ProjectID(req.ID), req.Name)
	if err != nil {
		return project.Project{}, err
	}
	if c.projects == nil {
		c.projects = map[string]project.Project{}
	}
	c.projects[req.ID] = created
	return created, nil
}

func (c *CatalogRepository) GetProject(_ context.Context, id string) (project.Project, error) {
	p, ok := c.projects[id]
	if !ok {
		return project.Project{}, fmt.Errorf("fake: %w: project %s", ports.ErrPersistenceNotFound, id)
	}
	return p, nil
}

// ListProjects mirrors sqlite's own listProjectsTx: every Project row, ID
// order.
func (c *CatalogRepository) ListProjects(_ context.Context) ([]project.Project, error) {
	result := make([]project.Project, 0, len(c.projects))
	for _, p := range c.projects {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (c *CatalogRepository) RegisterRepository(_ context.Context, req ports.RegisterRepositoryRequest) (project.Repository, error) {
	created, err := project.NewRepository(
		project.RepositoryID(req.ID), project.ProjectID(req.ProjectID), req.Name, req.RemoteLocator, req.DefaultRef,
	)
	if err != nil {
		return project.Repository{}, err
	}
	if _, ok := c.projects[req.ProjectID]; !ok {
		return project.Repository{}, fmt.Errorf("fake: %w: project %s", ports.ErrPersistenceNotFound, req.ProjectID)
	}
	if c.repositories == nil {
		c.repositories = map[string]project.Repository{}
	}
	c.repositories[req.ID] = created
	return created, nil
}

func (c *CatalogRepository) GetRepository(_ context.Context, id string) (project.Repository, error) {
	repo, ok := c.repositories[id]
	if !ok {
		return project.Repository{}, fmt.Errorf("fake: %w: repository %s", ports.ErrPersistenceNotFound, id)
	}
	return repo, nil
}

func (c *CatalogRepository) ListProjectRepositories(_ context.Context, projectID string) ([]project.Repository, error) {
	var result []project.Repository
	for _, repo := range c.repositories {
		if string(repo.ProjectID) == projectID {
			result = append(result, repo)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// TransitionRepositoryStatus mirrors sqlite's transitionRepositoryStatusTx:
// project.CanTransitionRepositoryStatus is checked first, then the CAS
// (ExpectedStatus/ExpectedVersion must match the stored row) either
// succeeds — version incremented by exactly one, LastProbeErrorCode
// written exactly as given — or fails with ports.ErrOptimisticConflict.
func (c *CatalogRepository) TransitionRepositoryStatus(_ context.Context, req ports.TransitionRepositoryStatusRequest) (project.Repository, error) {
	if err := project.CanTransitionRepositoryStatus(req.ExpectedStatus, req.NextStatus); err != nil {
		return project.Repository{}, err
	}
	repo, ok := c.repositories[req.RepositoryID]
	if !ok {
		return project.Repository{}, fmt.Errorf("fake: %w: repository %s", ports.ErrPersistenceNotFound, req.RepositoryID)
	}
	if repo.Status != req.ExpectedStatus || repo.Version != req.ExpectedVersion {
		return project.Repository{}, fmt.Errorf("fake: %w: repository %s expected %s@%d",
			ports.ErrOptimisticConflict, req.RepositoryID, req.ExpectedStatus, req.ExpectedVersion)
	}
	repo.Status = req.NextStatus
	repo.LastProbeErrorCode = req.LastProbeErrorCode
	repo.Version++
	c.repositories[req.RepositoryID] = repo
	return repo, nil
}

// RecordRepositoryProbeAttempt mirrors sqlite's
// recordRepositoryProbeAttemptTx: req.JobID is unique across every
// attempt this fake ever records, the same "active probe idempotent"
// guarantee the sqlite adapter's UNIQUE(job_id) constraint gives.
func (c *CatalogRepository) RecordRepositoryProbeAttempt(_ context.Context, req ports.RecordRepositoryProbeAttemptRequest) (ports.RepositoryProbeAttempt, error) {
	if c.probeJobIDs[req.JobID] {
		return ports.RepositoryProbeAttempt{}, fmt.Errorf("fake: duplicate repository probe attempt job id %q", req.JobID)
	}
	attempt := ports.RepositoryProbeAttempt{
		ID: req.ID, ProjectID: req.ProjectID, RepositoryID: req.RepositoryID, JobID: req.JobID,
		State: req.State, Result: req.Result, ErrorCode: req.ErrorCode, ErrorMessage: req.ErrorMessage,
		BaseCommit: req.BaseCommit, Dirty: req.Dirty, CreatedAt: time.Now().UTC(),
	}
	if c.probeAttempts == nil {
		c.probeAttempts = map[string][]ports.RepositoryProbeAttempt{}
	}
	if c.probeJobIDs == nil {
		c.probeJobIDs = map[string]bool{}
	}
	c.probeAttempts[req.RepositoryID] = append(c.probeAttempts[req.RepositoryID], attempt)
	c.probeJobIDs[req.JobID] = true
	return attempt, nil
}

func (c *CatalogRepository) ListRepositoryProbeAttempts(_ context.Context, repositoryID string) ([]ports.RepositoryProbeAttempt, error) {
	return append([]ports.RepositoryProbeAttempt(nil), c.probeAttempts[repositoryID]...), nil
}

func (c *CatalogRepository) CreateComponent(_ context.Context, req ports.CreateComponentRequest) (project.Component, error) {
	created, err := project.NewComponent(
		project.ComponentID(req.ID), project.ProjectID(req.ProjectID), project.RepositoryID(req.RepositoryID),
		req.Name, req.Path, req.Kind,
	)
	if err != nil {
		return project.Component{}, err
	}
	repo, ok := c.repositories[req.RepositoryID]
	if !ok {
		return project.Component{}, fmt.Errorf("fake: %w: repository %s", ports.ErrPersistenceNotFound, req.RepositoryID)
	}
	if string(repo.ProjectID) != req.ProjectID {
		return project.Component{}, fmt.Errorf("fake: %w: component repository %s belongs to project %s, not %s",
			ports.ErrCrossProjectReference, req.RepositoryID, repo.ProjectID, req.ProjectID)
	}
	if c.components == nil {
		c.components = map[string]project.Component{}
	}
	c.components[req.ID] = created
	return created, nil
}

func (c *CatalogRepository) GetComponent(_ context.Context, id string) (project.Component, error) {
	component, ok := c.components[id]
	if !ok {
		return project.Component{}, fmt.Errorf("fake: %w: component %s", ports.ErrPersistenceNotFound, id)
	}
	return component, nil
}

func (c *CatalogRepository) AssignComponentPack(_ context.Context, req ports.AssignComponentPackRequest) (project.ComponentPackAssignment, error) {
	created, err := project.NewComponentPackAssignment(
		project.ComponentPackAssignmentID(req.ID), project.ProjectID(req.ProjectID), project.ComponentID(req.ComponentID),
		project.PackVersionID(req.PackVersionID), req.EffectiveAt, req.Actor,
	)
	if err != nil {
		return project.ComponentPackAssignment{}, err
	}
	component, ok := c.components[req.ComponentID]
	if !ok {
		return project.ComponentPackAssignment{}, fmt.Errorf("fake: %w: component %s", ports.ErrPersistenceNotFound, req.ComponentID)
	}
	if string(component.ProjectID) != req.ProjectID {
		return project.ComponentPackAssignment{}, fmt.Errorf("fake: %w: component %s belongs to project %s, not %s",
			ports.ErrCrossProjectReference, req.ComponentID, component.ProjectID, req.ProjectID)
	}
	if c.assignments == nil {
		c.assignments = map[string][]project.ComponentPackAssignment{}
	}
	c.assignments[req.ComponentID] = append(c.assignments[req.ComponentID], created)
	return created, nil
}

func (c *CatalogRepository) ListComponentPackAssignments(_ context.Context, componentID string) ([]project.ComponentPackAssignment, error) {
	assignments := append([]project.ComponentPackAssignment(nil), c.assignments[componentID]...)
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].EffectiveAt.Before(assignments[j].EffectiveAt) })
	return assignments, nil
}

func (c *CatalogRepository) GetEffectiveComponentPackAssignment(_ context.Context, componentID string, at time.Time) (project.ComponentPackAssignment, error) {
	var best project.ComponentPackAssignment
	found := false
	for _, assignment := range c.assignments[componentID] {
		if assignment.EffectiveAt.After(at) {
			continue
		}
		if !found || assignment.EffectiveAt.After(best.EffectiveAt) {
			best = assignment
			found = true
		}
	}
	if !found {
		return project.ComponentPackAssignment{}, fmt.Errorf("fake: %w: no component pack assignment for component %s effective at or before %s",
			ports.ErrPersistenceNotFound, componentID, at)
	}
	return best, nil
}

// JobsRepository is an in-memory ports.JobsRepository — V3-01 gives this
// concern its first real method (EnqueueJob), so
// internal/app/catalog.RegisterRepository's own atomic Repository-row +
// probe-job enqueue can be tested against the fake without sqlite. leases/
// completed are populated now (V4-05): this fake never itself implements a
// ClaimJob-equivalent lifecycle (no fake worker pool exists), so a test
// that needs ValidateActiveJob/CompleteJob to see a job as genuinely LEASED
// calls the test-only SetActiveLease helper first — deep fencing edge
// cases (expired lease, wrong owner/token, WriteLease mismatch) are
// SQLite-only per this task's own test-layering decision; this fake only
// needs to support the CAS/rollback/happy-path shapes.
type JobsRepository struct {
	// runtime is populated now (V4-12B), mirroring WorkRepository's own
	// catalog field: EnqueueJob's own "run not cancelling" fence
	// (ports.ErrRunCancelling) needs to read WorkflowRun.State for a
	// RUN_WORK job's own RunID, and this fake has no other way to reach a
	// sibling repository's data — each one is its own independent struct.
	runtime   *RuntimeRepository
	jobs      []ports.EnqueueJobRequest
	leases    map[string]ports.JobLease
	completed map[string]bool
	// cancelled is populated now (V4-12B): job ID -> this job's own
	// cancel_epoch has been set by FenceAndCancelRunJobs. This fake never
	// modeled AVAILABLE/LEASED as a real per-job state machine to begin
	// with (see this struct's own pre-existing doc comment on
	// SetActiveLease) — a boolean "has this job been fenced" is the
	// meaningful signal a fake-backed test needs; the finer AVAILABLE-vs-
	// LEASED persistence mechanics are exercised at the sqlite level.
	cancelled map[string]bool
}

var _ ports.JobsRepository = (*JobsRepository)(nil)

func (j *JobsRepository) cloneWith(runtime *RuntimeRepository) *JobsRepository {
	jobs := append([]ports.EnqueueJobRequest(nil), j.jobs...)
	leases := make(map[string]ports.JobLease, len(j.leases))
	for k, v := range j.leases {
		leases[k] = v
	}
	completed := make(map[string]bool, len(j.completed))
	for k, v := range j.completed {
		completed[k] = v
	}
	cancelled := make(map[string]bool, len(j.cancelled))
	for k, v := range j.cancelled {
		cancelled[k] = v
	}
	return &JobsRepository{runtime: runtime, jobs: jobs, leases: leases, completed: completed, cancelled: cancelled}
}

// SetActiveLease records lease as the currently active claim on jobID —
// test-only setup standing in for a real ClaimJob call, since this fake
// has no worker-pool lifecycle of its own.
func (j *JobsRepository) SetActiveLease(jobID string, lease ports.JobLease) {
	if j.leases == nil {
		j.leases = map[string]ports.JobLease{}
	}
	j.leases[jobID] = lease
}

func (j *JobsRepository) EnqueueJob(_ context.Context, req ports.EnqueueJobRequest) (ports.DurableJob, error) {
	if err := ports.ValidateJobScope(req.Kind, req.ProjectID, req.RunID); err != nil {
		return ports.DurableJob{}, err
	}
	for _, existing := range j.jobs {
		if existing.IdempotencyKey == req.IdempotencyKey {
			// Mirrors the real sqlite adapter's own identical mapping of a
			// durable_jobs.idempotency_key UNIQUE conflict to
			// ports.ErrPersistenceAlreadyExists (scheduling.go's own
			// enqueueJobTx, V3-10) — internal/app/workspacereconcile's own
			// "already has an open reconciliation" check relies on this
			// exact sentinel, and the fake must return what the real
			// adapter returns for every caller that can run against either.
			return ports.DurableJob{}, fmt.Errorf("fake: %w: durable job with idempotency key %q", ports.ErrPersistenceAlreadyExists, req.IdempotencyKey)
		}
	}
	jobClass := ports.ClassifyJobKind(req.Kind)
	runID := req.RunID
	if jobClass == ports.JobClassRunWork && runID != "" && j.runtime != nil {
		if run, ok := j.runtime.workflowRuns[runID]; ok &&
			(run.State == domainruntime.WorkflowRunCancelling || run.State == domainruntime.WorkflowRunCancelled) {
			return ports.DurableJob{}, ports.ErrRunCancelling
		}
	}
	j.jobs = append(j.jobs, req)
	return ports.DurableJob{
		ID: req.ID, ProjectID: req.ProjectID, Kind: req.Kind, AggregateType: req.AggregateType,
		AggregateID: req.AggregateID, Payload: req.Payload, State: ports.JobAvailable,
		Priority: req.Priority, MaxClaims: req.MaxClaims, IdempotencyKey: req.IdempotencyKey, Version: 1,
		RunID: runID, JobClass: jobClass,
	}, nil
}

// FenceAndCancelRunJobs implements ports.JobsRepository (V4-12B): marks
// every non-completed RUN_WORK job of runID as fenced — see IsCancelled's
// own doc comment for what "fenced" means in this fake.
func (j *JobsRepository) FenceAndCancelRunJobs(_ context.Context, runID string) (int64, error) {
	if j.cancelled == nil {
		j.cancelled = map[string]bool{}
	}
	var affected int64
	for _, job := range j.jobs {
		id := string(job.ID)
		if job.RunID != runID || ports.ClassifyJobKind(job.Kind) != ports.JobClassRunWork {
			continue
		}
		if j.completed[id] || j.cancelled[id] {
			continue
		}
		j.cancelled[id] = true
		affected++
	}
	return affected, nil
}

// IsCancelled reports whether jobID has been fenced by
// FenceAndCancelRunJobs (V4-12B) — the fake's own stand-in for a real
// durable_jobs.cancel_epoch being set (and, for a job that was still
// AVAILABLE, its state flipping straight to CANCELLED).
func (j *JobsRepository) IsCancelled(jobID string) bool {
	return j.cancelled[jobID]
}

// Items returns a copy of every job enqueued so far, in enqueue order —
// for test assertions (mirroring EventsRepository.Items's own shape).
func (j *JobsRepository) Items() []ports.EnqueueJobRequest {
	return append([]ports.EnqueueJobRequest(nil), j.jobs...)
}

// ValidateActiveJob mirrors sqlite's jobsRepository.ValidateActiveJob
// (V4-05): the job named by lease.JobID must exist with the given
// (aggregateType, aggregateID), have an active lease matching
// lease.Owner/lease.Token (set via SetActiveLease), and not already be
// completed.
func (j *JobsRepository) ValidateActiveJob(_ context.Context, lease ports.JobLease, aggregateType, aggregateID string) error {
	var found *ports.EnqueueJobRequest
	for i := range j.jobs {
		if string(j.jobs[i].ID) == string(lease.JobID) {
			found = &j.jobs[i]
			break
		}
	}
	if found == nil || found.AggregateType != aggregateType || found.AggregateID != aggregateID {
		return fmt.Errorf("fake: %w: job %s", ports.ErrJobLeaseLost, lease.JobID)
	}
	if j.completed[string(lease.JobID)] {
		return fmt.Errorf("fake: %w: job %s already completed", ports.ErrJobLeaseLost, lease.JobID)
	}
	active, ok := j.leases[string(lease.JobID)]
	if !ok || active.Owner != lease.Owner || active.Token != lease.Token {
		return fmt.Errorf("fake: %w: job %s has no matching active lease", ports.ErrJobLeaseLost, lease.JobID)
	}
	return nil
}

// CompleteJob mirrors sqlite's jobsRepository.CompleteJob (V4-05): same
// fencing check as ValidateActiveJob (job's own AggregateType/AggregateID
// is not re-checked here, matching the real adapter's own CompleteJob,
// which fences purely on JobID/owner/token), then marks the job completed.
func (j *JobsRepository) CompleteJob(_ context.Context, lease ports.JobLease) error {
	if j.completed[string(lease.JobID)] {
		return fmt.Errorf("fake: %w: job %s already completed", ports.ErrJobLeaseLost, lease.JobID)
	}
	active, ok := j.leases[string(lease.JobID)]
	if !ok || active.Owner != lease.Owner || active.Token != lease.Token {
		return fmt.Errorf("fake: %w: job %s has no matching active lease", ports.ErrJobLeaseLost, lease.JobID)
	}
	if j.completed == nil {
		j.completed = map[string]bool{}
	}
	j.completed[string(lease.JobID)] = true
	return nil
}

// HasActiveJobForAggregateIDs mirrors sqlite's
// hasActiveJobForAggregateIDsTx (V3-11), with one deliberate
// simplification: this fake's own EnqueueJob never models a job actually
// completing (no ClaimJob/CompleteJob exists here — see JobsRepository's
// own doc comment), so every job this fake has ever enqueued is, as far as
// it is concerned, still active. That is exactly the state a
// RequestWorkspaceSetRelease unit test needs to exercise "an existing open
// job for one of this WorkspaceSet's own repository workspaces blocks a
// second release request" without sqlite.
func (j *JobsRepository) HasActiveJobForAggregateIDs(_ context.Context, aggregateIDs []string) (bool, error) {
	wanted := make(map[string]bool, len(aggregateIDs))
	for _, id := range aggregateIDs {
		wanted[id] = true
	}
	for _, job := range j.jobs {
		if wanted[job.AggregateID] {
			return true, nil
		}
	}
	return false, nil
}

// fakeWorkflowVersionToDefinitionFields mirrors sqlite's own
// workflowVersionToDefinitionFields (internal/adapters/sqlite/definitions.go)
// — duplicated rather than shared, since fake must never import the
// sqlite adapter package (V1-05's own "app service có thể test không
// SQLite" boundary).
func fakeWorkflowVersionToDefinitionFields(v workflow.WorkflowVersion) (definition.VersionFields, error) {
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

// AdapterBuildRepository is an in-memory ports.AdapterBuildRepository —
// V2-07A gives this concern real behavior from the start (unlike
// Catalog/Work/Definitions/Runtime/Jobs above), so an application-layer
// test of ProbeAdapterBuild/RegisterAdapterBuild never needs sqlite.
type AdapterBuildRepository struct {
	signingKey []byte
	builds     map[string]adapterbuild.Build
}

var _ ports.AdapterBuildRepository = (*AdapterBuildRepository)(nil)

func (a *AdapterBuildRepository) clone() *AdapterBuildRepository {
	builds := make(map[string]adapterbuild.Build, len(a.builds))
	for k, v := range a.builds {
		builds[k] = v
	}
	key := append([]byte(nil), a.signingKey...)
	return &AdapterBuildRepository{signingKey: key, builds: builds}
}

func (a *AdapterBuildRepository) LoadOrCreateSigningKey(context.Context) ([]byte, error) {
	if len(a.signingKey) == 0 {
		a.signingKey = randomKey()
	}
	return a.signingKey, nil
}

func (a *AdapterBuildRepository) LoadSigningKey(context.Context) ([]byte, error) {
	if len(a.signingKey) == 0 {
		return nil, ports.ErrNoSigningKey
	}
	return a.signingKey, nil
}

func (a *AdapterBuildRepository) RotateSigningKey(context.Context) ([]byte, error) {
	a.signingKey = randomKey()
	return a.signingKey, nil
}

func (a *AdapterBuildRepository) InsertIfAbsent(_ context.Context, build adapterbuild.Build) (adapterbuild.Build, bool, error) {
	if existing, ok := a.builds[build.ID()]; ok {
		return existing, true, nil
	}
	if a.builds == nil {
		a.builds = map[string]adapterbuild.Build{}
	}
	a.builds[build.ID()] = build
	return build, false, nil
}

func (a *AdapterBuildRepository) Get(_ context.Context, id string) (adapterbuild.Build, error) {
	build, ok := a.builds[id]
	if !ok {
		return adapterbuild.Build{}, ports.ErrAdapterBuildNotFound
	}
	return build, nil
}

func (a *AdapterBuildRepository) List(context.Context) ([]adapterbuild.Build, error) {
	builds := make([]adapterbuild.Build, 0, len(a.builds))
	for _, build := range a.builds {
		builds = append(builds, build)
	}
	sort.Slice(builds, func(i, j int) bool { return builds[i].RegisteredAt().Before(builds[j].RegisteredAt()) })
	return builds, nil
}

func randomKey() []byte {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return key
}

// ArtifactRepository is an in-memory ports.ArtifactRepository — V5-01
// gives this concern real behavior from the start (the same treatment
// AdapterBuildRepository/ReadinessRepository/WaitRepository/
// ApprovalRepository above already received), so an application-layer
// test of the attach flow never needs sqlite.
type ArtifactRepository struct {
	// catalog is populated now (V5-01), mirroring WorkRepository's own
	// catalog field: InsertArtifact's own "ProjectID must name a Project
	// that exists" check needs to read CatalogRepository's own project
	// records, and this fake has no other way to reach a sibling
	// repository's data.
	catalog   *CatalogRepository
	artifacts map[string]artifact.Artifact
	// locatorClaims mirrors artifact_locator_purge_claims (V5-14): keyed by
	// Locator, not by any one Artifact row's ID — see
	// ClaimArtifactLocatorForPurge's own doc comment.
	locatorClaims map[string]artifactLocatorClaim
	// sweepState mirrors RuntimeRepository's own reaperState field —
	// lazily seeded on first read, this fake having no migration mechanism
	// of its own to run migration 35's own seed through.
	sweepState *ports.ArtifactSweepState
}

// artifactLocatorClaim mirrors one artifact_locator_purge_claims row.
type artifactLocatorClaim struct {
	owner     string
	claimedAt time.Time
}

var _ ports.ArtifactRepository = (*ArtifactRepository)(nil)

func (a *ArtifactRepository) cloneWith(catalog *CatalogRepository) *ArtifactRepository {
	artifacts := make(map[string]artifact.Artifact, len(a.artifacts))
	for k, v := range a.artifacts {
		artifacts[k] = v
	}
	claims := make(map[string]artifactLocatorClaim, len(a.locatorClaims))
	for k, v := range a.locatorClaims {
		claims[k] = v
	}
	var sweepState *ports.ArtifactSweepState
	if a.sweepState != nil {
		copied := *a.sweepState
		sweepState = &copied
	}
	return &ArtifactRepository{catalog: catalog, artifacts: artifacts, locatorClaims: claims, sweepState: sweepState}
}

// InsertArtifact mirrors sqlite's insertArtifactTx: rec.ProjectID must name
// a Project this fake's own CatalogRepository already has, rec.Locator must
// not currently have an open purge claim (V5-14), and a duplicate ID
// returns the already-stored row rather than erroring.
func (a *ArtifactRepository) InsertArtifact(_ context.Context, rec artifact.Artifact) (artifact.Artifact, error) {
	if _, ok := a.catalog.projects[string(rec.ProjectID)]; !ok {
		return artifact.Artifact{}, fmt.Errorf("fake: %w: project %s", ports.ErrPersistenceNotFound, rec.ProjectID)
	}
	if existing, ok := a.artifacts[string(rec.ID)]; ok {
		return existing, nil
	}
	if _, claimed := a.locatorClaims[rec.Locator]; claimed {
		return artifact.Artifact{}, fmt.Errorf("fake: %w: locator %s is claimed for purge", ports.ErrPersistenceAlreadyExists, rec.Locator)
	}
	if a.artifacts == nil {
		a.artifacts = map[string]artifact.Artifact{}
	}
	a.artifacts[string(rec.ID)] = rec
	return rec, nil
}

func (a *ArtifactRepository) GetArtifact(_ context.Context, id string) (artifact.Artifact, error) {
	rec, ok := a.artifacts[id]
	if !ok {
		return artifact.Artifact{}, fmt.Errorf("fake: %w: artifact %s", ports.ErrPersistenceNotFound, id)
	}
	return rec, nil
}

// TransitionArtifactAttachState mirrors sqlite's own fenced CAS.
func (a *ArtifactRepository) TransitionArtifactAttachState(_ context.Context, req ports.TransitionArtifactAttachStateRequest) (artifact.Artifact, error) {
	rec, ok := a.artifacts[req.ArtifactID]
	if !ok {
		return artifact.Artifact{}, fmt.Errorf("fake: %w: artifact %s", ports.ErrPersistenceNotFound, req.ArtifactID)
	}
	if rec.AttachState != req.ExpectedState || rec.Version != req.ExpectedVersion {
		return artifact.Artifact{}, fmt.Errorf("fake: %w: artifact %s expected %s@%d",
			ports.ErrOptimisticConflict, req.ArtifactID, req.ExpectedState, req.ExpectedVersion)
	}
	rec.AttachState = req.NextState
	rec.Version++
	a.artifacts[req.ArtifactID] = rec
	return rec, nil
}

// SetArtifactHold mirrors sqlite's own fenced CAS.
func (a *ArtifactRepository) SetArtifactHold(_ context.Context, req ports.SetArtifactHoldRequest) (artifact.Artifact, error) {
	rec, ok := a.artifacts[req.ArtifactID]
	if !ok {
		return artifact.Artifact{}, fmt.Errorf("fake: %w: artifact %s", ports.ErrPersistenceNotFound, req.ArtifactID)
	}
	if rec.Version != req.ExpectedVersion {
		return artifact.Artifact{}, fmt.Errorf("fake: %w: artifact %s expected version %d",
			ports.ErrOptimisticConflict, req.ArtifactID, req.ExpectedVersion)
	}
	rec.Hold = req.Hold
	rec.Version++
	a.artifacts[req.ArtifactID] = rec
	return rec, nil
}

// ListOrphanedArtifacts mirrors sqlite's own query, ordered by
// (CreatedAt, ID) for a deterministic result either implementation gives.
func (a *ArtifactRepository) ListOrphanedArtifacts(_ context.Context, olderThan time.Time) ([]artifact.Artifact, error) {
	var result []artifact.Artifact
	for _, rec := range a.artifacts {
		if rec.AttachState != artifact.Orphan {
			continue
		}
		if rec.CreatedAt.After(olderThan) {
			continue
		}
		result = append(result, rec)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

// ListArtifactsByLocator mirrors sqlite's own query, ordered by ID for a
// deterministic result either implementation gives.
func (a *ArtifactRepository) ListArtifactsByLocator(_ context.Context, locator string) ([]artifact.Artifact, error) {
	var result []artifact.Artifact
	for _, rec := range a.artifacts {
		if rec.Locator != locator {
			continue
		}
		result = append(result, rec)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// ClaimArtifactLocatorForPurge mirrors sqlite's own claim insert: a second
// claim against an already-claimed Locator is ErrPersistenceAlreadyExists.
func (a *ArtifactRepository) ClaimArtifactLocatorForPurge(_ context.Context, locator, claimOwner string, claimedAt time.Time) error {
	if _, claimed := a.locatorClaims[locator]; claimed {
		return fmt.Errorf("fake: %w: artifact locator %s", ports.ErrPersistenceAlreadyExists, locator)
	}
	if a.locatorClaims == nil {
		a.locatorClaims = map[string]artifactLocatorClaim{}
	}
	a.locatorClaims[locator] = artifactLocatorClaim{owner: claimOwner, claimedAt: claimedAt}
	return nil
}

// ReleaseArtifactLocatorClaim mirrors sqlite's own idempotent delete.
func (a *ArtifactRepository) ReleaseArtifactLocatorClaim(_ context.Context, locator string) error {
	delete(a.locatorClaims, locator)
	return nil
}

// GetArtifactSweepState mirrors sqlite's GetArtifactSweepState: lazily
// seeds {Generation: 0, DryRun: true, Version: 1} on first read, mirroring
// migration 35's own seeded singleton row.
func (a *ArtifactRepository) GetArtifactSweepState(_ context.Context) (ports.ArtifactSweepState, error) {
	if a.sweepState == nil {
		a.sweepState = &ports.ArtifactSweepState{Generation: 0, DryRun: true, Version: 1}
	}
	return *a.sweepState, nil
}

// AdvanceArtifactSweepGeneration mirrors sqlite's identical method.
func (a *ArtifactRepository) AdvanceArtifactSweepGeneration(_ context.Context, req ports.AdvanceArtifactSweepGenerationRequest) (ports.ArtifactSweepState, error) {
	if a.sweepState == nil {
		a.sweepState = &ports.ArtifactSweepState{Generation: 0, DryRun: true, Version: 1}
	}
	if a.sweepState.Generation != req.ExpectedGeneration || a.sweepState.Version != req.ExpectedVersion {
		return ports.ArtifactSweepState{}, fmt.Errorf(
			"fake: %w: artifact sweep state expected generation=%d version=%d",
			ports.ErrOptimisticConflict, req.ExpectedGeneration, req.ExpectedVersion,
		)
	}
	a.sweepState.Generation++
	a.sweepState.Version++
	return *a.sweepState, nil
}

// SetArtifactSweepDryRun mirrors sqlite's identical method.
func (a *ArtifactRepository) SetArtifactSweepDryRun(_ context.Context, req ports.SetArtifactSweepDryRunRequest) (ports.ArtifactSweepState, error) {
	if a.sweepState == nil {
		a.sweepState = &ports.ArtifactSweepState{Generation: 0, DryRun: true, Version: 1}
	}
	if a.sweepState.Version != req.ExpectedVersion {
		return ports.ArtifactSweepState{}, fmt.Errorf(
			"fake: %w: artifact sweep state expected version=%d", ports.ErrOptimisticConflict, req.ExpectedVersion,
		)
	}
	a.sweepState.DryRun = req.DryRun
	a.sweepState.Version++
	return *a.sweepState, nil
}

// SafeSettingsRepository is an in-memory ports.SafeSettingsRepository
// (V6-10G) — the same "gets real behavior from the start" treatment
// AdapterBuildRepository/ArtifactRepository above already received: a
// single versioned record, CAS-updated by full-document replacement,
// mirroring sqlite's own safeSettingsRepository.Update exactly (RowsAffected
// == 1 there is "expected version matched" here).
type SafeSettingsRepository struct {
	record ports.SafeSettingsRecord
}

var _ ports.SafeSettingsRepository = (*SafeSettingsRepository)(nil)

func (s *SafeSettingsRepository) clone() *SafeSettingsRepository {
	return &SafeSettingsRepository{record: s.record}
}

func (s *SafeSettingsRepository) Get(context.Context) (ports.SafeSettingsRecord, error) {
	return s.record, nil
}

func (s *SafeSettingsRepository) Update(_ context.Context, req ports.UpdateSafeSettingsRequest) (ports.SafeSettingsRecord, error) {
	if s.record.Version != req.ExpectedVersion {
		return ports.SafeSettingsRecord{}, fmt.Errorf(
			"fake: %w: safe settings expected version=%d", ports.ErrOptimisticConflict, req.ExpectedVersion,
		)
	}
	s.record = ports.SafeSettingsRecord{
		Desired: req.Desired, Version: s.record.Version + 1, UpdatedAt: req.OccurredAt, UpdatedBy: req.UpdatedBy,
	}
	return s.record, nil
}

// QueryStore is an in-memory ports.QueryStore that is always reachable.
type QueryStore struct {
	// Unreachable, when true, makes Ping fail — for a handler test that
	// needs to exercise the "query store is down" path without a real
	// database to actually take offline.
	Unreachable bool
}

var _ ports.QueryStore = (*QueryStore)(nil)

func (q *QueryStore) Ping(context.Context) error {
	if q.Unreachable {
		return errors.New("fake: query store unreachable")
	}
	return nil
}
