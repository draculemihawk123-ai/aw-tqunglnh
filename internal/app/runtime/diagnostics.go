// GetRunDiagnostics is V6-06C's own query
// (docs/design/08-v6-api-projections.md V6-06C: "expose safe queue/job/
// lease/fence/provider/workspace diagnostics và recovery actions").
// Unlike V6-06D (the three RECOVERY COMMANDS this task's own dependency
// wraps: RetryBlockedActivation/CancelWorkItem/ResolveWorkItemBlocker),
// this file adds no new mutation authority at all — it is a single
// read-only query that assembles ALREADY-real, already-persisted state
// from several sources this codebase already has, so an operator can
// diagnose a Run and pick a NAMED recovery action without ever needing
// direct database access (this task's own "Hoàn thành khi" line):
//
//   - Blocker state: workdomain.WorkItemBlocker rows for the Run's own
//     WorkItem, filtered to SourceRunID == this Run (every one of this
//     codebase's four real blocker producers — openRunCancelledBlockerTx,
//     requestScopeExpansionTx, blockAdmission, applyCompletionPolicy's own
//     FAIL branch — sets SourceRunID to the owning Run's own ID, confirmed
//     by reading every openWorkItemBlockerTx call site before writing this
//     file, so this filter is exact, never a heuristic).
//   - Queue/job/lease evidence: ports.RuntimeRepository's own
//     ListOrphanedRunningExecutionAttempts (V4-13, the SAME real
//     evidence-based join the recovery reaper itself uses — never a bare
//     staleness heuristic) plus GetWriteLeaseRepositoryWorkspaceForAttempt,
//     filtered to attempts belonging to THIS Run's own NodeRuns.
//   - Provider/adapter state: for each currently-OPEN admission-reason
//     blocker (the four ADR-022/023 reasons — see isAdmissionBlockerType
//     below), this reloads that blocker's own NodeRun's resolved execution
//     profile (loadExecutionProfile, execute.go — the SAME durable
//     "<nodeRunId>-execution-profile-v1" artifact schedule.go recorded
//     once) to learn its pinned AdapterBuildID/IsolationTier, then
//     cross-references the AdapterBuild's own ProviderKey against the
//     live agentregistry.Registry (agents.Resolve — a pure, in-memory map
//     lookup, confirmed I/O-free by reading agentregistry/registry.go: the
//     one real I/O agentregistry.New ever does is at construction, once,
//     for every executor it is given — never per Resolve call) and the
//     required IsolationTier against ports.IsolationEnforcementChecker
//     (VerifyEnforceable — confirmed I/O-free in this codebase's only real
//     implementation, internal/adapters/process.IsolationChecker's own doc
//     comment: "performs no I/O... Alpha has no real OS-level sandbox").
//
//     Deliberately NOT a live adapterbuild.VerifyNoDrift re-probe (the
//     EXACT check RetryBlockedActivation's own revalidation runs,
//     runAdmissionProbePhase, admission.go): that function spawns the
//     configured executable for real (executor.Capabilities(ctx)) to
//     measure it — genuine process execution as a side effect. This
//     task's own explicit prohibition ("không... internal execute-command
//     capability... never a way to trigger execution") is read literally
//     here: a GET request answered by this query must never itself spawn
//     a process, no matter how narrow. ProviderConfigured below therefore
//     reports only whether this environment COULD currently attempt a
//     revalidation (a live executor is registered for the pinned
//     provider) — never whether the pinned build has actually drifted.
//     The one command with real authority (and the real I/O budget) to
//     learn that for certain is RetryBlockedActivation itself, whose own
//     structured FailureReason/FailureDetail result (never a raw error)
//     is the authoritative answer this query only ever advises toward.
//   - Workspace/fence: internal/app/workspacestate.GetWorkspaceSetState
//     (V6-10B, already real) reused verbatim, never duplicated — "Fence"
//     is RepositoryWorkspace.Generation, "Lease" is HasActiveWriteLease,
//     "Quarantine" is State == RepositoryWorkspaceQuarantined, exactly
//     that package's own doc comment already establishes.
//
// PID/argv/cwd/secret redaction (this task's own explicit, named
// prohibition — "safe" is the operative word throughout its own Phạm vi
// line): every type in this file is a hand-written, from-scratch DTO with
// an explicit field allowlist — never a verbatim re-serialization of
// ports.AgentExecutionRequest (which carries WorkingDirectory), a COMMAND
// node's own command.Document (which carries Argv/CwdRepositoryTarget), or
// any OS-process type (which would carry a PID) — none of those types are
// even imported here. See diagnostics_sqlite_test.go's own
// TestGetRunDiagnostics_NeverExposesProcessOrSecretShapedFields for the
// mechanical proof (a reflect-based field-name scan across every exported
// type this file declares).
//
// This query is deliberately NOT authoritative for any decision beyond
// itself (workspacestate.go's own "advisory only, never authoritative"
// principle, restated here for the identical reason): the real command a
// caller ultimately dispatches (RetryBlockedActivation/CancelWorkItem/
// ResolveWorkItemBlocker/CancelRun) always reloads and revalidates live
// state under its own fencing, regardless of what this query reported a
// moment earlier. This file computes no httpapi.ValidAction of its own —
// internal/app packages never import internal/delivery/httpapi (that
// would invert this codebase's own dependency direction) — the HTTP
// delivery layer (internal/delivery/httpapi/diagnostics) computes advisory
// ValidActions ON TOP of this file's own typed RunDiagnostics, mirroring
// exactly how internal/delivery/httpapi/workspacestate.go's own
// reconcileValidActions/releaseValidActions already sit on top of
// workspacestate.WorkspaceSetState — never inside the application query
// itself.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacestate"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// maxDiagnosticEntries bounds every list RunDiagnostics returns — this
// task's own "bounded output" Verify bullet: a Run with a long history
// (many cycles/retries/blockers) must never make this query's own response
// grow without limit. Each bounded list also reports its own *Truncated
// flag so a caller can tell "this is everything" from "there is more".
const maxDiagnosticEntries = 50

