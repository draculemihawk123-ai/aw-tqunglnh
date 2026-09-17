package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// CreateSharedDefinition inserts a new row into the shared definitions
// table (V2-02) for one of the 8 kinds that table serves — never
// KindWorkflow, which keeps using workflow_definitions
// (docs/design/04-v2-definition-plane.md V2-02's own scoping decision:
// no dual-write, no compatibility migration between the two layouts in
// this task). It starts the Definition at StatusDraft/generation 1, the
// same rule definition.Create already expresses — this is that rule's
// persistence counterpart.
func (s *Store) CreateSharedDefinition(ctx context.Context, id string, kind definition.Kind, scope definition.Scope, name string, now time.Time) error {
	// Wrapped in RunSerializedWrite (rather than a bare s.db.ExecContext,
	// as before V2-10) so this insert is genuinely Tx-composable — GC-INV-15
	// requires a state transition and its domain event to commit in the
	// same transaction, which createSharedDefinitionTx's new Tx-scoped
	// sibling (definitionsRepository.CreateDefinition) depends on being
	// able to share with whatever event write a command handler performs
	// alongside it. Observable behavior for this exported method itself is
	// unchanged: one insert, still committed or rolled back as a whole.
	return s.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return createSharedDefinitionTx(ctx, tx, id, kind, scope, name, now)
	})
}

