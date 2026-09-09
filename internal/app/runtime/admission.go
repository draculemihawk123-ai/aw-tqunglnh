// Package runtime's admission logic is V5-08's own scope
// (docs/design/07-v5-execution-evidence.md): "dựng envelope bất biến và
// chặn mọi thứ không đủ điều kiện trước khi provider được spawn." Four
// checks, each fail-closed, each ending the Attempt at QUEUED->BLOCKED
// (never RUNNING->BLOCKED — go-core-spec.md §4.5's own state-reason
// matrix has no other transition for any of these four reasons):
// isolation enforcement (ADR-023), adapter-build drift (ADR-022,
// GC-INV-23/AK-ARCH-020A), a node's required-vs-granted capabilities
// (HE-02-M02), and multi-repository write capability (GC-INV-24).
//
// Two of the four need real I/O (isolation, adapter-build) that this
// codebase's own established discipline forbids inside a database
// transaction (schedule.go's own two-phase RuntimeExecutionConfigProvider
// design; internal/archtest.TestRegisterAdapterBuildTransactionNeverCallsFilesystemOrProcess
// proves the analogous case for registration). admitOrClaimRunning
// (execute.go) is therefore itself two-phase: a read-only preflight loads
// the pins a probe needs, real I/O runs entirely outside any transaction,
// and a single serialized-write transaction re-verifies every pin fresh
// (protecting against a TOCTOU race between preflight and here, the same
// discipline ADR-022 itself requires of registration) before choosing
// BLOCKED or RUNNING atomically — confirmed with the user before writing
// this file.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// admissionPriority is the fixed, single-source order four admission
// checks are evaluated in (confirmed with the user before writing this
// file): isolation first, since it is the most fundamental trust/safety
// boundary (ADR-023's own "fail-closed before spawn" framing), then
// adapter identity, then the two authored-grant checks. When more than one
// check fails at once, only the highest-priority failure is ever recorded
// as the Attempt's own TerminationReason and blocker — never more than one
// blocker for one admission attempt (secondary failures are not surfaced
// here at all; a caller wanting them for diagnostics would need its own
// instrumentation, out of this function's own scope).
var admissionPriority = []runtimedomain.TerminationReason{
	runtimedomain.TerminationReasonIsolationEnforcementUnavailable,
	runtimedomain.TerminationReasonAdapterBuildDrift,
	runtimedomain.TerminationReasonCapabilityRequirementUnsatisfied,
	runtimedomain.TerminationReasonWriteCapabilityOrGrantMissing,
}

// admissionProbe is what admitOrClaimRunning's own Phase 1 (outside any
// transaction) gathers via real I/O, carried into Phase 2 as plain
// already-computed values — Phase 2 re-verifies the STATIC pins these
// probes were run against are unchanged, but never repeats the I/O itself
// inside the transaction.
type admissionProbe struct {
	isolationSatisfied bool
	isolationErr       error // the real error VerifyEnforceable returned, kept for a blocker's own diagnostic reason

	pinnedBuild    *domainadapterbuild.Build // nil when the node declares no AdapterBuildID
	driftSatisfied bool                      // true when pinnedBuild == nil (nothing to check) or VerifyNoDrift found none
	driftErr       error
}

