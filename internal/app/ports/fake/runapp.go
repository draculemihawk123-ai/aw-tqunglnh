package fake

import (
	"context"
	"fmt"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// RunAppRepository is the in-memory ports.RunAppRepository (V12-30).
type RunAppRepository struct {
	profiles map[string]ports.RunAppProfileRecord
	sessions map[string]ports.RunAppSession
}

func (r *RunAppRepository) clone() *RunAppRepository {
	out := &RunAppRepository{profiles: map[string]ports.RunAppProfileRecord{}, sessions: map[string]ports.RunAppSession{}}
	for k, v := range r.profiles {
		out.profiles[k] = v
	}
	for k, v := range r.sessions {
		out.sessions[k] = v
	}
	return out
}

func (r *RunAppRepository) init() {
	if r.profiles == nil {
		r.profiles = map[string]ports.RunAppProfileRecord{}
	}
	if r.sessions == nil {
		r.sessions = map[string]ports.RunAppSession{}
	}
}

// GetRunAppProfile mirrors the SQLite repository.
func (r *RunAppRepository) GetRunAppProfile(_ context.Context, repositoryID string) (ports.RunAppProfileRecord, error) {
	p, ok := r.profiles[repositoryID]
	if !ok {
		return ports.RunAppProfileRecord{}, fmt.Errorf("fake: %w: profile", ports.ErrPersistenceNotFound)
	}
	return p, nil
}

// PutRunAppProfile mirrors the SQLite repository.
func (r *RunAppRepository) PutRunAppProfile(_ context.Context, p ports.RunAppProfileRecord, expected uint64) (ports.RunAppProfileRecord, error) {
	r.init()
	existing, ok := r.profiles[p.RepositoryID]
	if (!ok && expected != 0) || (ok && existing.Version != expected) {
		return ports.RunAppProfileRecord{}, fmt.Errorf("fake: %w: profile", ports.ErrOptimisticConflict)
	}
	p.Version = expected + 1
	r.profiles[p.RepositoryID] = p
	return p, nil
}

// CreateRunAppSession mirrors the SQLite repository.
func (r *RunAppRepository) CreateRunAppSession(_ context.Context, s ports.RunAppSession) (ports.RunAppSession, error) {
	r.init()
	for _, other := range r.sessions {
		if other.RepositoryID == s.RepositoryID && other.State.Active() {
			return ports.RunAppSession{}, fmt.Errorf("fake: %w: active session", ports.ErrOptimisticConflict)
		}
	}
	s.Version = 1
	r.sessions[s.ID] = s
	return s, nil
}

// GetRunAppSession mirrors the SQLite repository.
func (r *RunAppRepository) GetRunAppSession(_ context.Context, id string) (ports.RunAppSession, error) {
	s, ok := r.sessions[id]
	if !ok {
		return ports.RunAppSession{}, fmt.Errorf("fake: %w: session", ports.ErrPersistenceNotFound)
	}
	return s, nil
}

// LatestRunAppSession mirrors the SQLite repository.
func (r *RunAppRepository) LatestRunAppSession(_ context.Context, rwID string) (ports.RunAppSession, error) {
	var best *ports.RunAppSession
	for _, s := range r.sessions {
		if s.RepositoryWorkspaceID == rwID && (best == nil || s.CreatedAt.After(best.CreatedAt)) {
			c := s
			best = &c
		}
	}
	if best == nil {
		return ports.RunAppSession{}, fmt.Errorf("fake: %w: session", ports.ErrPersistenceNotFound)
	}
	return *best, nil
}

// ListRunAppSessionsInStates mirrors the SQLite repository.
func (r *RunAppRepository) ListRunAppSessionsInStates(_ context.Context, states []ports.RunAppSessionState) ([]ports.RunAppSession, error) {
	want := map[ports.RunAppSessionState]bool{}
	for _, s := range states {
		want[s] = true
	}
	var out []ports.RunAppSession
	for _, s := range r.sessions {
		if want[s.State] {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// UpdateRunAppSession mirrors the SQLite repository.
func (r *RunAppRepository) UpdateRunAppSession(_ context.Context, s ports.RunAppSession, expected uint64) (ports.RunAppSession, error) {
	existing, ok := r.sessions[s.ID]
	if !ok || existing.Version != expected {
		return ports.RunAppSession{}, fmt.Errorf("fake: %w: session", ports.ErrOptimisticConflict)
	}
	s.Version = expected + 1
	r.sessions[s.ID] = s
	return s, nil
}
