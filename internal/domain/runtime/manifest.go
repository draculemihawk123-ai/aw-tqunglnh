package runtime

import (
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// ExecutionManifestID identifies one immutable ExecutionManifest row.
type ExecutionManifestID string

// ExecutionManifest is a WorkflowRun's own immutable pin bundle (ADR-011,
// GC-INV-06): "WorkflowRun pin WorkflowVersion, CompiledSnapshotHash và
// dependency manifest trong suốt lifetime; initial manifest không đổi, scope
// mới chỉ được append bằng RunManifestAmendment." It exists as a table
// distinct from workflow_runs specifically so the pin survives even if a
// future task ever needed to mutate workflow_runs' own workflow_version_id
// column — today nothing does, but ExecutionManifest is the durable proof
// either way, never updated once inserted.
//
// ExecutionProfileHash and ContextRoutePolicyHash are optional: no task
// before V4-01 resolves a default ExecutionProfile or context-route
// PolicyVersion at run-start time, so both stay empty until a real caller
// (V4-04's node scheduling, most likely) has one to pin.
type ExecutionManifest struct {
	ID                     ExecutionManifestID
	RunID                  WorkflowRunID
	WorkflowVersionID      workflow.WorkflowVersionID
	CompiledSnapshotHash   string
	DependencyManifest     workflow.DependencyManifest
	BaseRevisionSet        workspace.RevisionSet
	ExecutionProfileHash   string
	ContextRoutePolicyHash string
	CreatedAt              time.Time
}

// NewExecutionManifest validates and builds the initial, immutable manifest
// for a WorkflowRun. CompiledSnapshotHash and DependencyManifest are
// expected to be the exact values read off the pinned WorkflowVersion (the
// same "verify the caller's claim against the loaded source" discipline
// StartWorkflowRun's own pinnedVersion.ContentHash() comparison already
// uses) — this constructor only enforces shape, not that cross-check; the
// repository layer resolves and compares against the real WorkflowVersion.
func NewExecutionManifest(
	id ExecutionManifestID,
	runID WorkflowRunID,
	workflowVersionID workflow.WorkflowVersionID,
	compiledSnapshotHash string,
	dependencyManifest workflow.DependencyManifest,
	baseRevisionSet workspace.RevisionSet,
	executionProfileHash string,
	contextRoutePolicyHash string,
	createdAt time.Time,
) (ExecutionManifest, error) {
	if id == "" || runID == "" || workflowVersionID == "" {
		return ExecutionManifest{}, errors.New("execution manifest identities are required")
	}
	compiledSnapshotHash = strings.TrimSpace(compiledSnapshotHash)
	if compiledSnapshotHash == "" {
		return ExecutionManifest{}, errors.New("execution manifest compiled snapshot hash is required")
	}
	if createdAt.IsZero() {
		return ExecutionManifest{}, errors.New("execution manifest created timestamp is required")
	}
	revisions, err := workspace.NewRevisionSet(baseRevisionSet.Entries())
	if err != nil {
		return ExecutionManifest{}, err
	}
	return ExecutionManifest{
		ID:                     id,
		RunID:                  runID,
		WorkflowVersionID:      workflowVersionID,
		CompiledSnapshotHash:   compiledSnapshotHash,
		DependencyManifest:     dependencyManifest,
		BaseRevisionSet:        revisions,
		ExecutionProfileHash:   strings.TrimSpace(executionProfileHash),
		ContextRoutePolicyHash: strings.TrimSpace(contextRoutePolicyHash),
		CreatedAt:              createdAt.UTC(),
	}, nil
}

// RunManifestAmendmentID identifies one append-only RunManifestAmendment row.
type RunManifestAmendmentID string

// RunManifestAmendment records one approved scope expansion for a run
// without ever touching its immutable ExecutionManifest (ADR-011). Revision
// is a strictly increasing per-run counter starting at 1; PreviousRevision
// must equal the run's highest existing revision (0 means "amends the
// initial manifest directly") — the repository layer enforces this
// continuity, since it requires reading the run's current highest amendment
// revision.
type RunManifestAmendment struct {
	ID                   RunManifestAmendmentID
	RunID                WorkflowRunID
	Revision             uint64
	PreviousRevision     uint64
	ApprovedScopeVersion uint64
	Reason               string
	ApprovedBy           string
	ApprovedAt           time.Time
	ContentHash          string
}

// NewRunManifestAmendment validates and builds one amendment record.
func NewRunManifestAmendment(
	id RunManifestAmendmentID,
	runID WorkflowRunID,
	revision uint64,
	previousRevision uint64,
	approvedScopeVersion uint64,
	reason string,
	approvedBy string,
	approvedAt time.Time,
	contentHash string,
) (RunManifestAmendment, error) {
	if id == "" || runID == "" {
		return RunManifestAmendment{}, errors.New("run manifest amendment identities are required")
	}
	if revision == 0 {
		return RunManifestAmendment{}, errors.New("run manifest amendment revision must be greater than zero")
	}
	if revision != previousRevision+1 {
		return RunManifestAmendment{}, errors.New("run manifest amendment revision must be exactly one greater than its previous revision")
	}
	if approvedScopeVersion == 0 {
		return RunManifestAmendment{}, errors.New("run manifest amendment approved scope version must be greater than zero")
	}
	reason = strings.TrimSpace(reason)
	approvedBy = strings.TrimSpace(approvedBy)
	contentHash = strings.TrimSpace(contentHash)
	if reason == "" || approvedBy == "" || contentHash == "" {
		return RunManifestAmendment{}, errors.New("run manifest amendment reason, approver and content hash are required")
	}
	if approvedAt.IsZero() {
		return RunManifestAmendment{}, errors.New("run manifest amendment approved timestamp is required")
	}
	return RunManifestAmendment{
		ID:                   id,
		RunID:                runID,
		Revision:             revision,
		PreviousRevision:     previousRevision,
		ApprovedScopeVersion: approvedScopeVersion,
		Reason:               reason,
		ApprovedBy:           approvedBy,
		ApprovedAt:           approvedAt.UTC(),
		ContentHash:          contentHash,
	}, nil
}
