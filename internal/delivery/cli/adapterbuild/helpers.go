package adapterbuild

import (
	"flag"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// loadPrincipal resolves the acting principal exactly the way
// internal/delivery/cli/definitions and internal/delivery/cli/catalog's own
// identically named loadPrincipal (and `aw serve`'s own --principal-config
// flag, cmd/aw/serve.go) already do: load the trusted JSON config file (or
// the local-operator/[operator] default when path is empty/missing/omits
// localPrincipal), then validate its shape. This is the ONLY place any
// Run* function in this package ever resolves a principal from — never a
// per-invocation --actor/--role flag, per ADR-028 and
// cli.BindPrincipalFlag's own doc comment.
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
// composition root's own cli.ExitCodeFor) from a formatted message —
// mirrors internal/delivery/cli/definitions and internal/delivery/cli/
// catalog's own identical usageErrorf exactly.
func usageErrorf(format string, args ...any) error {
	return cli.UsageError{Err: fmt.Errorf(format, args...)}
}

// parseFlags mirrors internal/delivery/cli/definitions and
// internal/delivery/cli/catalog's own identical parseFlags exactly: the one
// place every Run* function's own flag handling goes through, wrapping a
// parse failure as a cli.UsageError and passing flag.ErrHelp through
// unwrapped.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return err
		}
		return cli.UsageError{Err: err}
	}
	return nil
}

// capabilityManifestFlags holds the bound flag pointers for a
// domainadapterbuild.CapabilityManifest — shared by `adapter probe` and
// `adapter register` alike: RegisterRequest.CapabilityManifest is
// re-supplied by the caller (not read back off the token) so
// RegisterAdapterBuild can re-derive its hash and compare against what the
// token bound, the same TOCTOU-closing re-measurement principle
// appadapterbuild.RegisterRequest's own doc comment documents for the
// executable file — see internal/app/adapterbuild/commands.go.
type capabilityManifestFlags struct {
	supportsStart  *bool
	supportsResume *bool
	supportsCancel *bool
	eventKinds     *string
}

// bindCapabilityManifestFlags registers --supports-start/--supports-resume/
// --supports-cancel/--canonical-event-kinds on fs. --supports-start
// defaults to false: domainadapterbuild.ValidateCapabilityManifest requires
// it true, so an operator who omits the flag gets a clear usage error
// rather than a silently accepted, useless manifest.
func bindCapabilityManifestFlags(fs *flag.FlagSet) capabilityManifestFlags {
	return capabilityManifestFlags{
		supportsStart:  fs.Bool("supports-start", false, "this build supports Start (required — must be set true)"),
		supportsResume: fs.Bool("supports-resume", false, "this build supports Resume"),
		supportsCancel: fs.Bool("supports-cancel", false, "this build supports Cancel"),
		eventKinds:     fs.String("canonical-event-kinds", "", "comma-separated list of canonical event kinds this build emits (e.g. TEXT_DELTA,TOOL_CALL)"),
	}
}

// manifest builds the domainadapterbuild.CapabilityManifest f's own bound
// flags describe. An empty --canonical-event-kinds produces a nil slice
// (never a single empty-string entry), so ValidateCapabilityManifest's own
// "no empty ... entry" rule is never spuriously tripped by an operator who
// simply omitted the flag.
func (f capabilityManifestFlags) manifest() domainadapterbuild.CapabilityManifest {
	var kinds []string
	if trimmed := strings.TrimSpace(*f.eventKinds); trimmed != "" {
		for _, kind := range strings.Split(trimmed, ",") {
			kind = strings.TrimSpace(kind)
			if kind != "" {
				kinds = append(kinds, kind)
			}
		}
	}
	return domainadapterbuild.CapabilityManifest{
		SupportsStart: *f.supportsStart, SupportsResume: *f.supportsResume, SupportsCancel: *f.supportsCancel,
		CanonicalEventKinds: kinds,
	}
}
