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

// EvidenceVerdictSucceeded is the fixed Verdict a COMMAND execution's own
// Evidence row uses — Command has no gate.Verdict-shaped vocabulary of its
// own (PASS/FAIL/ERROR/NOT_RUN/NOT_APPLICABLE); reaching this Evidence row
// at all already means the execution SUCCEEDED (a FAILED Command never
// proposes Evidence in this remediation's own PR1 scope).
const EvidenceVerdictSucceeded = "SUCCEEDED"

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