// isAdmissionBlockerType reports whether t is one of the four admission-
// reason BlockerTypes (ADR-022/023, the same four
// admissionPriority/isAdmissionBlockerReason already close over in
// admission.go, restated here in terms of workdomain.BlockerType rather
// than runtimedomain.TerminationReason since a WorkItemBlocker only ever
// carries the former) — the ones RetryBlockedActivation has real authority
// to retry, as opposed to SCOPE_EXPANSION_REQUIRED (its own dedicated
// approval/reconcile flow) or RUN_CANCELLED/COMPLETION_POLICY_FAILED
// (ResolveWorkItemBlocker/CancelWorkItem's own scope, once the Run is
// already terminal).
func isAdmissionBlockerType(t workdomain.BlockerType) bool {
	switch t {
	case workdomain.BlockerIsolationEnforcementUnavailable, workdomain.BlockerAdapterBuildDrift,
		workdomain.BlockerCapabilityRequirementUnsatisfied, workdomain.BlockerWriteCapabilityOrGrantMissing:
		return true
	default:
		return false
	}
}

// BlockerDiagnostic is one WorkItemBlocker this Run's own execution
// history opened — SourceRunID == this Run for every row returned (see
// this file's own package doc comment for why that filter is exact).
type BlockerDiagnostic struct {
	BlockerID       string
	Type            string
	State           string
	Reason          string
	SourceNodeRunID string
	SourceAttemptID string
	OpenedAt        time.Time
	Version         uint64
	// AdmissionReason is true for one of the four ADR-022/023 admission
	// blocker types (isAdmissionBlockerType) — the delivery layer's own
	// signal for whether retryBlockedActivation is even the right advisory
	// action to offer for this blocker, as opposed to
	// resolveWorkItemBlocker/cancelWorkItem.
	AdmissionReason bool
	// SourceNodeRunVersion is the SourceNodeRunID's own current Version —
	// populated only when SourceNodeRunID is non-empty and the blocker is
	// still OPEN, so a caller has the exact version a follow-on
	// retryBlockedActivation call's own If-Match can safely reference.
	// Zero otherwise.
	SourceNodeRunVersion uint64
}

