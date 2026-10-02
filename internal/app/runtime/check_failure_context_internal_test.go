package runtime

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// V9-02: the pure parts of the maker's failure summary — bounding, which
// stream is quoted, which verdicts count as failing.

func TestTailOf_BoundedAtARuneBoundaryAndMarkedWhenCut(t *testing.T) {
	t.Parallel()
	short := "line one\nline two\n"
	if got := tailOf(short, 100); got != "line one\nline two" {
		t.Fatalf("tailOf(short) = %q, want the whole text without its trailing newline", got)
	}

	// 5000 three-byte runes: every possible cut point inside a rune must be
	// moved to a rune boundary.
	long := strings.Repeat("é€", 2500)
	for limit := 10; limit < 16; limit++ {
		got := tailOf(long, limit)
		if !utf8.ValidString(got) {
			t.Fatalf("tailOf(limit=%d) = %q, not valid UTF-8", limit, got)
		}
		if !strings.HasPrefix(got, "...(earlier output omitted)\n") {
			t.Fatalf("tailOf(limit=%d) = %q, want the cut to be marked", limit, got)
		}
		if body := strings.TrimPrefix(got, "...(earlier output omitted)\n"); len(body) > limit || len(body) == 0 {
			t.Fatalf("tailOf(limit=%d) kept %d bytes, want 1..%d", limit, len(body), limit)
		}
	}
	if got := tailOf(strings.Repeat("a", 10)+"END", 3); !strings.HasSuffix(got, "END") {
		t.Fatalf("tailOf = %q, want the END of the text", got)
	}
}

func TestHeadOf_BoundedAtARuneBoundaryAndMarkedWhenCut(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("€", 100)
	got := headOf(long, 10)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "...(further criteria omitted)") {
		t.Fatalf("headOf = %q, want valid UTF-8 with the cut marked", got)
	}
	if got := headOf("short", 10); got != "short" {
		t.Fatalf("headOf(short) = %q", got)
	}
}

func TestSummarizeCommandFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		body         string
		wantOK       bool
		wantWhat     string
		wantWhy      string
		wantWhyEmpty bool
	}{
		{
			name: "stderr is quoted", body: `{"exitCode":2,"stdout":"out","stderr":"boom"}`,
			wantOK: true, wantWhat: `check "t" failed: the command exited with code 2.`, wantWhy: "stderr (last part):\nboom",
		},
		{
			name: "stdout is the fallback when stderr is empty", body: `{"exitCode":1,"stdout":"FAIL pkg"}`,
			wantOK: true, wantWhat: `exited with code 1`, wantWhy: "stdout (last part; stderr was empty):\nFAIL pkg",
		},
		{
			name: "nothing captured", body: `{"exitCode":3}`,
			wantOK: true, wantWhat: `exited with code 3`, wantWhyEmpty: true,
		},
		{
			name: "truncated non-zero exit says so", body: `{"exitCode":1,"truncated":true,"stderr":"x"}`,
			wantOK: true, wantWhat: "its output was cut at the command's maxOutputBytes",
		},
		{
			name: "exit zero with cut output", body: `{"exitCode":0,"truncated":true}`,
			wantOK: true, wantWhat: "exited 0 but its output was cut",
		},
		{name: "not a command record", body: `[1,2`, wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			what, why, ok := summarizeCommandFailure("t", test.body)
			if ok != test.wantOK {
				t.Fatalf("ok = %v, want %v", ok, test.wantOK)
			}
			if !strings.Contains(what, test.wantWhat) {
				t.Fatalf("what = %q, want it to contain %q", what, test.wantWhat)
			}
			if test.wantWhyEmpty && why != "" {
				t.Fatalf("why = %q, want empty", why)
			}
			if test.wantWhy != "" && why != test.wantWhy {
				t.Fatalf("why = %q, want %q", why, test.wantWhy)
			}
		})
	}
}

func TestSummarizeGateFailure_ListsOnlyCriteriaThatDidNotPass(t *testing.T) {
	t.Parallel()
	body := `{"overallVerdict":"FAIL","criteria":[` +
		`{"name":"lint","evidenceKey":"LINT","verdict":"PASS"},` +
		`{"name":"docs","evidenceKey":"DOCS","verdict":"NOT_APPLICABLE","reason":"no docs"},` +
		`{"name":"tests","evidenceKey":"TESTS","verdict":"FAIL","detail":"2 failed"},` +
		`{"name":"build","evidenceKey":"BUILD","verdict":"NOT_RUN"}]}`
	what, why, ok := summarizeGateFailure("g", body)
	if !ok {
		t.Fatal("ok = false for a gate result")
	}
	if !strings.Contains(what, "gate verdict FAIL") || !strings.Contains(what, "2 of 4 criteria did not pass") {
		t.Fatalf("what = %q", what)
	}
	for _, want := range []string{"- tests (TESTS): FAIL — 2 failed", "- build (BUILD): NOT_RUN"} {
		if !strings.Contains(why, want) {
			t.Fatalf("why = %q, want it to contain %q", why, want)
		}
	}
	for _, unwanted := range []string{"LINT", "DOCS"} {
		if strings.Contains(why, unwanted) {
			t.Fatalf("why = %q, must not list the passing criterion %s", why, unwanted)
		}
	}
	if _, _, ok := summarizeGateFailure("g", `{"exitCode":1}`); ok {
		t.Fatal("ok = true for a command record read as a gate result")
	}
}

func TestFailingEvidenceVerdict(t *testing.T) {
	t.Parallel()
	for _, verdict := range []string{runtimedomain.EvidenceVerdictFailed, string(gate.VerdictFail), string(gate.VerdictError), string(gate.VerdictNotRun)} {
		if !failingEvidenceVerdict(verdict) {
			t.Errorf("failingEvidenceVerdict(%q) = false, want true", verdict)
		}
	}
	for _, verdict := range []string{runtimedomain.EvidenceVerdictSucceeded, runtimedomain.EvidenceVerdictRecorded, string(gate.VerdictPass), string(gate.VerdictNotApplicable)} {
		if failingEvidenceVerdict(verdict) {
			t.Errorf("failingEvidenceVerdict(%q) = true, want false", verdict)
		}
	}
}

func TestCheckSuccessOutcomes(t *testing.T) {
	t.Parallel()
	if got := checkSuccessOutcomes([]string{"done"}, ""); len(got) != 1 || got[0] != "done" {
		t.Fatalf("no failureOutcome: got %v, want the allowed outcomes untouched", got)
	}
	if got := checkSuccessOutcomes([]string{"passed", "failed"}, "failed"); len(got) != 1 || got[0] != "passed" {
		t.Fatalf("with failureOutcome: got %v, want [passed]", got)
	}
}
