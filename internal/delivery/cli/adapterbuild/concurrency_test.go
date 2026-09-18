package adapterbuild_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	cliadapterbuild "github.com/taQuangLing/agent-workflow/internal/delivery/cli/adapterbuild"
)

// sqliteDeps builds a cliadapterbuild.Dependencies over a REAL, on-disk
// sqlite.UnitOfWork — the fake in-memory UnitOfWork's own WithSerializedWrite
// unlocks before running its closure and so cannot exercise genuine writer
// contention (the same reasoning internal/app/adapterbuild/
// commands_sqlite_test.go's own doc comment gives for its identical choice).
func sqliteDeps(t *testing.T, name string) cliadapterbuild.Dependencies {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	return cliadapterbuild.Dependencies{
		UoW: sqlite.NewUnitOfWork(store), IDs: idsource.NewSequential("adapterbuild-concurrent"),
		Now: func() time.Time { return now },
	}
}

type registerOutcome struct {
	buildID        string
	alreadyExisted bool
}

func decodeRegisterOutcome(t *testing.T, stdout []byte) registerOutcome {
	t.Helper()
	var envelope struct {
		Result struct {
			Build struct {
				ID string `json:"id"`
			} `json:"build"`
			AlreadyExisted bool `json:"alreadyExisted"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout, &envelope); err != nil {
		t.Fatalf("decode register stdout %q: %v", string(stdout), err)
	}
	return registerOutcome{buildID: envelope.Result.Build.ID, alreadyExisted: envelope.Result.AlreadyExisted}
}

func listBuildIDs(t *testing.T, deps cliadapterbuild.Dependencies) []string {
	t.Helper()
	var stdout bytes.Buffer
	if err := cliadapterbuild.RunList(context.Background(), deps, []string{"--json"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunList() error = %v", err)
	}
	var result struct {
		Builds []struct {
			ID string `json:"id"`
		} `json:"builds"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode list stdout: %v", err)
	}
	ids := make([]string, len(result.Builds))
	for i, b := range result.Builds {
		ids[i] = b.ID
	}
	return ids
}

// TestRunRegister_ConcurrentSameIdempotencyKey_ExactlyOneBuild is this
// task's own "concurrency" Verify bullet: N goroutines calling `aw adapter
// register` concurrently with the IDENTICAL idempotency key (and payload)
// must all observe the same RegisterResult (same build id, same
// AlreadyExisted), and the registry must end up with exactly one row —
// never a double-registration. Mirrors internal/app/adapterbuild/
// commands_sqlite_test.go's own
// TestRegisterAdapterBuild_ConcurrentSameKey_ExactlyOneBuildOneEvent,
// driven here through this package's own RunRegister entrypoint instead of
// calling appadapterbuild.RegisterAdapterBuild directly.
func TestRunRegister_ConcurrentSameIdempotencyKey_ExactlyOneBuild(t *testing.T) {
	deps := sqliteDeps(t, "adapter-register-concurrent-same-key.db")
	path := writeExecutable(t, "binary-content-v1")
	token, _ := probeToken(t, deps, path)
	tokenFile := writeTokenJSON(t, token)
	args := registerArgs("--json", "--idempotency-key=register-race", "--file="+tokenFile)

	const writers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	outcomes := make([]registerOutcome, writers)
	stdouts := make([]bytes.Buffer, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = cliadapterbuild.RunRegister(context.Background(), deps, args, nil, &stdouts[i], &bytes.Buffer{})
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
		outcomes[i] = decodeRegisterOutcome(t, stdouts[i].Bytes())
	}
	for i := 1; i < writers; i++ {
		if outcomes[i].buildID != outcomes[0].buildID {
			t.Fatalf("writer %d got build id %q, want %q (same as writer 0)", i, outcomes[i].buildID, outcomes[0].buildID)
		}
		if outcomes[i].alreadyExisted != outcomes[0].alreadyExisted {
			t.Fatalf("writer %d AlreadyExisted=%v, want %v (same as writer 0) — every racer must see the same persisted receipt", i, outcomes[i].alreadyExisted, outcomes[0].alreadyExisted)
		}
	}
	if outcomes[0].buildID == "" {
		t.Fatal("build id is empty")
	}

	if ids := listBuildIDs(t, deps); len(ids) != 1 {
		t.Fatalf("len(builds) = %d, want exactly 1 (concurrent registration under the same idempotency key must never create a second row)", len(ids))
	}
}

// TestRunRegister_ConcurrentDistinctKeysSameFingerprint_OneFreshInsert is
// this task's own "concurrency" bullet for the FINGERPRINT dedup axis
// (distinct from the idempotency-key axis above): N goroutines each
// register the identical unchanged executable through their OWN distinct,
// pre-computed probe+register command pair (distinct idempotency keys
// throughout — computed sequentially, before the race starts, since probing
// and building a token file are not themselves under test here). Exactly
// one goroutine's registration is the one that actually inserts the row
// (AlreadyExisted=false); every other goroutine must observe
// AlreadyExisted=true for the SAME build id — never a double-registration.
func TestRunRegister_ConcurrentDistinctKeysSameFingerprint_OneFreshInsert(t *testing.T) {
	deps := sqliteDeps(t, "adapter-register-concurrent-distinct-keys.db")
	path := writeExecutable(t, "binary-content-v1")

	const writers = 8
	argsPerWriter := make([][]string, writers)
	for i := 0; i < writers; i++ {
		suffix := strconv.Itoa(i)
		token, _ := probeToken(t, deps, path, "--idempotency-key=probe-distinct-"+suffix)
		tokenFile := writeTokenJSON(t, token)
		argsPerWriter[i] = registerArgs("--json", "--file="+tokenFile, "--idempotency-key=register-distinct-"+suffix)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	stdouts := make([]bytes.Buffer, writers)
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = cliadapterbuild.RunRegister(context.Background(), deps, argsPerWriter[i], nil, &stdouts[i], &bytes.Buffer{})
		}()
	}
	close(start)
	wg.Wait()

	outcomes := make([]registerOutcome, writers)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
		outcomes[i] = decodeRegisterOutcome(t, stdouts[i].Bytes())
	}
	freshCount := 0
	for i := 0; i < writers; i++ {
		if outcomes[i].buildID != outcomes[0].buildID {
			t.Fatalf("writer %d got build id %q, want %q (same fingerprint must always resolve to the same build)", i, outcomes[i].buildID, outcomes[0].buildID)
		}
		if !outcomes[i].alreadyExisted {
			freshCount++
		}
	}
	if freshCount != 1 {
		t.Fatalf("exactly one writer should report AlreadyExisted=false (the one that actually inserted the row), got %d", freshCount)
	}

	if ids := listBuildIDs(t, deps); len(ids) != 1 {
		t.Fatalf("len(builds) = %d, want exactly 1 (fingerprint dedup must never create a second row across racing distinct commands)", len(ids))
	}
}
