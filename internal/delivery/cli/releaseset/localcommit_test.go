package releaseset_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliReleaseSet "github.com/taQuangLing/agent-workflow/internal/delivery/cli/releaseset"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func decodeLocalCommitResult(t *testing.T, stdout *bytes.Buffer) cliReleaseSet.LocalCommitResult {
	t.Helper()
	var result cliReleaseSet.LocalCommitResult
	if err := json.Unmarshal([]byte(resultField(t, stdout.String())), &result); err != nil {
		t.Fatalf("decode LocalCommitResult: %v", err)
	}
	return result
}

func localCommitArgs(projectID, releaseSetID string, expectedReleaseSetVersion uint64, workspaceID string, expectedWorkspaceVersion uint64, idemKey string) []string {
	return []string{
		"--project-id", projectID, "--release-set-id", releaseSetID,
		"--expected-release-set-version", strconv.FormatUint(expectedReleaseSetVersion, 10),
		"--repository-workspace-id", workspaceID,
		"--expected-workspace-version", strconv.FormatUint(expectedWorkspaceVersion, 10),
		"--message", "release commit", "--author-name", "Release Bot", "--author-email", "release-bot@example.invalid",
		"--idempotency-key", idemKey,
	}
}

func TestRunLocalCommit_RequestsLocalCommit_StateRequested(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, rw, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	args := localCommitArgs("project-1", rs.ReleaseSetID, rs.Version, string(rw.ID), rw.Version, "key-local-commit-1")
	if err := cliReleaseSet.RunLocalCommit(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunLocalCommit() error = %v, stderr = %s", err, stderr.String())
	}
	result := decodeLocalCommitResult(t, &stdout)
	if result.State != "REQUESTED" || result.ReleaseSetLocalCommitID == "" || result.JobID == "" {
		t.Fatalf("result = %+v, want State=REQUESTED and non-empty ReleaseSetLocalCommitID/JobID", result)
	}
	if result.ReleaseSetID != rs.ReleaseSetID || result.RepositoryWorkspaceID != string(rw.ID) {
		t.Fatalf("result = %+v, want ReleaseSetID=%s RepositoryWorkspaceID=%s", result, rs.ReleaseSetID, rw.ID)
	}
	if result.Wait != nil {
		t.Fatalf("result.Wait = %+v, want nil (no --wait was requested)", result.Wait)
	}
}

// TestRunLocalCommit_ReplaySameIdempotencyKey_ReturnsIdenticalResult is this
// task's own "replay" Verify bullet applied to local-commit.
func TestRunLocalCommit_ReplaySameIdempotencyKey_ReturnsIdenticalResult(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, rw, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	args := localCommitArgs("project-1", rs.ReleaseSetID, rs.Version, string(rw.ID), rw.Version, "key-local-commit-replay")

	var first bytes.Buffer
	if err := cliReleaseSet.RunLocalCommit(context.Background(), deps, args, &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunLocalCommit() error = %v", err)
	}
	if replayedField(t, first.String()) {
		t.Fatal("first call reported Replayed = true, want false")
	}
	firstResult := decodeLocalCommitResult(t, &first)

	var second bytes.Buffer
	if err := cliReleaseSet.RunLocalCommit(context.Background(), deps, args, &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second (replay) RunLocalCommit() error = %v", err)
	}
	if !replayedField(t, second.String()) {
		t.Fatal("second call reported Replayed = false, want true")
	}
	secondResult := decodeLocalCommitResult(t, &second)
	if secondResult.ReleaseSetLocalCommitID != firstResult.ReleaseSetLocalCommitID || secondResult.JobID != firstResult.JobID {
		t.Fatalf("replay result = %+v, want the exact original %+v", secondResult, firstResult)
	}
}

// TestRunLocalCommit_StaleExpectedReleaseSetVersion_IsOptimisticConflict is
// this task's own "stale" Verify bullet, applied to the ReleaseSet half of
// local-commit's own two exact-revision fences.
func TestRunLocalCommit_StaleExpectedReleaseSetVersion_IsOptimisticConflict(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, rw, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	args := localCommitArgs("project-1", rs.ReleaseSetID, rs.Version+1, string(rw.ID), rw.Version, "key-stale-release-set")
	err := cliReleaseSet.RunLocalCommit(context.Background(), deps, args, &stdout, &stderr)
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("RunLocalCommit() with a stale --expected-release-set-version returned %v, want errors.Is(..., ports.ErrOptimisticConflict)", err)
	}
}

// TestRunLocalCommit_StaleExpectedWorkspaceVersion_IsOptimisticConflict is
// the other half of the "stale" Verify bullet: the RepositoryWorkspace
// generation/fence.
func TestRunLocalCommit_StaleExpectedWorkspaceVersion_IsOptimisticConflict(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, rw, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	args := localCommitArgs("project-1", rs.ReleaseSetID, rs.Version, string(rw.ID), rw.Version+1, "key-stale-workspace")
	err := cliReleaseSet.RunLocalCommit(context.Background(), deps, args, &stdout, &stderr)
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("RunLocalCommit() with a stale --expected-workspace-version returned %v, want errors.Is(..., ports.ErrOptimisticConflict)", err)
	}
}

