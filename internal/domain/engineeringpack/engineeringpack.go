// Package engineeringpack is the EngineeringPackVersion contract
// (docs/design/04-v2-definition-plane.md V2-06, lec-04's "Engineering
// Pack resolution" section): an Engineering Pack publishes "dependency
// set Skill/Layer/resource" (docs/design/01-system-design.md's
// DefinitionKind table) — a named, versioned composition of other
// passive resources (Skill and Layer versions, or other Engineering
// Pack versions, letting a baseline pack be extended by more specific
// packs the way lec-04's own example profile does:
// "enterprise-baseline + java + spring-boot + rest-api +
// payment-service").
//
// An Engineering Pack never executes anything and never itself grants
// permission (docs/design/01-system-design.md's "Quyền thực thi: Không"
// for this Kind; docs/00-start-here.md requirement 6 — see
// internal/domain/skill's own package doc for the exact citation).
// lec-04's own Merge policy says this explicitly: "Pack không tham gia
// merge permission, toolchain hay gate; ba concern đó được resolve bởi
// policy/environment/verification registry riêng" — so this package's
// ResolvePackGraph and CheckResourceConflicts (graph.go) only ever
// produce a manifest of resource identities, never a Command, Gate or
// permission grant. Neither this file nor graph.go imports
// internal/domain/command or internal/domain/gate; if a future change
// to this package ever needs to, that is a sign the passive-resource
// boundary has been misunderstood, not a signal to add the import.
//
// This package deliberately never imports internal/domain/skill or
// internal/domain/layer either — it references them only through
// definition.DependencyPin (Kind+DefinitionID+VersionID), the same
// pattern internal/domain/block uses to reference Command/Gate without
// importing either package. Where this package's own graph resolution
// needs to reason about a Skill/Layer's actual resources (for conflict
// detection), it takes definition.ResolvedResource values as plain
// input — the shape skill.ResourceIdentities and layer.ResourceIdentities
// both already produce — rather than reaching into either package
// itself.
//
// Like internal/domain/block/skill/layer, Engineering Pack plugs
// directly into the generic definition.VersionFields contract V2-02
// already built: there is no separate EngineeringPackVersion wrapper
// type, and Compile produces definition.VersionFields directly.
package engineeringpack

import (
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// EngineeringPackDefinitionID identifies an Engineering Pack's mutable
// Definition row, kept as its own named type per this codebase's
// kind-safe-ID convention (each kind declares its own ID type rather
// than sharing a generic phantom type — see internal/domain/definition's
// own package doc for why).
type EngineeringPackDefinitionID string

// EngineeringPackVersionID identifies one immutable, published
// EngineeringPackVersion.
type EngineeringPackVersionID string

// packDependencyKinds is the closed set of DefinitionKinds an
// Engineering Pack may depend on: Skill, Layer, and other Engineering
// Pack versions (pack-of-pack composition — see this package's own doc
// comment and ResolvePackGraph in graph.go, which is what actually walks
// the Pack-of-Pack edges this allows). Command, Gate, Agent Profile,
// Policy and Workflow are all deliberately absent: an Engineering Pack
// is a composition of passive resources, never of anything with
// execution authority.
var packDependencyKinds = map[definition.Kind]bool{
	definition.KindSkill:           true,
	definition.KindLayer:           true,
	definition.KindEngineeringPack: true,
}

// EngineeringPackDocument is an Engineering Pack's complete authored
// content: the set of Skill/Layer/Engineering-Pack versions it composes.
type EngineeringPackDocument struct {
	Dependencies []definition.DependencyPin `json:"dependencies" yaml:"dependencies"`
}
