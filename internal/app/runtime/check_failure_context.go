// V9-02 (ADR-031 decision 6) — what a MAKER reads when the check after it
// failed. A MAKER AGENT NodeRun activated through the edge leaving a check's
// failureOutcome has the failing attempt's Evidence rows in its
// ContextSnapshot (gatherCheckFailureEvidenceRefs); this file turns those rows
// into a short, plainly labelled section of the prompt:
//
//	"checkFailures": [{"checkNode": "test", "evidenceIds": [...],
//	                   "what": "...", "why": "...", "fix": "..."}]
//
// WHAT says which check failed and how it ended, WHY quotes the evidence — the
// tail of stderr for a COMMAND (stdout if it printed nothing to stderr), the
// failing criteria for a gate — and FIX says what is expected next. The text is
// derived from the pinned Evidence rows and their immutable artifacts, so the
// same snapshot always renders the same bytes ("cùng snapshot tạo cùng
// instruction hash"), and the artifacts were redacted when they were written
// (persistCommandOutputArtifact, persistGateResultArtifact).
//
// Deliberately minimal: a labelled section, not a priority or ordering scheme.
// It sits right after the task contract in both instruction schemas: in v1
// (before the messages) and in v2 (V9-03, instruction_artifact.go: after the
// task contract, before resources, messages and the closing checklist — the
// most specific instruction a maker sent back by a check has must not be buried
// under reference material). A prompt without a failing check has no such key
// at all (omitempty), so every other prompt is unchanged.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// maxCheckFailureTailBytes bounds each quoted block of a check failure — the
// stderr/stdout tail of a COMMAND, the criteria listing of a gate — so a noisy
// test run cannot swamp the maker's prompt (G7/V9-07 owns the general context
// budget). The tail is the END of the output because that is where test
// runners print what failed.
const maxCheckFailureTailBytes = 4096

// checkFailureFix is the FIX line of every check failure. The engine does not
// know how to fix a failing test; what it does know is what happens next, and
// that is what the maker needs in order not to stop early.
//
// V9-13b (live finding F3): the first wording only said to "address the cause",
// and a real model read its own file, found it matched the task text, and
// declared the work done — in every live run that got that far — or argued that
// the check contradicted the task. The check, not the maker's reading of the
// task, decides whether the work continues, so the line now says that, says to
// make the change rather than dispute the check, and still states what happens
// next. It is engine-owned text, not part of the artifact's structure
// (ADR-032): nothing hashes the rendered bytes, so rewording it changes no
// snapshot hash.
//
// Only a v2 artifact carries this wording. A v1 artifact (a snapshot without
// instructionSchemaVersion) keeps checkFailureFixV1 byte for byte, because
// ADR-032 promises such a snapshot assembles "exactly as before".
const checkFailureFixV1 = "Address the cause described under 'why'. The same check runs again after you finish, and the workflow continues only when it passes."

const checkFailureFix = "The check is authoritative: your work is not done until it passes, even if it already looks correct to you or the check asks for more than the task text says. Make the change that 'why' describes, then finish; do not argue that the check is wrong. The same check runs again after you finish, and the workflow continues only when it passes."

// instructionCheckFailure is one failing check attempt as the prompt shows it.
type instructionCheckFailure struct {
	// CheckNode is the key of the COMMAND or MACHINE_GATE node that failed.
	CheckNode string `json:"checkNode"`
	// EvidenceIDs are the Evidence rows of that attempt, as pinned by the
	// maker's ContextSnapshot — the references an operator follows to the full
	// output.
	EvidenceIDs []string `json:"evidenceIds"`
	What        string   `json:"what"`
	Why         string   `json:"why,omitempty"`
	// Baseline (V9-08) says how the repository stood BEFORE this task: whether
	// its own baseline passed (so a failing check is a regression of this task)
	// or had already failed (so the failure may not be the task's). Absent when
	// no write repository has a baseline.
	Baseline string `json:"baseline,omitempty"`
	Fix      string `json:"fix"`
}

// assembledCheckFailureInput is what Phase 1 (the read-only transaction) of
// AssembleAgentExecutionRequest gathers for one failing check attempt: its
// Evidence rows and the artifacts they reference. The artifact CONTENT is read
// in Phase 2, outside any transaction.
type assembledCheckFailureInput struct {
	checkNode string
	attemptID string
	evidence  []runtimedomain.Evidence
	artifacts []assembledCheckFailureArtifact
	// baseline is the V9-08 note about the repositories' baseline, the same for
	// every failure of one prompt (see baselineNoteAt).
	baseline string
}

