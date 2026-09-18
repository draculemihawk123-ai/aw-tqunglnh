package releaseset_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliReleaseSet "github.com/taQuangLing/agent-workflow/internal/delivery/cli/releaseset"
)

func TestRunSeal_SealsReleaseSet(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, _, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	args := []string{"--expected-version", "1", "--idempotency-key", "key-seal-1", rs.ReleaseSetID}
	if err := cliReleaseSet.RunSeal(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunSeal() error = %v, stderr = %s", err, stderr.String())
	}
	result := decodeReleaseSetResult(t, &stdout)
	if result.State != "SEALED" || result.Version != 2 {
		t.Fatalf("result = %+v, want State=SEALED Version=2", result)
	}
}

// TestRunSeal_ReplaySameIdempotencyKey_ReturnsIdenticalResult is this
// task's own "replay" Verify bullet applied to seal.
func TestRunSeal_ReplaySameIdempotencyKey_ReturnsIdenticalResult(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, _, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	args := []string{"--expected-version", "1", "--idempotency-key", "key-seal-replay", rs.ReleaseSetID}

	var first bytes.Buffer
	if err := cliReleaseSet.RunSeal(context.Background(), deps, args, &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunSeal() error = %v", err)
	}
	if replayedField(t, first.String()) {
		t.Fatal("first call reported Replayed = true, want false")
	}

	var second bytes.Buffer
	if err := cliReleaseSet.RunSeal(context.Background(), deps, args, &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second (replay) RunSeal() error = %v", err)
	}
	if !replayedField(t, second.String()) {
		t.Fatal("second call reported Replayed = false, want true")
	}
	firstResult := decodeReleaseSetResult(t, &first)
	secondResult := decodeReleaseSetResult(t, &second)
	if secondResult != firstResult {
		t.Fatalf("replay result = %+v, want the exact original %+v", secondResult, firstResult)
	}
}

// TestRunSeal_StaleExpectedVersion_IsOptimisticConflict is this task's own
// "stale" Verify bullet: a stale --expected-version is rejected with a
// typed conflict, never silently overwritten (or worse, silently sealed).
func TestRunSeal_StaleExpectedVersion_IsOptimisticConflict(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, _, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	args := []string{"--expected-version", "99", rs.ReleaseSetID}
	err := cliReleaseSet.RunSeal(context.Background(), deps, args, &stdout, &stderr)
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("RunSeal() with a stale --expected-version returned %v, want errors.Is(..., ports.ErrOptimisticConflict)", err)
	}

	detail, getErr := workapp.GetReleaseSet(context.Background(), u, rs.ReleaseSetID)
	if getErr != nil {
		t.Fatalf("reload release set: %v", getErr)
	}
	if detail.State != "CREATED" || detail.Version != 1 {
		t.Fatalf("release set after a rejected stale seal = %+v, want unchanged State=CREATED Version=1", detail)
	}
}

func TestRunSeal_MissingExpectedVersion_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, _, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	err := cliReleaseSet.RunSeal(context.Background(), deps, []string{rs.ReleaseSetID}, &stdout, &stderr)
	if !cli.IsUsageError(err) {
		t.Fatalf("RunSeal() with no --expected-version returned %v, want a cli.UsageError", err)
	}
}

func TestRunSeal_UnknownID_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	args := []string{"--expected-version", "1", "no-such-release-set"}
	if err := cliReleaseSet.RunSeal(context.Background(), deps, args, &stdout, &stderr); err == nil {
		t.Fatal("RunSeal() with an unknown id succeeded, want an error")
	}
}

// TestRunSeal_AlreadySealed_IsRejected proves a real, duplicate seal
// attempt (not a receipt replay — a fresh idempotency key) against an
// already-terminal ReleaseSet is rejected, never silently re-sealed.
func TestRunSeal_AlreadySealed_IsRejected(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, _, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	firstArgs := []string{"--expected-version", "1", "--idempotency-key", "key-seal-first", rs.ReleaseSetID}
	var first bytes.Buffer
	if err := cliReleaseSet.RunSeal(context.Background(), deps, firstArgs, &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunSeal() error = %v", err)
	}

	secondArgs := []string{"--expected-version", "2", "--idempotency-key", "key-seal-second", rs.ReleaseSetID}
	var second, stderr bytes.Buffer
	if err := cliReleaseSet.RunSeal(context.Background(), deps, secondArgs, &second, &stderr); err == nil {
		t.Fatal("second, distinct RunSeal() on an already-SEALED release set succeeded, want an error")
	}
}
