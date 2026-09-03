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
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
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
	catalog       *CatalogRepository
	work          *WorkRepository
	definitions   *DefinitionsRepository
	runtime       RuntimeRepository
	jobs          *JobsRepository
	events        *EventsRepository
	receipts      *ReceiptsRepository
	adapterBuilds *AdapterBuildRepository
	readiness     *ReadinessRepository
}

func newTx() Tx {
	catalog := &CatalogRepository{}
	return Tx{
		events:        &EventsRepository{},
		receipts:      &ReceiptsRepository{},
		adapterBuilds: &AdapterBuildRepository{},
		definitions:   &DefinitionsRepository{},
		catalog:       catalog,
		work:          &WorkRepository{catalog: catalog},
		jobs:          &JobsRepository{},
		readiness:     &ReadinessRepository{catalog: catalog},
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
	clone.jobs = t.jobs.clone()
	clone.readiness = t.readiness.cloneWith(clone.catalog)
	return clone
}

var _ ports.Tx = Tx{}

func (t Tx) Catalog() ports.CatalogRepository            { return t.catalog }
func (t Tx) Work() ports.WorkRepository                  { return t.work }
func (t Tx) Definitions() ports.DefinitionsRepository    { return t.definitions }
func (t Tx) Runtime() ports.RuntimeRepository            { return t.runtime }
func (t Tx) Jobs() ports.JobsRepository                  { return t.jobs }
func (t Tx) Events() ports.EventsRepository              { return t.events }
func (t Tx) Receipts() ports.ReceiptsRepository          { return t.receipts }
func (t Tx) AdapterBuilds() ports.AdapterBuildRepository { return t.adapterBuilds }
func (t Tx) Readiness() ports.ReadinessRepository        { return t.readiness }

type RuntimeRepository struct{}

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
// probe-job enqueue can be tested against the fake without sqlite.
type JobsRepository struct {
	jobs []ports.EnqueueJobRequest
}

var _ ports.JobsRepository = (*JobsRepository)(nil)

func (j *JobsRepository) clone() *JobsRepository {
	jobs := append([]ports.EnqueueJobRequest(nil), j.jobs...)
	return &JobsRepository{jobs: jobs}
}

func (j *JobsRepository) EnqueueJob(_ context.Context, req ports.EnqueueJobRequest) (ports.DurableJob, error) {
	for _, existing := range j.jobs {
		if existing.IdempotencyKey == req.IdempotencyKey {
			return ports.DurableJob{}, fmt.Errorf("fake: duplicate durable job idempotency key %q", req.IdempotencyKey)
		}
	}
	j.jobs = append(j.jobs, req)
	return ports.DurableJob{
		ID: req.ID, ProjectID: req.ProjectID, Kind: req.Kind, AggregateType: req.AggregateType,
		AggregateID: req.AggregateID, Payload: req.Payload, State: ports.JobAvailable,
		Priority: req.Priority, MaxClaims: req.MaxClaims, IdempotencyKey: req.IdempotencyKey, Version: 1,
	}, nil
}

// Items returns a copy of every job enqueued so far, in enqueue order —
// for test assertions (mirroring EventsRepository.Items's own shape).
func (j *JobsRepository) Items() []ports.EnqueueJobRequest {
	return append([]ports.EnqueueJobRequest(nil), j.jobs...)
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
