// Package safesettings is V6-10G's own application layer
// (docs/design/08-v6-api-projections.md V6-10G; ADR-016, ADR-017, ADR-025,
// ADR-028): GetSafeSettings/UpdateSafeSettings — the two installation-scoped
// public operations ADR-025's own command-scope table names explicitly
// ("safe-settings mutation" as an installation Command, "safe-settings
// read" as an installation Query) — plus the startup precedence/masking
// resolution (startup.go) that turns a persisted desired document into
// what a NEXT restart would actually run with. See internal/domain/safesettings
// for the desired document's own shape/validation, and
// internal/app/ports/safesettings.go for the SQLite-facing port this
// package drives.
package safesettings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/safesettings"
)

// ErrNotInstallationScoped is returned by UpdateSafeSettings when cmd.Scope
// is not the installation scope — ADR-025's own "Handler MUST validate
// scope khớp command type": safe-settings mutation is one of the explicitly
// enumerated installation-scoped commands, never project-scoped.
var ErrNotInstallationScoped = errors.New("safesettings: UpdateSafeSettings is installation-scoped; cmd.Scope must be ports.InstallationScope()")

// SafeSettingsResult is what GetSafeSettings/UpdateSafeSettings return (and
// what a replayed command-receipt reconstructs) — V6-10G's own "Response
// has desired/effective/version/restartRequired/maskedByStartupSource"
// line. Desired is the exact persisted document; RestartRequired is always
// true once Version > 1 for any field that is not yet the zero/never
// -configured document (Alpha's own "All changes are restart-required" —
// there is no hot-reload path at all, so this is a static fact of the
// document having ever been set, not a computed diff against a live
// process). Effective/per-field masking is a SEPARATE, richer picture
// (startup.go's own Effective type) a caller resolves on demand by also
// supplying the non-SQLite startup layers — GetSafeSettings alone only
// proves what is PERSISTED, never what a live process is actually running
// with (V6-10G's own "no live mutation of immutable process config": this
// package has no access to, and must never guess at, another process's own
// resolved config).
type SafeSettingsResult struct {
	Desired         safesettings.SafeSettings `json:"desired"`
	Version         uint64                    `json:"version"`
	UpdatedAt       time.Time                 `json:"updatedAt"`
	UpdatedBy       string                    `json:"updatedBy"`
	RestartRequired bool                      `json:"restartRequired"`
}

func resultFromRecord(record ports.SafeSettingsRecord) SafeSettingsResult {
	return SafeSettingsResult{
		Desired: record.Desired, Version: record.Version, UpdatedAt: record.UpdatedAt, UpdatedBy: record.UpdatedBy,
		RestartRequired: !record.Desired.IsZero(),
	}
}

// GetSafeSettings is the installation-scoped query (ADR-025) that returns
// the currently PERSISTED desired document — a plain read, no
// CommandEnvelope/receipt (mirroring internal/app/adapterbuild.GetAdapterBuild's
// own "a query needs no idempotency ledger" precedent): a second identical
// call is naturally idempotent because it performs no write.
func GetSafeSettings(ctx context.Context, uow ports.UnitOfWork) (SafeSettingsResult, error) {
	var result SafeSettingsResult
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		record, err := tx.SafeSettings().Get(ctx)
		if err != nil {
			return err
		}
		result = resultFromRecord(record)
		return nil
	})
	return result, err
}

// UpdateSafeSettingsRequest is what a caller supplies to UpdateSafeSettings.
// DesiredJSON is the FULL desired document as raw JSON — never a per-field
// patch (V6-10G's own "Store full desired document + version") — decoded
// strictly (safesettings.SafeSettings' own UnmarshalJSON rejects any field
// outside the closed 7-field allowlist, this task's own "unknown/
// startup-security field" Verify line). cmd.ExpectedVersion (already a
// field on ports.Command, the same convention SealReleaseSetRequest's own
// doc comment establishes) is the CAS fence against a stale caller.
type UpdateSafeSettingsRequest struct {
	DesiredJSON json.RawMessage
}

// UpdateSafeSettings is the installation-scoped mutation (ADR-025): strict-
// decodes and validates req.DesiredJSON, then performs the CAS full-
// document replacement, appending a registered SAFE_SETTINGS_UPDATED v1
// event and a command receipt in the same transaction (GC-INV-15/35) —
// mirroring internal/app/work.SealReleaseSet's own
// replay-check/mutate/event/receipt shape exactly. Follows V1-06's
// idempotent-command contract: a retry with the same IdempotencyKey and
// RequestHash replays the first call's result; the same key with a
// different RequestHash is ports.ErrReceiptConflict.
//
// This never touches, and has no way to touch, the calling process's own
// internal/app/config.Config — it only ever persists a desired document a
// FUTURE restart's own startup merge (startup.go's own ResolveEffective)
// will read. "restartRequired" in the returned SafeSettingsResult is
// therefore always true for any non-zero document: Alpha has no hot-reload
// path at all (V6-10G's own Không làm: "no ... live mutation of immutable
// process config").
func UpdateSafeSettings(
	ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req UpdateSafeSettingsRequest,
) (SafeSettingsResult, error) {
	if !cmd.Scope.IsInstallation() {
		return SafeSettingsResult{}, ErrNotInstallationScoped
	}

	var desired safesettings.SafeSettings
	if err := json.Unmarshal(req.DesiredJSON, &desired); err != nil {
		return SafeSettingsResult{}, fmt.Errorf("safesettings: decode desired document: %w", err)
	}
	if err := safesettings.Validate(desired); err != nil {
		return SafeSettingsResult{}, err
	}

	var result SafeSettingsResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		existingReceipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if found {
			if existingReceipt.RequestHash != cmd.RequestHash {
				return ports.ErrReceiptConflict
			}
			return json.Unmarshal([]byte(existingReceipt.ResultJSON), &result)
		}

		updated, err := tx.SafeSettings().Update(ctx, ports.UpdateSafeSettingsRequest{
			Desired: desired, ExpectedVersion: cmd.ExpectedVersion, UpdatedBy: cmd.Actor, OccurredAt: cmd.RequestedAt,
		})
		if err != nil {
			return err
		}
		result = resultFromRecord(updated)

		eventID := ids.NewID()
		eventPayload, err := json.Marshal(safeSettingsUpdatedEventPayload{
			ManagedWorkspaceRoot:     desired.ManagedWorkspaceRoot,
			ManagedArtifactRoot:      desired.ManagedArtifactRoot,
			EvidenceRetentionSeconds: int64(desired.EvidenceRetention.Seconds()),
			ProcessOutputLimit:       desired.ProcessOutputLimit,
			ProviderExecutablePath:   desired.ProviderExecutablePath,
			ProviderDefaultModel:     desired.ProviderDefaultModel,
			ProviderCredentialRef:    desired.ProviderCredentialRef,
			Version:                  updated.Version,
			UpdatedBy:                cmd.Actor,
		})
		if err != nil {
			return fmt.Errorf("marshal %s payload: %w", SafeSettingsUpdatedEventType, err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: eventID, AggregateType: "SafeSettings", AggregateID: "singleton", Sequence: int64(updated.Version),
			EventType: SafeSettingsUpdatedEventType, SchemaVersion: SafeSettingsUpdatedSchemaVersion,
			PayloadJSON: string(eventPayload), CorrelationID: cmd.CorrelationID, CausationID: cmd.ID,
			CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		resultJSON, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal receipt result: %w", err)
		}
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
			CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON),
			CreatedAt: cmd.RequestedAt,
		})
	})
	return result, err
}
