package engineeringpack_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/engineeringpack"
)

func TestResolvePackGraph_SimpleResolution(t *testing.T) {
	packs := engineeringpack.PackDependencies{
		"pack-root-v1": {
			{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: "skill-1-v1"},
			{Kind: definition.KindLayer, DefinitionID: "layer-1", VersionID: "layer-1-v1"},
		},
	}
	manifest, err := engineeringpack.ResolvePackGraph("pack-root-v1", packs)
	if err != nil {
		t.Fatalf("ResolvePackGraph: %v", err)
	}
	if len(manifest.Packs) != 1 || manifest.Packs[0] != "pack-root-v1" {
		t.Fatalf("Packs = %v, want [pack-root-v1]", manifest.Packs)
	}
	if len(manifest.Resources) != 2 {
		t.Fatalf("Resources = %v, want 2 entries", manifest.Resources)
	}
}

// TestResolvePackGraph_TransitiveThroughNestedPack is the "pack graph
// resolution" case itself: a resource declared by a nested Engineering
// Pack (not root's own direct dependency) must still end up in the
// resolved manifest.
func TestResolvePackGraph_TransitiveThroughNestedPack(t *testing.T) {
	packs := engineeringpack.PackDependencies{
		"pack-root-v1": {
			{Kind: definition.KindEngineeringPack, DefinitionID: "pack-nested", VersionID: "pack-nested-v1"},
		},
		"pack-nested-v1": {
			{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: "skill-1-v1"},
		},
	}
	manifest, err := engineeringpack.ResolvePackGraph("pack-root-v1", packs)
	if err != nil {
		t.Fatalf("ResolvePackGraph: %v", err)
	}
	if len(manifest.Packs) != 2 {
		t.Fatalf("Packs = %v, want 2 entries (root + nested)", manifest.Packs)
	}
	if len(manifest.Resources) != 1 || manifest.Resources[0].DefinitionID != "skill-1" {
		t.Fatalf("Resources = %v, want the nested pack's skill-1 pin", manifest.Resources)
	}
}

// TestResolvePackGraph_DedupSharedResource is lec-04's own Merge policy:
// "Resource list được hợp nhất, khử trùng theo stable ID/version" — two
// different packs pulling in the exact same Skill version must collapse
// to one manifest entry.
func TestResolvePackGraph_DedupSharedResource(t *testing.T) {
	sharedPin := definition.DependencyPin{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: "skill-1-v1"}
	packs := engineeringpack.PackDependencies{
		"pack-root-v1": {
			{Kind: definition.KindEngineeringPack, DefinitionID: "pack-b", VersionID: "pack-b-v1"},
			{Kind: definition.KindEngineeringPack, DefinitionID: "pack-c", VersionID: "pack-c-v1"},
		},
		"pack-b-v1": {sharedPin},
		"pack-c-v1": {sharedPin},
	}
	manifest, err := engineeringpack.ResolvePackGraph("pack-root-v1", packs)
	if err != nil {
		t.Fatalf("ResolvePackGraph: %v", err)
	}
	if len(manifest.Resources) != 1 {
		t.Fatalf("Resources = %v, want exactly 1 deduplicated entry", manifest.Resources)
	}
}

func TestResolvePackGraph_ResourceOrderIsDeterministicRegardlessOfInputOrder(t *testing.T) {
	packsA := engineeringpack.PackDependencies{
		"pack-root-v1": {
			{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: "skill-1-v1"},
			{Kind: definition.KindLayer, DefinitionID: "layer-1", VersionID: "layer-1-v1"},
		},
	}
	packsB := engineeringpack.PackDependencies{
		"pack-root-v1": {
			{Kind: definition.KindLayer, DefinitionID: "layer-1", VersionID: "layer-1-v1"},
			{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: "skill-1-v1"},
		},
	}
	manifestA, err := engineeringpack.ResolvePackGraph("pack-root-v1", packsA)
	if err != nil {
		t.Fatalf("ResolvePackGraph(A): %v", err)
	}
	manifestB, err := engineeringpack.ResolvePackGraph("pack-root-v1", packsB)
	if err != nil {
		t.Fatalf("ResolvePackGraph(B): %v", err)
	}
	if manifestA.Resources[0] != manifestB.Resources[0] || manifestA.Resources[1] != manifestB.Resources[1] {
		t.Fatalf("resolved resource order should not depend on authored pin order: %v vs %v",
			manifestA.Resources, manifestB.Resources)
	}
}

