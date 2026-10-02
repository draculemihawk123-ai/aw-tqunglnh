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
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
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
// still has no composition root (execute.go's own doc comment: "cmd/aw
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
	// environmentCeiling (V9-05, gap G5) resolves the EXECUTING worker's own
	// runtime execution config — its --env-allowlist — at spawn time. See
	// WithAgentEnvironmentCeiling. nil means no ceiling is known.
	environmentCeiling ports.RuntimeExecutionConfigProvider
}

// AgentNodeExecutorOption customizes NewAgentNodeExecutor. A variadic option
// rather than another positional parameter so that every existing caller
// (tests included) keeps compiling and keeps its behavior unchanged.
type AgentNodeExecutorOption func(*AgentNodeExecutor)

// WithAgentEnvironmentCeiling hands the executor the EXECUTING worker's own
// ports.RuntimeExecutionConfigProvider (the one `aw worker` also gives
// NodeSchedulingHandler) so that the environment an agent process inherits is
// never wider than that worker's current --env-allowlist (V9-05, gap G5).
//
// Why execution needs its own ceiling. The names an agent inherits are pinned
// per NodeRun, at scheduling time, as the intersection of the profile's
// envAllowlist and the scheduling worker's allowlist
// (ResolvedExecutionProfileV1.AgentInheritedEnvironment). Nothing between
// scheduling and spawn checks that the worker which EXECUTES the attempt runs
// with the same runtime config: admission (admission.go) verifies isolation,
// adapter-build drift and granted capabilities but never compares
// RuntimeExecutionConfigHash, and the pinned hash is not even decoded on the
// execute path. Several workers with different --env-allowlist values may
// share one database, and a worker may be restarted with other flags between
// scheduling and a retry. So the executor intersects the pinned list with the
// allowlist the worker in front of it holds NOW: when the config is unchanged
// that changes nothing (the pinned list is already a subset), when it has
// narrowed the agent receives less, and a worker can never be made to pass a
// name its own operator did not allow. Retry and recovery attempts of the same
// NodeRun start from the same pinned list and are cut by the same rule.
//
// Without this option the executor inherits nothing, which is exactly what AGENT
// processes got before V9-05: the safe direction. If the provider cannot say what
// the worker's list is, Execute fails closed (ErrRuntimeExecutionConfigUnavailable)
// without spawning anything. Names only — values are read by the process
// supervisor at spawn.
func WithAgentEnvironmentCeiling(provider ports.RuntimeExecutionConfigProvider) AgentNodeExecutorOption {
	return func(e *AgentNodeExecutor) { e.environmentCeiling = provider }
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
	opts ...AgentNodeExecutorOption,
) *AgentNodeExecutor {
	executor := &AgentNodeExecutor{
		uow: uow, ids: ids, store: store, workspaces: workspaces, writeLeases: writeLeases,
		agents: agents, registry: registry, matcher: matcher, checkpoints: checkpoints, clk: clk,
		interruptions: interruptions, reconciler: reconciler,
	}
	for _, opt := range opts {
		opt(executor)
	}
	return executor
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

	// V9-05 (gap G5): the request carries the PINNED environment names (the
	// profile's envAllowlist ∩ the scheduling worker's allowlist). Cut them
	// down to what THIS worker's allowlist allows right now before anything is
	// spawned — see WithAgentEnvironmentCeiling for why execution cannot just
	// trust the pin.
	request.InheritedEnvironment, err = e.EffectiveInheritedEnvironment(ctx, request.InheritedEnvironment)
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: resolve the agent process environment: %w", err)
	}

	resolved, err := e.resolveExecutionResources(ctx, req, request)
	if err != nil {
		// See CommandNodeExecutor.Execute's own identical branch for why: a
		// write lease another Attempt currently, actively holds is a real,
		// typed, timing-shaped contention — never a bare error the caller
		// would otherwise treat as immediately non-retryable.
		if errors.Is(err, ports.ErrWriteLeaseConflict) {
			return ports.NodeExecutionResult{
				State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
				ErrorCode: errorcode.CodeConflict,
			}, nil
		}
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: resolve agent execution resources: %w", err)
	}
	request.WorkspaceMounts = resolved.mounts

	// V9-01 (ADR-030): a CHECKER-role attempt is read-only relative to the
	// moment IT starts, so snapshot (or reuse the recorded) InputTree of
	// every mount now — after resources are resolved, before anything is
	// spawned, and outside every transaction. A MAKER (or Role-less)
	// attempt records nothing: its scope check is unchanged.
	profile, err := loadExecutionProfile(ctx, e.uow, req.NodeRunID)
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: load execution profile for node run %s: %w", req.NodeRunID, err)
	}
	if profile.Role == workflow.AgentRoleChecker {
		resolved.inputTrees, err = ensureInputTrees(ctx, e.uow, e.workspaces, req, resolved.mounts)
		if err != nil {
			if errors.Is(err, ErrInputTreeMissing) {
				return inputTreeUnavailableResult(), nil
			}
			return ports.NodeExecutionResult{}, fmt.Errorf("runtime: input tree of checker attempt %s: %w", req.AttemptID, err)
		}
	}

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
	if classifyErr == nil {
		nodeResult.WriteLeaseGrants = resolved.writeLeaseGrants
	}
	return nodeResult, classifyErr
}

