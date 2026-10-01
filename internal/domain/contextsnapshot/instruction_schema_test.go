package contextsnapshot

import (
	"testing"
	"time"
)

// V9-03 (ADR-032 decision 2): the instruction schema version a Snapshot
// records, its place in the manifest hash, and the clone that carries it.

func schemaFixtureSnapshot(t *testing.T, options ...Option) Snapshot {
	t.Helper()
	snap, err := NewSnapshot(
		"snap-1", "project-1", "work-item-1", "attempt-1",
		[]MessageRef{{MessageID: "message-001"}, {MessageID: "message-002"}},
		[]ResourceRef{
			{OwnerVersionID: "skill-v3", ResourceKey: "api-compat", ContentHash: "sha256:1111111111111111111111111111111111111111111111111111111111111111"},
			{ResourceKey: "legacy-rule", ContentHash: "sha256:2222222222222222222222222222222222222222222222222222222222222222"},
		},
		[]EvidenceRef{{EvidenceID: "evidence-2"}, {EvidenceID: "evidence-1"}},
		validRevisions(t), time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC), options...,
	)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	return snap
}

// TestNewSnapshot_WithoutInstructionSchemaVersion_HashesExactlyAsBeforeV903
// pins ManifestHash values taken from the code as it was before V9-03: a
// Snapshot that records no version is a v1 Snapshot and must keep hashing the
// same, or loadSnapshotTx's tamper check would reject every row written before
// the column existed.
func TestNewSnapshot_WithoutInstructionSchemaVersion_HashesExactlyAsBeforeV903(t *testing.T) {
	full := schemaFixtureSnapshot(t)
	if want := "sha256:4a2bc61a47d81fff167f5fc8ce9c3fc0762736315e7b28bc881d4bef2a7b5fc4"; full.ManifestHash != want {
		t.Fatalf("ManifestHash = %s, want the pre-V9-03 %s", full.ManifestHash, want)
	}
	minimal, err := NewSnapshot(
		"snap-1", "project-1", "work-item-1", "attempt-1",
		[]MessageRef{{MessageID: "message-001"}}, nil, nil, validRevisions(t), time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	if want := "sha256:0ff6d9baa8df217e01bf8f506b4ce9d247767f49698ef9da7ea97f8a0b20d206"; minimal.ManifestHash != want {
		t.Fatalf("ManifestHash = %s, want the pre-V9-03 %s", minimal.ManifestHash, want)
	}
	if full.InstructionSchemaVersion != 0 || full.InstructionSchema() != InstructionSchemaV1 {
		t.Fatalf("version = %d / effective %d, want 0 recorded and v1 effective", full.InstructionSchemaVersion, full.InstructionSchema())
	}
	// Recording 0 is recording nothing.
	if explicit := schemaFixtureSnapshot(t, WithInstructionSchemaVersion(0)); explicit.ManifestHash != full.ManifestHash {
		t.Fatalf("WithInstructionSchemaVersion(0) hash = %s, want the unversioned %s", explicit.ManifestHash, full.ManifestHash)
	}
}

func TestNewSnapshot_InstructionSchemaV2_IsPartOfTheManifest(t *testing.T) {
	v1 := schemaFixtureSnapshot(t)
	v2 := schemaFixtureSnapshot(t, WithInstructionSchemaVersion(InstructionSchemaV2))
	if v2.InstructionSchemaVersion != InstructionSchemaV2 || v2.InstructionSchema() != InstructionSchemaV2 {
		t.Fatalf("v2 snapshot version = %d / effective %d, want 2", v2.InstructionSchemaVersion, v2.InstructionSchema())
	}
	if v1.ManifestHash == v2.ManifestHash {
		t.Fatal("the same refs render to a different instruction under v2, so the manifest hash must differ")
	}
	if again := schemaFixtureSnapshot(t, WithInstructionSchemaVersion(InstructionSchemaV2)); again.ManifestHash != v2.ManifestHash {
		t.Fatalf("v2 hash not deterministic: %s vs %s", again.ManifestHash, v2.ManifestHash)
	}
}

func TestNewSnapshot_RejectsAnUnknownInstructionSchemaVersion(t *testing.T) {
	for _, version := range []int{1, 3, -1} {
		if _, err := NewSnapshot("s", "p", "w", "a", nil, nil, nil, validRevisions(t), time.Now(), WithInstructionSchemaVersion(version)); err == nil {
			t.Fatalf("NewSnapshot accepted instruction schema version %d (only none and %d are storable)", version, InstructionSchemaV2)
		}
	}
}

// TestSnapshot_CloneForAttempt_CarriesTheInstructionSchemaVersion: a retry or
// recovery clone gets a new identity and the old refs, and renders with the
// same schema as the snapshot it clones — an old v1 attempt's retry stays v1.
func TestSnapshot_CloneForAttempt_CarriesTheInstructionSchemaVersion(t *testing.T) {
	for name, options := range map[string][]Option{
		"v1": nil,
		"v2": {WithInstructionSchemaVersion(InstructionSchemaV2)},
	} {
		t.Run(name, func(t *testing.T) {
			original := schemaFixtureSnapshot(t, options...)
			clonedAt := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
			clone, err := original.CloneForAttempt("snap-2", "attempt-2", clonedAt)
			if err != nil {
				t.Fatalf("CloneForAttempt: %v", err)
			}
			if clone.ID != "snap-2" || clone.AttemptID != "attempt-2" || !clone.CreatedAt.Equal(clonedAt) {
				t.Fatalf("clone identity = %s / %s / %v, want snap-2 / attempt-2 / %v", clone.ID, clone.AttemptID, clone.CreatedAt, clonedAt)
			}
			if clone.InstructionSchemaVersion != original.InstructionSchemaVersion {
				t.Fatalf("clone version = %d, want the original's %d", clone.InstructionSchemaVersion, original.InstructionSchemaVersion)
			}
			if clone.ProjectID != original.ProjectID || clone.WorkItemID != original.WorkItemID ||
				len(clone.MessageRefs) != 2 || len(clone.ResourceRefs) != 2 || len(clone.EvidenceRefs) != 2 {
				t.Fatalf("clone = %+v, want the original's project, work item and refs", clone)
			}
			if clone.ManifestHash != original.ManifestHash {
				t.Fatalf("clone hash = %s, want the original's %s (same manifest content)", clone.ManifestHash, original.ManifestHash)
			}
		})
	}
}
