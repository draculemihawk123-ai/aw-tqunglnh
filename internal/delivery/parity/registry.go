package parity

import "github.com/taQuangLing/agent-workflow/internal/delivery/cli"

// OpKind classifies a public operation as a command (mutation, or a POST
// that validates without persisting) or a query (read).
type OpKind string

const (
	KindCommand OpKind = "COMMAND"
	KindQuery   OpKind = "QUERY"
)

// Exposure classifies whether delivery may expose an operation at all.
type Exposure string

const (
	// ExposurePublic: an application command/query HTTP and the CLI both
	// expose through the same authority.
	ExposurePublic Exposure = "PUBLIC"
	// ExposureInternal: a scheduler/worker-only command ADR-028 forbids any
	// delivery adapter to expose ("AdvanceRun, ExecuteWorkspaceReconciliation
	// hay ExecuteWorkspaceSetRelease" and their siblings).
	ExposureInternal Exposure = "INTERNAL"
	// ExposureLocal: a process-level CLI_LOCAL operation with no HTTP or
	// application counterpart by design (`aw evidence verify`).
	ExposureLocal Exposure = "LOCAL"
	// ExposureHealth: a process health probe — UX §15: "health check nội bộ
	// (không phải application command/query per se)".
	ExposureHealth Exposure = "HEALTH"
	// ExposureStream: the project event stream, delivered as SSE over HTTP
	// and NDJSON over the CLI on top of ports.EventsRepository.ScanJournal;
	// no single application function exists to name.
	ExposureStream Exposure = "STREAM"
	// ExposureProjectionRead: a query the HTTP layer answers by reading the
	// projection port directly (internal/delivery/httpapi/kanban) — there is
	// no application function behind it. Registered honestly as such; the
	// checker reports it (MISSING_APP) because ADR-028 wants every public
	// query on a public APPLICATION operation.
	ExposureProjectionRead Exposure = "PROJECTION_READ"
)

// HTTPBinding is one HTTP operationId an operation is exposed as, with the
// scope that operationId carries (ADR-025's closed installation list versus
// project scope). An operation with a global and a project variant
// (definitions, ADR-028's own "--scope global vs --project-id" example) has
// two bindings.
type HTTPBinding struct {
	OperationID string
	Scope       cli.ScopeKind
}

// PublicOperation is one entry of the public operation registry.
type PublicOperation struct {
	// Name is the operation's identifier as CLI descriptors name it
	// (cli.Descriptor.AppOperation); for a command it is byte-identical to
	// the receipt commandType the HTTP handlers and the CLI both use.
	Name     string
	Kind     OpKind
	Exposure Exposure
	// HTTP lists every operationId that exposes it, each with its scope.
	HTTP []HTTPBinding
	// Symbol is the real exported Go identifier that implements it, written
	// "internal/app/<pkg>.<Func>" or "internal/app/<pkg>.<Type>.<Method>".
	// TestRegistrySymbolsExist proves every non-empty Symbol against the
	// parsed source tree, so the registry cannot invent an operation. Empty
	// only for ExposureLocal/Health/Stream/ProjectionRead entries, which name
	// no application function.
	Symbol string
	// HighImpact records that the UX inventory puts a confirmation dialog in
	// front of the operation (destructive or privileged). Cite is the phrase
	// in docs/design/11-v6-00-ux-artifact.md that says so, verified against
	// the real document by TestHighImpactCitationsExistInTheUXDoc — HE-04-M07:
	// the marker has one authoritative source, the UX document, and this
	// registry only references it.
	HighImpact bool
	Cite       string
}

func project(id string) HTTPBinding      { return HTTPBinding{id, cli.ScopeProject} }
func installation(id string) HTTPBinding { return HTTPBinding{id, cli.ScopeInstallation} }