// EffectiveInheritedEnvironment returns pinned ∩ (the executing worker's own
// --env-allowlist), sorted — never a name the worker's operator did not allow,
// never a name the pinned profile did not list. An empty pinned list needs no
// lookup and inherits nothing; so does an executor with no ceiling option.
// Execute applies it to the request it assembles; it is exported so that a
// composition root (and its tests) can ask a built executor exactly what it
// would let a given pinned list through, without running a process.
func (e *AgentNodeExecutor) EffectiveInheritedEnvironment(ctx context.Context, pinned []string) ([]string, error) {
	if len(pinned) == 0 || e.environmentCeiling == nil {
		return nil, nil
	}
	input, err := e.environmentCeiling.Resolve(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRuntimeExecutionConfigUnavailable, err)
	}
	// The same normalization (and validation) scheduling applies, so the
	// ceiling compared against here is exactly what the worker would pin today.
	snapshot, _, err := runtimedomain.NewRuntimeExecutionConfigSnapshotV1(input)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRuntimeExecutionConfigUnavailable, err)
	}
	return runtimedomain.IntersectEnvironmentNames(pinned, snapshot.EnvAllowlist), nil
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
		// V9-09: the Sink's mid-run checkpoint rejected an out-of-scope
		// write and that rejection travelled up as the adapter's own error
		// (wrapped with %w the whole way). It is a verdict about what the
		// attempt DID, not about the provider being unreachable — and it
		// sits below the quiescence rule above on purpose: a mutating
		// attempt whose tree never confirmed quiescence stays
		// indeterminate whatever the error said.
		if errors.Is(agentErr, scopeguard.ErrScopeViolation) {
			return e.scopeViolationResult(ctx, req, resolved, agentErr), nil
		}
		// V9-03: the agent's terminal outcome marker was itself wrong — it
		// names an outcome outside AllowedOutcomes, appears more than once, or
		// is malformed (ports.ErrOutcomeMarkerRejected, which the adapters
		// wrap together with their own ErrProtocol). The provider was
		// reachable and finished its turn, so this is the agent's wrong
		// answer, not an unavailable provider: it ends exactly like a MISSING
		// marker on a node with a choice does, below. It sits next to the
		// scope-violation branch for the same reason and, like it, below the
		// lease-lost and quiescence checks above, which stay first.
		if errors.Is(agentErr, ports.ErrOutcomeMarkerRejected) {
			return outcomeRejectedResult(), nil
		}
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
		return outcomeRejectedResult(), nil
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
			return e.scopeViolationResult(ctx, req, resolved, err), nil
		}
		if errors.Is(err, ErrInputTreeMissing) {
			// ADR-030: the recorded InputTree vanished (pruned) between the
			// pre-spawn check and now — a technical failure of this attempt,
			// not a verdict on what the process did.
			return inputTreeUnavailableResult(), nil
		}
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: build finalization evidence: %w", err)
	}
	return ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: selectedOutcome, Evidence: evidence,
	}, nil
}

// outcomeRejectedResult is the one result every "the agent did not give a valid
// outcome" case ends in — a missing marker on a node with a choice
// (resolveSelectedOutcome failing), and since V9-03 a marker that names an
// unlisted outcome, repeats, or is malformed (ports.ErrOutcomeMarkerRejected):
// FAILED / OUTCOME_REJECTED with failure code VALIDATION_FAILED, the same
// values execute.go gives a malformed scope-expansion proposal. No evidence is
// attached, as before. Retry follows the pinned attempt policy like any other
// failure code (finalize.go decideRetryOrExhaustion): it retries only if the
// policy's RetryableErrorCodes lists VALIDATION_FAILED.
func outcomeRejectedResult() ports.NodeExecutionResult {
	return ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonOutcomeRejected,
		ErrorCode: errorcode.CodeValidationFailed,
	}
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
