// CommandNodeExecutor is V5-09's own production ports.NodeExecutor — the
// COMMAND-node sibling of AgentNodeExecutor (agent_node_executor.go,
// V5-08B), reusing the same admission (already kind-agnostic,
// admission.go), evidence-staging (buildEvidence/resolveExecutionResources,
// extracted to free functions this file calls directly), and V5-08C
// cancellation-disposition path (classifyCancellation/handleMutatingCancellation)
// AGENT already established — the design doc's own locked requirement is
// explicit: "COMMAND node dùng lại chính đường này" (V5-08C) and "cancel
// giữa một mutating command dùng đúng đường V5-08C, không có đường
// terminate riêng" (V5-09).
//
// What genuinely differs from AGENT: there is no ports.AgentExecutor
// adapter/streaming-JSONL protocol to go through at all — this file spawns
// a real process DIRECTLY via ports.ProcessSupervisor.Run (the exact same
// primitive claude.go/codex.go already use one layer down), and there is
// no terminal outcome MARKER to parse — success/failure is the process's
// own exit code, never a parsed assistant message.
//
// Every real I/O this file performs (loading the pinned CommandDocument's
// own script resource, resolving argv/cwd/secret values, spawning the
// process, capturing output) runs entirely outside any database
// transaction, the identical "gather in a read-only Tx, then real I/O"
// discipline every other V5-08/V5-09 task already established.
//
// This bridge's own two events (EXECUTION_STARTED, EXECUTION_FINISHED) are
// emitted through the exact same agentevents.Sink AGENT uses — its own
// AgentEventKind vocabulary is already generic (EXECUTION_STARTED/FINISHED
// carry no chat-specific meaning), and validateAndAttachFinalizationEvidenceTx's own
// finalize-time re-validation (finalize.go) unconditionally requires a
// real agent_events row matching TerminalEventSequence for ANY
// Evidence-bearing attempt — reusing Sink is what lets this executor reuse
// the REST of AGENT's evidence/checkpoint/lease-fencing machinery instead
// of inventing a second, parallel one.
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
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
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// ErrCommandInvocationUnresolvable is returned when a COMMAND's own
// declared Argv/Cwd/SecretRefs cannot be resolved against this NodeRun's
// own real EffectiveScope/worker environment — an unknown placeholder
// target, a cwd repository target outside scope, or a secret this worker
// cannot resolve. Always a FAILED classification (the process never even
// spawns), never ErrIndeterminateExecution — nothing was ever started, so
// there is no ambiguity about what state it left anything in.
var ErrCommandInvocationUnresolvable = errors.New("runtime: command's own argv/cwd/secret declaration could not be resolved against this node run")

// CommandNodeExecutor's own dependencies mirror AgentNodeExecutor's
// (agent_node_executor.go's own doc comment: this package still has no
// composition root) plus the two genuinely new ones COMMAND needs:
// supervisor (spawns the real process directly) and secrets (resolves
// SecretRefs into env values right before spawn).
type CommandNodeExecutor struct {
	uow           ports.UnitOfWork
	ids           idsource.Source
	store         ports.ArtifactStore
	workspaces    ports.WorkspaceProvider
	writeLeases   ports.WriteLeaseManager
	supervisor    ports.ProcessSupervisor
	secrets       ports.SecretResolver
	registry      *eventschema.Registry
	matcher       redact.Matcher
	checkpoints   agentevents.CheckpointStore
	clk           clock.Clock
	interruptions worker.InterruptionRecoveryStore
	reconciler    worker.WorkspaceReconciler
}

// NewCommandNodeExecutor returns a ready-to-register CommandNodeExecutor.
func NewCommandNodeExecutor(
	uow ports.UnitOfWork, ids idsource.Source, store ports.ArtifactStore, workspaces ports.WorkspaceProvider,
	writeLeases ports.WriteLeaseManager, supervisor ports.ProcessSupervisor, secrets ports.SecretResolver,
	registry *eventschema.Registry, matcher redact.Matcher, checkpoints agentevents.CheckpointStore, clk clock.Clock,
	interruptions worker.InterruptionRecoveryStore, reconciler worker.WorkspaceReconciler,
) *CommandNodeExecutor {
	return &CommandNodeExecutor{
		uow: uow, ids: ids, store: store, workspaces: workspaces, writeLeases: writeLeases,
		supervisor: supervisor, secrets: secrets, registry: registry, matcher: matcher,
		checkpoints: checkpoints, clk: clk, interruptions: interruptions, reconciler: reconciler,
	}
}

