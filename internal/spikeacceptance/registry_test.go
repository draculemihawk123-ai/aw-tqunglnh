package spikeacceptance

import (
	"context"
	"errors"
	"testing"
	"time"
)

func passingHandler(id SPKID) ScenarioFunc {
	return func(ctx context.Context) (SPKResult, error) {
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
		entries = append(entries, ScenarioEntry{SPKID: id, Handler: func(context.Context) (SPKResult, error) {
			counts[id]++
			return SPKResult{Passed: id == SPK01}, nil // SPKID intentionally left blank: RunAll must stamp it
		}})
	}
	registry, err := NewRegistry(entries)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	manifest, err := registry.RunAll(context.Background(), "full-suite-dry-run", time.Now().UTC())
	if err != nil {
		t.Fatalf("RunAll() error = %v, want nil (clean full-suite dry run)", err)
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

func TestRegistryRunAllPropagatesHandlerError(t *testing.T) {
	entries := requiredEntries(t)
	for i, entry := range entries {
		if entry.SPKID == SPK05 {
			entries[i].Handler = func(context.Context) (SPKResult, error) {
				return SPKResult{}, errors.New("fixture setup failed")
			}
		}
	}
	registry, err := NewRegistry(entries)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if _, err := registry.RunAll(context.Background(), "full-suite-broken", time.Now().UTC()); err == nil {
		t.Fatal("RunAll() error = nil, want a propagated handler error")
	}
}

func TestDefaultScenariosFormAValidRegistryAndCleanDryRun(t *testing.T) {
	registry, err := NewRegistry(DefaultScenarios())
	if err != nil {
		t.Fatalf("NewRegistry(DefaultScenarios()) error = %v, want nil", err)
	}
	manifest, err := registry.RunAll(context.Background(), "full-suite-default", time.Now().UTC())
	if err != nil {
		t.Fatalf("RunAll(DefaultScenarios) error = %v, want nil (clean dry run)", err)
	}
	if len(manifest.Results) != len(RequiredSPKIDs()) {
		t.Fatalf("got %d results, want %d", len(manifest.Results), len(RequiredSPKIDs()))
	}
	for _, result := range manifest.Results {
		if len(result.Assertions) == 0 {
			t.Fatalf("SPK %s result has no assertions explaining its current status", result.SPKID)
		}
	}
}
