// AgentNodeExecutor is V5-08B's own production ports.NodeExecutor — the
// bridge from ExecuteNodeHandler's generic dispatch (execute.go) to a real
// ports.AgentExecutor (claude.go/codex.go in production). It is the
// concrete answer to execute.go's own "V4-05's fake executor tương lai"
// and to admission.go's own "resolve WorkspaceHandle thật là việc của
// V5-08B's own execution bridge" deferral — every real I/O this file
// performs (workspace resolution, write-lease acquisition, the actual
// provider spawn, the post-quiescence diff) runs entirely OUTSIDE any
// database transaction, exactly the "gather in a read-only Tx, then real
// I/O, then a short prep Tx, then finalize" discipline every other V5-08
// task already established.
//
// Scope, locked with the user (baocaov5checklist.md's own "Quyết định sau
// review source of truth — 2026-09-08", V5-08B section): this file
// implements steps 3-4 (real evidence staging) and, since V5-08C
// (baocaov5checklist.md's own V5-08C section), the cancellation-execution
// path — real cancellation reaches a running provider process entirely
// through ctx propagation already built into ports.ProcessSupervisor.Run
// (no new plumbing needed there); this file's own job is classifying what
// AgentExecutor.Start reports back once that happens (see classify's own
// AgentExecutionCancelled case and agent_node_executor_cancellation.go).
// AgentExecutionRequest.CancellationToken stays an inert placeholder —
// V5-08C's own real signal is ExecuteNodeHandler's own poller cancelling
// this Execute call's own ctx, not that field.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// ErrIndeterminateExecution signals execute.go's own Handle to leave the
// Attempt RUNNING rather than finalize it — V5-08B's own locked
// provider-loss mapping (mapping #4): a JobLease/WriteLease lost
// mid-execution, or an unconfirmed process-tree quiescence on a mutating
// attempt, can never be safely classified as a definite FAILED. Neither
// LOST nor INDETERMINATE is itself a state FinalizeExecutionAttempt's own
// isFinalizableExecutionAttemptState accepts (deliberately: no live lease
// could ever be fenced for that transition) — internal/app/worker's own
// crash-recovery/interruption path (V4-13, ClassifyInterruptedAttempt) is
// the sole durable authority that ever resolves an Attempt left RUNNING
// this way.
var ErrIndeterminateExecution = errors.New("runtime: execution outcome is indeterminate — leave RUNNING for crash recovery")

// diffManifestArtifactMediaType names the JSON envelope
// stageMountEvidence.Put's own body encodes (RepositoryID/BaseRevision/
// CurrentRevision/Files/Patch — ports.WorkspaceDiff marshaled directly).
const diffManifestArtifactMediaType = "application/vnd.agentkit.diff-manifest+json"

// writeLeaseTTLGrace is added to the Attempt's own execution Timeout when
// acquiring WriteLeases (V5-08B) — AgentExecutor.Start/Resume is a single
// blocking call with no heartbeat hook exposed to this caller, so the
// lease must outlive the whole call outright rather than being refreshed
// mid-flight; a generous fixed grace absorbs scheduling/spawn overhead
// the Timeout itself does not budget for.
const writeLeaseTTLGrace = 2 * time.Minute

// AgentNodeExecutor's own dependencies are all injected — this package
// still has no composition root (execute.go's own doc comment: "cmd/agentkit
// serve/worker vẫn là stub trống"), so every real value (which registry,
// which matcher's known secrets, which concrete WorkspaceProvider/
// WriteLeaseManager) is this file's own caller's decision, not something
// constructed here.
type AgentNodeExecutor struct {
	uow           ports.UnitOfWork
	ids           idsource.Source
	store         ports.ArtifactStore
	workspaces    ports.WorkspaceProvider
	writeLeases   ports.WriteLeaseManager
	agents        *agentregistry.Registry
	registry      *eventschema.Registry
	matcher       redact.Matcher
	checkpoints   agentevents.CheckpointStore
	clk           clock.Clock
	interruptions worker.InterruptionRecoveryStore
	reconciler    worker.WorkspaceReconciler
}

