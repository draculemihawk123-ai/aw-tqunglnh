package docscoverage

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClassifyToken(t *testing.T) {
	cases := []struct {
		token string
		want  tokenKind
	}{
		{"ADR-024", tokenADR},
		{"AK-ARCH-001", tokenAKArch},
		{"AK-ARCH-005A", tokenAKArch},
		{"HE-06-M08", tokenHE},
		{"HE-11-S06", tokenHE},
		{"GC-INV-35", tokenGCInv},
		{"GC-ACC-12", tokenGCAcc},
		{"GC-DS-10", tokenGCDs},
		{"ROADMAP-§7", tokenRoadmap},
		{"ROADMAP-§5B", tokenRoadmap},
		{"AK-ARCH-1", tokenInvalid},       // wrong digit count
		{"HE-6-M08", tokenInvalid},        // wrong digit count
		{"GC-INV-035", tokenInvalid},      // wrong digit count
		{"ROADMAP-7", tokenInvalid},       // missing §
		{"AK-ARCH-001 ", tokenInvalid},    // untrimmed whitespace is the caller's job, not the classifier's
		{"", tokenInvalid},
		{"NOT-A-REF-1", tokenInvalid},
	}
	for _, c := range cases {
		got := classifyToken(c.token)
		if got != c.want {
			t.Errorf("classifyToken(%q) = %q, want %q", c.token, got, c.want)
		}
	}
}

func TestTokenizeNguon(t *testing.T) {
	got := tokenizeNguon("ADR-024, ROADMAP-§3.")
	want := []string{"ADR-024", "ROADMAP-§3"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tokenizeNguon = %#v, want %#v", got, want)
	}
	if tokenizeNguon("") != nil {
		t.Errorf("tokenizeNguon(\"\") should be nil")
	}
	if tokenizeNguon("   .") != nil {
		t.Errorf("tokenizeNguon of whitespace-only should be nil")
	}
}

// TestRun_RealRepo runs the checker against this repository's own docs — the
// actual V1-00C CI gate. A non-zero debt here is real: it means some
// ALPHA_MUST criterion, task, or SPK genuinely lacks the coverage
// 00-roadmap.md §3 requires, and the report below names exactly which.
func TestRun_RealRepo(t *testing.T) {
	report, err := Run(findModuleRoot(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Debt() != 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "coverage debt = %d\n", report.Debt())
		for _, v := range report.Violations {
			fmt.Fprintf(&b, "  [%s] %s\n", v.Rule, v.Detail)
		}
		t.Error(b.String())
	}
}

func findModuleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file location")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test file")
		}
		dir = parent
	}
}

// --- synthetic fixture: proves each rule actually fires, independent of
// whatever debt (or lack of it) the real repo currently has. ---