var _ ports.NodeExecutor = (*CommandNodeExecutor)(nil)

// Execute implements ports.NodeExecutor. See this file's own package doc
// comment for the full design.
func (e *CommandNodeExecutor) Execute(ctx context.Context, req ports.NodeExecutionRequest) (ports.NodeExecutionResult, error) {
	inputs, err := gatherCommandExecutionInputs(ctx, e.uow, req)
	if err != nil {
		// V5-09 acceptance-gap remediation (2026-09-10 post-merge review):
		// an incompatible OS or an unauthorized NetworkAccess declaration
		// (verifyCommandCompatibilityAndPolicy, inside
		// gatherCommandExecutionInputs's own transaction) is exactly as
		// deterministic and pre-spawn as an unresolvable argv/cwd/secret —
		// never a transient error worth retrying, so it gets the identical
		// typed FAILED classification, not a hard error the caller would
		// otherwise treat as worth investigating/retrying.
		if errors.Is(err, ErrCommandInvocationUnresolvable) {
			return ports.NodeExecutionResult{
				State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
				ErrorCode: errorcode.CodeValidationFailed,
			}, nil
		}
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: gather command execution inputs: %w", err)
	}

	request := ports.AgentExecutionRequest{
		AttemptID: ports.ExecutionAttemptID(req.AttemptID), EffectiveScope: inputs.effectiveScope,
		WorkspaceMounts: inputs.workspaceMounts, Timeout: inputs.timeout, AllowedOutcomes: inputs.allowedOutcomes,
		ExecutionProfileHash: inputs.executionProfileHash,
	}

	resolved, err := resolveExecutionResources(ctx, e.uow, e.workspaces, e.writeLeases, req, request)
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: resolve command execution resources: %w", err)
	}
	request.WorkspaceMounts = resolved.mounts

	argv, cwd, env, err := resolveCommandInvocation(ctx, inputs.doc, resolved.mounts, e.secrets)
	if err != nil {
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			ErrorCode: errorcode.CodeValidationFailed,
		}, nil
	}

	scriptPath, cleanup, err := materializeExecutable(inputs.scriptPayload, inputs.doc.Executable.ResourceKey)
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: materialize command executable: %w", err)
	}
	defer cleanup()

	sink, err := agentevents.NewSink(ctx, agentevents.Config{
		RunID: req.RunID, NodeRunID: req.NodeRunID, AttemptID: req.AttemptID,
		ContextSnapshotID: string(inputs.snapshotID), EffectiveScope: inputs.effectiveScope,
		Mounts: resolved.sinkMounts, Workspaces: e.workspaces, Registry: e.registry, Matcher: e.matcher,
		UOW: e.uow, Checkpoints: e.checkpoints, IDs: e.ids, Clock: e.clk,
		JobLease: req.JobLease, WriteLeases: resolved.writeLeaseGrants,
	})
	if err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: construct command event sink: %w", err)
	}

	if err := sink.Accept(ctx, ports.AgentEvent{
		AttemptID: ports.ExecutionAttemptID(req.AttemptID), Sequence: 1,
		Kind: ports.AgentEventExecutionStarted, ObservedAt: time.Now().UTC(),
	}); err != nil {
		if errors.Is(err, ports.ErrJobLeaseLost) || errors.Is(err, ports.ErrWriteLeaseLost) {
			return ports.NodeExecutionResult{}, fmt.Errorf("%w: job/write lease lost before command spawn: %v", ErrIndeterminateExecution, err)
		}
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: emit command execution-started event: %w", err)
	}

	var stdout, stderr bytes.Buffer
	result, runErr := e.supervisor.Run(ctx, ports.ProcessSpec{
		ID: ports.ProcessID(req.AttemptID), Executable: scriptPath, Argv: argv, WorkingDirectory: cwd,
		Environment: env, InheritedEnvironment: inputs.doc.EnvAllowlist, Timeout: inputs.timeout,
		OutputLimitBytes: int(inputs.doc.Output.MaxOutputBytes),
	}, &stdout, &stderr)

	acceptErr := sink.Accept(ctx, ports.AgentEvent{
		AttemptID: ports.ExecutionAttemptID(req.AttemptID), Sequence: 2,
		Kind: ports.AgentEventExecutionFinished, ObservedAt: time.Now().UTC(),
		Message: fmt.Sprintf("exit code %d", result.ExitCode),
	})
	flushErr := sink.Flush(ctx)

	return e.classify(ctx, req, request, resolved, inputs.doc, result, runErr, firstNonNil(acceptErr, flushErr), stdout.Bytes(), stderr.Bytes(), env)
}