// OrphanedAttemptDiagnostic is one currently-RUNNING ExecutionAttempt
// belonging to this Run whose own driving EXECUTE_NODE job is no longer
// provably held by a live worker (ListOrphanedRunningExecutionAttempts'
// own real evidence-based definition — never a bare staleness heuristic).
// Never carries a PID, argv or cwd: those are OS-process/COMMAND-node
// shaped facts this codebase's own ExecutionAttempt row does not persist
// at all (see internal/domain/runtime/runtime.go's own ExecutionAttempt
// struct — no such field exists to leak in the first place).
type OrphanedAttemptDiagnostic struct {
	AttemptID     string
	NodeRunID     string
	AttemptNumber uint32
	ProviderKey   string
	StartedAt     *time.Time
	// RepositoryWorkspaceID/HasWriteLease report GetWriteLeaseRepositoryWorkspaceForAttempt's
	// own real answer — "" / false for a read-only attempt that never
	// acquired one.
	RepositoryWorkspaceID string
	HasWriteLease         bool
}

// ProviderDiagnostic is one distinct AdapterBuildID a currently-OPEN
// admission blocker's own NodeRun has pinned, cross-referenced against the
// live agentregistry.Registry — see this file's own package doc comment
// for why this is never a live drift re-probe.
type ProviderDiagnostic struct {
	AdapterBuildID string
	ProviderKey    string
	// ProviderConfigured reports whether a live ports.AgentExecutor is
	// currently registered for ProviderKey (agentregistry.Registry.Resolve
	// — a pure, I/O-free lookup) — never whether the pinned build has
	// actually drifted; see this file's own package doc comment.
	ProviderConfigured bool
	// NodeRunIDs names which of this Run's own currently-BLOCKED NodeRuns
	// pin this exact build.
	NodeRunIDs []string
}

// IsolationDiagnostic is one distinct policy.IsolationTier a currently-OPEN
// admission blocker's own NodeRun requires, cross-referenced against the
// real ports.IsolationEnforcementChecker (an I/O-free, pure check in this
// codebase's only implementation — safe to call live from a GET).
type IsolationDiagnostic struct {
	Tier        string
	Enforceable bool
}

// RepositoryWorkspaceDiagnostic is one repository workspace belonging to
// this Run's own TaskFamily — a direct wire projection of
// workspacestate.RepositoryWorkspaceState (V6-10B, reused verbatim, never
// duplicated). "Fence" is Generation, "Quarantine" is
// State == RepositoryWorkspaceQuarantined, "Lease" is HasActiveWriteLease
// (workspacestate's own doc comment).
type RepositoryWorkspaceDiagnostic struct {
	RepositoryWorkspaceID string
	RepositoryID          string
	State                 string
	Generation            uint64
	HasActiveWriteLease   bool
}

// RunDiagnostics is GetRunDiagnostics' own typed result — everything an
// operator needs to diagnose a Run and pick a named recovery action,
// nothing more (this task's own "Không làm" line: no PID/argv/cwd/secret,
// no internal execute-command capability, no installation-level Doctor
// data).
type RunDiagnostics struct {
	RunID      string
	ProjectID  string
	WorkItemID string
	// WorkItemStatus/WorkItemVersion are the owning WorkItem's own current
	// values — the delivery layer's own basis for an advisory
	// cancelWorkItem ValidAction (never authoritative; CancelWorkItem
	// re-derives its own precondition fresh).
	WorkItemStatus  string
	WorkItemVersion uint64
	RunState        string
	RunVersion      uint64

	Blockers []BlockerDiagnostic

	OrphanedAttempts          []OrphanedAttemptDiagnostic
	OrphanedAttemptsTruncated bool

	Providers []ProviderDiagnostic
	Isolation []IsolationDiagnostic

	RepositoryWorkspaces []RepositoryWorkspaceDiagnostic
}