type assembledCheckFailureArtifact struct {
	id        string
	mediaType string
	ref       ports.ArtifactRef
}

// failingEvidenceVerdict reports whether verdict marks a check that did not
// pass — the verdicts a check attempt can leave that completion never accepts.
// RECORDED (an agent's own diff record) is not one of them.
func failingEvidenceVerdict(verdict string) bool {
	switch verdict {
	case runtimedomain.EvidenceVerdictFailed, string(gate.VerdictFail), string(gate.VerdictError), string(gate.VerdictNotRun):
		return true
	default:
		return false
	}
}

// gatherCheckFailureInputs resolves snapshot's EvidenceRefs into the failing
// check attempts they belong to. Rows that are not failing are kept with their
// attempt (a gate's passing criteria sit next to its failing one) but an
// attempt with no failing row is dropped. Order is deterministic: by check node
// key, then attempt id.
func gatherCheckFailureInputs(ctx context.Context, tx ports.Tx, refs []contextsnapshot.EvidenceRef) ([]assembledCheckFailureInput, error) {
	byAttempt := make(map[string]*assembledCheckFailureInput)
	var order []string
	for _, ref := range refs {
		row, err := tx.Runtime().GetEvidence(ctx, ref.EvidenceID)
		if err != nil {
			return nil, fmt.Errorf("runtime: load evidence %s pinned by the context snapshot: %w", ref.EvidenceID, err)
		}
		attemptID := string(row.AttemptID)
		group, ok := byAttempt[attemptID]
		if !ok {
			nodeRun, err := tx.Runtime().GetNodeRun(ctx, string(row.NodeRunID))
			if err != nil {
				return nil, fmt.Errorf("runtime: load node run %s of evidence %s: %w", row.NodeRunID, row.ID, err)
			}
			group = &assembledCheckFailureInput{checkNode: nodeRun.NodeKey, attemptID: attemptID}
			byAttempt[attemptID] = group
			order = append(order, attemptID)
		}
		group.evidence = append(group.evidence, row)
	}

	var inputs []assembledCheckFailureInput
	for _, attemptID := range order {
		group := byAttempt[attemptID]
		failing := false
		for _, row := range group.evidence {
			if failingEvidenceVerdict(row.Verdict) {
				failing = true
			}
		}
		if !failing {
			continue
		}
		sort.Slice(group.evidence, func(i, j int) bool { return group.evidence[i].ID < group.evidence[j].ID })
		seen := make(map[string]bool)
		for _, row := range group.evidence {
			for _, artifactID := range row.ArtifactReferences {
				if seen[artifactID] {
					continue
				}
				seen[artifactID] = true
				record, err := tx.Artifacts().GetArtifact(ctx, artifactID)
				if err != nil {
					return nil, fmt.Errorf("runtime: load artifact %s of evidence %s: %w", artifactID, row.ID, err)
				}
				group.artifacts = append(group.artifacts, assembledCheckFailureArtifact{
					id: artifactID, mediaType: record.MediaType,
					ref: ports.ArtifactRef{Locator: record.Locator, SHA256: record.ContentHash, Size: record.Size, ContentType: record.MediaType, Sensitivity: record.Sensitivity, Redacted: record.Redacted},
				})
			}
		}
		inputs = append(inputs, *group)
	}
	sort.Slice(inputs, func(i, j int) bool {
		if inputs[i].checkNode != inputs[j].checkNode {
			return inputs[i].checkNode < inputs[j].checkNode
		}
		return inputs[i].attemptID < inputs[j].attemptID
	})
	return inputs, nil
}

