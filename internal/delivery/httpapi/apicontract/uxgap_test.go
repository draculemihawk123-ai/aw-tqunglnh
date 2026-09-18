package apicontract

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"testing"
)

// uxDocRelativePath locates docs/design/11-v6-00-ux-artifact.md from this
// package's own directory (internal/delivery/httpapi/apicontract) — four
// levels up reaches the repository root.
const uxDocRelativePath = "../../../../docs/design/11-v6-00-ux-artifact.md"

// parseRealUXDoc reads and parses the real, committed
// docs/design/11-v6-00-ux-artifact.md — every test in this file that needs
// UX-doc rows calls this rather than embedding its own copy, so a doc edit
// is picked up by every test uniformly.
func parseRealUXDoc(t *testing.T) []UXRow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(uxDocRelativePath))
	if err != nil {
		t.Fatalf("read %s: %v", uxDocRelativePath, err)
	}
	rows := ParseUXDoc(string(data))
	if len(rows) == 0 {
		t.Fatalf("ParseUXDoc returned zero rows for %s — parser or file path is broken", uxDocRelativePath)
	}
	return rows
}

// buildRealContract builds the Contract for the real, fully-composed
// production route set (see composetest_test.go's own buildRealRegistry).
func buildRealContract(t *testing.T) Contract {
	t.Helper()
	return Build(buildRealRegistry(t).Descriptors())
}

func assertStringSliceEqual(t *testing.T, label string, got, want []string) {
	t.Helper()
	gotSorted := append([]string(nil), got...)
	wantSorted := append([]string(nil), want...)
	sort.Strings(gotSorted)
	sort.Strings(wantSorted)
	if !reflect.DeepEqual(gotSorted, wantSorted) {
		t.Errorf("%s changed — got %v, want %v (see this test's own doc comment before updating the pinned expectation)",
			label, gotSorted, wantSorted)
	}
}

// TestParseUXDoc_RealDocument is a structural smoke test over the real
// document: a stable row count (pinned, like every other snapshot in this
// package) plus one specific, hand-verified row, proving the parser is
// reading the real file correctly rather than silently returning nothing
// or garbage.
func TestParseUXDoc_RealDocument(t *testing.T) {
	rows := parseRealUXDoc(t)

	const wantRowCount = 72
	if len(rows) != wantRowCount {
		t.Errorf("parsed %d UX doc rows, want exactly %d — if the doc was intentionally edited, "+
			"review the diff before updating this pinned count", len(rows), wantRowCount)
	}

	var doctorRow *UXRow
	for i := range rows {
		if len(rows[i].OperationIDs) == 1 && rows[i].OperationIDs[0] == "getInstallationDoctorReport" {
			doctorRow = &rows[i]
			break
		}
	}
	if doctorRow == nil {
		t.Fatal("expected a row proposing operationId getInstallationDoctorReport (§2 Screen 1 row 1) — not found")
	}
	if !doctorRow.ChuaCoGap {
		t.Error("getInstallationDoctorReport row: ChuaCoGap = false, want true (doc marks it [CHƯA CÓ])")
	}
	if doctorRow.OwnerTaskID != "V6-10A" {
		t.Errorf("getInstallationDoctorReport row: OwnerTaskID = %q, want %q", doctorRow.OwnerTaskID, "V6-10A")
	}

	// The escaped-pipe row (§12 Screen 11 row 4, "aw artifact get --output
	// <path\|->") must parse its Owner Task ID as the real "V6-07B", never
	// a shifted "`GetArtifactContent` [CHƯA CÓ]..." fragment from the
	// column before it (see uxdoc.go's own escapedPipePlaceholder doc
	// comment for the bug this specifically guards against).
	var artifactContentRow *UXRow
	for i := range rows {
		if len(rows[i].OperationIDs) == 1 && rows[i].OperationIDs[0] == "getArtifactContent" {
			artifactContentRow = &rows[i]
			break
		}
	}
	if artifactContentRow == nil {
		t.Fatal("expected a row proposing operationId getArtifactContent (§12 Screen 11 row 4) — not found")
	}
	if artifactContentRow.OwnerTaskID != "V6-07B" {
		t.Errorf("getArtifactContent row: OwnerTaskID = %q, want %q (escaped-pipe column misalignment?)",
			artifactContentRow.OwnerTaskID, "V6-07B")
	}
}

