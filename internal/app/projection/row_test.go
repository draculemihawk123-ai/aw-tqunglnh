package projection

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// TestStatusConstants_MatchWorkItemStatus guards row.go's own doc-comment
// claim that Status is "always one of work.WorkItemStatus's own six closed
// values" — this package deliberately does not import internal/domain/work
// in row.go/reducers.go itself (keeping this schema package free of a
// domain-package dependency), so this test is the one place that
// cross-checks the hand-copied wire strings never drift from the real enum.
func TestStatusConstants_MatchWorkItemStatus(t *testing.T) {
	cases := []struct {
		local string
		real  work.WorkItemStatus
	}{
		{statusBacklog, work.WorkItemBacklog},
		{statusReady, work.WorkItemReady},
		{statusActive, work.WorkItemActive},
		{statusBlocked, work.WorkItemBlocked},
		{statusDone, work.WorkItemDone},
		{statusCancelled, work.WorkItemCancelled},
	}
	for _, tt := range cases {
		if tt.local != string(tt.real) {
			t.Errorf("local status constant %q does not match work.WorkItemStatus %q", tt.local, tt.real)
		}
	}
}