// classify maps one completed ProcessSupervisor.Run call into either a
// genuine NodeExecutionResult proposal or an error signaling "do not
// finalize" — the COMMAND-side counterpart of AgentNodeExecutor's own
// classify (agent_node_executor.go), same ordering, same reused
// cancellation path, but exit-code-driven rather than marker-driven.
func (e *CommandNodeExecutor) classify(
	ctx context.Context, req ports.NodeExecutionRequest, request ports.AgentExecutionRequest, resolved resolvedExecutionResources,
	doc command.CommandDocument, result ports.ProcessResult, runErr error, syncErr error, stdout, stderr []byte, secretValues map[string]string,
) (ports.NodeExecutionResult, error) {
	if errors.Is(syncErr, ports.ErrJobLeaseLost) || errors.Is(syncErr, ports.ErrWriteLeaseLost) {
		return ports.NodeExecutionResult{}, fmt.Errorf("%w: job/write lease lost mid-execution: %v", ErrIndeterminateExecution, syncErr)
	}
	// V5-09's own restatement of V5-08B's own locked decision #3: a
	// mutating attempt must never have its own terminal disposition
	// trusted while process-tree quiescence was never confirmed.
	if !result.TreeQuiesced && resolved.hasWriteMount {
		return ports.NodeExecutionResult{}, fmt.Errorf("%w: process tree did not confirm quiescence on a mutating attempt", ErrIndeterminateExecution)
	}
	if runErr != nil {
		// A bare Go error spawning the process itself (executable missing,
		// permission denied) with quiescence otherwise confirmed — the
		// same row-1 mapping AgentNodeExecutor's own classify already uses
		// for an analogous bare provider-spawn error.
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			ErrorCode: errorcode.CodeProviderUnavailable,
		}, nil
	}
	if result.Cancelled {
		// V5-09's own locked requirement: reuse V5-08C's cancellation path
		// exactly, no separate terminate path.
		return classifyCancellation(ctx, e.uow, e.interruptions, e.workspaces, e.reconciler, e.writeLeases, req, resolved)
	}
	if result.TimedOut {
		// Folds into the same generic FAILED bucket AgentNodeExecutor's own
		// classify already documents for a provider-internal timeout —
		// execute.go's own OUTER attemptCtx deadline is what actually
		// routes TIMED_OUT for this envelope, not this executor.
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			ErrorCode: errorcode.CodeTimeout,
		}, nil
	}
	if result.ExitCode != 0 {
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			ErrorCode: errorcode.CodeExecutionFailed,
		}, nil
	}
	// V5-09 acceptance-gap remediation (2026-09-10 post-merge review):
	// ports.ProcessResult.OutputTruncated's own doc comment already says
	// "the caller must never treat a truncated capture as a complete one
	// for evidence purposes" — never wired up until now. A truncated
	// capture means bytes beyond OutputLimitBytes were silently discarded;
	// an exit code of 0 alongside that tells us nothing about what the
	// discarded bytes would have shown, so this can never be trusted as a
	// clean success. Reuses CodeExecutionFailed rather than a new code:
	// go-core-spec §18's own error model is a closed, spec-enumerated set
	// (internal/domain/errorcode's own doc comment) — adding a value there
	// is a spec change, not something this remediation invents unilaterally.
	if result.OutputTruncated {
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			ErrorCode: errorcode.CodeExecutionFailed,
		}, nil
	}
	if syncErr != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: flush command event sink after a successful execution: %w", syncErr)
	}

	// A COMMAND node never proposes an outcome itself (no marker protocol
	// exists for it) — proposed is always nil, so this only ever succeeds
	// when exactly one outcome is selectable, the identical restriction
	// AgentNodeExecutor's own single-outcome derivation already enforces.
	selectedOutcome, proposedOutcome, err := resolveSelectedOutcome(nil, request.AllowedOutcomes)
	if err != nil {
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonOutcomeRejected,
			ErrorCode: errorcode.CodeValidationFailed,
		}, nil
	}

	evidence, err := buildEvidence(ctx, e.uow, e.ids, e.clk, e.store, e.workspaces, req, request, resolved, proposedOutcome)
	if err != nil {
		if errors.Is(err, scopeguard.ErrScopeViolation) {
			return ports.NodeExecutionResult{
				State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonScopeViolation,
				ErrorCode: errorcode.CodeScopeViolation,
			}, nil
		}
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: build command finalization evidence: %w", err)
	}

	if doc.Output.CaptureStdout || doc.Output.CaptureStderr {
		outputArtifactID, err := e.persistCommandOutputArtifact(ctx, req, resolved.projectID, doc, stdout, stderr, secretValues)
		if err != nil {
			return ports.NodeExecutionResult{}, fmt.Errorf("runtime: persist command output artifact: %w", err)
		}
		evidence.OutputArtifactRefs = append(evidence.OutputArtifactRefs, outputArtifactID)
		// V5-09 acceptance-gap remediation (2026-09-10 post-merge review):
		// one Evidence row for this execution — skipped entirely when
		// output capture is off (nothing to reference; runtime.NewEvidence
		// itself requires at least one artifact reference, the same
		// invariant Checkpoint already enforces).
		evidence.EvidenceEntries = append(evidence.EvidenceEntries, ports.EvidenceProposal{
			Kind: runtimedomain.EvidenceKindCommandExecution, Verdict: runtimedomain.EvidenceVerdictSucceeded,
			ArtifactReferences: []string{outputArtifactID}, PolicyVersion: request.ExecutionProfileHash,
		})
	}

	return ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: selectedOutcome, Evidence: evidence,
	}, nil
}

