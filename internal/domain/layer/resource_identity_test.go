package layer_test

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/layer"
)

func TestResourceIdentities_CarriesOwnerVersionKeyKindAndPriority(t *testing.T) {
	doc := validDocument()
	identities, err := layer.ResourceIdentities("layer-v1", doc)
	if err != nil {
		t.Fatalf("ResourceIdentities: %v", err)
	}
	if len(identities) != 1 {
		t.Fatalf("len(identities) = %d, want 1", len(identities))
	}
	got := identities[0]
	if got.Identity.OwnerVersionID != "layer-v1" {
		t.Fatalf("OwnerVersionID = %q, want %q", got.Identity.OwnerVersionID, "layer-v1")
	}
	if got.Identity.ResourceKey != doc.Resources[0].Key {
		t.Fatalf("ResourceKey = %q, want %q", got.Identity.ResourceKey, doc.Resources[0].Key)
	}
	if got.Identity.ContentHash == "" {
		t.Fatal("ContentHash should not be empty")
	}
	if got.OwnerKind != definition.KindLayer {
		t.Fatalf("OwnerKind = %v, want KindLayer", got.OwnerKind)
	}
	if got.Priority != doc.Resources[0].Priority {
		t.Fatalf("Priority = %v, want %v", got.Priority, doc.Resources[0].Priority)
	}
}

func TestResourceIdentities_ContentHashExcludesProvenance(t *testing.T) {
	doc := validDocument()
	before, err := layer.ResourceIdentities("layer-v1", doc)
	if err != nil {
		t.Fatalf("ResourceIdentities(before): %v", err)
	}

	doc.Resources[0].Provenance.Owner = "a-different-team"
	doc.Resources[0].Provenance.LastVerified = verifiedAt(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	after, err := layer.ResourceIdentities("layer-v1", doc)
	if err != nil {
		t.Fatalf("ResourceIdentities(after): %v", err)
	}

	if before[0].Identity.ContentHash != after[0].Identity.ContentHash {
		t.Fatalf("ContentHash changed after only provenance changed: %s vs %s",
			before[0].Identity.ContentHash, after[0].Identity.ContentHash)
	}
}

func TestResourceIdentities_ContentHashChangesWithConvention(t *testing.T) {
	doc := validDocument()
	before, err := layer.ResourceIdentities("layer-v1", doc)
	if err != nil {
		t.Fatalf("ResourceIdentities(before): %v", err)
	}

	doc.Resources[0].Convention = "a genuinely different convention"
	after, err := layer.ResourceIdentities("layer-v1", doc)
	if err != nil {
		t.Fatalf("ResourceIdentities(after): %v", err)
	}

	if before[0].Identity.ContentHash == after[0].Identity.ContentHash {
		t.Fatal("ContentHash should differ when convention content genuinely differs")
	}
}
