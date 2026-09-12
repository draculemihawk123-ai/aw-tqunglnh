// GateNodeExecutor is V5-10's own production ports.NodeExecutor — the
// MACHINE_GATE sibling of CommandNodeExecutor (command_node_executor.go,
// V5-09), reusing the exact same execution mechanics COMMAND already
// established (resolveExecutionResources/buildEvidence,
// resolveArgvAndSecrets, materializeExecutable, the V5-08C cancellation
// path via classifyCancellation) to run the ONE Command a GateVersion
// pins (gate.GateDocument.CommandRef) — then, unlike COMMAND, maps that
// execution's own output into an authoritative, per-criterion Verdict
// with provenance ("MACHINE_GATE tạo verdict authoritative với
// provenance", V5-10's own Mục tiêu) instead of a single exit-code
// pass/fail.
//
// Locked scope decision (confirmed with the user, 2026-09-10, verbatim):
// "V5-10 dùng exact RevisionSet làm input bắt buộc. Không tạo ReleaseSet
// giả hoặc dependency ngược. Khi V5-10A cung cấp ReleaseSet, bổ sung nó
// vào provenance/input của gate mà giữ nguyên GateResult, Evidence và
// verdict semantics." — this file takes the RevisionSet already resolved
// onto every ports.AgentWorkspaceMount (the same pin AGENT/COMMAND already
// consume) as its own sole provenance input; ReleaseSet does not exist
// anywhere in this codebase yet (V5-10A's own future scope) and is
// deliberately NOT invented here. GateResult's own shape is designed so a
// later ReleaseSet-provenance field is purely additive — never a change to
// OverallVerdict/Criteria's own meaning.
//
// A Gate is read-only by design ("execute GateVersion trên exact
// read-only RevisionSet"). EffectiveScope is a WHOLE-WorkItem concept in
// this codebase, not customized per node-type or per-NodeRun — a real
// workflow legitimately has AGENT/COMMAND nodes that write and
// MACHINE_GATE nodes that verify, all sharing the identical WorkItem-level
// scope snapshot, so rejecting a Gate outright just because ITS OWN
// WorkItem happens to be WRITE-granted somewhere would be wrong (nearly
// every real Gate would then fail to even start). Instead,
// forceReadOnlyMounts (gatherGateExecutionInputs's own final step) always
// downgrades every resolved mount to READ-ONLY before this executor ever
// sees it, regardless of what the underlying EffectiveScope actually
// grants — a Gate's own THIS-EXECUTION access is always read-only,
// unconditionally, never trusting or even inspecting the WorkItem's own
// broader grant. Because of that, resolved.hasWriteMount is always false
// by the time classify's own cancellation branch could ever be reached —
// classifyCancellation's own mutating-attempt path
// (handleMutatingCancellation, INDETERMINATE + reconciliation) can
// structurally never fire for a Gate; only its simple read-only branch
// (CANCELLED) ever does.
//
// "Scratch output nằm ngoài source workspace": unlike CommandNodeExecutor,
// this executor never spawns the evaluator with a real repository mount
// as its own working directory — cwd is always a fresh, empty scratch
// temp directory (scratchDirectory below), so nothing the evaluator
// writes while running can ever land inside — or be mistaken for a diff
// against — any repository's own real working tree. Repositories the
// Gate's own underlying Command references by PLACEHOLDER are still real,
// read-only paths it can pass as arguments; only cwd itself is
// decoupled from CwdRepositoryTarget for this executor.
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// ErrGateInvocationUnresolvable is returned when a Gate's own underlying
// Command cannot be resolved/spawned at all — an unresolvable argv/secret
// (mirrors ErrCommandInvocationUnresolvable), or a stale revision (the
// live on-disk revision of a mount no longer matches what this Attempt's
// own ContextSnapshot pinned — evaluating against data that has already
// moved on is never trustworthy). Always a FAILED classification, never
// ErrIndeterminateExecution — nothing was ever started, so there is no
// ambiguity about what state it left anything in.
var ErrGateInvocationUnresolvable = errors.New("runtime: gate's own underlying command could not be resolved/verified before evaluation")

// GateResult is V5-10's own authoritative, provenance-bearing verdict for
// one MACHINE_GATE evaluation — richer than NodeExecutionResult's own
// coarse State/TerminationReason, and the one shape this task's own
// locked scope decision requires staying stable once V5-10A adds
// ReleaseSet provenance (see this file's own package doc comment).
// Persisted as evidence (persistGateResultArtifact) — the durable,
// tamper-evident record of what a Gate actually verified, never re-trusted
// from the evaluator's own raw stdout again once this exists.
type GateResult struct {
	OverallVerdict gate.Verdict          `json:"overallVerdict"`
	Criteria       []GateCriterionResult `json:"criteria"`
}