// createSharedDefinitionTx is CreateSharedDefinition's own core, scoped to
// an already-open *sql.Tx it neither begins nor commits/rolls back — see
// publishWorkflowVersionTx's doc comment in workflow_store.go for why this
// shape is what makes a repository method genuinely Tx-composable.
func createSharedDefinitionTx(ctx context.Context, tx *sql.Tx, id string, kind definition.Kind, scope definition.Scope, name string, now time.Time) error {
	if kind == definition.KindWorkflow {
		return errors.New("sqlite: KindWorkflow does not use the shared definitions table — use PublishWorkflowVersion's own definition path")
	}
	fields, err := definition.Create(definition.CreateRequest{Kind: kind, Scope: scope, Name: name})
	if err != nil {
		return err
	}
	var projectID any
	if !scope.IsGlobal() {
		projectID = string(*scope.ProjectID)
	}
	timestamp := now.UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `
INSERT INTO definitions (id, kind, project_id, name, status, version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, string(fields.Kind), projectID, fields.Name, string(fields.Status), fields.Version,
		timestamp, timestamp,
	)
	if err != nil {
		return MapSQLiteError(fmt.Errorf("create shared definition: %w", err))
	}
	return nil
}

// LoadSharedDefinitionVersion loads one previously published Version by
// ID from the shared definition_versions table.
func (s *Store) LoadSharedDefinitionVersion(ctx context.Context, versionID string) (definition.VersionFields, error) {
	var fields definition.VersionFields
	err := s.RunReadOnly(ctx, func(tx *sql.Tx) error {
		loaded, err := loadSharedDefinitionVersion(ctx, tx, versionID)
		fields = loaded
		return err
	})
	return fields, err
}

// PublishDefinitionVersion implements ports.DefinitionPublisher: it
// routes by Kind to whichever storage that Kind actually uses, so
// application code never has to know there are two layouts.
func (s *Store) PublishDefinitionVersion(ctx context.Context, req ports.PublishVersionRequest) (definition.VersionFields, error) {
	if req.Kind == definition.KindWorkflow {
		return s.publishWorkflowDefinitionVersion(ctx, req)
	}
	return s.publishSharedDefinitionVersion(ctx, req)
}

var _ ports.DefinitionPublisher = (*Store)(nil)

func (s *Store) publishSharedDefinitionVersion(ctx context.Context, req ports.PublishVersionRequest) (definition.VersionFields, error) {
	var result definition.VersionFields
	err := s.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = publishSharedDefinitionVersionTx(ctx, tx, req)
		return err
	})
	return result, err
}

// publishSharedDefinitionVersionTx is publishSharedDefinitionVersion's own
// core, scoped to an already-open *sql.Tx it neither begins nor commits —
// see publishWorkflowVersionTx's doc comment in workflow_store.go for why
// this shape is what makes a repository method genuinely Tx-composable
// (definitionsRepository.PublishVersion, V2-10's own entry point, calls
// this directly against a Tx a command handler already opened).
func publishSharedDefinitionVersionTx(ctx context.Context, tx *sql.Tx, req ports.PublishVersionRequest) (definition.VersionFields, error) {
	if err := validatePublishVersionRequest(req); err != nil {
		return definition.VersionFields{}, err
	}

	var status string
	var projectID sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT status, project_id FROM definitions WHERE id = ? AND kind = ?`,
		req.DefinitionID, string(req.Kind),
	).Scan(&status, &projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return definition.VersionFields{}, fmt.Errorf("%w: definition %s (kind %s)", ports.ErrPersistenceNotFound, req.DefinitionID, req.Kind)
	}
	if err != nil {
		return definition.VersionFields{}, MapSQLiteError(fmt.Errorf("load definition for publish: %w", err))
	}
	if err := definition.CanPublish(definition.Status(status)); err != nil {
		return definition.VersionFields{}, err
	}

	// Cross-project dependency validation: each pin's actual project
	// is resolved from the repository itself, never trusted from the
	// request (ports.ErrCrossProjectDependency's own doc comment).
	for _, pin := range req.Dependencies.Pins {
		var pinProjectID sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT project_id FROM definitions WHERE id = ?`, pin.DefinitionID).Scan(&pinProjectID)
		if errors.Is(err, sql.ErrNoRows) {
			return definition.VersionFields{}, fmt.Errorf("%w: dependency %s not found", ports.ErrPersistenceNotFound, pin.DefinitionID)
		}
		if err != nil {
			return definition.VersionFields{}, MapSQLiteError(fmt.Errorf("resolve dependency project: %w", err))
		}
		if pinProjectID.Valid != projectID.Valid || pinProjectID.String != projectID.String {
			return definition.VersionFields{}, fmt.Errorf("%w: dependency %s", ports.ErrCrossProjectDependency, pin.DefinitionID)
		}
	}

	// Idempotent republish: identical compiled content for this
	// definition already exists — return it rather than insert a
	// duplicate row.
	var existingID string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM definition_versions WHERE definition_id = ? AND compiled_hash = ?`,
		req.DefinitionID, req.CompiledHash,
	).Scan(&existingID)
	if err == nil {
		return loadSharedDefinitionVersion(ctx, tx, existingID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return definition.VersionFields{}, MapSQLiteError(fmt.Errorf("find existing definition version: %w", err))
	}

	var nextVersionNo uint64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_no), 0) + 1 FROM definition_versions WHERE definition_id = ?`,
		req.DefinitionID,
	).Scan(&nextVersionNo); err != nil {
		return definition.VersionFields{}, MapSQLiteError(fmt.Errorf("allocate definition version number: %w", err))
	}

	dependenciesJSON, err := json.Marshal(req.Dependencies)
	if err != nil {
		return definition.VersionFields{}, fmt.Errorf("marshal dependency manifest: %w", err)
	}
	publishedAt := req.PublishedAt.UTC().Format(time.RFC3339Nano)

	if _, err := tx.ExecContext(ctx, `
