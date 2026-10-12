package redo

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Argument handling is checked before anything touches storage; the commands themselves are covered by internal/app/runtime and the
// HTTP tests, which drive the same service.
func TestCommands_RejectBadInvocationsBeforeTouchingStorage(t *testing.T) {
	cases := []struct {
		name string
		run  func(args []string) error
		args []string
	}{
		{"retry without an id", func(a []string) error { return Retry(context.Background(), Dependencies{}, a, &bytes.Buffer{}, &bytes.Buffer{}) }, nil},
		{"retry with two ids", func(a []string) error { return Retry(context.Background(), Dependencies{}, a, &bytes.Buffer{}, &bytes.Buffer{}) }, []string{"a", "b"}},
		{"recreate without an id", func(a []string) error { return Recreate(context.Background(), Dependencies{}, a, &bytes.Buffer{}, &bytes.Buffer{}) }, []string{"--reason", "x"}},
		{"recreate without a reason", func(a []string) error { return Recreate(context.Background(), Dependencies{}, a, &bytes.Buffer{}, &bytes.Buffer{}) }, []string{"wi-1"}},
		{"redo-preview without an id", func(a []string) error { return Preview(context.Background(), Dependencies{}, a, &bytes.Buffer{}, &bytes.Buffer{}) }, nil},
		{"unknown flag", func(a []string) error { return Retry(context.Background(), Dependencies{}, a, &bytes.Buffer{}, &bytes.Buffer{}) }, []string{"--nope", "wi-1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var usage cli.UsageError
			if err := c.run(c.args); !errors.As(err, &usage) {
				t.Fatalf("err = %v, want a usage error", err)
			}
		})
	}
}

func TestDescriptorsAreRegistered(t *testing.T) {
	want := map[string]string{"retry": "retryWorkItem", "recreate": "recreateWorkItem", "redo-preview": "getWorkItemRedoPreview"}
	for _, d := range cli.All() {
		if len(d.Path) == 2 && d.Path[0] == "work-item" {
			if operation, ok := want[d.Path[1]]; ok {
				if d.HTTPOperationID != operation {
					t.Errorf("aw work-item %s maps to %s, want %s", d.Path[1], d.HTTPOperationID, operation)
				}
				delete(want, d.Path[1])
			}
		}
	}
	for leaf := range want {
		t.Errorf("aw work-item %s is not registered", leaf)
	}
}
