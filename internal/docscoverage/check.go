package docscoverage

import (
	"fmt"
	"strings"
)

type resolvedCriterion struct {
	family Family
	label  Label
}

// Run parses every doc file V1-00C's coverage checker depends on and
// returns a Report whose Violations are computed fresh each call — never a
// hard-coded debt number (00-roadmap.md §5A: "không hard-code con số debt
// vào tài liệu vì nó lệch ngay khi task đầu tiên nhận Nguồn").
func Run(repoRoot string) (Report, error) {
	archDir := repoRoot + "/docs/architecture"
	harnessDir := repoRoot + "/docs/harness-engineering"
	designDir := repoRoot + "/docs/design"

	akArch, err := parseAKArch(archDir)
	if err != nil {
		return Report{}, err
	}
	he, err := parseHE(harnessDir)
	if err != nil {
		return Report{}, err
	}
	gc, err := parseGC(archDir)
	if err != nil {
		return Report{}, err
	}
	tasks, err := parseTasks(designDir)
	if err != nil {
		return Report{}, err
	}
	spkMap, err := parseSPKMap(designDir)
	if err != nil {
		return Report{}, err
	}
	adrIDs, err := parseADRIDs(archDir)
	if err != nil {
		return Report{}, err
	}
	roadmapSections, err := parseRoadmapSections(designDir)
	if err != nil {
		return Report{}, err
	}

	var report Report

	registry := map[string]resolvedCriterion{}
	for _, set := range []criterionSet{akArch, he, gc} {
		resolveFamily(set, registry, &report)
	}

	resolves := func(kind tokenKind, token string) bool {
		switch kind {
		case tokenADR:
			return adrIDs[token]
		case tokenRoadmap:
			return roadmapSections[strings.TrimPrefix(token, "ROADMAP-§")]
		case tokenAKArch:
			return akArch.labeled[token]
		case tokenHE:
			return he.labeled[token] || he.citableOnly[token]
		case tokenGCInv, tokenGCAcc, tokenGCDs:
			return gc.labeled[token]
		default:
			return false
		}
	}
	validateTokens := func(tokens []string, context string) {
		for _, tok := range tokens {
			kind := classifyToken(tok)
			if kind == tokenInvalid {
				report.add("d", fmt.Sprintf("%s: token %q does not match any SourceRef grammar (00-roadmap.md §3)", context, tok))
				continue
			}
			if !resolves(kind, tok) {
				report.add("d", fmt.Sprintf("%s: token %q matches grammar but does not resolve to a real item", context, tok))
			}
		}
	}

	// Rule c: every V1..V8 task must declare a Nguồn field.
	for _, t := range tasks {
		if !t.HasNguon {
			report.add("c", fmt.Sprintf("%s (%s:%d) has no Nguồn field", t.ID, t.File, t.Line))
			continue
		}
		validateTokens(t.RawSources, fmt.Sprintf("task %s (%s:%d)", t.ID, t.File, t.Line))
	}

	// Rule g: every SPK-01..14 must map to at least one criterion, and
	// every criterion it cites must itself be a valid, resolvable token.
	for i := 1; i <= 14; i++ {
		id := fmt.Sprintf("SPK-%02d", i)
		entry, ok := spkMap[id]
		if !ok || len(entry.criteria) == 0 {
			report.add("g", fmt.Sprintf("%s is not mapped to any criterion in docs/design/02-v0-spike-verdict.md", id))
			continue
		}
		validateTokens(entry.criteria, fmt.Sprintf("SPK map %s (line %d)", id, entry.line))
	}

	// Rule b: every ALPHA_MUST criterion needs an owner — a V1..V8 Task ID
	// or a V0 SPK (00-roadmap.md §3's coverage-map requirement).
	owned := map[string]bool{}
	for _, t := range tasks {
		for _, tok := range t.RawSources {
			owned[tok] = true
		}
	}
	for _, entry := range spkMap {
		for _, c := range entry.criteria {
			owned[c] = true
		}
	}
	for id, rc := range registry {
		if rc.label == AlphaMust && !owned[id] {
			report.add("b", fmt.Sprintf("%s is ALPHA_MUST but is not owned by any Task ID or SPK", id))
		}
	}

	return report, nil
}

// resolveFamily applies Rules a, e and f to one criterion family's phase
// classification (default statement + exceptions table), then records each
// labeled criterion's resolved Label into registry for Rule b to consume.
func resolveFamily(set criterionSet, registry map[string]resolvedCriterion, report *Report) {
	rowsByID := map[string][]exceptionRow{}
	for _, row := range set.exceptionRows {
		rowsByID[row.id] = append(rowsByID[row.id], row)
	}

	for id, rows := range rowsByID {
		if len(rows) > 1 {
			report.add("e", fmt.Sprintf("%s appears in %d phase-classification rows in family %s (must carry exactly one label)", id, len(rows), set.family))
		}
		for _, row := range rows {
			if !validLabel(row.label) {
				report.add("a", fmt.Sprintf("%s: phase table row at line %d has invalid label %q", id, row.line, row.label))
			}
			if row.label == NotApplicable && strings.TrimSpace(row.reason) == "" {
				report.add("f", fmt.Sprintf("%s: NOT_APPLICABLE row at line %d has no authority reason", id, row.line))
			}
		}
		if !set.labeled[id] {
			report.add("d", fmt.Sprintf("phase-classification table in family %s cites %s, which is not a defined criterion", set.family, id))
		}
	}

	for id := range set.labeled {
		if !set.defaultFound {
			report.add("a", fmt.Sprintf("%s: family %s's default-ALPHA_MUST statement was not found in its source doc — cannot derive a phase label", id, set.family))
			continue
		}
		label := AlphaMust
		if rows, ok := rowsByID[id]; ok && len(rows) > 0 && validLabel(rows[0].label) {
			label = rows[0].label
		}
		registry[id] = resolvedCriterion{family: set.family, label: label}
	}
}
