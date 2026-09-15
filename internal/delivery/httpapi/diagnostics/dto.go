package diagnostics

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// blockerScopeExpansionRequiredType mirrors
// workdomain.BlockerScopeExpansionRequired's own wire value ("SCOPE_EXPANSION_REQUIRED")
// — this package deliberately does not import internal/domain/work solely
// for that one constant (runtime.BlockerDiagnostic.Type is already the
// wire-ready string this package's own DTO re-exposes verbatim), mirroring
// workdomain.BlockerType.ResolvableViaCommand()'s own one documented
// exception (SCOPE_EXPANSION_REQUIRED is the only type that command has NO
// authority over at all, in either resolution mode — see that method's own
// doc comment in internal/domain/work/blocker.go).
const blockerScopeExpansionRequiredType = "SCOPE_EXPANSION_REQUIRED"

const blockerStateOpen = "OPEN"

// blockerResponse is one BlockerDiagnostic's own wire projection, plus its
// own advisory ValidActions — mirrors
// internal/delivery/httpapi/workspacestate.go's own
// repositoryWorkspaceStateResponse convention exactly (ValidActions always
// present, never omitted, even when empty).
type blockerResponse struct {
	BlockerID       string    `json:"blockerId"`
	Type            string    `json:"type"`
	State           string    `json:"state"`
	Reason          string    `json:"reason"`
	SourceNodeRunID string    `json:"sourceNodeRunId,omitempty"`
	SourceAttemptID string    `json:"sourceAttemptId,omitempty"`
	OpenedAt        time.Time `json:"openedAt"`
	Version         uint64    `json:"version"`
	AdmissionReason bool      `json:"admissionReason"`

	ValidActions []httpapi.ValidAction `json:"validActions"`
}

// blockerValidActions computes the advisory recovery action a currently-OPEN
// blocker suggests — never authoritative (ValidAction's own doc comment,
// action.go: the real command always re-derives its own precondition fresh
// under its own fencing regardless of what this query reported a moment
// earlier).
//
//   - An OPEN admission-reason blocker (AdmissionReason=true) advises
//     retryBlockedActivation, targeting the blocked NodeRun's own current
//     Version (SourceNodeRunVersion) — the exact ID/version a follow-on
//     POST /node-runs/{nodeRunId}/retry-blocked-activation call needs.
//   - An OPEN blocker of any other type EXCEPT SCOPE_EXPANSION_REQUIRED
//     advises resolveWorkItemBlocker, targeting the blocker's own current
//     Version — mirrors workdomain.BlockerType.ResolvableViaCommand()'s own
//     one exception exactly (see blockerScopeExpansionRequiredType's own
//     doc comment above).
//   - SCOPE_EXPANSION_REQUIRED and anything not OPEN advise nothing: that
//     blocker type's own dedicated approval/reconcile flow is the only
//     thing with authority over it (ResolveWorkItemBlocker's own package
//     doc comment), and a blocker that already left OPEN has nothing left
//     to advise a NEW decision on.
func blockerValidActions(b runtime.BlockerDiagnostic) []httpapi.ValidAction {
	if b.State != blockerStateOpen {
		return []httpapi.ValidAction{}
	}
	if b.AdmissionReason {
		return []httpapi.ValidAction{{
			OperationID: "retryBlockedActivation", ScopeKind: httpapi.ScopeProject, TargetVersion: int64(b.SourceNodeRunVersion),
		}}
	}
	if b.Type == blockerScopeExpansionRequiredType {
		return []httpapi.ValidAction{}
	}
	return []httpapi.ValidAction{{
		OperationID: "resolveWorkItemBlocker", ScopeKind: httpapi.ScopeProject, TargetVersion: int64(b.Version),
	}}
}

func toBlockerResponse(b runtime.BlockerDiagnostic) blockerResponse {
	return blockerResponse{
		BlockerID: b.BlockerID, Type: b.Type, State: b.State, Reason: b.Reason,
		SourceNodeRunID: b.SourceNodeRunID, SourceAttemptID: b.SourceAttemptID,
		OpenedAt: b.OpenedAt, Version: b.Version, AdmissionReason: b.AdmissionReason,
		ValidActions: blockerValidActions(b),
	}
}

