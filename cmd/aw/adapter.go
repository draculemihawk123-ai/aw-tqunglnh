package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/codex"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// The pre-V6 `aw adapter probe|register|list|show` handlers that used to
// live in this file (V2-07B) were retired by V6-15O: `aw adapter ...` is now
// routed to internal/delivery/cli/adapterbuild (V6-15F), the hardened
// CommandEnvelope leaf (see cli.go and oneshot.go). What remains here is the
// one helper other composition-root code still needs.

// newAgentExecutor constructs the real, already-built AgentExecutor (V0/V1)
// for providerKey pointed at executablePath, using the exact same
// construction pattern claude.New/codex.New already establish. `aw worker`
// and the one-shot resource router both build their live provider registry
// from it, so a provider is constructed in exactly one place.
//
// probeEnvironment (V9-05, gap G5) is the list of parent-environment variable
// NAMES the executable's capability probe — the `--version` spawn that
// agentregistry.New and every admission's drift check run — inherits: the
// process' own `--env-allowlist`. nil (what every caller passed before V9-05)
// keeps the probe's environment empty. It never affects what a task run
// inherits; that is formed per attempt from the pinned execution profile
// (internal/app/runtime AgentNodeExecutor).
func newAgentExecutor(providerKey, executablePath string, probeEnvironment []string, options ...agentProviderOption) (ports.AgentExecutor, error) {
	var settings agentProviderSettings
	for _, option := range options {
		option(&settings)
	}
	switch ports.ProviderKey(providerKey) {
	case ports.ProviderClaude:
		return claude.New(process.NewSupervisor(), claude.Config{
			Executable: executablePath, VersionInheritedEnvironment: probeEnvironment, PermissionMode: settings.claudePermissionMode,
			Effort: settings.claudeEffort, MaxBudgetUSD: settings.claudeMaxBudgetUSD,
		})
	case ports.ProviderCodex:
		return codex.New(process.NewSupervisor(), codex.Config{Executable: executablePath, VersionInheritedEnvironment: probeEnvironment})
	default:
		return nil, fmt.Errorf("adapter: unknown provider %q (want %q or %q)", providerKey, ports.ProviderClaude, ports.ProviderCodex)
	}
}

// agentProviderSettings are the per-provider settings of a live executor that
// are not part of its identity.
type agentProviderSettings struct {
	claudePermissionMode string
	claudeEffort         string
	claudeMaxBudgetUSD   float64
}

// agentProviderOption adjusts how newAgentExecutor builds a provider executor.
type agentProviderOption func(*agentProviderSettings)

// withClaudePermissionMode (V9-11, finding F1) sets the Claude CLI's
// --permission-mode for every task it runs. The default, no mode, makes a
// headless Claude refuse every file write in a worktree it has not been told to
// trust — and an `aw` worktree is new for every WorkItem, so it never is — so
// an agent that must change files needs "acceptEdits" (or another mode the
// operator chooses). An unknown value is refused when the executor is built
// (claude.New), at startup, not on the first task. It has no effect on a
// provider other than Claude.
func withClaudePermissionMode(mode string) agentProviderOption {
	return func(settings *agentProviderSettings) { settings.claudePermissionMode = strings.TrimSpace(mode) }
}

// withClaudeEffort (V9-13a, finding F4) sets the Claude CLI's --effort for every
// task. An unknown value is refused when the executor is built, at startup.
func withClaudeEffort(effort string) agentProviderOption {
	return func(settings *agentProviderSettings) { settings.claudeEffort = strings.TrimSpace(effort) }
}

// withClaudeMaxBudgetUSD (V9-13a, finding F4) sets the Claude CLI's
// --max-budget-usd: the most ONE attempt may spend. Zero is no ceiling.
func withClaudeMaxBudgetUSD(dollars float64) agentProviderOption {
	return func(settings *agentProviderSettings) { settings.claudeMaxBudgetUSD = dollars }
}

// probeProviderExecutable is `aw doctor`'s ProviderProbe (V9-05, gap G5): it
// runs the provider adapter's own capability probe — the same `--version`
// spawn with the same bounded timeout the worker runs at startup and at every
// admission — against executablePath, with exactly the variables named in
// inherited as the probe's inherited environment, and reports only whether it
// worked. A fresh adapter per call: nothing is registered, cached or pinned,
// and nothing but names goes in; a failure is the adapter's typed
// *ports.CapabilityProbeError, which the doctor turns into a stable code
// without printing any variable value or raw operating-system error.
func probeProviderExecutable(ctx context.Context, providerKey, executablePath string, inherited []string) error {
	executor, err := newAgentExecutor(providerKey, executablePath, inherited)
	if err != nil {
		return err
	}
	_, err = executor.Capabilities(ctx)
	return err
}
