package docscoverage

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return lines, nil
}

// phaseTableRow matches one row of a "| ID | `LABEL` | reason |" table.
var phaseTableRow = regexp.MustCompile("^\\| ([A-Z0-9-]+) \\| `([A-Z_]+)` \\| (.*) \\|$")

// parsePhaseSection scans lines starting after a "### Phase classification"
// (or "## NN. Phase classification"/"§14 Phase classification") heading for
// the default-label statement and the closed exceptions table, stopping at
// the next heading of equal-or-higher level. headingPrefixes lists the
// heading line(s) that open the subsection in this file (a file may have
// more than one candidate heading spelling across the three doc families).
func parsePhaseSection(lines []string, headingMatch func(string) bool, defaultMarker string) (defaultFound bool, rows []exceptionRow) {
	start := -1
	for i, line := range lines {
		if headingMatch(line) {
			start = i + 1
			break
		}
	}
	if start == -1 {
		return false, nil
	}
	for i := start; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "## ") {
			break // next top-level section
		}
		if strings.Contains(line, defaultMarker) {
			defaultFound = true
		}
		if m := phaseTableRow.FindStringSubmatch(line); m != nil {
			id, label, reason := m[1], m[2], strings.TrimSpace(m[3])
			if id == "Criterion" { // header row, e.g. "| Criterion | Nhãn | ... |"
				continue
			}
			rows = append(rows, exceptionRow{id: id, label: Label(label), reason: reason, line: i + 1})
		}
	}
	return defaultFound, rows
}

var akArchDefLine = regexp.MustCompile(`^- \*\*(AK-ARCH-\d{3}[A-Z]?):\*\*`)

func parseAKArch(architectureDir string) (criterionSet, error) {
	path := architectureDir + "/03-system-architecture.md"
	lines, err := readLines(path)
	if err != nil {
		return criterionSet{}, err
	}
	set := criterionSet{family: FamilyAKArch, labeled: map[string]bool{}, citableOnly: map[string]bool{}}
	for _, line := range lines {
		if m := akArchDefLine.FindStringSubmatch(line); m != nil {
			set.labeled[m[1]] = true
		}
	}
	set.defaultFound, set.exceptionRows = parsePhaseSection(lines,
		func(l string) bool { return strings.HasPrefix(l, "### Phase classification") },
		"Mặc định của mọi AK-ARCH trong tài liệu này là `ALPHA_MUST`")
	return set, nil
}

var heDefLine = regexp.MustCompile(`^- \*\*(HE-\d{2}-([MS])\d{2})`)

func parseHE(harnessDir string) (criterionSet, error) {
	set := criterionSet{family: FamilyHE, labeled: map[string]bool{}, citableOnly: map[string]bool{}}

	lectureFiles, err := globLectureFiles(harnessDir)
	if err != nil {
		return criterionSet{}, err
	}
	for _, path := range lectureFiles {
		lines, err := readLines(path)
		if err != nil {
			return criterionSet{}, err
		}
		for _, line := range lines {
			m := heDefLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			id, tier := m[1], m[2]
			if tier == "M" {
				set.labeled[id] = true
			} else {
				set.citableOnly[id] = true
			}
		}
	}

	overviewPath := harnessDir + "/00-tong-quan.md"
	overviewLines, err := readLines(overviewPath)
	if err != nil {
		return criterionSet{}, err
	}
	set.defaultFound, set.exceptionRows = parsePhaseSection(overviewLines,
		func(l string) bool { return strings.HasPrefix(l, "## 14. Phase classification") },
		"Mặc định của mọi `HE-NN-Mxx` là `ALPHA_MUST`")
	return set, nil
}

var gcDefLine = regexp.MustCompile(`^\d+\. \*\*(GC-(?:INV|ACC|DS)-\d{2}):\*\*`)

func parseGC(architectureDir string) (criterionSet, error) {
	path := architectureDir + "/04-go-core-spec.md"
	lines, err := readLines(path)
	if err != nil {
		return criterionSet{}, err
	}
	set := criterionSet{family: FamilyGC, labeled: map[string]bool{}, citableOnly: map[string]bool{}}
	for _, line := range lines {
		if m := gcDefLine.FindStringSubmatch(line); m != nil {
			set.labeled[m[1]] = true
		}
	}
	set.defaultFound, set.exceptionRows = parsePhaseSection(lines,
		func(l string) bool { return strings.HasPrefix(l, "### Phase classification") && sectionAfter22_1(lines, l) },
		"Mặc định của mọi tiêu chí trong ba mục này là `ALPHA_MUST`")
	return set, nil
}

// sectionAfter22_1 disambiguates go-core-spec.md's "### Phase
// classification" heading (there is exactly one) from any other doc that
// might reuse the same heading text; kept as a narrow guard rather than a
// line-number constant so the parser stays robust to unrelated edits above
// this section.
func sectionAfter22_1(lines []string, heading string) bool {
	sawTargets := false
	for _, l := range lines {
		if strings.Contains(l, "22.1 Downstream Alpha compliance targets") {
			sawTargets = true
		}
		if l == heading {
			return sawTargets
		}
	}
	return false
}

func globLectureFiles(harnessDir string) ([]string, error) {
	entries, err := readDir(harnessDir)
	if err != nil {
		return nil, err
	}
	var files []string
	lectureRe := regexp.MustCompile(`^\d{2}-lec-\d{2}-.*\.md$`)
	for _, e := range entries {
		if lectureRe.MatchString(e) {
			files = append(files, harnessDir+"/"+e)
		}
	}
	return files, nil
}

func readDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
