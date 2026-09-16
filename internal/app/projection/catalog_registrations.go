package projection

// registerClassifications wires every (EventType, SchemaVersion) key this
// codebase's real eventschema registries register (11 RegisterEventSchemas
// call sites — see internal/archtest/event_catalog_test.go's own combined
// list, agentevents' own separate agent_events journal deliberately
// excluded there and here for the identical reason its own doc comment
// gives) into this Catalog — V6-08's own "exhaustive reducer
// classification," verified exhaustive by
// TestCatalog_ClassifiesEveryRegisteredEventKey (catalog_test.go), which
// fails closed on any key registered on master but missing here. 47 keys
// as of this task: 16 Apply, 31 Ignore.
func registerClassifications(c *Catalog) {
	// --- Apply: WorkItem lifecycle (internal/app/work) ---

	c.apply("RootWorkItemCreated", 1, 1, reduceRootWorkItemCreated,
		entityKeyFromField(decode[rootWorkItemCreatedPayload], func(p rootWorkItemCreatedPayload) string { return p.WorkItemID }), "")
	c.apply("ChildWorkItemCreated", 1, 1, reduceChildWorkItemCreated,
		entityKeyFromField(decode[childWorkItemCreatedPayload], func(p childWorkItemCreatedPayload) string { return p.WorkItemID }), "")
	c.apply("WORK_ITEM_MARKED_READY", 1, 1, reduceWorkItemMarkedReady,
		entityKeyFromField(decode[workItemMarkedReadyPayload], func(p workItemMarkedReadyPayload) string { return p.WorkItemID }), "")

	// ScopeExpansion* events key by FamilyID, only sometimes naming a
	// specific ReferencedWorkItemID directly. When ReferencedWorkItemID IS
	// populated, EntityKeyOf resolves it directly (a request/decision
	// scoped to one specific child WorkItem's own row). When it is empty
	// (a family-level expansion with no single child target), EntityKeyOf
	// returns ok=false: the caller (V6-08A) resolves the family's own ROOT
	// WorkItem row instead, via ListProjectionRows(generation) filtered to
	// FamilyID == this event's FamilyID && IsRoot == true — a query this
	// schema already supports with no new repository method, matching this
	// task's own "Hoàn thành khi: ... without new design choice."
	c.apply("ScopeExpansionRequested", 1, 1, reduceScopeExpansionRequested,
		entityKeyFromField(decode[scopeExpansionRequestedPayload], func(p scopeExpansionRequestedPayload) string { return p.ReferencedWorkItemID }),
		"empty ReferencedWorkItemID: resolve via ListProjectionRows(generation) where FamilyID matches and IsRoot")
	c.apply("ScopeExpansionApproved", 1, 1, reduceScopeExpansionApproved,
		entityKeyFromField(decode[scopeExpansionApprovedPayload], func(p scopeExpansionApprovedPayload) string { return p.ReferencedWorkItemID }),
		"empty ReferencedWorkItemID: resolve via ListProjectionRows(generation) where FamilyID matches and IsRoot")
	c.apply("ScopeExpansionRejected", 1, 1, reduceScopeExpansionRejected,
		familyRootEntityKey(decode[scopeExpansionRejectedPayload]),
		"resolve via ListProjectionRows(generation) where FamilyID matches and IsRoot (payload never names a specific WorkItem)")
	c.apply("ScopeExpansionWithdrawn", 1, 1, reduceScopeExpansionWithdrawn,
		familyRootEntityKey(decode[scopeExpansionWithdrawnPayload]),
		"resolve via ListProjectionRows(generation) where FamilyID matches and IsRoot (payload never names a specific WorkItem)")

	// --- Apply: Run lifecycle (internal/app/runtime) ---

	c.apply("WorkflowRunStarted", 1, 1, reduceWorkflowRunStarted,
		entityKeyFromField(decode[workflowRunStartedPayload], func(p workflowRunStartedPayload) string { return p.WorkItemID }), "")
	c.apply("RUN_CANCELLATION_REQUESTED", 1, 1, reduceRunCancellationRequested,
		entityKeyFromField(decode[runCancellationRequestedPayload], func(p runCancellationRequestedPayload) string { return p.WorkItemID }), "")
	c.apply("RUN_COMPLETION_REQUESTED", 1, 1, reduceRunCompletionRequested,
		entityKeyFromField(decode[runCompletionRequestedPayload], func(p runCompletionRequestedPayload) string { return p.WorkItemID }), "")
	c.apply("RUN_FAILED", 1, 1, reduceRunFailed,
		entityKeyFromField(decode[runFailedPayload], func(p runFailedPayload) string { return p.WorkItemID }), "")
	c.apply("RUN_CANCELLED", 1, 1, reduceRunCancelled,
		entityKeyFromField(decode[runCancelledPayload], func(p runCancelledPayload) string { return p.WorkItemID }), "")
	// WORKFLOW_RUN_FINALIZED's own payload (internal/adapters/sqlite's own
	// workflow_store.go, the durable job-lease finalize confirmation) never
	// carries WorkItemID, only RunID — EntityKeyOf always returns ok=false.
	c.apply("WORKFLOW_RUN_FINALIZED", 1, 1, reduceWorkflowRunFinalized, nil,
		"payload has no WorkItemID: resolve via ListProjectionRows(generation) where ActiveRunID equals the event's own RunID")
	c.apply("WORK_ITEM_BLOCKED", 1, 1, reduceWorkItemBlocked,
		entityKeyFromField(decode[workItemBlockedPayload], func(p workItemBlockedPayload) string { return p.WorkItemID }), "")
	c.apply("WORK_ITEM_BLOCKER_RESOLVED", 1, 1, reduceWorkItemBlockerResolved,
		entityKeyFromField(decode[workItemBlockerResolvedPayload], func(p workItemBlockerResolvedPayload) string { return p.WorkItemID }), "")
	c.apply("WORK_ITEM_CANCELLED", 1, 1, reduceWorkItemCancelled,
		entityKeyFromField(decode[workItemCancelledPayload], func(p workItemCancelledPayload) string { return p.WorkItemID }), "")

	// --- Ignore: catalog/repository/component (internal/app/catalog) ---

	c.ignore("ProjectCreated", 1, "Project existence is catalog data, not a Kanban/task-detail entity; ProjectID scoping for this projection's own rows comes from each WorkItem/Run event's own ProjectID field, never from tracking Project creation itself.")
	c.ignore("RepositoryRegistered", 1, "Repository catalog data — V6-10's own multi-repo badge aggregation cross-references catalog state directly at read time (its own explicit Thực hiện bullet), never via this projection.")
	c.ignore("RepositoryProbeRetried", 1, "Repository catalog/onboarding data, no Kanban/task-detail surface.")
	c.ignore("ComponentPackAssigned", 1, "Component/pack catalog data, no Kanban/task-detail surface.")

	// --- Ignore: definition authoring (internal/app/definitions) ---

	c.ignore("DefinitionCreated", 1, "Definition authoring data (Screen 3), unrelated to a WorkItem's own Kanban/task-detail state.")
	c.ignore("DefinitionVersionPublished", 1, "Definition authoring data (Screen 3), unrelated to a WorkItem's own Kanban/task-detail state.")

	// --- Ignore: conversation (internal/app/message) ---

	c.ignore("MessageAppended", 1, "Conversation content — Screen 7's own conversation tab is a live, authoritative query (V6-07), never served from a projection.")

	// --- Ignore: node/attempt/graph-level runtime detail (internal/app/runtime) ---
	// Screen 8 (Run graph & timeline) reads these directly from
	// authoritative runtime tables via V6-06B (already shipped, no
	// projection dependency) — V6-10's own "Không làm: projection không
	// decide readiness/ValidAction" plus its own "Phạm vi: projected
	// card/detail only" keep this projection scoped to coarse WorkItem/Run
	// summary facts, never per-node/attempt graph detail.

	c.ignore("NODE_ROUTED", 1, "Node/attempt-level graph detail, served directly by V6-06B from authoritative runtime tables — out of this projection's Kanban/task-detail scope.")
	c.ignore("NODE_SCHEDULED", 1, "Node/attempt-level graph detail, served directly by V6-06B — out of scope.")
	c.ignore("NODE_RUN_DISPATCHED", 1, "Node/attempt-level graph detail, served directly by V6-06B — out of scope.")
	c.ignore("NODE_RUN_COMPLETED", 1, "Node/attempt-level graph detail, served directly by V6-06B — out of scope.")
	c.ignore("NODE_RUN_FAILED", 1, "Node/attempt-level graph detail, served directly by V6-06B — out of scope.")
	c.ignore("NODE_CYCLE_EXHAUSTED", 1, "Node/attempt-level graph detail, served directly by V6-06B — out of scope.")
	c.ignore("NODE_FORKED", 1, "Node/attempt-level graph detail (fork/join visualization), served directly by V6-06B — out of scope.")
	c.ignore("JOIN_DECIDED", 1, "Node/attempt-level graph detail (fork/join visualization), served directly by V6-06B — out of scope.")
	c.ignore("EXECUTION_ATTEMPT_FINALIZED", 1, "Attempt-level detail, served directly by V6-06B/V6-06C — out of scope.")
	c.ignore("EXECUTION_ATTEMPT_TERMINATED", 1, "Attempt-level detail, served directly by V6-06B/V6-06C — out of scope.")
	c.ignore("COMPLETION_DECIDED", 1, "The completion-policy VERDICT is a node/graph-level detail (served directly by V6-06B/V6-06C); its WorkItem-level CONSEQUENCES are what RUN_FAILED/WORK_ITEM_BLOCKED/RUN_COMPLETION_REQUESTED already surface to this projection, which is what this Catalog classifies Apply — applying the raw verdict a second time here would be redundant, lower-layer duplication.")
	c.ignore("RECOVERY_DECISION_RECORDED", 1, "Recovery-reaper diagnostic detail, served directly by V6-06C — out of scope.")

	// --- Ignore: workspace lifecycle/release/reconcile ---

	c.ignore("REPOSITORY_WORKSPACE_QUARANTINED", 1, "RepositoryWorkspace health/lifecycle — Doctor/Screen-2-level concern, no Kanban card badge per V6-00's own Screen 5 spec.")
	c.ignore("REPOSITORY_WORKSPACE_RELEASED", 1, "RepositoryWorkspace health/lifecycle — out of scope, see REPOSITORY_WORKSPACE_QUARANTINED.")
	c.ignore("REPOSITORY_WORKSPACE_RECREATED", 1, "RepositoryWorkspace health/lifecycle — out of scope, see REPOSITORY_WORKSPACE_QUARANTINED.")
	c.ignore("WorkspaceSetReleaseRequested", 1, "ReleaseSet/local-Git operation, a separate screen's own concern (V6-10F), no Kanban card surface.")
	c.ignore("WorkspaceReconciliationRequested", 1, "Workspace reconcile operation, no Kanban card surface.")

	// --- Ignore: ReleaseSet local commit (internal/app/releasesetcommit) ---

	c.ignore("ReleaseSetLocalCommitRequested", 1, "ReleaseSet local-commit operation status (V6-10F's own screen), no Kanban card surface.")
	c.ignore("ReleaseSetLocalCommitCommitted", 1, "ReleaseSet local-commit operation status — out of scope, see ReleaseSetLocalCommitRequested.")
	c.ignore("ReleaseSetLocalCommitFailed", 1, "ReleaseSet local-commit operation status — out of scope, see ReleaseSetLocalCommitRequested.")

	// --- Ignore: installation-scoped administrative data ---

	c.ignore("SafeSettingsUpdated", 1, "Installation-scoped safe-settings singleton, no per-WorkItem Kanban surface.")
	c.ignore("AdapterBuildRegistered", 1, "Adapter-build registry, an installation-scoped administrative concern, no Kanban card surface.")
	c.ignore("ARTIFACT_SWEEP_COMPLETED", 1, "Artifact-sweeper housekeeping operation, no Kanban card surface.")

	// --- Ignore: transient WorkItem-level cancellation intent ---

	c.ignore("WORK_ITEM_CANCELLATION_REQUESTED", 1, "Two-phase cancellation INTENT; V6-00's own Screen 5 spec names no 'cancelling' badge requirement (only STALE/DEGRADED freshness and BLOCKED badges) — the Kanban-visible signal is the terminal WORK_ITEM_CANCELLED this Catalog classifies Apply. A future task can reclassify this Apply if a 'cancelling' badge is ever specified, without a schema change (the row already has ActiveRunStatus for exactly this kind of transient badge).")
}

// familyRootEntityKey builds an EntityKeyFunc for a ScopeExpansion* event
// whose payload never names a specific WorkItem at all (only FamilyID) —
// always ok=false, the caller resolves the family's own root row instead
// (see this file's own registerClassifications doc comment).
func familyRootEntityKey[T any](decodeFn func(string) (T, error)) EntityKeyFunc {
	return func(payloadJSON string) (string, bool, error) {
		if _, err := decodeFn(payloadJSON); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
}