INSERT INTO definition_versions (
    id, definition_id, version_no, schema_version, canonical_source, source_hash,
    compiled_snapshot, compiled_hash, dependency_manifest, published_by, published_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.VersionID, req.DefinitionID, nextVersionNo, req.SchemaVersion,
		req.CanonicalSource, req.SourceHash, req.CompiledSnapshot, req.CompiledHash,
		string(dependenciesJSON), req.PublishedBy, publishedAt,
	); err != nil {
		return definition.VersionFields{}, MapSQLiteError(fmt.Errorf("insert definition version: %w", err))
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE definitions SET version = version + 1, updated_at = ? WHERE id = ?`,
		publishedAt, req.DefinitionID,
	); err != nil {
		return definition.VersionFields{}, MapSQLiteError(fmt.Errorf("bump definition generation: %w", err))
	}

	return definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID: req.VersionID, DefinitionID: req.DefinitionID, Kind: req.Kind,
		VersionNumber: nextVersionNo, SchemaVersion: req.SchemaVersion,
		CanonicalSource: req.CanonicalSource, SourceHash: req.SourceHash,
		CompiledSnapshot: req.CompiledSnapshot, CompiledHash: req.CompiledHash,
		Dependencies: req.Dependencies, PublishedBy: req.PublishedBy, PublishedAt: req.PublishedAt,
	})
}

// listSharedDefinitionVersionsTx returns every published Version for
// definitionID from the shared definition_versions table, oldest first —
// the list counterpart loadSharedDefinitionVersion (one version by ID)
// already has for a single lookup.
func listSharedDefinitionVersionsTx(ctx context.Context, tx *sql.Tx, definitionID string) ([]definition.VersionFields, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT dv.id, dv.definition_id, d.kind, dv.version_no, dv.schema_version, dv.canonical_source, dv.source_hash,
       dv.compiled_snapshot, dv.compiled_hash, dv.dependency_manifest, dv.published_by, dv.published_at
FROM definition_versions dv
JOIN definitions d ON d.id = dv.definition_id
WHERE dv.definition_id = ?
ORDER BY dv.version_no`, definitionID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list definition versions: %w", err))
	}
	defer rows.Close()

	var result []definition.VersionFields
	for rows.Next() {
		fields, err := scanSharedDefinitionVersionRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, fields)
	}
	if err := rows.Err(); err != nil {
		return nil, MapSQLiteError(fmt.Errorf("iterate definition versions: %w", err))
	}
	return result, nil
}

func loadSharedDefinitionVersion(ctx context.Context, tx *sql.Tx, versionID string) (definition.VersionFields, error) {
	row := tx.QueryRowContext(ctx, `
SELECT dv.id, dv.definition_id, d.kind, dv.version_no, dv.schema_version, dv.canonical_source, dv.source_hash,
       dv.compiled_snapshot, dv.compiled_hash, dv.dependency_manifest, dv.published_by, dv.published_at
FROM definition_versions dv
JOIN definitions d ON d.id = dv.definition_id
WHERE dv.id = ?`, versionID)
	fields, err := scanSharedDefinitionVersionRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return definition.VersionFields{}, ports.ErrDefinitionVersionNotFound
	}
	return fields, err
}

// definitionVersionRowScanner is satisfied by both *sql.Row
// (loadSharedDefinitionVersion's single-row lookup) and *sql.Rows
// (listSharedDefinitionVersionsTx's multi-row listing), so
// scanSharedDefinitionVersionRow's column-decoding logic exists exactly
// once regardless of which query produced the row.
type definitionVersionRowScanner interface {
	Scan(dest ...any) error
}

func scanSharedDefinitionVersionRow(row definitionVersionRowScanner) (definition.VersionFields, error) {
	var id, definitionID, kind, canonicalSource, sourceHash, compiledSnapshot, compiledHash string
	var dependenciesJSON, publishedBy, publishedAtText string
	var versionNo uint64
	var schemaVersion int
	err := row.Scan(&id, &definitionID, &kind, &versionNo, &schemaVersion, &canonicalSource, &sourceHash,
		&compiledSnapshot, &compiledHash, &dependenciesJSON, &publishedBy, &publishedAtText)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return definition.VersionFields{}, err
		}
		return definition.VersionFields{}, MapSQLiteError(fmt.Errorf("load definition version: %w", err))
	}
	var dependencies definition.DependencyManifest
	if err := json.Unmarshal([]byte(dependenciesJSON), &dependencies); err != nil {
		return definition.VersionFields{}, fmt.Errorf("decode dependency manifest: %w", err)
	}
	publishedAt, err := parseDBTime(publishedAtText)
	if err != nil {
		return definition.VersionFields{}, err
	}
	return definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID: id, DefinitionID: definitionID, Kind: definition.Kind(kind),
		VersionNumber: versionNo, SchemaVersion: schemaVersion,
		CanonicalSource: canonicalSource, SourceHash: sourceHash,
		CompiledSnapshot: compiledSnapshot, CompiledHash: compiledHash,
		Dependencies: dependencies, PublishedBy: publishedBy, PublishedAt: publishedAt,
	})
}

