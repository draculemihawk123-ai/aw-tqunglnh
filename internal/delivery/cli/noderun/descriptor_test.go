package noderun_test

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	_ "github.com/taQuangLing/agent-workflow/internal/delivery/cli/noderun"
)

// TestNodeRunPackage_RegistersDescriptorInDefault mirrors
// internal/delivery/cli/run's own identical descriptor_test.go — proves
// `node-run retry-blocked` registered itself into the shared
// internal/delivery/cli.Default registry via this package's own init().
func TestNodeRunPackage_RegistersDescriptorInDefault(t *testing.T) {
	for _, d := range cli.All() {
		if len(d.Path) == 2 && d.Path[0] == "node-run" && d.Path[1] == "retry-blocked" {
			if d.Scope != cli.ScopeProject {
				t.Errorf("node-run retry-blocked Scope = %q, want PROJECT", d.Scope)
			}
			if d.AppOperation != "RetryBlockedActivation" {
				t.Errorf("node-run retry-blocked AppOperation = %q, want RetryBlockedActivation", d.AppOperation)
			}
			if d.HTTPOperationID != "retryBlockedActivation" {
				t.Errorf("node-run retry-blocked HTTPOperationID = %q, want retryBlockedActivation", d.HTTPOperationID)
			}
			return
		}
	}
	t.Fatal("descriptor {node-run, retry-blocked} not found in cli.Default")
}
