package ports_test

import (
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func TestProviderFailureMessage(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 800)
	for _, tc := range []struct {
		name, summary, reason, want string
	}{
		{"no reason keeps the summary", "Claude reported a failed result", "", "Claude reported a failed result"},
		{"blank reason keeps the summary", "Claude reported a failed result", " \n\t ", "Claude reported a failed result"},
		{"reason is appended", "Claude reported a failed result", "You've hit your limit", "Claude reported a failed result: You've hit your limit"},
		{"whitespace collapses", "S", "line one\n\n  line\ttwo", "S: line one line two"},
		{"control characters are dropped", "S", "a\x00b\x1b[31mred", "S: ab[31mred"},
		{"long reason is cut", "S", long, "S: " + strings.Repeat("x", 500) + "…"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ports.ProviderFailureMessage(tc.summary, tc.reason); got != tc.want {
				t.Fatalf("ProviderFailureMessage(%q, %q) = %q, want %q", tc.summary, tc.reason, got, tc.want)
			}
		})
	}
}