// commandOutputArtifactContent is the exact JSON shape
// persistCommandOutputArtifact stores — only the streams doc.Output
// actually asked to capture are non-empty, matching the declared
// CaptureStdout/CaptureStderr contract precisely rather than always
// persisting both regardless of what was authorized.
type commandOutputArtifactContent struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
}

const commandOutputArtifactMediaType = "application/vnd.agentkit.command-output+json"

// persistCommandOutputArtifact persists this attempt's own captured
// output as a durable artifact, staged ORPHAN — V5-09 acceptance-gap
// remediation (2026-09-10 post-merge review): this used to insert directly
// ATTACHED, in its own unfenced transaction, before finalize.go's own
// evidence re-validation had any promotion step that would ever reach it.
// validateAndAttachFinalizationEvidenceTx (finalize.go) now promotes this
// artifact ORPHAN->ATTACHED itself, atomically with everything else it
// commits — mirroring buildEvidence's own Phase 1 (Put/Verify, real I/O,
// no transaction) + Phase 2 (insert ORPHAN, one short transaction) split
// exactly.
func (e *CommandNodeExecutor) persistCommandOutputArtifact(
	ctx context.Context, req ports.NodeExecutionRequest, projectID project.ProjectID, doc command.CommandDocument,
	stdout, stderr []byte, secretValues map[string]string,
) (string, error) {
	// V5-09 acceptance-gap remediation (2026-09-10 post-merge review): a
	// resolved secret must never reach persisted output unredacted — scope
	// a Matcher to THIS execution's own just-resolved SecretRefs (never
	// folded into e.matcher itself, which every other caller still shares)
	// and scrub both streams before they are ever marshaled, exactly the
	// "redact BEFORE Put" ordering internal/app/message's own AppendMessage
	// already establishes.
	scopedMatcher := e.matcher
	if len(secretValues) > 0 {
		values := make([]string, 0, len(secretValues))
		for _, v := range secretValues {
			values = append(values, v)
		}
		scopedMatcher = e.matcher.WithSecrets(values...)
	}
	content := commandOutputArtifactContent{}
	if doc.Output.CaptureStdout {
		redactedStdout, _ := scopedMatcher.Redact(stdout)
		content.Stdout = string(redactedStdout)
	}
	if doc.Output.CaptureStderr {
		redactedStderr, _ := scopedMatcher.Redact(stderr)
		content.Stderr = string(redactedStderr)
	}
	body, err := json.Marshal(content)
	if err != nil {
		return "", fmt.Errorf("encode command output artifact: %w", err)
	}
	ref, err := e.store.Put(ctx, ports.ArtifactMetadata{ContentType: commandOutputArtifactMediaType, Sensitivity: redact.Sensitive, Redacted: true}, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("put command output artifact: %w", err)
	}
	if err := e.store.Verify(ctx, ref); err != nil {
		return "", fmt.Errorf("verify command output artifact: %w", err)
	}
	artifactID := e.ids.NewID()
	a, err := artifact.NewArtifact(
		artifact.ID(artifactID), projectID, ref.Locator, ref.SHA256, ref.Size, ref.ContentType,
		ref.Sensitivity, ref.Redacted, artifact.RetentionCanonicalContext, artifact.Orphan, false, nil, e.clk.Now(), 1,
	)
	if err != nil {
		return "", fmt.Errorf("construct command output artifact record: %w", err)
	}
	if err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Artifacts().InsertArtifact(ctx, a)
		return err
	}); err != nil {
		return "", fmt.Errorf("insert command output artifact: %w", err)
	}
	return artifactID, nil
}

