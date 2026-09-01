package spikeacceptance

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/evidence"
)

func passingHandler(id SPKID) ScenarioFunc {
	return func(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
		return SPKResult{SPKID: id, Passed: true}, nil
	}
}

func requiredEntries(t *testing.T) []ScenarioEntry {
	t.Helper()
	entries := make([]ScenarioEntry, 0, len(RequiredSPKIDs()))
	for _, id := range RequiredSPKIDs() {
		entries = append(entries, ScenarioEntry{SPKID: id, Handler: passingHandler(id)})
	}
	return entries
}

func TestNewRegistryAcceptsExactlyRequiredHandlers(t *testing.T) {
	if _, err := NewRegistry(requiredEntries(t)); err != nil {
		t.Fatalf("NewRegistry() error = %v, want nil", err)
	}
}

func TestNewRegistryRejectsMissingHandler(t *testing.T) {
	entries := requiredEntries(t)
	entries = entries[:len(entries)-1] // drop SPK-14, the last RequiredSPKIDs() entry
	_, err := NewRegistry(entries)
	if !errors.Is(err, ErrScenarioMissing) {
		t.Fatalf("NewRegistry() error = %v, want ErrScenarioMissing", err)
	}
}

func TestNewRegistryRejectsDuplicateHandler(t *testing.T) {
	entries := append(requiredEntries(t), ScenarioEntry{SPKID: SPK01, Handler: passingHandler(SPK01)})
	_, err := NewRegistry(entries)
	if !errors.Is(err, ErrScenarioDuplicate) {
		t.Fatalf("NewRegistry() error = %v, want ErrScenarioDuplicate", err)
	}
}

func TestNewRegistryRejectsNoOpHandler(t *testing.T) {
	entries := requiredEntries(t)
	entries[len(entries)-1] = ScenarioEntry{SPKID: SPK14, Handler: nil}
	_, err := NewRegistry(entries)
	if !errors.Is(err, ErrScenarioNoOp) {
		t.Fatalf("NewRegistry() error = %v, want ErrScenarioNoOp", err)
	}
}

func TestNewRegistryRejectsUnknownHandler(t *testing.T) {
	entries := requiredEntries(t)
	entries[len(entries)-1] = ScenarioEntry{SPKID: SPKID("SPK-99"), Handler: passingHandler(SPKID("SPK-99"))} // replaces SPK-14
	_, err := NewRegistry(entries)
	if !errors.Is(err, ErrScenarioUnknown) {
		t.Fatalf("NewRegistry() error = %v, want ErrScenarioUnknown", err)
	}
	if !errors.Is(err, ErrScenarioMissing) {
		t.Fatalf("NewRegistry() error = %v, want also ErrScenarioMissing for SPK-14", err)
	}
}

func TestRegistryRunAllInvokesEveryHandlerExactlyOnceAndStampsSPKID(t *testing.T) {
	counts := make(map[SPKID]int)
	entries := make([]ScenarioEntry, 0, len(RequiredSPKIDs()))
	for _, id := range RequiredSPKIDs() {
		id := id
		entries = append(entries, ScenarioEntry{SPKID: id, Handler: func(context.Context, ScenarioContext) (SPKResult, error) {
			counts[id]++
			return SPKResult{Passed: id == SPK01}, nil // SPKID intentionally left blank: RunAll must stamp it
		}})
	}
	registry, err := NewRegistry(entries)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	manifest, err := registry.RunAll(context.Background(), t.TempDir(), "full-suite-dry-run", time.Now().UTC(), ScenarioBinaries{})
	if err != nil {
		t.Fatalf("RunAll() error = %v, want nil (clean full-suite run)", err)
	}
	if len(manifest.Results) != len(RequiredSPKIDs()) {
		t.Fatalf("got %d results, want %d", len(manifest.Results), len(RequiredSPKIDs()))
	}
	for _, id := range RequiredSPKIDs() {
		if counts[id] != 1 {
			t.Fatalf("handler %s invoked %d times, want exactly 1", id, counts[id])
		}
	}
	for _, result := range manifest.Results {
		if result.SPKID == "" {
			t.Fatalf("RunAll left an empty SPKID in the manifest: %+v", result)
		}
	}
}

func TestRegistryRunAllWritesOneSealedVerifiedBundlePerSPK(t *testing.T) {
	entries := make([]ScenarioEntry, 0, len(RequiredSPKIDs()))
	for _, id := range RequiredSPKIDs() {
		id := id
		entries = append(entries, ScenarioEntry{SPKID: id, Handler: func(_ context.Context, sc ScenarioContext) (SPKResult, error) {
			artifact, err := sc.Bundle.PutJSON("assertions/report.json", map[string]bool{"passed": true})
			if err != nil {
				return SPKResult{}, err
			}
			return SPKResult{
				Passed:    true,
				Artifacts: []ArtifactRef{{Kind: ArtifactKindAssertions, Artifact: artifact}},
			}, nil
		}})
	}
	registry, err := NewRegistry(entries)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	evidenceRoot := t.TempDir()
	suiteID := "full-suite-bundles"
	now := time.Now().UTC()
	manifest, err := registry.RunAll(context.Background(), evidenceRoot, suiteID, now, ScenarioBinaries{})
	if err != nil {
		t.Fatalf("RunAll() error = %v", err)
	}
	for _, id := range RequiredSPKIDs() {
		verified, err := evidence.Verify(filepath.Join(evidenceRoot, suiteID+"-"+string(id)))
		if err != nil {
			t.Fatalf("Verify(bundle for %s) error = %v", id, err)
		}
		if verified.Metadata["spkId"] != string(id) || verified.Metadata["suiteId"] != suiteID {
			t.Fatalf("bundle metadata for %s = %#v", id, verified.Metadata)
		}
	}
	if len(manifest.Results) != len(RequiredSPKIDs()) {
		t.Fatalf("got %d results, want %d", len(manifest.Results), len(RequiredSPKIDs()))
	}
}