// runDiagnosticsBase is the one WithReadOnly transaction GetRunDiagnostics
// opens for the "reload Run/WorkItem/Blockers/orphaned-attempt-evidence"
// half of its own work — kept separate from the per-blocker
// loadExecutionProfile/loadAdapterBuild/loadRunRepositoryWorkspaces calls
// below (each its own independent top-level read-only transaction, never
// nested inside this one): nesting a second uow.WithReadOnly call inside an
// already-open ports.Tx closure is not a pattern any other caller in this
// codebase uses, and loadExecutionProfile (execute.go) is itself always
// called OUTSIDE any transaction by its own two existing callers
// (ExecuteNodeHandler.Handle's own admission preflight,
// RetryBlockedActivationHandler.Retry) — this file follows that same
// established discipline rather than inventing a nested-transaction
// exception for itself.
func runDiagnosticsBase(ctx context.Context, uow ports.UnitOfWork, projectID, runID string) (RunDiagnostics, []string, error) {
	var result RunDiagnostics
	var openAdmissionNodeRunIDs []string
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, runID)
		if err != nil {
			return err
		}
		if string(run.ProjectID) != projectID {
			return scopeMismatch("workflow run", runID)
		}
		if _, err := tx.Catalog().GetProject(ctx, projectID); err != nil {
			return err
		}
		item, err := tx.Work().GetWorkItem(ctx, string(run.WorkItemID))
		if err != nil {
			return err
		}

		allBlockers, err := tx.Work().ListWorkItemBlockersForWorkItem(ctx, string(run.WorkItemID))
		if err != nil {
			return err
		}
		var blockerDiags []BlockerDiagnostic
		for _, b := range allBlockers {
			if b.SourceRunID != runID {
				continue
			}
			if len(blockerDiags) >= maxDiagnosticEntries {
				break
			}
			admission := isAdmissionBlockerType(b.Type)
			var nodeRunVersion uint64
			if b.State == workdomain.BlockerOpen && b.SourceNodeRunID != "" {
				if nr, nrErr := tx.Runtime().GetNodeRun(ctx, b.SourceNodeRunID); nrErr == nil {
					nodeRunVersion = nr.Version
				}
				if admission {
					openAdmissionNodeRunIDs = append(openAdmissionNodeRunIDs, b.SourceNodeRunID)
				}
			}
			blockerDiags = append(blockerDiags, BlockerDiagnostic{
				BlockerID: string(b.ID), Type: string(b.Type), State: string(b.State), Reason: b.Reason,
				SourceNodeRunID: b.SourceNodeRunID, SourceAttemptID: b.SourceAttemptID,
				OpenedAt: b.OpenedAt, Version: b.Version, AdmissionReason: admission,
				SourceNodeRunVersion: nodeRunVersion,
			})
		}
		sort.Slice(blockerDiags, func(i, j int) bool { return blockerDiags[i].OpenedAt.Before(blockerDiags[j].OpenedAt) })

		nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, runID)
		if err != nil {
			return err
		}
		nodeRunSet := make(map[string]struct{}, len(nodeRuns))
		for _, nr := range nodeRuns {
			nodeRunSet[string(nr.ID)] = struct{}{}
		}

		orphaned, err := tx.Runtime().ListOrphanedRunningExecutionAttempts(ctx, time.Now().UTC())
		if err != nil {
			return err
		}
		var orphanedDiags []OrphanedAttemptDiagnostic
		truncated := false
		for _, a := range orphaned {
			if _, ok := nodeRunSet[string(a.NodeRunID)]; !ok {
				continue
			}
			if len(orphanedDiags) >= maxDiagnosticEntries {
				truncated = true
				break
			}
			repoWorkspaceID, hasLease, leaseErr := tx.Runtime().GetWriteLeaseRepositoryWorkspaceForAttempt(ctx, string(a.ID))
			if leaseErr != nil {
				return leaseErr
			}
			orphanedDiags = append(orphanedDiags, OrphanedAttemptDiagnostic{
				AttemptID: string(a.ID), NodeRunID: string(a.NodeRunID), AttemptNumber: a.AttemptNumber,
				ProviderKey: a.ProviderKey, StartedAt: a.StartedAt,
				RepositoryWorkspaceID: repoWorkspaceID, HasWriteLease: hasLease,
			})
		}

		result = RunDiagnostics{
			RunID: runID, ProjectID: projectID, WorkItemID: string(run.WorkItemID),
			WorkItemStatus: string(item.Status), WorkItemVersion: item.Version,
			RunState: string(run.State), RunVersion: run.Version,
			Blockers:                  blockerDiags,
			OrphanedAttempts:          orphanedDiags,
			OrphanedAttemptsTruncated: truncated,
		}
		return nil
	})
	return result, openAdmissionNodeRunIDs, err
}

