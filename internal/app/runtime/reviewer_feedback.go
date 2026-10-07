// V9-17 — what a MAKER reads when a CHECKER sent it back.
//
// A workflow `implement → review (CHECKER) → implement` could loop, but the
// second `implement` had no way to learn WHY the reviewer chose `rework`: the
// reviewer's reason is its message, and nothing carried that message anywhere.
// The maker re-ran from the same task text and had to guess.
//
// A SUCCEEDED agent attempt now leaves an AGENT_OUTPUT Evidence row
// (agent_output_evidence.go). A MAKER AGENT NodeRun activated through an edge
// leaving a CHECKER AGENT node pins that row in its ContextSnapshot's
// EvidenceRefs (gatherReviewerFeedbackEvidenceRefs), exactly the way a maker
// sent back by a failing COMMAND or MACHINE_GATE pins that check's rows
// (check_failure_outcome.go, V9-02). AssembleAgentExecutionRequest then renders
// them as a short, labelled section of the prompt:
//
//	"reviewerFeedback": [{"reviewNode": "ai-review", "outcome": "rework",
//	                      "evidenceIds": [...], "message": "...", "fix": "..."}]
//
// The text comes from an immutable artifact named by the pinned row, so the
// same snapshot always renders the same bytes. Only schema v2 renders it: a
// snapshot that recorded no instructionSchemaVersion (v1) was scheduled before
// this row existed and pins no AGENT_OUTPUT row.
//
// The opposite direction is closed on purpose: a CHECKER never receives a
// MAKER's AGENT_OUTPUT row (gatherCheckerEvidenceRefs skips it). V5-12's checker
// input allowlist is "requirement, diff and evidence", never the maker's own
// account of its work, so that the review stays independent.
package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// maxReviewerFeedbackBytes bounds one reviewer's quoted message, so a very long
// review cannot swamp the maker's prompt (V9-07 owns the general budget). The
// start is kept: a review states its findings before its closing verdict line.
const maxReviewerFeedbackBytes = 8192

// reviewerFeedbackFix is the engine-owned line that says what the section asks
// of the maker. Nothing hashes the rendered bytes (ADR-032), so rewording it
// changes no snapshot hash.
const reviewerFeedbackFix = "The reviewer is authoritative for this round: make the changes its message asks for, then finish; do not argue that the review is wrong. The same review runs again after you finish, and the workflow continues only when it approves."

// instructionReviewerFeedback is one reviewer verdict as the prompt shows it.
type instructionReviewerFeedback struct {
	// ReviewNode is the key of the CHECKER AGENT node that sent the maker back.
	ReviewNode string `json:"reviewNode"`
	// Outcome is the outcome that reviewer reported, the one whose edge led here.
	Outcome string `json:"outcome"`
	// EvidenceIDs are the AGENT_OUTPUT rows the section was rendered from — the
	// references an operator follows to the same text.
	EvidenceIDs []string `json:"evidenceIds"`
	Message     string   `json:"message"`
	Fix         string   `json:"fix"`
}

// assembledReviewerFeedbackInput is what Phase 1 gathers for one reviewer
// attempt: its AGENT_OUTPUT row and the artifact it references. The artifact
// CONTENT is read in Phase 2, outside any transaction.
type assembledReviewerFeedbackInput struct {
	reviewNode string
	outcome    string
	attemptID  string
	evidence   runtimedomain.Evidence
	artifact   assembledCheckFailureArtifact
}

// gatherReviewerFeedbackEvidenceRefs resolves the EvidenceRefs of a MAKER AGENT
// NodeRun that was activated through an edge leaving a CHECKER AGENT node: the
// AGENT_OUTPUT row of that reviewer's own terminal attempt.
//
// Like gatherCheckFailureEvidenceRefs it reads which reviewer sent the maker
// here from the run's own history instead of storing anything new. The
// reviewer NodeRun is the one that is
//
//   - SUCCEEDED, on an AGENT node whose effective role is CHECKER,
//   - ActivationSequence exactly one before nodeRun's,
//   - in the same lineage (same BranchTokenID, so parallel FORK branches never
//     borrow each other's reviews), and
//   - whose selected-outcome edge actually leads to nodeRun's node.
//
// Anything else — the very first activation of the maker, an activation
// through another edge, a scope-expansion reactivation — yields nil, and the
// maker's snapshot is exactly what it was before V9-17. Within the reviewer's
// NodeRun only its LATEST SUCCEEDED attempt counts.
func gatherReviewerFeedbackEvidenceRefs(
	ctx context.Context, tx ports.Tx, run runtimedomain.WorkflowRun, document workflow.WorkflowDocument, nodeRun runtimedomain.NodeRun,
) ([]contextsnapshot.EvidenceRef, error) {
	if nodeRun.ActivationSequence == 0 {
		return nil, nil
	}
	nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, string(run.ID))
	if err != nil {
		return nil, err
	}
	var reviewer *runtimedomain.NodeRun
	for i := range nodeRuns {
		candidate := nodeRuns[i]
		if candidate.State != runtimedomain.NodeRunSucceeded || candidate.ActivationSequence+1 != nodeRun.ActivationSequence {
			continue
		}
		if !sameBranchLineage(candidate.BranchTokenID, nodeRun.BranchTokenID) {
			continue
		}
		node, ok := findNode(document, candidate.NodeKey)
		if !ok || node.Type != workflow.NodeAgent || node.Agent.EffectiveRole() != workflow.AgentRoleChecker {
			continue
		}
		if edge, ok := findEdge(document, node.Key, candidate.SelectedOutcome); !ok || edge.To != nodeRun.NodeKey {
			continue
		}
		reviewer = &candidate
		break
	}
	if reviewer == nil {
		return nil, nil
	}

	attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, string(run.ID))
	if err != nil {
		return nil, err
	}
	var latest *runtimedomain.ExecutionAttempt
	for i := range attempts {
		attempt := attempts[i]
		if attempt.NodeRunID != reviewer.ID || attempt.State != runtimedomain.ExecutionAttemptSucceeded {
			continue
		}
		if latest == nil || attempt.AttemptNumber > latest.AttemptNumber {
			latest = &attempt
		}
	}
	if latest == nil {
		return nil, nil
	}
	evidence, err := tx.Runtime().ListEvidenceForAttempt(ctx, string(latest.ID))
	if err != nil {
		return nil, err
	}
	var refs []contextsnapshot.EvidenceRef
	for _, row := range evidence {
		if row.Kind == runtimedomain.EvidenceKindAgentOutput {
			refs = append(refs, contextsnapshot.EvidenceRef{EvidenceID: string(row.ID)})
		}
	}
	return refs, nil
}

