package layer

import (
	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// resourceContentSetPaths mirrors documentSetPaths' selector-dimension
// entries, scoped to one resource's own content (see resourceContent).
var resourceContentSetPaths = map[string]bool{
	"selector.componentTags": true,
	"selector.pathTags":      true,
	"selector.taskKinds":     true,
	"selector.blockKinds":    true,
	"selector.riskClasses":   true,
}

// resourceContent is the subset of Resource that actually makes up its
// semantic content for ADR-012's content_hash — Key and Provenance are
// both deliberately excluded (see
// internal/domain/skill.ResourceIdentities' doc comment, which applies
// identically here).
type resourceContent struct {
	Convention string                   `json:"convention"`
	Priority   definition.PriorityClass `json:"priority"`
	Global     bool                     `json:"global,omitempty"`
	Selector   Selector                 `json:"selector"`
}

// ResourceIdentities computes ADR-012's owner_version_id + resource_key +
// content_hash identity for every resource in doc, plus the
// definition.PriorityClass and definition.KindLayer facts
// engineeringpack.CheckResourceConflicts needs to reason about a
// resource without importing this package. See
// internal/domain/skill.ResourceIdentities' doc comment for the full
// reasoning (identical here): ContentHash excludes Provenance so
// re-verification never changes a resource's identity, and this is a
// pure function callable against a draft document.
func ResourceIdentities(ownerVersionID LayerVersionID, doc LayerDocument) ([]definition.ResolvedResource, error) {
	identities := make([]definition.ResolvedResource, 0, len(doc.Resources))
	for _, resource := range doc.Resources {
		_, hash, err := authoring.Canonicalize(resourceContent{
			Convention: resource.Convention,
			Priority:   resource.Priority,
			Global:     resource.Global,
			Selector:   resource.Selector,
		}, authoring.CanonicalizeOptions{SetPaths: resourceContentSetPaths})
		if err != nil {
			return nil, err
		}
		identities = append(identities, definition.ResolvedResource{
			Identity: definition.ResourceIdentity{
				OwnerVersionID: string(ownerVersionID),
				ResourceKey:    resource.Key,
				ContentHash:    hash,
			},
			OwnerKind: definition.KindLayer,
			Priority:  resource.Priority,
		})
	}
	return identities, nil
}
