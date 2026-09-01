package spikeacceptance

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/evidence"
)

func passingHandler(id SPKID) ScenarioFunc {
	return func(ctx context.Context, writer EvidenceWriter) (SPKResult, error) {
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
		entries = append(entries, ScenarioEntry{SPKID: id, Handler: func(context.Context, EvidenceWriter) (SPKResult, error) {
			counts[id]++
			return SPKResult{Passed: id == SPK01}, nil // SPKID intentionally left blank: RunAll must stamp it
		}})
	}
	registry, err := NewRegistry(entries)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	manifest, err := registry.RunAll(context.Background(), t.TempDir(), "full-suite-dry-run", time.Now().UTC())
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
		entries = append(entries, ScenarioEntry{SPKID: id, Handler: func(_ context.Context, writer EvidenceWriter) (SPKResult, error) {
			artifact, err := writer.PutJSON("assertions/report.json", map[string]bool{"passed": true})
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
	manifest, err := registry.RunAll(context.Background(), evidenceRoot, suiteID, now)
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

func TestRegistryRunAllPropagatesHandlerError(t *testing.T) {
	entries := requiredEntries(t)
	for i, entry := range entries {
		if entry.SPKID == SPK05 {
			entries[i].Handler = func(context.Context, EvidenceWriter) (SPKResult, error) {
				return SPKResult{}, errors.New("fixture setup failed")
			}
		}
	}
	registry, err := NewRegistry(entries)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if _, err := registry.RunAll(context.Background(), t.TempDir(), "full-suite-broken", time.Now().UTC()); err == nil {
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
	manifest, err := registry.RunAll(context.Background(), evidenceRoot, suiteID, time.Now().UTC())
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