// renderCheckFailures is Phase 2: read each attempt's artifacts and compose its
// WHAT/WHY/FIX. An artifact of a kind this renderer does not know contributes
// nothing to WHY (the row's own kind and verdict still make WHAT); one that
// cannot be read fails the assembly, the same fail-closed discipline message
// content has.
func renderCheckFailures(ctx context.Context, store ports.ArtifactStore, inputs []assembledCheckFailureInput) ([]instructionCheckFailure, error) {
	rendered := make([]instructionCheckFailure, 0, len(inputs))
	for _, input := range inputs {
		failure := instructionCheckFailure{CheckNode: input.checkNode, Baseline: input.baseline, Fix: checkFailureFix}
		for _, row := range input.evidence {
			failure.EvidenceIDs = append(failure.EvidenceIDs, string(row.ID))
		}
		var whats, whys []string
		for _, art := range input.artifacts {
			body, err := readArtifact(ctx, store, art.ref)
			if err != nil {
				return nil, fmt.Errorf("runtime: open check failure artifact %s: %w", art.id, err)
			}
			switch art.mediaType {
			case commandOutputArtifactMediaType:
				what, why, ok := summarizeCommandFailure(input.checkNode, body)
				if ok {
					whats = append(whats, what)
					if why != "" {
						whys = append(whys, why)
					}
				}
			case gateResultArtifactMediaType:
				what, why, ok := summarizeGateFailure(input.checkNode, body)
				if ok {
					whats = append(whats, what)
					if why != "" {
						whys = append(whys, why)
					}
				}
			}
		}
		if len(whats) == 0 {
			whats = append(whats, fmt.Sprintf("check %q failed.", input.checkNode))
		}
		failure.What = strings.Join(whats, " ")
		failure.Why = strings.Join(whys, "\n")
		rendered = append(rendered, failure)
	}
	return rendered, nil
}

// summarizeCommandFailure renders a COMMAND_EXECUTION record: WHAT is how the
// process ended, WHY is the tail of its stderr — or of its stdout when it wrote
// nothing to stderr, since many test runners report on stdout. ok is false when
// body is not a command record.
func summarizeCommandFailure(checkNode, body string) (what, why string, ok bool) {
	var record commandOutputArtifactContent
	if err := json.Unmarshal([]byte(body), &record); err != nil {
		return "", "", false
	}
	what = fmt.Sprintf("check %q failed: the command exited with code %d", checkNode, record.ExitCode)
	if record.ExitCode == 0 && record.Truncated {
		what = fmt.Sprintf("check %q failed: the command exited 0 but its output was cut, so the result cannot be trusted", checkNode)
	}
	if record.Truncated && record.ExitCode != 0 {
		what += " (its output was cut at the command's maxOutputBytes)"
	}
	what += "."
	switch {
	case strings.TrimSpace(record.Stderr) != "":
		why = "stderr (last part):\n" + tailOf(record.Stderr, maxCheckFailureTailBytes)
	case strings.TrimSpace(record.Stdout) != "":
		why = "stdout (last part; stderr was empty):\n" + tailOf(record.Stdout, maxCheckFailureTailBytes)
	}
	return what, why, true
}

// summarizeGateFailure renders a GateResult: WHAT is the overall verdict, WHY
// lists the criteria that did not pass. ok is false when body is not a gate
// result.
func summarizeGateFailure(checkNode, body string) (what, why string, ok bool) {
	var result GateResult
	if err := json.Unmarshal([]byte(body), &result); err != nil || result.OverallVerdict == "" {
		return "", "", false
	}
	var lines []string
	for _, criterion := range result.Criteria {
		if criterion.Verdict == gate.VerdictPass || criterion.Verdict == gate.VerdictNotApplicable {
			continue
		}
		line := fmt.Sprintf("- %s (%s): %s", criterion.Name, criterion.EvidenceKey, criterion.Verdict)
		if strings.TrimSpace(criterion.Detail) != "" {
			line += " — " + strings.TrimSpace(criterion.Detail)
		}
		lines = append(lines, line)
	}
	what = fmt.Sprintf("check %q failed: gate verdict %s, %d of %d criteria did not pass.", checkNode, result.OverallVerdict, len(lines), len(result.Criteria))
	if len(lines) > 0 {
		why = "failed criteria:\n" + headOf(strings.Join(lines, "\n"), maxCheckFailureTailBytes)
	}
	return what, why, true
}

// tailOf returns the last at-most-limit bytes of s, starting at a rune
// boundary, prefixed with an ellipsis line when anything was cut.
func tailOf(s string, limit int) string {
	if len(s) <= limit {
		return strings.TrimRight(s, "\r\n")
	}
	cut := s[len(s)-limit:]
	for len(cut) > 0 && !utf8.RuneStart(cut[0]) {
		cut = cut[1:]
	}
	return "...(earlier output omitted)\n" + strings.TrimRight(cut, "\r\n")
}

// headOf returns the first at-most-limit bytes of s, ending at a rune boundary,
// followed by an ellipsis line when anything was cut.
func headOf(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := s[:limit]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "\n...(further criteria omitted)"
}