// LoadVersion implements ports.DefinitionsRepository (V2-09): the
// Tx-composable equivalent of Store.LoadSharedDefinitionVersion above,
// for a caller (the graph/dependency compiler) resolving a node-level
// dependency pin from inside an already-open UnitOfWork call rather than
// opening its own separate read transaction per pin.
func (r definitionsRepository) LoadVersion(ctx context.Context, versionID string) (definition.VersionFields, error) {
	return loadSharedDefinitionVersion(ctx, r.tx, versionID)
}

// CreateDefinition implements ports.DefinitionsRepository (V2-10): the
// Tx-composable equivalent of Store.CreateSharedDefinition, routed by Kind
// the same way PublishVersion below is — a command handler never has to
// know a Workflow Definition lives in a different table than the other
// eight kinds. For KindWorkflow this upserts the workflow_definitions row
// via the same ensureWorkflowDefinition helper PublishWorkflowVersion
// already relies on (workflow_store.go): a fresh workflow_definitions row
// starts DRAFT at generation 1, exactly definition.Create's own rule for
// every other kind, and a second CreateDefinition call for the same id is
// a safe no-op as long as the identity it supplies still matches.
func (r definitionsRepository) CreateDefinition(ctx context.Context, id string, kind definition.Kind, scope definition.Scope, name string, now time.Time) error {
	if kind == definition.KindWorkflow {
		wfDefinition := workflow.WorkflowDefinition{
			ID: workflow.WorkflowDefinitionID(id), Name: name,
			Status: workflow.DefinitionStatus(definition.StatusDraft), Version: 1,
		}
		if !scope.IsGlobal() {
			pid := *scope.ProjectID
			wfDefinition.ProjectID = &pid
		}
		return ensureWorkflowDefinition(ctx, r.tx, wfDefinition, now)
	}
	return createSharedDefinitionTx(ctx, r.tx, id, kind, scope, name, now)
}

// GetDefinition implements ports.DefinitionsRepository (V6-05): reads one
// Definition row back — the read half CreateDefinition's own writer half
// never had until now (see that interface method's own doc comment for
// why). Routed by kind exactly like CreateDefinition/PublishVersion/
// ListVersions: KindWorkflow reads workflow_definitions, every other kind
// reads the shared definitions table filtered by (id, kind) so a row
// stored under a different kind than asked for reads as
// ports.ErrPersistenceNotFound, never a distinct "wrong kind" error.
func (r definitionsRepository) GetDefinition(ctx context.Context, kind definition.Kind, id string) (definition.Fields, error) {
	if kind == definition.KindWorkflow {
		var name, status string
		var projectID sql.NullString
		var version uint64
		err := r.tx.QueryRowContext(ctx,
			`SELECT name, status, project_id, version FROM workflow_definitions WHERE id = ?`, id,
		).Scan(&name, &status, &projectID, &version)
		if errors.Is(err, sql.ErrNoRows) {
			return definition.Fields{}, fmt.Errorf("%w: workflow definition %s", ports.ErrPersistenceNotFound, id)
		}
		if err != nil {
			return definition.Fields{}, MapSQLiteError(fmt.Errorf("load workflow definition: %w", err))
		}
		return definition.Fields{
			Kind: definition.KindWorkflow, Scope: scopeFromNullableProjectID(projectID),
			Name: name, Status: definition.Status(status), Version: version,
		}, nil
	}

	var name, status string
	var projectID sql.NullString
	var version uint64
	err := r.tx.QueryRowContext(ctx,
		`SELECT project_id, name, status, version FROM definitions WHERE id = ? AND kind = ?`, id, string(kind),
	).Scan(&projectID, &name, &status, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return definition.Fields{}, fmt.Errorf("%w: definition %s (kind %s)", ports.ErrPersistenceNotFound, id, kind)
	}
	if err != nil {
		return definition.Fields{}, MapSQLiteError(fmt.Errorf("load definition: %w", err))
	}
	return definition.Fields{
		Kind: kind, Scope: scopeFromNullableProjectID(projectID),
		Name: name, Status: definition.Status(status), Version: version,
	}, nil
}

