package adapterbuild_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
)

// TestProbeAdapterBuild_ConcurrentSameKey_AllCallersGetIdenticalToken is
// the "concurrency" Verify bullet: N goroutines racing the exact same
// (Actor, Scope, IdempotencyKey, Type, RequestHash) against the real
// SQLite UnitOfWork (the in-memory fake's own WithSerializedWrite unlocks
// before running fn and so cannot exercise genuine writer contention —
// the same reasoning internal/app/catalog/commands_sqlite_test.go's own
// concurrent test gives) must all observe the SAME persisted candidate
// token — never a losing writer's own freshly-signed (and therefore
// differently-nonced) phantom token that never actually got stored.
func TestProbeAdapterBuild_ConcurrentSameKey_AllCallersGetIdenticalToken(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-adapterbuild-probe-concurrent.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)

	path := writeExecutable(t, "binary-content-v1")
	req := probeRequest(path)
	cmd := probeCommand("probe-race", "hash-race")

	const writers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	tokens := make([]string, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, cmd, req)
			errs[i] = err
			if err == nil {
				tokens[i] = token.Signature + "|" + token.Nonce + "|" + token.ExpiresAt.String()
			}
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	for i := 1; i < writers; i++ {
		if tokens[i] != tokens[0] {
			t.Fatalf("writer %d got a different persisted token than writer 0:\nwriter 0: %s\nwriter %d: %s", i, tokens[0], i, tokens[i])
		}
	}

	count, err := store.CountCommandReceiptsByIdempotencyKey(ctx, "probe-race")
	if err != nil {
		t.Fatalf("CountCommandReceiptsByIdempotencyKey: %v", err)
	}
	if count != 1 {
		t.Fatalf("command_receipts rows for idempotency key = %d, want exactly 1", count)
	}
}