// resolveCommandInvocation resolves doc's own Argv/Cwd/SecretRefs into
// real, spawnable values — argv-only substitution (never a shell string:
// PLACEHOLDER elements substitute into their OWN argv slot and no other,
// exactly like command.CommandDocument's own doc comment requires), cwd
// resolved against CwdRepositoryTarget, secrets resolved into an
// Environment map (never Argv — see ports.SecretResolver's own doc
// comment for why a secret value must never appear in a process's own
// argv). Each PLACEHOLDER/CwdRepositoryTarget name is looked up directly
// against mounts by RepositoryID — the convention this task locks in
// (confirmed by reading the code, not asked): a Command's own
// PlaceholderAllowlist names are validated closed at publish time
// (command.ValidateDocument) but never checked against a real
// EffectiveScope until now, since EffectiveScope varies per WorkItem/Run;
// a name that does not match any repository actually granted to this
// NodeRun fails closed here rather than ever substituting something
// unintended.
func resolveCommandInvocation(
	ctx context.Context, doc command.CommandDocument, mounts []ports.AgentWorkspaceMount, secrets ports.SecretResolver,
) (argv []string, cwd string, env map[string]string, err error) {
	mountByRepo := mountsByRepositoryID(mounts)
	cwdMount, ok := mountByRepo[doc.CwdRepositoryTarget]
	if !ok {
		return nil, "", nil, fmt.Errorf("%w: cwd repository target %q is not in this node run's effective scope", ErrCommandInvocationUnresolvable, doc.CwdRepositoryTarget)
	}
	argv, env, err = resolveArgvAndSecrets(ctx, doc, mountByRepo, secrets)
	if err != nil {
		return nil, "", nil, err
	}
	return argv, cwdMount.WorkingDirectory, env, nil
}

// mountsByRepositoryID is resolveCommandInvocation/resolveArgvAndSecrets's
// own shared lookup builder — a free function (V5-10: GateNodeExecutor,
// gate_node_executor.go, needs the identical map to resolve its own
// evaluated Command's argv/secrets, just never for cwd — see this file's
// own package doc comment for why Gate's cwd is never a real repository
// mount at all).
func mountsByRepositoryID(mounts []ports.AgentWorkspaceMount) map[string]ports.AgentWorkspaceMount {
	byRepo := make(map[string]ports.AgentWorkspaceMount, len(mounts))
	for _, m := range mounts {
		byRepo[string(m.RepositoryID)] = m
	}
	return byRepo
}

