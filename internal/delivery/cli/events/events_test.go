package events_test

import (
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// TestDescriptorRegistered proves this leaf's own init() registered
// `aw events watch` into cli.Default with the real HTTP operationId V6-11
// already established (watchProjectEvents).
func TestDescriptorRegistered(t *testing.T) {
	const wantPath = "events watch"
	const wantOp = "watchProjectEvents"
	found := false
	for _, d := range cli.Default.All() {
		if strings.Join(d.Path, " ") != wantPath {
			continue
		}
		found = true
		if d.HTTPOperationID != wantOp {
			t.Errorf("descriptor %q HTTPOperationID = %q, want %q", wantPath, d.HTTPOperationID, wantOp)
		}
		if d.Scope != cli.ScopeProject {
			t.Errorf("descriptor %q Scope = %q, want ScopeProject", wantPath, d.Scope)
		}
	}
	if !found {
		t.Fatalf("descriptor %q was not registered", wantPath)
	}
}