// TestRegisterAdapterBuild_ConcurrentSameKey_ExactlyOneBuildOneEvent is
// the "concurrency" Verify bullet for RegisterAdapterBuild: N goroutines
// racing the exact same command envelope must all observe the same
// RegisterResult, the registry must end up with exactly one row, and
// AdapterBuildRegistered must have been appended exactly once — never
// once per racing writer.
func TestRegisterAdapterBuild_ConcurrentSameKey_ExactlyOneBuildOneEvent(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-adapterbuild-register-concurrent.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)

	path := writeExecutable(t, "binary-content-v1")
	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-1", "hash-1"), probeRequest(path))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	cmd := registerCommand("register-race", "hash-race", "operator-race")

	const writers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	buildIDs := make([]string, writers)
	alreadyExisted := make([]bool, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := adapterbuild.RegisterAdapterBuild(ctx, uow, cmd, adapterbuild.RegisterRequest{
				Token: token, CapabilityManifest: validManifest(),
			})
			errs[i] = err
			if err == nil {
				buildIDs[i] = result.Build.ID()
				alreadyExisted[i] = result.AlreadyExisted
			}
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	for i := 1; i < writers; i++ {
		if buildIDs[i] != buildIDs[0] {
			t.Fatalf("writer %d got build id %q, want %q (same as writer 0)", i, buildIDs[i], buildIDs[0])
		}
		if alreadyExisted[i] != alreadyExisted[0] {
			t.Fatalf("writer %d AlreadyExisted=%v, want %v (same as writer 0) — every racer must see the same persisted receipt", i, alreadyExisted[i], alreadyExisted[0])
		}
	}

	builds, err := adapterbuild.ListAdapterBuilds(ctx, uow)
	if err != nil {
		t.Fatalf("ListAdapterBuilds: %v", err)
	}
	if len(builds) != 1 {
		t.Fatalf("len(builds) = %d, want exactly 1", len(builds))
	}

	eventCount, err := store.CountDomainEvents(ctx, "AdapterBuildVersion", buildIDs[0])
	if err != nil {
		t.Fatalf("CountDomainEvents: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("AdapterBuildRegistered event count for build %s = %d, want exactly 1", buildIDs[0], eventCount)
	}

	receiptCount, err := store.CountCommandReceiptsByIdempotencyKey(ctx, "register-race")
	if err != nil {
		t.Fatalf("CountCommandReceiptsByIdempotencyKey: %v", err)
	}
	if receiptCount != 1 {
		t.Fatalf("command_receipts rows for idempotency key = %d, want exactly 1", receiptCount)
	}
}

// TestRegisterAdapterBuild_ReplayAfterCommit_SimulatesCrashBeforeAck is
// the "crash-after-commit" Verify bullet, mirroring
// internal/app/catalog/commands_sqlite_test.go's own real-SQLite pattern:
// a first RegisterAdapterBuild call commits for real (build row + event +
// receipt), and a caller that never saw the response (a crash between
// commit and ack) simply calls again with the identical command envelope.
// The retry must return the exact original result without re-verifying
// the token or re-touching the filesystem — proven here by deleting the
// executable and mutating the on-disk token's own bound path before the
// retry: if RegisterAdapterBuild's replay path fell through to a real
// re-probe, it would fail loudly instead of succeeding.
func TestRegisterAdapterBuild_ReplayAfterCommit_SimulatesCrashBeforeAck(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-adapterbuild-register-crash.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)

	path := writeExecutable(t, "binary-content-v1")
	probeToken, err := adapterbuild.ProbeAdapterBuild(ctx, uow, probeCommand("probe-1", "hash-1"), probeRequest(path))
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	cmd := registerCommand("register-1", "hash-1", "operator-1")

	first, err := adapterbuild.RegisterAdapterBuild(ctx, uow, cmd, adapterbuild.RegisterRequest{
		Token: probeToken, CapabilityManifest: validManifest(),
	})
	if err != nil {
		t.Fatalf("first register (the one that actually commits): %v", err)
	}

	// Simulate the world moving on after the commit but before the caller
	// ever saw the response: the executable is now gone.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove executable: %v", err)
	}

	second, err := adapterbuild.RegisterAdapterBuild(ctx, uow, cmd, adapterbuild.RegisterRequest{
		Token: probeToken, CapabilityManifest: validManifest(),
	})
	if err != nil {
		t.Fatalf("retry after simulated crash-before-ack should replay without re-touching the now-deleted executable: %v", err)
	}
	if second.Build.ID() != first.Build.ID() {
		t.Fatalf("replayed build id = %s, want %s", second.Build.ID(), first.Build.ID())
	}
	if second.AlreadyExisted != first.AlreadyExisted {
		t.Fatalf("replayed AlreadyExisted = %v, want %v", second.AlreadyExisted, first.AlreadyExisted)
	}
	if second.Build.RegisteredBy() != first.Build.RegisteredBy() {
		t.Fatalf("replayed RegisteredBy = %q, want %q", second.Build.RegisteredBy(), first.Build.RegisteredBy())
	}

	eventCount, err := store.CountDomainEvents(ctx, "AdapterBuildVersion", first.Build.ID())
	if err != nil {
		t.Fatalf("CountDomainEvents: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("AdapterBuildRegistered event count = %d, want exactly 1 (replay must never re-append)", eventCount)
	}
}

// TestProbeAdapterBuild_ReplayAfterCommit_SimulatesCrashBeforeAck is the
// "crash-after-commit" Verify bullet for ProbeAdapterBuild itself: a
// retry of the identical probe command after the first one already
// committed must replay the exact original candidate.
func TestProbeAdapterBuild_ReplayAfterCommit_SimulatesCrashBeforeAck(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-adapterbuild-probe-crash.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)

	path := writeExecutable(t, "binary-content-v1")
	cmd := probeCommand("probe-1", "hash-1")
	req := probeRequest(path)

	first, err := adapterbuild.ProbeAdapterBuild(ctx, uow, cmd, req)
	if err != nil {
		t.Fatalf("first probe (the one that actually commits): %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove executable: %v", err)
	}

	second, err := adapterbuild.ProbeAdapterBuild(ctx, uow, cmd, req)
	if err != nil {
		t.Fatalf("retry after simulated crash-before-ack should replay without re-touching the now-deleted executable: %v", err)
	}
	if second != first {
		t.Fatalf("replayed token differs from the original:\nfirst:  %+v\nsecond: %+v", first, second)
	}
}
