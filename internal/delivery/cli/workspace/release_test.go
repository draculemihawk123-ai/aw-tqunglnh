package workspace_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacerelease"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliworkspace "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workspace"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func decodeReleaseResult(t *testing.T, stdout *bytes.Buffer) (cli.ResultEnvelope, workspacerelease.RequestWorkspaceSetReleaseResult) {
	t.Helper()
	var envelope cli.ResultEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v\nstdout=%s", err, stdout.String())
	}
	resultBytes, err := json.Marshal(envelope.Result)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	var result workspacerelease.RequestWorkspaceSetReleaseResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("decode RequestWorkspaceSetReleaseResult: %v", err)
	}
	return envelope, result
}

// releaseArgs builds `workspace-set release`'s own flag arguments — flags
// must precede the positional <familyId> argument (see
// internal/delivery/cli/workspace/reconcile_test.go's own releaseArgs
// sibling, reconcileArgs, for why).
func releaseArgs(sw seededWorkspace, expectedVersion, idempotencyKey string) []string {
	args := []string{"--project-id", sw.projectID}
	if expectedVersion != "" {
		args = append(args, "--expected-version", expectedVersion)
	}
	args = append(args, "--idempotency-key", idempotencyKey, sw.familyID)
	return args
}

func TestWorkspaceSetRelease_HappyPath_EnqueuesRealJob(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")
	env.sealReleaseSet(t, sw)

	var stdout, stderr bytes.Buffer
	args := releaseArgs(sw, "1", "key-1")
	if err := cliworkspace.RunWorkspaceSetRelease(context.Background(), env.deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunWorkspaceSetRelease: %v, stderr=%s", err, stderr.String())
	}

	envelope, result := decodeReleaseResult(t, &stdout)
	if envelope.Replayed {
		t.Fatal("fresh release reported Replayed=true, want false")
	}
	if result.ReleaseJobID == "" {
		t.Fatal("ReleaseJobID is empty")
	}

	var hasJob bool
	if err := env.uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		hasJob, err = tx.Jobs().HasActiveJobForAggregateIDs(context.Background(), []string{sw.workspaceSetID})
		return err
	}); err != nil {
		t.Fatalf("HasActiveJobForAggregateIDs: %v", err)
	}
	if !hasJob {
		t.Fatal("expected a real, active WORKSPACE_SET_RELEASE job for the workspace set's own AggregateID, found none")
	}
}

// TestWorkspaceSetRelease_Replay_ReturnsIdenticalJobID proves the "replay"
// Verify bullet: the exact same --idempotency-key dispatched twice returns
// the exact same releaseJobId both times — idsource.Random{} means a
// genuinely-minted second job would almost certainly produce a different
// id, mirroring internal/delivery/httpapi/workspace_test.go's own
// TestRequestWorkspaceSetRelease_Replay_ReturnsIdenticalJobID.
func TestWorkspaceSetRelease_Replay_ReturnsIdenticalJobID(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")
	env.sealReleaseSet(t, sw)
	args := releaseArgs(sw, "1", "key-1")

	var stdout1, stderr1 bytes.Buffer
	if err := cliworkspace.RunWorkspaceSetRelease(context.Background(), env.deps, args, &stdout1, &stderr1); err != nil {
		t.Fatalf("RunWorkspaceSetRelease (first): %v, stderr=%s", err, stderr1.String())
	}
	_, first := decodeReleaseResult(t, &stdout1)

	var stdout2, stderr2 bytes.Buffer
	if err := cliworkspace.RunWorkspaceSetRelease(context.Background(), env.deps, args, &stdout2, &stderr2); err != nil {
		t.Fatalf("RunWorkspaceSetRelease (replay): %v, stderr=%s", err, stderr2.String())
	}
	envelope2, second := decodeReleaseResult(t, &stdout2)
	if !envelope2.Replayed {
		t.Fatal("retry with identical idempotency key reported Replayed=false, want true")
	}
	if second.ReleaseJobID != first.ReleaseJobID {
		t.Fatalf("replayed ReleaseJobID = %q, want the exact original %q", second.ReleaseJobID, first.ReleaseJobID)
	}
}

