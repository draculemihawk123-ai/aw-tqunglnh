package fake

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// ForgeRepository is the in-memory ports.ForgeRepository (V12-28). Approved holds, per repository workspace, the revisions a test
// says went through an approval gate.
type ForgeRepository struct {
	remotes  map[string]ports.RemoteBranchRecord
	Approved map[string][]string
}

func (r *ForgeRepository) clone() *ForgeRepository {
	out := &ForgeRepository{remotes: map[string]ports.RemoteBranchRecord{}, Approved: map[string][]string{}}
	for k, v := range r.remotes {
		out.remotes[k] = v
	}
	for k, v := range r.Approved {
		out.Approved[k] = append([]string(nil), v...)
	}
	return out
}

// GetRemoteBranch mirrors the SQLite repository.
func (r *ForgeRepository) GetRemoteBranch(_ context.Context, id string) (ports.RemoteBranchRecord, error) {
	rec, ok := r.remotes[id]
	if !ok {
		return ports.RemoteBranchRecord{}, fmt.Errorf("fake: %w: remote branch of %s", ports.ErrPersistenceNotFound, id)
	}
	return rec, nil
}

// PutRemoteBranch mirrors the SQLite repository.
func (r *ForgeRepository) PutRemoteBranch(_ context.Context, rec ports.RemoteBranchRecord, expectedVersion uint64) (ports.RemoteBranchRecord, error) {
	if r.remotes == nil {
		r.remotes = map[string]ports.RemoteBranchRecord{}
	}
	existing, ok := r.remotes[rec.RepositoryWorkspaceID]
	if (!ok && expectedVersion != 0) || (ok && existing.Version != expectedVersion) {
		return ports.RemoteBranchRecord{}, fmt.Errorf("fake: %w: remote branch", ports.ErrOptimisticConflict)
	}
	rec.Version = expectedVersion + 1
	r.remotes[rec.RepositoryWorkspaceID] = rec
	return rec, nil
}

// ApprovedCommitRevisions returns what the test seeded.
func (r *ForgeRepository) ApprovedCommitRevisions(_ context.Context, id string) ([]string, error) {
	return append([]string(nil), r.Approved[id]...), nil
}
