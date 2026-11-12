// Package manifestpublish is the application side of V12-21 (ADR-056): reading whether a project has a definition manifest
// configured and running the kit's publisher over it. The publisher itself (kit/scripts/aw-publish.py) stays in the kit; a
// ports.ManifestPublisher runs it. HTTP (getDefinitionManifest, runDefinitionManifest) and the CLI (aw definition manifest-status,
// manifest-run) both call these two functions.
package manifestpublish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// ErrInvalidMode: the mode is neither CHECK nor PUBLISH.
var ErrInvalidMode = errors.New("manifestpublish: mode must be CHECK or PUBLISH")

// Status reads the project's manifest configuration and its last run. A nil publisher means none is configured.
func Status(publisher ports.ManifestPublisher, projectID string) ports.ManifestStatus {
	if publisher == nil {
		return ports.ManifestStatus{ProjectID: projectID}
	}
	return publisher.ManifestStatus(projectID)
}

// Run runs the kit's publisher over the project's manifest in mode (CHECK or PUBLISH).
func Run(ctx context.Context, publisher ports.ManifestPublisher, projectID, mode string) (ports.ManifestRun, error) {
	if mode != ports.ManifestCheck && mode != ports.ManifestPublish {
		return ports.ManifestRun{}, fmt.Errorf("%w (got %q)", ErrInvalidMode, mode)
	}
	if publisher == nil {
		return ports.ManifestRun{}, ports.ErrManifestNotConfigured
	}
	return publisher.RunManifest(ctx, projectID, mode)
}

// RunCommand is Run as an idempotent command: the same cmd (idempotency key and request hash) replays the first run's result
// instead of running the publisher again; the same key with another request is ports.ErrReceiptConflict.
func RunCommand(ctx context.Context, uow ports.UnitOfWork, publisher ports.ManifestPublisher, cmd ports.Command, projectID, mode string) (ports.ManifestRun, bool, error) {
	var replayed *ports.ManifestRun
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().GetProject(ctx, projectID); err != nil {
			return err
		}
		receipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil || !found {
			return err
		}
		if receipt.RequestHash != cmd.RequestHash {
			return ports.ErrReceiptConflict
		}
		var run ports.ManifestRun
		if err := json.Unmarshal([]byte(receipt.ResultJSON), &run); err != nil {
			return err
		}
		replayed = &run
		return nil
	}); err != nil {
		return ports.ManifestRun{}, false, err
	}
	if replayed != nil {
		return *replayed, true, nil
	}
	run, err := Run(ctx, publisher, projectID, mode)
	if err != nil {
		return ports.ManifestRun{}, false, err
	}
	resultJSON, err := json.Marshal(run)
	if err != nil {
		return ports.ManifestRun{}, false, err
	}
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey, CommandType: cmd.Type,
			RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON), CreatedAt: cmd.RequestedAt,
		})
	})
	return run, false, err
}
