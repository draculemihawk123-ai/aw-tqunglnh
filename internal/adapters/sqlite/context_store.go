package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

type contextSnapshotRowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type persistedContextSnapshot struct {
	Messages  []runtime.ContextMessage  `json:"messages"`
	Resources []runtime.ContextResource `json:"resources"`
	Revisions []workspace.Revision      `json:"revisions"`
}

func (s *Store) StoreContextSnapshot(
	ctx context.Context,
	projectID project.ProjectID,
	snapshot runtime.ContextSnapshot,
) (runtime.ContextSnapshot, error) {
	if projectID == "" || snapshot.ID() == "" || snapshot.AttemptID() == "" ||
		strings.TrimSpace(snapshot.ContentHash()) == "" || snapshot.CreatedAt().IsZero() {
		return runtime.ContextSnapshot{}, errors.New("context snapshot project, identity, hash and timestamp are required")
	}
	revisionJSON, err := json.Marshal(snapshot.Revisions().Entries())
	if err != nil {
		return runtime.ContextSnapshot{}, fmt.Errorf("encode context snapshot revisions: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO context_snapshots(
    id, project_id, attempt_id, canonical_content, content_hash, revision_set_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		snapshot.ID(),
		projectID,
		snapshot.AttemptID(),
		string(snapshot.CanonicalContent()),
		snapshot.ContentHash(),
		string(revisionJSON),
		formatWorkflowTime(snapshot.CreatedAt()),
	)
	if err == nil {
		return snapshot, nil
	}

	// Snapshot creation is idempotent only for the exact immutable content.
	existing, loadErr := s.LoadContextSnapshot(ctx, snapshot.ID())
	if loadErr == nil {
		if existing.AttemptID() == snapshot.AttemptID() && existing.ContentHash() == snapshot.ContentHash() &&
			bytes.Equal(existing.CanonicalContent(), snapshot.CanonicalContent()) {
			return existing, nil
		}
		return runtime.ContextSnapshot{}, fmt.Errorf("%w: context snapshot %s", ports.ErrImmutableVersionConflict, snapshot.ID())
	}
	var existingID runtime.ContextSnapshotID
	lookupErr := s.db.QueryRowContext(ctx, `
SELECT id FROM context_snapshots WHERE attempt_id = ? AND content_hash = ?`,
		snapshot.AttemptID(), snapshot.ContentHash()).Scan(&existingID)
	if lookupErr == nil {
		return s.LoadContextSnapshot(ctx, existingID)
	}
	return runtime.ContextSnapshot{}, fmt.Errorf("store context snapshot: %w", err)
}

func (s *Store) LoadContextSnapshot(
	ctx context.Context,
	id runtime.ContextSnapshotID,
) (runtime.ContextSnapshot, error) {
	return loadContextSnapshot(ctx, s.db, id)
}

func loadContextSnapshot(
	ctx context.Context,
	queryer contextSnapshotRowQueryer,
	id runtime.ContextSnapshotID,
) (runtime.ContextSnapshot, error) {
	if id == "" {
		return runtime.ContextSnapshot{}, errors.New("context snapshot id is required")
	}
	var attemptID runtime.ExecutionAttemptID
	var canonicalContent string
	var contentHash string
	var revisionSetJSON string
	var createdAt string
	err := queryer.QueryRowContext(ctx, `
SELECT attempt_id, canonical_content, content_hash, revision_set_json, created_at
FROM context_snapshots WHERE id = ?`, id).Scan(
		&attemptID,
		&canonicalContent,
		&contentHash,
		&revisionSetJSON,
		&createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.ContextSnapshot{}, fmt.Errorf("%w: context snapshot %s", ports.ErrPersistenceNotFound, id)
	}
	if err != nil {
		return runtime.ContextSnapshot{}, fmt.Errorf("load context snapshot: %w", err)
	}
	var content persistedContextSnapshot
	if err := json.Unmarshal([]byte(canonicalContent), &content); err != nil {
		return runtime.ContextSnapshot{}, fmt.Errorf("decode context snapshot canonical content: %w", err)
	}
	var storedRevisions []workspace.Revision
	if err := json.Unmarshal([]byte(revisionSetJSON), &storedRevisions); err != nil {
		return runtime.ContextSnapshot{}, fmt.Errorf("decode context snapshot revisions: %w", err)
	}
	if !sameRevisions(content.Revisions, storedRevisions) {
		return runtime.ContextSnapshot{}, fmt.Errorf("%w: context snapshot revision index differs", ports.ErrImmutableVersionConflict)
	}
	revisions, err := workspace.NewRevisionSet(content.Revisions)
	if err != nil {
		return runtime.ContextSnapshot{}, err
	}
	created, err := parseWorkflowTime(createdAt)
	if err != nil {
		return runtime.ContextSnapshot{}, err
	}
	rebuilt, err := runtime.NewContextSnapshot(runtime.ContextSnapshotInput{
		ID:        id,
		AttemptID: attemptID,
		Messages:  content.Messages,
		Resources: content.Resources,
		Revisions: revisions,
		CreatedAt: created,
	})
	if err != nil {
		return runtime.ContextSnapshot{}, fmt.Errorf("%w: rebuild context snapshot: %v", ports.ErrImmutableVersionConflict, err)
	}
	if rebuilt.ContentHash() != contentHash || !bytes.Equal(rebuilt.CanonicalContent(), []byte(canonicalContent)) {
		return runtime.ContextSnapshot{}, fmt.Errorf("%w: context snapshot canonical hash mismatch", ports.ErrImmutableVersionConflict)
	}
	return rebuilt, nil
}

func sameRevisions(left, right []workspace.Revision) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}
