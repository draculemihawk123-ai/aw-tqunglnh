package ports

import (
	"context"
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// NodeExecutionRequest is what ExecuteNodeHandler (internal/app/runtime,
// V4-05) hands to a NodeExecutor to actually perform one ExecutionAttempt's
// own work.
type NodeExecutionRequest struct {
	AttemptID            string
	NodeRunID            string
	RunID                string
	ExecutorKind         string
	ExecutionProfileHash string
	// JobLease is populated now (V5-08B): the exact fencing proof the
	// driving EXECUTE_NODE job carries at the moment ExecuteNodeHandler
	// invokes Execute — a real executor (the NodeExecutor->AgentExecutor
	// bridge) needs this to fence its own AcquireWriteLeases call and to
	// construct an agentevents.Sink whose every write is fenced against
	// the same job. Unused by the fake NodeExecutor V4-05's own tests
	// still use.
	JobLease JobLease
}

// NodeExecutionResult is what a NodeExecutor proposes back. This proposal
// is deliberately never trusted or committed as-is (GC-INV-17/18):
// FinalizeExecutionAttempt is the sole authority that decides whether to
// accept it, based on whether the JobLease/WriteLease fencing this
// executor ran under is still valid at acceptance time — "worker chỉ
// propose outcome; orchestrator quyết transition" (V4-05's own Hoàn thành
// khi). State is only ever SUCCEEDED, FAILED or (V4-12A) BLOCKED: TIMED_OUT
// and the "cancelled context, do not finalize" case are both decided by
// the envelope itself observing its own derived-deadline context, never
// proposed by the executor as a result value (see ExecuteNodeHandler's own
// doc comment for why).
type NodeExecutionResult struct {
	State             runtime.ExecutionAttemptState
	TerminationReason runtime.TerminationReason
	// SelectedOutcome is the NodeRun outcome to route on — required (and
	// must name one of the node's own declared Outcomes; AdvanceRun's own
	// GC-INV-11 allow-list check is the actual enforcement point) when
	// State == SUCCEEDED, meaningless otherwise.
	SelectedOutcome string
	// ErrorCode is populated now (V4-06): required when State == FAILED —
	// the exact errorcode.Code (go-core-spec §18) this executor classifies
	// its own failure as, so ExecuteNodeHandler's retry decision
	// (FinalizeExecutionAttempt) can check it against the pinned
	// AttemptRules.RetryableErrorCodes allow-list. Never populated for
	// State == SUCCEEDED. TIMED_OUT is deliberately NOT a value this type
	// itself ever carries — the execution envelope's own derived deadline
	// decides that outcome, never the executor (see ExecuteNodeHandler's
	// own doc comment).
	ErrorCode     errorcode.Code
	ResultPayload json.RawMessage
	// RequestedScopeExpansion is populated now (V4-12A, confirmed with the
	// user before writing this task's code): required when State ==
	// ExecutionAttemptBlocked, meaningless (and rejected as
	// OUTCOME_REJECTED if present) otherwise — mutually exclusive with
	// SelectedOutcome/ErrorCode, exactly like those two are already
	// mutually exclusive with each other. The executor only ever
	// PROPOSES; FinalizeExecutionAttempt validates/canonicalizes it before
	// anything durable (a ScopeExpansionOrigin, a BLOCKED Attempt/NodeRun)
	// is ever built from it — never itself a grant.
	RequestedScopeExpansion *runtime.ScopeExpansionProposal
	// Evidence is populated only when State == ExecutionAttemptSucceeded
	// (V5-08B) — the terminal evidence bundle FinalizeExecutionAttempt
	// re-validates, inside its own fenced transaction, before ever
	// committing SUCCEEDED. See AttemptFinalizationEvidence's own doc
	// comment for the full contract.
	Evidence *AttemptFinalizationEvidence
}

// DiffManifestArtifactRef names one durable artifact.Artifact row holding
// one repository mount's own post-quiescence diff manifest (V5-08B) — the
// executor already Put/Verified body and inserted the row as ORPHAN
// (ArtifactRepository.InsertArtifact) before ever proposing this evidence;
// FinalizeExecutionAttempt is what promotes it ORPHAN->ATTACHED, and only
// once every other check in its own transaction has already passed.
type DiffManifestArtifactRef struct {
	RepositoryID project.RepositoryID
	ArtifactID   string
}

// AttemptFinalizationEvidence is the terminal evidence bundle a NodeExecutor
// proposes alongside NodeExecutionResult when State is
// ExecutionAttemptSucceeded — V5-08B's own locked decision #2 ("Quyết định
// sau review source of truth — 2026-09-08", baocaov5checklist.md): a bare
// "at least one checkpoint exists" is not evidence. This is a PROPOSAL,
// never trusted as-is (the same GC-INV-17/18 discipline NodeExecutionResult
// itself already carries) — FinalizeExecutionAttempt re-validates every
// field here inside its own fenced transaction: the terminal event/
// checkpoint really exist for this Attempt/ContextSnapshot, the diff scope
// matches EffectiveScope, every DiffManifestArtifact transitions cleanly
// ORPHAN->ATTACHED, and ProposedOutcome names an outcome AdvanceRun's own
// GC-INV-11 allow-list actually accepts — before ANY of it is committed.
type AttemptFinalizationEvidence struct {
	SchemaVersion int
	// TerminalEventSequence is the Sequence of the agent_events row this
	// Attempt's own EXECUTION_FINISHED (or provider-equivalent terminal)
	// event was durably persisted under — FinalizeExecutionAttempt
	// confirms a matching row actually exists before accepting this
	// evidence.
	TerminalEventSequence uint64
	// CompletionCheckpointID is an ID minted by the executor (never by
	// FinalizeExecutionAttempt) — the finalize transaction constructs and
	// inserts the REAL Checkpoint row using exactly this ID (via
	// CheckpointsRepository.InsertCheckpoint, atomically with everything
	// else it commits), using a real canonicalStateHash rather than
	// agentevents.Sink's own mid-run surrogate hash.
	CompletionCheckpointID string
	// FinalRevisionSet is the exact workspace.RevisionSet measured AFTER
	// ProcessSupervisor confirmed process-tree quiescence on every mount
	// (AgentExecutionResult.TreeQuiesced) — never trusted if that was
	// false; the executor itself must refuse to propose SUCCEEDED at all
	// in that case (V5-08B's own locked decision #3).
	FinalRevisionSet workspace.RevisionSet
	// DiffManifestArtifacts names one durable, ORPHAN-inserted diff
	// manifest Artifact per repository mount — mandatory for every mount
	// regardless of output policy (V5-08B's own locked decision #2).
	DiffManifestArtifacts []DiffManifestArtifactRef
	// OutputArtifactRefs is optional — decision #2: "có thể không có
	// provider output artifact nếu output policy cho phép."
	OutputArtifactRefs []string
	// ProposedOutcome is the same value NodeExecutionResult.SelectedOutcome
	// already carries, repeated here as part of the evidence bundle
	// FinalizeExecutionAttempt validates as a whole (go-core-spec.md §14's
	// own "AgentExecutionResult phải có... typed proposed outcome").
	ProposedOutcome *AgentProposedOutcome
	// EvidenceEntries is populated now (V5-09/V5-10 acceptance-gap
	// remediation, 2026-09-10 post-merge review) exactly when the proposing
	// executor is CommandNodeExecutor (exactly one entry, the whole
	// execution) or GateNodeExecutor (one entry per MACHINE_GATE criterion)
	// — nil/empty for AGENT and the pre-existing fake NodeExecutor, whose
	// own diff-manifest evidence above is already sufficient. Each entry's
	// own ArtifactReferences must be a subset of OutputArtifactRefs — never
	// a fresh, unlisted artifact ID — so promotion only ever happens once,
	// via OutputArtifactRefs, and Evidence rows merely reference the result.
	EvidenceEntries []EvidenceProposal
}

// EvidenceProposal is one Evidence row a NodeExecutor proposes —
// re-validated and persisted (runtime.NewEvidence, RuntimeRepository.
// CreateEvidence) by validateAndAttachFinalizationEvidenceTx alongside
// everything else it commits, never trusted as-is.
type EvidenceProposal struct {
	// Kind identifies what this row is evidence of: a MACHINE_GATE
	// criterion's own EvidenceKey (e.g. "lint-clean"), or a fixed constant
	// for a COMMAND execution (runtime.EvidenceKindCommandExecution).
	Kind string
	// Verdict is a MACHINE_GATE criterion's own gate.Verdict string value,
	// or runtime.EvidenceVerdictSucceeded for a COMMAND execution (which has
	// no PASS/FAIL/ERROR/NOT_RUN/NOT_APPLICABLE vocabulary of its own).
	Verdict string
	// ArtifactReferences must be a non-empty subset of OutputArtifactRefs.
	ArtifactReferences []string
	// PolicyVersion is the exact, pinned policy this Evidence row's own
	// verdict was decided under (e.g. the GateVersion/CommandVersion's own
	// DefinitionID/VersionID) — never re-derived later.
	PolicyVersion string
}

// NodeExecutor executes one ExecutionAttempt's actual work — a real
// provider/command adapter in production (V5), a scripted fake in tests
// (V4-05's own scope, ports/fake.NodeExecutor). Execute must respect ctx
// cancellation/deadline: ExecuteNodeHandler derives a deadline from the
// Attempt's own pinned TimeoutSeconds and relies on Execute returning
// (possibly with ctx.Err()) once that deadline (or an outer cancellation)
// fires, rather than running unboundedly in the background.
type NodeExecutor interface {
	Execute(ctx context.Context, req NodeExecutionRequest) (NodeExecutionResult, error)
}
