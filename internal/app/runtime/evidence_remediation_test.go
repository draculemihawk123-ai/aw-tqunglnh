package runtime_test

// This file is the V5-09/V5-10 acceptance-gap remediation's own test suite
// (2026-09-10 post-merge review, PR1 scope: PASS/SUCCEEDED path only —
// see baocaov5checklist.md's own remediation entry): proves
// CommandNodeExecutor/GateNodeExecutor's own output artifact is staged
// ORPHAN (never ATTACHED directly), promoted only by
// validateAndAttachFinalizationEvidenceTx inside FinalizeExecutionAttempt's
// own fenced transaction, alongside one Evidence row per proposal —
// mirroring TestAgentNodeExecutor_Success_BuildsEvidenceAndFinalizesEndToEnd/
// TestFinalizeExecutionAttempt_TamperedEvidence_RejectsBeforeCommitting's
// own exact style for the pre-existing diff-manifest evidence path.

import (
	"context"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func mustGetArtifactAttachState(t *testing.T, uow *fake.UnitOfWork, artifactID string) string {
	t.Helper()
	var state string
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		record, err := tx.Artifacts().GetArtifact(context.Background(), artifactID)
		if err != nil {
			return err
		}
		state = string(record.AttachState)
		return nil
	}); err != nil {
		t.Fatalf("load artifact %s: %v", artifactID, err)
	}
	return state
}

func TestCommandNodeExecutor_Success_OutputArtifactOrphanUntilFinalizePromotesItWithEvidence(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		argv: []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
	})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})
	ctx := context.Background()

	result, err := executor.Execute(ctx, ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Evidence == nil || len(result.Evidence.OutputArtifactRefs) != 1 {
		t.Fatalf("result.Evidence = %+v, want exactly one output artifact ref", result.Evidence)
	}
	if len(result.Evidence.EvidenceEntries) != 1 {
		t.Fatalf("result.Evidence.EvidenceEntries = %+v, want exactly one proposal", result.Evidence.EvidenceEntries)
	}
	entry := result.Evidence.EvidenceEntries[0]
	if entry.Kind != runtimedomain.EvidenceKindCommandExecution || entry.Verdict != runtimedomain.EvidenceVerdictSucceeded {
		t.Fatalf("entry = %+v, want Kind=%s Verdict=%s", entry, runtimedomain.EvidenceKindCommandExecution, runtimedomain.EvidenceVerdictSucceeded)
	}

	artifactID := result.Evidence.OutputArtifactRefs[0]
	if state := mustGetArtifactAttachState(t, uow, artifactID); state != "ORPHAN" {
		t.Fatalf("output artifact AttachState before finalize = %s, want ORPHAN", state)
	}

	version := loadAttemptVersion(t, uow, attemptID)
	if _, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: version,
		NextState: result.State, TerminationReason: result.TerminationReason, SelectedOutcome: result.SelectedOutcome,
		JobLease: jobLease, CorrelationID: "corr-1", Evidence: result.Evidence,
	}); err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}

	if state := mustGetArtifactAttachState(t, uow, artifactID); state != "ATTACHED" {
		t.Fatalf("output artifact AttachState after finalize = %s, want ATTACHED", state)
	}

	evidenceID := attemptID + ":" + runtimedomain.EvidenceKindCommandExecution
	var stored runtimedomain.Evidence
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		stored, err = tx.Runtime().GetEvidence(ctx, evidenceID)
		return err
	}); err != nil {
		t.Fatalf("GetEvidence(%s): %v", evidenceID, err)
	}
	if stored.Verdict != runtimedomain.EvidenceVerdictSucceeded || len(stored.ArtifactReferences) != 1 || stored.ArtifactReferences[0] != artifactID {
		t.Fatalf("stored evidence = %+v, want Verdict=%s referencing %s", stored, runtimedomain.EvidenceVerdictSucceeded, artifactID)
	}
}