// orphanedAttemptResponse is one OrphanedAttemptDiagnostic's own wire
// projection — deliberately hand-mapped field by field (never a verbatim
// struct embed) so a future field added to
// internal/app/runtime.OrphanedAttemptDiagnostic never silently reaches an
// HTTP response without this package's own author reviewing it against
// this task's own PID/argv/cwd/secret prohibition first.
type orphanedAttemptResponse struct {
	AttemptID             string     `json:"attemptId"`
	NodeRunID             string     `json:"nodeRunId"`
	AttemptNumber         uint32     `json:"attemptNumber"`
	ProviderKey           string     `json:"providerKey,omitempty"`
	StartedAt             *time.Time `json:"startedAt,omitempty"`
	RepositoryWorkspaceID string     `json:"repositoryWorkspaceId,omitempty"`
	HasWriteLease         bool       `json:"hasWriteLease"`
}

func toOrphanedAttemptResponse(a runtime.OrphanedAttemptDiagnostic) orphanedAttemptResponse {
	return orphanedAttemptResponse{
		AttemptID: a.AttemptID, NodeRunID: a.NodeRunID, AttemptNumber: a.AttemptNumber, ProviderKey: a.ProviderKey,
		StartedAt: a.StartedAt, RepositoryWorkspaceID: a.RepositoryWorkspaceID, HasWriteLease: a.HasWriteLease,
	}
}

// providerResponse is one ProviderDiagnostic's own wire projection —
// AdapterBuildID/ProviderKey/ProviderConfigured only, exactly what
// internal/app/runtime.GetRunDiagnostics' own package doc comment already
// establishes is safe (never a live drift re-probe, never an
// ExecutablePath).
type providerResponse struct {
	AdapterBuildID     string   `json:"adapterBuildId"`
	ProviderKey        string   `json:"providerKey"`
	ProviderConfigured bool     `json:"providerConfigured"`
	NodeRunIDs         []string `json:"nodeRunIds,omitempty"`
}

func toProviderResponse(p runtime.ProviderDiagnostic) providerResponse {
	return providerResponse{
		AdapterBuildID: p.AdapterBuildID, ProviderKey: p.ProviderKey,
		ProviderConfigured: p.ProviderConfigured, NodeRunIDs: p.NodeRunIDs,
	}
}

type isolationResponse struct {
	Tier        string `json:"tier"`
	Enforceable bool   `json:"enforceable"`
}

func toIsolationResponse(i runtime.IsolationDiagnostic) isolationResponse {
	return isolationResponse{Tier: i.Tier, Enforceable: i.Enforceable}
}

// repositoryWorkspaceDiagResponse is one RepositoryWorkspaceDiagnostic's own
// wire projection — "Fence" is Generation, "Quarantine" is
// State == "QUARANTINED", "Lease" is HasActiveWriteLease (mirrors
// internal/delivery/httpapi/workspacestate.go's own identical vocabulary,
// V6-10B, reused rather than reinvented).
type repositoryWorkspaceDiagResponse struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	RepositoryID          string `json:"repositoryId"`
	State                 string `json:"state"`
	Generation             uint64 `json:"generation"`
	HasActiveWriteLease    bool   `json:"hasActiveWriteLease"`
}

func toRepositoryWorkspaceDiagResponse(rw runtime.RepositoryWorkspaceDiagnostic) repositoryWorkspaceDiagResponse {
	return repositoryWorkspaceDiagResponse{
		RepositoryWorkspaceID: rw.RepositoryWorkspaceID, RepositoryID: rw.RepositoryID,
		State: rw.State, Generation: rw.Generation, HasActiveWriteLease: rw.HasActiveWriteLease,
	}
}

