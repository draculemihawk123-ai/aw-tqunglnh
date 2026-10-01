package alphagate

import (
	"regexp"
	"strings"
	"testing"
)

// V8-12R-02: the `v8-alpha-gate` CI job ran non-enforcing (--enforce=false)
// while the parity ledger still made gatePass honestly false, because an
// always-red check would have made every unrelated PR unmergeable. It flips to
// enforcing exactly when the recorded verdict is ALPHA_READY. This test ties
// the two together in both directions: the record cannot say ALPHA_READY while
// the job would let a failing gate through, and the job cannot be switched to
// enforcing while the record still says the gate does not pass.

var assessFlag = regexp.MustCompile(`--enforce=(true|false)`)

func TestV8AlphaGateJobEnforcesExactlyWhenTheVerdictIsAlphaReady(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/spike-gate.yml")
	start := strings.Index(workflow, "\n  v8-alpha-gate:\n")
	if start < 0 {
		t.Fatal("the v8-alpha-gate job is missing from .github/workflows/spike-gate.yml")
	}
	job := workflow[start+1:]
	// The job ends where the next top-level job (two-space-indented key) begins.
	if next := regexp.MustCompile(`\n  [a-z0-9][a-z0-9-]*:\n`).FindStringIndex(job[1:]); next != nil {
		job = job[:next[0]+1]
	}

	flags := assessFlag.FindAllStringSubmatch(stripYAMLComments(job), -1)
	if len(flags) != 1 {
		t.Fatalf("the v8-alpha-gate job must pass --enforce exactly once on its assess command, found %d", len(flags))
	}
	enforcing := flags[0][1] == "true"

	v := loadVerdict(t)
	switch {
	case v.Verdict == "ALPHA_READY" && !enforcing:
		t.Error("the verdict is ALPHA_READY but the v8-alpha-gate job is still --enforce=false: a regression would pass CI")
	case v.Verdict != "ALPHA_READY" && enforcing:
		t.Errorf("the v8-alpha-gate job is --enforce=true but the recorded verdict is %s: the gate would be red on every PR", v.Verdict)
	}
}

func stripYAMLComments(s string) string {
	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