func TestGateNodeExecutor_AllCriteriaPass_OutputArtifactOrphanUntilFinalizePromotesItWithEvidencePerCriterion(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{})
	supervisor := &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
		Stdout: string(gateStdout(t, map[string]map[string]string{"lint": {"verdict": "PASS"}})),
	}
	executor, _, _ := newTestGateNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})
	ctx := context.Background()

	result, err := executor.Execute(ctx, ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(result.Evidence.EvidenceEntries) != 1 || result.Evidence.EvidenceEntries[0].Kind != "lint" {
		t.Fatalf("result.Evidence.EvidenceEntries = %+v, want exactly one proposal for criterion lint", result.Evidence.EvidenceEntries)
	}
	if result.Evidence.EvidenceEntries[0].Verdict != "PASS" {
		t.Fatalf("EvidenceEntries[0].Verdict = %q, want PASS", result.Evidence.EvidenceEntries[0].Verdict)
	}

	artifactID := result.Evidence.OutputArtifactRefs[0]
	if state := mustGetArtifactAttachState(t, uow, artifactID); state != "ORPHAN" {
		t.Fatalf("gate result artifact AttachState before finalize = %s, want ORPHAN", state)
	}

	version := loadAttemptVersion(t, uow, attemptID)
	if _, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: version,
		NextState: result.State, TerminationReason: result.TerminationReason, SelectedOutcome: result.SelectedOutcome,
		JobLease: jobLease, CorrelationID: "corr-1", Evidence: result.Evidence,
	}); err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}

	if state := mustGetArtifactAttachState(t, uow, artifactID); state != "ATTACHED" {
		t.Fatalf("gate result artifact AttachState after finalize = %s, want ATTACHED", state)
	}

	evidenceID := attemptID + ":lint"
	var stored runtimedomain.Evidence
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		stored, err = tx.Runtime().GetEvidence(ctx, evidenceID)
		return err
	}); err != nil {
		t.Fatalf("GetEvidence(%s): %v", evidenceID, err)
	}
	if stored.Verdict != "PASS" || len(stored.ArtifactReferences) != 1 || stored.ArtifactReferences[0] != artifactID {
		t.Fatalf("stored evidence = %+v, want Verdict=PASS referencing %s", stored, artifactID)
	}
}

// TestFinalizeExecutionAttempt_EvidenceEntryNamesUnlistedArtifact_RejectsBeforeCommitting
// proves a tampered/malformed EvidenceProposal — one naming an artifact ID
// never listed in OutputArtifactRefs — is rejected before anything commits,
// the same "reject before promoting or persisting anything" discipline
// TestFinalizeExecutionAttempt_TamperedEvidence_RejectsBeforeCommitting
// already proves for the diff-manifest path.
func TestFinalizeExecutionAttempt_EvidenceEntryNamesUnlistedArtifact_RejectsBeforeCommitting(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		argv: []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
	})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})
	ctx := context.Background()

	result, err := executor.Execute(ctx, ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	tampered := *result.Evidence
	tampered.EvidenceEntries = []ports.EvidenceProposal{{
		Kind: runtimedomain.EvidenceKindCommandExecution, Verdict: runtimedomain.EvidenceVerdictSucceeded,
		ArtifactReferences: []string{"artifact-never-listed"}, PolicyVersion: "policy-1",
	}}

	version := loadAttemptVersion(t, uow, attemptID)
	if _, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: version,
		NextState: result.State, TerminationReason: result.TerminationReason, SelectedOutcome: result.SelectedOutcome,
		JobLease: jobLease, CorrelationID: "corr-1", Evidence: &tampered,
	}); err == nil {
		t.Fatal("FinalizeExecutionAttempt accepted an evidence entry naming an artifact outside OutputArtifactRefs, want a fail-closed error")
	}

	if after := loadAttemptVersion(t, uow, attemptID); after != version {
		t.Fatalf("attempt version after rejected finalize = %d, want unchanged %d", after, version)
	}
	artifactID := result.Evidence.OutputArtifactRefs[0]
	if state := mustGetArtifactAttachState(t, uow, artifactID); state != "ORPHAN" {
		t.Fatalf("output artifact AttachState after rejected finalize = %s, want still ORPHAN", state)
	}
}

// TestFinalizeExecutionAttempt_MissingOutputArtifact_RejectsBeforeCommitting
// proves OutputArtifactRefs itself naming a nonexistent artifact ID (never
// actually staged ORPHAN by the executor) is rejected outright, rather than
// silently skipped.
func TestFinalizeExecutionAttempt_MissingOutputArtifact_RejectsBeforeCommitting(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		argv: []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
	})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})
	ctx := context.Background()

	result, err := executor.Execute(ctx, ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	tampered := *result.Evidence
	tampered.OutputArtifactRefs = []string{"artifact-does-not-exist"}
	tampered.EvidenceEntries = nil

	version := loadAttemptVersion(t, uow, attemptID)
	if _, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: version,
		NextState: result.State, TerminationReason: result.TerminationReason, SelectedOutcome: result.SelectedOutcome,
		JobLease: jobLease, CorrelationID: "corr-1", Evidence: &tampered,
	}); err == nil {
		t.Fatal("FinalizeExecutionAttempt accepted a nonexistent output artifact reference, want a fail-closed error")
	}

	if after := loadAttemptVersion(t, uow, attemptID); after != version {
		t.Fatalf("attempt version after rejected finalize = %d, want unchanged %d", after, version)
	}
	// The REAL output artifact this attempt actually staged must still be
	// ORPHAN — the whole transaction rolled back, not just the bad
	// reference.
	realArtifactID := result.Evidence.OutputArtifactRefs[0]
	if state := mustGetArtifactAttachState(t, uow, realArtifactID); state != "ORPHAN" {
		t.Fatalf("real output artifact AttachState after rejected finalize = %s, want still ORPHAN", state)
	}
}
