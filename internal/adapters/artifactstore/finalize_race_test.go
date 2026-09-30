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
// Stat and both publish to the same content-addressed path. On Windows, the
// old implementation (os.Rename, which REPLACES an existing file) made the
// loser fail with "Access is denied" and made a concurrent reader hashing the
// object fail with "being used by another process". That was the recurring CI
// failure of internal/app/message's TestAppendConversationAttachment_*
// Concurrency_* (8+ hits across 7+ PRs, always filed as "Windows rename lock,
// environmental") — a real concurrency bug in the store: an upload that should
// have been a no-op failed, and a reader could not open a finished object.
// These tests force the interleavings on every OS by replacing publishObject.

func tempFiles(t *testing.T, store *Store) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.root, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// The object appears between Put's Stat and its publish; Put must neither
// fail nor touch it. The marker content stands for "a concurrent winner's
// object" — a correct publish leaves it byte-for-byte as it was, which is
// what makes it safe for a reader that is hashing it right now.
func TestPut_ObjectAppearsBeforePublish_IsLeftUntouchedAndIsASuccess(t *testing.T) {
	store := openStore(t)
	body := []byte("identical bytes uploaded by two racing requests")
	marker := []byte("the concurrent winner's object, which must not be replaced")

	real := publishObject
	t.Cleanup(func() { publishObject = real })
	publishObject = func(tmpPath, finalPath string) error {
		if err := os.MkdirAll(filepath.Dir(finalPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(finalPath, marker, 0o600); err != nil {
			return err
		}
		return real(tmpPath, finalPath) // the real publish, now facing an existing object
	}

	ref, err := store.Put(context.Background(), ports.ArtifactMetadata{ContentType: "text/plain"}, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put must succeed when the object already exists at publish time: %v", err)
	}
	if ref.Size != int64(len(body)) || ref.Locator == "" {
		t.Fatalf("ref = %+v, want the object's own ref", ref)
	}
	got, err := os.ReadFile(store.objectPath(ref.Locator[len("sha256:"):]))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, marker) {
		t.Fatalf("publish replaced an existing object: %q", got)
	}
	if n := tempFiles(t, store); n != 0 {
		t.Fatalf("the losing Put left %d temp file(s) behind", n)
	}
}

// The publish fails with a platform error (the rename fallback on a
// filesystem without hard links) but the object exists by the time Put looks:
// still a success.
func TestPut_PublishFailsButObjectNowExists_IsASuccessAndLeavesNoTempFile(t *testing.T) {
	store := openStore(t)
	body := []byte("identical bytes uploaded by two racing requests")

	real := publishObject
	t.Cleanup(func() { publishObject = real })
	publishObject = func(_, finalPath string) error {
		if err := os.MkdirAll(filepath.Dir(finalPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(finalPath, body, 0o600); err != nil {
			return err
		}
		return errors.New("rename: Access is denied.")
	}

	ref, err := store.Put(context.Background(), ports.ArtifactMetadata{ContentType: "text/plain"}, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Put must succeed when the content-addressed object exists after a failed publish: %v", err)
	}
	if err := store.Verify(context.Background(), ref); err != nil {
		t.Fatalf("the stored object must verify: %v", err)
	}
	if n := tempFiles(t, store); n != 0 {
		t.Fatalf("the losing Put left %d temp file(s) behind", n)
	}
}

func TestPut_PublishFailsAndObjectStillAbsent_IsAnError(t *testing.T) {
	store := openStore(t)
	real := publishObject
	t.Cleanup(func() { publishObject = real })
	publishObject = func(string, string) error { return errors.New("rename: Access is denied.") }

	if _, err := store.Put(context.Background(), ports.ArtifactMetadata{}, bytes.NewReader([]byte("content that never lands"))); err == nil {
		t.Fatal("a failed publish with no object at the final path must still be reported")
	}
	if n := tempFiles(t, store); n != 0 {
		t.Fatalf("a failed Put left %d temp file(s) behind", n)
	}
}

func TestPut_FirstPublishLeavesOneObjectAndNoTempFile(t *testing.T) {
	store := openStore(t)
	body := []byte("plain first upload")
	ref, err := store.Put(context.Background(), ports.ArtifactMetadata{ContentType: "text/plain"}, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Verify(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if n := tempFiles(t, store); n != 0 {
		t.Fatalf("Put left %d temp file(s) behind after publishing", n)
	}
}

// TestPut_ConcurrentIdenticalContent_AllSucceedAndAreReadable is the real-
// publish version: many goroutines Put identical bytes at once and each then
// verifies the object, as the attachment flow does. With the fix none may
// error, whatever the platform does with the racing publishes.
func TestPut_ConcurrentIdenticalContent_AllSucceedAndAreReadable(t *testing.T) {
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
			ref, err := store.Put(context.Background(), ports.ArtifactMetadata{ContentType: "text/plain"}, bytes.NewReader(body))
			if err == nil {
				err = store.Verify(context.Background(), ref)
			}
			refs[i], errs[i] = ref, err
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
	if n := tempFiles(t, store); n != 0 {
		t.Fatalf("%d temp file(s) left behind", n)
	}
}
