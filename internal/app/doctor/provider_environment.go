package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// ProviderProbe runs a provider adapter's own capability probe — the bounded
// `--version` spawn the worker runs at startup and at every admission —
// against executable, with exactly the parent-environment variable NAMES in
// inherited as the probe process' inherited environment, and returns nil when
// the executable ran and printed a version. Only names go in and only an error
// comes out. A failure of the probe itself should be (or wrap) the adapter's
// typed *ports.CapabilityProbeError, which CheckProviderEnvironment turns into
// a stable code and a safe one-phrase reason.
//
// It is a function type, supplied by the composition root, because the real
// adapters live in internal/adapters and this package may not import them
// (internal/archtest).
type ProviderProbe func(ctx context.Context, provider, executable string, inherited []string) error

const (
	// ProviderEnvInsufficient is the stable machine-readable code a provider
	// environment check starts its Detail with (followed by a colon) when the
	// configured executable could not run its version probe in the
	// environment an agent would get from the worker (V9-05, gap G5).
	ProviderEnvInsufficient = "PROVIDER_ENV_INSUFFICIENT"
	// ProviderProbeError is the code when the probe could not be run at all
	// for a reason that is not the executable's own failure (the provider is
	// unknown to the composition root, the probe was not constructible).
	ProviderProbeError = "PROVIDER_PROBE_ERROR"
)

// CheckProviderEnvironment answers the question the existing
// "provider:<name>" check deliberately does not: not whether the configured
// executable EXISTS (it only observes a fingerprint), but whether it can RUN
// in the environment a worker would give an agent (V9-05, gap G5).
//
// An agent process is spawned with nothing but the variables named by both the
// AgentProfile's envAllowlist and the worker's --env-allowlist; the widest
// environment any profile can ever receive is therefore exactly the worker's
// --env-allowlist (inherited here). The check runs the provider's own version
// probe with that and nothing else:
//
//   - the probe works: HEALTHY, naming the variables it was run with;
//   - it fails: DEGRADED, Detail starts with "PROVIDER_ENV_INSUFFICIENT:" and
//     gives the typed reason (exit code, timeout, could not start, no output),
//     and Remediation names the allowlists to extend (the worker's, and the
//     profile's, because a name must be in both).
//
// It returns ok=false — "no result, say nothing" — when probe is nil, when no
// executable is configured, or when the configured path does not exist: all
// three are already (or deliberately not) reported by CheckProviderExecutable,
// and probing a file that is not there would only repeat that finding as a
// misleading environment problem.
//
// Nothing here ever prints a variable value or a raw operating-system error:
// the probe is given names, the failure is classified by its typed reason, and
// the text is this function's own.
func CheckProviderEnvironment(ctx context.Context, name, path string, inherited []string, probe ProviderProbe) (result CheckResult, ok bool) {
	if probe == nil || strings.TrimSpace(path) == "" {
		return CheckResult{}, false
	}
	if _, err := os.Stat(path); err != nil {
		return CheckResult{}, false
	}

	checkName := "provider_environment:" + name
	names := sortedUniqueNames(inherited)
	err := probe(ctx, name, path, names)
	if err == nil {
		return CheckResult{
			Name: checkName, Category: CategoryCapability, Status: StatusHealthy,
			Detail: fmt.Sprintf(
				"the %s executable ran its version probe in the environment an agent would get from this worker, which inherits only the variable names: %s",
				name, describeNames(names)),
		}, true
	}

	var probeErr *ports.CapabilityProbeError
	if !errors.As(err, &probeErr) {
		return CheckResult{
			Name: checkName, Category: CategoryCapability, Status: StatusDegraded,
			Detail:      ProviderProbeError + ": the version probe of the " + name + " executable could not be run",
			Remediation: fmt.Sprintf("check that %s is a provider this build supports (claude or codex) and that the executable path configured for it is right", name),
		}, true
	}
	return CheckResult{
		Name: checkName, Category: CategoryCapability, Status: StatusDegraded,
		Detail: fmt.Sprintf(
			"%s: the %s executable could not run its version probe (%s) in the environment an agent would get from this worker, which inherits only the variable names: %s",
			ProviderEnvInsufficient, name, describeProbeFailure(probeErr), describeNames(names)),
		Remediation: "Allow the variables the provider CLI needs — typically PATH and HOME, and USERPROFILE and SystemRoot on Windows — " +
			"in `aw worker --env-allowlist` (give `aw doctor` the same list with --env-allowlist or AW_ENV_ALLOWLIST), " +
			"and list the same names, spelled identically, in the AgentProfile's envAllowlist: an agent process inherits only the names that BOTH lists contain. " +
			"If the executable also fails when run by hand with those variables, the problem is the executable, not the allowlist.",
	}, true
}

// describeProbeFailure renders a typed probe failure as one safe phrase: the
// reason and, for a non-zero exit, its code — never the error's own text,
// which may carry a path or an operating-system message.
func describeProbeFailure(err *ports.CapabilityProbeError) string {
	switch err.Reason {
	case ports.CapabilityProbeStartFailed:
		return "the executable could not be started"
	case ports.CapabilityProbeNonZeroExit:
		return fmt.Sprintf("it exited with code %d", err.ExitCode)
	case ports.CapabilityProbeTimedOut:
		return "it did not answer within the probe's time limit"
	case ports.CapabilityProbeCancelled:
		return "the probe was cancelled"
	case ports.CapabilityProbeEmptyOutput:
		return "it exited normally but printed no version"
	default:
		return "reason " + string(err.Reason)
	}
}

func describeNames(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// sortedUniqueNames returns a sorted, deduplicated copy of names, so the probe
// and the text always see the same canonical list whatever order the operator
// typed it in.
func sortedUniqueNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(names))
	unique := make([]string, 0, len(names))
	for _, name := range names {
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		unique = append(unique, name)
	}
	sort.Strings(unique)
	return unique
}
