package alphagate

// V9-12 (docs/design/12-v9-harness-alignment.md): "Ghi verdict V9_DONE|REWORK|
// CHƯA ĐỦ EVIDENCE ... từ chính lần chạy gate của commit đã merge, không suy ra
// từ unit test", and "Mỗi G1–G10 có ít nhất một test tái hiện failure mode gốc".
//
// docs/release/v9-verdict.json is the machine-readable record. These tests keep
// it, docs/harness-engineering/15-doi-chieu-v9.md, docs/00-start-here.md and the
// release report from disagreeing, enforce the verdict rule, and check that every
// test the record cites really exists (so a renamed or deleted test cannot leave
// a gap "closed" by a name that no longer runs).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type v9Evidence struct {
	Test string `json:"test"`
	File string `json:"file"`
}

type v9Verdict struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Verdict        string `json:"verdict"`
	AssessedCommit string `json:"assessedCommit"`
	AssessmentRun  struct {
		ID       int64  `json:"id"`
		URL      string `json:"url"`
		Job      string `json:"job"`
		Artifact string `json:"artifact"`
	} `json:"assessmentRun"`
	Gate struct {
		AlphaGatePass               bool `json:"alphaGatePass"`
		Enforcing                   bool `json:"enforcing"`
		GoldenWorkloadV801Unchanged bool `json:"goldenWorkloadV801Unchanged"`
		AlphaGated                  struct{ Total, Pass int } `json:"alphaGatedCriteria"`
		CriteriaWithTestEvidence    int                       `json:"criteriaWithTestEvidence"`
		Journeys                    struct{ Total, Pass int } `json:"journeys"`
		VersionGates                struct{ Total, Pass int } `json:"versionGates"`
		FinalGates                  struct{ Total, Pass int } `json:"finalGates"`
	} `json:"gate"`
	ADRs []string `json:"adrs"`
	Gaps []struct {
		ID       string       `json:"id"`
		Task     string       `json:"task"`
		PR       int          `json:"pr"`
		Title    string       `json:"title"`
		Evidence []v9Evidence `json:"evidence"`
	} `json:"gaps"`
	OperatorDefects []struct {
		Task string `json:"task"`
		PR   int    `json:"pr"`
		v9Evidence
	} `json:"operatorDefects"`
	LiveProvider struct {
		Status   string   `json:"status"`
		Provider string   `json:"provider"`
		Test     string   `json:"test"`
		File     string   `json:"file"`
		Evidence string   `json:"evidence"`
		Codex    string   `json:"codex"`
		Open     []string `json:"open"`
	} `json:"liveProvider"`
	Blockers []struct {
		ID       string `json:"id"`
		Detail   string `json:"detail"`
		NextTask string `json:"nextTask"`
	} `json:"blockers"`
	KnownLimitations []string `json:"knownLimitations"`
}

var allowedV9Verdicts = map[string]bool{"V9_DONE": true, "REWORK": true, "CHƯA ĐỦ EVIDENCE": true}

func loadV9Verdict(t *testing.T) v9Verdict {
	t.Helper()
	var v v9Verdict
	decoder := json.NewDecoder(strings.NewReader(readRepoFile(t, "docs/release/v9-verdict.json")))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&v); err != nil {
		t.Fatalf("docs/release/v9-verdict.json: %v", err)
	}
	return v
}

func TestV9Verdict_FollowsTheVerdictRule(t *testing.T) {
	v := loadV9Verdict(t)
	if !allowedV9Verdicts[v.Verdict] {
		t.Fatalf("verdict %q is not one of V9_DONE, REWORK, CHƯA ĐỦ EVIDENCE", v.Verdict)
	}
	if v.Verdict != "V9_DONE" {
		if len(v.Blockers) == 0 {
			t.Fatalf("verdict %s must name its blocker(s) and the next narrow task", v.Verdict)
		}
		for _, b := range v.Blockers {
			if b.ID == "" || b.Detail == "" || b.NextTask == "" {
				t.Errorf("blocker %+v needs an id, a detail and a next task", b)
			}
		}
		return
	}
	if len(v.Blockers) != 0 {
		t.Error("V9_DONE with blockers listed")
	}
	if !v.Gate.AlphaGatePass || !v.Gate.Enforcing || !v.Gate.GoldenWorkloadV801Unchanged {
		t.Errorf("V9_DONE needs the v8-alpha-gate enforcing and green with the V8-01 golden workload unchanged, got %+v", v.Gate)
	}
	g := v.Gate
	if g.AlphaGated.Pass != g.AlphaGated.Total || g.Journeys.Pass != g.Journeys.Total ||
		g.VersionGates.Pass != g.VersionGates.Total || g.FinalGates.Pass != g.FinalGates.Total || g.AlphaGated.Total == 0 {
		t.Errorf("V9_DONE with a gate that is not all green: %+v", g)
	}
	for _, id := range []string{"ADR-030", "ADR-031", "ADR-032", "ADR-033"} {
		found := false
		for _, have := range v.ADRs {
			found = found || have == id
		}
		if !found {
			t.Errorf("V9_DONE needs %s recorded as accepted", id)
		}
		if !strings.Contains(readRepoFile(t, "docs/architecture/02-architecture-decisions.md"), id+" —") {
			t.Errorf("%s is not in docs/architecture/02-architecture-decisions.md", id)
		}
	}
	seen := map[string]bool{}
	for _, gap := range v.Gaps {
		seen[gap.ID] = true
		if len(gap.Evidence) == 0 || gap.Task == "" || gap.PR == 0 || gap.Title == "" {
			t.Errorf("%s needs a task, a PR, a title and at least one test", gap.ID)
		}
	}
	for i := 1; i <= 10; i++ {
		if id := fmt.Sprintf("G%d", i); !seen[id] {
			t.Errorf("V9_DONE has no entry for %s", id)
		}
	}
	if len(v.Gaps) != 10 {
		t.Errorf("%d gap entries, want exactly G1..G10", len(v.Gaps))
	}
}