// runAdmissionProbePhase is admitOrClaimRunning's own Phase 1: a read-only
// preflight to learn what to probe, then real I/O entirely outside any
// transaction. Returns a probe result even on a technical error reading
// pins closed — the caller (admitOrClaimRunning) treats a returned error
// as a genuine Handle() failure (retried like any other job error), never
// as a business BLOCKED outcome.
func (h *ExecuteNodeHandler) runAdmissionProbePhase(ctx context.Context, payload ExecuteNodeJobPayload, profile resolvedExecutionProfileView) (admissionProbe, error) {
	var probe admissionProbe

	if profile.IsolationTier == "" {
		return admissionProbe{}, fmt.Errorf("runtime: admission: node run %s pins no isolation tier", payload.NodeRunID)
	}
	if err := h.isolation.VerifyEnforceable(ctx, profile.IsolationTier); err != nil {
		probe.isolationErr = err
	} else {
		probe.isolationSatisfied = true
	}

	if profile.AdapterBuild == nil {
		// GC-INV-23 ("Attempt pin immutable AdapterBuildVersion; version
		// khác bị từ chối trước dispatch") assumes pinning one is
		// mandatory, not optional. Audit finding (2026-09-08): this used
		// to unconditionally set driftSatisfied=true here (documented as
		// "a legitimate deferred Alpha state"), which let ANY AGENT node
		// with no build declared sail through the drift check — directly
		// contradicting GC-INV-23's own premise and this task's own
		// "Hoàn thành khi" bar ("không request nào tới được
		// ProcessSupervisor nếu thiếu một pin/grant bắt buộc").
		// COMMAND/MACHINE_GATE nodes have no AdapterBuildVersion concept
		// at all (only AgentNodeConfig ever declares one) — nothing to
		// verify for those, so they alone keep the original pass-through.
		if profile.Executor.Kind != string(runtimedomain.ExecutorKindAgent) {
			probe.driftSatisfied = true
			return probe, nil
		}
		probe.driftErr = fmt.Errorf("AGENT node declares no AdapterBuildID — GC-INV-23 requires an Attempt to pin an immutable AdapterBuildVersion")
		return probe, nil
	}
	var pinnedBuild domainadapterbuild.Build
	if err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		pinnedBuild, err = tx.AdapterBuilds().Get(ctx, profile.AdapterBuild.BuildID)
		return err
	}); err != nil {
		return admissionProbe{}, fmt.Errorf("runtime: admission: load pinned adapter build %s: %w", profile.AdapterBuild.BuildID, err)
	}
	probe.pinnedBuild = &pinnedBuild

	executor, _, err := h.agents.Resolve(ports.ProviderKey(pinnedBuild.Tuple().ProviderKey), agentregistry.Requirements{})
	if err != nil {
		return admissionProbe{}, fmt.Errorf("runtime: admission: resolve agent executor for provider %s: %w", pinnedBuild.Tuple().ProviderKey, err)
	}
	if err := adapterbuild.VerifyNoDrift(ctx, executor, pinnedBuild); err != nil {
		probe.driftErr = err
	} else {
		probe.driftSatisfied = true
	}
	return probe, nil
}

// admissionDecision is the single, priority-resolved outcome of every
// check Phase 2 evaluates (the two carried over from Phase 1, plus the
// two pure-data checks run fresh inside the transaction). reason is empty
// when admission passes.
type admissionDecision struct {
	reason      runtimedomain.TerminationReason
	blockerType workdomain.BlockerType
	detail      string
}

// evaluateAdmission resolves probe (Phase 1's own results) plus the two
// pure-data checks (capability requirement, multi-repository write grant
// — both fully answerable from data this same transaction already has, no
// I/O needed) into a single admissionDecision, honoring admissionPriority.
// A non-nil error is a genuine technical failure (propagate, do not
// block); an empty-reason admissionDecision means admission passes.
func evaluateAdmission(
	ctx context.Context, tx ports.Tx, profile resolvedExecutionProfileView, nodeRun runtimedomain.NodeRun, probe admissionProbe,
) (admissionDecision, error) {
	capabilitySatisfied, err := checkCapabilityRequirement(ctx, tx, profile)
	if err != nil {
		return admissionDecision{}, err
	}
	multiRepoSatisfied := checkMultiRepositoryWriteGrant(nodeRun, profile)

	satisfied := map[runtimedomain.TerminationReason]bool{
		runtimedomain.TerminationReasonIsolationEnforcementUnavailable:  probe.isolationSatisfied,
		runtimedomain.TerminationReasonAdapterBuildDrift:                probe.driftSatisfied,
		runtimedomain.TerminationReasonCapabilityRequirementUnsatisfied: capabilitySatisfied,
		runtimedomain.TerminationReasonWriteCapabilityOrGrantMissing:    multiRepoSatisfied,
	}
	detail := map[runtimedomain.TerminationReason]string{
		runtimedomain.TerminationReasonIsolationEnforcementUnavailable:  fmt.Sprintf("isolation tier %s is not enforceable: %v", profile.IsolationTier, probe.isolationErr),
		runtimedomain.TerminationReasonAdapterBuildDrift:                fmt.Sprintf("adapter build drift: %v", probe.driftErr),
		runtimedomain.TerminationReasonCapabilityRequirementUnsatisfied: "a required capability is not granted",
		runtimedomain.TerminationReasonWriteCapabilityOrGrantMissing:    "more than one repository is write-scoped without INTEGRATION_MULTI_REPOSITORY_WRITE granted",
	}
	blockerTypes := map[runtimedomain.TerminationReason]workdomain.BlockerType{
		runtimedomain.TerminationReasonIsolationEnforcementUnavailable:  workdomain.BlockerIsolationEnforcementUnavailable,
		runtimedomain.TerminationReasonAdapterBuildDrift:                workdomain.BlockerAdapterBuildDrift,
		runtimedomain.TerminationReasonCapabilityRequirementUnsatisfied: workdomain.BlockerCapabilityRequirementUnsatisfied,
		runtimedomain.TerminationReasonWriteCapabilityOrGrantMissing:    workdomain.BlockerWriteCapabilityOrGrantMissing,
	}

	for _, reason := range admissionPriority {
		if !satisfied[reason] {
			return admissionDecision{reason: reason, blockerType: blockerTypes[reason], detail: detail[reason]}, nil
		}
	}
	return admissionDecision{}, nil
}

