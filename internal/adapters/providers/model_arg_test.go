package providers_test

// V9-14a: the model an AgentProfile pins reaches the CLI as --model.
//
// The path profile -> ResolvedExecutionProfile -> AgentExecutionRequest.Model ->
// adapter was read and believed but never locked by a test that looks at the
// command line. These two tests lock the adapter end of it; the acceptance test
// agent_model_test.go (internal/integration/v5accept) locks the profile end.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func TestProviderAdapters_RequestModelIsPassedAsDashDashModel(t *testing.T) {
	t.Parallel()
	for _, tc := range outputLimitCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			executor, err := tc.newWith(0)
			if err != nil {
				t.Fatal(err)
			}
			valuesAfter := func(arguments []string) []string {
				var values []string
				for i, argument := range arguments {
					if argument == "--model" && i+1 < len(arguments) {
						values = append(values, arguments[i+1])
					}
				}
				return values
			}
			run := func(name, model string) []string {
				capture := filepath.Join(t.TempDir(), name+".json")
				request := helperRequest(name, t.TempDir(), capture, "success")
				request.Model = model
				if _, err := executor.Start(context.Background(), request, &eventCollector{}); err != nil {
					t.Fatalf("start (model %q): %v", model, err)
				}
				return valuesAfter(readCapture(t, capture).Argv)
			}

			if got := run("model-set", "claude-opus-5-5"); len(got) != 1 || got[0] != "claude-opus-5-5" {
				t.Fatalf("--model = %v, want exactly claude-opus-5-5", got)
			}
			if got := run("model-unset", ""); len(got) != 0 {
				t.Fatalf("--model = %v with no model requested, want none (the CLI's own default)", got)
			}

			// A resumed attempt asks for its model too.
			resumeCapture := filepath.Join(t.TempDir(), "model-resume.json")
			resumeRequest := helperRequest("model-resume", t.TempDir(), resumeCapture, "success")
			resumeRequest.Model = "claude-haiku-4-5-20251001"
			if _, err := executor.Resume(context.Background(), resumeRequest,
				ports.ProviderSessionRef{Provider: sessionProvider(tc.name), SessionID: sessionID(tc.name)}, &eventCollector{}); err != nil {
				t.Fatalf("resume: %v", err)
			}
			if got := valuesAfter(readCapture(t, resumeCapture).Argv); len(got) != 1 || got[0] != "claude-haiku-4-5-20251001" {
				t.Fatalf("resume --model = %v, want exactly claude-haiku-4-5-20251001", got)
			}
		})
	}
}

func sessionProvider(name string) ports.ProviderKey {
	if name == "codex" {
		return ports.ProviderCodex
	}
	return ports.ProviderClaude
}

func sessionID(name string) string {
	if name == "codex" {
		return "codex-session-0001"
	}
	return "claude-session-0001"
}