// TestCheckUXGaps_NoUnresolvedGap is V6-12's own "dependency/reference
// checker" Verify bullet — the hard gate. It cross-references every
// "[CHƯA CÓ]" row in the real UX doc against the real, fully-composed
// Contract and fails the build the moment ANY finding is
// GapOpenUnresolved: by this package's own review (see uxgap.go's own
// knownRenamedProposals/knownUnimplementedGaps doc comments), every
// "[CHƯA CÓ]" gap in the document today is accounted for one of three
// ways — actually registered (IMPLEMENTED_DOC_STALE), registered under a
// reviewed rename (IMPLEMENTED_RENAMED), or a real, already-acknowledged
// open gap in an already-merged task's own deliverable
// (OPEN_GAP_ACKNOWLEDGED) — so zero UNRESOLVED findings is the correct,
// current state, not an aspirational one.
//
// The second half of this test locks the ACKNOWLEDGED set to exactly
// knownUnimplementedGaps' own key set: if a future leaf task closes
// `listDefinitions` (the one entry there today), this test starts
// failing — not because anything is wrong, but as the forcing function to
// go delete that now-stale entry from knownUnimplementedGaps. Symmetrically,
// if some OTHER, previously-unknown gap were to appear (a scenario this
// package's own git history should never actually reach, but that a
// future run of this exact test — against a possibly-edited UX doc —
// could hit for real), it would show up as GapOpenUnresolved, and the
// first half of this test would fail loudly rather than silently
// swallowing it into "probably fine".
func TestCheckUXGaps_NoUnresolvedGap(t *testing.T) {
	rows := parseRealUXDoc(t)
	contract := buildRealContract(t)

	findings := CheckUXGaps(rows, contract)
	if len(findings) == 0 {
		t.Fatal("CheckUXGaps returned zero findings — expected at least the known IMPLEMENTED_DOC_STALE/" +
			"IMPLEMENTED_RENAMED/OPEN_GAP_ACKNOWLEDGED findings; either the UX doc or the real route " +
			"composition changed in a way that broke this check's own assumptions")
	}

	var unresolved []UXGapFinding
	var acknowledgedIDs []string
	counts := map[GapStatus]int{}
	for _, f := range findings {
		counts[f.Status]++
		switch f.Status {
		case GapOpenUnresolved:
			unresolved = append(unresolved, f)
		case GapOpenAcknowledged:
			acknowledgedIDs = append(acknowledgedIDs, f.OperationID)
		}
	}

	if len(unresolved) > 0 {
		t.Errorf("%d unresolved UX-doc gap(s) — a proposed operationId is neither registered, nor a "+
			"reviewed rename, nor an acknowledged open gap; add a knownRenamedProposals or "+
			"knownUnimplementedGaps entry after verifying which case applies:", len(unresolved))
		for _, f := range unresolved {
			t.Errorf("  %s (owner=%s, section=%s)", f.OperationID, f.OwnerTaskID, f.Section)
		}
	}

	wantAcknowledged := make([]string, 0, len(knownUnimplementedGaps))
	for id := range knownUnimplementedGaps {
		wantAcknowledged = append(wantAcknowledged, id)
	}
	assertStringSliceEqual(t, "OPEN_GAP_ACKNOWLEDGED operationId set", acknowledgedIDs, wantAcknowledged)

	t.Logf("CheckUXGaps: %d findings — %d IMPLEMENTED_DOC_STALE, %d IMPLEMENTED_RENAMED, "+
		"%d OPEN_GAP_ACCEPTABLE, %d OPEN_GAP_ACKNOWLEDGED, %d OPEN_GAP_UNRESOLVED",
		len(findings), counts[GapImplementedDocStale], counts[GapImplementedRenamed],
		counts[GapOpenAcceptable], counts[GapOpenAcknowledged], counts[GapOpenUnresolved])
}

