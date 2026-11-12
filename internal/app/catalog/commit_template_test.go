package catalog

import (
	"errors"
	"testing"
)

func TestRenderCommitTemplate(t *testing.T) {
	cases := []struct{ template, title, want string }{
		{"", "P1-01: reopen keeps the status (lần 3)", "P1-01: reopen keeps the status"},
		{"feat: {title}", "Add tracing (lần 2)", "feat: Add tracing"},
		{"feat({taskId}): {title}", "T-01: Order status API", "feat(T-01): Order status API"},
		{"{taskId} {title}\n\nWork item {workItemId}", "Docs only", "Docs only\n\nWork item wi-9"},
	}
	for _, c := range cases {
		if got := RenderCommitTemplate(c.template, c.title, "wi-9"); got != c.want {
			t.Errorf("RenderCommitTemplate(%q, %q) = %q, want %q", c.template, c.title, got, c.want)
		}
	}
}

func TestValidateCommitTemplate(t *testing.T) {
	for _, ok := range []string{"feat: {title}", "{taskId}: {title}\n\nRefs: {workItemId}"} {
		if err := ValidateCommitTemplate(ok); err != nil {
			t.Errorf("ValidateCommitTemplate(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "  \n\t", "feat: {name}", "a\rb"} {
		if err := ValidateCommitTemplate(bad); !errors.Is(err, ErrInvalidCommitTemplate) {
			t.Errorf("ValidateCommitTemplate(%q) = %v, want ErrInvalidCommitTemplate", bad, err)
		}
	}
}