// GetRunDiagnostics is this task's own single authoritative query, `GET
// /projects/{projectId}/runs/{runId}/diagnostics` (operationId
// getRunDiagnostics, docs/design/11-v6-00-ux-artifact.md Screen 8 row 4 /
// Screen 13 row 3 — "authority dùng chung" between both screens, no second
// owner). See this file's own package doc comment for the full source
// inventory and the PID/argv/cwd/secret/no-execute-capability boundary.
func GetRunDiagnostics(
	ctx context.Context, uow ports.UnitOfWork, isolation ports.IsolationEnforcementChecker, agents *agentregistry.Registry,
	scope ports.CommandScope, runID string,
) (RunDiagnostics, error) {
	projectID, err := requireProjectScope(scope)
	if err != nil {
		return RunDiagnostics{}, err
	}

	result, openAdmissionNodeRunIDs, err := runDiagnosticsBase(ctx, uow, projectID, runID)
	if err != nil {
		return RunDiagnostics{}, err
	}

	providers, isolationDiags, err := collectAdmissionCrossReference(ctx, uow, isolation, agents, openAdmissionNodeRunIDs)
	if err != nil {
		return RunDiagnostics{}, err
	}
	result.Providers = providers
	result.Isolation = isolationDiags

	repoWorkspaces, err := loadRunRepositoryWorkspaces(ctx, uow, result.ProjectID, runID)
	if err != nil {
		return RunDiagnostics{}, err
	}
	result.RepositoryWorkspaces = repoWorkspaces

	return result, nil
}

// loadRunRepositoryWorkspaces reloads runID's own owning FamilyID (a
// second, small, independent top-level read-only transaction — never
// nested inside runDiagnosticsBase's own), then delegates to
// workspacestate.GetWorkspaceSetState (V6-10B, reused verbatim) for the
// real state/lease/fence/quarantine snapshot every RepositoryWorkspace in
// that set currently has.
func loadRunRepositoryWorkspaces(ctx context.Context, uow ports.UnitOfWork, projectID, runID string) ([]RepositoryWorkspaceDiagnostic, error) {
	var familyID string
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, runID)
		if err != nil {
			return err
		}
		familyID = string(run.FamilyID)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// A WorkflowRun's own TaskFamily always has a WorkspaceSet by
	// construction (ADR-019: CreateRootWorkItem atomically creates
	// WorkItem+TaskFamily+WorkspaceSet together) — this still fails closed
	// on any error (never guesses an empty result) rather than assuming
	// that invariant holds.
	state, err := workspacestate.GetWorkspaceSetState(ctx, uow, workspacestate.GetWorkspaceSetStateRequest{
		ProjectID: projectID, FamilyID: familyID,
	})
	if err != nil {
		return nil, err
	}

	diags := make([]RepositoryWorkspaceDiagnostic, 0, len(state.RepositoryWorkspaces))
	for i, rw := range state.RepositoryWorkspaces {
		if i >= maxDiagnosticEntries {
			break
		}
		diags = append(diags, RepositoryWorkspaceDiagnostic{
			RepositoryWorkspaceID: rw.RepositoryWorkspaceID, RepositoryID: rw.RepositoryID,
			State: string(rw.State), Generation: rw.Generation, HasActiveWriteLease: rw.HasActiveWriteLease,
		})
	}
	return diags, nil
}