// PublicOperations returns the public operation registry.
//
// # What "the public operation registry" is
//
// The design names four parties for V6-15O but the fourth had no artifact:
// the application layer exposes commands and queries as ordinary exported Go
// functions under internal/app/*, and the only machine-readable statements
// about them were the AppOperation strings CLI descriptors carry and the
// commandType strings HTTP handlers pass to the receipt store. The registry
// is the independent statement of that layer: for every operation a delivery
// adapter may expose it declares the exact name, kind, scope shape, the HTTP
// operationIds it is served as, the real implementing symbol and whether the
// UX inventory gates it behind a confirmation — plus the internal
// (worker-only) operations no adapter may expose. Because it is declared
// independently, the checker can catch a CLI descriptor and an HTTP route
// that each look self-consistent yet disagree with the application layer
// (APP_MISMATCH), an operation that reaches a delivery adapter without a
// registry entry (MISSING_APP), and an operation whose Symbol no longer
// exists (TestRegistrySymbolsExist).
//
// It is a checker-side construct: nothing in production reads it, so it
// changes no runtime behavior. Keeping it a plain Go table (not generated
// from the descriptors) is deliberate — a generated registry would agree
// with the descriptors by construction and prove nothing.
func PublicOperations() []PublicOperation {
	const (
		catalog     = "internal/app/catalog"
		definitions = "internal/app/definitions"
		adapterb    = "internal/app/adapterbuild"
		work        = "internal/app/work"
		runtimeApp  = "internal/app/runtime"
		message     = "internal/app/message"
		safe        = "internal/app/safesettings"
		proj        = "internal/app/projectionrebuild"
		wsState     = "internal/app/workspacestate"
		wsRelease   = "internal/app/workspacerelease"
		wsReconcile = "internal/app/workspacereconcile"
		wsInspect   = "internal/app/workspaceinspection"
		rsCommit    = "internal/app/releasesetcommit"
	)
	return []PublicOperation{
		// ---- installation health and configuration -----------------------
		{Name: "HealthLive", Kind: KindQuery, Exposure: ExposureHealth, HTTP: []HTTPBinding{installation("healthLive")}},
		{Name: "HealthReady", Kind: KindQuery, Exposure: ExposureHealth, HTTP: []HTTPBinding{installation("healthReady")}},
		{Name: "Doctor", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("doctor")}, Symbol: "internal/app/doctor.Run"},
		{Name: "GetSafeSettings", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("getSafeSettings")}, Symbol: safe + ".GetSafeSettings"},
		{Name: "UpdateSafeSettings", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("updateSafeSettings")}, Symbol: safe + ".UpdateSafeSettings"},

		// ---- catalog ------------------------------------------------------
		{Name: "CreateProject", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("projectsCreate")}, Symbol: catalog + ".CreateProject"},
		{Name: "ListProjects", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("projectsList")}, Symbol: catalog + ".ListProjects"},
		{Name: "GetProject", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("projectsGet")}, Symbol: catalog + ".GetProject"},
		{Name: "RegisterRepository", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("projectRepositoriesRegister")}, Symbol: catalog + ".RegisterRepository"},
		{Name: "ListProjectRepositories", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("projectRepositoriesList")}, Symbol: catalog + ".ListProjectRepositories"},
		{Name: "GetRepository", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("repositoriesGet")}, Symbol: catalog + ".GetRepository"},
		{Name: "RepositoryOnboarding", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("repositoriesOnboarding")}, Symbol: catalog + ".ListRepositoryProbeAttempts"},
		{Name: "RetryRepositoryProbe", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("repositoriesRetryProbe")}, Symbol: catalog + ".RetryRepositoryProbe"},
		{Name: "ListComponents", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("projectComponentsList")}, Symbol: catalog + ".ListComponents"},
		{Name: "AssignComponentPack", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("componentPackAssignmentsAssign")}, Symbol: catalog + ".AssignComponentPack"},
		{Name: "ListComponentPackAssignments", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("componentPackAssignmentsList")}, Symbol: catalog + ".ListComponentPackAssignments"},

		// ---- definitions (global + project variants) ---------------------
		{Name: "CreateDefinition", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("createDefinition"), project("createProjectDefinition")}, Symbol: definitions + ".CreateDefinition"},
		{Name: "ListDefinitions", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("listDefinitions"), project("listProjectDefinitions")}, Symbol: definitions + ".ListDefinitions"},
		{Name: "GetDefinition", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("getDefinition"), project("getProjectDefinition")}, Symbol: definitions + ".GetDefinition"},
		{Name: "ListVersions", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("listDefinitionVersions"), project("listProjectDefinitionVersions")}, Symbol: definitions + ".ListVersions"},
		{Name: "ValidateDraft", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("validateDefinitionDraft"), project("validateProjectDefinitionDraft")}, Symbol: definitions + ".ValidateDraft"},
		{Name: "PublishDefinitionVersion", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("publishDefinitionVersion"), project("publishProjectDefinitionVersion")}, Symbol: definitions + ".PublishDefinitionVersion",
			HighImpact: true, Cite: "dialog confirm publish"},
		{Name: "LoadAnyVersion", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("getDefinitionVersion"), project("getProjectDefinitionVersion")}, Symbol: definitions + ".LoadAnyVersion"},
		{Name: "DiffVersions", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("diffDefinitionVersions"), project("diffProjectDefinitionVersions")}, Symbol: definitions + ".DiffVersions"},

		// ---- adapter builds (installation-global, ADR-022/ADR-025) -------
		{Name: "ListAdapterBuilds", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("listAdapterBuilds")}, Symbol: adapterb + ".ListAdapterBuilds"},
		{Name: "GetAdapterBuild", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("getAdapterBuild")}, Symbol: adapterb + ".GetAdapterBuild"},
		{Name: "ProbeAdapterBuild", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("probeAdapterBuild")}, Symbol: adapterb + ".ProbeAdapterBuild"},
		{Name: "RegisterAdapterBuild", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{installation("registerAdapterBuild")}, Symbol: adapterb + ".RegisterAdapterBuild",
			HighImpact: true, Cite: "Đăng ký (confirm) adapter build sau probe"},

		// ---- work items, families, scope expansion -----------------------
		{Name: "CreateRootWorkItem", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("createRootWorkItem")}, Symbol: work + ".CreateRootWorkItem"},
		{Name: "CreateChildWorkItem", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("createChildWorkItem")}, Symbol: work + ".CreateChildWorkItem"},
		{Name: "ListWorkItems", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("listWorkItems")}, Symbol: work + ".ListWorkItems"},
		{Name: "GetWorkItem", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getWorkItem")}, Symbol: work + ".GetWorkItem"},
		{Name: "ListChildWorkItems", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("listChildWorkItems")}, Symbol: work + ".ListChildWorkItems"},
		{Name: "GetTaskFamily", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getTaskFamily")}, Symbol: work + ".GetTaskFamily"},
		{Name: "ExplainWorkItemReadiness", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getWorkItemReadiness")}, Symbol: work + ".ExplainWorkItemReadiness"},
		{Name: "MarkWorkItemReady", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("markWorkItemReady")}, Symbol: work + ".MarkWorkItemReady"},
		{Name: "CancelWorkItem", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("cancelWorkItem")}, Symbol: runtimeApp + ".CancelWorkItem",
			HighImpact: true, Cite: "cancel run/cancel task"},
		{Name: "ResolveWorkItemBlocker", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("resolveWorkItemBlocker")}, Symbol: runtimeApp + ".ResolveWorkItemBlocker"},
		{Name: "RequestScopeExpansion", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("requestScopeExpansion")}, Symbol: work + ".RequestScopeExpansion"},
		{Name: "GetScopeExpansionRequest", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getScopeExpansionRequest")}, Symbol: work + ".GetScopeExpansionRequest"},
		{Name: "ApproveScopeExpansion", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("approveScopeExpansion")}, Symbol: work + ".ApproveScopeExpansion"},
		{Name: "RejectScopeExpansion", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("rejectScopeExpansion")}, Symbol: work + ".RejectScopeExpansion"},
		{Name: "WithdrawScopeExpansion", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("withdrawScopeExpansion")}, Symbol: work + ".WithdrawScopeExpansion"},

		// ---- projected read model (delivery reads the projection port) ---
		{Name: "ListWorkItemKanban", Kind: KindQuery, Exposure: ExposureProjectionRead, HTTP: []HTTPBinding{project("listWorkItemKanban")}},
		{Name: "GetWorkItemProjectedDetail", Kind: KindQuery, Exposure: ExposureProjectionRead, HTTP: []HTTPBinding{project("getWorkItemProjectedDetail")}},

		// ---- runs, human decisions, recovery -----------------------------
		{Name: "StartWorkflowRun", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("startWorkflowRun")}, Symbol: runtimeApp + ".StartWorkflowRun"},
		{Name: "CancelRun", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("cancelRun")}, Symbol: runtimeApp + ".CancelRun",
			HighImpact: true, Cite: "cancel run/cancel task"},
		{Name: "GetRunDetail", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getRunDetail")}, Symbol: runtimeApp + ".GetRunDetail"},
		{Name: "GetRunGraph", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getRunGraph")}, Symbol: runtimeApp + ".GetRunGraph"},
		{Name: "GetRunTimeline", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getRunTimeline")}, Symbol: runtimeApp + ".GetRunTimeline"},
		{Name: "GetRunDiagnostics", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getRunDiagnostics")}, Symbol: runtimeApp + ".GetRunDiagnostics"},
		{Name: "RetryBlockedActivation", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("retryBlockedActivation")}, Symbol: runtimeApp + ".RetryBlockedActivationHandler.Retry"},
		{Name: "ResolveApproval", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("resolveApproval")}, Symbol: runtimeApp + ".ResolveApproval"},
		{Name: "SignalWait", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("submitWaitSignal")}, Symbol: runtimeApp + ".SignalWait"},

		// ---- conversation, evidence, artifacts ---------------------------
		{Name: "ListMessages", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("listMessages")}, Symbol: message + ".ListMessages"},
		{Name: "AppendMessage", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("appendMessage")}, Symbol: message + ".AppendMessage"},
		{Name: "AppendConversationAttachment", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("appendConversationAttachment")}, Symbol: message + ".AppendConversationAttachment"},
		{Name: "ListEvidenceForWorkItem", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("listEvidence")}, Symbol: runtimeApp + ".ListEvidenceForWorkItem"},
		{Name: "GetEvidence", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getEvidence")}, Symbol: runtimeApp + ".GetEvidence"},
		{Name: "ListArtifactsForEvidence", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("listArtifacts")}, Symbol: runtimeApp + ".ListArtifactsForEvidence"},
		{Name: "ResolveEvidenceArtifactContent", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getArtifactContent")}, Symbol: runtimeApp + ".ResolveEvidenceArtifactContent"},
		{Name: "GetContextSnapshot", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getContextSnapshot"), project("getMessageContextSnapshot")}, Symbol: runtimeApp + ".GetContextSnapshot"},
		{Name: "VerifyEvidence", Kind: KindQuery, Exposure: ExposureLocal},

		// ---- workspaces and bounded source -------------------------------
		{Name: "GetWorkspaceSetState", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getWorkspaceSetState")}, Symbol: wsState + ".GetWorkspaceSetState"},
		{Name: "GetRepositoryWorkspaceState", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getRepositoryWorkspaceState")}, Symbol: wsState + ".GetRepositoryWorkspaceState"},
		{Name: "RequestWorkspaceSetRelease", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("requestWorkspaceSetRelease")}, Symbol: wsRelease + ".RequestWorkspaceSetRelease",
			HighImpact: true, Cite: "confirm phải nêu rõ đây là intent"},
		{Name: "RequestWorkspaceReconciliation", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("requestWorkspaceReconciliation")}, Symbol: wsReconcile + ".RequestWorkspaceReconciliation"},
		{Name: "GetSource", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getWorkspaceSource")}, Symbol: wsInspect + ".Queries.GetSource"},
		{Name: "GetDiff", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getWorkspaceDiff")}, Symbol: wsInspect + ".Queries.GetDiff"},
		{Name: "GetRepositoryLog", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getWorkspaceRepositoryLog")}, Symbol: wsInspect + ".Queries.GetRepositoryLog"},

		// ---- release sets and local commit -------------------------------
		{Name: "CreateReleaseSet", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("createReleaseSet")}, Symbol: work + ".CreateReleaseSet"},
		{Name: "ListReleaseSetsForFamily", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("listReleaseSetsForFamily")}, Symbol: work + ".ListReleaseSetsForFamily"},
		{Name: "GetReleaseSet", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getReleaseSet")}, Symbol: work + ".GetReleaseSet"},
		{Name: "SealReleaseSet", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("sealReleaseSet")}, Symbol: work + ".SealReleaseSet",
			HighImpact: true, Cite: "Dialog confirm seal/abandon/local-commit"},
		{Name: "AbandonReleaseSet", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("abandonReleaseSet")}, Symbol: work + ".AbandonReleaseSet",
			HighImpact: true, Cite: "Dialog confirm seal/abandon/local-commit"},
		{Name: "RequestReleaseSetLocalCommit", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("requestReleaseSetLocalCommit")}, Symbol: rsCommit + ".RequestReleaseSetLocalCommit",
			HighImpact: true, Cite: "Dialog confirm seal/abandon/local-commit"},
		{Name: "GetReleaseSetLocalCommitStatus", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getReleaseSetLocalCommitStatus")}, Symbol: work + ".GetReleaseSetLocalCommitStatus"},

		// ---- projection and events ---------------------------------------
		{Name: "GetProjectionStatus", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getProjectionStatus")}, Symbol: proj + ".GetProjectionStatus"},
		{Name: "RequestProjectionRebuild", Kind: KindCommand, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("requestProjectionRebuild")}, Symbol: proj + ".RequestProjectionRebuild"},
		{Name: "GetProjectionRebuildStatus", Kind: KindQuery, Exposure: ExposurePublic, HTTP: []HTTPBinding{project("getProjectionRebuildOperationStatus")}, Symbol: proj + ".GetProjectionRebuildStatus"},
		{Name: "WatchProjectEvents", Kind: KindQuery, Exposure: ExposureStream, HTTP: []HTTPBinding{project("watchProjectEvents")}},

		// ---- internal (scheduler/worker-only): no adapter may expose ------
		{Name: "AdvanceRun", Kind: KindCommand, Exposure: ExposureInternal, Symbol: runtimeApp + ".AdvanceRun"},
		{Name: "ExecuteWorkspaceReconciliation", Kind: KindCommand, Exposure: ExposureInternal, Symbol: wsReconcile + ".ExecuteWorkspaceReconciliation"},
		{Name: "ExecuteWorkspaceSetRelease", Kind: KindCommand, Exposure: ExposureInternal, Symbol: wsRelease + ".ExecuteWorkspaceSetRelease"},
		{Name: "ExecuteReleaseSetLocalCommit", Kind: KindCommand, Exposure: ExposureInternal, Symbol: rsCommit + ".ExecuteReleaseSetLocalCommit"},
		{Name: "ExecuteProjectionRebuild", Kind: KindCommand, Exposure: ExposureInternal, Symbol: "internal/app/projectionrebuildworker.ExecuteProjectionRebuild"},
		{Name: "ExecuteArtifactSweep", Kind: KindCommand, Exposure: ExposureInternal, Symbol: "internal/app/artifactsweep.ExecuteArtifactSweep"},
		{Name: "ReconcileInterruptedAttempt", Kind: KindCommand, Exposure: ExposureInternal, Symbol: "internal/app/worker.ReconcileInterruptedAttempt"},
		{Name: "ReconcileMutatingAttempt", Kind: KindCommand, Exposure: ExposureInternal, Symbol: "internal/app/worker.ReconcileMutatingAttempt"},
	}
}