// checkCapabilityRequirement is HE-02-M02's own "scheduler MUST kiểm tra
// runner/provider có capability cần thiết trước khi claim job" made real.
// Confirmed with the user before writing this file: AgentProfileDocument's
// own RequiredCapabilities is already pinned indirectly-but-completely by
// ResolvedExecutionProfileV1.Executor.CompiledHash (the published
// AgentProfileVersion is immutable), so this check re-loads the EXACT
// pinned version fresh, verifies it still matches that pin, and decodes
// RequiredCapabilities from it — never denormalizing the requirement onto
// ResolvedExecutionProfileV1 itself (that locked, hash-bearing V1 type
// stays exactly as V4-04 defined it; a future ResolvedExecutionProfileV2
// could denormalize this for performance, not V5-08). Only meaningful for
// an AGENT executor — COMMAND/MACHINE_GATE profiles carry no
// RequiredCapabilities concept and always satisfy this check trivially.
func checkCapabilityRequirement(ctx context.Context, tx ports.Tx, profile resolvedExecutionProfileView) (bool, error) {
	if profile.Executor.Kind != string(runtimedomain.ExecutorKindAgent) {
		return true, nil
	}
	version, err := tx.Definitions().LoadVersion(ctx, profile.Executor.VersionID)
	if err != nil {
		return false, fmt.Errorf("runtime: admission: load agent profile version %s: %w", profile.Executor.VersionID, err)
	}
	if version.DefinitionID() != profile.Executor.DefinitionID || version.CompiledHash() != profile.Executor.CompiledHash {
		return false, fmt.Errorf("runtime: admission: agent profile version %s no longer matches its own pin (definitionId/compiledHash changed)", profile.Executor.VersionID)
	}
	agentDoc, err := decodeCompiledAgentProfile(version.CompiledSnapshot())
	if err != nil {
		return false, fmt.Errorf("runtime: admission: decode agent profile version %s: %w", profile.Executor.VersionID, err)
	}
	allowed := make(map[string]struct{}, len(profile.AllowedCapabilities))
	for _, capability := range profile.AllowedCapabilities {
		allowed[capability] = struct{}{}
	}
	for _, required := range agentDoc.RequiredCapabilities {
		if _, ok := allowed[required]; !ok {
			return false, nil
		}
	}
	return true, nil
}

// checkMultiRepositoryWriteGrant is GC-INV-24 made real: "Mutating attempt
// có đúng một repository READ_WRITE, trừ khi capability
// INTEGRATION_MULTI_REPOSITORY_WRITE được pin và cấp." nodeRun's own
// EffectiveScope (already resolved and pinned at schedule-time,
// schedule.go's own tx.Work().ListWorkItemEffectiveScopes call) is the
// exact signal: count distinct repositories scoped WRITE, and require the
// capability whenever more than one exists. This is the same computation
// buildExecutionEnvelope uses to decide each mount's own Access — a node
// that fails this check would otherwise get an envelope with more than
// one READ_WRITE mount, exactly the shape this invariant forbids.
func checkMultiRepositoryWriteGrant(nodeRun runtimedomain.NodeRun, profile resolvedExecutionProfileView) bool {
	writeRepositories := distinctWriteRepositories(nodeRun)
	if len(writeRepositories) <= 1 {
		return true
	}
	for _, capability := range profile.AllowedCapabilities {
		if capability == integrationMultiRepositoryWriteCapability {
			return true
		}
	}
	return false
}

// integrationMultiRepositoryWriteCapability names GC-INV-24's own escape
// hatch — the identical literal internal/app/workflowcompiler's own
// compile-time checkScopeAndCapability already uses for the equivalent
// declared-graph-shape check; V5-08's own check is the runtime-scope
// counterpart (this WorkItem's actually-granted EffectiveScope, not a
// compiled COMMAND node's own declared CwdRepositoryTarget).
const integrationMultiRepositoryWriteCapability = "INTEGRATION_MULTI_REPOSITORY_WRITE"

func distinctWriteRepositories(nodeRun runtimedomain.NodeRun) []string {
	seen := make(map[string]struct{}, len(nodeRun.EffectiveScope))
	var repositories []string
	for _, scope := range nodeRun.EffectiveScope {
		if scope.Access() != workdomain.RepositoryWrite {
			continue
		}
		id := string(scope.RepositoryID())
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		repositories = append(repositories, id)
	}
	return repositories
}

