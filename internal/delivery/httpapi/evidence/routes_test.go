package evidence_test

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	evidencehttp "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/evidence"
)

// TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet mirrors
// internal/delivery/httpapi/message/message_test.go's own identical test:
// the closed set of five operationIds below — locked in by
// docs/design/11-v6-00-ux-artifact.md's own Screen 11/12 action inventory —
// is EVERY route this package ever registers.
func TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet(t *testing.T) {
	reg := httpapi.NewRouteRegistry()
	evidencehttp.RegisterRoutes(reg, evidencehttp.Dependencies{})

	want := map[string]bool{
		"listEvidence": true, "getEvidence": true, "listArtifacts": true,
		"getArtifactContent": true, "getContextSnapshot": true,
	}
	got := reg.Descriptors()
	if len(got) != len(want) {
		t.Fatalf("len(Descriptors()) = %d, want %d", len(got), len(want))
	}
	for _, d := range got {
		if !want[d.OperationID] {
			t.Errorf("unexpected operationId %q registered", d.OperationID)
		}
		if d.ScopeKind != httpapi.ScopeProject {
			t.Errorf("operationId %q has ScopeKind %q, want PROJECT", d.OperationID, d.ScopeKind)
		}
		delete(want, d.OperationID)
	}
	if len(want) != 0 {
		t.Errorf("missing operationIds: %v", want)
	}
}
