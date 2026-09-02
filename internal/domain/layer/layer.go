// Package layer is the LayerVersion contract
// (docs/design/04-v2-definition-plane.md V2-06, HE-04-M02/M03/M04,
// HE-03-M06): a Layer publishes "stack convention/resource selectors"
// (docs/design/01-system-design.md's DefinitionKind table) — a set of
// named, passive stack-convention resources (a framework's idiomatic
// layout, a language's house style, a platform's operational baseline),
// each declaring when it applies, how important it is, and who is
// accountable for it staying accurate.
//
// This package is deliberately the near-mirror of internal/domain/skill:
// both are passive-resource DefinitionKinds sharing the exact same
// applicability/priority/provenance/identity model (ADR-012,
// HE-04-M02/M03/M04, HE-03-M06). They stay two separate Go packages
// rather than one shared implementation for the same reason
// internal/domain/block keeps its own kind-safe ID types instead of a
// generic phantom type: SkillDefinitionID and LayerDefinitionID must
// never be interchangeable at compile time, and Skill vs Layer is a
// genuine authoring-time distinction a publisher chooses (a stack
// convention is not an instruction, even though both end up as
// applicability-selected text an attempt may load) — collapsing them
// into one generic "PassiveResourceKind" package would blur that
// distinction and make a future kind-specific rule (e.g. a Layer-only
// compatibility field) awkward to add without affecting Skill.
//
// A Layer never executes anything (docs/design/01-system-design.md's
// "Quyền thực thi: Không" for this Kind; docs/00-start-here.md
// requirement 6 — see internal/domain/skill's own package doc for the
// exact citation, which applies here identically).
//
// Like internal/domain/block and internal/domain/skill, Layer plugs
// directly into the generic definition.VersionFields contract V2-02
// already built: there is no separate LayerVersion wrapper type, and
// Compile produces definition.VersionFields directly.
package layer

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// LayerDefinitionID identifies a Layer's mutable Definition row, kept as
// its own named type per this codebase's kind-safe-ID convention (each
// kind declares its own ID type rather than sharing a generic phantom
// type — see internal/domain/definition's own package doc for why).
type LayerDefinitionID string

// LayerVersionID identifies one immutable, published LayerVersion.
type LayerVersionID string

// Selector is the applicability declaration HE-04-M02 requires of every
// non-global resource — identical in shape to internal/domain/skill's
// own Selector (see that package's doc comment for the reasoning behind
// every field and the OR-across/OR-within matching semantics).
type Selector struct {
	ComponentTags []string `json:"componentTags,omitempty" yaml:"componentTags,omitempty"`
	PathTags      []string `json:"pathTags,omitempty" yaml:"pathTags,omitempty"`
	TaskKinds     []string `json:"taskKinds,omitempty" yaml:"taskKinds,omitempty"`
	BlockKinds    []string `json:"blockKinds,omitempty" yaml:"blockKinds,omitempty"`
	RiskClasses   []string `json:"riskClasses,omitempty" yaml:"riskClasses,omitempty"`
}

// Provenance is HE-04-M04/HE-03-M06's knowledge-provenance requirement,
// identical in shape to internal/domain/skill's own Provenance (see that
// package's doc comment for the reasoning behind each field).
type Provenance struct {
	Owner        string     `json:"owner" yaml:"owner"`
	Source       string     `json:"source" yaml:"source"`
	LastVerified *time.Time `json:"lastVerified,omitempty" yaml:"lastVerified,omitempty"`
	Revision     string     `json:"revision,omitempty" yaml:"revision,omitempty"`
}

// Resource is one passive stack-convention resource a Layer publishes.
// Its ADR-012 identity (owner_version_id + resource_key + content_hash)
// is not a field on this struct for the same reason as
// internal/domain/skill.Resource — see ResourceIdentities.
type Resource struct {
	// Key names this resource within its document. Combined with the
	// enclosing version's ID it is half of ADR-012's resource identity;
	// it MUST be unique within one LayerDocument (see ValidateDocument).
	Key string `json:"key" yaml:"key"`
	// Convention is this resource's actual stack-convention content —
	// the text an attempt is meant to load and follow.
	Convention string `json:"convention" yaml:"convention"`
	// Priority is HE-04-M03's required priority class.
	Priority definition.PriorityClass `json:"priority" yaml:"priority"`
	// Global marks a resource that applies everywhere, exempting it from
	// HE-04-M02's selector requirement — see
	// internal/domain/skill.Resource.Global's doc comment.
	Global bool `json:"global,omitempty" yaml:"global,omitempty"`
	// Selector declares when this resource applies. Required and must be
	// non-empty when Global is false.
	Selector Selector `json:"selector" yaml:"selector"`
	// Provenance is this resource's owner/source/last-verified anchor.
	Provenance Provenance `json:"provenance" yaml:"provenance"`
}

// LayerDocument is a Layer's complete authored content: the set of
// stack-convention resources it publishes.
type LayerDocument struct {
	Resources []Resource `json:"resources" yaml:"resources"`
}
