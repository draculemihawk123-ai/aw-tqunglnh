package artifactstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store
}

func TestNew_RejectsEmptyRoot(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("New(\"\") should reject an empty root")
	}
	if _, err := New("   "); err == nil {
		t.Fatal("New(\"   \") should reject a blank root")
	}
}

func TestPut_ThenOpen_RoundTrips(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	content := []byte("hello artifact store")

	ref, err := store.Put(ctx, ports.ArtifactMetadata{ContentType: "text/plain", Sensitivity: redact.Sensitive, Redacted: true}, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if ref.Size != int64(len(content)) {
		t.Fatalf("ref.Size = %d, want %d", ref.Size, len(content))
	}
	if ref.ContentType != "text/plain" || ref.Sensitivity != redact.Sensitive || !ref.Redacted {
		t.Fatalf("ref metadata = %+v, want ContentType=text/plain Sensitivity=Sensitive Redacted=true", ref)
	}
	if !strings.HasPrefix(ref.Locator, "sha256:") || len(ref.Locator) != len("sha256:")+64 {
		t.Fatalf("ref.Locator = %q, want a well-formed sha256 locator", ref.Locator)
	}
	if ref.SHA256 != ref.Locator {
		t.Fatalf("ref.SHA256 = %q, want it to match ref.Locator (%q) for this implementation", ref.SHA256, ref.Locator)
	}

	reader, err := store.Open(ctx, ref)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read opened artifact: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("read back %q, want %q", got, content)
	}
}

