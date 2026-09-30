package alphagate

// Journeys is the closed list of the system acceptance journeys in
// docs/design/01-system-design.md §13, each mapped to the real evidence that
// backs it (V8-11: "assessment đầy đủ cho toàn bộ system journeys").
//
// No journey→test mapping existed before V8-11 — the journeys were written
// first and V5/V6 built suites around them without recording which test
// answers which journey. This table is that record. It is deliberately
// conservative: a journey lists only tests whose name and body assert the
// behaviour the journey states, and TestJourneyTableMatchesTheRepository
// keeps it honest (every named test must exist, every journey in the design
// doc must have a row and vice versa).
var Journeys = []Journey{
	{
		ID:      "J01",
		Summary: "Publish workflow V1, publish V2; an existing run keeps V1",
		Tests: []TestRef{
			{"internal/app/definitions", "TestPublishDefinitionVersion_DifferentContent_ProducesNewVersionAndEvent"},
			{"internal/adapters/sqlite", "TestWorkflowVersionAndRunSurviveRestartWithPinnedSnapshot"},
			{"internal/delivery/httpapi/run", "TestStartWorkflowRun_HTTP_PinnedWorkflowVersionMismatch_ReturnsConflict"},
		},
	},
	{
		ID:      "J02",
		Summary: "Two-repo Project; two root tasks get their own worktrees; a child reuses the family workspace",
		Tests: []TestRef{
			{"internal/adapters/gitworktree", "TestProviderMultiRepositoryWorkspaceSetAndChildReuse"},
			{"internal/app/workspaceprovision", "TestEndToEnd_MultiRepoProvision_BothReachReadyWithBaseRevisionSet"},
			{"internal/app/work", "TestCreateRootWorkItem_MultiRepoFixtureSQLite"},
		},
	},
	{
		ID:      "J03",
		Summary: "Kill the worker at each fault boundary of a mutating agent node; restart reconciles with no duplicate side effect",
		Tests: []TestRef{
			{"internal/integration/v8fault", "TestV8Fault_Boundary4_AfterCheckpointBeforeProcessExit"},
			{"internal/integration/v8fault", "TestV8Fault_Boundary5_AfterProcessExitBeforeOutcomeCommit_Mutating"},
			{"internal/integration/v8fault", "TestV8Fault_Boundary6_AfterOutcomeCommitBeforeNextDispatch"},
			{"internal/integration/v5accept", "TestV5AcceptCrashRecovery_RealRetryReplaysWithNoDuplicates"},
		},
	},
	{
		ID:      "J04",
		Summary: "Agent claims done but a required gate is missing or failing: the WorkItem is not DONE",
		Tests:   []TestRef{{"internal/integration/v5accept", "TestV5AcceptFalseCompletionOracle"}},
	},
	{
		ID:      "J05",
		Summary: "Provider session reference lost: a new Attempt rebuilds its context and continues with Start",
		Tests: []TestRef{
			{"internal/adapters/providers", "TestSPK12InvalidProviderSessionNeverBlocksRecovery"},
			{"internal/adapters/sqlite", "TestContextSnapshotSurvivesRestartWithoutProviderSession"},
		},
	},
	{
		ID:      "J06",
		Summary: "Claude and Codex fake contracts produce the same normalized domain result",
		Tests:   []TestRef{{"internal/integration/v5accept", "TestV5AcceptConformanceMatrix"}},
	},
	{
		ID:      "J07",
		Summary: "A scope violation is blocked before the fenced finalize and its evidence is visible in the UI",
		Tests: []TestRef{
			{"internal/integration/v5accept", "TestV5AcceptScopeViolation_RealDiffRejectsOutOfScopeWrite"},
			{"internal/adapters/sqlite", "TestFinalizerRejectsOutOfScopeDiffBeforeFencedSQLiteMutation"},
		},
		Suites: []string{SuiteWeb},
	},
	{
		ID:      "J08",
		Summary: "Approval, rework, fork and join keep the right route across a restart",
		Tests: []TestRef{
			{"internal/app/runtime", "TestApprovalTimeoutHandler_SQLite_PersistsAcrossRestart"},
			{"internal/app/runtime", "TestResolveApproval_AuthorizedActor_ApprovesAndRoutes"},
			{"internal/app/runtime", "TestGetRunGraph_SQLite_ForkJoin_ReturnsStructureAndActivations"},
			{"internal/app/runtime", "TestGetRunGraph_SQLite_Rework_CreatesNewActivationOnReworkTarget"},
			{"internal/integration/v5accept", "TestV5AcceptFullComposition_RealFourRoleGraphReachesSucceededAndSurvivesRestart"},
		},
	},
	{
		ID:      "J09",
		Summary: "Delete the projection and rebuild it from the watermark for the same view; a poison event shows DEGRADED/STALE instead of being skipped",
		Tests: []TestRef{
			{"internal/integration/v6accept", "TestV6HTTPAcceptance_Fault_PoisonProjection"},
			{"internal/integration/v6accept", "TestV6HTTPAcceptance_Fault_CrashAfterRebuildCutover"},
		},
	},
	{
		ID:      "J10",
		Summary: "A tampered artifact fails verify; a secret fixture never appears in the DB, events or artifacts",
		Tests: []TestRef{
			{"internal/integration/v5accept", "TestV5AcceptArtifactTamper_RealVerifyDetectsRealCorruption"},
			{"internal/integration/v5accept", "TestV5AcceptRetainedDataSecretScan_RealSecretNeverPersistedUnredacted"},
		},
	},
	{
		ID:      "J11",
		Summary: "The whole journey can be operated from the browser without touching SQLite or Git by hand",
		Suites:  []string{SuiteE2E},
	},
	{
		ID:      "J12",
		Summary: "The semantic suite passes on Windows and Linux and the race detector passes in supported CI",
		Suites:  []string{SuiteSemanticDiff, SuiteRace, SuiteContract},
	},
	{
		ID:      "J13",
		Summary: "Wrong Host/Origin, a mutation without a session token, and an external bind are all refused",
		Suites:  []string{SuiteV8Security},
	},
	{
		ID:      "J14",
		Summary: "Scope expansion grants only a new activation; multi-repo write without the capability and checker write-to-source are both blocked",
		Tests: []TestRef{
			{"internal/app/work", "TestApproveScopeExpansion_HappyPath_BumpsScopeVersionAndProvisionsNewRepository"},
			{"internal/integration/v5accept", "TestV5AcceptMultiRepositoryWriteWithoutGrant_RealAdmissionRejectsBeforeSpawn"},
			{"internal/integration/v5accept", "TestV5AcceptCheckerWriteAttempt_RealStrictReadOnlyDiffRejectsRealMutation"},
		},
	},
	{
		ID:      "J15",
		Summary: "END stops at VERIFYING; CompletionPolicy checks the exact sealed ReleaseSet before atomically completing Run and WorkItem; Alpha makes a local commit and never pushes",
		Tests: []TestRef{
			{"internal/app/runtime", "TestAdvanceRun_SQLite_EndReached_RunVerifyingPersistsAcrossRestart"},
			{"internal/app/runtime", "TestEvaluateCompletionCandidate_Block_ReleaseSetNotSealed"},
			{"internal/app/runtime", "TestCompletionOrchestrator_SQLite_DecidesAVerifyingRunAndItSurvivesRestart"},
			{"internal/adapters/gitworktree", "TestProvider_CreateLocalCommit_NeverTouchesRemote"},
			{"internal/integration/v6accept", "TestV6HTTPAcceptance_Fault_CrashAfterGitCommit"},
		},
	},
	{
		ID:      "J15A",
		Summary: "A REWORK completion decision with no valid rework edge returns BLOCK; no route is built outside the pinned graph",
		Tests: []TestRef{
			{"internal/app/runtime", "TestEvaluateCompletionCandidate_Block_MissingEvidence"},
			{"internal/app/runtime", "TestEvaluateCompletionCandidate_Block_ReworkBudgetExhausted"},
			{"internal/app/runtime", "TestEvaluateCompletionCandidate_Rework_ValidReworkEdgeUnderBudget"},
		},
	},
	{
		ID:      "J16",
		Summary: "The periodic recovery reaper detects a lease that expires after startup without creating a duplicate recovery job",
		Tests: []TestRef{
			{"internal/app/runtime", "TestRecoveryReaperHandler_OrphanedReadOnlyAttempt_RetriesWithNewAttemptAndJob"},
			{"internal/integration/v8fault", "TestV8Fault_Boundary2_AfterJobCommitBeforeClaim"},
			{"internal/integration/v8fault", "TestV8Fault_Boundary3_AfterJobClaimBeforeProcessSpawn"},
		},
	},
	{
		ID:      "J17",
		Summary: "The retention sweep never deletes canonical conversation, referenced context, held artifacts or audit metadata",
		Tests: []TestRef{
			{"internal/integration/v5accept", "TestV5AcceptRetentionSweep_CanonicalConversationHeldAndReferencedSurviveRealEligibleOrphanPurge"},
			{"internal/app/artifactsweep", "TestExecuteArtifactSweep_AttachedSiblingSharesLocator_BlocksWholeGroup"},
		},
	},
	{
		ID:      "J18",
		Summary: "CancelRun mid mutating attempt: CANCELLING, process tree stopped, WriteLease released only after confirmation, unknown outcome INDETERMINATE + QUARANTINED, no automatic workspace cleanup",
		Tests: []TestRef{
			{"internal/integration/v5accept", "TestV5AcceptCancelDuringMutatingAttempt_RealQuarantineNeverPromotes"},
			{"internal/integration/v6accept", "TestV6HTTPAcceptance_Fault_CancelQuiesceSurvivesWorkerCrash"},
		},
	},
	{
		ID:      "J19",
		Summary: "A WAIT signal sent twice is consumed once; a due timer job never replaces signal authority",
		Tests: []TestRef{
			{"internal/app/runtime", "TestSignalWait_AfterAlreadyConsumed_WonFalseNoReRoute"},
			{"internal/delivery/cli/decision", "TestSignalWait_Concurrent_SameSignalKey_ExactlyOneWinner"},
		},
	},
	{
		ID:      "J20",
		Summary: "A provider CLI upgrade: the new build is refused at admission until the operator probes/registers and republishes; a running run keeps its pinned build",
		Tests:   []TestRef{{"internal/integration/v5accept", "TestV5AcceptAdapterDrift_RealAdmissionRejectsMismatchedPin"}},
	},
	{
		ID:      "J21",
		Summary: "An environment that cannot enforce ENFORCED_ISOLATED returns ISOLATION_ENFORCEMENT_UNAVAILABLE with zero process spawns and no auto-downgrade",
		Tests:   []TestRef{{"internal/integration/v5accept", "TestV5AcceptIsolationUnavailable_RealAdmissionRejectsBeforeSpawn"}},
	},
	{
		ID:      "J22",
		Summary: "An old event schema version still replays after a new one is registered; removing a decoder a fixture references fails CI",
		Tests: []TestRef{
			{"internal/app/eventschema", "TestGoldenFixtures_DecodeEveryRegisteredVersion"},
			{"internal/app/eventschema", "TestGoldenFixtures_EveryTestdataFileIsCovered"},
			{"internal/app/eventschema", "TestDecodeLatest_UpcastsHistoricalVersionToCurrentShape"},
		},
	},
}
