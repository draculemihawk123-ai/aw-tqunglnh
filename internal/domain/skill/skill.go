// Package skill is the SkillVersion contract
// (docs/design/04-v2-definition-plane.md V2-06, HE-04-M02/M03/M04,
// HE-03-M06): a Skill publishes "instruction/resource selectors"
// (docs/design/01-system-design.md's DefinitionKind table) — a set of
// named, passive instruction resources, each declaring when it applies,
// how important it is, and who is accountable for it staying accurate.
//
// A Skill never executes anything (docs/design/01-system-design.md's
// "Quyền thực thi: Không" for this Kind; docs/00-start-here.md
// requirement 6: "Skill/Layer/Engineering Pack chỉ chứa instruction và
// resource. Command, gate, scaffold hay script thực thi phải là
// executable definition/executor versioned, được policy cấp quyền tường
// minh"). This package has no notion of running a resource's content —
// it only ever validates and canonicalizes it. internal/domain/block's
// own validateExecutorRef already enforces the other half of that same
// boundary: a Block can never point its ExecutorRef at a Skill.
//
// Every resource this package's SkillDocument declares carries the four
// things HE-04/HE-03 require of a non-global rule:
//
//   - a Selector, declaring when it applies (HE-04-M02: "mọi resource
//     không toàn cục MUST khai báo khi nào áp dụng: component/path tag,
//     task kind, block kind hoặc risk class") — or an explicit Global
//     flag for the rare resource meant to apply everywhere;
//   - a Priority (HE-04-M03: HARD_CONSTRAINT / REQUIRED_PROCEDURE /
//     GUIDANCE / REFERENCE), shared cross-kind vocabulary that lives in
//     internal/domain/definition (see that package's priority.go doc
//     comment for why);
//   - Provenance — owner, source and a last-verified-or-revision anchor
//     (HE-04-M04, HE-03-M06);
//   - a stable Key, which combined with the enclosing SkillVersion's own
//     ID and a canonical content hash gives every resource ADR-012's
//     owner_version_id + resource_key + content_hash identity (see
//     ResourceIdentities).
//
// Like internal/domain/block, Skill plugs directly into the generic
// definition.VersionFields contract V2-02 already built: there is no
// separate SkillVersion wrapper type, and Compile produces
// definition.VersionFields directly.
package skill

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// SkillDefinitionID identifies a Skill's mutable Definition row, kept as
// its own named type per this codebase's kind-safe-ID convention (each
// kind declares its own ID type rather than sharing a generic phantom
// type — see internal/domain/definition's own package doc for why).
type SkillDefinitionID string

// SkillVersionID identifies one immutable, published SkillVersion.
type SkillVersionID string

// Selector is the applicability declaration HE-04-M02 requires of every
// non-global resource: component/path tag, task kind, block kind or risk
// class. Every field is a set of tags of its own dimension; a resource
// applies whenever the current task/attempt context matches at least one
// tag in at least one populated dimension (an OR across dimensions AND
// an OR within a dimension — the widest reading consistent with
// "applicable when any of these say so"; a narrower AND-across-
// dimensions selector language is a context-assembler concern for a
// later task, not a shape this authoring schema needs to pre-decide).
type Selector struct {
	ComponentTags []string `json:"componentTags,omitempty" yaml:"componentTags,omitempty"`
	PathTags      []string `json:"pathTags,omitempty" yaml:"pathTags,omitempty"`
	TaskKinds     []string `json:"taskKinds,omitempty" yaml:"taskKinds,omitempty"`
	BlockKinds    []string `json:"blockKinds,omitempty" yaml:"blockKinds,omitempty"`
	RiskClasses   []string `json:"riskClasses,omitempty" yaml:"riskClasses,omitempty"`
}

// Provenance is HE-04-M04/HE-03-M06's knowledge-provenance requirement:
// every rule/resource MUST have source/owner, applicability and a
// review/expiry condition or last_verified (or an equivalent revision
// anchor). Applicability is Resource.Selector, not repeated here;
// Provenance carries the remaining three.
type Provenance struct {
	// Owner is who is accountable for this resource staying correct —
	// required, since HE-03-M06/HE-04-M04 both list it first and an
	// un-owned rule is exactly lec-04's "Quy tắc không có owner hoặc
	// ngày kiểm chứng" anti-pattern.
	Owner string `json:"owner" yaml:"owner"`
	// Source is where this resource's content actually comes from (a
	// doc, an ADR, an incident writeup) — also required, for the same
	// reason as Owner: "owner/source" is HE-03-M06's own pairing, and a
	// resource with an owner but no traceable source is still not
	// verifiable by anyone else.
	Source string `json:"source" yaml:"source"`
	// LastVerified is when Owner last confirmed this resource is still
	// accurate. Either this or Revision (not necessarily both) must be
	// set — HE-03-M06's "last_verified hoặc revision tương đương".
	LastVerified *time.Time `json:"lastVerified,omitempty" yaml:"lastVerified,omitempty"`
	// Revision is an equivalent-to-last_verified anchor for a resource
	// whose accuracy is tied to a specific revision of something else
	// (a commit, a spec version) rather than a review date.
	Revision string `json:"revision,omitempty" yaml:"revision,omitempty"`
}

// Resource is one passive instruction resource a Skill publishes. Its
// ADR-012 identity (owner_version_id + resource_key + content_hash) is
// not a field on this struct — Key is only the author-chosen half of
// that identity; ContentHash is derived at compile time (see
// ResourceIdentities) and OwnerVersionID comes from the enclosing
// SkillVersion, neither of which the author writes directly.
type Resource struct {
	// Key names this resource within its document. Combined with the
	// enclosing version's ID it is half of ADR-012's resource identity;
	// it MUST be unique within one SkillDocument (see ValidateDocument).
	Key string `json:"key" yaml:"key"`
	// Instruction is this resource's actual instructional content — the
	// text an attempt is meant to load and follow.
	Instruction string `json:"instruction" yaml:"instruction"`
	// Priority is HE-04-M03's required priority class.
	Priority definition.PriorityClass `json:"priority" yaml:"priority"`
	// Global marks a resource that applies everywhere, exempting it from
	// HE-04-M02's selector requirement (that requirement is scoped to
	// "resource không toàn cục" — non-global resources — precisely so a
	// genuinely universal rule doesn't need a selector that would just
	// list every possible tag). A global resource may still carry a
	// Selector, but ValidateDocument does not require one.
	Global bool `json:"global,omitempty" yaml:"global,omitempty"`
	// Selector declares when this resource applies. Required and must be
	// non-empty when Global is false.
	Selector Selector `json:"selector" yaml:"selector"`
	// Provenance is this resource's owner/source/last-verified anchor.
	Provenance Provenance `json:"provenance" yaml:"provenance"`
}

// SkillDocument is a Skill's complete authored content: the set of
// instruction resources it publishes.
type SkillDocument struct {
	Resources []Resource `json:"resources" yaml:"resources"`
}