func TestV9Verdict_EveryCitedTestExists(t *testing.T) {
	v := loadV9Verdict(t)
	var cited []v9Evidence
	for _, gap := range v.Gaps {
		cited = append(cited, gap.Evidence...)
	}
	for _, d := range v.OperatorDefects {
		cited = append(cited, d.v9Evidence)
	}
	cited = append(cited, v9Evidence{Test: v.LiveProvider.Test, File: v.LiveProvider.File})
	for _, e := range cited {
		if !strings.HasSuffix(e.File, "_test.go") {
			t.Errorf("%s: %q is not a test file", e.Test, e.File)
			continue
		}
		source := readRepoFile(t, e.File)
		if !regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(e.Test) + `\(`).MatchString(source) {
			t.Errorf("%s is cited as evidence but %s has no such test", e.Test, e.File)
		}
	}
	// The recorded live run is a bundle in the repository, not a claim.
	if _, err := readRepoFileOrErr(v.LiveProvider.Evidence); err != nil {
		t.Errorf("live provider evidence %s: %v", v.LiveProvider.Evidence, err)
	}
}

func TestV9Verdict_DocsAgreeWithTheRecord(t *testing.T) {
	v := loadV9Verdict(t)
	sha := regexp.MustCompile(`^[0-9a-f]{40}$`)
	if !sha.MatchString(v.AssessedCommit) {
		t.Fatalf("assessedCommit %q is not a full SHA-1", v.AssessedCommit)
	}
	if !strings.HasSuffix(v.AssessmentRun.URL, "/actions/runs/"+itoa64(v.AssessmentRun.ID)) {
		t.Errorf("assessment run url %q does not end in the run id %d", v.AssessmentRun.URL, v.AssessmentRun.ID)
	}
	headline := "**Verdict V9: `" + v.Verdict + "`**"
	for _, rel := range []string{
		"docs/harness-engineering/15-doi-chieu-v9.md",
		"docs/00-start-here.md",
		"docs/release/alpha-release-report.md",
	} {
		doc := readRepoFile(t, rel)
		if !strings.Contains(doc, headline) {
			t.Errorf("%s does not state %s", rel, headline)
		}
		if !strings.Contains(doc, "`"+v.AssessedCommit[:7]+"`") && !strings.Contains(doc, v.AssessedCommit) {
			t.Errorf("%s does not name the assessed commit %s", rel, v.AssessedCommit[:7])
		}
		if !strings.Contains(doc, v.AssessmentRun.URL) && !strings.Contains(doc, itoa64(v.AssessmentRun.ID)) {
			t.Errorf("%s does not name the assessment run %d", rel, v.AssessmentRun.ID)
		}
	}
	// Every gap and every cited test appears in the reconciliation document.
	reconcile := readRepoFile(t, "docs/harness-engineering/15-doi-chieu-v9.md")
	for _, gap := range v.Gaps {
		for _, e := range gap.Evidence {
			if !strings.Contains(reconcile, e.Test) {
				t.Errorf("15-doi-chieu-v9.md does not cite %s as evidence for %s", e.Test, gap.ID)
			}
		}
	}
	// The live-provider status is the one alpha-verdict.json and the report carry.
	alpha := loadVerdict(t)
	if alpha.LiveProviderCompatibility.Status != v.LiveProvider.Status {
		t.Errorf("live provider status is %s here and %s in alpha-verdict.json", v.LiveProvider.Status, alpha.LiveProviderCompatibility.Status)
	}
}

func readRepoFileOrErr(rel string) (string, error) {
	content, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	return string(content), err
}
