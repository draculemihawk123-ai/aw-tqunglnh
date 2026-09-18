package evidence_test

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	_ "github.com/taQuangLing/agent-workflow/internal/delivery/cli/evidence"
)

// TestEvidencePackage_RegistersEveryDescriptorInDefault proves this
// package's own four init() registrations (list.go, verify.go,
// contextsnapshot.go, artifact.go) all landed in the shared
// internal/delivery/cli.Default registry, with the exact HTTPOperationID
// this task's own brief names for each — cli.CLILocalOperation for
// `evidence verify` only (the one leaf with no HTTP route,
// docs/design/08-v6-api-projections.md V6-15O's own closed set naming
// "evidence verify" directly), real HTTP operationIds for the other three.
func TestEvidencePackage_RegistersEveryDescriptorInDefault(t *testing.T) {
	want := map[string]struct {
		appOp    string
		httpOpID string
	}{
		"evidence list|PROJECT":         {"ListEvidenceForWorkItem", "listEvidence"},
		"evidence verify|PROJECT":       {"VerifyEvidence", cli.CLILocalOperation},
		"context-snapshot show|PROJECT": {"GetContextSnapshot", "getContextSnapshot"},
		"artifact get|PROJECT":          {"ResolveEvidenceArtifactContent", "getArtifactContent"},
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
