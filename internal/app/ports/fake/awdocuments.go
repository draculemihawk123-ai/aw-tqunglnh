package fake

import (
	"context"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// AwDocumentSnapshotRepository is an in-memory ports.AwDocumentSnapshotRepository (V12-33).
type AwDocumentSnapshotRepository struct {
	rows map[string]ports.AwDocumentSnapshot
}

func (r *AwDocumentSnapshotRepository) clone() *AwDocumentSnapshotRepository {
	out := &AwDocumentSnapshotRepository{rows: map[string]ports.AwDocumentSnapshot{}}
	for k, v := range r.rows {
		out.rows[k] = v
	}
	return out
}

// ListAwDocumentSnapshots implements the port.
func (r *AwDocumentSnapshotRepository) ListAwDocumentSnapshots(_ context.Context, familyID, repositoryID string) ([]ports.AwDocumentSnapshot, error) {
	out := []ports.AwDocumentSnapshot{}
	for _, s := range r.rows {
		if s.FamilyID == familyID && s.RepositoryID == repositoryID {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// PutAwDocumentSnapshot implements the port.
func (r *AwDocumentSnapshotRepository) PutAwDocumentSnapshot(_ context.Context, s ports.AwDocumentSnapshot) error {
	if r.rows == nil {
		r.rows = map[string]ports.AwDocumentSnapshot{}
	}
	r.rows[s.FamilyID+"\x00"+s.RepositoryID+"\x00"+s.Path] = s
	return nil
}
