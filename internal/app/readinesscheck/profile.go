package readinesscheck

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/readiness"
)

// SetReadinessProfile persists repository's own canonical
// setup/verification recipe — a thin, non-enveloped helper (no
// Command/receipt/event), the same shape
// internal/app/catalog.CreateComponent already establishes for a concern
// with no cited public command of its own: HE-06-M02 asks that a
// project/component "MUST có setup, start/health và verification recipes
// xác định được", it names no publish/review/idempotent-command workflow,
// and this method's own storage-layer upsert (ports.ReadinessRepository.
// SetReadinessProfile's own doc comment) is already this task's whole
// replay story — a caller that calls it twice with the same profile
// simply gets the same result written twice, never a duplicate or a
// conflict.
func SetReadinessProfile(ctx context.Context, uow ports.UnitOfWork, profile readiness.Profile) (readiness.Profile, error) {
	var result readiness.Profile
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		created, err := tx.Readiness().SetReadinessProfile(ctx, profile)
		result = created
		return err
	})
	return result, err
}

// GetReadinessProfile is a read-only query, never a Command — mirroring
// internal/app/catalog.ListProjectRepositories's own identical shape.
func GetReadinessProfile(ctx context.Context, uow ports.UnitOfWork, repositoryID string) (readiness.Profile, error) {
	var result readiness.Profile
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		profile, err := tx.Readiness().GetReadinessProfile(ctx, repositoryID)
		result = profile
		return err
	})
	return result, err
}

// ListBaselineAttempts is a read-only query over the append-only baseline
// evidence log for one RepositoryWorkspace.
func ListBaselineAttempts(ctx context.Context, uow ports.UnitOfWork, repositoryWorkspaceID string) ([]ports.BaselineAttempt, error) {
	var result []ports.BaselineAttempt
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempts, err := tx.Readiness().ListBaselineAttempts(ctx, repositoryWorkspaceID)
		result = attempts
		return err
	})
	return result, err
}

// GetOpenEnvironmentBlocker is a read-only query — the read a future
// writer-gating check (V4+, out of this task's own scope) uses to decide
// whether a RepositoryWorkspace currently has an unresolved environment
// blocker.
func GetOpenEnvironmentBlocker(ctx context.Context, uow ports.UnitOfWork, repositoryWorkspaceID string) (ports.EnvironmentBlocker, error) {
	var result ports.EnvironmentBlocker
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		blocker, err := tx.Readiness().GetOpenEnvironmentBlocker(ctx, repositoryWorkspaceID)
		result = blocker
		return err
	})
	return result, err
}