// validFixture returns a minimal but complete doc tree with zero debt: one
// criterion per family under the default label, one exception per family,
// every ALPHA_MUST criterion owned by either a task or the SPK map, all 14
// SPKs mapped, and every V1..V8 file carrying at least one task with a
// resolvable Nguồn field.
func writeValidFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWriteFile(t, root, "docs/architecture/02-architecture-decisions.md", strings.Join([]string{
		"# Architecture decisions",
		"",
		"## 2. ADR-001 — Test decision",
		"Content.",
		"",
	}, "\n"))
	mustWriteFile(t, root, "docs/architecture/03-system-architecture.md", strings.Join([]string{
		"# System architecture",
		"",
		"## 17. Architecture acceptance criteria",
		"",
		"- **AK-ARCH-001:** Test criterion one.",
		"- **AK-ARCH-002:** Test criterion two.",
		"",
		"### Phase classification",
		"",
		"Mặc định của mọi AK-ARCH trong tài liệu này là `ALPHA_MUST`; các ngoại lệ dưới đây là danh sách đóng.",
		"",
		"| Criterion | Nhãn | Phần Alpha vẫn phải làm |",
		"|---|---|---|",
		"| AK-ARCH-002 | `NOT_APPLICABLE` | Test reason. |",
		"",
		"## 18. Next section",
		"",
	}, "\n"))
	mustWriteFile(t, root, "docs/architecture/04-go-core-spec.md", strings.Join([]string{
		"# Go core spec",
		"",
		"## 5. Invariant bắt buộc",
		"",
		"1. **GC-INV-01:** Test invariant one.",
		"",
		"## 22. Acceptance gate của Go spike",
		"",
		"1. **GC-ACC-01:** Test acceptance one.",
		"",
		"### 22.1 Downstream Alpha compliance targets — test",
		"",
		"1. **GC-DS-01:** **V1:** Test downstream one.",
		"",
		"### Phase classification",
		"",
		"Mặc định của mọi tiêu chí trong ba mục này là `ALPHA_MUST`.",
		"",
		"| Criterion | Nhãn | Phần Alpha vẫn phải làm |",
		"|---|---|---|",
		"",
		"## 23. Next",
		"",
	}, "\n"))
	mustWriteFile(t, root, "docs/harness-engineering/00-tong-quan.md", strings.Join([]string{
		"# Harness overview",
		"",
		"## 14. Phase classification",
		"",
		"Mặc định của mọi `HE-NN-Mxx` là `ALPHA_MUST`. Ngoại lệ dưới đây là danh sách đóng:",
		"",
		"| Criterion | Nhãn | Lý do |",
		"|---|---|---|",
		"| HE-01-M02 | `NOT_APPLICABLE` | Test reason. |",
		"",
		"## 15. Next",
		"",
	}, "\n"))
	mustWriteFile(t, root, "docs/harness-engineering/01-lec-01-test.md", strings.Join([]string{
		"# Lec 01",
		"",
		"## Tiêu chí bắt buộc",
		"",
		"- **HE-01-M01 — Test:** MUST test one.",
		"- **HE-01-M02 — Test:** MUST test two.",
		"",
		"## Tiêu chí nên có",
		"",
		"- **HE-01-S01:** SHOULD test one.",
		"",
	}, "\n"))
	mustWriteFile(t, root, "docs/design/00-roadmap.md", strings.Join([]string{
		"# Roadmap",
		"",
		"## 1. Authority",
		"Text.",
		"",
		"## 3. Nguyên tắc chia task",
		"Text.",
		"",
		"## 7. Chính sách schema",
		"Text.",
		"",
	}, "\n"))

	var spkRows strings.Builder
	spkRows.WriteString("| SPK | Criterion | Vì sao |\n|---|---|---|\n")
	for i := 1; i <= 14; i++ {
		fmt.Fprintf(&spkRows, "| SPK-%02d | AK-ARCH-001 | reason |\n", i)
	}
	mustWriteFile(t, root, "docs/design/02-v0-spike-verdict.md", "# V0\n\n## SPK map\n\n"+spkRows.String())

	mustWriteFile(t, root, "docs/design/03-v1-alpha-foundation.md", strings.Join([]string{
		"# V1",
		"",
		"## V1-01 — Test task",
		"- **Mục tiêu:** test.",
		"- **Nguồn:** GC-INV-01, GC-ACC-01, GC-DS-01, HE-01-M01, ADR-001, ROADMAP-§7.",
		"",
	}, "\n"))
	for i, name := range versionFiles[1:] {
		vn := fmt.Sprintf("V%d", i+2)
		mustWriteFile(t, root, "docs/design/"+name, strings.Join([]string{
			"# " + vn,
			"",
			"## " + vn + "-01 — Test task",
			"- **Mục tiêu:** test.",
			"- **Nguồn:** ADR-001.",
			"",
		}, "\n"))
	}
	return root
}

func mustWriteFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func TestRun_ValidFixtureHasZeroDebt(t *testing.T) {
	root := writeValidFixture(t)
	report, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Debt() != 0 {
		t.Fatalf("valid fixture should have zero debt, got %d: %+v", report.Debt(), report.Violations)
	}
}

func TestRun_CatchesMissingNguon(t *testing.T) {
	root := writeValidFixture(t)
	mustWriteFile(t, root, "docs/design/03-v1-alpha-foundation.md", strings.Join([]string{
		"# V1",
		"",
		"## V1-01 — Test task",
		"- **Mục tiêu:** test.",
		"",
	}, "\n"))
	report, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasRule(report, "c", "V1-01") {
		t.Fatalf("expected a rule-c violation naming V1-01, got: %+v", report.Violations)
	}
}

