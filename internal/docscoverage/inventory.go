package docscoverage

import (
	"sort"
	"strings"
)

// Criterion is one phase-labeled acceptance criterion exactly as V1-00C's
// own parsers resolve it: the label is the one ADR-024 fixed before V1
// started (family default ALPHA_MUST, or the closed exceptions table) and is
// never recomputed or reinterpreted here — V8-11 consumes it, it does not
// reclassify (docs/design/10-v8-alpha-hardening.md V8-11: "V8 không được
// phân loại lại criteria").
type Criterion struct {
	ID     string
	Family Family
	Label  Label
	// Reason is the classification table's own free text for this criterion
	// (the authority reason for NOT_APPLICABLE, the "Alpha part that still
	// has to be done" for the Beta/cross-phase rows); empty for a plain
	// default-ALPHA_MUST criterion.
	Reason string
	// OwnerTasks are the V1..V8 Task IDs whose Nguồn field names this
	// criterion; OwnerSPKs are the V0 spikes whose coverage-map row names
	// it. Ownership is what the existing coverage checker already enforces
	// for every ALPHA_MUST — this just exposes who the owners are.
	OwnerTasks []string
	OwnerSPKs  []string
}

// Decision is one architecture decision record (ADR-NNN). ADRs are decision
// sources, not acceptance criteria (00-roadmap.md §3: "ADR và ROADMAP là
// nguồn quyết định, còn AK-ARCH/HE/GC-* mới là criterion mang nhãn phase"),
// so they carry owners but no phase label.
type Decision struct {
	ID         string
	OwnerTasks []string
}

// Inventory is the whole coverage map in one machine-readable value.
type Inventory struct {
	// Criteria is every phase-labeled criterion, sorted by ID.
	Criteria []Criterion
	// Decisions is every ADR, sorted by ID.
	Decisions []Decision
	// Violations is the coverage checker's own full debt list for the same
	// repository — Run's result, not a second opinion; Debt() is its length.
	Violations []Violation
}

// Debt is the number of SourceRef/coverage violations the V1-00C checker
// found (0 is the only acceptable value for a release).
func (i Inventory) Debt() int { return len(i.Violations) }

// LoadInventory parses the same doc files Run does and returns the resolved
// criteria with their owners, the ADR list with its owners, and Run's own
// violations. It only reads.
func LoadInventory(repoRoot string) (Inventory, error) {
	report, err := Run(repoRoot)
	if err != nil {
		return Inventory{}, err
	}

	archDir := repoRoot + "/docs/architecture"
	harnessDir := repoRoot + "/docs/harness-engineering"
	designDir := repoRoot + "/docs/design"

	akArch, err := parseAKArch(archDir)
	if err != nil {
		return Inventory{}, err
	}
	he, err := parseHE(harnessDir)
	if err != nil {
		return Inventory{}, err
	}
	gc, err := parseGC(archDir)
	if err != nil {
		return Inventory{}, err
	}
	tasks, err := parseTasks(designDir)
	if err != nil {
		return Inventory{}, err
	}
	spkMap, err := parseSPKMap(designDir)
	if err != nil {
		return Inventory{}, err
	}
	adrIDs, err := parseADRIDs(archDir)
	if err != nil {
		return Inventory{}, err
	}

	registry := map[string]resolvedCriterion{}
	reasons := map[string]string{}
	var scratch Report // resolveFamily's own violations are already in `report` (Run)
	for _, set := range []criterionSet{akArch, he, gc} {
		resolveFamily(set, registry, &scratch)
		for _, row := range set.exceptionRows {
			reasons[row.id] = strings.TrimSpace(row.reason)
		}
	}

	taskOwners := map[string][]string{}
	for _, t := range tasks {
		for _, token := range t.RawSources {
			taskOwners[token] = appendUnique(taskOwners[token], t.ID)
		}
	}
	spkOwners := map[string][]string{}
	for _, entry := range spkMap {
		for _, token := range entry.criteria {
			spkOwners[token] = appendUnique(spkOwners[token], entry.id)
		}
	}

	inventory := Inventory{Violations: report.Violations}
	for id, rc := range registry {
		inventory.Criteria = append(inventory.Criteria, Criterion{
			ID: id, Family: rc.family, Label: rc.label, Reason: reasons[id],
			OwnerTasks: sortedCopy(taskOwners[id]), OwnerSPKs: sortedCopy(spkOwners[id]),
		})
	}
	sort.Slice(inventory.Criteria, func(a, b int) bool { return inventory.Criteria[a].ID < inventory.Criteria[b].ID })

	for id := range adrIDs {
		inventory.Decisions = append(inventory.Decisions, Decision{ID: id, OwnerTasks: sortedCopy(taskOwners[id])})
	}
	sort.Slice(inventory.Decisions, func(a, b int) bool { return inventory.Decisions[a].ID < inventory.Decisions[b].ID })
	return inventory, nil
}

func appendUnique(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}

func sortedCopy(list []string) []string {
	out := append([]string(nil), list...)
	sort.Strings(out)
	return out
}
