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

// --- V9-07: omitted message refs ---

func omittedFixture(ids ...string) []OmittedMessageRef {
	refs := make([]OmittedMessageRef, len(ids))
	for i, id := range ids {
		refs[i] = OmittedMessageRef{MessageID: id, Reason: OmittedMessageBudgetExceeded}
	}
	return refs
}

// A Snapshot that omitted nothing hashes exactly as before V9-07 (the pinned
// value is the one TestNewSnapshot_WithoutInstructionSchemaVersion... holds),
// while one that omitted a message hashes differently, and a different
// omitted list is a different manifest.
func TestNewSnapshot_OmittedMessageRefs_ArePartOfTheManifestHash(t *testing.T) {
	none := schemaFixtureSnapshot(t, WithOmittedMessageRefs(nil))
	if want := "sha256:4a2bc61a47d81fff167f5fc8ce9c3fc0762736315e7b28bc881d4bef2a7b5fc4"; none.ManifestHash != want {
		t.Fatalf("a snapshot without omitted messages hashes %s, want the unchanged %s", none.ManifestHash, want)
	}
	one := schemaFixtureSnapshot(t, WithOmittedMessageRefs(omittedFixture("message-000")))
	two := schemaFixtureSnapshot(t, WithOmittedMessageRefs(omittedFixture("message-000", "message-003")))
	if one.ManifestHash == none.ManifestHash || one.ManifestHash == two.ManifestHash {
		t.Fatalf("omitted lists %v, %v and none share a hash", one.ManifestHash, two.ManifestHash)
	}
}

func TestNewSnapshot_OmittedMessageRefs_Validation(t *testing.T) {
	build := func(refs []OmittedMessageRef) error {
		_, err := NewSnapshot("snap-1", "project-1", "work-item-1", "attempt-1",
			[]MessageRef{{MessageID: "message-001"}}, nil, nil, validRevisions(t), time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC),
			WithOmittedMessageRefs(refs))
		return err
	}
	for name, refs := range map[string][]OmittedMessageRef{
		"blank id":             {{MessageID: " ", Reason: OmittedMessageBudgetExceeded}},
		"unknown reason":       {{MessageID: "message-000", Reason: "BECAUSE"}},
		"repeated":             omittedFixture("message-000", "message-000"),
		"also an included ref": omittedFixture("message-001"),
		"blank reason":         {{MessageID: "message-000"}},
	} {
		if err := build(refs); err == nil {
			t.Errorf("%s: NewSnapshot accepted %v", name, refs)
		}
	}
	if err := build(omittedFixture("message-000", "message-002")); err != nil {
		t.Errorf("a valid omitted list was rejected: %v", err)
	}
}

func TestCloneForAttempt_CarriesOmittedMessageRefs(t *testing.T) {
	original := schemaFixtureSnapshot(t, WithInstructionSchemaVersion(InstructionSchemaV2), WithOmittedMessageRefs(omittedFixture("message-000")))
	clone, err := original.CloneForAttempt("snap-2", "attempt-2", time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("CloneForAttempt: %v", err)
	}
	if len(clone.OmittedMessageRefs) != 1 || clone.OmittedMessageRefs[0].MessageID != "message-000" || clone.ManifestHash != original.ManifestHash {
		t.Fatalf("clone omitted refs = %+v hash %s, want message-000 and the original's %s", clone.OmittedMessageRefs, clone.ManifestHash, original.ManifestHash)
	}
}

// --- V9-10: repository instruction files ---

func instructionFileFixture(repository, path string, size int64) InstructionFileRef {
	return InstructionFileRef{RepositoryID: repository, Path: path, SHA256: "sha256:" + path, SizeBytes: size, WarnLimitBytes: 1000}
}

