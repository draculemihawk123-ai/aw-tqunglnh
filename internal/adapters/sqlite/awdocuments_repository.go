package sqlite

import (
	"context"
	"database/sql"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// awDocumentSnapshotRepository implements ports.AwDocumentSnapshotRepository (V12-33, migration 0065).
type awDocumentSnapshotRepository struct {
	tx *sql.Tx
}

func (r awDocumentSnapshotRepository) ListAwDocumentSnapshots(ctx context.Context, familyID, repositoryID string) ([]ports.AwDocumentSnapshot, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT family_id, repository_id, path, project_id, artifact_id, sha256, size, updated_at
		FROM aw_document_snapshots WHERE family_id = ? AND repository_id = ? ORDER BY path`, familyID, repositoryID)
	if err != nil {
		return nil, MapSQLiteError(err)
	}
	defer rows.Close()
	out := []ports.AwDocumentSnapshot{}
	for rows.Next() {
		var s ports.AwDocumentSnapshot
		var updated string
		if err := rows.Scan(&s.FamilyID, &s.RepositoryID, &s.Path, &s.ProjectID, &s.ArtifactID, &s.SHA256, &s.Size, &updated); err != nil {
			return nil, MapSQLiteError(err)
		}
		if s.UpdatedAt, err = parseWorkflowTime(updated); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, MapSQLiteError(rows.Err())
}

func (r awDocumentSnapshotRepository) PutAwDocumentSnapshot(ctx context.Context, s ports.AwDocumentSnapshot) error {
	_, err := r.tx.ExecContext(ctx, `INSERT INTO aw_document_snapshots (family_id, repository_id, path, project_id, artifact_id, sha256, size, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (family_id, repository_id, path) DO UPDATE SET artifact_id = excluded.artifact_id, sha256 = excluded.sha256,
		size = excluded.size, updated_at = excluded.updated_at`,
		s.FamilyID, s.RepositoryID, s.Path, s.ProjectID, s.ArtifactID, s.SHA256, s.Size, formatWorkflowTime(s.UpdatedAt))
	return MapSQLiteError(err)
}