// TestRunLocalCommit_Wait_ObservesFailedState is this task's own
// "crash-after-Git" Verify bullet: prove --wait polling correctly observes
// a FAILED state (WORKSPACE_QUARANTINED) without itself corrupting
// anything or retrying the mutation. Mirrors
// internal/delivery/cli/run/start_test.go's own
// TestRunStart_Wait_ObservesTerminalState technique exactly: force the
// terminal row directly (the real worker's own crash-recovery logic is
// already proven in internal/app/releasesetcommit's own execute_test.go —
// this test's only job is to prove the CLI's own --wait polling observes
// it correctly), then re-issue the SAME idempotency key with --wait so the
// request itself replays instantly and the FIRST observe call already
// finds the forced terminal state — deps.Sleep fails the test if it is
// ever called, proving zero delay and zero retry of the mutation.
func TestRunLocalCommit_Wait_ObservesFailedState(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, rw, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	args := localCommitArgs("project-1", rs.ReleaseSetID, rs.Version, string(rw.ID), rw.Version, "key-local-commit-wait-failed")

	var setup bytes.Buffer
	if err := cliReleaseSet.RunLocalCommit(context.Background(), deps, args, &setup, &bytes.Buffer{}); err != nil {
		t.Fatalf("setup RunLocalCommit(): %v", err)
	}
	setupResult := decodeLocalCommitResult(t, &setup)

	if err := forceLocalCommitFailed(u, setupResult.ReleaseSetLocalCommitID, workdomain.FailureWorkspaceQuarantined); err != nil {
		t.Fatalf("forceLocalCommitFailed: %v", err)
	}

	deps.Sleep = func(ctx context.Context, d time.Duration) error {
		t.Fatal("Wait should have observed a terminal state on its first poll and never needed to sleep")
		return nil
	}

	waitArgs := append(append([]string{}, args...), "--wait", "--wait-timeout", "0s")
	var stdout, stderr bytes.Buffer
	if err := cliReleaseSet.RunLocalCommit(context.Background(), deps, waitArgs, &stdout, &stderr); err != nil {
		t.Fatalf("RunLocalCommit(--wait) error = %v, stderr = %s", err, stderr.String())
	}
	if !replayedField(t, stdout.String()) {
		t.Fatal("RunLocalCommit(--wait) with the same idempotency key reported Replayed = false, want true (must never re-dispatch the mutation)")
	}
	result := decodeLocalCommitResult(t, &stdout)
	if result.Wait == nil {
		t.Fatal("result.Wait is nil, want a populated terminal ReleaseSetLocalCommitStatus observation")
	}
	if result.Wait.State != "FAILED" || result.Wait.FailureReason != "WORKSPACE_QUARANTINED" {
		t.Fatalf("result.Wait = %+v, want State=FAILED FailureReason=WORKSPACE_QUARANTINED", result.Wait)
	}
}

// TestRunLocalCommit_Wait_ObservesCommittedState mirrors the FAILED test
// above for the symmetric COMMITTED outcome.
func TestRunLocalCommit_Wait_ObservesCommittedState(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, rw, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	args := localCommitArgs("project-1", rs.ReleaseSetID, rs.Version, string(rw.ID), rw.Version, "key-local-commit-wait-committed")

	var setup bytes.Buffer
	if err := cliReleaseSet.RunLocalCommit(context.Background(), deps, args, &setup, &bytes.Buffer{}); err != nil {
		t.Fatalf("setup RunLocalCommit(): %v", err)
	}
	setupResult := decodeLocalCommitResult(t, &setup)

	if err := forceLocalCommitCommitted(u, setupResult.ReleaseSetLocalCommitID); err != nil {
		t.Fatalf("forceLocalCommitCommitted: %v", err)
	}

	waitArgs := append(append([]string{}, args...), "--wait", "--wait-timeout", "0s")
	var stdout, stderr bytes.Buffer
	if err := cliReleaseSet.RunLocalCommit(context.Background(), deps, waitArgs, &stdout, &stderr); err != nil {
		t.Fatalf("RunLocalCommit(--wait) error = %v, stderr = %s", err, stderr.String())
	}
	result := decodeLocalCommitResult(t, &stdout)
	if result.Wait == nil {
		t.Fatal("result.Wait is nil, want a populated terminal ReleaseSetLocalCommitStatus observation")
	}
	if result.Wait.State != "COMMITTED" || result.Wait.ResultVCSObjectID != "committed-1" {
		t.Fatalf("result.Wait = %+v, want State=COMMITTED ResultVCSObjectID=committed-1", result.Wait)
	}
}

func TestRunLocalCommit_MissingMessage_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	args := []string{
		"--project-id", "project-1", "--release-set-id", "rs-1", "--expected-release-set-version", "1",
		"--repository-workspace-id", "rw-1", "--expected-workspace-version", "1",
		"--author-name", "Release Bot", "--author-email", "release-bot@example.invalid",
	}
	err := cliReleaseSet.RunLocalCommit(context.Background(), deps, args, &stdout, &stderr)
	if !cli.IsUsageError(err) {
		t.Fatalf("RunLocalCommit() with no --message returned %v, want a cli.UsageError", err)
	}
}

func TestRunLocalCommit_MissingExpectedReleaseSetVersion_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	args := []string{
		"--project-id", "project-1", "--release-set-id", "rs-1",
		"--repository-workspace-id", "rw-1", "--expected-workspace-version", "1",
		"--message", "m", "--author-name", "Release Bot", "--author-email", "release-bot@example.invalid",
	}
	err := cliReleaseSet.RunLocalCommit(context.Background(), deps, args, &stdout, &stderr)
	if !cli.IsUsageError(err) {
		t.Fatalf("RunLocalCommit() with no --expected-release-set-version returned %v, want a cli.UsageError", err)
	}
}