func TestRun_CatchesInvalidSourceRefGrammar(t *testing.T) {
	root := writeValidFixture(t)
	mustWriteFile(t, root, "docs/design/03-v1-alpha-foundation.md", strings.Join([]string{
		"# V1",
		"",
		"## V1-01 — Test task",
		"- **Mục tiêu:** test.",
		"- **Nguồn:** GC-INV-01, GC-ACC-01, GC-DS-01, HE-01-M01, ADR-001, ROADMAP-§7, NOT-A-VALID-REF.",
		"",
	}, "\n"))
	report, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasRule(report, "d", "NOT-A-VALID-REF") {
		t.Fatalf("expected a rule-d violation naming the malformed token, got: %+v", report.Violations)
	}
}

func TestRun_CatchesSourceRefThatDoesNotResolve(t *testing.T) {
	root := writeValidFixture(t)
	mustWriteFile(t, root, "docs/design/03-v1-alpha-foundation.md", strings.Join([]string{
		"# V1",
		"",
		"## V1-01 — Test task",
		"- **Mục tiêu:** test.",
		"- **Nguồn:** GC-INV-01, GC-ACC-01, GC-DS-01, HE-01-M01, ADR-001, ROADMAP-§7, AK-ARCH-999.",
		"",
	}, "\n"))
	report, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasRule(report, "d", "AK-ARCH-999") {
		t.Fatalf("expected a rule-d violation naming the unresolved criterion, got: %+v", report.Violations)
	}
}

func TestRun_CatchesUnownedAlphaMust(t *testing.T) {
	root := writeValidFixture(t)
	// Drop GC-INV-01 from the one task that owned it; nothing else cites it.
	mustWriteFile(t, root, "docs/design/03-v1-alpha-foundation.md", strings.Join([]string{
		"# V1",
		"",
		"## V1-01 — Test task",
		"- **Mục tiêu:** test.",
		"- **Nguồn:** GC-ACC-01, GC-DS-01, HE-01-M01, ADR-001, ROADMAP-§7.",
		"",
	}, "\n"))
	report, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasRule(report, "b", "GC-INV-01") {
		t.Fatalf("expected a rule-b violation naming the unowned criterion, got: %+v", report.Violations)
	}
}

func TestRun_CatchesUnmappedSPK(t *testing.T) {
	root := writeValidFixture(t)
	var spkRows strings.Builder
	spkRows.WriteString("| SPK | Criterion | Vì sao |\n|---|---|---|\n")
	for i := 1; i <= 13; i++ { // SPK-14 deliberately omitted
		fmt.Fprintf(&spkRows, "| SPK-%02d | AK-ARCH-001 | reason |\n", i)
	}
	mustWriteFile(t, root, "docs/design/02-v0-spike-verdict.md", "# V0\n\n## SPK map\n\n"+spkRows.String())
	report, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasRule(report, "g", "SPK-14") {
		t.Fatalf("expected a rule-g violation naming SPK-14, got: %+v", report.Violations)
	}
}

func TestRun_CatchesMissingAuthorityReason(t *testing.T) {
	root := writeValidFixture(t)
	content, err := os.ReadFile(filepath.Join(root, "docs/harness-engineering/00-tong-quan.md"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	broken := strings.Replace(string(content),
		"| HE-01-M02 | `NOT_APPLICABLE` | Test reason. |",
		"| HE-01-M02 | `NOT_APPLICABLE` |  |", 1)
	mustWriteFile(t, root, "docs/harness-engineering/00-tong-quan.md", broken)
	report, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasRule(report, "f", "HE-01-M02") {
		t.Fatalf("expected a rule-f violation naming HE-01-M02, got: %+v", report.Violations)
	}
}

func TestRun_CatchesDuplicateLabel(t *testing.T) {
	root := writeValidFixture(t)
	content, err := os.ReadFile(filepath.Join(root, "docs/architecture/03-system-architecture.md"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	broken := strings.Replace(string(content),
		"| AK-ARCH-002 | `NOT_APPLICABLE` | Test reason. |",
		"| AK-ARCH-002 | `NOT_APPLICABLE` | Test reason. |\n| AK-ARCH-002 | `CROSS_PHASE_GUARD` | Second row. |", 1)
	mustWriteFile(t, root, "docs/architecture/03-system-architecture.md", broken)
	report, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hasRule(report, "e", "AK-ARCH-002") {
		t.Fatalf("expected a rule-e violation naming AK-ARCH-002, got: %+v", report.Violations)
	}
}

func hasRule(report Report, rule, substring string) bool {
	for _, v := range report.Violations {
		if v.Rule == rule && strings.Contains(v.Detail, substring) {
			return true
		}
	}
	return false
}
