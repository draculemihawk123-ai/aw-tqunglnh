// Package artifactstore is the filesystem-backed ports.ArtifactStore
// production port (docs/design/03-v1-alpha-foundation.md V1-08):
// content-addressed and immutable, with an atomic temp-write-then-rename
// finalize so a crash mid-write never leaves a partial artifact visible
// at its content address, and Verify/Open re-hash stored content before
// ever handing bytes back to a caller — detecting on-disk corruption or
// tampering rather than trusting whatever bytes happen to be on disk.
package artifactstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// ErrIntegrity is returned by Verify/Open when stored content no longer
// hashes to its own recorded locator.
var ErrIntegrity = errors.New("artifactstore: content does not match its recorded hash")

// locatorPattern is the only shape a Locator this store issues (or
// accepts back) may ever take: "sha256:" plus exactly 64 lowercase hex
// characters. Validating a caller-supplied Locator against this pattern
// before it is ever used to build a filesystem path is the store's
// traversal defense — a well-formed hex digest cannot contain "..", "/",
// "\", or a NUL byte, so there is nothing left to sanitize once the
// pattern matches; a Locator that doesn't match is rejected outright
// rather than partially cleaned up.
var locatorPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Store is a filesystem-backed ports.ArtifactStore rooted at one
// directory: content lives under <root>/objects/<aa>/<bb>/<hash>
// (git-style two-level sharding, so no single directory ever has to hold
// every artifact the store has seen); in-flight writes land under
// <root>/tmp first and are only ever made visible at their final,
// content-addressed path via os.Rename — which both POSIX and Windows
// (via MoveFileEx with MOVEFILE_REPLACE_EXISTING, what Go's os.Rename
// uses) guarantee is atomic within the same volume.
type Store struct {
	root string
}

var _ ports.ArtifactStore = (*Store)(nil)

