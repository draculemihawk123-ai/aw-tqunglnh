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
	_, err = s.db.ExecContext(ctx, `
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
	if err := validatePublishVersionRequest(req); err != nil {
		return definition.VersionFields{}, err
	}

	var result definition.VersionFields
	err := s.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		var status string
		var projectID sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT status, project_id FROM definitions WHERE id = ? AND kind = ?`,
			req.DefinitionID, string(req.Kind),
		).Scan(&status, &projectID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: definition %s (kind %s)", ports.ErrPersistenceNotFound, req.DefinitionID, req.Kind)
		}
		if err != nil {
			return MapSQLiteError(fmt.Errorf("load definition for publish: %w", err))
		}
		if err := definition.CanPublish(definition.Status(status)); err != nil {
			return err
		}

		// Cross-project dependency validation: each pin's actual project
		// is resolved from the repository itself, never trusted from the
		// request (ports.ErrCrossProjectDependency's own doc comment).
		for _, pin := range req.Dependencies.Pins {
			var pinProjectID sql.NullString
			err := tx.QueryRowContext(ctx, `SELECT project_id FROM definitions WHERE id = ?`, pin.DefinitionID).Scan(&pinProjectID)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: dependency %s not found", ports.ErrPersistenceNotFound, pin.DefinitionID)
			}
			if err != nil {
				return MapSQLiteError(fmt.Errorf("resolve dependency project: %w", err))
			}
			if pinProjectID.Valid != projectID.Valid || pinProjectID.String != projectID.String {
				return fmt.Errorf("%w: dependency %s", ports.ErrCrossProjectDependency, pin.DefinitionID)
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
			result, err = loadSharedDefinitionVersion(ctx, tx, existingID)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return MapSQLiteError(fmt.Errorf("find existing definition version: %w", err))
		}

		var nextVersionNo uint64
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(version_no), 0) + 1 FROM definition_versions WHERE definition_id = ?`,
			req.DefinitionID,
		).Scan(&nextVersionNo); err != nil {
			return MapSQLiteError(fmt.Errorf("allocate definition version number: %w", err))
		}

		dependenciesJSON, err := json.Marshal(req.Dependencies)
		if err != nil {
			return fmt.Errorf("marshal dependency manifest: %w", err)
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
			return MapSQLiteError(fmt.Errorf("insert definition version: %w", err))
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE definitions SET version = version + 1, updated_at = ? WHERE id = ?`,
			publishedAt, req.DefinitionID,
		); err != nil {
			return MapSQLiteError(fmt.Errorf("bump definition generation: %w", err))
		}

		result, err = definition.NewVersionFields(definition.NewVersionFieldsRequest{
			ID: req.VersionID, DefinitionID: req.DefinitionID, Kind: req.Kind,
			VersionNumber: nextVersionNo, SchemaVersion: req.SchemaVersion,
			CanonicalSource: req.CanonicalSource, SourceHash: req.SourceHash,
			CompiledSnapshot: req.CompiledSnapshot, CompiledHash: req.CompiledHash,
			Dependencies: req.Dependencies, PublishedBy: req.PublishedBy, PublishedAt: req.PublishedAt,
		})
		return err
	})
	return result, err
}

func loadSharedDefinitionVersion(ctx context.Context, tx *sql.Tx, versionID string) (definition.VersionFields, error) {
	var definitionID, kind, canonicalSource, sourceHash, compiledSnapshot, compiledHash string
	var dependenciesJSON, publishedBy, publishedAtText string
	var versionNo uint64
	var schemaVersion int
	err := tx.QueryRowContext(ctx, `
SELECT dv.definition_id, d.kind, dv.version_no, dv.schema_version, dv.canonical_source, dv.source_hash,
       dv.compiled_snapshot, dv.compiled_hash, dv.dependency_manifest, dv.published_by, dv.published_at
FROM definition_versions dv
JOIN definitions d ON d.id = dv.definition_id
WHERE dv.id = ?`, versionID,
	).Scan(&definitionID, &kind, &versionNo, &schemaVersion, &canonicalSource, &sourceHash,
		&compiledSnapshot, &compiledHash, &dependenciesJSON, &publishedBy, &publishedAtText)
	if errors.Is(err, sql.ErrNoRows) {
		return definition.VersionFields{}, ports.ErrDefinitionVersionNotFound
	}
	if err != nil {
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
		ID: versionID, DefinitionID: definitionID, Kind: definition.Kind(kind),
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
// kinds. It loads the current workflow_definitions row itself (that
// table has no exported "load definition" method — only the publish/load
// version paths this reuses) so PublishWorkflowVersion's own
// create-or-validate-match semantics have a real WorkflowDefinition to
// check against.
func (s *Store) publishWorkflowDefinitionVersion(ctx context.Context, req ports.PublishVersionRequest) (definition.VersionFields, error) {
	if err := validatePublishVersionRequest(req); err != nil {
		return definition.VersionFields{}, err
	}

	var name, status string
	var projectIDText sql.NullString
	var generation uint64
	err := s.db.QueryRowContext(ctx,
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
	err = s.db.QueryRowContext(ctx,
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

	published, err := s.PublishWorkflowVersion(ctx, wfDefinition, candidate)
	if err != nil {
		return definition.VersionFields{}, err
	}

	// workflow.WorkflowVersion.SchemaVersion() is a string (e.g. "1") —
	// the shared definition.VersionFields uses an int instead, since the
	// 8 new kinds define theirs that way from the start (V2-03+). Every
	// existing workflow fixture uses a plain numeric string; a
	// non-numeric one (never seen in practice) falls back to 0 rather
	// than silently pretending it means "1".
	schemaVersion, _ := strconv.Atoi(candidate.SchemaVersion())

	return definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID: string(published.ID()), DefinitionID: string(published.DefinitionID()), Kind: definition.KindWorkflow,
		VersionNumber: published.VersionNumber(), SchemaVersion: schemaVersion,
		CanonicalSource: string(published.CanonicalContent()), SourceHash: published.ContentHash(),
		CompiledSnapshot: string(published.CanonicalContent()), CompiledHash: published.ContentHash(),
		Dependencies: req.Dependencies, PublishedBy: published.PublishedBy(), PublishedAt: published.PublishedAt(),
	})
}
