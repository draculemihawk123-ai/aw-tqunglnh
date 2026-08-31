package spikeacceptance

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func allRequiredResults() []SPKResult {
	ids := RequiredSPKIDs()
	results := make([]SPKResult, 0, len(ids))
	for _, id := range ids {
		results = append(results, SPKResult{SPKID: id, Passed: true})
	}
	return results
}

func TestNewSPKManifestAcceptsExactlyRequiredIDs(t *testing.T) {
	now := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	manifest, err := NewSPKManifest("full-suite-1", now, allRequiredResults())
	if err != nil {
		t.Fatalf("NewSPKManifest() error = %v, want nil", err)
	}
	if manifest.SuiteID != "full-suite-1" || !manifest.GeneratedAt.Equal(now) {
		t.Fatalf("unexpected manifest header: %+v", manifest)
	}
	if len(manifest.Results) != len(RequiredSPKIDs()) {
		t.Fatalf("got %d results, want %d", len(manifest.Results), len(RequiredSPKIDs()))
	}
}

func TestNewSPKManifestRejectsMissingIDs(t *testing.T) {
	results := allRequiredResults()
	results = results[:len(results)-1] // drop SPK-14, the last RequiredSPKIDs() entry
	_, err := NewSPKManifest("full-suite-missing", time.Now().UTC(), results)
	if !errors.Is(err, ErrSPKIDMissing) {
		t.Fatalf("NewSPKManifest() error = %v, want ErrSPKIDMissing", err)
	}
	if !strings.Contains(err.Error(), string(SPK14)) {
		t.Fatalf("error %q does not name the missing id %q", err.Error(), SPK14)
	}
}

func TestNewSPKManifestRejectsDuplicateIDs(t *testing.T) {
	results := append(allRequiredResults(), SPKResult{SPKID: SPK01, Passed: true})
	_, err := NewSPKManifest("full-suite-duplicate", time.Now().UTC(), results)
	if !errors.Is(err, ErrSPKIDDuplicate) {
		t.Fatalf("NewSPKManifest() error = %v, want ErrSPKIDDuplicate", err)
	}
	if !strings.Contains(err.Error(), string(SPK01)) {
		t.Fatalf("error %q does not name the duplicated id %q", err.Error(), SPK01)
	}
}

func TestNewSPKManifestRejectsUnknownID(t *testing.T) {
	results := allRequiredResults()
	results[len(results)-1] = SPKResult{SPKID: SPKID("SPK-99"), Passed: true} // replaces SPK-14
	_, err := NewSPKManifest("full-suite-unknown", time.Now().UTC(), results)
	if !errors.Is(err, ErrSPKIDUnknown) {
		t.Fatalf("NewSPKManifest() error = %v, want ErrSPKIDUnknown", err)
	}
	if !errors.Is(err, ErrSPKIDMissing) {
		t.Fatalf("NewSPKManifest() error = %v, want also ErrSPKIDMissing for SPK-14", err)
	}
}
