package definition

// PriorityClass is the closed set of instruction-priority tiers
// HE-04-M03 requires every non-global resource to declare
// (docs/harness-engineering/04-lec-04-progressive-disclosure.md:
// "instruction MUST phân biệt HARD_CONSTRAINT, REQUIRED_PROCEDURE,
// GUIDANCE, REFERENCE"). It lives here rather than inside skill/layer
// separately because Skill, Layer and Engineering Pack all need to speak
// the exact same vocabulary — Engineering Pack's conflict resolution
// (docs/design/04-v2-definition-plane.md V2-06's "hard-constraint
// conflict fail closed") compares Priority values across resources that
// originated from either kind, and it does so without importing either
// package's own Go types (mirroring why DependencyPin already lives
// here rather than in one specific kind's package).
type PriorityClass string

const (
	// PriorityHardConstraint is a rule that MUST hold; two applicable
	// HARD_CONSTRAINT resources that disagree on the same resource_key
	// is a configuration error, never a silently resolved "last wins"
	// (lec-04's Merge policy: "Hai hard constraint áp dụng đồng thời
	// nhưng mâu thuẫn gây configuration error; resolver không tự chọn
	// bên thắng").
	PriorityHardConstraint PriorityClass = "HARD_CONSTRAINT"
	// PriorityRequiredProcedure is a procedure an attempt MUST follow,
	// but which — unlike a HARD_CONSTRAINT — is a sequence of steps
	// rather than a single invariant value, so two required procedures
	// applying at once is not automatically the same kind of conflict.
	PriorityRequiredProcedure PriorityClass = "REQUIRED_PROCEDURE"
	// PriorityGuidance is advisory: worth following, never fail-closed
	// on its own.
	PriorityGuidance PriorityClass = "GUIDANCE"
	// PriorityReference is background material an attempt may consult
	// but never a rule it must comply with.
	PriorityReference PriorityClass = "REFERENCE"
)

var validPriorityClasses = map[PriorityClass]bool{
	PriorityHardConstraint:    true,
	PriorityRequiredProcedure: true,
	PriorityGuidance:          true,
	PriorityReference:         true,
}

// Valid reports whether p is one of the four defined PriorityClass
// values.
func (p PriorityClass) Valid() bool { return validPriorityClasses[p] }

// ResourceIdentity is ADR-012's passive-resource identity
// (docs/architecture/02-architecture-decisions.md ADR-012: "Resource
// thụ động nằm trong Skill/Layer/Pack version và có identity
// owner_version_id + resource_key + content_hash; không cần top-level
// ResourceDefinition riêng"). OwnerVersionID is the SkillVersionID or
// LayerVersionID whose published document declared this resource;
// ResourceKey is the author-chosen key naming it within that document;
// ContentHash is a canonical hash of the resource's own semantic content
// only (never its provenance metadata — see skill/layer's own
// ResourceIdentities doc comment for why), so re-verifying a resource
// without changing what it says never changes its identity.
type ResourceIdentity struct {
	OwnerVersionID string
	ResourceKey    string
	ContentHash    string
}

// ResolvedResource pairs a ResourceIdentity with the two extra facts
// Engineering Pack conflict resolution needs about it: which
// DefinitionKind owns it (SKILL or LAYER) and its authored PriorityClass
// (docs/design/04-v2-definition-plane.md V2-06's "hard-constraint
// conflict fail closed"). It is the shape skill.ResourceIdentities and
// layer.ResourceIdentities both return, and the shape
// engineeringpack.CheckResourceConflicts consumes — letting Engineering
// Pack reason about resources from either kind without importing either
// kind's own package.
type ResolvedResource struct {
	Identity  ResourceIdentity
	OwnerKind Kind
	Priority  PriorityClass
}
