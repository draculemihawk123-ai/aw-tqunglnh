package alphagate

// V8-12 (docs/design/10-v8-alpha-hardening.md): "Verify: links/evidence/checksums
// tồn tại, docs/status nhất quán" and "Hoàn thành khi: ALPHA_READY chỉ khi
// gatePass=true; verdict khác ghi blocker và next narrow rework/evidence task".
//
// docs/release/alpha-verdict.json is the machine-readable verdict record;
// docs/release/alpha-release-report.md and the status section of
// docs/00-start-here.md restate it for people. These tests keep the three from
// disagreeing, enforce the verdict rule, and check that every link and recorded
// checksum is well formed. (internal/delivery/parity additionally ties the
// record to the real parity ledger, so the record cannot go stale when the last
// debt entry is closed.)

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type verdictRecord struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Verdict        string `json:"verdict"`
	GatePass       bool   `json:"gatePass"`
	AssessedCommit string `json:"assessedCommit"`
	AssessmentRun  struct {
		ID       int64  `json:"id"`
		URL      string `json:"url"`
		Artifact string `json:"artifact"`
		Job      string `json:"job"`
	} `json:"assessmentRun"`
	Summary struct {
		Criteria struct {
			Assessed        int `json:"assessed"`
			AlphaMust       int `json:"alphaMust"`
			CrossPhaseGuard int `json:"crossPhaseGuard"`
			OutOfAlphaScope int `json:"outOfAlphaScope"`
			NotApplicable   int `json:"notApplicable"`
		} `json:"criteria"`
		AlphaGated struct {
			Total        int `json:"total"`
			Pass         int `json:"pass"`
			Fail         int `json:"fail"`
			Insufficient int `json:"insufficient"`
		} `json:"alphaGatedCriteria"`
		WithTestEvidence int                             `json:"criteriaWithTestEvidence"`
		SuiteOnly        int                             `json:"criteriaWithSuiteEvidenceOnly"`
		Journeys         struct{ Total, Pass int }       `json:"journeys"`
		VersionGates     struct{ Total, Pass int }       `json:"versionGates"`
		FinalGates       struct{ Total, Pass, Fail int } `json:"finalGates"`
	} `json:"summary"`
	Blockers []struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		Detail   string `json:"detail"`
		NextTask string `json:"nextTask"`
	} `json:"blockers"`
	NextTasks []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Scope string `json:"scope"`
	} `json:"nextTasks"`
	KnownLimitations          []string `json:"knownLimitations"`
	LiveProviderCompatibility struct {
		Status string `json:"status"`
		Detail string `json:"detail"`
	} `json:"liveProviderCompatibility"`
	InstallArtifacts []struct {
		OS              string `json:"os"`
		File            string `json:"file"`
		SHA256          string `json:"sha256"`
		ReproducedTwice bool   `json:"reproducedTwice"`
		Source          string `json:"source"`
	} `json:"installArtifacts"`
}

var allowedVerdicts = map[string]bool{"ALPHA_READY": true, "REWORK": true, "STOP": true, "CHƯA ĐỦ EVIDENCE": true}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(content), "\r\n", "\n")
}

func loadVerdict(t *testing.T) verdictRecord {
	t.Helper()
	var v verdictRecord
	decoder := json.NewDecoder(strings.NewReader(readRepoFile(t, "docs/release/alpha-verdict.json")))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&v); err != nil {
		t.Fatalf("docs/release/alpha-verdict.json: %v", err)
	}
	return v
}