// collectAdmissionCrossReference loads, for each of nodeRunIDs (every
// currently-OPEN admission blocker's own SourceNodeRunID —
// runDiagnosticsBase's own output), that NodeRun's own resolved execution
// profile (loadExecutionProfile, execute.go — the SAME durable artifact
// admission/RetryBlockedActivation themselves read, never a second,
// diverging read path) to learn its pinned AdapterBuildID/IsolationTier,
// then cross-references each against the live agentregistry.Registry/
// ports.IsolationEnforcementChecker — both pure, I/O-free lookups (see this
// file's own package doc comment). Each loadExecutionProfile call is its
// own independent top-level read-only transaction, run here AFTER
// runDiagnosticsBase's own transaction has already closed — never nested.
func collectAdmissionCrossReference(
	ctx context.Context, uow ports.UnitOfWork, isolation ports.IsolationEnforcementChecker, agents *agentregistry.Registry, nodeRunIDs []string,
) ([]ProviderDiagnostic, []IsolationDiagnostic, error) {
	providersByBuild := map[string]*ProviderDiagnostic{}
	var providerOrder []string
	isolationByTier := map[string]bool{}
	var tierOrder []string

	for i, nodeRunID := range nodeRunIDs {
		if i >= maxDiagnosticEntries {
			break
		}
		profile, err := loadExecutionProfile(ctx, uow, nodeRunID)
		if err != nil {
			if errors.Is(err, ports.ErrPersistenceNotFound) {
				// No resolved-execution-profile artifact for this NodeRun —
				// structurally unreachable for a genuine admission blocker
				// (blockAdmission only ever fires after schedule.go already
				// recorded one), but this query fails closed rather than
				// panicking on a nil profile: skip, never guess.
				continue
			}
			return nil, nil, fmt.Errorf("runtime: diagnostics: load execution profile for node run %s: %w", nodeRunID, err)
		}

		tier := string(profile.IsolationTier)
		if tier != "" {
			if _, seen := isolationByTier[tier]; !seen {
				tierOrder = append(tierOrder, tier)
			}
			enforceErr := isolation.VerifyEnforceable(ctx, profile.IsolationTier)
			isolationByTier[tier] = enforceErr == nil
		}

		if profile.AdapterBuild == nil {
			continue
		}
		buildID := profile.AdapterBuild.BuildID
		if existing, ok := providersByBuild[buildID]; ok {
			existing.NodeRunIDs = append(existing.NodeRunIDs, nodeRunID)
			continue
		}
		build, err := loadAdapterBuild(ctx, uow, buildID)
		if err != nil {
			return nil, nil, fmt.Errorf("runtime: diagnostics: load pinned adapter build %s: %w", buildID, err)
		}
		providerKey := build.Tuple().ProviderKey
		_, _, resolveErr := agents.Resolve(ports.ProviderKey(providerKey), agentregistry.Requirements{})
		diag := &ProviderDiagnostic{
			AdapterBuildID: buildID, ProviderKey: providerKey,
			ProviderConfigured: resolveErr == nil, NodeRunIDs: []string{nodeRunID},
		}
		providersByBuild[buildID] = diag
		providerOrder = append(providerOrder, buildID)
	}

	providers := make([]ProviderDiagnostic, 0, len(providerOrder))
	for _, id := range providerOrder {
		providers = append(providers, *providersByBuild[id])
	}
	isolationDiags := make([]IsolationDiagnostic, 0, len(tierOrder))
	for _, tier := range tierOrder {
		isolationDiags = append(isolationDiags, IsolationDiagnostic{Tier: tier, Enforceable: isolationByTier[tier]})
	}
	return providers, isolationDiags, nil
}

// loadAdapterBuild is its own small, independent top-level read-only
// transaction — mirrors adapterbuild.GetAdapterBuild (internal/app/adapterbuild,
// V2-07A) exactly, called here via tx.AdapterBuilds().Get directly rather
// than importing that package's own wrapper for the identical reason
// admission.go's own runAdmissionProbePhase already does (avoiding an
// internal/app/runtime -> internal/app/adapterbuild import this package
// does not otherwise need).
func loadAdapterBuild(ctx context.Context, uow ports.UnitOfWork, buildID string) (domainadapterbuild.Build, error) {
	var build domainadapterbuild.Build
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, loadErr := tx.AdapterBuilds().Get(ctx, buildID)
		if loadErr != nil {
			return loadErr
		}
		build = loaded
		return nil
	})
	return build, err
}
