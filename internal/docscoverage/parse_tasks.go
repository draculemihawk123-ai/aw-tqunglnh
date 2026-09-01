package docscoverage

import (
	"regexp"
	"strings"
)

var taskHeading = regexp.MustCompile(`^## (V\d+-[0-9A-Z]+) `)
var nguonField = regexp.MustCompile(`^- \*\*Nguồn:\*\* (.+)$`)

// versionFiles are the eight V1..V8 task files V1-00B is scoped to
// (docs/design/03..10-*.md); order does not matter for the checker.
var versionFiles = []string{
	"03-v1-alpha-foundation.md",
	"04-v2-definition-plane.md",
	"05-v3-project-workspace.md",
	"06-v4-runtime-engine.md",
	"07-v5-execution-evidence.md",
	"08-v6-api-projections.md",
	"09-v7-alpha-ui.md",
	"10-v8-alpha-hardening.md",
}

// parseTasks extracts every V1..V8 Task ID and its Nguồn field (if any)
// from designDir. A task's Nguồn field is whatever "- **Nguồn:** ..." line
// appears before the next "## " heading; a task with no such line has
// HasNguon=false, which Rule c reports as a defect.
func parseTasks(designDir string) ([]Task, error) {
	var tasks []Task
	for _, file := range versionFiles {
		path := designDir + "/" + file
		lines, err := readLines(path)
		if err != nil {
			return nil, err
		}
		var current *Task
		flush := func() {
			if current != nil {
				tasks = append(tasks, *current)
				current = nil
			}
		}
		for i, line := range lines {
			if m := taskHeading.FindStringSubmatch(line); m != nil {
				flush()
				current = &Task{ID: m[1], File: file, Line: i + 1}
				continue
			}
			if current == nil {
				continue
			}
			if m := nguonField.FindStringSubmatch(line); m != nil {
				current.HasNguon = true
				current.RawSources = tokenizeNguon(m[1])
			}
		}
		flush()
	}
	return tasks, nil
}

var spkTableRow = regexp.MustCompile(`^\| (SPK-\d{2}) \| ([^|]+) \| `)

// parseSPKMap extracts the SPK-01..14 -> criteria table added to
// docs/design/02-v0-spike-verdict.md by V1-00B, so V0's SPKs count toward
// ALPHA_MUST ownership without V0 tasks themselves carrying a Nguồn field
// (00-roadmap.md §3's explicit exemption for V0).
func parseSPKMap(designDir string) (map[string]spkEntry, error) {
	path := designDir + "/02-v0-spike-verdict.md"
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	result := map[string]spkEntry{}
	for i, line := range lines {
		m := spkTableRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		id := m[1]
		raw := strings.TrimSpace(m[2])
		var criteria []string
		for _, tok := range strings.Split(raw, ",") {
			tok = strings.TrimSpace(tok)
			tok = strings.Trim(tok, "`")
			if tok != "" {
				criteria = append(criteria, tok)
			}
		}
		result[id] = spkEntry{id: id, criteria: criteria, line: i + 1}
	}
	return result, nil
}

var adrHeading = regexp.MustCompile(`^## \d+\. (ADR-\d{3})`)

func parseADRIDs(architectureDir string) (map[string]bool, error) {
	lines, err := readLines(architectureDir + "/02-architecture-decisions.md")
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, line := range lines {
		if m := adrHeading.FindStringSubmatch(line); m != nil {
			ids[m[1]] = true
		}
	}
	return ids, nil
}

var roadmapHeading = regexp.MustCompile(`^## (\d+[A-Za-z]?)\. `)

func parseRoadmapSections(designDir string) (map[string]bool, error) {
	lines, err := readLines(designDir + "/00-roadmap.md")
	if err != nil {
		return nil, err
	}
	sections := map[string]bool{}
	for _, line := range lines {
		if m := roadmapHeading.FindStringSubmatch(line); m != nil {
			sections[m[1]] = true
		}
	}
	return sections, nil
}