func TestAlphaVerdict_FollowsTheVerdictRule(t *testing.T) {
	v := loadVerdict(t)
	if !allowedVerdicts[v.Verdict] {
		t.Fatalf("verdict %q is not one of ALPHA_READY, REWORK, STOP, CHƯA ĐỦ EVIDENCE", v.Verdict)
	}
	if v.Verdict == "ALPHA_READY" {
		if !v.GatePass {
			t.Fatal("ALPHA_READY may only be recorded when gatePass is true")
		}
		if len(v.Blockers) != 0 {
			t.Fatal("ALPHA_READY with blockers listed")
		}
		return
	}
	if v.GatePass {
		t.Fatalf("gatePass is true but the verdict is %s — a passing gate must be recorded as ALPHA_READY", v.Verdict)
	}
	if len(v.Blockers) == 0 {
		t.Fatalf("verdict %s must name its blocker(s)", v.Verdict)
	}
	tasks := map[string]bool{}
	for _, task := range v.NextTasks {
		if task.ID == "" || task.Title == "" || task.Scope == "" {
			t.Errorf("next task %+v needs an id, a title and a scope", task)
		}
		tasks[task.ID] = true
	}
	if len(tasks) == 0 {
		t.Fatalf("verdict %s must name the next narrow task", v.Verdict)
	}
	for _, b := range v.Blockers {
		if b.ID == "" || b.Detail == "" {
			t.Errorf("blocker %+v needs an id and a detail", b)
		}
		if !tasks[b.NextTask] {
			t.Errorf("blocker %s names next task %q, which is not in nextTasks", b.ID, b.NextTask)
		}
	}
}

func TestAlphaVerdict_SummaryIsInternallyConsistent(t *testing.T) {
	v := loadVerdict(t)
	s := v.Summary
	if got := s.Criteria.AlphaMust + s.Criteria.CrossPhaseGuard; got != s.AlphaGated.Total {
		t.Errorf("alphaMust+crossPhaseGuard = %d, alphaGatedCriteria.total = %d", got, s.AlphaGated.Total)
	}
	if got := s.AlphaGated.Total + s.Criteria.OutOfAlphaScope + s.Criteria.NotApplicable; got != s.Criteria.Assessed {
		t.Errorf("gated+outOfScope+notApplicable = %d, assessed = %d", got, s.Criteria.Assessed)
	}
	if s.AlphaGated.Pass+s.AlphaGated.Fail+s.AlphaGated.Insufficient != s.AlphaGated.Total {
		t.Errorf("alpha-gated pass+fail+insufficient does not add up to total: %+v", s.AlphaGated)
	}
	if s.WithTestEvidence+s.SuiteOnly != s.AlphaGated.Total {
		t.Errorf("test-evidence %d + suite-only %d != alpha-gated total %d", s.WithTestEvidence, s.SuiteOnly, s.AlphaGated.Total)
	}
	if s.FinalGates.Pass+s.FinalGates.Fail > s.FinalGates.Total {
		t.Errorf("finalGates %+v inconsistent", s.FinalGates)
	}

	allGreen := s.AlphaGated.Pass == s.AlphaGated.Total && s.Journeys.Pass == s.Journeys.Total &&
		s.VersionGates.Pass == s.VersionGates.Total && s.FinalGates.Pass == s.FinalGates.Total
	if allGreen != v.GatePass {
		t.Errorf("the summary is all green = %v but gatePass = %v", allGreen, v.GatePass)
	}
}