// GateCriterionResult is one Criterion's own resolved verdict.
type GateCriterionResult struct {
	Name        string       `json:"name"`
	EvidenceKey string       `json:"evidenceKey"`
	Verdict     gate.Verdict `json:"verdict"`
	Detail      string       `json:"detail,omitempty"`
	// Reason is required whenever Verdict is NOT_APPLICABLE (repo-wide
	// convention: docs/design/01-system-design.md's own "NOT_APPLICABLE
	// cần policy và reason") — a criterion the evaluator reports
	// NOT_APPLICABLE with no reason is rejected as ERROR instead (fail
	// closed), never silently accepted.
	Reason string `json:"reason,omitempty"`
}

// gateEvaluatorCriterionOutput is the JSON shape this task's own locked
// protocol requires of the underlying Command's own stdout: a single JSON
// object, keyed by each Criterion's own EvidenceKey, naming that
// criterion's own verdict. This convention is new — GateDocument's own
// domain schema (internal/domain/gate/gate.go) deliberately carries no
// exit-code-per-criterion or output-schema field of its own (confirmed by
// reading that package's own doc comment: "never evaluates anything
// itself... the runtime's job"), so this executor is the FIRST and only
// definer of what "the runtime's job" actually means. EvidenceKey already
// existing as a per-criterion identifier in the authored schema is the
// direct textual justification for keying output by it rather than by
// Name (an author-facing label, never guaranteed unique or stable).
type gateEvaluatorCriterionOutput struct {
	Verdict string `json:"verdict"`
	Detail  string `json:"detail,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// verdictSeverity orders gate.Verdict from least to most severe — a
// Gate's own OverallVerdict is always the MOST severe verdict any single
// criterion resolved to (PASS/NOT_APPLICABLE never outrank a real
// problem): ERROR (something is untrustworthy) outranks FAIL (a
// legitimate, trusted failure), which outranks NOT_RUN (incomplete),
// which outranks PASS/NOT_APPLICABLE (both fine — NOT_APPLICABLE never
// counts against the overall verdict, matching its own "does not apply"
// meaning).
var verdictSeverity = map[gate.Verdict]int{
	gate.VerdictPass: 0, gate.VerdictNotApplicable: 0, gate.VerdictNotRun: 1, gate.VerdictFail: 2, gate.VerdictError: 3,
}

// GateNodeExecutor's own dependencies mirror CommandNodeExecutor's
// exactly (this package still has no composition root) — deliberately no
// writeLeases field: forceReadOnlyMounts (gatherGateExecutionInputs's own
// final step) always downgrades every resolved mount to READ-ONLY before
// Execute ever sees it (see this file's own package doc comment for why),
// so resolveExecutionResources's own write-lease-acquisition branch can
// never be reached for a Gate.
type GateNodeExecutor struct {
	uow           ports.UnitOfWork
	ids           idsource.Source
	store         ports.ArtifactStore
	workspaces    ports.WorkspaceProvider
	supervisor    ports.ProcessSupervisor
	secrets       ports.SecretResolver
	registry      *eventschema.Registry
	matcher       redact.Matcher
	checkpoints   agentevents.CheckpointStore
	clk           clock.Clock
	interruptions worker.InterruptionRecoveryStore
	reconciler    worker.WorkspaceReconciler
}

// NewGateNodeExecutor returns a ready-to-register GateNodeExecutor.
func NewGateNodeExecutor(
	uow ports.UnitOfWork, ids idsource.Source, store ports.ArtifactStore, workspaces ports.WorkspaceProvider,
	supervisor ports.ProcessSupervisor, secrets ports.SecretResolver, registry *eventschema.Registry, matcher redact.Matcher,
	checkpoints agentevents.CheckpointStore, clk clock.Clock, interruptions worker.InterruptionRecoveryStore, reconciler worker.WorkspaceReconciler,
) *GateNodeExecutor {
	return &GateNodeExecutor{
		uow: uow, ids: ids, store: store, workspaces: workspaces, supervisor: supervisor, secrets: secrets,
		registry: registry, matcher: matcher, checkpoints: checkpoints, clk: clk, interruptions: interruptions, reconciler: reconciler,
	}
}

var _ ports.NodeExecutor = (*GateNodeExecutor)(nil)

// Execute implements ports.NodeExecutor. See this file's own package doc
// comment for the full design.
func (e *GateNodeExecutor) Execute(ctx context.Context, req ports.NodeExecutionRequest) (ports.NodeExecutionResult, error) {
	inputs, err := gatherGateExecutionInputs(ctx, e.uow, req)
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: gather gate execution inputs: %w", err)
	}

	request := ports.AgentExecutionRequest{
		AttemptID: ports.ExecutionAttemptID(req.AttemptID), EffectiveScope: inputs.effectiveScope,
		WorkspaceMounts: inputs.workspaceMounts, Timeout: inputs.timeout, AllowedOutcomes: inputs.allowedOutcomes,
		ExecutionProfileHash: inputs.executionProfileHash,
	}

	// No writeLeases dependency exists on this executor at all — a nil
	// ports.WriteLeaseManager is safe here specifically because
	// gatherGateExecutionInputs already rejected any WRITE grant, so
	// resolveExecutionResources's own writeTargets branch (the only
	// caller of it) can never be reached below.
	resolved, err := resolveExecutionResources(ctx, e.uow, e.workspaces, nil, req, request)
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: resolve gate execution resources: %w", err)
	}
	request.WorkspaceMounts = resolved.mounts

	if staleRepositoryID, staleErr := staleMountRevision(ctx, e.workspaces, resolved.mounts); staleErr != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: check gate mount revision freshness: %w", staleErr)
	} else if staleRepositoryID != "" {
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			ErrorCode: errorcode.CodeValidationFailed,
		}, nil
	}

	argv, env, err := resolveArgvAndSecrets(ctx, inputs.commandDoc, mountsByRepositoryID(resolved.mounts), e.secrets)
	if err != nil {
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			ErrorCode: errorcode.CodeValidationFailed,
		}, nil
	}

	scriptPath, cleanupScript, err := materializeExecutable(inputs.scriptPayload, inputs.commandDoc.Executable.ResourceKey)
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: materialize gate executable: %w", err)
	}
	defer cleanupScript()

	scratchDir, cleanupScratch, err := scratchDirectory()
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: create gate scratch directory: %w", err)
	}
	defer cleanupScratch()

	sink, err := agentevents.NewSink(ctx, agentevents.Config{
		RunID: req.RunID, NodeRunID: req.NodeRunID, AttemptID: req.AttemptID,
		ContextSnapshotID: string(inputs.snapshotID), EffectiveScope: inputs.effectiveScope,
		Mounts: resolved.sinkMounts, Workspaces: e.workspaces, Registry: e.registry, Matcher: e.matcher,
		UOW: e.uow, Checkpoints: e.checkpoints, IDs: e.ids, Clock: e.clk,
		JobLease: req.JobLease, WriteLeases: resolved.writeLeaseGrants,
	})
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: construct gate event sink: %w", err)
	}

	if err := sink.Accept(ctx, ports.AgentEvent{
		AttemptID: ports.ExecutionAttemptID(req.AttemptID), Sequence: 1,
		Kind: ports.AgentEventExecutionStarted, ObservedAt: time.Now().UTC(),
	}); err != nil {
		if errors.Is(err, ports.ErrJobLeaseLost) || errors.Is(err, ports.ErrWriteLeaseLost) {
			return ports.NodeExecutionResult{}, fmt.Errorf("%w: job/write lease lost before gate spawn: %v", ErrIndeterminateExecution, err)
		}
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: emit gate execution-started event: %w", err)
	}

	var stdout, stderr bytes.Buffer
	result, runErr := e.supervisor.Run(ctx, ports.ProcessSpec{
		ID: ports.ProcessID(req.AttemptID), Executable: scriptPath, Argv: argv, WorkingDirectory: scratchDir,
		Environment: env, InheritedEnvironment: inputs.commandDoc.EnvAllowlist, Timeout: inputs.timeout,
		OutputLimitBytes: int(inputs.commandDoc.Output.MaxOutputBytes),
	}, &stdout, &stderr)

	acceptErr := sink.Accept(ctx, ports.AgentEvent{
		AttemptID: ports.ExecutionAttemptID(req.AttemptID), Sequence: 2,
		Kind: ports.AgentEventExecutionFinished, ObservedAt: time.Now().UTC(),
		Message: fmt.Sprintf("exit code %d", result.ExitCode),
	})
	flushErr := sink.Flush(ctx)

	classified, err := e.classify(ctx, req, request, resolved, inputs.criteria, result, runErr, firstNonNil(acceptErr, flushErr), stdout.Bytes(), env)
	if err == nil {
		// Always empty in practice (see this method's own "No writeLeases
		// dependency" comment above) — attached anyway so all three
		// executors carry this field identically, rather than one silently
		// omitting it.
		classified.WriteLeaseGrants = resolved.writeLeaseGrants
	}
	return classified, err
}

// classify maps one completed ProcessSupervisor.Run call into either a
// genuine NodeExecutionResult proposal or an error signaling "do not
// finalize" — the GATE-side counterpart of CommandNodeExecutor's own
// classify, same ordering, same reused cancellation path, but deriving a
// full GateResult from the evaluator's own structured output rather than
// a single exit-code check.
func (e *GateNodeExecutor) classify(
	ctx context.Context, req ports.NodeExecutionRequest, request ports.AgentExecutionRequest, resolved resolvedExecutionResources,
	criteria []gate.Criterion, result ports.ProcessResult, runErr error, syncErr error, stdout []byte, secretValues map[string]string,
) (ports.NodeExecutionResult, error) {
	if errors.Is(syncErr, ports.ErrJobLeaseLost) || errors.Is(syncErr, ports.ErrWriteLeaseLost) {
		return ports.NodeExecutionResult{}, fmt.Errorf("%w: job/write lease lost mid-execution: %v", ErrIndeterminateExecution, syncErr)
	}
	// A Gate is read-only by construction (forceReadOnlyMounts,
	// gatherGateExecutionInputs) — resolved.hasWriteMount is always false
	// here, so this check can never actually block a
	// legitimate quiesced exit; kept for the identical defense-in-depth
	// reason AgentNodeExecutor/CommandNodeExecutor both already apply it.
	if !result.TreeQuiesced && resolved.hasWriteMount {
		return ports.NodeExecutionResult{}, fmt.Errorf("%w: process tree did not confirm quiescence", ErrIndeterminateExecution)
	}
	if result.Cancelled {
		// V5-09/V5-08C's own locked requirement extended to Gate: reuse
		// the exact same cancellation path. Structurally always the
		// simple read-only branch for a Gate (see this file's own
		// package doc comment).
		return classifyCancellation(ctx, e.uow, e.ids, e.workspaces, nil, req, resolved)
	}

	gateResult := deriveGateResult(criteria, result, runErr, stdout)
	if syncErr != nil && gateResult.OverallVerdict == gate.VerdictPass {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: flush gate event sink after a passing evaluation: %w", syncErr)
	}

	if gateResult.OverallVerdict != gate.VerdictPass {
		// V5-10 acceptance-gap remediation PR2 (2026-09-10 post-merge
		// review): a non-PASS verdict is exactly as much evidence as a PASS
		// one ("Gate phải trả Evidence cho FAIL/ERROR/NOT_RUN, không persist
		// rồi bỏ artifact ID" — user's own locked PR2 scope) — every
		// criterion gets its own Evidence row here too, never just a
		// dropped artifact ID. buildEvidence is safe to call regardless of
		// outcome: EXECUTION_STARTED/FINISHED are emitted unconditionally
		// before classify ever runs (Execute, above), so a real terminal
		// event always exists, and a Gate's own mounts are always
		// read-only (forceReadOnlyMounts) — its diff-manifest set is
		// always structurally empty, never a real mutation to report.
		evidence, err := buildEvidence(ctx, e.uow, e.ids, e.clk, e.store, e.workspaces, req, request, resolved, nil, true)
		if err != nil {
			if errors.Is(err, scopeguard.ErrScopeViolation) {
				return ports.NodeExecutionResult{
					State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonScopeViolation,
					ErrorCode: errorcode.CodeScopeViolation,
				}, nil
			}
			return ports.NodeExecutionResult{}, fmt.Errorf("runtime: build gate finalization evidence for non-PASS verdict: %w", err)
		}
		resultArtifactID, err := e.persistGateResultArtifact(ctx, resolved.projectID, req.AttemptID, gateResult, artifact.Orphan, secretValues)
		if err != nil {
			return ports.NodeExecutionResult{}, fmt.Errorf("runtime: persist gate result artifact: %w", err)
		}
		evidence.OutputArtifactRefs = append(evidence.OutputArtifactRefs, resultArtifactID)
		for _, criterion := range gateResult.Criteria {
			evidence.EvidenceEntries = append(evidence.EvidenceEntries, ports.EvidenceProposal{
				Kind: criterion.EvidenceKey, Verdict: string(criterion.Verdict),
				ArtifactReferences: []string{resultArtifactID}, PolicyVersion: request.ExecutionProfileHash,
			})
		}
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			ErrorCode: errorcode.CodeExecutionFailed, Evidence: evidence,
		}, nil
	}

	selectedOutcome, proposedOutcome, err := resolveSelectedOutcome(nil, request.AllowedOutcomes)
	if err != nil {
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonOutcomeRejected,
			ErrorCode: errorcode.CodeValidationFailed,
		}, nil
	}

	evidence, err := buildEvidence(ctx, e.uow, e.ids, e.clk, e.store, e.workspaces, req, request, resolved, proposedOutcome, true)
	if err != nil {
		if errors.Is(err, scopeguard.ErrScopeViolation) {
			// A Gate's own evaluator wrote something despite being
			// read-only — never trust a PASS verdict from an evaluation
			// that mutated anything it was never granted.
			return ports.NodeExecutionResult{
				State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonScopeViolation,
				ErrorCode: errorcode.CodeScopeViolation,
			}, nil
		}
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: build gate finalization evidence: %w", err)
	}

	resultArtifactID, err := e.persistGateResultArtifact(ctx, resolved.projectID, req.AttemptID, gateResult, artifact.Orphan, secretValues)
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: persist gate result artifact: %w", err)
	}
	evidence.OutputArtifactRefs = append(evidence.OutputArtifactRefs, resultArtifactID)
	// V5-10 acceptance-gap remediation (2026-09-10 post-merge review): one
	// Evidence row per criterion — GateResult artifact is no substitute for
	// criteria-level Evidence (docs/design/07-v5-execution-evidence.md's
	// own review finding). Every criterion shares the SAME GateResult
	// artifact (it covers all of them at once); Kind is that criterion's
	// own EvidenceKey, Verdict its own resolved gate.Verdict — always PASS
	// here (a non-PASS OverallVerdict returns FAILED above, before this
	// point is ever reached; a future remediation PR is what extends
	// Evidence to the FAILED branch).
	for _, criterion := range gateResult.Criteria {
		evidence.EvidenceEntries = append(evidence.EvidenceEntries, ports.EvidenceProposal{
			Kind: criterion.EvidenceKey, Verdict: string(criterion.Verdict),
			ArtifactReferences: []string{resultArtifactID}, PolicyVersion: request.ExecutionProfileHash,
		})
	}

	return ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: selectedOutcome, Evidence: evidence,
	}, nil
}

// deriveGateResult is this task's own locked criterion-verdict mapping
// (confirmed by reading the code, not asked — internal/domain/gate's own
// package doc comment explicitly leaves this to the runtime): a stale
// revision, a bare spawn error, a timeout, or a nonzero exit code all
// fail EVERY criterion ERROR outright — "Hoàn thành khi: error/missing
// evidence không thể PASS" (an evaluator that itself could not be
// trusted to finish cleanly can never produce trustworthy per-criterion
// output, no matter what its own stdout happens to contain). Only a
// clean exit (0, not timed out, not cancelled) reaches the actual
// output-parsing step below.
func deriveGateResult(criteria []gate.Criterion, result ports.ProcessResult, runErr error, stdout []byte) GateResult {
	if runErr != nil {
		return errorAllCriteria(criteria, fmt.Sprintf("gate evaluator could not be spawned: %v", runErr))
	}
	if result.TimedOut {
		return errorAllCriteria(criteria, "gate evaluator timed out")
	}
	if result.ExitCode != 0 {
		return errorAllCriteria(criteria, fmt.Sprintf("gate evaluator exited %d", result.ExitCode))
	}
	// V5-10 acceptance-gap remediation (2026-09-10 post-merge review):
	// truncated stdout could still happen to end on a syntactically valid
	// JSON object (e.g. truncation lands after the object but before some
	// trailing log text) — never trust ANY criterion's own verdict when
	// bytes beyond OutputLimitBytes were silently discarded, exactly the
	// same "cannot be trusted as clean" reasoning CommandNodeExecutor's own
	// classify now applies.
	if result.OutputTruncated {
		return errorAllCriteria(criteria, "gate evaluator output was truncated")
	}

	var output map[string]gateEvaluatorCriterionOutput
	if err := json.Unmarshal(stdout, &output); err != nil {
		return errorAllCriteria(criteria, fmt.Sprintf("gate evaluator produced no parseable output: %v", err))
	}

	results := make([]GateCriterionResult, 0, len(criteria))
	overall := gate.VerdictPass
	for _, c := range criteria {
		entry, ok := output[c.EvidenceKey]
		if !ok {
			results = append(results, GateCriterionResult{
				Name: c.Name, EvidenceKey: c.EvidenceKey, Verdict: gate.VerdictNotRun,
				Detail: "gate evaluator did not report this criterion's own evidence key",
			})
			overall = maxSeverityVerdict(overall, gate.VerdictNotRun)
			continue
		}
		verdict := gate.Verdict(entry.Verdict)
		switch verdict {
		case gate.VerdictNotApplicable:
			// Repo-wide convention (docs/design/01-system-design.md:
			// "NOT_APPLICABLE cần policy và reason") — BOTH halves are
			// checked now (V5-10 acceptance-gap remediation, 2026-09-10):
			// a non-empty reason alone used to be enough, but a claim for
			// a criterion the author never authorized as
			// AllowNotApplicable is exactly as untrustworthy as one with
			// no reason at all — an evaluator's own runtime claim can
			// never grant itself an exemption authoring time never gave
			// it. Both failure modes fail closed to ERROR identically.
			if !c.AllowNotApplicable {
				results = append(results, GateCriterionResult{
					Name: c.Name, EvidenceKey: c.EvidenceKey, Verdict: gate.VerdictError,
					Detail: "gate evaluator reported NOT_APPLICABLE for a criterion not authored as AllowNotApplicable",
				})
				overall = maxSeverityVerdict(overall, gate.VerdictError)
				continue
			}
			if strings.TrimSpace(entry.Reason) == "" {
				results = append(results, GateCriterionResult{
					Name: c.Name, EvidenceKey: c.EvidenceKey, Verdict: gate.VerdictError,
					Detail: "gate evaluator reported NOT_APPLICABLE with no reason",
				})
				overall = maxSeverityVerdict(overall, gate.VerdictError)
				continue
			}
			fallthrough
		case gate.VerdictPass, gate.VerdictFail, gate.VerdictError, gate.VerdictNotRun:
			results = append(results, GateCriterionResult{
				Name: c.Name, EvidenceKey: c.EvidenceKey, Verdict: verdict, Detail: entry.Detail, Reason: entry.Reason,
			})
			overall = maxSeverityVerdict(overall, verdict)
		default:
			// Not one of the 5 valid values at all — tamper/malformed
			// output, never trusted.
			results = append(results, GateCriterionResult{
				Name: c.Name, EvidenceKey: c.EvidenceKey, Verdict: gate.VerdictError,
				Detail: fmt.Sprintf("gate evaluator reported unrecognized verdict %q", entry.Verdict),
			})
			overall = maxSeverityVerdict(overall, gate.VerdictError)
		}
	}
	return GateResult{OverallVerdict: overall, Criteria: results}
}

// errorAllCriteria is deriveGateResult's own fail-closed default: every
// declared Criterion becomes ERROR with the identical detail — never a
// partial result when the evaluator itself could not be trusted to run
// cleanly.
func errorAllCriteria(criteria []gate.Criterion, detail string) GateResult {
	results := make([]GateCriterionResult, 0, len(criteria))
	for _, c := range criteria {
		results = append(results, GateCriterionResult{Name: c.Name, EvidenceKey: c.EvidenceKey, Verdict: gate.VerdictError, Detail: detail})
	}
	return GateResult{OverallVerdict: gate.VerdictError, Criteria: results}
}

// maxSeverityVerdict returns whichever of a/b verdictSeverity ranks
// higher — see verdictSeverity's own doc comment for the ordering.
func maxSeverityVerdict(a, b gate.Verdict) gate.Verdict {
	if verdictSeverity[b] > verdictSeverity[a] {
		return b
	}
	return a
}

// staleMountRevision re-captures every resolved mount's own LIVE on-disk
// revision and compares it against what this Attempt's own ContextSnapshot
// already pinned (mount.VCSObjectID) — the identical CaptureRevision
// primitive V5-08C's own handleMutatingCancellation already uses for the
// same "has anything actually changed since we pinned this" question.
// Returns the first mismatched RepositoryID found (empty string when
// every mount is still fresh).
func staleMountRevision(ctx context.Context, workspaces ports.WorkspaceProvider, mounts []ports.AgentWorkspaceMount) (project.RepositoryID, error) {
	for _, mount := range mounts {
		current, err := workspaces.CaptureRevision(ctx, mount.Handle)
		if err != nil {
			return "", fmt.Errorf("capture current revision for repository %s: %w", mount.RepositoryID, err)
		}
		if current.VCSObjectID != mount.VCSObjectID {
			return mount.RepositoryID, nil
		}
	}
	return "", nil
}

// scratchDirectory creates a fresh, empty, local temp directory outside
// any repository's own workspace — see this file's own package doc
// comment for why this executor never uses a real repository mount as
// its own working directory. The caller MUST call cleanup once execution
// is over.
func scratchDirectory() (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "agentkit-gate-scratch-*")
	if err != nil {
		return "", nil, fmt.Errorf("create gate scratch directory: %w", err)
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

const gateResultArtifactMediaType = "application/vnd.agentkit.gate-result+json"

// persistGateResultArtifact persists gateResult as a durable artifact.
// Persisted for EVERY verdict, not only PASS — a FAILED/ERROR Gate result
// is exactly the kind of provenance-bearing record this task's own Mục
// tiêu names, and the caller (classify) reaches this on both its PASS and
// non-PASS paths.
//
// attachState is Orphan on both paths since the V5-09/V5-10 acceptance-gap
// remediation (2026-09-10 post-merge review, PR1+PR2):
// validateAndAttachEvidenceArtifactsTx (finalize.go) promotes it
// ORPHAN->ATTACHED itself, atomically with the criteria-level Evidence
// rows it also writes, from both the SUCCEEDED and FAILED branches —
// mirroring buildEvidence's own Phase 1 (Put/Verify, real I/O, no
// transaction) + Phase 2 (insert ORPHAN, one short transaction) split
// exactly.
//
// secretValues redacts every criterion's own Detail/Reason before they are
// ever marshaled — the evaluator's own stdout is free text an author
// wrote, and a resolved secret could easily end up echoed into one of
// these fields the same way it could end up in Command's own captured
// stdout/stderr.
func (e *GateNodeExecutor) persistGateResultArtifact(
	ctx context.Context, projectID project.ProjectID, attemptID string, result GateResult, attachState artifact.AttachState,
	secretValues map[string]string,
) (string, error) {
	scopedMatcher := e.matcher
	if len(secretValues) > 0 {
		values := make([]string, 0, len(secretValues))
		for _, v := range secretValues {
			values = append(values, v)
		}
		scopedMatcher = e.matcher.WithSecrets(values...)
	}
	for i, criterion := range result.Criteria {
		if redactedDetail, _ := scopedMatcher.Redact([]byte(criterion.Detail)); string(redactedDetail) != criterion.Detail {
			result.Criteria[i].Detail = string(redactedDetail)
		}
		if redactedReason, _ := scopedMatcher.Redact([]byte(criterion.Reason)); string(redactedReason) != criterion.Reason {
			result.Criteria[i].Reason = string(redactedReason)
		}
	}
	body, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode gate result artifact: %w", err)
	}
	ref, err := e.store.Put(ctx, ports.ArtifactMetadata{ContentType: gateResultArtifactMediaType, Sensitivity: redact.Sensitive, Redacted: true}, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("put gate result artifact: %w", err)
	}
	if err := e.store.Verify(ctx, ref); err != nil {
		return "", fmt.Errorf("verify gate result artifact: %w", err)
	}
	artifactID := e.ids.NewID()
	a, err := artifact.NewArtifact(
		artifact.ID(artifactID), projectID, ref.Locator, ref.SHA256, ref.Size, ref.ContentType,
		ref.Sensitivity, ref.Redacted, artifact.RetentionCanonicalContext, attachState, false, nil, e.clk.Now(), 1,
	)
	if err != nil {
		return "", fmt.Errorf("construct gate result artifact record for attempt %s: %w", attemptID, err)
	}
	if err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Artifacts().InsertArtifact(ctx, a)
		return err
	}); err != nil {
		return "", fmt.Errorf("insert gate result artifact: %w", err)
	}
	return artifactID, nil
}

// gateExecutionInputs is gatherGateExecutionInputs's own output — plain
// data only, gathered inside one read-only transaction.
type gateExecutionInputs struct {
	commandDoc           command.CommandDocument
	criteria             []gate.Criterion
	scriptPayload        []byte
	effectiveScope       []workdomain.RepositoryScope
	workspaceMounts      []ports.AgentWorkspaceMount
	timeout              time.Duration
	allowedOutcomes      []string
	snapshotID           string
	executionProfileHash string
}

// gatherGateExecutionInputs is GateNodeExecutor's own Phase 1 — the
// GATE-shaped counterpart of gatherCommandExecutionInputs
// (command_node_executor.go): identical attempt/nodeRun/run/snapshot
// cross-checks, but loads and decodes TWO pinned definitions instead of
// one — the GateVersion itself (Executor.VersionID/DefinitionID, from
// resolveExecutionProfile's own MACHINE_GATE branch, schedule.go) via
// decodeCompiledGate, then the CommandVersion it names
// (GateDocument.CommandRef) via decodeCompiledCommand, exactly the same
// "re-load fresh from the pin, never denormalize" discipline
// gatherCommandExecutionInputs already established. Its own final step,
// forceReadOnlyMounts, downgrades every resolved mount to READ-ONLY
// regardless of what this NodeRun's own (WorkItem-wide) EffectiveScope
// actually grants — see this file's own package doc comment for why a
// Gate is never rejected outright just because its WorkItem happens to
// be WRITE-granted somewhere.
func gatherGateExecutionInputs(ctx context.Context, uow ports.UnitOfWork, req ports.NodeExecutionRequest) (gateExecutionInputs, error) {
	var inputs gateExecutionInputs
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(ctx, req.AttemptID)
		if err != nil {
			return err
		}
		if string(attempt.NodeRunID) != req.NodeRunID {
			return fmt.Errorf("%w: attempt %s belongs to node run %s, not %s", ErrContextSnapshotUnverified, req.AttemptID, attempt.NodeRunID, req.NodeRunID)
		}
		if attempt.ContextSnapshotID == nil {
			return fmt.Errorf("%w: attempt %s has no bound context snapshot", ErrContextSnapshotUnverified, req.AttemptID)
		}

		nodeRun, err := tx.Runtime().GetNodeRun(ctx, req.NodeRunID)
		if err != nil {
			return err
		}
		if string(nodeRun.RunID) != req.RunID {
			return fmt.Errorf("%w: node run %s belongs to run %s, not %s", ErrNodeRunMismatch, req.NodeRunID, nodeRun.RunID, req.RunID)
		}

		run, err := tx.Runtime().GetWorkflowRun(ctx, req.RunID)
		if err != nil {
			return err
		}

		snapshot, err := tx.ContextSnapshots().GetSnapshot(ctx, string(*attempt.ContextSnapshotID))
		if err != nil {
			return fmt.Errorf("%w: load snapshot %s: %v", ErrContextSnapshotUnverified, *attempt.ContextSnapshotID, err)
		}
		if string(snapshot.AttemptID) != req.AttemptID {
			return fmt.Errorf("%w: snapshot %s is bound to attempt %s, not %s", ErrContextSnapshotUnverified, snapshot.ID, snapshot.AttemptID, req.AttemptID)
		}

		version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID))
		if err != nil {
			return err
		}
		document := version.Document()
		node, ok := findNode(document, nodeRun.NodeKey)
		if !ok {
			return fmt.Errorf("runtime: node %s not found in workflow version %s", nodeRun.NodeKey, run.WorkflowVersionID)
		}
		allowedOutcomes := agentSelectableOutcomes(node)
		if len(allowedOutcomes) == 0 {
			return fmt.Errorf("runtime: node %s has no selectable outcome (every declared outcome is its own CyclePolicy escalation outcome) — cannot execute it", nodeRun.NodeKey)
		}

		decision, err := tx.Runtime().GetDecisionArtifact(ctx, req.NodeRunID+"-execution-profile-v1")
		if err != nil {
			return fmt.Errorf("runtime: load execution profile decision for node run %s: %w", req.NodeRunID, err)
		}
		var profile resolvedExecutionProfileView
		if err := json.Unmarshal(decision.Result, &profile); err != nil {
			return fmt.Errorf("runtime: decode execution profile decision for node run %s: %w", req.NodeRunID, err)
		}

		gateVersion, err := tx.Definitions().LoadVersion(ctx, profile.Executor.VersionID)
		if err != nil {
			return fmt.Errorf("runtime: load gate version %s: %w", profile.Executor.VersionID, err)
		}
		if gateVersion.DefinitionID() != profile.Executor.DefinitionID || gateVersion.CompiledHash() != profile.Executor.CompiledHash {
			return fmt.Errorf("runtime: gate version %s no longer matches its own pin (definitionId/compiledHash changed)", profile.Executor.VersionID)
		}
		gateDoc, err := decodeCompiledGate(gateVersion.CompiledSnapshot())
		if err != nil {
			return fmt.Errorf("runtime: node %s: %w", nodeRun.NodeKey, err)
		}

		commandVersion, err := tx.Definitions().LoadVersion(ctx, gateDoc.CommandRef.VersionID)
		if err != nil {
			return fmt.Errorf("runtime: load gate's own pinned command version %s: %w", gateDoc.CommandRef.VersionID, err)
		}
		// V5-10 acceptance-gap remediation (2026-09-10 post-merge review):
		// re-verify CommandRef's own exact DefinitionID after load — this
		// pin was loaded by VersionID alone until now, unlike the
		// GateVersion pin immediately above (which already re-checks
		// DefinitionID/CompiledHash). A VersionID that happened to resolve
		// to a version under the WRONG DefinitionID would otherwise be
		// evaluated as if it were the Gate's own genuinely pinned Command.
		if commandVersion.DefinitionID() != gateDoc.CommandRef.DefinitionID {
			return fmt.Errorf("runtime: gate's own pinned command version %s belongs to definition %s, not %s",
				gateDoc.CommandRef.VersionID, commandVersion.DefinitionID(), gateDoc.CommandRef.DefinitionID)
		}
		commandDoc, err := decodeCompiledCommand(commandVersion.CompiledSnapshot())
		if err != nil {
			return fmt.Errorf("runtime: node %s: %w", nodeRun.NodeKey, err)
		}

		scriptCandidate, err := loadResourceCandidate(ctx, tx, policy.ResourceRef{
			OwnerVersionID: commandDoc.Executable.OwnerVersionID, ResourceKey: commandDoc.Executable.ResourceKey, ContentHash: commandDoc.Executable.ContentHash,
		})
		if err != nil {
			return fmt.Errorf("runtime: load gate's own evaluator resource: %w", err)
		}

		timeout := time.Duration(profile.TimeoutSeconds) * time.Second
		if commandDoc.TimeoutSeconds > 0 {
			if cmdTimeout := time.Duration(commandDoc.TimeoutSeconds) * time.Second; cmdTimeout < timeout {
				timeout = cmdTimeout
			}
		}

		inputs = gateExecutionInputs{
			commandDoc: commandDoc, criteria: gateDoc.Criteria, scriptPayload: scriptCandidate.Payload,
			effectiveScope: nodeRun.EffectiveScope, workspaceMounts: forceReadOnlyMounts(assembleWorkspaceMounts(nodeRun.EffectiveScope, snapshot.Revisions.Entries())),
			timeout: timeout, allowedOutcomes: allowedOutcomes, snapshotID: string(snapshot.ID),
			executionProfileHash: attempt.ExecutionProfileHash,
		}
		return nil
	})
	return inputs, err
}

// forceReadOnlyMounts returns a copy of mounts with every Access field
// downgraded to ports.WorkspaceReadOnly — this executor's own sole
// enforcement point for "a Gate never mutates anything", applied
// unconditionally regardless of what the underlying (WorkItem-wide)
// EffectiveScope actually grants (see this file's own package doc
// comment for why rejecting outright would be wrong). A mount already
// read-only is unchanged; nothing else about it (RepositoryID, VCSObjectID,
// WorkspaceGeneration) is touched.
func forceReadOnlyMounts(mounts []ports.AgentWorkspaceMount) []ports.AgentWorkspaceMount {
	readOnly := make([]ports.AgentWorkspaceMount, len(mounts))
	for i, m := range mounts {
		m.Access = ports.WorkspaceReadOnly
		readOnly[i] = m
	}
	return readOnly
}
