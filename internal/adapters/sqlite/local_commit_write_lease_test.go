package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func newLocalCommitWriteLeaseFixture(t *testing.T) (*Store, ports.LocalCommitWriteLeaseTarget) {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "local-commit-write-lease.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-root"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureRepositoryWorkspace(ctx, store, "project-1", "family-1", "set-1", "repo-1", "rw-1"); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}
	return store, ports.LocalCommitWriteLeaseTarget{RepositoryID: "repo-1", RepositoryWorkspaceID: "rw-1", Generation: 1}
}

func claimAnyJob(t *testing.T, store *Store, owner string, ttl time.Duration) ports.JobLease {
	t.Helper()
	ctx := context.Background()
	jobID := ports.JobID("job-" + owner)
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: jobID, ProjectID: "project-1", Kind: "PROBE", AggregateType: "Probe", AggregateID: string(jobID),
		MaxClaims: 1, IdempotencyKey: string(jobID),
	}); err != nil {
		t.Fatalf("EnqueueJob(%s): %v", owner, err)
	}
	job, _, err := store.ClaimJob(ctx, owner, ttl)
	if err != nil {
		t.Fatalf("ClaimJob(%s): %v", owner, err)
	}
	return ports.JobLease{JobID: job.ID, Owner: job.LeaseOwner, Token: job.LeaseToken}
}

// TestAcquireLocalCommitWriteLease_TwoWorkers_OnlyOneSucceeds is V6-10E's
// own "two workers" Verify-line scenario: two genuinely concurrent,
// distinct job leases racing to acquire the SAME local-commit write-lease
// target — exactly one must win, the other must observe
// ErrLocalCommitWriteLeaseConflict, never both silently granted.
func TestAcquireLocalCommitWriteLease_TwoWorkers_OnlyOneSucceeds(t *testing.T) {
	store, target := newLocalCommitWriteLeaseFixture(t)
	leaseA := claimAnyJob(t, store, "worker-a", 10*time.Minute)
	leaseB := claimAnyJob(t, store, "worker-b", 10*time.Minute)

	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, results[0] = store.AcquireLocalCommitWriteLease(context.Background(), ports.AcquireLocalCommitWriteLeaseRequest{
			JobLease: leaseA, Target: target, TTL: time.Minute,
		})
	}()
	go func() {
		defer wg.Done()
		_, results[1] = store.AcquireLocalCommitWriteLease(context.Background(), ports.AcquireLocalCommitWriteLeaseRequest{
			JobLease: leaseB, Target: target, TTL: time.Minute,
		})
	}()
	wg.Wait()

	successes, conflicts := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ports.ErrLocalCommitWriteLeaseConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error racing for the write lease: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want exactly one of each", successes, conflicts)
	}
}

// TestValidateLocalCommitWriteLease_Stolen_ReturnsLost proves a grant whose
// own fence_token has since been superseded (a later Acquire call, after
// this grant's own TTL lapsed) is correctly reported as lost — the exact
// check a finalize path composes before ever trusting a grant it acquired
// earlier.
func TestValidateLocalCommitWriteLease_Stolen_ReturnsLost(t *testing.T) {
	store, target := newLocalCommitWriteLeaseFixture(t)
	ctx := context.Background()
	leaseA := claimAnyJob(t, store, "worker-a", 10*time.Minute)

	grant, err := store.AcquireLocalCommitWriteLease(ctx, ports.AcquireLocalCommitWriteLeaseRequest{
		JobLease: leaseA, Target: target, TTL: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("acquire (worker A): %v", err)
	}

	time.Sleep(120 * time.Millisecond)
	leaseB := claimAnyJob(t, store, "worker-b", 10*time.Minute)
	if _, err := store.AcquireLocalCommitWriteLease(ctx, ports.AcquireLocalCommitWriteLeaseRequest{
		JobLease: leaseB, Target: target, TTL: 10 * time.Minute,
	}); err != nil {
		t.Fatalf("acquire (worker B, stealing): %v", err)
	}

	if err := store.ValidateLocalCommitWriteLease(ctx, grant); !errors.Is(err, ports.ErrLocalCommitWriteLeaseLost) {
		t.Fatalf("ValidateLocalCommitWriteLease(stolen grant) error = %v, want ErrLocalCommitWriteLeaseLost", err)
	}
}

// TestReleaseLocalCommitWriteLease_Idempotent proves a second release of
// the same grant is reported as ErrLocalCommitWriteLeaseLost, never a
// silent success or a double-release of someone else's now-current lease —
// mirrors ReleaseWriteLeases' own identical idempotency discipline
// (scheduling.go).
func TestReleaseLocalCommitWriteLease_Idempotent(t *testing.T) {
	store, target := newLocalCommitWriteLeaseFixture(t)
	ctx := context.Background()
	lease := claimAnyJob(t, store, "worker-a", 10*time.Minute)

	grant, err := store.AcquireLocalCommitWriteLease(ctx, ports.AcquireLocalCommitWriteLeaseRequest{
		JobLease: lease, Target: target, TTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := store.ReleaseLocalCommitWriteLease(ctx, grant); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := store.ReleaseLocalCommitWriteLease(ctx, grant); !errors.Is(err, ports.ErrLocalCommitWriteLeaseLost) {
		t.Fatalf("second release error = %v, want ErrLocalCommitWriteLeaseLost", err)
	}
}
