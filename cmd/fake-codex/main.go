// Command fake-codex plays the Codex CLI's wire protocol for the
// agentkit-spike acceptance suite, driven by the same
// AGENTKIT_HELPER_MODE/AGENTKIT_CAPTURE_PATH environment contract the real
// codex.Adapter already sets on every child process it spawns (see
// internal/adapters/providers/codex). It shares its entire fixture behavior
// with contract_test.go's TestProviderHelperProcess via
// providers.RunFakeProviderCLI, so a `go test` run and a standalone
// acceptance-suite run can never observe two different fake protocols.
package main

import (
	"os"

	"github.com/taQuangLing/agent-workflow/internal/adapters/providers"
)

func main() {
	code := providers.RunFakeProviderCLI(
		"codex", os.Args[1:],
		os.Getenv("AGENTKIT_HELPER_MODE"), os.Getenv("AGENTKIT_CAPTURE_PATH"),
		os.Stdin, os.Stdout,
	)
	os.Exit(code)
}