// resolveArgvAndSecrets resolves doc's own Argv/SecretRefs into real,
// spawnable values — argv-only substitution (never a shell string:
// PLACEHOLDER elements substitute into their OWN argv slot and no other,
// exactly like command.CommandDocument's own doc comment requires),
// secrets resolved into an Environment map (never Argv — see
// ports.SecretResolver's own doc comment for why a secret value must
// never appear in a process's own argv). Each PLACEHOLDER name is looked
// up directly against mountByRepo by RepositoryID — the convention this
// task locks in (confirmed by reading the code, not asked): a Command's
// own PlaceholderAllowlist names are validated closed at publish time
// (command.ValidateDocument) but never checked against a real
// EffectiveScope until now, since EffectiveScope varies per WorkItem/Run;
// a name that does not match any repository actually granted to this
// NodeRun fails closed here rather than ever substituting something
// unintended.
//
// A free function shared by resolveCommandInvocation (COMMAND, cwd
// resolves to a real repository mount) and GateNodeExecutor (V5-10, cwd
// is always a fresh scratch directory instead) — cwd resolution is
// deliberately NOT part of this function; each caller resolves its own.
func resolveArgvAndSecrets(
	ctx context.Context, doc command.CommandDocument, mountByRepo map[string]ports.AgentWorkspaceMount, secrets ports.SecretResolver,
) (argv []string, env map[string]string, err error) {
	argv = make([]string, 0, len(doc.Argv))
	for i, element := range doc.Argv {
		switch element.Kind {
		case command.ArgvLiteral:
			argv = append(argv, element.Value)
		case command.ArgvPlaceholder:
			mount, ok := mountByRepo[element.Value]
			if !ok {
				return nil, nil, fmt.Errorf("%w: argv[%d] placeholder %q does not name a repository in this node run's effective scope", ErrCommandInvocationUnresolvable, i, element.Value)
			}
			argv = append(argv, mount.WorkingDirectory)
		default:
			return nil, nil, fmt.Errorf("%w: argv[%d] has unsupported kind %q", ErrCommandInvocationUnresolvable, i, element.Kind)
		}
	}

	if len(doc.SecretRefs) > 0 {
		env = make(map[string]string, len(doc.SecretRefs))
		for _, ref := range doc.SecretRefs {
			value, resolveErr := secrets.Resolve(ctx, ref)
			if resolveErr != nil {
				return nil, nil, fmt.Errorf("%w: secret %q: %v", ErrCommandInvocationUnresolvable, ref, resolveErr)
			}
			env[ref] = value
		}
	}
	return argv, env, nil
}

// materializeExecutable writes payload to a fresh, real, local file — a
// Command's own script resource lives as content inside a published
// Skill/Layer version (loadResourceCandidate's own already-content-hash-
// verified Payload; see gatherCommandExecutionInputs), never as a file
// that already exists anywhere, so this is the one place that content
// ever becomes something an OS can spawn. name is derived from the
// resource's own ResourceKey (its final path segment) so an author-
// declared extension (.sh, .ps1, ...) survives — this codebase has no
// OS-level sandbox for script execution (ADR-013/ADR-023,
// internal/adapters/process.IsolationChecker's own honest admission),
// so which interpreter actually runs the file is left entirely to the
// author's own Compatibility declaration and the file's own extension,
// exactly like any other locally-installed script would be. The caller
// MUST call cleanup once execution is over.
func materializeExecutable(payload []byte, resourceKey string) (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "agentkit-command-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp directory for command executable: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	name := filepath.Base(resourceKey)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "command-executable"
	}
	path = filepath.Join(dir, name)
	if err := os.WriteFile(path, payload, 0o755); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write command executable: %w", err)
	}
	return path, cleanup, nil
}

