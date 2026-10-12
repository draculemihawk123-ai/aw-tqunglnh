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

// V11-02: the provider's own words decide whether a failure is a limit. The
// vocabulary below is what the Claude and Codex CLIs print; a context or output
// length limit is not a quota.
func TestIsProviderQuotaMessage(t *testing.T) {
	for _, reason := range []string{
		"You've hit your limit · resets 5pm",
		"You've hit your session limit · resets 5:30pm (UTC)",
		"You've hit your usage limit.\nTry again at 5pm.",
		"Rate limit exceeded, retry later",
		"API Error: 429 {\"type\":\"rate_limit_error\"}",
		"5-hour limit reached",
		"Quota exceeded for this project",
		"Too many requests",
		"Credit balance is too low",
	} {
		if !ports.IsProviderQuotaMessage(reason) {
			t.Errorf("IsProviderQuotaMessage(%q) = false, want true", reason)
		}
	}
	for _, reason := range []string{
		"", "Invalid API key · Please run /login", "Prompt is too long", "context limit exceeded", "output token limit exceeded",
		"Execution error", "The model claude-x does not exist",
	} {
		if ports.IsProviderQuotaMessage(reason) {
			t.Errorf("IsProviderQuotaMessage(%q) = true, want false", reason)
		}
	}
}