// NewAgentNodeExecutor returns a ready-to-register AgentNodeExecutor.
// checkpoints backs agentevents.Sink's own mid-run checkpoints exactly as
// it already does for every other Sink caller (in production, the same
// *sqlite.Store that also backs uow) — this executor's own COMPLETION
// checkpoint is separate (built inside FinalizeExecutionAttempt via
// ports.CheckpointsRepository), never routed through this dependency.
// interruptions/reconciler are V5-08C's own dependencies (spike-era,
// *sqlite.Store-direct — the same narrow surfaces RecoveryReaperHandler
// already uses, recovery_reaper.go), needed only for a genuinely
// cancelled MUTATING attempt: terminating it INDETERMINATE and
// reconciling/quarantining the workspace it held a WriteLease against,
// reusing V4-13's own already-proven primitives rather than inventing a
// second way to reach the identical durable outcome.
func NewAgentNodeExecutor(
	uow ports.UnitOfWork, ids idsource.Source, store ports.ArtifactStore, workspaces ports.WorkspaceProvider,
	writeLeases ports.WriteLeaseManager, agents *agentregistry.Registry, registry *eventschema.Registry,
	matcher redact.Matcher, checkpoints agentevents.CheckpointStore, clk clock.Clock,
	interruptions worker.InterruptionRecoveryStore, reconciler worker.WorkspaceReconciler,
) *AgentNodeExecutor {
	return &AgentNodeExecutor{
		uow: uow, ids: ids, store: store, workspaces: workspaces, writeLeases: writeLeases,
		agents: agents, registry: registry, matcher: matcher, checkpoints: checkpoints, clk: clk,
		interruptions: interruptions, reconciler: reconciler,
	}
}

var _ ports.NodeExecutor = (*AgentNodeExecutor)(nil)

// Execute implements ports.NodeExecutor. See this file's own package doc
// comment for the full real-I/O staging discipline.
func (e *AgentNodeExecutor) Execute(ctx context.Context, req ports.NodeExecutionRequest) (ports.NodeExecutionResult, error) {
	request, err := AssembleAgentExecutionRequest(ctx, e.uow, e.store, AssembleAgentExecutionRequestRequest{
		RunID: req.RunID, NodeRunID: req.NodeRunID, AttemptID: req.AttemptID,
	})
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: assemble agent execution request: %w", err)
	}

	resolved, err := e.resolveExecutionResources(ctx, req, request)
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: resolve agent execution resources: %w", err)
	}
	request.WorkspaceMounts = resolved.mounts
	workingDirectory, cleanupWorkingDirectory, err := resolveAgentWorkingDirectory(resolved.mounts)
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: resolve agent working directory: %w", err)
	}
	if cleanupWorkingDirectory != nil {
		defer cleanupWorkingDirectory()
	}
	request.WorkingDirectory = workingDirectory

	executor, _, err := e.agents.Resolve(request.ProviderKey, agentregistry.Requirements{})
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: resolve agent executor for provider %s: %w", request.ProviderKey, err)
	}

	sink, err := agentevents.NewSink(ctx, agentevents.Config{
		RunID: req.RunID, NodeRunID: req.NodeRunID, AttemptID: req.AttemptID,
		ContextSnapshotID: string(request.ContextSnapshot.ID), EffectiveScope: request.EffectiveScope,
		Mounts: resolved.sinkMounts, Workspaces: e.workspaces, Registry: e.registry, Matcher: e.matcher,
		UOW: e.uow, Checkpoints: e.checkpoints, IDs: e.ids, Clock: e.clk,
		JobLease: req.JobLease, WriteLeases: resolved.writeLeaseGrants,
	})
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: construct agent event sink: %w", err)
	}

	// ADR-005 ("Alpha luôn khởi động agent mới từ ContextSnapshot"): every
	// ExecutionAttempt — including a V4-06 technical retry, which always
	// creates a brand-new AttemptID — always Starts fresh; nothing in this
	// codebase ever persists a ProviderSessionRef for reuse, so Resume has
	// no real caller in Alpha (V5-08B's own locked mapping #4, last row:
	// a stale/invalid ProviderSessionRef always means "new Attempt, Start
	// from canonical snapshot," never "call Resume").
	agentResult, agentErr := executor.Start(ctx, request, sink)
	flushErr := sink.Flush(ctx)

	nodeResult, classifyErr := e.classify(ctx, req, request, resolved, agentResult, agentErr, flushErr)
	return nodeResult, classifyErr
}

