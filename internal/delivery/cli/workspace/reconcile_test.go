package workspace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliworkspace "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workspace"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func decodeReconcileResult(t *testing.T, stdout *bytes.Buffer) (cli.ResultEnvelope, workspacereconcile.RequestWorkspaceReconciliationResult) {
	t.Helper()
	var envelope cli.ResultEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v\nstdout=%s", err, stdout.String())
	}
	resultBytes, err := json.Marshal(envelope.Result)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	var result workspacereconcile.RequestWorkspaceReconciliationResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("decode RequestWorkspaceReconciliationResult: %v", err)
	}
	return envelope, result
}

// reconcileArgs builds `repository-workspace reconcile`'s own flag
// arguments — flags must precede the positional <repositoryWorkspaceId>
// argument: Go's flag.FlagSet stops parsing flags at the first non-flag
// token, so a positional argument placed BEFORE a flag would make that
// flag (and everything after it) look like more positional arguments
// instead — mirrors internal/delivery/cli/run/start_test.go's own args
// ordering exactly.
func reconcileArgs(sw seededWorkspace, expectedVersion, idempotencyKey string) []string {
	args := []string{"--project-id", sw.projectID}
	if expectedVersion != "" {
		args = append(args, "--expected-version", expectedVersion)
	}
	args = append(args, "--idempotency-key", idempotencyKey, sw.repositoryWorkspaceID)
	return args
}

func TestRepositoryWorkspaceReconcile_HappyPath_EnqueuesRealJob(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")

	var stdout, stderr bytes.Buffer
	args := reconcileArgs(sw, "1", "key-1")
	if err := cliworkspace.RunRepositoryWorkspaceReconcile(context.Background(), env.deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunRepositoryWorkspaceReconcile: %v, stderr=%s", err, stderr.String())
	}

	envelope, result := decodeReconcileResult(t, &stdout)
	if envelope.Replayed {
		t.Fatal("fresh reconcile reported Replayed=true, want false")
	}
	if result.ReconciliationJobID == "" {
		t.Fatal("ReconciliationJobID is empty")
	}

	var hasJob bool
	if err := env.uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		hasJob, err = tx.Jobs().HasActiveJobForAggregateIDs(context.Background(), []string{sw.repositoryWorkspaceID})
		return err
	}); err != nil {
		t.Fatalf("HasActiveJobForAggregateIDs: %v", err)
	}
	if !hasJob {
		t.Fatal("expected a real, active WORKSPACE_RECONCILIATION job for the repository workspace's own AggregateID, found none")
	}
}

func TestRepositoryWorkspaceReconcile_Replay_ReturnsIdenticalJobID(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")
	args := reconcileArgs(sw, "1", "key-1")

	var stdout1, stderr1 bytes.Buffer
	if err := cliworkspace.RunRepositoryWorkspaceReconcile(context.Background(), env.deps, args, &stdout1, &stderr1); err != nil {
		t.Fatalf("RunRepositoryWorkspaceReconcile (first): %v, stderr=%s", err, stderr1.String())
	}
	_, first := decodeReconcileResult(t, &stdout1)

	var stdout2, stderr2 bytes.Buffer
	if err := cliworkspace.RunRepositoryWorkspaceReconcile(context.Background(), env.deps, args, &stdout2, &stderr2); err != nil {
		t.Fatalf("RunRepositoryWorkspaceReconcile (replay): %v, stderr=%s", err, stderr2.String())
	}
	envelope2, second := decodeReconcileResult(t, &stdout2)
	if !envelope2.Replayed {
		t.Fatal("retry with identical idempotency key reported Replayed=false, want true")
	}
	if second.ReconciliationJobID != first.ReconciliationJobID {
		t.Fatalf("replayed ReconciliationJobID = %q, want the exact original %q", second.ReconciliationJobID, first.ReconciliationJobID)
	}
}

// TestRepositoryWorkspaceReconcile_NotReconcilable_Blocked drives the
// repository workspace to RELEASED through the real
// ports.WorkspaceLifecycle.ReleaseRepositoryWorkspace transition (a state
// ErrWorkspaceNotReconcilable never allows), then confirms reconcile
// refuses it for real.
func TestRepositoryWorkspaceReconcile_NotReconcilable_Blocked(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")

	if err := env.store.ReleaseRepositoryWorkspace(context.Background(), ports.ReleaseRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(sw.repositoryWorkspaceID), ExpectedVersion: 1,
		EventID: "evt-release-1",
	}); err != nil {
		t.Fatalf("ReleaseRepositoryWorkspace: %v", err)
	}

	var stdout, stderr bytes.Buffer
	args := reconcileArgs(sw, "2", "key-1")
	err := cliworkspace.RunRepositoryWorkspaceReconcile(context.Background(), env.deps, args, &stdout, &stderr)
	if !errors.Is(err, workspacereconcile.ErrWorkspaceNotReconcilable) {
		t.Fatalf("RunRepositoryWorkspaceReconcile error = %v, want ErrWorkspaceNotReconcilable", err)
	}
}

// TestRepositoryWorkspaceReconcile_StaleExpectedVersion_Rejected is a
// GENUINELY stale scenario: a real quarantine transition bumps the
// repository workspace's own version 1->2 for real, and a caller still
// presenting the pre-quarantine --expected-version (1) is rejected.
func TestRepositoryWorkspaceReconcile_StaleExpectedVersion_Rejected(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")

	if err := env.store.QuarantineRepositoryWorkspace(context.Background(), ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(sw.repositoryWorkspaceID), ExpectedVersion: 1,
		Reason: "test: simulated lease-losing writer", EventID: "evt-quarantine-1",
	}); err != nil {
		t.Fatalf("QuarantineRepositoryWorkspace: %v", err)
	}

	var stdout, stderr bytes.Buffer
	args := reconcileArgs(sw, "1", "key-1")
	err := cliworkspace.RunRepositoryWorkspaceReconcile(context.Background(), env.deps, args, &stdout, &stderr)
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("RunRepositoryWorkspaceReconcile error = %v, want ports.ErrOptimisticConflict", err)
	}

	// The CURRENT --expected-version (2) succeeds — proving the rejection
	// above was really about staleness, not reconcile refusing QUARANTINED
	// outright (it must not: QUARANTINED is one of the two reconcilable
	// states).
	var okStdout, okStderr bytes.Buffer
	okArgs := reconcileArgs(sw, "2", "key-2")
	if err := cliworkspace.RunRepositoryWorkspaceReconcile(context.Background(), env.deps, okArgs, &okStdout, &okStderr); err != nil {
		t.Fatalf("RunRepositoryWorkspaceReconcile (current version): %v, stderr=%s", err, okStderr.String())
	}
}

func TestRepositoryWorkspaceReconcile_ZeroExpectedVersion_ReturnsUsageError(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")

	var stdout, stderr bytes.Buffer
	args := reconcileArgs(sw, "", "key-1")
	err := cliworkspace.RunRepositoryWorkspaceReconcile(context.Background(), env.deps, args, &stdout, &stderr)
	if !cli.IsUsageError(err) {
		t.Fatalf("RunRepositoryWorkspaceReconcile error = %v, want a cli.UsageError for a missing --expected-version", err)
	}
}
