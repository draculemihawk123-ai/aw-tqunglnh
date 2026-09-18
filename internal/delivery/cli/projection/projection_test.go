package projection_test

import (
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// TestDescriptorsRegistered proves this leaf's own init() registered all
// three `aw projection ...` commands into cli.Default with the real HTTP
// operationIds V6-09B already established.
func TestDescriptorsRegistered(t *testing.T) {
	want := map[string]string{
		"projection status":         "getProjectionStatus",
		"projection rebuild":        "requestProjectionRebuild",
		"projection rebuild-status": "getProjectionRebuildOperationStatus",
	}
	found := map[string]bool{}
	for _, d := range cli.Default.All() {
		key := strings.Join(d.Path, " ")
		op, ok := want[key]
		if !ok {
			continue
		}
		found[key] = true
		if d.HTTPOperationID != op {
			t.Errorf("descriptor %q HTTPOperationID = %q, want %q", key, d.HTTPOperationID, op)
		}
		if d.Scope != cli.ScopeProject {
			t.Errorf("descriptor %q Scope = %q, want ScopeProject", key, d.Scope)
		}
	}
	for key := range want {
		if !found[key] {
			t.Errorf("descriptor %q was not registered", key)
		}
	}
}
