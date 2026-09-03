package fake

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
)

// ReadinessRepository is an in-memory ports.ReadinessRepository — V3-07
// gives this concern its first real behavior, so
// internal/app/readinesscheck can be tested without sqlite (the same
// V1-05 discipline every other fake repository here already follows).
// catalog is a pointer to the same Tx's CatalogRepository, mirroring how
// WorkRepository resolves a Repository's own existence from the shared
// CatalogRepository rather than duplicating that state.
type ReadinessRepository struct {
	catalog *CatalogRepository

	profiles         map[string]readiness.Profile       // by RepositoryID
	baselineAttempts map[string][]ports.BaselineAttempt // by RepositoryWorkspaceID, insertion order
	attemptsByJobID  map[string]ports.BaselineAttempt
	openBlockers     map[string]ports.EnvironmentBlocker // by RepositoryWorkspaceID, only while OPEN
}

var _ ports.ReadinessRepository = (*ReadinessRepository)(nil)

func (r *ReadinessRepository) cloneWith(catalog *CatalogRepository) *ReadinessRepository {
	profiles := make(map[string]readiness.Profile, len(r.profiles))
	for k, v := range r.profiles {
		profiles[k] = v
	}
	baselineAttempts := make(map[string][]ports.BaselineAttempt, len(r.baselineAttempts))
	for k, v := range r.baselineAttempts {
		baselineAttempts[k] = append([]ports.BaselineAttempt(nil), v...)
	}
	attemptsByJobID := make(map[string]ports.BaselineAttempt, len(r.attemptsByJobID))
	for k, v := range r.attemptsByJobID {
		attemptsByJobID[k] = v
	}
	openBlockers := make(map[string]ports.EnvironmentBlocker, len(r.openBlockers))
	for k, v := range r.openBlockers {
		openBlockers[k] = v
	}
	return &ReadinessRepository{
		catalog: catalog, profiles: profiles,
		baselineAttempts: baselineAttempts, attemptsByJobID: attemptsByJobID, openBlockers: openBlockers,
	}
}

func (r *ReadinessRepository) SetReadinessProfile(_ context.Context, profile readiness.Profile) (readiness.Profile, error) {
	repositoryID := string(profile.RepositoryID)
	if _, ok := r.catalog.repositories[repositoryID]; !ok {
		return readiness.Profile{}, fmt.Errorf("fake: %w: repository %s", ports.ErrPersistenceNotFound, repositoryID)
	}
	if r.profiles == nil {
		r.profiles = map[string]readiness.Profile{}
	}
	next := profile
	if existing, ok := r.profiles[repositoryID]; ok {
		next.Version = existing.Version + 1
	} else {
		next.Version = 1
	}
	r.profiles[repositoryID] = next
	return next, nil
}

func (r *ReadinessRepository) GetReadinessProfile(_ context.Context, repositoryID string) (readiness.Profile, error) {
	profile, ok := r.profiles[repositoryID]
	if !ok {
		return readiness.Profile{}, fmt.Errorf("fake: %w: readiness profile for repository %s", ports.ErrPersistenceNotFound, repositoryID)
	}
	return profile, nil
}

func (r *ReadinessRepository) RecordBaselineAttempt(_ context.Context, req ports.RecordBaselineAttemptRequest) (ports.BaselineAttempt, error) {
	if _, exists := r.attemptsByJobID[req.JobID]; exists {
		return ports.BaselineAttempt{}, fmt.Errorf("fake: duplicate baseline attempt job id %q", req.JobID)
	}
	attempt := ports.BaselineAttempt{
		ID: req.ID, ProjectID: req.ProjectID, RepositoryWorkspaceID: req.RepositoryWorkspaceID,
		RepositoryID: req.RepositoryID, JobID: req.JobID, Stage: req.Stage, Outcome: req.Outcome,
		ExitCode: req.ExitCode, DurationMS: req.DurationMS, StdoutExcerpt: req.StdoutExcerpt, StderrExcerpt: req.StderrExcerpt,
		ErrorCode: req.ErrorCode, ErrorMessage: req.ErrorMessage, CreatedAt: time.Now().UTC(),
	}
	if r.baselineAttempts == nil {
		r.baselineAttempts = map[string][]ports.BaselineAttempt{}
	}
	if r.attemptsByJobID == nil {
		r.attemptsByJobID = map[string]ports.BaselineAttempt{}
	}
	r.baselineAttempts[req.RepositoryWorkspaceID] = append(r.baselineAttempts[req.RepositoryWorkspaceID], attempt)
	r.attemptsByJobID[req.JobID] = attempt
	return attempt, nil
}

func (r *ReadinessRepository) GetBaselineAttemptByJobID(_ context.Context, jobID string) (ports.BaselineAttempt, error) {
	attempt, ok := r.attemptsByJobID[jobID]
	if !ok {
		return ports.BaselineAttempt{}, fmt.Errorf("fake: %w: baseline attempt for job %s", ports.ErrPersistenceNotFound, jobID)
	}
	return attempt, nil
}

func (r *ReadinessRepository) ListBaselineAttempts(_ context.Context, repositoryWorkspaceID string) ([]ports.BaselineAttempt, error) {
	attempts := append([]ports.BaselineAttempt(nil), r.baselineAttempts[repositoryWorkspaceID]...)
	sort.Slice(attempts, func(i, j int) bool { return attempts[i].CreatedAt.Before(attempts[j].CreatedAt) })
	return attempts, nil
}

func (r *ReadinessRepository) OpenEnvironmentBlocker(_ context.Context, req ports.OpenEnvironmentBlockerRequest) (ports.EnvironmentBlocker, bool, error) {
	if existing, ok := r.openBlockers[req.RepositoryWorkspaceID]; ok {
		return existing, true, nil
	}
	blocker := ports.EnvironmentBlocker{
		ID: req.ID, ProjectID: req.ProjectID, RepositoryWorkspaceID: req.RepositoryWorkspaceID,
		RepositoryID: req.RepositoryID, JobID: req.JobID, Type: readiness.EnvironmentBlockerType,
		Reason: req.Reason, Status: readiness.BlockerOpen, CreatedAt: time.Now().UTC(),
	}
	if r.openBlockers == nil {
		r.openBlockers = map[string]ports.EnvironmentBlocker{}
	}
	r.openBlockers[req.RepositoryWorkspaceID] = blocker
	return blocker, false, nil
}

func (r *ReadinessRepository) ResolveOpenEnvironmentBlocker(_ context.Context, repositoryWorkspaceID string, _ time.Time) error {
	delete(r.openBlockers, repositoryWorkspaceID)
	return nil
}

func (r *ReadinessRepository) GetOpenEnvironmentBlocker(_ context.Context, repositoryWorkspaceID string) (ports.EnvironmentBlocker, error) {
	blocker, ok := r.openBlockers[repositoryWorkspaceID]
	if !ok {
		return ports.EnvironmentBlocker{}, fmt.Errorf("fake: %w: open environment blocker for repository workspace %s", ports.ErrPersistenceNotFound, repositoryWorkspaceID)
	}
	return blocker, nil
}
