package decision_test

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	_ "github.com/taQuangLing/agent-workflow/internal/delivery/cli/decision"
)

// TestDecisionPackage_RegistersEveryDescriptorInDefault proves this
// package's own two init() registrations (approval.go, wait.go) all landed
// in the shared internal/delivery/cli.Default registry — mirrors
// internal/delivery/cli/run's own identically-named test exactly.
func TestDecisionPackage_RegistersEveryDescriptorInDefault(t *testing.T) {
	want := map[string]string{
		"approval resolve|PROJECT": "ResolveApproval",
		"wait signal|PROJECT":      "SignalWait",
	}

	got := map[string]string{}
	for _, d := range cli.All() {
		key := d.Path[0]
		for _, seg := range d.Path[1:] {
			key += " " + seg
		}
		key += "|" + string(d.Scope)
		got[key] = d.AppOperation
	}

	for key, appOp := range want {
		gotOp, ok := got[key]
		if !ok {
			t.Errorf("descriptor %q not found in cli.Default", key)
			continue
		}
		if gotOp != appOp {
			t.Errorf("descriptor %q AppOperation = %q, want %q", key, gotOp, appOp)
		}
	}
}
