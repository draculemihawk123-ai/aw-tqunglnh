package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Load implements ports.ReceiptsRepository.
func (r receiptsRepository) Load(ctx context.Context, actor string, scope ports.CommandScope, idempotencyKey, commandType string) (ports.Receipt, bool, error) {
	var (
		requestHash string
		resultJSON  sql.NullString
		errorCode   sql.NullString
		createdAt   string
	)
	err := r.tx.QueryRowContext(ctx, `
SELECT request_hash, result_json, error_code, created_at
FROM command_receipts
WHERE actor = ? AND scope_key = ? AND idempotency_key = ? AND command_type = ?`,
		actor, scope.Key(), idempotencyKey, commandType,
	).Scan(&requestHash, &resultJSON, &errorCode, &createdAt)
	if err == sql.ErrNoRows {
		return ports.Receipt{}, false, nil
	}
	if err != nil {
		return ports.Receipt{}, false, MapSQLiteError(fmt.Errorf("load command receipt: %w", err))
	}
	createdAtTime, err := parseDBTime(createdAt)
	if err != nil {
		return ports.Receipt{}, false, fmt.Errorf("parse command receipt created_at: %w", err)
	}
	return ports.Receipt{
		Actor:          actor,
		Scope:          scope,
		IdempotencyKey: idempotencyKey,
		CommandType:    commandType,
		RequestHash:    requestHash,
		ResultJSON:     resultJSON.String,
		ErrorCode:      errorCode.String,
		CreatedAt:      createdAtTime,
	}, true, nil
}

// Record implements ports.ReceiptsRepository. It never classifies a
// SQLite error string to detect a duplicate key — it uses ON CONFLICT DO
// NOTHING and then checks RowsAffected, so a concurrent racing insert of
// the exact same receipt (the loser of a genuine idempotency race, not a
// caller bug) resolves as a no-op success rather than a spurious error,
// while a receipt already present with a different RequestHash is
// reported as ports.ErrReceiptConflict.
func (r receiptsRepository) Record(ctx context.Context, receipt ports.Receipt) error {
	result, err := r.tx.ExecContext(ctx, `
INSERT INTO command_receipts (actor, scope_key, idempotency_key, command_type, request_hash, result_json, error_code, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (actor, scope_key, idempotency_key, command_type) DO NOTHING`,
		receipt.Actor, receipt.Scope.Key(), receipt.IdempotencyKey, receipt.CommandType,
		receipt.RequestHash, nullableString(receipt.ResultJSON), nullableString(receipt.ErrorCode),
		receipt.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return MapSQLiteError(fmt.Errorf("record command receipt: %w", err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read record command receipt result: %w", err)
	}
	if affected == 1 {
		return nil
	}

	existing, found, err := r.Load(ctx, receipt.Actor, receipt.Scope, receipt.IdempotencyKey, receipt.CommandType)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("record command receipt: insert reported no rows affected but no existing receipt was found")
	}
	if existing.RequestHash != receipt.RequestHash {
		return ports.ErrReceiptConflict
	}
	return nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
