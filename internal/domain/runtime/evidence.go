// Evidence (V5-09/V5-10 acceptance-gap remediation, 2026-09-10 post-merge
// review — docs/design/07-v5-execution-evidence.md's own "GateResult
// artifact không thay thế criteria-level Evidence") is the durable,
// full-lineage record a terminal ExecutionAttempt produces for one
// criterion (MACHINE_GATE) or one execution (COMMAND/AGENT) — never a
// stand-in artifact row that only an executor's own internal code can
// interpret. Reuses the `evidence` table migration 0001 already declared
// (WorkItem/Run/NodeRun/Attempt lineage, Verdict, RevisionSet,
// PolicyVersion) but never wrote to until this remediation.
//
// ID is deterministic — attemptID plus this Evidence row's own Kind — the
// identical "idempotent by ID" discipline work.ReleaseSet/
// work.WorkItemBlocker already establish: a redelivered finalize for the
// SAME Attempt always re-derives the SAME ID, so CreateEvidence's own
// insert-or-load-existing semantics (ports.RuntimeRepository's own doc
// comment) are what make replay safe, not a separate idempotency-key
// column. A retry creates a NEW ExecutionAttempt (a new AttemptID), so its
// own Evidence rows are structurally distinct from the attempt it
// replaces — never reused, never merged.
package runtime

import (
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// EvidenceID identifies one durable Evidence row.
type EvidenceID string

// EvidenceKindCommandExecution is the fixed Kind a COMMAND node's own
// single Evidence row uses — a Command has no per-criterion breakdown the
// way a MACHINE_GATE does (whose own Kind is instead that Criterion's own
// EvidenceKey).
const EvidenceKindCommandExecution = "COMMAND_EXECUTION"

// EvidenceKindAgentExecution is the fixed Kind an AGENT node's own single
// Evidence row uses (V9-01): its artifact references are the attempt's diff
// manifests, so a CHECKER scheduled after this node finds — via the
// EvidenceRefs of its ContextSnapshot (gatherCheckerEvidenceRefs) — the
// uncommitted change this node left, "requirement, diff và evidence" as
// V5-12 intended. Like COMMAND_EXECUTION it is a single row per execution,
// not a per-criterion breakdown.
const EvidenceKindAgentExecution = "AGENT_EXECUTION"

// EvidenceKindAgentOutput is the fixed Kind of the Evidence row an AGENT
// attempt leaves for what the agent SAID (V9-17): its one artifact is the
// agent's final message, redacted, as plain text. Two readers need it. An
// operator reads it with the evidence and artifact commands — before this row
// the only place the words existed was the agent_events table, which no API
// exposes. And a MAKER sent back by a CHECKER reads it as the reviewer's
// feedback in its prompt (gatherReviewerFeedbackEvidenceRefs): the reason a
// CHECKER chose `rework` is nothing but its message. The row is a record, not
// a verdict (EvidenceVerdictRecorded), so it can never satisfy a
// CompletionPolicy. It is a separate Kind from EvidenceKindAgentExecution,
// whose artifacts are the diff manifests, so the row id (AttemptID plus Kind)
// of neither changes.
const EvidenceKindAgentOutput = "AGENT_OUTPUT"

// EvidenceVerdictRecorded is the fixed Verdict an AGENT execution's Evidence
// row carries: the row RECORDS what the agent produced (its diff manifests),
// it does not VERIFY it. It is deliberately not one of the verdicts
// completion accepts (SUCCEEDED, PASS, NOT_APPLICABLE — isPassingVerdict), so
// an agent's own claim can never satisfy a CompletionPolicy's
// RequiredEvidenceKinds on its own; only an independent check (a
// MACHINE_GATE, a COMMAND, an approval) can, exactly as before this row
// existed.
const EvidenceVerdictRecorded = "RECORDED"

// EvidenceVerdictSucceeded is the fixed Verdict a COMMAND execution's own
// Evidence row uses when the process exited 0 with its output intact —
// Command has no gate.Verdict-shaped vocabulary of its own
// (PASS/FAIL/ERROR/NOT_RUN/NOT_APPLICABLE). An AGENT execution's row uses
// EvidenceVerdictRecorded instead; a COMMAND that did not succeed uses
// EvidenceVerdictFailed.
const EvidenceVerdictSucceeded = "SUCCEEDED"

// EvidenceVerdictFailed is the Verdict a COMMAND execution's Evidence row
// carries when the process finished but did not succeed (V9-02, ADR-031
// decision 4): it exited on its own with a non-zero code (a functional
// failure), or exited 0 with output that was cut and so cannot be trusted. It
// is deliberately not one of the verdicts completion accepts (SUCCEEDED, PASS,
// NOT_APPLICABLE — isPassingVerdict), so a check whose latest activation
// failed never satisfies a CompletionPolicy's RequiredEvidenceKinds. The row
// exists whether or not the node declares a failureOutcome: it only adds
// data, it changes no transition.
const EvidenceVerdictFailed = "FAILED"

// Evidence is one terminal ExecutionAttempt's own durable, full-lineage
// record for one criterion (MACHINE_GATE: Kind is that Criterion's own
// EvidenceKey) or its one execution (COMMAND/AGENT: Kind names the kind of
// execution, e.g. "COMMAND_EXECUTION").
type Evidence struct {
	ID         EvidenceID
	ProjectID  project.ProjectID
	WorkItemID workdomain.WorkItemID
	RunID      WorkflowRunID
	NodeRunID  NodeRunID
	AttemptID  ExecutionAttemptID
	Kind       string
	Verdict    string
	// ArtifactReferences names every durable Artifact this Evidence row's
	// own manifest depends on — mirrors Checkpoint.ArtifactReferences
	// exactly (sorted, deduplicated, non-empty entries).
	ArtifactReferences []string
	Revisions          workspace.RevisionSet
	PolicyVersion      string
	CreatedAt          time.Time
}

// NewEvidence validates and builds a new Evidence row.
func NewEvidence(
	id EvidenceID, projectID project.ProjectID, workItemID workdomain.WorkItemID, runID WorkflowRunID, nodeRunID NodeRunID,
	attemptID ExecutionAttemptID, kind string, verdict string, artifactReferences []string, revisions workspace.RevisionSet,
	policyVersion string, createdAt time.Time,
) (Evidence, error) {
	if id == "" || projectID == "" || workItemID == "" || runID == "" || nodeRunID == "" || attemptID == "" {
		return Evidence{}, errors.New("evidence identities are required")
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return Evidence{}, errors.New("evidence kind is required")
	}
	verdict = strings.TrimSpace(verdict)
	if verdict == "" {
		return Evidence{}, errors.New("evidence verdict is required")
	}
	policyVersion = strings.TrimSpace(policyVersion)
	if policyVersion == "" {
		return Evidence{}, errors.New("evidence policy version is required")
	}
	if createdAt.IsZero() {
		return Evidence{}, errors.New("evidence created timestamp is required")
	}
	references, err := normalizeArtifactReferences(artifactReferences)
	if err != nil {
		return Evidence{}, err
	}
	if len(references) == 0 {
		return Evidence{}, errors.New("evidence requires at least one artifact reference")
	}
	copyOfRevisions, err := workspace.NewRevisionSet(revisions.Entries())
	if err != nil {
		return Evidence{}, err
	}
	return Evidence{
		ID: id, ProjectID: projectID, WorkItemID: workItemID, RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID,
		Kind: kind, Verdict: verdict, ArtifactReferences: references, Revisions: copyOfRevisions,
		PolicyVersion: policyVersion, CreatedAt: createdAt.UTC(),
	}, nil
}