func TestRegistryRunAllSupportsScenarioSubBundles(t *testing.T) {
	suffix := "extra"
	entries := make([]ScenarioEntry, 0, len(RequiredSPKIDs()))
	for _, id := range RequiredSPKIDs() {
		entries = append(entries, ScenarioEntry{SPKID: id, Handler: func(_ context.Context, sc ScenarioContext) (SPKResult, error) {
			sub, err := sc.NewSubBundle(suffix)
			if err != nil {
				return SPKResult{}, err
			}
			if _, err := sub.PutJSON("assertions/report.json", map[string]bool{"passed": true}); err != nil {
				return SPKResult{}, err
			}
			if _, err := sub.Finalize(nil); err != nil {
				return SPKResult{}, err
			}
			if _, err := evidence.Verify(sub.Directory()); err != nil {
				return SPKResult{}, err
			}
			return SPKResult{Passed: true}, nil
		}})
	}
	registry, err := NewRegistry(entries)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	evidenceRoot := t.TempDir()
	suiteID := "full-suite-subbundle"
	if _, err := registry.RunAll(context.Background(), evidenceRoot, suiteID, time.Now().UTC(), ScenarioBinaries{}); err != nil {
		t.Fatalf("RunAll() error = %v", err)
	}
	for _, id := range RequiredSPKIDs() {
		if _, err := evidence.Verify(filepath.Join(evidenceRoot, suiteID+"-"+string(id)+"-"+suffix)); err != nil {
			t.Fatalf("Verify(sub-bundle for %s) error = %v", id, err)
		}
	}
}

func TestRegistryRunAllPropagatesHandlerError(t *testing.T) {
	entries := requiredEntries(t)
	for i, entry := range entries {
		if entry.SPKID == SPK05 {
			entries[i].Handler = func(context.Context, ScenarioContext) (SPKResult, error) {
				return SPKResult{}, errors.New("fixture setup failed")
			}
		}
	}
	registry, err := NewRegistry(entries)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if _, err := registry.RunAll(context.Background(), t.TempDir(), "full-suite-broken", time.Now().UTC(), ScenarioBinaries{}); err == nil {
		t.Fatal("RunAll() error = nil, want a propagated handler error")
	}
}

func TestDefaultScenariosFormAValidRegistryAndCleanRun(t *testing.T) {
	registry, err := NewRegistry(DefaultScenarios())
	if err != nil {
		t.Fatalf("NewRegistry(DefaultScenarios()) error = %v, want nil", err)
	}
	evidenceRoot := t.TempDir()
	suiteID := "full-suite-default"
	manifest, err := registry.RunAll(context.Background(), evidenceRoot, suiteID, time.Now().UTC(), buildScenarioBinaries(t))
	if err != nil {
		t.Fatalf("RunAll(DefaultScenarios) error = %v, want nil (clean run)", err)
	}
	if len(manifest.Results) != len(RequiredSPKIDs()) {
		t.Fatalf("got %d results, want %d", len(manifest.Results), len(RequiredSPKIDs()))
	}
	for _, result := range manifest.Results {
		if len(result.Assertions) == 0 {
			t.Fatalf("SPK %s result has no assertions explaining its current status", result.SPKID)
		}
		if len(result.Artifacts) == 0 {
			t.Fatalf("SPK %s result has no artifacts even though its bundle was sealed", result.SPKID)
		}
		if _, err := evidence.Verify(filepath.Join(evidenceRoot, suiteID+"-"+string(result.SPKID))); err != nil {
			t.Fatalf("Verify(bundle for %s) error = %v", result.SPKID, err)
		}
	}
}

// moduleRoot walks up from this test file's own directory to find go.mod,
// so buildScenarioBinaries can `go build` the fixture binaries regardless of
// the test runner's working directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file location")
	}
	directory := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("go.mod not found above test file")
		}
		directory = parent
	}
}

// buildScenarioBinaries builds the three fixture binaries SPK-06/07/11/12
// need into a fresh temp directory, using the same `go` toolchain currently
// running the test (via runtime.GOROOT, not a bare "go" on PATH, since a
// portable Go bundle may not be on PATH).
func buildScenarioBinaries(t *testing.T) ScenarioBinaries {
	t.Helper()
	root := moduleRoot(t)
	binDir := t.TempDir()
	goExecutable := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goExecutable += ".exe"
	}
	build := func(name, pkg string) string {
		output := filepath.Join(binDir, name)
		if runtime.GOOS == "windows" {
			output += ".exe"
		}
		command := exec.Command(goExecutable, "build", "-o", output, pkg)
		command.Dir = root
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", pkg, err, out)
		}
		return output
	}
	return ScenarioBinaries{
		FakeClaude:  build("fake-claude", "./cmd/fake-claude"),
		FakeCodex:   build("fake-codex", "./cmd/fake-codex"),
		SpikeHelper: build("spike-helper", "./cmd/spike-helper"),
		SpikeWorker: build("spike-worker", "./cmd/spike-worker"),
	}
}
