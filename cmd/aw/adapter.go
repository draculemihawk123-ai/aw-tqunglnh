package main

import (
	"fmt"

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
func newAgentExecutor(providerKey, executablePath string) (ports.AgentExecutor, error) {
	switch ports.ProviderKey(providerKey) {
	case ports.ProviderClaude:
		return claude.New(process.NewSupervisor(), claude.Config{Executable: executablePath})
	case ports.ProviderCodex:
		return codex.New(process.NewSupervisor(), codex.Config{Executable: executablePath})
	default:
		return nil, fmt.Errorf("adapter: unknown provider %q (want %q or %q)", providerKey, ports.ProviderClaude, ports.ProviderCodex)
	}
}
