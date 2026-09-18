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

func TestRunAbandon_AbandonsReleaseSet(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, _, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	args := []string{"--expected-version", "1", "--idempotency-key", "key-abandon-1", rs.ReleaseSetID}
	if err := cliReleaseSet.RunAbandon(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunAbandon() error = %v, stderr = %s", err, stderr.String())
	}
	result := decodeReleaseSetResult(t, &stdout)
	if result.State != "ABANDONED" || result.Version != 2 {
		t.Fatalf("result = %+v, want State=ABANDONED Version=2", result)
	}
}

// TestRunAbandon_ReplaySameIdempotencyKey_ReturnsIdenticalResult is this
// task's own "replay" Verify bullet applied to abandon.
func TestRunAbandon_ReplaySameIdempotencyKey_ReturnsIdenticalResult(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, _, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	args := []string{"--expected-version", "1", "--idempotency-key", "key-abandon-replay", rs.ReleaseSetID}

	var first bytes.Buffer
	if err := cliReleaseSet.RunAbandon(context.Background(), deps, args, &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunAbandon() error = %v", err)
	}
	if replayedField(t, first.String()) {
		t.Fatal("first call reported Replayed = true, want false")
	}

	var second bytes.Buffer
	if err := cliReleaseSet.RunAbandon(context.Background(), deps, args, &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second (replay) RunAbandon() error = %v", err)
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

// TestRunAbandon_StaleExpectedVersion_IsOptimisticConflict is this task's
// own "stale" Verify bullet applied to abandon.
func TestRunAbandon_StaleExpectedVersion_IsOptimisticConflict(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, _, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	args := []string{"--expected-version", "99", rs.ReleaseSetID}
	err := cliReleaseSet.RunAbandon(context.Background(), deps, args, &stdout, &stderr)
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("RunAbandon() with a stale --expected-version returned %v, want errors.Is(..., ports.ErrOptimisticConflict)", err)
	}

	detail, getErr := workapp.GetReleaseSet(context.Background(), u, rs.ReleaseSetID)
	if getErr != nil {
		t.Fatalf("reload release set: %v", getErr)
	}
	if detail.State != "CREATED" || detail.Version != 1 {
		t.Fatalf("release set after a rejected stale abandon = %+v, want unchanged State=CREATED Version=1", detail)
	}
}

func TestRunAbandon_MissingExpectedVersion_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	_, _, rs := releaseSetFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	err := cliReleaseSet.RunAbandon(context.Background(), deps, []string{rs.ReleaseSetID}, &stdout, &stderr)
	if !cli.IsUsageError(err) {
		t.Fatalf("RunAbandon() with no --expected-version returned %v, want a cli.UsageError", err)
	}
}