// TestKnownRenamedProposalsAreActuallyRegistered re-verifies every target
// operationId in knownRenamedProposals against the real Contract, so that
// map can never silently rot into pointing at an operationId that was
// itself later renamed or removed — a mismatch here means the rename
// entry itself needs re-review, not that CheckUXGaps should trust it
// blindly.
func TestKnownRenamedProposalsAreActuallyRegistered(t *testing.T) {
	contract := buildRealContract(t)
	registered := make(map[string]bool, len(contract.Operations))
	for _, op := range contract.Operations {
		registered[op.OperationID] = true
	}

	for proposed, actualIDs := range knownRenamedProposals {
		for _, actual := range actualIDs {
			if !registered[actual] {
				t.Errorf("knownRenamedProposals[%q] names %q, which is not a currently-registered "+
					"operationId — this rename entry is stale and needs re-review", proposed, actual)
			}
		}
	}
}

// mergedTaskIDPattern extracts every "V6-NN" or "V6-NNX" token from an
// Owner Task ID cell's raw text (e.g. "V6-10I (hardening) → V6-10J
// (route)" yields ["V6-10I", "V6-10J"]).
var mergedTaskIDPattern = regexp.MustCompile(`V6-\d+[A-Z]?`)

// alreadyMergedTasks is every V6 backend Task ID this package's own
// review confirms is already merged as of V6-12's own snapshot — see
// knownOpenOwnerTasks' own doc comment (uxgap.go) for the full dependency-
// chain reasoning (V6-12's own explicit dependency list, plus V6-09B's
// transitive chain through V6-08/V6-08A/V6-09/V6-09A, plus V6-10F/V6-10D's
// own transitive {V6-10C, V6-10E} dependencies).
var alreadyMergedTasks = map[string]bool{
	"V6-00": true, "V6-00A": true, "V6-01": true, "V6-01A": true, "V6-02": true, "V6-02A": true,
	"V6-03": true, "V6-03A": true,
	"V6-04": true, "V6-04A": true,
	"V6-05": true,
	"V6-06": true, "V6-06A": true, "V6-06B": true, "V6-06C": true, "V6-06D": true,
	"V6-07": true, "V6-07A": true, "V6-07B": true,
	"V6-08": true, "V6-08A": true,
	"V6-09": true, "V6-09A": true, "V6-09B": true,
	"V6-10": true, "V6-10A": true, "V6-10B": true, "V6-10C": true, "V6-10D": true, "V6-10E": true,
	"V6-10F": true, "V6-10G": true, "V6-10H": true, "V6-10I": true, "V6-10J": true,
	"V6-11": true,
}

// TestEveryOwnerTaskIDIsInTheKnownMergedSet is the mechanical proof behind
// knownOpenOwnerTasks' own doc comment claim that it is allowed to be
// empty today: it extracts every distinct "V6-NN" token appearing in ANY
// row's own Owner Task ID cell across the whole real UX doc, and asserts
// each one is a member of alreadyMergedTasks above. A token that is NOT a
// member would mean either (a) alreadyMergedTasks itself is stale/wrong —
// review and fix it — or (b) a genuinely open Owner Task ID exists that
// knownOpenOwnerTasks should now name explicitly.
func TestEveryOwnerTaskIDIsInTheKnownMergedSet(t *testing.T) {
	rows := parseRealUXDoc(t)

	seen := map[string]bool{}
	for _, row := range rows {
		for _, token := range mergedTaskIDPattern.FindAllString(row.OwnerTaskID, -1) {
			seen[token] = true
		}
	}
	if len(seen) == 0 {
		t.Fatal("found zero V6-NN tokens across every parsed row's own Owner Task ID — parser regression?")
	}

	var notMerged []string
	for token := range seen {
		if !alreadyMergedTasks[token] {
			notMerged = append(notMerged, token)
		}
	}
	sort.Strings(notMerged)
	if len(notMerged) > 0 {
		t.Errorf("Owner Task ID token(s) not in alreadyMergedTasks: %v — either alreadyMergedTasks is "+
			"stale, or these are genuinely open and belong in knownOpenOwnerTasks instead", notMerged)
	}
}