// scopeFromNullableProjectID converts the definitions/workflow_definitions
// tables' own nullable project_id column into a definition.Scope — NULL
// means global, mirroring createSharedDefinitionTx's own inverse
// (scope.IsGlobal() -> a nil projectID bind parameter).
func scopeFromNullableProjectID(projectID sql.NullString) definition.Scope {
	if !projectID.Valid {
		return definition.GlobalScope()
	}
	return definition.ProjectScope(project.ProjectID(projectID.String))
}

// PublishVersion implements ports.DefinitionsRepository (V2-10): the
// Tx-composable equivalent of Store.PublishDefinitionVersion, for a
// command handler (internal/app/definitions) that needs the version
// insert and whatever domain event/receipt it writes alongside it to
// commit as one atomic transaction (GC-INV-15) — Store.PublishDefinitionVersion
// itself cannot be reused here since it always opens (and commits) its
// own transaction. It only ever serves the eight shared kinds; a Workflow
// publish goes through PublishWorkflowVersion below instead, since a
// Workflow candidate is always already-compiled (its own dependency
// resolution happens beforehand, in workflowcompiler.CompileAndResolve)
// rather than something this method's PublishVersionRequest shape could
// carry without losing the resolved dependency hashes workflow.Compile
// itself requires (definition.DependencyPin, unlike workflow.DependencyPin,
// carries no Hash field).
func (r definitionsRepository) PublishVersion(ctx context.Context, req ports.PublishVersionRequest) (definition.VersionFields, error) {
	if req.Kind == definition.KindWorkflow {
		return definition.VersionFields{}, errors.New("sqlite: DefinitionsRepository.PublishVersion does not support KindWorkflow — use PublishWorkflowVersion instead")
	}
	return publishSharedDefinitionVersionTx(ctx, r.tx, req)
}

// PublishWorkflowVersion implements ports.DefinitionsRepository (V2-10):
// the Tx-composable equivalent of Store.PublishWorkflowVersion, taking an
// already-compiled candidate (produced by workflowcompiler.CompileAndResolve,
// which needs its own read-only registry snapshot and therefore must run
// before the write transaction this method is composed inside even opens)
// rather than re-deriving one from a PublishVersionRequest's compiled
// snapshot the way Store.publishWorkflowDefinitionVersion does — seeing
// this doc comment's own sibling method's doc comment for why that
// round-trip cannot carry resolved dependency hashes.
func (r definitionsRepository) PublishWorkflowVersion(ctx context.Context, def workflow.WorkflowDefinition, candidate workflow.WorkflowVersion) (workflow.WorkflowVersion, error) {
	return publishWorkflowVersionTx(ctx, r.tx, def, candidate)
}

// GetWorkflowVersion implements ports.DefinitionsRepository (V4-02): reuses
// workflow_store.go's own loadWorkflowVersion, the identical rebuild/
// verify-against-hash logic WorkflowPersistence.LoadWorkflowVersion already
// runs for the spike-era caller, composed here against the given Tx instead
// of Store's own connection pool.
func (r definitionsRepository) GetWorkflowVersion(ctx context.Context, versionID string) (workflow.WorkflowVersion, error) {
	return loadWorkflowVersion(ctx, r.tx, workflow.WorkflowVersionID(versionID))
}

