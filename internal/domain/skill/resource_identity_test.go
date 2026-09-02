package skill_test

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
)

func TestResourceIdentities_CarriesOwnerVersionKeyKindAndPriority(t *testing.T) {
	doc := validDocument()
	identities, err := skill.ResourceIdentities("skill-v1", doc)
	if err != nil {
		t.Fatalf("ResourceIdentities: %v", err)
	}
	if len(identities) != 1 {
		t.Fatalf("len(identities) = %d, want 1", len(identities))
	}
	got := identities[0]
	if got.Identity.OwnerVersionID != "skill-v1" {
		t.Fatalf("OwnerVersionID = %q, want %q", got.Identity.OwnerVersionID, "skill-v1")
	}
	if got.Identity.ResourceKey != doc.Resources[0].Key {
		t.Fatalf("ResourceKey = %q, want %q", got.Identity.ResourceKey, doc.Resources[0].Key)
	}
	if got.Identity.ContentHash == "" {
		t.Fatal("ContentHash should not be empty")
	}
	if got.OwnerKind != definition.KindSkill {
		t.Fatalf("OwnerKind = %v, want KindSkill", got.OwnerKind)
	}
	if got.Priority != doc.Resources[0].Priority {
		t.Fatalf("Priority = %v, want %v", got.Priority, doc.Resources[0].Priority)
	}
}

// TestResourceIdentities_ContentHashExcludesProvenance is
// ResourceIdentities' own central design decision: re-verifying a
// resource (or renaming its owner) must never change what content_hash
// says the resource IS.
func TestResourceIdentities_ContentHashExcludesProvenance(t *testing.T) {
	doc := validDocument()
	before, err := skill.ResourceIdentities("skill-v1", doc)
	if err != nil {
		t.Fatalf("ResourceIdentities(before): %v", err)
	}

	doc.Resources[0].Provenance.Owner = "a-different-team"
	doc.Resources[0].Provenance.LastVerified = verifiedAt(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	after, err := skill.ResourceIdentities("skill-v1", doc)
	if err != nil {
		t.Fatalf("ResourceIdentities(after): %v", err)
	}

	if before[0].Identity.ContentHash != after[0].Identity.ContentHash {
		t.Fatalf("ContentHash changed after only provenance changed: %s vs %s",
			before[0].Identity.ContentHash, after[0].Identity.ContentHash)
	}
}

func TestResourceIdentities_ContentHashChangesWithInstruction(t *testing.T) {
	doc := validDocument()
	before, err := skill.ResourceIdentities("skill-v1", doc)
	if err != nil {
		t.Fatalf("ResourceIdentities(before): %v", err)
	}

	doc.Resources[0].Instruction = "a genuinely different instruction"
	after, err := skill.ResourceIdentities("skill-v1", doc)
	if err != nil {
		t.Fatalf("ResourceIdentities(after): %v", err)
	}

	if before[0].Identity.ContentHash == after[0].Identity.ContentHash {
		t.Fatal("ContentHash should differ when instruction content genuinely differs")
	}
}

// TestResourceIdentities_KeyChangeDoesNotAffectContentHash confirms Key
// is identity, not content: renaming a resource with byte-identical
// remaining content keeps the same content_hash (only ResourceKey, the
// other half of ADR-012's identity, changes).
func TestResourceIdentities_KeyChangeDoesNotAffectContentHash(t *testing.T) {
	doc := validDocument()
	before, err := skill.ResourceIdentities("skill-v1", doc)
	if err != nil {
		t.Fatalf("ResourceIdentities(before): %v", err)
	}

	doc.Resources[0].Key = "a-renamed-key"
	after, err := skill.ResourceIdentities("skill-v1", doc)
	if err != nil {
		t.Fatalf("ResourceIdentities(after): %v", err)
	}

	if before[0].Identity.ContentHash != after[0].Identity.ContentHash {
		t.Fatalf("ContentHash changed after renaming key: %s vs %s",
			before[0].Identity.ContentHash, after[0].Identity.ContentHash)
	}
	if after[0].Identity.ResourceKey != "a-renamed-key" {
		t.Fatalf("ResourceKey = %q, want %q", after[0].Identity.ResourceKey, "a-renamed-key")
	}
}