// TestResolvePackGraph_DetectsTwoPackCycle is V2-06's own cycle test:
// two packs pinning each other must fail closed, never resolve via an
// arbitrary traversal order.
func TestResolvePackGraph_DetectsTwoPackCycle(t *testing.T) {
	packs := engineeringpack.PackDependencies{
		"pack-a-v1": {
			{Kind: definition.KindEngineeringPack, DefinitionID: "pack-b", VersionID: "pack-b-v1"},
		},
		"pack-b-v1": {
			{Kind: definition.KindEngineeringPack, DefinitionID: "pack-a", VersionID: "pack-a-v1"},
		},
	}
	_, err := engineeringpack.ResolvePackGraph("pack-a-v1", packs)
	if !errors.Is(err, engineeringpack.ErrPackCycle) {
		t.Fatalf("ResolvePackGraph should fail closed with ErrPackCycle, got %v", err)
	}
}

func TestResolvePackGraph_DetectsSelfCycle(t *testing.T) {
	packs := engineeringpack.PackDependencies{
		"pack-a-v1": {
			{Kind: definition.KindEngineeringPack, DefinitionID: "pack-a", VersionID: "pack-a-v1"},
		},
	}
	_, err := engineeringpack.ResolvePackGraph("pack-a-v1", packs)
	if !errors.Is(err, engineeringpack.ErrPackCycle) {
		t.Fatalf("ResolvePackGraph should fail closed with ErrPackCycle for a self-reference, got %v", err)
	}
}

// TestResolvePackGraph_DiamondIsNotACycle makes sure the cycle detector
// does not over-fire: two packs independently depending on the same
// third pack (a diamond, not a cycle) must resolve cleanly.
func TestResolvePackGraph_DiamondIsNotACycle(t *testing.T) {
	packs := engineeringpack.PackDependencies{
		"pack-root-v1": {
			{Kind: definition.KindEngineeringPack, DefinitionID: "pack-b", VersionID: "pack-b-v1"},
			{Kind: definition.KindEngineeringPack, DefinitionID: "pack-c", VersionID: "pack-c-v1"},
		},
		"pack-b-v1": {{Kind: definition.KindEngineeringPack, DefinitionID: "pack-d", VersionID: "pack-d-v1"}},
		"pack-c-v1": {{Kind: definition.KindEngineeringPack, DefinitionID: "pack-d", VersionID: "pack-d-v1"}},
		"pack-d-v1": {{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: "skill-1-v1"}},
	}
	manifest, err := engineeringpack.ResolvePackGraph("pack-root-v1", packs)
	if err != nil {
		t.Fatalf("ResolvePackGraph should accept a diamond shape: %v", err)
	}
	if len(manifest.Packs) != 4 {
		t.Fatalf("Packs = %v, want 4 entries (root, b, c, d)", manifest.Packs)
	}
	if len(manifest.Resources) != 1 {
		t.Fatalf("Resources = %v, want exactly 1 deduplicated entry", manifest.Resources)
	}
}

// TestResolvePackGraph_UnknownNestedPackIsRecordedButNotExpanded
// documents this package's own design decision: a pin naming a pack
// version the caller did not supply data for is not an error (a caller
// resolving incrementally may not have fetched every pack yet) — it is
// simply left unexpanded.
func TestResolvePackGraph_UnknownNestedPackIsRecordedButNotExpanded(t *testing.T) {
	packs := engineeringpack.PackDependencies{
		"pack-root-v1": {
			{Kind: definition.KindEngineeringPack, DefinitionID: "pack-unknown", VersionID: "pack-unknown-v1"},
			{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: "skill-1-v1"},
		},
	}
	manifest, err := engineeringpack.ResolvePackGraph("pack-root-v1", packs)
	if err != nil {
		t.Fatalf("ResolvePackGraph should not error on an unknown nested pack: %v", err)
	}
	if len(manifest.Resources) != 1 {
		t.Fatalf("Resources = %v, want only skill-1 (the unknown pack cannot be expanded)", manifest.Resources)
	}
}