// gatherReviewerFeedbackInputs resolves snapshot's EvidenceRefs into the
// reviewer messages they carry: the AGENT_OUTPUT rows, each with its reviewer
// node, the outcome that reviewer reported and its artifact. Every other row is
// left to gatherCheckFailureInputs. Order is deterministic: by reviewer node
// key, then attempt id.
func gatherReviewerFeedbackInputs(ctx context.Context, tx ports.Tx, refs []contextsnapshot.EvidenceRef) ([]assembledReviewerFeedbackInput, error) {
	var inputs []assembledReviewerFeedbackInput
	for _, ref := range refs {
		row, err := tx.Runtime().GetEvidence(ctx, ref.EvidenceID)
		if err != nil {
			return nil, fmt.Errorf("runtime: load evidence %s pinned by the context snapshot: %w", ref.EvidenceID, err)
		}
		if row.Kind != runtimedomain.EvidenceKindAgentOutput || len(row.ArtifactReferences) == 0 {
			continue
		}
		nodeRun, err := tx.Runtime().GetNodeRun(ctx, string(row.NodeRunID))
		if err != nil {
			return nil, fmt.Errorf("runtime: load node run %s of evidence %s: %w", row.NodeRunID, row.ID, err)
		}
		artifactID := row.ArtifactReferences[0]
		record, err := tx.Artifacts().GetArtifact(ctx, artifactID)
		if err != nil {
			return nil, fmt.Errorf("runtime: load artifact %s of evidence %s: %w", artifactID, row.ID, err)
		}
		inputs = append(inputs, assembledReviewerFeedbackInput{
			reviewNode: nodeRun.NodeKey, outcome: nodeRun.SelectedOutcome, attemptID: string(row.AttemptID), evidence: row,
			artifact: assembledCheckFailureArtifact{
				id: artifactID, mediaType: record.MediaType,
				ref: ports.ArtifactRef{Locator: record.Locator, SHA256: record.ContentHash, Size: record.Size, ContentType: record.MediaType, Sensitivity: record.Sensitivity, Redacted: record.Redacted},
			},
		})
	}
	sort.Slice(inputs, func(i, j int) bool {
		if inputs[i].reviewNode != inputs[j].reviewNode {
			return inputs[i].reviewNode < inputs[j].reviewNode
		}
		return inputs[i].attemptID < inputs[j].attemptID
	})
	return inputs, nil
}

// renderReviewerFeedback is Phase 2: read each reviewer's message artifact. One
// that cannot be read fails the assembly, the same fail-closed discipline
// message content and check failures have.
func renderReviewerFeedback(ctx context.Context, store ports.ArtifactStore, inputs []assembledReviewerFeedbackInput) ([]instructionReviewerFeedback, error) {
	rendered := make([]instructionReviewerFeedback, 0, len(inputs))
	for _, input := range inputs {
		body, err := readArtifact(ctx, store, input.artifact.ref)
		if err != nil {
			return nil, fmt.Errorf("runtime: open reviewer feedback artifact %s: %w", input.artifact.id, err)
		}
		rendered = append(rendered, instructionReviewerFeedback{
			ReviewNode: input.reviewNode, Outcome: input.outcome, EvidenceIDs: []string{string(input.evidence.ID)},
			Message: headOfReviewerMessage(strings.TrimSpace(body), maxReviewerFeedbackBytes), Fix: reviewerFeedbackFix,
		})
	}
	return rendered, nil
}

// headOfReviewerMessage returns the first at-most-limit bytes of s, ending at a
// rune boundary, followed by an ellipsis line when anything was cut.
func headOfReviewerMessage(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := s[:limit]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut + "\n...(the rest of the review is omitted; the full text is in the evidence row named by evidenceIds)"
}
