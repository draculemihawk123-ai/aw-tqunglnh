package catalog

import (
	"flag"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Command-type constants for the four mutating leaves in this package.
// Each value is byte-for-byte identical to the commandType string literal
// internal/delivery/httpapi/catalog's own handlers already pass to
// beginMutation (command.go) — this is not a coincidence but a hard
// requirement: cli.Dispatch/cli.BuildEnvelope reuse the exact same
// httpapi.SemanticHash/LookupReceipt/ReconcileReceipt replay authority
// internal/delivery/httpapi/catalog itself uses (internal/delivery/cli's
// own doc.go), and command_receipts is keyed in part on this command_type
// string — an HTTP call and a CLI call for what should be "the same
// command" must hash and replay identically, which only holds if both
// delivery mechanisms use the identical literal.
const (
	commandTypeCreateProject        = "CreateProject"
	commandTypeRegisterRepository   = "RegisterRepository"
	commandTypeRetryRepositoryProbe = "RetryRepositoryProbe"
	commandTypeAssignComponentPack  = "AssignComponentPack"
)

// AppOperation constants for this package's six read-only leaves —
// descriptor.go's own "fourth column of ADR-028's eventual inventory".
// Each name mirrors the internal/app/catalog query function it calls,
// except appOpRepositoryOnboarding: `repository onboarding` composes TWO
// application queries (GetRepository + ListRepositoryProbeAttempts) into
// one combined view, the exact same composition
// internal/delivery/httpapi/catalog/repository.go's own
// getRepositoryOnboarding already performs for HTTP — there is no single
// application function name to record here, so this constant names the
// composed operation itself rather than picking one of its two
// constituents arbitrarily.
const (
	appOpListProjects                 = "ListProjects"
	appOpGetProject                   = "GetProject"
	appOpListProjectRepositories      = "ListProjectRepositories"
	appOpRepositoryOnboarding         = "RepositoryOnboarding"
	appOpListComponents               = "ListComponents"
	appOpListComponentPackAssignments = "ListComponentPackAssignments"
)

// loadPrincipal resolves the acting principal exactly the way `aw serve`'s
// own --principal-config flag already does (cmd/aw/serve.go): load the
// trusted JSON config file (or the local-operator/[operator] default when
// path is empty/missing/omits localPrincipal), then validate its shape.
// This is the ONLY place any Run* function in this package ever resolves a
// principal from — never a per-invocation --actor/--role flag, per
// ADR-028 and cli.BindPrincipalFlag's own doc comment.
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

// usageErrorf builds a cli.UsageError (mapped to cli.ExitUsage by a future
// composition root's own cli.ExitCodeFor) from a formatted message — every
// bad-invocation condition in this package (wrong argument count, a
// required flag left empty) returns one of these rather than a bare error,
// so it is never confused with a real domain/persistence failure.
func usageErrorf(format string, args ...any) error {
	return cli.UsageError{Err: fmt.Errorf(format, args...)}
}

// parseFlags parses args into fs, writing its own usage/error text to
// whatever fs.SetOutput was already given, and wraps a parse failure as a
// cli.UsageError — the one place every Run* function's own flag handling
// goes through, so that wrapping is never forgotten by an individual
// leaf. flag.ErrHelp (from -h/--help) is returned unwrapped so a future
// composition root can keep treating it as cli.ExitSuccess, the same
// convention cmd/aw/cli.go's own run() already follows.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return err
		}
		return cli.UsageError{Err: err}
	}
	return nil
}
