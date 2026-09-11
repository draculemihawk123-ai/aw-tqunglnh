package runtime_test

// This file is the V5-09/V5-10 acceptance-gap remediation's own test suite
// for the "combined PR" scope the user asked for (2026-09-10, "gộp 4 PR
// thành 1 luôn"): truncation fail-closed and secret redaction for both
// CommandNodeExecutor and GateNodeExecutor.

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func mustReadArtifactContent(t *testing.T, uow *fake.UnitOfWork, store ports.ArtifactStore, artifactID string) string {
	t.Helper()
	ctx := context.Background()
	var ref ports.ArtifactRef
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		record, err := tx.Artifacts().GetArtifact(ctx, artifactID)
		if err != nil {
			return err
		}
		ref = ports.ArtifactRef{
			Locator: record.Locator, SHA256: record.ContentHash, Size: record.Size,
			ContentType: record.MediaType, Sensitivity: record.Sensitivity, Redacted: record.Redacted,
		}
		return nil
	}); err != nil {
		t.Fatalf("load artifact record %s: %v", artifactID, err)
	}
	rc, err := store.Open(ctx, ref)
	if err != nil {
		t.Fatalf("Open artifact %s: %v", artifactID, err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read artifact %s: %v", artifactID, err)
	}
	return string(body)
}

// --- Truncation fail-closed ---

func TestCommandNodeExecutor_OutputTruncated_FailsClosedDespiteZeroExit(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		argv: []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
	})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true, OutputTruncated: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("result = %+v, want FAILED — a truncated capture must never be trusted as a clean success even with ExitCode=0", result)
	}
	if result.Evidence != nil {
		t.Fatalf("result.Evidence = %+v, want nil — a truncated attempt never proposes SUCCEEDED evidence", result.Evidence)
	}
}

func TestGateNodeExecutor_OutputTruncated_AllCriteriaErrorDespiteValidJSON(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{})
	supervisor := &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true, OutputTruncated: true},
		Stdout: string(gateStdout(t, map[string]map[string]string{"lint": {"verdict": "PASS"}})),
	}
	executor, _, _ := newTestGateNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("result = %+v, want FAILED — truncation must override even a syntactically valid, all-PASS JSON body", result)
	}
	if len(result.Evidence.EvidenceEntries) != 1 || result.Evidence.EvidenceEntries[0].Verdict != "ERROR" {
		t.Fatalf("EvidenceEntries = %+v, want exactly one entry with Verdict=ERROR", result.Evidence.EvidenceEntries)
	}
}

// --- Secret redaction ---

func TestCommandNodeExecutor_EchoedSecret_RedactedInPersistedArtifact(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		argv:       []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		secretRefs: []string{"MY_SECRET"},
	})
	supervisor := &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
		Stdout: "debug: using token sh-secret-value for auth",
		Stderr: "warning: sh-secret-value expires soon",
	}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{Values: map[string]string{"MY_SECRET": "sh-secret-value"}})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Evidence == nil || len(result.Evidence.OutputArtifactRefs) != 1 {
		t.Fatalf("result.Evidence = %+v, want exactly one output artifact ref", result.Evidence)
	}

	content := mustReadArtifactContent(t, uow, store, result.Evidence.OutputArtifactRefs[0])
	if strings.Contains(content, "sh-secret-value") {
		t.Fatalf("persisted command output artifact still contains the raw secret value: %s", content)
	}
	if !strings.Contains(content, "[REDACTED]") {
		t.Fatalf("persisted command output artifact was never redacted at all: %s", content)
	}
}

func TestCommandNodeExecutor_NoSecretsResolved_OutputPersistedAsIs(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		argv: []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
	})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}, Stdout: "plain, unremarkable output"}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := mustReadArtifactContent(t, uow, store, result.Evidence.OutputArtifactRefs[0])
	if !strings.Contains(content, "plain, unremarkable output") {
		t.Fatalf("persisted command output artifact = %s, want the original content unchanged (no secrets were ever resolved)", content)
	}
}

func TestGateNodeExecutor_EchoedSecret_RedactedInPersistedDetailAndReason(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{
		commandSecretRefs: []string{"MY_SECRET"},
	})
	stdout := `{"lint":{"verdict":"FAIL","detail":"leaked sh-secret-value in output","reason":"n/a"}}`
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}, Stdout: stdout}
	executor, _, _ := newTestGateNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{Values: map[string]string{"MY_SECRET": "sh-secret-value"}})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := mustReadArtifactContent(t, uow, store, result.Evidence.OutputArtifactRefs[0])
	if strings.Contains(content, "sh-secret-value") {
		t.Fatalf("persisted gate result artifact still contains the raw secret value: %s", content)
	}
	if !strings.Contains(content, "[REDACTED]") {
		t.Fatalf("persisted gate result artifact was never redacted at all: %s", content)
	}
}
