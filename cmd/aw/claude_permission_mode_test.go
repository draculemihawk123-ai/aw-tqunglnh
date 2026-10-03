package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

// V9-11 (live finding F1): `aw worker --claude-permission-mode` is how an
// operator lets a headless Claude change files in an aw worktree, which it
// never trusts. The mode reaches claude.Config.PermissionMode — what puts
// `--permission-mode <mode>` on the CLI's command line is covered by the
// provider contract test — and a value the adapter does not know is refused when
// the executor is built, at startup, rather than on the first task.

func TestNewAgentExecutor_ClaudePermissionMode(t *testing.T) {
	executable := os.Args[0]
	for _, mode := range []string{"", "acceptEdits", "dontAsk", "plan", "bypassPermissions"} {
		if _, err := newAgentExecutor("claude", executable, nil, withClaudePermissionMode(mode)); err != nil {
			t.Errorf("mode %q was refused: %v", mode, err)
		}
	}
	_, err := newAgentExecutor("claude", executable, nil, withClaudePermissionMode("allowEverything"))
	if err == nil || !strings.Contains(err.Error(), "permission mode") {
		t.Fatalf("an unknown permission mode = %v, want an error naming the permission mode", err)
	}
	// Surrounding whitespace from a flag value is not part of the mode.
	if _, err := newAgentExecutor("claude", executable, nil, withClaudePermissionMode("  acceptEdits ")); err != nil {
		t.Errorf("a padded mode was refused: %v", err)
	}
	// A provider other than Claude ignores the setting.
	if _, err := newAgentExecutor("codex", executable, nil, withClaudePermissionMode("allowEverything")); err != nil {
		t.Errorf("the Claude permission mode broke the Codex executor: %v", err)
	}
}

func TestNewWorkerAgentRegistry_RefusesAnUnknownClaudePermissionModeAtStartup(t *testing.T) {
	_, err := newWorkerAgentRegistry(context.Background(), os.Args[0], "", nil, withClaudePermissionMode("allowEverything"))
	if err == nil || !strings.Contains(err.Error(), "permission mode") {
		t.Fatalf("newWorkerAgentRegistry = %v, want a startup error naming the permission mode", err)
	}
}