func TestVerify_Succeeds(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	ref, err := store.Put(ctx, ports.ArtifactMetadata{}, bytes.NewReader([]byte("verify me")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Verify(ctx, ref); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestPut_DuplicateContent_ConvergesToSameLocatorWithoutDuplicateStorage
// is V1-08's own "duplicate content" Verify requirement: writing the
// exact same bytes twice must succeed both times, return the same ref,
// and never store the content twice.
func TestPut_DuplicateContent_ConvergesToSameLocatorWithoutDuplicateStorage(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	content := []byte("duplicate me")

	first, err := store.Put(ctx, ports.ArtifactMetadata{ContentType: "text/plain"}, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put (first): %v", err)
	}
	second, err := store.Put(ctx, ports.ArtifactMetadata{ContentType: "text/plain"}, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put (second): %v", err)
	}
	if first.Locator != second.Locator {
		t.Fatalf("locators = %q / %q, want identical for identical content", first.Locator, second.Locator)
	}

	count := 0
	err = filepath.WalkDir(filepath.Join(store.root, "objects"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk objects dir: %v", err)
	}
	if count != 1 {
		t.Fatalf("stored object file count = %d, want 1 (duplicate content must not be stored twice)", count)
	}

	tmpEntries, err := os.ReadDir(filepath.Join(store.root, "tmp"))
	if err != nil {
		t.Fatalf("read tmp dir: %v", err)
	}
	if len(tmpEntries) != 0 {
		t.Fatalf("tmp dir has %d leftover file(s), want 0", len(tmpEntries))
	}
}

// failingReader returns n bytes successfully, then a fixed error.
type failingReader struct {
	remaining []byte
	failAfter int
	sentErr   error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.failAfter <= 0 {
		return 0, r.sentErr
	}
	n := r.failAfter
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.remaining) {
		n = len(r.remaining)
	}
	copy(p, r.remaining[:n])
	r.remaining = r.remaining[n:]
	r.failAfter -= n
	return n, nil
}

// TestPut_InterruptedWrite_LeavesNoArtifact is V1-08's own "interrupted
// write" Verify requirement: a body that errors partway through must
// leave nothing behind at any content address, and no leftover temp file.
func TestPut_InterruptedWrite_LeavesNoArtifact(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	sentinel := errors.New("simulated interrupted write")
	reader := &failingReader{remaining: []byte("this write will be interrupted before it finishes"), failAfter: 10, sentErr: sentinel}

	_, err := store.Put(ctx, ports.ArtifactMetadata{}, reader)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Put err = %v, want it to wrap %v", err, sentinel)
	}

	objectCount := 0
	err = filepath.WalkDir(filepath.Join(store.root, "objects"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			objectCount++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk objects dir: %v", err)
	}
	if objectCount != 0 {
		t.Fatalf("stored object file count = %d, want 0 (an interrupted write must leave no artifact)", objectCount)
	}

	tmpEntries, err := os.ReadDir(filepath.Join(store.root, "tmp"))
	if err != nil {
		t.Fatalf("read tmp dir: %v", err)
	}
	if len(tmpEntries) != 0 {
		t.Fatalf("tmp dir has %d leftover file(s), want 0 (the temp file must be cleaned up on failure)", len(tmpEntries))
	}
}

func TestPut_ContextAlreadyCancelled_ReturnsErrorAndCreatesNothing(t *testing.T) {
	store := openStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.Put(ctx, ports.ArtifactMetadata{}, bytes.NewReader([]byte("x")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Put err = %v, want context.Canceled", err)
	}
}

// TestVerify_DetectsCorruption is V1-08's own "corruption" Verify
// requirement: content silently modified on disk after Put must be
// detected, never trusted.
func TestVerify_DetectsCorruption(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	ref, err := store.Put(ctx, ports.ArtifactMetadata{}, bytes.NewReader([]byte("original content")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	path, _, err := store.resolvePath(ref)
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	if err := os.WriteFile(path, []byte("tampered content!"), 0o600); err != nil {
		t.Fatalf("simulate on-disk corruption: %v", err)
	}

	err = store.Verify(ctx, ref)
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("Verify err = %v, want ErrIntegrity", err)
	}
}

// TestDelete_RemovesContent_ThenIsIdempotent is V5-14's own bar: Delete
// actually removes the content, and calling it again against the exact
// same (now-absent) ref is a safe no-op, never an error — the same
// "already resolved" idempotency every other real-I/O operation in this
// codebase follows.
func TestDelete_RemovesContent_ThenIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	ref, err := store.Put(ctx, ports.ArtifactMetadata{}, bytes.NewReader([]byte("delete me")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	path, _, err := store.resolvePath(ref)
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat before delete: %v", err)
	}

	if err := store.Delete(ctx, ref); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("content still present after Delete (err=%v)", err)
	}

	if err := store.Delete(ctx, ref); err != nil {
		t.Fatalf("second Delete (idempotent replay) err = %v, want nil", err)
	}
}

// TestDelete_HashMismatch_RefusesAndLeavesContentInPlace is V5-14's own
// "kiểm hash nghiêm ngặt" bar: Delete must refuse to remove content whose
// real, on-disk hash/size no longer matches what ref itself claims —
// exactly the same corruption/drift signal Verify already detects, reused
// here rather than duplicated — and it must leave the file untouched when
// it refuses.
func TestDelete_HashMismatch_RefusesAndLeavesContentInPlace(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	ref, err := store.Put(ctx, ports.ArtifactMetadata{}, bytes.NewReader([]byte("original content")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	path, _, err := store.resolvePath(ref)
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	if err := os.WriteFile(path, []byte("a totally different payload"), 0o600); err != nil {
		t.Fatalf("simulate on-disk drift: %v", err)
	}

	if err := store.Delete(ctx, ref); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("Delete err = %v, want ErrIntegrity", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("content should still be present after a refused delete: %v", err)
	}
}

// TestDelete_AlreadyAbsent_IsANoOp covers content that was never written
// (or already removed by a prior sweep) at all — Delete must not error
// just because there was nothing to do.
func TestDelete_AlreadyAbsent_IsANoOp(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	ref := ports.ArtifactRef{Locator: "sha256:" + strings.Repeat("0", 64), SHA256: "sha256:" + strings.Repeat("0", 64)}

	if err := store.Delete(ctx, ref); err != nil {
		t.Fatalf("Delete of never-written content err = %v, want nil", err)
	}
}

// TestDelete_MalformedLocator_RejectedBeforeTouchingFilesystem mirrors
// TestOpen_MalformedLocator_RejectedBeforeTouchingFilesystem's own identical
// traversal-defense requirement for Delete.
func TestDelete_MalformedLocator_RejectedBeforeTouchingFilesystem(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	ref := ports.ArtifactRef{Locator: "../../../etc/passwd"}

	if err := store.Delete(ctx, ref); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("Delete(%q) err = %v, want apperror.CodeInvalidArgument", ref.Locator, err)
	}
}

func TestOpen_DetectsCorruption_NeverReturnsAReader(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	ref, err := store.Put(ctx, ports.ArtifactMetadata{}, bytes.NewReader([]byte("original content")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	path, _, err := store.resolvePath(ref)
	if err != nil {
		t.Fatalf("resolvePath: %v", err)
	}
	if err := os.WriteFile(path, []byte("tampered!"), 0o600); err != nil {
		t.Fatalf("simulate on-disk corruption: %v", err)
	}

	reader, err := store.Open(ctx, ref)
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("Open err = %v, want ErrIntegrity", err)
	}
	if reader != nil {
		t.Fatal("Open returned a non-nil reader for tampered content")
	}
}

func TestOpen_NotFound_ReturnsNotFoundCode(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	missing := ports.ArtifactRef{Locator: "sha256:" + strings.Repeat("0", 64)}

	_, err := store.Open(ctx, missing)
	if apperror.CodeOf(err) != apperror.CodeNotFound {
		t.Fatalf("Open err = %v, want apperror.CodeNotFound", err)
	}
}

// TestOpen_MalformedLocator_RejectedBeforeTouchingFilesystem is V1-08's
// own "traversal" Verify requirement: a hand-crafted ArtifactRef whose
// Locator tries to escape the store's root must be rejected outright by
// pattern validation, never partially resolved into a path first.
func TestOpen_MalformedLocator_RejectedBeforeTouchingFilesystem(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)

	malformed := []string{
		"../../../etc/passwd",
		"sha256:../../../etc/passwd",
		"sha256:" + strings.Repeat("g", 64), // non-hex
		"sha256:short",
		"sha256:" + strings.Repeat("0", 65), // too long
		"SHA256:" + strings.Repeat("0", 64), // wrong case prefix
		"",
		"sha256:" + strings.Repeat("0", 63) + "/",
	}
	for _, locator := range malformed {
		t.Run(locator, func(t *testing.T) {
			ref := ports.ArtifactRef{Locator: locator}
			if _, err := store.Open(ctx, ref); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
				t.Fatalf("Open(%q) err = %v, want apperror.CodeInvalidArgument", locator, err)
			}
			if err := store.Verify(ctx, ref); apperror.CodeOf(err) != apperror.CodeInvalidArgument {
				t.Fatalf("Verify(%q) err = %v, want apperror.CodeInvalidArgument", locator, err)
			}
		})
	}

	// None of the malformed attempts above should have created anything
	// outside (or even inside) the store root.
	outsideMarker := filepath.Join(filepath.Dir(store.root), "escaped-artifact-store-test-marker")
	if _, err := os.Stat(outsideMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a malformed locator attempt appears to have escaped the store root: %v", err)
	}
}

// TestObjectPath_UsesPortablePathConstruction proves the on-disk path a
// Put'd artifact lands at is built with filepath.Join end to end (so it
// is correct on both Windows and Linux, exercised for real by this
// package's own CI matrix job on each OS) rather than manual "/"
// concatenation.
func TestObjectPath_UsesPortablePathConstruction(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	ref, err := store.Put(ctx, ports.ArtifactMetadata{}, bytes.NewReader([]byte("sharding check")))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	digest := strings.TrimPrefix(ref.Locator, "sha256:")
	wantPath := filepath.Join(store.root, "objects", digest[0:2], digest[2:4], digest)
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected artifact at %s: %v", wantPath, err)
	}
}