// classify maps one completed AgentExecutor.Start call into either a
// genuine NodeExecutionResult proposal or an error signaling "do not
// finalize" — V5-08B's own locked provider-loss mapping table
// (baocaov5checklist.md's own mapping #4). agentResult is populated
// (including TreeQuiesced) even when agentErr != nil, since claude.go and
// codex.go both build their own result before checking their own parse/run
// errors.
func (e *AgentNodeExecutor) classify(
	ctx context.Context, req ports.NodeExecutionRequest, request ports.AgentExecutionRequest, resolved resolvedExecutionResources,
	agentResult ports.AgentExecutionResult, agentErr error, flushErr error,
) (ports.NodeExecutionResult, error) {
	if errors.Is(agentErr, ports.ErrJobLeaseLost) || errors.Is(agentErr, ports.ErrWriteLeaseLost) ||
		errors.Is(flushErr, ports.ErrJobLeaseLost) || errors.Is(flushErr, ports.ErrWriteLeaseLost) {
		return ports.NodeExecutionResult{}, fmt.Errorf("%w: job/write lease lost mid-execution: %v", ErrIndeterminateExecution, firstNonNil(agentErr, flushErr))
	}
	// V5-08B's own locked decision #3: a mutating attempt (at least one
	// WRITE mount) must never have its own terminal disposition — success
	// OR failure — trusted while process-tree quiescence was never
	// confirmed; a descendant could still be writing.
	if !agentResult.TreeQuiesced && resolved.hasWriteMount {
		return ports.NodeExecutionResult{}, fmt.Errorf("%w: process tree did not confirm quiescence on a mutating attempt", ErrIndeterminateExecution)
	}
	if agentErr != nil {
		// A bare Go error before/during the provider process's own
		// lifecycle (spawn failure, protocol/parse error) with quiescence
		// otherwise confirmed (or nothing writable to have been left
		// dirty) — row 1 of the locked mapping table: definite FAILED,
		// PROVIDER_UNAVAILABLE.
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			ErrorCode: errorcode.CodeProviderUnavailable,
		}, nil
	}
	if agentResult.Status == ports.AgentExecutionCancelled {
		// V5-08C: the underlying process genuinely stopped in response to
		// ctx being cancelled (ports.ProcessSupervisor.Run's own existing
		// ctx-propagation — no new plumbing needed for the process to
		// actually die; see this file's own package doc comment). Whether
		// that ctx cancellation was a genuine, durable run-cancellation
		// intent or something else entirely (e.g. pool shutdown escalating
		// jobsCtx) is not something this bridge was TOLD — it re-derives
		// the truth from durable state itself, the same "trust durable
		// state, never a live signal" discipline this whole codebase
		// already follows everywhere else.
		return e.classifyCancellation(ctx, req, resolved)
	}
	if agentResult.Status != ports.AgentExecutionSucceeded {
		// The provider itself reported a determinate non-success (row 2):
		// definite FAILED, EXECUTION_FAILED. TIMED_OUT at the provider's
		// own internal level folds into the same bucket here — this
		// executor's own OUTER deadline is already handled one layer up by
		// execute.go's own attemptCtx observation (this file's own package
		// doc comment), which is what V4-05/V5-08 actually route
		// TIMED_OUT through.
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			ErrorCode: errorcode.CodeExecutionFailed,
		}, nil
	}
	if flushErr != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: flush agent event sink after a successful execution: %w", flushErr)
	}

	selectedOutcome, proposedOutcome, err := resolveSelectedOutcome(agentResult.ProposedOutcome, request.AllowedOutcomes)
	if err != nil {
		// The agent completed but never satisfied V5-08B's own terminal
		// outcome marker contract — a real, classifiable protocol
		// violation on OUR side of the wire (not an infra/connectivity
		// concern), mirroring execute.go's own established "malformed
		// scope expansion proposal -> OUTCOME_REJECTED" precedent exactly.
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonOutcomeRejected,
			ErrorCode: errorcode.CodeValidationFailed,
		}, nil
	}

	evidence, err := e.buildEvidence(ctx, req, request, resolved, proposedOutcome)
	if err != nil {
		if errors.Is(err, scopeguard.ErrScopeViolation) {
			// ADR-020's own TerminationReasonScopeViolation, reserved but
			// never produced before V5-08B (baocaov5checklist.md's own
			// locked decision text names this executor as its likely
			// first real producer): the final, post-quiescence diff
			// itself — not just a mid-run checkpoint's own diff — exceeded
			// this Attempt's own EffectiveScope.
			return ports.NodeExecutionResult{
				State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonScopeViolation,
				ErrorCode: errorcode.CodeScopeViolation,
			}, nil
		}
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: build finalization evidence: %w", err)
	}
	return ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: selectedOutcome, Evidence: evidence,
	}, nil
}

// resolveSelectedOutcome applies V5-08B's own locked split of
// responsibility exactly (confirmed with the user 2026-09-09): the
// provider adapter (claude.go/codex.go) only ever parses a terminal
// marker if one is present; deriving the sole allowed outcome when no
// marker was needed — or rejecting a genuinely missing one — is this
// bridge's own job, never the adapter's.
func resolveSelectedOutcome(proposed *ports.AgentProposedOutcome, allowedOutcomes []string) (string, *ports.AgentProposedOutcome, error) {
	if proposed != nil {
		return proposed.Value, proposed, nil
	}
	if len(allowedOutcomes) == 1 {
		derived := &ports.AgentProposedOutcome{Value: allowedOutcomes[0], Source: ports.AgentOutcomeDerivedSingleAllowed, SchemaVersion: 1}
		return derived.Value, derived, nil
	}
	return "", nil, fmt.Errorf("agent execution succeeded but reported no terminal outcome marker, and %d outcomes were allowed (a marker was required)", len(allowedOutcomes))
}

func firstNonNil(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