// TestWorkspaceSetRelease_QuarantinedRepository_Blocked is this task's own
// "quarantine" Verify bullet: a real
// ports.WorkspaceLifecycle.QuarantineRepositoryWorkspace transition (never
// a fabricated row) blocks release.
func TestWorkspaceSetRelease_QuarantinedRepository_Blocked(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")
	env.sealReleaseSet(t, sw)

	if err := env.store.QuarantineRepositoryWorkspace(context.Background(), ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(sw.repositoryWorkspaceID), ExpectedVersion: 1,
		Reason: "test: simulated lease-losing writer", EventID: "evt-quarantine-1",
	}); err != nil {
		t.Fatalf("QuarantineRepositoryWorkspace: %v", err)
	}

	var stdout, stderr bytes.Buffer
	args := releaseArgs(sw, "1", "key-1")
	err := cliworkspace.RunWorkspaceSetRelease(context.Background(), env.deps, args, &stdout, &stderr)
	if !errors.Is(err, workspacerelease.ErrWorkspaceSetHasQuarantinedRepository) {
		t.Fatalf("RunWorkspaceSetRelease error = %v, want ErrWorkspaceSetHasQuarantinedRepository", err)
	}
}

// TestWorkspaceSetRelease_ActiveWriteLease_Blocked is this task's own
// "lease" Verify bullet: a real write lease
// (sqlite.SeedFixtureWriteLease drives the real EnqueueJob/ClaimJob/
// AcquireWriteLeases path) blocks release.
func TestWorkspaceSetRelease_ActiveWriteLease_Blocked(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")
	env.sealReleaseSet(t, sw)

	if err := sqlite.SeedFixtureWriteLease(context.Background(), env.store, sw.projectID, sw.familyID, sw.workItemID, sw.repositoryID, sw.repositoryWorkspaceID, 1); err != nil {
		t.Fatalf("SeedFixtureWriteLease: %v", err)
	}

	var stdout, stderr bytes.Buffer
	args := releaseArgs(sw, "1", "key-1")
	err := cliworkspace.RunWorkspaceSetRelease(context.Background(), env.deps, args, &stdout, &stderr)
	if !errors.Is(err, workspacerelease.ErrWorkspaceSetHasActiveWriteLease) {
		t.Fatalf("RunWorkspaceSetRelease error = %v, want ErrWorkspaceSetHasActiveWriteLease", err)
	}
}

// TestWorkspaceSetRelease_NotAuthorized_Blocked is GC-INV-26's own refusal:
// no ReleaseSet exists for this family at all, so work.EligibilityAuthority
// (the real port, no fake) reports unauthorized.
func TestWorkspaceSetRelease_NotAuthorized_Blocked(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")

	var stdout, stderr bytes.Buffer
	args := releaseArgs(sw, "1", "key-1")
	err := cliworkspace.RunWorkspaceSetRelease(context.Background(), env.deps, args, &stdout, &stderr)
	if !errors.Is(err, workspacerelease.ErrReleaseNotAuthorized) {
		t.Fatalf("RunWorkspaceSetRelease error = %v, want ErrReleaseNotAuthorized", err)
	}
}

// TestWorkspaceSetRelease_StaleExpectedVersion_Rejected is this task's own
// "stale" Verify bullet.
func TestWorkspaceSetRelease_StaleExpectedVersion_Rejected(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")
	env.sealReleaseSet(t, sw)

	var stdout, stderr bytes.Buffer
	args := releaseArgs(sw, "99", "key-1")
	err := cliworkspace.RunWorkspaceSetRelease(context.Background(), env.deps, args, &stdout, &stderr)
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("RunWorkspaceSetRelease error = %v, want ports.ErrOptimisticConflict", err)
	}
}

func TestWorkspaceSetRelease_ZeroExpectedVersion_ReturnsUsageError(t *testing.T) {
	env := newReleaseTestEnv(t)
	sw := env.seedReadyWorkspace(t, "1")

	var stdout, stderr bytes.Buffer
	args := releaseArgs(sw, "", "key-1")
	err := cliworkspace.RunWorkspaceSetRelease(context.Background(), env.deps, args, &stdout, &stderr)
	if !cli.IsUsageError(err) {
		t.Fatalf("RunWorkspaceSetRelease error = %v, want a cli.UsageError for a missing --expected-version", err)
	}
}