func TestAlphaVerdict_ReportAndStartHereAgreeWithTheRecord(t *testing.T) {
	v := loadVerdict(t)
	report := readRepoFile(t, "docs/release/alpha-release-report.md")
	startHere := readRepoFile(t, "docs/00-start-here.md")

	if want := "**Verdict: `" + v.Verdict + "`**"; !strings.Contains(report, want) {
		t.Errorf("the release report does not open with %q", want)
	}
	gate := "`gatePass = false`"
	if v.GatePass {
		gate = "`gatePass = true`"
	}
	if !strings.Contains(report, gate) {
		t.Errorf("the release report does not state %s", gate)
	}
	if want := "**Verdict Alpha: `" + v.Verdict + "`"; !strings.Contains(startHere, want) {
		t.Errorf("docs/00-start-here.md section 4A does not state %q", want)
	}
	short := v.AssessedCommit[:7]
	if !strings.Contains(report, v.AssessedCommit) || !strings.Contains(startHere, "`"+short+"`") {
		t.Errorf("the assessed commit %s is missing from the report or from start-here", v.AssessedCommit)
	}
	if !strings.Contains(report, v.AssessmentRun.URL) {
		t.Errorf("the report does not link the assessment run %s", v.AssessmentRun.URL)
	}

	for _, b := range v.Blockers {
		if !strings.Contains(report, b.ID) || !strings.Contains(startHere, b.ID) {
			t.Errorf("blocker %s must appear in both the report and start-here", b.ID)
		}
	}
	for _, task := range v.NextTasks {
		if !strings.Contains(report, task.ID) || !strings.Contains(startHere, task.ID) {
			t.Errorf("next task %s must appear in both the report and start-here", task.ID)
		}
	}

	// Known limitations: the ids in the record and the ids in the report are the same set.
	inRecord := map[string]bool{}
	for _, id := range v.KnownLimitations {
		inRecord[id] = true
		if !strings.Contains(report, "**"+id+" ") && !strings.Contains(report, "**"+id+"**") {
			t.Errorf("limitation %s is in the record but not described in the report", id)
		}
	}
	for _, id := range regexp.MustCompile(`\*\*(LIM-\d{2}) `).FindAllStringSubmatch(report, -1) {
		if !inRecord[id[1]] {
			t.Errorf("the report describes %s, which is not in the record's knownLimitations", id[1])
		}
	}

	if !strings.Contains(report, "**Status: "+v.LiveProviderCompatibility.Status+".**") {
		t.Errorf("the report does not state live provider compatibility status %s", v.LiveProviderCompatibility.Status)
	}
}

func TestAlphaVerdict_InstallArtifactChecksumsAreWellFormedAndPublished(t *testing.T) {
	v := loadVerdict(t)
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(v.AssessedCommit) {
		t.Errorf("assessedCommit %q is not a full SHA-1", v.AssessedCommit)
	}
	if !strings.HasSuffix(v.AssessmentRun.URL, "/actions/runs/"+itoa64(v.AssessmentRun.ID)) {
		t.Errorf("assessment run url %q does not end in the run id %d", v.AssessmentRun.URL, v.AssessmentRun.ID)
	}
	report := readRepoFile(t, "docs/release/alpha-release-report.md")
	platforms := map[string]bool{}
	sha := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for _, a := range v.InstallArtifacts {
		platforms[a.OS] = true
		if !sha.MatchString(a.SHA256) {
			t.Errorf("%s/%s: sha256 %q is not 64 hex characters", a.OS, a.File, a.SHA256)
		}
		if !a.ReproducedTwice {
			t.Errorf("%s/%s was not reproduced by a second build", a.OS, a.File)
		}
		if !strings.Contains(report, a.SHA256) || !strings.Contains(report, "`"+a.File+"`") {
			t.Errorf("%s/%s: its checksum or file name is missing from the release report", a.OS, a.File)
		}
	}
	for _, want := range []string{"windows", "linux"} {
		if !platforms[want] {
			t.Errorf("no install artifact recorded for %s", want)
		}
	}
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

var markdownLink = regexp.MustCompile(`\]\(([^)\s]+)\)`)

// TestReleaseAndOperatorDocsHaveNoBrokenRelativeLinks resolves every relative
// markdown link in the release docs, the operator docs and start-here.
func TestReleaseAndOperatorDocsHaveNoBrokenRelativeLinks(t *testing.T) {
	var files []string
	for _, pattern := range []string{"docs/release/*.md", "docs/operator/*.md", "docs/00-start-here.md"} {
		matches, err := filepath.Glob(filepath.Join(repoRoot, filepath.FromSlash(pattern)))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	if len(files) < 12 {
		t.Fatalf("found only %d markdown files to check — the glob or the layout changed", len(files))
	}
	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range markdownLink.FindAllStringSubmatch(string(content), -1) {
			target := m[1]
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "#") {
				continue
			}
			if i := strings.IndexByte(target, '#'); i >= 0 {
				target = target[:i]
			}
			if target == "" {
				continue
			}
			resolved := filepath.Join(filepath.Dir(file), filepath.FromSlash(target))
			if _, err := os.Stat(resolved); err != nil {
				rel, _ := filepath.Rel(repoRoot, file)
				t.Errorf("%s links to %q, which does not exist", filepath.ToSlash(rel), m[1])
			}
		}
	}
}