// New returns a Store rooted at root, creating it (and its objects/tmp
// subdirectories) if it doesn't already exist.
func New(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("artifactstore: root is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("artifactstore: resolve root: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(absRoot, "objects"), 0o700); err != nil {
		return nil, fmt.Errorf("artifactstore: create objects directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(absRoot, "tmp"), 0o700); err != nil {
		return nil, fmt.Errorf("artifactstore: create tmp directory: %w", err)
	}
	return &Store{root: absRoot}, nil
}

// Put implements ports.ArtifactStore. It streams body to a temp file
// while hashing it, and only ever creates (or leaves visible) a file at
// the content-addressed final path once the entire body has been read
// and synced successfully — an error or a cancelled ctx partway through
// removes the temp file and leaves no trace at any content address.
//
// If the exact content is already stored (same SHA256), Put discards the
// new temp file and returns the existing artifact's ref rather than
// re-writing or verifying it: an object already present at a
// content-addressed path is, by construction, already known-good — this
// store is its only writer, and it never mutates a finalized object.
func (s *Store) Put(ctx context.Context, meta ports.ArtifactMetadata, body io.Reader) (ports.ArtifactRef, error) {
	if body == nil {
		return ports.ArtifactRef{}, apperror.New(apperror.CodeInvalidArgument, "artifactstore: body is required", false)
	}
	if err := ctx.Err(); err != nil {
		return ports.ArtifactRef{}, err
	}

	tmpFile, err := os.CreateTemp(filepath.Join(s.root, "tmp"), "artifact-*")
	if err != nil {
		return ports.ArtifactRef{}, fmt.Errorf("artifactstore: create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	finalized := false
	defer func() {
		if !finalized {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmpFile, hash), body)
	if err != nil {
		return ports.ArtifactRef{}, fmt.Errorf("artifactstore: write artifact content: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ports.ArtifactRef{}, err
	}
	if err := tmpFile.Sync(); err != nil {
		return ports.ArtifactRef{}, fmt.Errorf("artifactstore: sync artifact content: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return ports.ArtifactRef{}, fmt.Errorf("artifactstore: close artifact content: %w", err)
	}

	digest := hex.EncodeToString(hash.Sum(nil))
	locator := "sha256:" + digest
	ref := ports.ArtifactRef{
		Locator: locator, SHA256: locator, Size: size,
		ContentType: meta.ContentType, Sensitivity: meta.Sensitivity, Redacted: meta.Redacted,
	}
	finalPath := s.objectPath(digest)

	if _, statErr := os.Stat(finalPath); statErr == nil {
		_ = os.Remove(tmpPath)
		finalized = true
		return ref, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return ports.ArtifactRef{}, fmt.Errorf("artifactstore: stat final artifact path: %w", statErr)
	}

	if err := os.MkdirAll(filepath.Dir(finalPath), 0o700); err != nil {
		return ports.ArtifactRef{}, fmt.Errorf("artifactstore: create artifact shard directory: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return ports.ArtifactRef{}, fmt.Errorf("artifactstore: finalize artifact: %w", err)
	}
	finalized = true
	return ref, nil
}

// Verify implements ports.ArtifactStore: it re-hashes the stored content
// at ref.Locator and confirms it still matches ref.SHA256/ref.Size,
// without streaming the content back to the caller.
func (s *Store) Verify(ctx context.Context, ref ports.ArtifactRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, digest, err := s.resolvePath(ref)
	if err != nil {
		return err
	}
	actualDigest, actualSize, err := hashFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return apperror.New(apperror.CodeNotFound, "artifactstore: artifact not found", false)
	}
	if err != nil {
		return fmt.Errorf("artifactstore: hash stored artifact: %w", err)
	}
	if actualDigest != digest || actualSize != ref.Size {
		return fmt.Errorf("%w: locator=%s", ErrIntegrity, ref.Locator)
	}
	return nil
}

// Open implements ports.ArtifactStore: it verifies stored content
// against ref before returning a reader over it, so a caller can never
// read tampered or corrupted bytes without an error — "verify-open" as
// one guarantee, not two separate steps a caller has to remember to
// compose.
func (s *Store) Open(ctx context.Context, ref ports.ArtifactRef) (io.ReadCloser, error) {
	if err := s.Verify(ctx, ref); err != nil {
		return nil, err
	}
	path, _, err := s.resolvePath(ref)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("artifactstore: open artifact: %w", err)
	}
	return file, nil
}

// Delete implements ports.ArtifactStore (V5-14): removes the content at
// ref.Locator only after re-confirming it still hashes/sizes to
// ref.SHA256/ref.Size — the same strict check Verify itself performs,
// reused here rather than duplicated, so this method can never be tricked
// into deleting content that silently drifted from what its caller
// believes it is deleting. Idempotent: already-absent content is a
// success, not an error, mirroring gitworktree.Provider.Release's own
// identical "no-op if already gone" discipline for the sibling filesystem
// release path.
func (s *Store) Delete(ctx context.Context, ref ports.ArtifactRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, digest, err := s.resolvePath(ref)
	if err != nil {
		return err
	}
	actualDigest, actualSize, err := hashFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("artifactstore: hash artifact before delete: %w", err)
	}
	if actualDigest != digest || actualSize != ref.Size {
		return fmt.Errorf("%w: locator=%s", ErrIntegrity, ref.Locator)
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("artifactstore: delete artifact: %w", err)
	}
	return nil
}

func (s *Store) objectPath(hexDigest string) string {
	return filepath.Join(s.root, "objects", hexDigest[0:2], hexDigest[2:4], hexDigest)
}

// resolvePath validates ref.Locator against locatorPattern before ever
// using it to build a filesystem path — see locatorPattern's doc comment
// for why this is sufficient traversal defense on its own.
func (s *Store) resolvePath(ref ports.ArtifactRef) (path string, digest string, err error) {
	if !locatorPattern.MatchString(ref.Locator) {
		return "", "", apperror.New(apperror.CodeInvalidArgument, "artifactstore: malformed locator", false)
	}
	digest = strings.TrimPrefix(ref.Locator, "sha256:")
	return s.objectPath(digest), digest, nil
}

func hashFile(path string) (digest string, size int64, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err = io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
