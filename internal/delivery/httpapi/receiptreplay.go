package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// LookupReceipt is V6-02's own one read-only fast-path check: whether a
// receipt already exists for (actor, scope, idempotencyKey, commandType).
// It never writes anything — a plain uow.WithReadOnly call — matching
// this whole package's own "middleware không ghi/cache receipt" rule
// (commandenvelope.go's own doc comment; enforced for real by
// TestDeliveryNeverWritesOrRecordsAReceipt, commandenvelope_test.go). A
// caller that finds nothing here still dispatches to the real application
// command exactly as if this function did not exist — that command's own
// transaction is the one and only place a receipt is ever authoritatively
// checked-then-written (V6-02's own "command transaction recheck
// receipt"); this is purely a latency/UX optimization for the common
// replay case, never a correctness dependency.
func LookupReceipt(ctx context.Context, uow ports.UnitOfWork, actor string, scope ports.CommandScope, idempotencyKey, commandType string) (ports.Receipt, bool, error) {
	var receipt ports.Receipt
	var found bool
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, ok, loadErr := tx.Receipts().Load(ctx, actor, scope, idempotencyKey, commandType)
		receipt, found = loaded, ok
		return loadErr
	})
	if err != nil {
		return ports.Receipt{}, false, fmt.Errorf("httpapi: lookup receipt: %w", err)
	}
	return receipt, found, nil
}

// ErrReceiptHashConflict is ReconcileReceipt's own typed result for the
// "same idempotency key, different payload" case — a caller maps this to
// a 409 Conflict distinct from a genuine resync/version conflict.
var ErrReceiptHashConflict = errors.New("httpapi: idempotency key reused with a different request")

// ReconcileReceipt compares an already-looked-up receipt (see
// LookupReceipt) against the semantic hash of the CURRENT request: equal
// hashes means a true replay (the caller should return the receipt's own
// stored outcome verbatim, see WriteReceiptReplay); different hashes is
// ErrReceiptHashConflict (V6-02's own "different body conflict trước
// I/O" — this check runs before any real work, exactly like the receipt
// lookup itself).
func ReconcileReceipt(receipt ports.Receipt, requestHash string) error {
	if receipt.RequestHash != requestHash {
		return ErrReceiptHashConflict
	}
	return nil
}

// WriteReceiptReplay writes receipt's own stored outcome verbatim — a
// prior success's ResultJSON with 200, or a prior failure's ErrorCode
// mapped through StatusForAppErrorCode with a generic message (the
// receipt itself never stored the original human-readable message, only
// the code, so replay is best-effort on message text but exact on
// status/code — a client's own automated retry logic, which is what this
// path exists for, keys on the code, never the prose). This is
// deliberately "wins over ETag/state drift" (V6-02's own "Thực hiện"
// line): a real replay never re-validates If-Match/current version — the
// command already truly happened once, so a caller asking again with the
// same key gets the same answer regardless of what the resource's own
// state has done since. Current authorization is still the caller's own
// responsibility, checked before this function is ever reached — see
// this package's own doc comment for the full flow ordering.
func WriteReceiptReplay(w http.ResponseWriter, receipt ports.Receipt) {
	if receipt.ErrorCode != "" {
		status, code := StatusForAppErrorCode(apperror.Code(receipt.ErrorCode))
		WriteError(w, status, code, "replayed: command previously failed", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if receipt.ResultJSON == "" {
		_, _ = w.Write([]byte("{}"))
		return
	}
	_, _ = w.Write([]byte(receipt.ResultJSON))
}

// EncodeResult deterministically writes a fresh (non-replayed) command
// result as the HTTP response body plus, when etag is non-empty, the
// ETag response header — V6-02's own "HTTP deterministically encode
// stored application result/ETag/operation reference" line. status is
// the caller's own choice (201 for a create, 200 for an update) since
// this package does not know which a given command represents.
func EncodeResult(w http.ResponseWriter, status int, result any, etag string) error {
	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("httpapi: marshal result: %w", err)
	}
	w.Header().Set("Content-Type", "application/json")
	if etag != "" {
		w.Header().Set(ETagHeader, etag)
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
	return nil
}