// ListVersions implements ports.DefinitionsRepository (V2-10): every
// published Version for definitionID, oldest first, routed by Kind the
// same way PublishVersion/CreateDefinition are.
func (r definitionsRepository) ListVersions(ctx context.Context, kind definition.Kind, definitionID string) ([]definition.VersionFields, error) {
	if kind == definition.KindWorkflow {
		versions, err := listWorkflowVersionsTx(ctx, r.tx, definitionID)
		if err != nil {
			return nil, err
		}
		result := make([]definition.VersionFields, 0, len(versions))
		for _, version := range versions {
			fields, err := workflowVersionToDefinitionFields(version, workflowDependencyManifestToDefinition(version.Dependencies()))
			if err != nil {
				return nil, err
			}
			result = append(result, fields)
		}
		return result, nil
	}
	return listSharedDefinitionVersionsTx(ctx, r.tx, definitionID)
}

// ListDefinitions implements ports.DefinitionsRepository (V6-15E): every
// Definition of kind that exists in scope — routed by kind exactly like
// GetDefinition/CreateDefinition/ListVersions (KindWorkflow reads
// workflow_definitions, every other kind reads the shared definitions
// table filtered by kind), further filtered to rows whose own project_id
// matches scope exactly (NULL for global, the named project otherwise).
// Ordered by id for a stable, diffable listing — no created_at tiebreak is
// needed since id is already the table's own primary key.
func (r definitionsRepository) ListDefinitions(ctx context.Context, kind definition.Kind, scope definition.Scope) ([]ports.DefinitionSummary, error) {
	var projectID any
	if !scope.IsGlobal() {
		projectID = string(*scope.ProjectID)
	}

	if kind == definition.KindWorkflow {
		rows, err := r.tx.QueryContext(ctx,
			`SELECT id, name, status, project_id, version FROM workflow_definitions WHERE project_id IS ? ORDER BY id`, projectID)
		if err != nil {
			return nil, MapSQLiteError(fmt.Errorf("list workflow definitions: %w", err))
		}
		defer rows.Close()
		result := make([]ports.DefinitionSummary, 0)
		for rows.Next() {
			var id, name, status string
			var rowProjectID sql.NullString
			var version uint64
			if err := rows.Scan(&id, &name, &status, &rowProjectID, &version); err != nil {
				return nil, MapSQLiteError(fmt.Errorf("scan workflow definition row: %w", err))
			}
			result = append(result, ports.DefinitionSummary{
				ID: id,
				Fields: definition.Fields{
					Kind: definition.KindWorkflow, Scope: scopeFromNullableProjectID(rowProjectID),
					Name: name, Status: definition.Status(status), Version: version,
				},
			})
		}
		if err := rows.Err(); err != nil {
			return nil, MapSQLiteError(fmt.Errorf("list workflow definitions: %w", err))
		}
		return result, nil
	}

	rows, err := r.tx.QueryContext(ctx,
		`SELECT id, project_id, name, status, version FROM definitions WHERE kind = ? AND project_id IS ? ORDER BY id`, string(kind), projectID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list definitions: %w", err))
	}
	defer rows.Close()
	result := make([]ports.DefinitionSummary, 0)
	for rows.Next() {
		var id, name, status string
		var rowProjectID sql.NullString
		var version uint64
		if err := rows.Scan(&id, &rowProjectID, &name, &status, &version); err != nil {
			return nil, MapSQLiteError(fmt.Errorf("scan definition row: %w", err))
		}
		result = append(result, ports.DefinitionSummary{
			ID: id,
			Fields: definition.Fields{
				Kind: kind, Scope: scopeFromNullableProjectID(rowProjectID),
				Name: name, Status: definition.Status(status), Version: version,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list definitions: %w", err))
	}
	return result, nil
}

func validatePublishVersionRequest(req ports.PublishVersionRequest) error {
	if strings.TrimSpace(req.DefinitionID) == "" {
		return errors.New("sqlite: PublishVersionRequest.DefinitionID is required")
	}
	if !req.Kind.Valid() {
		return fmt.Errorf("sqlite: unknown kind %q", req.Kind)
	}
	if strings.TrimSpace(req.VersionID) == "" {
		return errors.New("sqlite: PublishVersionRequest.VersionID is required")
	}
	return nil
}

// publishWorkflowDefinitionVersion routes a Workflow publish through the
// existing PublishWorkflowVersion (V0), so DefinitionPublisher's one
// entry point genuinely works for Workflow too, not only the 8 shared
// kinds.
func (s *Store) publishWorkflowDefinitionVersion(ctx context.Context, req ports.PublishVersionRequest) (definition.VersionFields, error) {
	var result definition.VersionFields
	err := s.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = publishWorkflowDefinitionVersionTx(ctx, tx, req)
		return err
	})
	return result, err
}

// publishWorkflowDefinitionVersionTx is publishWorkflowDefinitionVersion's
// own core, scoped to an already-open *sql.Tx it neither begins nor
// commits (see publishWorkflowVersionTx's doc comment in
// workflow_store.go for why). It loads the current workflow_definitions
// row itself (that table has no exported "load definition" method — only
// the publish/load version paths this reuses) so publishWorkflowVersionTx's
// own create-or-validate-match semantics have a real WorkflowDefinition to
// check against.
//
// This path re-derives workflow.DependencyPin values from req.Dependencies
// without a Hash (definition.DependencyPin, unlike workflow.DependencyPin,
// has no Hash field to carry one) and so only ever succeeds when
// req.Dependencies is empty — workflow.Compile's own normalizeManifest
// rejects any pin with a blank Hash. That is a pre-existing gap in this
// exact contract (Store.PublishDefinitionVersion for KindWorkflow), never
// exercised by any test with real dependency pins; it is out of scope to
// fix here; V2-10's own new command layer (internal/app/definitions)
// never goes through this function for a real Workflow publish — it uses
// DefinitionsRepository.PublishWorkflowVersion directly with a candidate
// workflowcompiler.CompileAndResolve already fully resolved, which
// carries real dependency hashes and has none of this method's
// limitation.
func publishWorkflowDefinitionVersionTx(ctx context.Context, tx *sql.Tx, req ports.PublishVersionRequest) (definition.VersionFields, error) {
	if err := validatePublishVersionRequest(req); err != nil {
		return definition.VersionFields{}, err
	}

	var name, status string
	var projectIDText sql.NullString
	var generation uint64
	err := tx.QueryRowContext(ctx,
		`SELECT name, status, project_id, version FROM workflow_definitions WHERE id = ?`,
		req.DefinitionID,
	).Scan(&name, &status, &projectIDText, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return definition.VersionFields{}, fmt.Errorf("%w: workflow definition %s", ports.ErrPersistenceNotFound, req.DefinitionID)
	}
	if err != nil {
		return definition.VersionFields{}, fmt.Errorf("load workflow definition: %w", err)
	}

	wfDefinition := workflow.WorkflowDefinition{
		ID: workflow.WorkflowDefinitionID(req.DefinitionID), Name: name,
		Status: workflow.DefinitionStatus(status), Version: generation,
	}
	if projectIDText.Valid {
		pid := project.ProjectID(projectIDText.String)
		wfDefinition.ProjectID = &pid
	}

	var document workflow.WorkflowDocument
	if err := json.Unmarshal([]byte(req.CompiledSnapshot), &document); err != nil {
		return definition.VersionFields{}, fmt.Errorf("decode workflow compiled snapshot: %w", err)
	}
	var wfDependencies workflow.DependencyManifest
	for _, pin := range req.Dependencies.Pins {
		wfDependencies.Pins = append(wfDependencies.Pins, workflow.DependencyPin{
			Kind: string(pin.Kind), Key: pin.DefinitionID, Version: pin.VersionID,
		})
	}

	var nextVersionNo uint64
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_no), 0) + 1 FROM workflow_versions WHERE definition_id = ?`,
		req.DefinitionID,
	).Scan(&nextVersionNo)
	if err != nil {
		return definition.VersionFields{}, fmt.Errorf("allocate workflow version number: %w", err)
	}

	candidate, err := workflow.Compile(wfDefinition, workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID(req.VersionID), VersionNumber: nextVersionNo,
		Document: document, Dependencies: wfDependencies,
		PublishedBy: req.PublishedBy, PublishedAt: req.PublishedAt,
	})
	if err != nil {
		return definition.VersionFields{}, fmt.Errorf("compile workflow version: %w", err)
	}

	published, err := publishWorkflowVersionTx(ctx, tx, wfDefinition, candidate)
	if err != nil {
		return definition.VersionFields{}, err
	}
	return workflowVersionToDefinitionFields(published, req.Dependencies)
}

// workflowVersionToDefinitionFields converts a compiled/persisted
// workflow.WorkflowVersion into the kind-agnostic definition.VersionFields
// shape every DefinitionPublisher/DefinitionsRepository caller sees,
// regardless of which of the two storage layouts actually served the
// Kind. dependencies is supplied by the caller, not derived from
// v.Dependencies(), because the two call sites need different manifests:
// publishWorkflowDefinitionVersionTx reports back the original
// PublishVersionRequest.Dependencies its own caller supplied (an
// established, if imperfect, convention this refactor preserves
// unchanged — see that function's own doc comment); listWorkflowVersionsTx's
// caller (DefinitionsRepository.ListVersions) has no such original
// request and instead derives one from the persisted version's own
// resolved manifest via workflowDependencyManifestToDefinition.
//
// workflow.WorkflowVersion.SchemaVersion() is a string (e.g. "1") — the
// shared definition.VersionFields uses an int instead, since the 8 new
// kinds define theirs that way from the start (V2-03+). Every existing
// workflow fixture uses a plain numeric string; a non-numeric one (never
// seen in practice) falls back to 0 rather than silently pretending it
// means "1".
func workflowVersionToDefinitionFields(v workflow.WorkflowVersion, dependencies definition.DependencyManifest) (definition.VersionFields, error) {
	schemaVersion, _ := strconv.Atoi(v.SchemaVersion())
	return definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID: string(v.ID()), DefinitionID: string(v.DefinitionID()), Kind: definition.KindWorkflow,
		VersionNumber: v.VersionNumber(), SchemaVersion: schemaVersion,
		CanonicalSource: string(v.CanonicalContent()), SourceHash: v.ContentHash(),
		CompiledSnapshot: string(v.CanonicalContent()), CompiledHash: v.ContentHash(),
		Dependencies: dependencies, PublishedBy: v.PublishedBy(), PublishedAt: v.PublishedAt(),
	})
}

// workflowDependencyManifestToDefinition converts a resolved
// workflow.DependencyManifest into the kind-agnostic
// definition.DependencyManifest shape — dropping each pin's resolved Hash
// (definition.DependencyPin has no field for it), which is fine for a
// read/report value like this one: nothing re-derives a content hash from
// it the way workflow.Compile's own normalizeManifest would.
func workflowDependencyManifestToDefinition(m workflow.DependencyManifest) definition.DependencyManifest {
	var result definition.DependencyManifest
	for _, pin := range m.Pins {
		result.Pins = append(result.Pins, definition.DependencyPin{
			Kind: definition.Kind(pin.Kind), DefinitionID: pin.Key, VersionID: pin.Version,
		})
	}
	return result
}
