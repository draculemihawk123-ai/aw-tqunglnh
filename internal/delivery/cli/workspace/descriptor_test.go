package workspace_test

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	_ "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workspace"
)

// TestWorkspacePackage_RegistersEveryDescriptorInDefault proves this
// package's own six init() registrations (show.go, release.go, source.go,
// diff.go, log.go, reconcile.go) all landed in the shared
// internal/delivery/cli.Default registry, with the exact HTTPOperationID
// this task's own brief names for each — every one of them has a real HTTP
// operationId (this package, unlike internal/delivery/cli/evidence, has no
// CLI_LOCAL leaf of its own).
func TestWorkspacePackage_RegistersEveryDescriptorInDefault(t *testing.T) {
	want := map[string]struct {
		appOp    string
		httpOpID string
	}{
		"workspace-set show|PROJECT":             {"GetWorkspaceSetState", "getWorkspaceSetState"},
		"workspace-set release|PROJECT":          {"RequestWorkspaceSetRelease", "requestWorkspaceSetRelease"},
		"repository-workspace source|PROJECT":    {"GetSource", "getWorkspaceSource"},
		"repository-workspace diff|PROJECT":      {"GetDiff", "getWorkspaceDiff"},
		"repository-workspace log|PROJECT":       {"GetRepositoryLog", "getWorkspaceRepositoryLog"},
		"repository-workspace reconcile|PROJECT": {"RequestWorkspaceReconciliation", "requestWorkspaceReconciliation"},
	}

	got := map[string]cli.Descriptor{}
	for _, d := range cli.All() {
		key := d.Path[0]
		for _, seg := range d.Path[1:] {
			key += " " + seg
		}
		key += "|" + string(d.Scope)
		got[key] = d
	}

	for key, want := range want {
		d, ok := got[key]
		if !ok {
			t.Errorf("descriptor %q not found in cli.Default", key)
			continue
		}
		if d.AppOperation != want.appOp {
			t.Errorf("descriptor %q AppOperation = %q, want %q", key, d.AppOperation, want.appOp)
		}
		if d.HTTPOperationID != want.httpOpID {
			t.Errorf("descriptor %q HTTPOperationID = %q, want %q", key, d.HTTPOperationID, want.httpOpID)
		}
	}
}
