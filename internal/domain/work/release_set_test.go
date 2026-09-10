package work

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
)

func TestNewReleaseSetNormalizesOrderAndIsImmutable(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	first, err := NewReleaseSet("release-1", "project-1", "family-1", []RepositoryRelease{
		{RepositoryID: "repo-web", BaseVCSObjectID: " base-web ", ResultVCSObjectID: "result-web", Verdict: gate.VerdictPass},
		{RepositoryID: "repo-api", BaseVCSObjectID: "base-api", ResultVCSObjectID: "result-api", Verdict: gate.VerdictFail},
	}, createdAt)
	if err != nil {
		t.Fatalf("create first release set: %v", err)
	}
	second, err := NewReleaseSet("release-1", "project-1", "family-1", []RepositoryRelease{
		{RepositoryID: "repo-api", BaseVCSObjectID: "base-api", ResultVCSObjectID: "result-api", Verdict: gate.VerdictFail},
		{RepositoryID: "repo-web", BaseVCSObjectID: "base-web", ResultVCSObjectID: "result-web", Verdict: gate.VerdictPass},
	}, createdAt)
	if err != nil {
		t.Fatalf("create second release set: %v", err)
	}
	if first.ContentHash() != second.ContentHash() {
		t.Fatalf("entry order changed release set hash: %q != %q", first.ContentHash(), second.ContentHash())
	}
	entries := first.Entries()
	if entries[0].RepositoryID != "repo-api" || entries[1].RepositoryID != "repo-web" {
		t.Fatalf("release set entries are not normalized: %#v", entries)
	}
	entries[0].BaseVCSObjectID = "mutated"
	release, ok := first.ReleaseFor("repo-api")
	if !ok || release.BaseVCSObjectID != "base-api" {
		t.Fatal("ReleaseSet leaked its mutable entry slice")
	}
	if first.State != ReleaseSetCreated || first.Version != 1 {
		t.Fatalf("new release set = state %s version %d, want CREATED/1", first.State, first.Version)
	}
}

func TestNewReleaseSetRejectsDuplicateRepository(t *testing.T) {
	t.Parallel()

	_, err := NewReleaseSet("release-1", "project-1", "family-1", []RepositoryRelease{
		{RepositoryID: "repo-api", BaseVCSObjectID: "base-1", ResultVCSObjectID: "result-1", Verdict: gate.VerdictPass},
		{RepositoryID: "repo-api", BaseVCSObjectID: "base-2", ResultVCSObjectID: "result-2", Verdict: gate.VerdictFail},
	}, time.Now())
	if err == nil {
		t.Fatal("duplicate repository release was accepted")
	}
}

func TestNewReleaseSetRejectsInvalidVerdict(t *testing.T) {
	t.Parallel()

	_, err := NewReleaseSet("release-1", "project-1", "family-1", []RepositoryRelease{
		{RepositoryID: "repo-api", BaseVCSObjectID: "base-1", ResultVCSObjectID: "result-1", Verdict: gate.Verdict("BOGUS")},
	}, time.Now())
	if err == nil {
		t.Fatal("invalid verdict was accepted")
	}
}

func TestNewReleaseSetRejectsMissingRequiredFields(t *testing.T) {
	t.Parallel()

	validEntry := []RepositoryRelease{
		{RepositoryID: "repo-api", BaseVCSObjectID: "base-1", ResultVCSObjectID: "result-1", Verdict: gate.VerdictPass},
	}
	now := time.Now()

	if _, err := NewReleaseSet("", "project-1", "family-1", validEntry, now); err == nil {
		t.Fatal("empty ReleaseSetID was accepted")
	}
	if _, err := NewReleaseSet("release-1", "", "family-1", validEntry, now); err == nil {
		t.Fatal("empty ProjectID was accepted")
	}
	if _, err := NewReleaseSet("release-1", "project-1", "", validEntry, now); err == nil {
		t.Fatal("empty FamilyID was accepted")
	}
	if _, err := NewReleaseSet("release-1", "project-1", "family-1", validEntry, time.Time{}); err == nil {
		t.Fatal("zero CreatedAt was accepted")
	}
	if _, err := NewReleaseSet("release-1", "project-1", "family-1", nil, now); err == nil {
		t.Fatal("empty repositories list was accepted")
	}
	if _, err := NewReleaseSet("release-1", "project-1", "family-1", []RepositoryRelease{
		{RepositoryID: "repo-api", BaseVCSObjectID: "", ResultVCSObjectID: "result-1", Verdict: gate.VerdictPass},
	}, now); err == nil {
		t.Fatal("empty BaseVCSObjectID was accepted")
	}
}

// TestNewReleaseSetAllowsMixedVerdictsAcrossRepositories is this task's own
// "partial result" Verify-line scenario: a ReleaseSet with a mix of PASS
// and FAIL repositories (unlike Gate's own single OverallVerdict, a
// ReleaseSet never collapses per-repository verdicts into one aggregate —
// V5-10A's own Thực hiện line names "verdict từng repository" specifically)
// must persist and round-trip each repository's own exact verdict.
func TestNewReleaseSetAllowsMixedVerdictsAcrossRepositories(t *testing.T) {
	t.Parallel()

	releaseSet, err := NewReleaseSet("release-1", "project-1", "family-1", []RepositoryRelease{
		{RepositoryID: "repo-pass", BaseVCSObjectID: "base-pass", ResultVCSObjectID: "result-pass", Verdict: gate.VerdictPass},
		{RepositoryID: "repo-fail", BaseVCSObjectID: "base-fail", ResultVCSObjectID: "result-fail", Verdict: gate.VerdictFail},
		{RepositoryID: "repo-error", BaseVCSObjectID: "base-error", ResultVCSObjectID: "result-error", Verdict: gate.VerdictError},
	}, time.Now())
	if err != nil {
		t.Fatalf("create mixed-verdict release set: %v", err)
	}

	pass, ok := releaseSet.ReleaseFor("repo-pass")
	if !ok || pass.Verdict != gate.VerdictPass {
		t.Fatalf("repo-pass release = %+v, ok=%v, want Verdict=PASS", pass, ok)
	}
	fail, ok := releaseSet.ReleaseFor("repo-fail")
	if !ok || fail.Verdict != gate.VerdictFail {
		t.Fatalf("repo-fail release = %+v, ok=%v, want Verdict=FAIL", fail, ok)
	}
	gateErr, ok := releaseSet.ReleaseFor("repo-error")
	if !ok || gateErr.Verdict != gate.VerdictError {
		t.Fatalf("repo-error release = %+v, ok=%v, want Verdict=ERROR", gateErr, ok)
	}
	if _, ok := releaseSet.ReleaseFor("repo-unknown"); ok {
		t.Fatal("ReleaseFor found an entry for a repository never named in the release set")
	}
}
