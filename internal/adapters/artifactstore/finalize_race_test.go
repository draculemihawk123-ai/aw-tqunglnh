package artifactstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Two concurrent Puts of identical bytes can both pass Put's "already stored?"
// Stat and both rename onto the same content-addressed path; on Windows the
// loser's rename fails with "Access is denied" although the object it wanted
// to create now exists. That surfaced as the recurring CI failure of
// internal/app/message's TestAppendConversationAttachment_*Concurrency_* (7+
// hits across 6+ PRs, always filed as "Windows rename lock, environmental").
// It is a real concurrency bug in the store: an upload that should have been a
// no-op failed with an error. These tests force the interleaving on every OS
// by replacing the rename seam.

func TestPut_RenameFailsButObjectNowExists_IsASuccessAndLeavesNoTempFile(t *testing.T) {
	store := openStore(t)
	body := []byte("identical bytes uploaded by two racing requests")

	real := renameFile
	t.Cleanup(func() { renameFile = real })
	renameFile = func(oldPath, newPath string) error {
		// The concurrent winner finalizes the same object first ...
		if err := os.MkdirAll(filepath.Dir(newPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(newPath, body, 0o600); err != nil {
			return err
		}
		// ... and our own rename then fails the way Windows does.
		return errors.New("rename: Access is denied.")
	}

	ref, err := store.Put(context.Background(), ports.ArtifactMetadata{ContentType: "text/plain"}, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put must succeed when the content-addressed object exists after a failed rename: %v", err)
	}
	if ref.Size != int64(len(body)) || ref.Locator == "" {
		t.Fatalf("ref = %+v, want the object's own ref", ref)
	}
	if err := store.Verify(context.Background(), ref); err != nil {
		t.Fatalf("the stored object must verify: %v", err)
	}

	leftovers, err := os.ReadDir(filepath.Join(store.root, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("the losing Put left %d temp file(s) behind", len(leftovers))
	}
}

func TestPut_RenameFailsAndObjectStillAbsent_IsAnError(t *testing.T) {
	store := openStore(t)
	real := renameFile
	t.Cleanup(func() { renameFile = real })
	renameFile = func(string, string) error { return errors.New("rename: Access is denied.") }

	if _, err := store.Put(context.Background(), ports.ArtifactMetadata{}, bytes.NewReader([]byte("content that never lands"))); err == nil {
		t.Fatal("a failed rename with no object at the final path must still be reported")
	}
	leftovers, err := os.ReadDir(filepath.Join(store.root, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("a failed Put left %d temp file(s) behind", len(leftovers))
	}
}

// TestPut_ConcurrentIdenticalContent_AllSucceed is the real-rename version:
// many goroutines Put identical bytes at once. With the fix none may error,
// whatever the platform does with the racing renames.
func TestPut_ConcurrentIdenticalContent_AllSucceed(t *testing.T) {
	store := openStore(t)
	body := bytes.Repeat([]byte("racing identical content "), 4096)

	const workers = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, workers)
	refs := make([]ports.ArtifactRef, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			refs[i], errs[i] = store.Put(context.Background(), ports.ArtifactMetadata{ContentType: "text/plain"}, bytes.NewReader(body))
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
		if refs[i].Locator != refs[0].Locator {
			t.Fatalf("worker %d got locator %s, want %s", i, refs[i].Locator, refs[0].Locator)
		}
	}
	if err := store.Verify(context.Background(), refs[0]); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
