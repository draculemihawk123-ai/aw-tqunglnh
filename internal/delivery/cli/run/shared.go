package run

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// loadPrincipal resolves --principal-config into a validated
// config.LocalPrincipal — the SAME two-step (LoadLocalPrincipalFile then
// ValidateLocalPrincipal) `aw serve` itself performs (cmd/aw/serve.go),
// applied here per-invocation since a one-shot `aw` process has no
// long-lived startup phase separate from the command itself. This is
// deliberately duplicated (not exported from a shared location) in package
// noderun too — internal/delivery/httpapi/rundetail/fixture_test.go's own
// doc comment on why sibling packages duplicate a small helper rather than
// share one applies equally to two sibling leaf-command packages here.
func loadPrincipal(path string) (config.LocalPrincipal, error) {
	principal, err := config.LoadLocalPrincipalFile(path)
	if err != nil {
		return config.LocalPrincipal{}, err
	}
	if err := config.ValidateLocalPrincipal(principal); err != nil {
		return config.LocalPrincipal{}, err
	}
	return principal, nil
}

// loadWorkItemProjectID mirrors internal/delivery/httpapi/run's own
// loadWorkItemProjectID (start.go there) exactly: a read-only reload of the
// target WorkItem's own authoritative ProjectID, so this package (like that
// one) never trusts a caller-supplied project id for `run start` — the
// WorkItem's own row is the only source (contract point 3, "Không tin ID
// shape, payload hoặc projection... route reload authoritative target để
// suy Project/scope").
func loadWorkItemProjectID(ctx context.Context, uow ports.UnitOfWork, workItemID string) (string, error) {
	var projectID string
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		projectID = string(item.ProjectID)
		return nil
	})
	return projectID, err
}
