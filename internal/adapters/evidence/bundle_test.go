package evidence

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBundleFinalizesAndVerifiesRedactedArtifacts(t *testing.T) {
	bundle, err := Create(t.TempDir(), "spk-14-pass")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	secret := "credential-value-that-must-not-persist"
	artifact, err := bundle.PutRedacted(
		"providers/raw-redacted.jsonl",
		[]byte(`{"token":"`+secret+`","message":"safe"}`+"\n"),
		secret,
	)
	if err != nil {
		t.Fatalf("PutRedacted() error = %v", err)
	}
	if artifact.Size == 0 || !strings.HasPrefix(artifact.SHA256, "sha256:") {
		t.Fatalf("unexpected artifact metadata: %+v", artifact)
	}
	if _, err := bundle.PutJSON("runtime/run.json", map[string]string{"runId": "run-1"}); err != nil {
		t.Fatalf("PutJSON() error = %v", err)
	}
	manifest, err := bundle.Finalize(map[string]string{"testId": "SPK-14"})
	if err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if manifest.BundleID != "spk-14-pass" || len(manifest.Artifacts) != 2 {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}

	verified, err := Verify(bundle.Directory())
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if verified.Metadata["testId"] != "SPK-14" {
		t.Fatalf("verified metadata = %#v", verified.Metadata)
	}
	raw, err := os.ReadFile(filepath.Join(bundle.Directory(), "providers", "raw-redacted.jsonl"))
	if err != nil {
		t.Fatalf("read redacted artifact: %v", err)
	}
	if strings.Contains(string(raw), secret) || !strings.Contains(string(raw), "[REDACTED]") {
		t.Fatalf("artifact was not redacted: %q", raw)
	}
	if _, err := bundle.Put("late.txt", []byte("late")); !errors.Is(err, ErrBundleFinalized) {
		t.Fatalf("Put() after Finalize error = %v, want ErrBundleFinalized", err)
	}
}

func TestVerifyDetectsArtifactTampering(t *testing.T) {
	bundle, err := Create(t.TempDir(), "spk-14-tamper")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Put("assertions/report.json", []byte(`{"pass":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Finalize(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(bundle.Directory()); err != nil {
		t.Fatalf("baseline Verify() error = %v", err)
	}

	target := filepath.Join(bundle.Directory(), "assertions", "report.json")
	if err := os.WriteFile(target, []byte(`{"pass":false}`), 0o600); err != nil {
		t.Fatalf("tamper artifact: %v", err)
	}
	if _, err := Verify(bundle.Directory()); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("tampered Verify() error = %v, want ErrIntegrity", err)
	}
}

func TestBundleRejectsTraversalAndUnmanifestedFiles(t *testing.T) {
	bundle, err := Create(t.TempDir(), "spk-14-paths")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../escape", "/absolute", `C:\\absolute`, "manifest.json"} {
		if _, err := bundle.Put(path, []byte("unsafe")); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Put(%q) error = %v, want ErrInvalidPath", path, err)
		}
	}
	if _, err := bundle.Put("runtime/run.json", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Finalize(nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle.Directory(), "orphan.txt"), []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(bundle.Directory()); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("Verify() with unmanifested file error = %v, want ErrIntegrity", err)
	}
}

func TestPruneExpiredRemovesOnlySealedBundlesOutsideRetention(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	old, err := CreateAt(root, "old-bundle", now.Add(-8*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Put("runtime/run.json", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Finalize(nil); err != nil {
		t.Fatal(err)
	}
	newer, err := CreateAt(root, "new-bundle", now.Add(-6*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newer.Put("runtime/run.json", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := newer.Finalize(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateAt(root, "unfinished", now.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	tampered, err := CreateAt(root, "tampered", now.Add(-8*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tampered.Put("runtime/run.json", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tampered.Finalize(nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tampered.Directory(), "runtime", "run.json"), []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	deleted, err := PruneExpired(root, now, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("PruneExpired() error = %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "old-bundle" {
		t.Fatalf("deleted bundles = %#v", deleted)
	}
	if _, err := os.Stat(old.Directory()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old bundle remains after prune: %v", err)
	}
	if _, err := os.Stat(newer.Directory()); err != nil {
		t.Fatalf("new bundle was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "unfinished")); err != nil {
		t.Fatalf("unfinished bundle was removed: %v", err)
	}
	if _, err := os.Stat(tampered.Directory()); err != nil {
		t.Fatalf("tampered bundle was removed: %v", err)
	}
}