func resolvedResource(key, ownerVersionID, contentHash string, priority definition.PriorityClass) definition.ResolvedResource {
	return definition.ResolvedResource{
		Identity: definition.ResourceIdentity{
			OwnerVersionID: ownerVersionID, ResourceKey: key, ContentHash: contentHash,
		},
		OwnerKind: definition.KindSkill,
		Priority:  priority,
	}
}

func TestCheckResourceConflicts_NoConflictWhenContentIdentical(t *testing.T) {
	resources := []definition.ResolvedResource{
		resolvedResource("shared-key", "skill-a-v1", "sha256:same", definition.PriorityHardConstraint),
		resolvedResource("shared-key", "skill-b-v1", "sha256:same", definition.PriorityHardConstraint),
	}
	if err := engineeringpack.CheckResourceConflicts(resources); err != nil {
		t.Fatalf("CheckResourceConflicts should allow identical content under the same key: %v", err)
	}
}

func TestCheckResourceConflicts_NoErrorWithoutHardConstraint(t *testing.T) {
	resources := []definition.ResolvedResource{
		resolvedResource("shared-key", "skill-a-v1", "sha256:one", definition.PriorityGuidance),
		resolvedResource("shared-key", "skill-b-v1", "sha256:two", definition.PriorityGuidance),
	}
	if err := engineeringpack.CheckResourceConflicts(resources); err != nil {
		t.Fatalf("CheckResourceConflicts should not fail closed without a HARD_CONSTRAINT involved: %v", err)
	}
}

// TestCheckResourceConflicts_FailsClosedOnHardConstraintConflict is
// lec-04's own acceptance test #2 (phép thử chấp nhận): two hard
// constraints applying at once with different content must fail, never
// silently resolve with a "last wins" pick.
func TestCheckResourceConflicts_FailsClosedOnHardConstraintConflict(t *testing.T) {
	resources := []definition.ResolvedResource{
		resolvedResource("java-baseline", "skill-a-v1", "sha256:one", definition.PriorityHardConstraint),
		resolvedResource("java-baseline", "skill-b-v1", "sha256:two", definition.PriorityGuidance),
	}
	err := engineeringpack.CheckResourceConflicts(resources)
	if !errors.Is(err, engineeringpack.ErrResourceConflict) {
		t.Fatalf("CheckResourceConflicts should fail closed with ErrResourceConflict, got %v", err)
	}
}

func TestCheckResourceConflicts_DifferentKeysNeverConflict(t *testing.T) {
	resources := []definition.ResolvedResource{
		resolvedResource("key-a", "skill-a-v1", "sha256:one", definition.PriorityHardConstraint),
		resolvedResource("key-b", "skill-b-v1", "sha256:two", definition.PriorityHardConstraint),
	}
	if err := engineeringpack.CheckResourceConflicts(resources); err != nil {
		t.Fatalf("CheckResourceConflicts should never conflict across different keys: %v", err)
	}
}

// TestResolvePackGraph_GoldenManifest is V2-06's own "golden manifest
// tests" requirement: a representative multi-pack graph's resolved
// manifest is compared against a checked-in fixture, catching any change
// to ResolvePackGraph's dedup/sort/traversal behavior.
func TestResolvePackGraph_GoldenManifest(t *testing.T) {
	packs := engineeringpack.PackDependencies{
		"pack-root-v1": {
			{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: "skill-1-v1"},
			{Kind: definition.KindEngineeringPack, DefinitionID: "pack-nested", VersionID: "pack-nested-v1"},
		},
		"pack-nested-v1": {
			{Kind: definition.KindLayer, DefinitionID: "layer-1", VersionID: "layer-1-v1"},
			{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: "skill-1-v1"},
		},
	}
	manifest, err := engineeringpack.ResolvePackGraph("pack-root-v1", packs)
	if err != nil {
		t.Fatalf("ResolvePackGraph: %v", err)
	}

	got, _, err := authoring.Canonicalize(manifest, authoring.CanonicalizeOptions{})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}

	goldenPath := filepath.Join("testdata", "golden", "pack-manifest-example.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	if string(got)+"\n" != string(want) {
		t.Fatalf("resolved manifest does not match golden fixture.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