// buildExecutionEnvelope is V5-08's own "dựng envelope bất biến" —
// ports.AgentWorkspaceMount's first producer anywhere in this codebase.
// Exactly one mount is READ_WRITE by default; every EffectiveScope entry
// scoped WRITE gets one only when checkMultiRepositoryWriteGrant already
// confirmed that is safe (more than one WRITE-scoped repository without
// the multi-repo capability never reaches here — that path is BLOCKED
// before an envelope is ever built). Handle/WorkingDirectory are left at
// their zero value: resolving a real ports.WorkspaceHandle for a
// repository is V3's own workspace-lifecycle machinery, and wiring THAT in
// is real execution-bridge work the user confirmed stays out of V5-08's
// own scope (execution keeps going through the existing, unmodified
// ports.NodeExecutor) — a future caller that actually spawns a provider is
// the one with a reason to resolve it.
func buildExecutionEnvelope(nodeRun runtimedomain.NodeRun) []ports.AgentWorkspaceMount {
	mounts := make([]ports.AgentWorkspaceMount, 0, len(nodeRun.EffectiveScope))
	seen := make(map[string]struct{}, len(nodeRun.EffectiveScope))
	for _, scope := range nodeRun.EffectiveScope {
		id := string(scope.RepositoryID())
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		access := ports.WorkspaceReadOnly
		if scope.Access() == workdomain.RepositoryWrite {
			access = ports.WorkspaceReadWrite
		}
		mounts = append(mounts, ports.AgentWorkspaceMount{
			RepositoryID: project.RepositoryID(id),
			Access:       access,
		})
	}
	return mounts
}

// persistedEnvelopeMount is what recordExecutionEnvelope actually
// persists — RepositoryID and Access only, never the full
// ports.AgentWorkspaceMount: that type's own Handle field refuses to
// serialize while empty (WorkspaceHandle.MarshalText's own safety guard,
// working exactly as intended here), and resolving a real
// ports.WorkspaceHandle per repository is V3's own workspace-lifecycle
// machinery — real execution-bridge work the user confirmed stays out of
// V5-08's own scope. A future caller that actually spawns a provider is
// the one with a reason to resolve real handles and build the full
// ports.AgentWorkspaceMount this envelope's own RepositoryID/Access
// already determine.
type persistedEnvelopeMount struct {
	RepositoryID string                `json:"repositoryId"`
	Access       ports.WorkspaceAccess `json:"access"`
}

// recordExecutionEnvelope persists buildExecutionEnvelope's own result as a
// durable DecisionArtifact keyed by NodeRunID — schedule.go's own
// "<nodeRunId>-execution-profile-v1" pattern extended with a sibling
// "-execution-envelope-v1" artifact, so a future reader (the real
// NodeExecutor bridge, not V5-08's own scope) can look this up the exact
// same way.
//
// RecordDecisionArtifact itself is a plain append-only insert (its own
// doc comment: "there is no corresponding update/delete method, now or
// ever"), never idempotent by ID on its own — unlike the execution-profile
// artifact (recorded exactly once, inside ScheduleExecutableNodeRun's own
// PENDING->QUEUED transaction, which a NodeRun only ever passes through
// once), this envelope is recorded from admitOrClaimRunning, which runs
// once per ExecutionAttempt — and a NodeRun can have more than one Attempt
// (a technical retry). So this function checks for the artifact first: a
// NodeRun's own EffectiveScope never changes between its Attempts, so an
// already-recorded envelope is always still correct, and a second retry's
// own admission pass must be a genuine no-op here, not a duplicate-insert
// error.
func recordExecutionEnvelope(ctx context.Context, tx ports.Tx, nodeRunID string, projectID project.ProjectID, nodeRun runtimedomain.NodeRun) error {
	id := nodeRunID + "-execution-envelope-v1"
	if _, err := tx.Runtime().GetDecisionArtifact(ctx, id); err == nil {
		return nil
	} else if !errors.Is(err, ports.ErrPersistenceNotFound) {
		return fmt.Errorf("runtime: admission: check existing execution envelope for node run %s: %w", nodeRunID, err)
	}

	mounts := buildExecutionEnvelope(nodeRun)
	persisted := make([]persistedEnvelopeMount, len(mounts))
	for i, mount := range mounts {
		persisted[i] = persistedEnvelopeMount{RepositoryID: string(mount.RepositoryID), Access: mount.Access}
	}
	envelopeJSON, err := json.Marshal(persisted)
	if err != nil {
		return fmt.Errorf("runtime: admission: marshal execution envelope for node run %s: %w", nodeRunID, err)
	}
	decision, err := runtimedomain.NewDecisionArtifact(
		runtimedomain.DecisionArtifactID(id), projectID, "EXECUTION_ENVELOPE_V1", "v1",
		json.RawMessage(`{}`), envelopeJSON, time.Now().UTC(),
	)
	if err != nil {
		return err
	}
	_, err = tx.Runtime().RecordDecisionArtifact(ctx, decision)
	return err
}
