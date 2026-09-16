package run_test

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	_ "github.com/taQuangLing/agent-workflow/internal/delivery/cli/run"
)

// TestRunPackage_RegistersEveryDescriptorInDefault proves this package's
// own six init() registrations (start.go, cancel.go, show.go, graph.go,
// timeline.go, diagnostics.go) all landed in the shared
// internal/delivery/cli.Default registry — this task's own "leaf package
// registers its own cli.Descriptor(s) via its own package init()" bar,
// made mechanical (the blank import above is what triggers those init()
// functions in a test binary that otherwise never calls into this
// package's own exported command functions directly).
func TestRunPackage_RegistersEveryDescriptorInDefault(t *testing.T) {
	want := map[string]string{
		"run cancel|PROJECT":      "CancelRun",
		"run diagnostics|PROJECT": "GetRunDiagnostics",
		"run graph|PROJECT":       "GetRunGraph",
		"run show|PROJECT":        "GetRunDetail",
		"run start|PROJECT":       "StartWorkflowRun",
		"run timeline|PROJECT":    "GetRunTimeline",
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
