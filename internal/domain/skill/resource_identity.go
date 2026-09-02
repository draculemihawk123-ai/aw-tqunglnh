package skill

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
// semantic content for ADR-012's content_hash — Key (half of the
// identity itself, not part of what it hashes) and Provenance
// (administrative metadata, not content — see ResourceIdentities' own
// doc comment) are both deliberately excluded.
type resourceContent struct {
	Instruction string                   `json:"instruction"`
	Priority    definition.PriorityClass `json:"priority"`
	Global      bool                     `json:"global,omitempty"`
	Selector    Selector                 `json:"selector"`
}

// ResourceIdentities computes ADR-012's owner_version_id + resource_key +
// content_hash identity for every resource in doc, plus the
// definition.PriorityClass and definition.KindSkill facts
// engineeringpack.CheckResourceConflicts needs to reason about a
// resource without importing this package.
//
// ContentHash covers a resource's semantic content only (Instruction,
// Priority, Global, Selector) — never its Provenance. This is a
// deliberate design decision: re-verifying a resource (bumping
// Provenance.LastVerified) or updating who owns it must never change
// what content_hash says the resource IS, or every routine
// re-verification would look like a brand-new resource to anything
// deduplicating by content_hash. It also means two resources with
// byte-identical instructional content hash identically even under
// different keys or in different Skills — which is the intended
// "the same rule got copied into two places" detection signal, not a
// false collision.
//
// This is a pure function of doc: it never requires the resource to
// have actually been published, so it is equally callable against a
// draft document (e.g. from a CLI preview, not yet built here) as
// against an already-compiled version's decoded document.
func ResourceIdentities(ownerVersionID SkillVersionID, doc SkillDocument) ([]definition.ResolvedResource, error) {
	identities := make([]definition.ResolvedResource, 0, len(doc.Resources))
	for _, resource := range doc.Resources {
		_, hash, err := authoring.Canonicalize(resourceContent{
			Instruction: resource.Instruction,
			Priority:    resource.Priority,
			Global:      resource.Global,
			Selector:    resource.Selector,
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
			OwnerKind: definition.KindSkill,
			Priority:  resource.Priority,
		})
	}
	return identities, nil
}
