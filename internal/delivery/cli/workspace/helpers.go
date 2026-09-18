package workspace

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	appinspection "github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// usageErrorf builds a cli.UsageError from a formatted message — every
// bad-invocation condition in this package (wrong argument count, a
// required flag left empty) returns one of these rather than a bare error,
// mirroring internal/delivery/cli/workitem/helpers.go's own identical
// helper.
func usageErrorf(format string, args ...any) error {
	return cli.UsageError{Err: fmt.Errorf(format, args...)}
}

// parseFlags parses args into fs and wraps a parse failure as a
// cli.UsageError — mirroring internal/delivery/cli/workitem/helpers.go's
// own identical helper. flag.ErrHelp (from -h/--help) is returned
// unwrapped so a future composition root can keep treating it as
// cli.ExitSuccess.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return err
		}
		return cli.UsageError{Err: err}
	}
	return nil
}

// requireProjectID validates a --project-id flag value non-empty, returning
// a cli.UsageError naming the flag when it is blank — every subcommand in
// this package is project-scoped.
func requireProjectID(projectID string) error {
	if strings.TrimSpace(projectID) == "" {
		return usageErrorf("--project-id is required")
	}
	return nil
}

// loadPrincipal resolves --principal-config into a validated
// config.LocalPrincipal — the same two-step (LoadLocalPrincipalFile then
// ValidateLocalPrincipal) `aw serve` itself performs (cmd/aw/serve.go),
// applied here per-invocation. Deliberately duplicated rather than
// exported from a shared location — internal/delivery/cli/run/shared.go's
// own doc comment on loadPrincipal explains why sibling leaf-command
// packages duplicate this small helper rather than share one.
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

// revisionResult is the CLI's own delivery-owned wire shape for one
// workspace.Revision — mirrors
// internal/delivery/httpapi/workspaceinspection/dto.go's own
// revisionResponse field-for-field, duplicated rather than imported since
// that type is unexported in a different delivery package (the same "two
// independent delivery mechanisms each own their own wire vocabulary"
// convention V6-02A already established, quoted in
// internal/delivery/cli/evidence/doc.go).
type revisionResult struct {
	RepositoryID        string `json:"repositoryId"`
	VCSObjectID         string `json:"vcsObjectId"`
	WorkspaceGeneration uint64 `json:"workspaceGeneration"`
}

func toRevisionResult(r workspace.Revision) revisionResult {
	return revisionResult{
		RepositoryID: string(r.RepositoryID), VCSObjectID: r.VCSObjectID, WorkspaceGeneration: r.WorkspaceGeneration,
	}
}

// mapInspectionQueryError maps every error
// appinspection.Queries.GetSource/GetDiff/GetRepositoryLog can return onto
// a safe, opaque CLI error — mirrors
// internal/delivery/httpapi/workspaceinspection/errors.go's own
// writeQueryError EXACTLY, restated for this package's own error-return
// shape instead of an HTTP response write.
//
// Only THREE error conditions are ever named here by their own concrete
// sentinel: appinspection.ErrScopeMismatch, appinspection.ErrWorkspaceNotReady
// and ports.ErrPersistenceNotFound — every other error these three queries
// can return (internal/adapters/gitworktree's own ErrInvalidSpec,
// ErrPathNotFound, ErrUnsupportedEntry, ErrWorkspaceReleased,
// ErrWorkspaceNotFound, ErrInvalidHandle, ErrGit, ...) is structurally
// UNNAMEABLE here: naming any of them would require importing
// internal/adapters/gitworktree, which this package's own
// internal/archtest/cli_workspace_boundary_test.go forbids outright — see
// this package's own doc.go for the full reasoning.
//
// The default branch below is therefore a deliberate, single OPAQUE
// message for every one of those adapter-level conditions, not an
// oversight — a path-traversal attempt, an unauthorized/stale revision, a
// binary/directory/symlink/submodule rejection, and a non-ancestor log
// cursor are all indistinguishable to a caller of this function, exactly
// like writeQueryError's own identical default branch. The returned error
// is always a FRESH, generic error — never err itself re-wrapped — so a
// future composition root that prints a returned error's own .Error() text
// straight to stderr (internal/delivery/cli/evidence/artifact.go's own
// sanitizeStoreError doc comment describes the identical discipline for
// ports.ArtifactStore's own path-bearing errors) can never leak an
// adapter-internal detail this function was built specifically to hide.
func mapInspectionQueryError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, appinspection.ErrScopeMismatch), errors.Is(err, ports.ErrPersistenceNotFound):
		return errors.New("workspace: repository workspace not found")
	case errors.Is(err, appinspection.ErrWorkspaceNotReady):
		return errors.New("workspace: repository workspace is not currently READY for inspection")
	default:
		return errors.New("workspace: the request names an invalid, unauthorized, or unreadable revision, path, or cursor")
	}
}

// requireScopeFlags validates the four WorkspaceScope identifiers every
// `repository-workspace source|diff|log` subcommand requires — mirrors
// internal/delivery/httpapi/workspaceinspection/params.go's own
// requireQueryParam discipline: repositoryId/workspaceSetId are always
// REQUIRED, never inferred from repositoryWorkspaceId alone (this task's
// own brief: "your CLI must require the same four scope IDs as flags/args,
// never let a bare ID infer the rest").
func requireScopeFlags(repositoryWorkspaceID, projectID, repositoryID, workspaceSetID string) error {
	if strings.TrimSpace(repositoryWorkspaceID) == "" {
		return usageErrorf("<repositoryWorkspaceId> argument is required")
	}
	if strings.TrimSpace(projectID) == "" {
		return usageErrorf("--project-id is required")
	}
	if strings.TrimSpace(repositoryID) == "" {
		return usageErrorf("--repository-id is required")
	}
	if strings.TrimSpace(workspaceSetID) == "" {
		return usageErrorf("--workspace-set-id is required")
	}
	return nil
}