// commandExecutionInputs is gatherCommandExecutionInputs's own output —
// plain data only, gathered inside one read-only transaction.
type commandExecutionInputs struct {
	doc                  command.CommandDocument
	scriptPayload        []byte
	effectiveScope       []workdomain.RepositoryScope
	workspaceMounts      []ports.AgentWorkspaceMount
	timeout              time.Duration
	allowedOutcomes      []string
	snapshotID           string
	executionProfileHash string
}

// networkAccessCapability is the named PermissionRules.GrantedCapabilities
// string (V5-09 acceptance-gap remediation, 2026-09-10 post-merge review)
// a pinned PERMISSION policy must grant before a CommandDocument declaring
// NetworkAccessAllowed may ever spawn — the same extensible-by-name
// capability-grant convention PermissionRules.GrantedCapabilities's own
// doc comment already establishes via INTEGRATION_MULTI_REPOSITORY_WRITE
// as "the one concrete example."
const networkAccessCapability = "NETWORK_ACCESS"

// verifyCommandCompatibilityAndPolicy is the V5-09 acceptance-gap
// remediation's own new pre-spawn gate (2026-09-10 post-merge review:
// "CommandDocument.PolicyRefs chưa được đọc ở execution time; compatibility
// OS/toolchain và NetworkAccess chưa được verify/enforce"):
//
//  1. OS compatibility: doc.Compatibility.OS must name the worker's own
//     real runtime.GOOS. Every published CommandDocument already has a
//     non-empty OS list (validateCompatibility, command/validate.go,
//     rejects an empty one at publish time) — the len(...)>0 guard below
//     is defense in depth for a document that reached this function some
//     other way, never a "no constraint" escape hatch. Toolchain
//     compatibility is deliberately NOT
//     checked here — this codebase has no toolchain-version-probing
//     mechanism anywhere to check it against (a real gap, left honestly
//     unaddressed rather than a fabricated always-pass check).
//  2. NetworkAccess: NetworkAccessAllowed requires at least one of
//     doc.PolicyRefs to resolve to a real, published PolicyVersion whose
//     own DefinitionID matches the pin (never trusted by VersionID alone —
//     the same re-verification discipline this remediation already added
//     for Gate's own CommandRef) and whose PERMISSION rules grant
//     networkAccessCapability. This is authorization, not sandboxing: Alpha
//     has no OS-level sandbox for script execution (materializeExecutable's
//     own doc comment) to actually PREVENT a network call technically —
//     "enforce" here means the declaration must be authorized before spawn,
//     not that the spawned process is physically contained.
//
// Every doc.PolicyRefs entry is resolved regardless of NetworkAccess (an
// unresolvable pin is rejected either way) — this is also this task's own
// "PolicyRefs chưa được đọc ở execution time" fix: they are now read and
// verified unconditionally, not only consulted for the one capability this
// function currently cares about.
func verifyCommandCompatibilityAndPolicy(ctx context.Context, tx ports.Tx, doc command.CommandDocument) error {
	if len(doc.Compatibility.OS) > 0 {
		matched := false
		for _, os := range doc.Compatibility.OS {
			if os == goruntime.GOOS {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%w: command declares OS compatibility %v, worker is %s", ErrCommandInvocationUnresolvable, doc.Compatibility.OS, goruntime.GOOS)
		}
	}

	networkGranted := false
	for _, ref := range doc.PolicyRefs {
		version, err := tx.Definitions().LoadVersion(ctx, ref.VersionID)
		if err != nil {
			return fmt.Errorf("%w: resolve command policy ref %s: %v", ErrCommandInvocationUnresolvable, ref.VersionID, err)
		}
		if version.DefinitionID() != ref.DefinitionID {
			return fmt.Errorf("%w: command policy ref %s belongs to definition %s, not %s",
				ErrCommandInvocationUnresolvable, ref.VersionID, version.DefinitionID(), ref.DefinitionID)
		}
		policyDoc, err := decodeCompiledPolicy(version.CompiledSnapshot())
		if err != nil {
			return fmt.Errorf("%w: decode command policy ref %s: %v", ErrCommandInvocationUnresolvable, ref.VersionID, err)
		}
		if policyDoc.Permission == nil {
			continue
		}
		for _, capability := range policyDoc.Permission.GrantedCapabilities {
			if capability == networkAccessCapability {
				networkGranted = true
			}
		}
	}
	if doc.NetworkAccess == command.NetworkAccessAllowed && !networkGranted {
		return fmt.Errorf("%w: command declares NetworkAccess=%s but no pinned policy grants %s",
			ErrCommandInvocationUnresolvable, command.NetworkAccessAllowed, networkAccessCapability)
	}
	return nil
}

// gatherCommandExecutionInputs is CommandNodeExecutor's own Phase 1 —
// the COMMAND-shaped counterpart of gatherAssembledRequestInputs
// (assemble_execution_request.go): the SAME attempt/nodeRun/run/snapshot
// cross-checks that function already establishes for AGENT, but never
// builds an InstructionArtifact (a Command has no "prompt" — no Messages,
// no Resources beyond its own Executable) — instead it loads and decodes
// the pinned CommandVersion itself (never eagerly decoded by
// resolveExecutionProfile, unlike AGENT's own AgentProfileDocument — see
// decodeCompiledCommand's own doc comment for why) and its own Executable
// resource candidate, content-hash-verified by loadResourceCandidate.
func gatherCommandExecutionInputs(ctx context.Context, uow ports.UnitOfWork, req ports.NodeExecutionRequest) (commandExecutionInputs, error) {
	var inputs commandExecutionInputs
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

		commandVersion, err := tx.Definitions().LoadVersion(ctx, profile.Executor.VersionID)
		if err != nil {
			return fmt.Errorf("runtime: load command version %s: %w", profile.Executor.VersionID, err)
		}
		if commandVersion.DefinitionID() != profile.Executor.DefinitionID || commandVersion.CompiledHash() != profile.Executor.CompiledHash {
			return fmt.Errorf("runtime: command version %s no longer matches its own pin (definitionId/compiledHash changed)", profile.Executor.VersionID)
		}
		doc, err := decodeCompiledCommand(commandVersion.CompiledSnapshot())
		if err != nil {
			return fmt.Errorf("runtime: node %s: %w", nodeRun.NodeKey, err)
		}
		if err := verifyCommandCompatibilityAndPolicy(ctx, tx, doc); err != nil {
			return err
		}

		scriptCandidate, err := loadResourceCandidate(ctx, tx, policy.ResourceRef{
			OwnerVersionID: doc.Executable.OwnerVersionID, ResourceKey: doc.Executable.ResourceKey, ContentHash: doc.Executable.ContentHash,
		})
		if err != nil {
			return fmt.Errorf("runtime: load command executable resource: %w", err)
		}

		// The Attempt's own AttemptPolicy timeout (profile.TimeoutSeconds,
		// the outer envelope's own bound — execute.go's own attemptCtx
		// deadline is already derived from this exact same value) and the
		// CommandDocument's own self-declared TimeoutSeconds are two
		// independent bounds; the tighter one governs ProcessSpec.Timeout
		// (confirmed by reading the code, not asked: the outer ctx deadline
		// would cut a longer CommandDocument timeout short anyway, but a
		// SHORTER author-declared timeout is a real per-command constraint
		// this executor must also honor, not just the broader attempt
		// policy).
		timeout := time.Duration(profile.TimeoutSeconds) * time.Second
		if doc.TimeoutSeconds > 0 {
			if cmdTimeout := time.Duration(doc.TimeoutSeconds) * time.Second; cmdTimeout < timeout {
				timeout = cmdTimeout
			}
		}

		inputs = commandExecutionInputs{
			doc: doc, scriptPayload: scriptCandidate.Payload, effectiveScope: nodeRun.EffectiveScope,
			workspaceMounts: assembleWorkspaceMounts(nodeRun.EffectiveScope, snapshot.Revisions.Entries()),
			timeout:         timeout, allowedOutcomes: allowedOutcomes, snapshotID: string(snapshot.ID),
			executionProfileHash: attempt.ExecutionProfileHash,
		}
		return nil
	})
	return inputs, err
}
