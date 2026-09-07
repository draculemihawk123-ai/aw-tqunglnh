package contextsnapshot

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func validRevisions(t *testing.T) workspace.RevisionSet {
	t.Helper()
	set, err := workspace.NewRevisionSet([]workspace.Revision{
		{RepositoryID: "repo-1", VCSObjectID: "abc123", WorkspaceGeneration: 1},
	})
	if err != nil {
		t.Fatalf("NewRevisionSet: %v", err)
	}
	return set
}

func TestNewSnapshot_Valid_Succeeds(t *testing.T) {
	createdAt := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	snap, err := NewSnapshot(
		"snap-1", project.ProjectID("project-1"), work.WorkItemID("work-item-1"), AttemptID("attempt-1"),
		[]MessageRef{{MessageID: "msg-1"}, {MessageID: "msg-2"}},
		[]ResourceRef{{ResourceKey: "res-1", ContentHash: "hash-1"}},
		validRevisions(t), createdAt,
	)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	if snap.ManifestHash == "" {
		t.Fatal("ManifestHash must not be empty")
	}
	if len(snap.MessageRefs) != 2 || snap.MessageRefs[0].MessageID != "msg-1" || snap.MessageRefs[1].MessageID != "msg-2" {
		t.Fatalf("MessageRefs order not preserved: %+v", snap.MessageRefs)
	}
}

func TestNewSnapshot_EmptyRefsAllowed(t *testing.T) {
	// A snapshot with zero messages/resources is not itself invalid at the
	// domain level (e.g. a resolution that selected nothing) — only blank
	// individual ref values are rejected.
	_, err := NewSnapshot(
		"snap-1", "project-1", "work-item-1", "attempt-1",
		nil, nil, validRevisions(t), time.Now(),
	)
	if err != nil {
		t.Fatalf("NewSnapshot with empty refs: %v", err)
	}
}

func TestNewSnapshot_RejectsMissingRequiredFields(t *testing.T) {
	revisions := validRevisions(t)
	now := time.Now()

	cases := []struct {
		name string
		fn   func() (Snapshot, error)
	}{
		{"empty ID", func() (Snapshot, error) { return NewSnapshot("", "p", "w", "a", nil, nil, revisions, now) }},
		{"empty ProjectID", func() (Snapshot, error) { return NewSnapshot("s", "", "w", "a", nil, nil, revisions, now) }},
		{"empty WorkItemID", func() (Snapshot, error) { return NewSnapshot("s", "p", "", "a", nil, nil, revisions, now) }},
		{"empty AttemptID", func() (Snapshot, error) { return NewSnapshot("s", "p", "w", "", nil, nil, revisions, now) }},
		{"zero CreatedAt", func() (Snapshot, error) { return NewSnapshot("s", "p", "w", "a", nil, nil, revisions, time.Time{}) }},
		{"blank MessageRef", func() (Snapshot, error) {
			return NewSnapshot("s", "p", "w", "a", []MessageRef{{MessageID: ""}}, nil, revisions, now)
		}},
		{"blank ResourceRef key", func() (Snapshot, error) {
			return NewSnapshot("s", "p", "w", "a", nil, []ResourceRef{{ResourceKey: "", ContentHash: "h"}}, revisions, now)
		}},
		{"blank ResourceRef hash", func() (Snapshot, error) {
			return NewSnapshot("s", "p", "w", "a", nil, []ResourceRef{{ResourceKey: "k", ContentHash: ""}}, revisions, now)
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

func TestNewSnapshot_ManifestHash_OrderSensitive(t *testing.T) {
	revisions := validRevisions(t)
	now := time.Now()

	forward, err := NewSnapshot("s1", "p", "w", "a", []MessageRef{{MessageID: "m1"}, {MessageID: "m2"}}, nil, revisions, now)
	if err != nil {
		t.Fatalf("NewSnapshot (forward): %v", err)
	}
	reversed, err := NewSnapshot("s2", "p", "w", "a", []MessageRef{{MessageID: "m2"}, {MessageID: "m1"}}, nil, revisions, now)
	if err != nil {
		t.Fatalf("NewSnapshot (reversed): %v", err)
	}
	if forward.ManifestHash == reversed.ManifestHash {
		t.Fatal("ManifestHash must differ when MessageRefs order differs — order is semantically meaningful (HE-04-M06)")
	}
}

func TestNewSnapshot_ManifestHash_DeterministicForIdenticalContent(t *testing.T) {
	revisions := validRevisions(t)
	now := time.Now()
	refs := []MessageRef{{MessageID: "m1"}}

	a, err := NewSnapshot("s1", "p", "w", "a", refs, nil, revisions, now)
	if err != nil {
		t.Fatalf("NewSnapshot (a): %v", err)
	}
	b, err := NewSnapshot("s2", "p", "w", "a", refs, nil, revisions, now)
	if err != nil {
		t.Fatalf("NewSnapshot (b): %v", err)
	}
	if a.ManifestHash != b.ManifestHash {
		t.Fatalf("ManifestHash = %q vs %q, want identical for identical manifest content", a.ManifestHash, b.ManifestHash)
	}
}

func TestNewSnapshot_ManifestHash_ChangesWithRevisions(t *testing.T) {
	now := time.Now()
	refs := []MessageRef{{MessageID: "m1"}}
	revisionsA := validRevisions(t)
	revisionsB, err := workspace.NewRevisionSet([]workspace.Revision{
		{RepositoryID: "repo-1", VCSObjectID: "different-commit", WorkspaceGeneration: 1},
	})
	if err != nil {
		t.Fatalf("NewRevisionSet: %v", err)
	}

	a, err := NewSnapshot("s1", "p", "w", "a", refs, nil, revisionsA, now)
	if err != nil {
		t.Fatalf("NewSnapshot (a): %v", err)
	}
	b, err := NewSnapshot("s2", "p", "w", "a", refs, nil, revisionsB, now)
	if err != nil {
		t.Fatalf("NewSnapshot (b): %v", err)
	}
	if a.ManifestHash == b.ManifestHash {
		t.Fatal("ManifestHash must differ when the pinned RevisionSet differs")
	}
}
