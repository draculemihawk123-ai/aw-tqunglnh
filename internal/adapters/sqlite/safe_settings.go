package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/safesettings"
)

// safeSettingsRepository implements ports.SafeSettingsRepository (V6-10G,
// docs/design/08-v6-api-projections.md) over the singleton safe_settings
// row (migration 0036_safe_settings.sql).
type safeSettingsRepository struct{ tx *sql.Tx }

var _ ports.SafeSettingsRepository = safeSettingsRepository{}

// Get implements ports.SafeSettingsRepository. The singleton row always
// exists once migration 0036 has run, so sql.ErrNoRows here would mean the
// migration itself never ran — the same "should never happen" case
// GetRecoveryReaperState documents for its own singleton row — and is
// surfaced as a plain error, never ports.ErrPersistenceNotFound (a caller
// has no legitimate "create it" fallback to reach for). A desired_json
// that fails to decode — a bit-rotted or hand-edited row — is
// ports.ErrSafeSettingsCorrupt, this task's own typed "corrupt persisted
// settings" Verify signal, never a panic or a silent fallback to the zero
// document.
func (r safeSettingsRepository) Get(ctx context.Context) (ports.SafeSettingsRecord, error) {
	return loadSafeSettingsTx(ctx, r.tx)
}

func loadSafeSettingsTx(ctx context.Context, tx *sql.Tx) (ports.SafeSettingsRecord, error) {
	var desiredJSON, updatedAt string
	var version uint64
	var updatedBy sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT desired_json, version, updated_at, updated_by FROM safe_settings WHERE id = 'singleton'`,
	).Scan(&desiredJSON, &version, &updatedAt, &updatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.SafeSettingsRecord{}, errors.New("safe settings: singleton row is missing — migration 0036_safe_settings.sql did not run")
	}
	if err != nil {
		return ports.SafeSettingsRecord{}, MapSQLiteError(fmt.Errorf("load safe settings: %w", err))
	}
	var desired safesettings.SafeSettings
	if err := json.Unmarshal([]byte(desiredJSON), &desired); err != nil {
		return ports.SafeSettingsRecord{}, fmt.Errorf("%w: %v", ports.ErrSafeSettingsCorrupt, err)
	}
	occurredAt, err := parseWorkflowTime(updatedAt)
	if err != nil {
		return ports.SafeSettingsRecord{}, fmt.Errorf("%w: updated_at: %v", ports.ErrSafeSettingsCorrupt, err)
	}
	return ports.SafeSettingsRecord{
		Desired: desired, Version: version, UpdatedAt: occurredAt, UpdatedBy: updatedBy.String,
	}, nil
}

// Update implements ports.SafeSettingsRepository: a CAS full-document
// replacement, mirroring workRepository.TransitionReleaseSetState's own
// "UPDATE ... SET ..., version = version + 1 WHERE id = ? AND version = ?;
// check RowsAffected" shape exactly.
func (r safeSettingsRepository) Update(ctx context.Context, req ports.UpdateSafeSettingsRequest) (ports.SafeSettingsRecord, error) {
	desiredJSON, err := json.Marshal(req.Desired)
	if err != nil {
		return ports.SafeSettingsRecord{}, fmt.Errorf("marshal safe settings desired document: %w", err)
	}
	updatedBy := sql.NullString{String: req.UpdatedBy, Valid: req.UpdatedBy != ""}
	result, err := r.tx.ExecContext(ctx, `
UPDATE safe_settings
SET desired_json = ?, version = version + 1, updated_at = ?, updated_by = ?
WHERE id = 'singleton' AND version = ?`,
		string(desiredJSON), formatWorkflowTime(req.OccurredAt), updatedBy, req.ExpectedVersion,
	)
	if err != nil {
		return ports.SafeSettingsRecord{}, MapSQLiteError(fmt.Errorf("update safe settings: %w", err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ports.SafeSettingsRecord{}, fmt.Errorf("read safe settings update result: %w", err)
	}
	if affected != 1 {
		return ports.SafeSettingsRecord{}, fmt.Errorf(
			"%w: safe settings expected version=%d", ports.ErrOptimisticConflict, req.ExpectedVersion,
		)
	}
	return loadSafeSettingsTx(ctx, r.tx)
}
