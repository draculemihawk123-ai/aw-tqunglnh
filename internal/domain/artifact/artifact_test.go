package artifact

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

func validArgs() (ID, project.ProjectID, string, string, int64, string, redact.Sensitivity, bool, RetentionClass, AttachState, bool, *time.Time, time.Time, uint64) {
	createdAt := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(rawOutputTempTTL)
	return "artifact-1", "project-1", "sha256:aaaa", "sha256:aaaa", 42, "text/plain",
		redact.Public, false, RetentionRawOutputTemp, Attached, false, &expiresAt, createdAt, 1
}

func TestNewArtifact_ValidRawOutputTemp_Succeeds(t *testing.T) {
	id, projectID, locator, hash, size, mediaType, sensitivity, redacted, class, state, hold, expiresAt, createdAt, version := validArgs()
	a, err := NewArtifact(id, projectID, locator, hash, size, mediaType, sensitivity, redacted, class, state, hold, expiresAt, createdAt, version)
	if err != nil {
		t.Fatalf("NewArtifact returned error: %v", err)
	}
	if a.ID != id || a.RetentionClass != RetentionRawOutputTemp || a.AttachState != Attached {
		t.Fatalf("unexpected artifact: %+v", a)
	}
	if a.ExpiresAt == nil || !a.ExpiresAt.Equal(*expiresAt) {
		t.Fatalf("ExpiresAt not preserved: %+v", a.ExpiresAt)
	}
	if a.CreatedAt.Location() != time.UTC {
		t.Fatalf("CreatedAt not normalized to UTC: %v", a.CreatedAt)
	}
}

func TestNewArtifact_ValidCanonicalContext_NilExpiresAt_Succeeds(t *testing.T) {
	createdAt := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	a, err := NewArtifact("artifact-2", project.ProjectID("project-1"), "sha256:bbbb", "sha256:bbbb", 10, "text/plain",
		redact.Sensitive, true, RetentionCanonicalContext, Orphan, true, nil, createdAt, 1)
	if err != nil {
		t.Fatalf("NewArtifact returned error: %v", err)
	}
	if a.ExpiresAt != nil {
		t.Fatalf("RetentionCanonicalContext must not carry an ExpiresAt, got %v", a.ExpiresAt)
	}
	if !a.Redacted || !a.Hold || a.AttachState != Orphan {
		t.Fatalf("unexpected artifact: %+v", a)
	}
}

func TestNewArtifact_RejectsMissingRequiredFields(t *testing.T) {
	id, projectID, locator, hash, size, mediaType, sensitivity, redacted, class, state, hold, expiresAt, createdAt, version := validArgs()

	cases := []struct {
		name string
		fn   func() (Artifact, error)
	}{
		{"empty ID", func() (Artifact, error) {
			return NewArtifact("", projectID, locator, hash, size, mediaType, sensitivity, redacted, class, state, hold, expiresAt, createdAt, version)
		}},
		{"empty ProjectID", func() (Artifact, error) {
			return NewArtifact(id, project.ProjectID(""), locator, hash, size, mediaType, sensitivity, redacted, class, state, hold, expiresAt, createdAt, version)
		}},
		{"empty Locator", func() (Artifact, error) {
			return NewArtifact(id, projectID, "", hash, size, mediaType, sensitivity, redacted, class, state, hold, expiresAt, createdAt, version)
		}},
		{"empty ContentHash", func() (Artifact, error) {
			return NewArtifact(id, projectID, locator, "", size, mediaType, sensitivity, redacted, class, state, hold, expiresAt, createdAt, version)
		}},
		{"negative Size", func() (Artifact, error) {
			return NewArtifact(id, projectID, locator, hash, -1, mediaType, sensitivity, redacted, class, state, hold, expiresAt, createdAt, version)
		}},
		{"empty MediaType", func() (Artifact, error) {
			return NewArtifact(id, projectID, locator, hash, size, "", sensitivity, redacted, class, state, hold, expiresAt, createdAt, version)
		}},
		{"unknown Sensitivity", func() (Artifact, error) {
			return NewArtifact(id, projectID, locator, hash, size, mediaType, redact.Sensitivity(99), redacted, class, state, hold, expiresAt, createdAt, version)
		}},
		{"unknown RetentionClass", func() (Artifact, error) {
			return NewArtifact(id, projectID, locator, hash, size, mediaType, sensitivity, redacted, RetentionClass("BOGUS"), state, hold, expiresAt, createdAt, version)
		}},
		{"unknown AttachState", func() (Artifact, error) {
			return NewArtifact(id, projectID, locator, hash, size, mediaType, sensitivity, redacted, class, AttachState("BOGUS"), hold, expiresAt, createdAt, version)
		}},
		{"zero CreatedAt", func() (Artifact, error) {
			return NewArtifact(id, projectID, locator, hash, size, mediaType, sensitivity, redacted, class, state, hold, expiresAt, time.Time{}, version)
		}},
		{"zero Version", func() (Artifact, error) {
			return NewArtifact(id, projectID, locator, hash, size, mediaType, sensitivity, redacted, class, state, hold, expiresAt, createdAt, 0)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.fn(); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestNewArtifact_RejectsRetentionExpiresAtMismatch(t *testing.T) {
	id, projectID, locator, hash, size, mediaType, sensitivity, redacted, _, state, hold, expiresAt, createdAt, version := validArgs()

	t.Run("RAW_OUTPUT_TEMP without ExpiresAt", func(t *testing.T) {
		if _, err := NewArtifact(id, projectID, locator, hash, size, mediaType, sensitivity, redacted,
			RetentionRawOutputTemp, state, hold, nil, createdAt, version); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
	t.Run("CANONICAL_CONTEXT with ExpiresAt", func(t *testing.T) {
		if _, err := NewArtifact(id, projectID, locator, hash, size, mediaType, sensitivity, redacted,
			RetentionCanonicalContext, state, hold, expiresAt, createdAt, version); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
	t.Run("ExpiresAt before CreatedAt", func(t *testing.T) {
		before := createdAt.Add(-time.Hour)
		if _, err := NewArtifact(id, projectID, locator, hash, size, mediaType, sensitivity, redacted,
			RetentionRawOutputTemp, state, hold, &before, createdAt, version); err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestComputeExpiresAt(t *testing.T) {
	createdAt := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

	rawExpiry := ComputeExpiresAt(RetentionRawOutputTemp, createdAt)
	if rawExpiry == nil {
		t.Fatal("RetentionRawOutputTemp: expected non-nil ExpiresAt")
	}
	if want := createdAt.Add(7 * 24 * time.Hour); !rawExpiry.Equal(want) {
		t.Fatalf("RetentionRawOutputTemp: got %v, want %v", rawExpiry, want)
	}

	if canonicalExpiry := ComputeExpiresAt(RetentionCanonicalContext, createdAt); canonicalExpiry != nil {
		t.Fatalf("RetentionCanonicalContext: expected nil ExpiresAt, got %v", canonicalExpiry)
	}
}

func TestRetentionClassValid(t *testing.T) {
	if !RetentionRawOutputTemp.Valid() || !RetentionCanonicalContext.Valid() {
		t.Fatal("known retention classes must be valid")
	}
	if RetentionClass("BOGUS").Valid() {
		t.Fatal("unknown retention class must not be valid")
	}
}

func TestAttachStateValid(t *testing.T) {
	if !Orphan.Valid() || !Attached.Valid() || !Purged.Valid() {
		t.Fatal("known attach states must be valid")
	}
	if AttachState("BOGUS").Valid() {
		t.Fatal("unknown attach state must not be valid")
	}
}