// A Snapshot that pinned no instruction file hashes exactly as before V9-10, and
// one that pinned a file hashes differently — and depends on which file.
func TestNewSnapshot_RepositoryInstructionFiles_ArePartOfTheManifestHash(t *testing.T) {
	none := schemaFixtureSnapshot(t, WithRepositoryInstructionFiles(nil))
	if want := "sha256:4a2bc61a47d81fff167f5fc8ce9c3fc0762736315e7b28bc881d4bef2a7b5fc4"; none.ManifestHash != want {
		t.Fatalf("a snapshot without instruction files hashes %s, want the unchanged %s", none.ManifestHash, want)
	}
	claude := schemaFixtureSnapshot(t, WithRepositoryInstructionFiles([]InstructionFileRef{instructionFileFixture("repo-1", "CLAUDE.md", 10)}))
	other := schemaFixtureSnapshot(t, WithRepositoryInstructionFiles([]InstructionFileRef{instructionFileFixture("repo-1", "CLAUDE.md", 11)}))
	if claude.ManifestHash == none.ManifestHash || claude.ManifestHash == other.ManifestHash {
		t.Fatalf("hashes %s, %s and %s are not all different", none.ManifestHash, claude.ManifestHash, other.ManifestHash)
	}
}

func TestNewSnapshot_RepositoryInstructionFiles_AreSortedAndValidated(t *testing.T) {
	sorted := schemaFixtureSnapshot(t, WithRepositoryInstructionFiles([]InstructionFileRef{
		instructionFileFixture("repo-2", "AGENTS.md", 1), instructionFileFixture("repo-1", "CLAUDE.md", 2), instructionFileFixture("repo-1", "AGENTS.md", 3),
	}))
	got := sorted.RepositoryInstructionFiles
	if len(got) != 3 || got[0].Path != "AGENTS.md" || got[0].RepositoryID != "repo-1" || got[1].Path != "CLAUDE.md" || got[2].RepositoryID != "repo-2" {
		t.Fatalf("order = %+v, want repository then path", got)
	}
	reordered := schemaFixtureSnapshot(t, WithRepositoryInstructionFiles([]InstructionFileRef{
		instructionFileFixture("repo-1", "AGENTS.md", 3), instructionFileFixture("repo-2", "AGENTS.md", 1), instructionFileFixture("repo-1", "CLAUDE.md", 2),
	}))
	if reordered.ManifestHash != sorted.ManifestHash {
		t.Fatal("the same files in another order hash differently; the manifest must not depend on lookup order")
	}

	build := func(files ...InstructionFileRef) error {
		_, err := NewSnapshot("snap-1", "project-1", "work-item-1", "attempt-1", nil, nil, nil, validRevisions(t),
			time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC), WithRepositoryInstructionFiles(files))
		return err
	}
	for name, files := range map[string][]InstructionFileRef{
		"blank repository": {{Path: "CLAUDE.md", SHA256: "sha256:x"}},
		"blank path":       {{RepositoryID: "repo-1", SHA256: "sha256:x"}},
		"blank hash":       {{RepositoryID: "repo-1", Path: "CLAUDE.md"}},
		"negative size":    {{RepositoryID: "repo-1", Path: "CLAUDE.md", SHA256: "sha256:x", SizeBytes: -1}},
		"duplicate":        {instructionFileFixture("repo-1", "CLAUDE.md", 1), instructionFileFixture("repo-1", "CLAUDE.md", 2)},
	} {
		if err := build(files...); err == nil {
			t.Errorf("%s: NewSnapshot accepted %+v", name, files)
		}
	}
}

func TestInstructionFileRef_Oversized(t *testing.T) {
	for _, tt := range []struct {
		size, limit int64
		want        bool
	}{{100, 1000, false}, {1000, 1000, false}, {1001, 1000, true}, {5000, 0, false}} {
		if got := (InstructionFileRef{SizeBytes: tt.size, WarnLimitBytes: tt.limit}).Oversized(); got != tt.want {
			t.Errorf("size %d limit %d: Oversized = %v, want %v", tt.size, tt.limit, got, tt.want)
		}
	}
}

func TestCloneForAttempt_CarriesRepositoryInstructionFiles(t *testing.T) {
	original := schemaFixtureSnapshot(t, WithRepositoryInstructionFiles([]InstructionFileRef{instructionFileFixture("repo-1", "CLAUDE.md", 10)}))
	clone, err := original.CloneForAttempt("snap-2", "attempt-2", time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC))
	if err != nil || len(clone.RepositoryInstructionFiles) != 1 || clone.ManifestHash != original.ManifestHash {
		t.Fatalf("clone = %+v (%v), want the file and the original's hash %s", clone.RepositoryInstructionFiles, err, original.ManifestHash)
	}
}
