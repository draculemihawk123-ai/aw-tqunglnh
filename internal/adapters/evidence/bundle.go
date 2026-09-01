package evidence

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const SchemaVersion = 1

var (
	ErrInvalidPath     = errors.New("invalid evidence path")
	ErrBundleFinalized = errors.New("evidence bundle is already finalized")
	ErrIntegrity       = errors.New("evidence bundle integrity check failed")
)

type Artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	BundleID      string            `json:"bundleId"`
	CreatedAt     time.Time         `json:"createdAt"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Artifacts     []Artifact        `json:"artifacts"`
}

// Bundle writes an evidence directory once. It never overwrites an artifact
// and becomes read-only at the adapter boundary after Finalize succeeds.
type Bundle struct {
	mu        sync.Mutex
	id        string
	directory string
	createdAt time.Time
	entries   map[string]Artifact
	finalized bool
}

func Create(root, bundleID string) (*Bundle, error) {
	return CreateAt(root, bundleID, time.Now().UTC())
}

func CreateAt(root, bundleID string, createdAt time.Time) (*Bundle, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("evidence root is required")
	}
	if err := validateBundleID(bundleID); err != nil {
		return nil, err
	}
	if createdAt.IsZero() {
		return nil, errors.New("evidence bundle creation timestamp is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve evidence root: %w", err)
	}
	if err := os.MkdirAll(absRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create evidence root: %w", err)
	}
	directory := filepath.Join(absRoot, bundleID)
	if err := ensureWithin(absRoot, directory); err != nil {
		return nil, err
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create evidence bundle: %w", err)
	}
	return &Bundle{
		id:        bundleID,
		directory: directory,
		createdAt: createdAt.UTC(),
		entries:   make(map[string]Artifact),
	}, nil
}

// PruneExpired removes finalized, non-symlink evidence bundles older than the
// supplied retention period. Callers decide retention; alpha uses seven days.
// An invalid/unsealed bundle is retained for investigation rather than being
// silently removed by a cleanup job.
func PruneExpired(root string, now time.Time, retention time.Duration) ([]string, error) {
	if strings.TrimSpace(root) == "" || now.IsZero() || retention <= 0 {
		return nil, errors.New("evidence root, current time and positive retention are required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve evidence root: %w", err)
	}
	entries, err := os.ReadDir(absRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read evidence root: %w", err)
	}
	cutoff := now.UTC().Add(-retention)
	deleted := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if err := validateBundleID(entry.Name()); err != nil {
			continue
		}
		directory := filepath.Join(absRoot, entry.Name())
		if err := ensureWithin(absRoot, directory); err != nil {
			return nil, err
		}
		manifest, err := Verify(directory)
		if err != nil || manifest.SchemaVersion != SchemaVersion || manifest.BundleID != entry.Name() ||
			!manifest.CreatedAt.Before(cutoff) {
			continue
		}
		// Verify containment immediately before recursive deletion. The directory
		// name was validated and symlinks are skipped above; this extra check
		// keeps cleanup constrained even if the root is supplied by an operator.
		if err := ensureWithin(absRoot, directory); err != nil {
			return nil, err
		}
		if err := os.RemoveAll(directory); err != nil {
			return nil, fmt.Errorf("remove expired evidence bundle %s: %w", entry.Name(), err)
		}
		deleted = append(deleted, entry.Name())
	}
	sort.Strings(deleted)
	return deleted, nil
}

func (b *Bundle) Directory() string {
	return b.directory
}

func (b *Bundle) Put(path string, body []byte) (Artifact, error) {
	return b.PutReader(path, bytes.NewReader(body))
}

func (b *Bundle) PutRedacted(path string, body []byte, secrets ...string) (Artifact, error) {
	return b.Put(path, Redact(body, secrets...))
}

func (b *Bundle) PutJSON(path string, value any) (Artifact, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return Artifact{}, fmt.Errorf("encode evidence JSON: %w", err)
	}
	encoded = append(encoded, '\n')
	return b.Put(path, encoded)
}

func (b *Bundle) PutReader(path string, body io.Reader) (Artifact, error) {
	if body == nil {
		return Artifact{}, errors.New("evidence body is required")
	}
	normalized, err := normalizeRelativePath(path)
	if err != nil {
		return Artifact{}, err
	}
	if normalized == "manifest.json" || normalized == "checksums.sha256" {
		return Artifact{}, fmt.Errorf("%w: %s is reserved", ErrInvalidPath, normalized)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finalized {
		return Artifact{}, ErrBundleFinalized
	}
	if _, exists := b.entries[normalized]; exists {
		return Artifact{}, fmt.Errorf("evidence artifact %q already exists", normalized)
	}
	target := filepath.Join(b.directory, filepath.FromSlash(normalized))
	if err := ensureWithin(b.directory, target); err != nil {
		return Artifact{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return Artifact{}, fmt.Errorf("create evidence artifact directory: %w", err)
	}

	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Artifact{}, fmt.Errorf("create evidence artifact: %w", err)
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(target)
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), body)
	if err != nil {
		return Artifact{}, fmt.Errorf("write evidence artifact: %w", err)
	}
	if err := file.Sync(); err != nil {
		return Artifact{}, fmt.Errorf("sync evidence artifact: %w", err)
	}
	if err := file.Close(); err != nil {
		return Artifact{}, fmt.Errorf("close evidence artifact: %w", err)
	}
	keep = true
	artifact := Artifact{
		Path:   normalized,
		SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil)),
		Size:   size,
	}
	b.entries[normalized] = artifact
	return artifact, nil
}

func (b *Bundle) Finalize(metadata map[string]string) (Manifest, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finalized {
		return Manifest{}, ErrBundleFinalized
	}

	artifacts := make([]Artifact, 0, len(b.entries))
	for _, artifact := range b.entries {
		artifacts = append(artifacts, artifact)
	}
	sort.Slice(artifacts, func(left, right int) bool {
		return artifacts[left].Path < artifacts[right].Path
	})
	manifest := Manifest{
		SchemaVersion: SchemaVersion,
		BundleID:      b.id,
		CreatedAt:     b.createdAt,
		Metadata:      cloneMetadata(metadata),
		Artifacts:     artifacts,
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, fmt.Errorf("encode evidence manifest: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')
	manifestHash := sha256.Sum256(manifestBytes)
	if err := writeExclusive(filepath.Join(b.directory, "manifest.json"), manifestBytes); err != nil {
		return Manifest{}, fmt.Errorf("write evidence manifest: %w", err)
	}

	var checksums strings.Builder
	for _, artifact := range artifacts {
		checksums.WriteString(strings.TrimPrefix(artifact.SHA256, "sha256:"))
		checksums.WriteString("  ")
		checksums.WriteString(artifact.Path)
		checksums.WriteByte('\n')
	}
	checksums.WriteString(hex.EncodeToString(manifestHash[:]))
	checksums.WriteString("  manifest.json\n")
	if err := writeExclusive(filepath.Join(b.directory, "checksums.sha256"), []byte(checksums.String())); err != nil {
		_ = os.Remove(filepath.Join(b.directory, "manifest.json"))
		return Manifest{}, fmt.Errorf("write evidence checksums: %w", err)
	}
	b.finalized = true
	return manifest, nil
}

func Verify(directory string) (Manifest, error) {
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve evidence bundle: %w", err)
	}
	manifest, manifestBytes, err := readManifestWithBytes(absDirectory)
	if err != nil {
		return Manifest{}, integrityError("read manifest", err)
	}
	if manifest.SchemaVersion != SchemaVersion || manifest.BundleID != filepath.Base(absDirectory) {
		return Manifest{}, integrityError("manifest identity", nil)
	}

	expected := make(map[string]string, len(manifest.Artifacts)+1)
	seen := make(map[string]struct{}, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		normalized, err := normalizeRelativePath(artifact.Path)
		if err != nil || normalized != artifact.Path {
			return Manifest{}, integrityError("manifest artifact path", err)
		}
		if _, duplicate := seen[normalized]; duplicate {
			return Manifest{}, integrityError("duplicate manifest artifact", nil)
		}
		seen[normalized] = struct{}{}
		target := filepath.Join(absDirectory, filepath.FromSlash(normalized))
		if err := ensureWithin(absDirectory, target); err != nil {
			return Manifest{}, integrityError("artifact containment", err)
		}
		hash, size, err := hashFile(target)
		if err != nil {
			return Manifest{}, integrityError("hash artifact", err)
		}
		if hash != artifact.SHA256 || size != artifact.Size {
			return Manifest{}, integrityError("artifact digest mismatch: "+normalized, nil)
		}
		expected[normalized] = strings.TrimPrefix(hash, "sha256:")
	}
	manifestHash := sha256.Sum256(manifestBytes)
	expected["manifest.json"] = hex.EncodeToString(manifestHash[:])

	checksums, err := readChecksums(filepath.Join(absDirectory, "checksums.sha256"))
	if err != nil {
		return Manifest{}, integrityError("read checksums", err)
	}
	if len(checksums) != len(expected) {
		return Manifest{}, integrityError("checksum entry count", nil)
	}
	for path, want := range expected {
		if got := checksums[path]; got != want {
			return Manifest{}, integrityError("checksum mismatch: "+path, nil)
		}
	}

	allowed := map[string]struct{}{"manifest.json": {}, "checksums.sha256": {}}
	for path := range seen {
		allowed[path] = struct{}{}
	}
	err = filepath.WalkDir(absDirectory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(absDirectory, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if _, ok := allowed[relative]; !ok {
			return fmt.Errorf("unmanifested file %s", relative)
		}
		return nil
	})
	if err != nil {
		return Manifest{}, integrityError("walk bundle", err)
	}
	return manifest, nil
}

func readManifest(directory string) (Manifest, error) {
	manifest, _, err := readManifestWithBytes(directory)
	return manifest, err
}

func readManifestWithBytes(directory string) (Manifest, []byte, error) {
	manifestBytes, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return Manifest{}, nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, nil, err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Manifest{}, nil, err
	}
	return manifest, manifestBytes, nil
}

func Redact(body []byte, secrets ...string) []byte {
	redacted := append([]byte(nil), body...)
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		redacted = bytes.ReplaceAll(redacted, []byte(secret), []byte("[REDACTED]"))
	}
	return redacted
}

func readChecksums(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	result := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 || len(parts[0]) != sha256.Size*2 {
			return nil, fmt.Errorf("invalid checksum line %q", line)
		}
		if _, err := hex.DecodeString(parts[0]); err != nil {
			return nil, fmt.Errorf("invalid checksum digest: %w", err)
		}
		normalized, err := normalizeRelativePath(parts[1])
		if err != nil || normalized != parts[1] {
			return nil, fmt.Errorf("invalid checksum path %q", parts[1])
		}
		if _, duplicate := result[normalized]; duplicate {
			return nil, fmt.Errorf("duplicate checksum path %q", normalized)
		}
		result[normalized] = parts[0]
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), size, nil
}

func writeExclusive(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	keep = true
	return nil
}

func normalizeRelativePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) ||
		strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`) {
		return "", ErrInvalidPath
	}
	path = filepath.ToSlash(path)
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") ||
		strings.ContainsRune(cleaned, '\x00') || strings.Contains(cleaned, ":") {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, path)
	}
	return cleaned, nil
}

func validateBundleID(id string) error {
	if id == "" || id != strings.TrimSpace(id) || id == "." || id == ".." ||
		strings.ContainsAny(id, `/\\:`) || strings.ContainsRune(id, '\x00') {
		return fmt.Errorf("%w: invalid bundle id", ErrInvalidPath)
	}
	return nil
}

func ensureWithin(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return ErrInvalidPath
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func cloneMetadata(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func integrityError(detail string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrIntegrity, detail)
	}
	return fmt.Errorf("%w: %s: %v", ErrIntegrity, detail, cause)
}
