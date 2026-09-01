package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func openReceiptsStore(t *testing.T, name string) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func recordReceipt(ctx context.Context, tx *sql.Tx, receipt ports.Receipt) error {
	return receiptsRepository{tx: tx}.Record(ctx, receipt)
}

func loadReceipt(ctx context.Context, tx *sql.Tx, actor string, scope ports.CommandScope, idempotencyKey, commandType string) (ports.Receipt, bool, error) {
	return receiptsRepository{tx: tx}.Load(ctx, actor, scope, idempotencyKey, commandType)
}

func TestReceiptsRepository_LoadNotFound(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-receipts-notfound.db")

	err := store.RunReadOnly(ctx, func(tx *sql.Tx) error {
		_, found, err := loadReceipt(ctx, tx, "actor-1", ports.InstallationScope(), "idem-1", "CreateProject")
		if err != nil {
			return err
		}
		if found {
			t.Fatal("Load found a receipt that was never recorded")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunReadOnly: %v", err)
	}
}

func TestReceiptsRepository_RecordThenLoad_RoundTrips(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-receipts-roundtrip.db")

	want := ports.Receipt{
		Actor:          "actor-1",
		Scope:          ports.ProjectScope("proj-1"),
		IdempotencyKey: "idem-1",
		CommandType:    "CreateProject",
		RequestHash:    "hash-a",
		ResultJSON:     `{"id":"p1"}`,
		ErrorCode:      "",
		CreatedAt:      time.Now().UTC().Truncate(time.Second),
	}

	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return recordReceipt(ctx, tx, want)
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	err = store.RunReadOnly(ctx, func(tx *sql.Tx) error {
		got, found, err := loadReceipt(ctx, tx, want.Actor, want.Scope, want.IdempotencyKey, want.CommandType)
		if err != nil {
			return err
		}
		if !found {
			t.Fatal("Load did not find the recorded receipt")
		}
		if got.RequestHash != want.RequestHash || got.ResultJSON != want.ResultJSON {
			t.Fatalf("got = %+v, want RequestHash/ResultJSON matching %+v", got, want)
		}
		if got.Scope.IsInstallation() != want.Scope.IsInstallation() {
			t.Fatalf("got.Scope = %+v, want %+v", got.Scope, want.Scope)
		}
		if gotID, _ := got.Scope.ProjectID(); gotID != "proj-1" {
			t.Fatalf("got.Scope.ProjectID() = %q, want %q", gotID, "proj-1")
		}
		if !got.CreatedAt.Equal(want.CreatedAt) {
			t.Fatalf("got.CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunReadOnly: %v", err)
	}
}

func TestReceiptsRepository_Record_DuplicateSamePayload_IsIdempotentNoOp(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-receipts-dup-same.db")

	receipt := ports.Receipt{
		Actor:          "actor-1",
		Scope:          ports.InstallationScope(),
		IdempotencyKey: "idem-1",
		CommandType:    "RotateKey",
		RequestHash:    "hash-a",
		ResultJSON:     `{"ok":true}`,
		CreatedAt:      time.Now().UTC(),
	}

	for i := 0; i < 2; i++ {
		err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
			return recordReceipt(ctx, tx, receipt)
		})
		if err != nil {
			t.Fatalf("Record attempt %d: %v", i, err)
		}
	}

	err := store.RunReadOnly(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM command_receipts`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("count = %d, want 1 (recording the identical receipt twice must not duplicate the row)", count)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("count receipts: %v", err)
	}
}

func TestReceiptsRepository_Record_DuplicateDifferentPayload_ReturnsConflict(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-receipts-dup-conflict.db")

	first := ports.Receipt{
		Actor:          "actor-1",
		Scope:          ports.InstallationScope(),
		IdempotencyKey: "idem-1",
		CommandType:    "RotateKey",
		RequestHash:    "hash-a",
		CreatedAt:      time.Now().UTC(),
	}
	second := first
	second.RequestHash = "hash-b"

	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return recordReceipt(ctx, tx, first)
	}); err != nil {
		t.Fatalf("Record first: %v", err)
	}

	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return recordReceipt(ctx, tx, second)
	})
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("Record second (different hash, same key) err = %v, want ports.ErrReceiptConflict", err)
	}
}

func TestReceiptsRepository_InstallationAndProjectScope_SameIdempotencyKey_DoNotCollide(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-receipts-scope-collision.db")

	installationReceipt := ports.Receipt{
		Actor: "actor-1", Scope: ports.InstallationScope(),
		IdempotencyKey: "idem-shared", CommandType: "Sync", RequestHash: "hash-install",
		CreatedAt: time.Now().UTC(),
	}
	projectReceipt := ports.Receipt{
		Actor: "actor-1", Scope: ports.ProjectScope("proj-1"),
		IdempotencyKey: "idem-shared", CommandType: "Sync", RequestHash: "hash-project",
		CreatedAt: time.Now().UTC(),
	}

	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		if err := recordReceipt(ctx, tx, installationReceipt); err != nil {
			return err
		}
		return recordReceipt(ctx, tx, projectReceipt)
	})
	if err != nil {
		t.Fatalf("recording both scopes with the same idempotency key: %v", err)
	}

	err = store.RunReadOnly(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM command_receipts`).Scan(&count); err != nil {
			return err
		}
		if count != 2 {
			t.Fatalf("count = %d, want 2 (installation and project scope must be independent rows)", count)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunReadOnly: %v", err)
	}
}

// TestReceiptsRepository_ConcurrentDuplicateInstallationCommand_OnlyOneReceiptStored
// is V1-06's own explicit Verify requirement: two genuinely separate
// connections racing to record the exact same installation-scoped receipt
// (the loser of a real idempotency race, not a caller bug) must both
// succeed, and exactly one row must exist afterward — never two receipts,
// never a spurious error on the loser.
func TestReceiptsRepository_ConcurrentDuplicateInstallationCommand_OnlyOneReceiptStored(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-receipts-concurrent.db")
	storeA, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open (A): %v", err)
	}
	defer storeA.Close()
	storeB, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open (B): %v", err)
	}
	defer storeB.Close()

	receipt := ports.Receipt{
		Actor:          "actor-1",
		Scope:          ports.InstallationScope(),
		IdempotencyKey: "idem-race",
		CommandType:    "RotateKey",
		RequestHash:    "hash-a",
		ResultJSON:     `{"ok":true}`,
		CreatedAt:      time.Now().UTC(),
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	race := func(store *Store, idx int) {
		defer wg.Done()
		<-start
		errs[idx] = store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
			return recordReceipt(ctx, tx, receipt)
		})
	}
	wg.Add(2)
	go race(storeA, 0)
	go race(storeB, 1)
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d failed: %v (a duplicate identical receipt must resolve as a no-op, not an error)", i, err)
		}
	}

	var count int
	if err := storeA.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM command_receipts`).Scan(&count); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want exactly 1 (two racing identical receipts must never produce two rows)", count)
	}
}

func TestCommandScope_Key_InstallationAndProjectDiffer(t *testing.T) {
	if got := ports.InstallationScope().Key(); got != "installation" {
		t.Fatalf("InstallationScope().Key() = %q, want %q", got, "installation")
	}
	if got := ports.ProjectScope("p1").Key(); got != "project:p1" {
		t.Fatalf("ProjectScope(\"p1\").Key() = %q, want %q", got, "project:p1")
	}
}

func TestProjectScope_EmptyProjectID_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("ProjectScope(\"\") should panic (a project scope with no project id is a construction bug)")
		}
	}()
	ports.ProjectScope("")
}
