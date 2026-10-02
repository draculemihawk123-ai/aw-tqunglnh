package providers_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/providers"
)

// V9-03: the fake CLI's "outcome-from-prompt" mode reads
// taskContract.allowedOutcomes from the instruction artifact on stdin and
// reports an outcome of its pick, the way an agent following a v2 prompt does.

const promptWithOutcomes = `{"schemaVersion":2,"taskContract":{"allowedOutcomes":["approved","rework","abandon"]}}`

func runOutcomeFromPrompt(t *testing.T, provider, prompt, pick string) (stdout string, exit int) {
	t.Helper()
	t.Setenv("AGENTKIT_HELPER_OUTCOME_PICK", pick)
	var out bytes.Buffer
	exit = providers.RunFakeProviderCLI(provider, []string{"-p"}, "outcome-from-prompt", "", strings.NewReader(prompt), &out)
	return out.String(), exit
}

func TestRunFakeProviderCLI_OutcomeFromPrompt_ReportsThePickedOutcome(t *testing.T) {
	// The marker rides inside a JSON string of the provider's event, so it is
	// escaped there (quotes, angle brackets): compare against the escaped form.
	escapedMarker := func(outcome string) string {
		encoded, err := json.Marshal(providers.OutcomeMarker(outcome))
		if err != nil {
			t.Fatalf("encode marker: %v", err)
		}
		return string(encoded[1 : len(encoded)-1])
	}
	for _, provider := range []string{"claude", "codex"} {
		for _, tc := range []struct {
			pick string
			want string // outcome reported; "" means no marker
		}{
			{"", "approved"},
			{"first", "approved"},
			{"last", "abandon"},
			{"1", "rework"},
			{"not-listed", providers.OutcomeNotListed},
			{"none", ""},
		} {
			t.Run(provider+"/"+tc.pick, func(t *testing.T) {
				stdout, exit := runOutcomeFromPrompt(t, provider, promptWithOutcomes, tc.pick)
				if exit != 0 {
					t.Fatalf("exit = %d, want 0", exit)
				}
				if tc.want == "" {
					if strings.Contains(stdout, "agentkit-outcome") {
						t.Fatalf("pick none still wrote a marker:\n%s", stdout)
					}
					return
				}
				if !strings.Contains(stdout, escapedMarker(tc.want)) {
					t.Fatalf("stdout does not carry the marker for %q:\n%s", tc.want, stdout)
				}
				if strings.Count(stdout, "/agentkit-outcome") != 1 {
					t.Fatalf("want exactly one marker:\n%s", stdout)
				}
			})
		}
	}
}

func TestRunFakeProviderCLI_OutcomeFromPrompt_RefusesAPromptThatOffersNoChoice(t *testing.T) {
	for name, prompt := range map[string]string{
		"a v1 instruction artifact": `{"taskContract":{"workItemId":"w","title":"t"},"messages":[],"resources":[]}`,
		"not JSON":                  `please implement the feature`,
	} {
		t.Run(name, func(t *testing.T) {
			stdout, exit := runOutcomeFromPrompt(t, "claude", prompt, "first")
			if exit != 5 || strings.Contains(stdout, "agentkit-outcome") {
				t.Fatalf("exit = %d, stdout = %q, want exit 5 and no marker", exit, stdout)
			}
		})
	}
	if _, exit := runOutcomeFromPrompt(t, "claude", promptWithOutcomes, "7"); exit != 5 {
		t.Fatalf("an index past the list: exit = %d, want 5", exit)
	}
}