// runDiagnosticsValidActions computes this response's own top-level
// advisory recovery actions — cancelRun/cancelWorkItem, mirroring
// internal/delivery/httpapi/run's own CancelRunResponse and
// internal/delivery/httpapi/recovery's own CancelWorkItemResponse
// conventions: advisory only, the real command always re-derives its own
// precondition fresh.
//
//   - cancelRun is offered whenever RunState is not already one of the
//     three states past which CancelRun's own idempotent-by-run contract
//     has nothing left to do (CANCELLING — already requested;
//     CANCELLED/SUCCEEDED/FAILED — already terminal).
//   - cancelWorkItem is offered whenever WorkItemStatus is not already
//     DONE or CANCELLED (CancelWorkItem's own ErrWorkItemAlreadyTerminal
//     precondition, cancel_work_item.go).
func runDiagnosticsValidActions(diag runtime.RunDiagnostics) []httpapi.ValidAction {
	var actions []httpapi.ValidAction
	switch diag.RunState {
	case "CANCELLING", "CANCELLED", "SUCCEEDED", "FAILED":
		// nothing to advise
	default:
		actions = append(actions, httpapi.ValidAction{OperationID: "cancelRun", ScopeKind: httpapi.ScopeProject, TargetVersion: int64(diag.RunVersion)})
	}
	switch diag.WorkItemStatus {
	case "DONE", "CANCELLED":
		// nothing to advise
	default:
		actions = append(actions, httpapi.ValidAction{OperationID: "cancelWorkItem", ScopeKind: httpapi.ScopeProject, TargetVersion: int64(diag.WorkItemVersion)})
	}
	if actions == nil {
		actions = []httpapi.ValidAction{}
	}
	return actions
}

// RunDiagnosticsResponse is the wire DTO for GET
// /projects/{projectId}/runs/{runId}/diagnostics — a delivery-owned shape
// distinct from internal/app/runtime.RunDiagnostics (V6-02A's own
// convention: this package owns its own wire vocabulary, never serializes
// an internal/app type verbatim — that type's own Go field names carry no
// json tags at all).
type RunDiagnosticsResponse struct {
	RunID          string `json:"runId"`
	ProjectID      string `json:"projectId"`
	WorkItemID     string `json:"workItemId"`
	WorkItemStatus string `json:"workItemStatus"`
	RunState       string `json:"runState"`

	Blockers []blockerResponse `json:"blockers"`

	OrphanedAttempts          []orphanedAttemptResponse `json:"orphanedAttempts"`
	OrphanedAttemptsTruncated bool                       `json:"orphanedAttemptsTruncated"`

	Providers []providerResponse  `json:"providers"`
	Isolation []isolationResponse `json:"isolation"`

	RepositoryWorkspaces []repositoryWorkspaceDiagResponse `json:"repositoryWorkspaces"`

	ValidActions []httpapi.ValidAction `json:"validActions"`
}

func toRunDiagnosticsResponse(diag runtime.RunDiagnostics) RunDiagnosticsResponse {
	blockers := make([]blockerResponse, 0, len(diag.Blockers))
	for _, b := range diag.Blockers {
		blockers = append(blockers, toBlockerResponse(b))
	}
	orphaned := make([]orphanedAttemptResponse, 0, len(diag.OrphanedAttempts))
	for _, a := range diag.OrphanedAttempts {
		orphaned = append(orphaned, toOrphanedAttemptResponse(a))
	}
	providers := make([]providerResponse, 0, len(diag.Providers))
	for _, p := range diag.Providers {
		providers = append(providers, toProviderResponse(p))
	}
	isolationDiags := make([]isolationResponse, 0, len(diag.Isolation))
	for _, i := range diag.Isolation {
		isolationDiags = append(isolationDiags, toIsolationResponse(i))
	}
	repoWorkspaces := make([]repositoryWorkspaceDiagResponse, 0, len(diag.RepositoryWorkspaces))
	for _, rw := range diag.RepositoryWorkspaces {
		repoWorkspaces = append(repoWorkspaces, toRepositoryWorkspaceDiagResponse(rw))
	}
	return RunDiagnosticsResponse{
		RunID: diag.RunID, ProjectID: diag.ProjectID, WorkItemID: diag.WorkItemID,
		WorkItemStatus: diag.WorkItemStatus, RunState: diag.RunState,
		Blockers: blockers, OrphanedAttempts: orphaned, OrphanedAttemptsTruncated: diag.OrphanedAttemptsTruncated,
		Providers: providers, Isolation: isolationDiags, RepositoryWorkspaces: repoWorkspaces,
		ValidActions: runDiagnosticsValidActions(diag),
	}
}
