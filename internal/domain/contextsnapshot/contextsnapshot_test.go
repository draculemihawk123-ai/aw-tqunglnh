package contextsnapshot

import (
	"crypto/sha256"
	"encoding/hex"
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
		nil,
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
	// A snapshot with zero messages/resources/evidence is not itself
	// invalid at the domain level (e.g. a resolution that selected
	// nothing, or a MAKER/COMMAND/MACHINE_GATE Attempt, which never has
	// EvidenceRefs) — only blank individual ref values are rejected.
	_, err := NewSnapshot(
		"snap-1", "project-1", "work-item-1", "attempt-1",
		nil, nil, nil, validRevisions(t), time.Now(),
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
		{"empty ID", func() (Snapshot, error) { return NewSnapshot("", "p", "w", "a", nil, nil, nil, revisions, now) }},
		{"empty ProjectID", func() (Snapshot, error) { return NewSnapshot("s", "", "w", "a", nil, nil, nil, revisions, now) }},
		{"empty WorkItemID", func() (Snapshot, error) { return NewSnapshot("s", "p", "", "a", nil, nil, nil, revisions, now) }},
		{"empty AttemptID", func() (Snapshot, error) { return NewSnapshot("s", "p", "w", "", nil, nil, nil, revisions, now) }},
		{"zero CreatedAt", func() (Snapshot, error) {
			return NewSnapshot("s", "p", "w", "a", nil, nil, nil, revisions, time.Time{})
		}},
		{"blank MessageRef", func() (Snapshot, error) {
			return NewSnapshot("s", "p", "w", "a", []MessageRef{{MessageID: ""}}, nil, nil, revisions, now)
		}},
		{"blank ResourceRef key", func() (Snapshot, error) {
			return NewSnapshot("s", "p", "w", "a", nil, []ResourceRef{{ResourceKey: "", ContentHash: "h"}}, nil, revisions, now)
		}},
		{"blank ResourceRef hash", func() (Snapshot, error) {
			return NewSnapshot("s", "p", "w", "a", nil, []ResourceRef{{ResourceKey: "k", ContentHash: ""}}, nil, revisions, now)
		}},
		{"blank EvidenceRef", func() (Snapshot, error) {
			return NewSnapshot("s", "p", "w", "a", nil, nil, []EvidenceRef{{EvidenceID: ""}}, revisions, now)
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

	forward, err := NewSnapshot("s1", "p", "w", "a", []MessageRef{{MessageID: "m1"}, {MessageID: "m2"}}, nil, nil, revisions, now)
	if err != nil {
		t.Fatalf("NewSnapshot (forward): %v", err)
	}
	reversed, err := NewSnapshot("s2", "p", "w", "a", []MessageRef{{MessageID: "m2"}, {MessageID: "m1"}}, nil, nil, revisions, now)
	if err != nil {
		t.Fatalf("NewSnapshot (reversed): %v", err)
	}
	if forward.ManifestHash == reversed.ManifestHash {
		t.Fatal("ManifestHash must differ when MessageRefs order differs — order is semantically meaningful (HE-04-M06)")
	}
}

// TestNewSnapshot_EvidenceRefs_OrderInsensitive is EvidenceRefs' own
// counterpart to TestNewSnapshot_ManifestHash_OrderSensitive above — the
// opposite behavior, deliberately: unlike MessageRefs (a rendered
// transcript, where order is semantically meaningful), EvidenceRefs has no
// "rendering order" of its own (see EvidenceRef's own doc comment), so
// NewSnapshot sorts it and two Snapshots differing only in EvidenceRefs
// input order must hash identically.
func TestNewSnapshot_EvidenceRefs_OrderInsensitive(t *testing.T) {
	revisions := validRevisions(t)
	now := time.Now()

	forward, err := NewSnapshot("s1", "p", "w", "a", nil, nil, []EvidenceRef{{EvidenceID: "e1"}, {EvidenceID: "e2"}}, revisions, now)
	if err != nil {
		t.Fatalf("NewSnapshot (forward): %v", err)
	}
	reversed, err := NewSnapshot("s2", "p", "w", "a", nil, nil, []EvidenceRef{{EvidenceID: "e2"}, {EvidenceID: "e1"}}, revisions, now)
	if err != nil {
		t.Fatalf("NewSnapshot (reversed): %v", err)
	}
	if forward.ManifestHash != reversed.ManifestHash {
		t.Fatal("ManifestHash must be identical regardless of EvidenceRefs input order — order carries no meaning here")
	}
	if len(forward.EvidenceRefs) != 2 || forward.EvidenceRefs[0].EvidenceID != "e1" || forward.EvidenceRefs[1].EvidenceID != "e2" {
		t.Fatalf("EvidenceRefs = %+v, want sorted [e1 e2]", forward.EvidenceRefs)
	}
}

func TestNewSnapshot_ManifestHash_DeterministicForIdenticalContent(t *testing.T) {
	revisions := validRevisions(t)
	now := time.Now()
	refs := []MessageRef{{MessageID: "m1"}}

	a, err := NewSnapshot("s1", "p", "w", "a", refs, nil, nil, revisions, now)
	if err != nil {
		t.Fatalf("NewSnapshot (a): %v", err)
	}
	b, err := NewSnapshot("s2", "p", "w", "a", refs, nil, nil, revisions, now)
	if err != nil {
		t.Fatalf("NewSnapshot (b): %v", err)
	}
	if a.ManifestHash != b.ManifestHash {
		t.Fatalf("ManifestHash = %q vs %q, want identical for identical manifest content", a.ManifestHash, b.ManifestHash)
	}
}

// TestNewSnapshot_ManifestHash_BackwardCompatibleWithoutOwnerVersionID proves
// V5-08B0's own addition of ResourceRef.OwnerVersionID does not change the
// ManifestHash of a Snapshot whose ResourceRefs were written before this
// field existed (an empty OwnerVersionID, the only value any pre-V5-08B0
// row could ever have). This is exactly the tamper-check
// loadSnapshotTx (internal/adapters/sqlite) re-runs on every load: if this
// test ever failed, every Snapshot row stored before this field was added
// would start failing ErrImmutableVersionConflict on the very next read.
func TestNewSnapshot_ManifestHash_BackwardCompatibleWithoutOwnerVersionID(t *testing.T) {
	revisions := validRevisions(t)
	now := time.Now()

	withEmptyOwner, err := NewSnapshot("s1", "p", "w", "a",
		nil, []ResourceRef{{ResourceKey: "res-1", ContentHash: "hash-1"}}, nil, revisions, now)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}

	// The exact JSON a pre-V5-08B0 ResourceRef{ResourceKey, ContentHash}
	// (no OwnerVersionID field in the Go type at all) would have produced,
	// hashed through the identical canonicalManifest wrapper this package
	// has always used. No "evidenceRefs" key either — V5-12 predates this
	// row too, and EvidenceRefs is omitempty for exactly this reason.
	legacyJSON := `{"messageRefs":null,"resourceRefs":[{"ResourceKey":"res-1","ContentHash":"hash-1"}],"revisionSetHash":"` + revisions.ContentHash() + `"}`
	sum := sha256.Sum256([]byte(legacyJSON))
	legacyHash := "sha256:" + hex.EncodeToString(sum[:])

	if withEmptyOwner.ManifestHash != legacyHash {
		t.Fatalf("ManifestHash = %q, want %q (a pre-V5-08B0 row's own hash) — OwnerVersionID must be omitempty so an old row's JSON re-marshals identically", withEmptyOwner.ManifestHash, legacyHash)
	}

	withOwner, err := NewSnapshot("s2", "p", "w", "a",
		nil, []ResourceRef{{OwnerVersionID: "owner-1", ResourceKey: "res-1", ContentHash: "hash-1"}}, nil, revisions, now)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	if withOwner.ManifestHash == withEmptyOwner.ManifestHash {
		t.Fatal("ManifestHash must differ once OwnerVersionID is populated — it is part of the manifest content now")
	}
}

// TestNewSnapshot_ManifestHash_BackwardCompatibleWithoutEvidenceRefs is
// TestNewSnapshot_ManifestHash_BackwardCompatibleWithoutOwnerVersionID's own
// sibling for V5-12's own EvidenceRefs addition (2026-09-10): a Snapshot
// with no EvidenceRefs (every MAKER/COMMAND/MACHINE_GATE Attempt, and
// every pre-V5-12 row) must keep hashing identically to what it hashed
// before this field existed — the same "old row still re-verifies" bar
// loadSnapshotTx's own tamper check depends on.
func TestNewSnapshot_ManifestHash_BackwardCompatibleWithoutEvidenceRefs(t *testing.T) {
	revisions := validRevisions(t)
	now := time.Now()

	withoutEvidence, err := NewSnapshot("s1", "p", "w", "a", nil, nil, nil, revisions, now)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}

	legacyJSON := `{"messageRefs":null,"resourceRefs":null,"revisionSetHash":"` + revisions.ContentHash() + `"}`
	sum := sha256.Sum256([]byte(legacyJSON))
	legacyHash := "sha256:" + hex.EncodeToString(sum[:])

	if withoutEvidence.ManifestHash != legacyHash {
		t.Fatalf("ManifestHash = %q, want %q (identical to a pre-V5-12 row's own hash) — EvidenceRefs must be omitempty when empty", withoutEvidence.ManifestHash, legacyHash)
	}

	withEvidence, err := NewSnapshot("s2", "p", "w", "a", nil, nil, []EvidenceRef{{EvidenceID: "evidence-1"}}, revisions, now)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	if withEvidence.ManifestHash == withoutEvidence.ManifestHash {
		t.Fatal("ManifestHash must differ once EvidenceRefs is populated — it is part of the manifest content now")
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

	a, err := NewSnapshot("s1", "p", "w", "a", refs, nil, nil, revisionsA, now)
	if err != nil {
		t.Fatalf("NewSnapshot (a): %v", err)
	}
	b, err := NewSnapshot("s2", "p", "w", "a", refs, nil, nil, revisionsB, now)
	if err != nil {
		t.Fatalf("NewSnapshot (b): %v", err)
	}
	if a.ManifestHash == b.ManifestHash {
		t.Fatal("ManifestHash must differ when the pinned RevisionSet differs")
	}
}
