// Package agentprofile is the AgentProfileVersion contract
// (docs/design/04-v2-definition-plane.md V2-07, docs/design/00-roadmap.md
// §2): docs/design/01-system-design.md's DefinitionKind table (§4.1)
// describes Agent Profile as publishing "provider/model/context/tool
// policy refs" with execution authority "Qua provider policy" — an Agent
// Profile never runs anything itself, it only pins the concrete
// provider/model, the exact context-route Policy, the tool refs a
// provider adapter may use, and the compatibility/capability/budget
// envelope a run under this profile must satisfy.
//
// A Profile pins its provider by an abstract ProviderKey string, never a
// concrete AdapterBuildVersion: docs/design/04-v2-definition-plane.md's
// own V2-07A section makes AdapterBuildVersion a separate, later,
// operational-registry concept (ADR-022) that is not even a
// DefinitionKind and does not exist as an authoring-time reference yet —
// V2-07's own scope stops at naming the provider, not pinning a specific
// build of it. Likewise a Profile never imports package policy's Go
// types for its context-route pin: it references the exact Policy
// version by definition.DependencyPin the same way every other
// cross-kind reference in this codebase already works (e.g. Block's
// ExecutorRef/PolicyRefs), constrained to Kind == definition.KindPolicy.
// This package cannot additionally verify, at authoring time, that the
// referenced Policy's own Category is CONTEXT specifically — that
// Category only exists inside the Policy's own document, which this
// layer never resolves (dependency resolution/verification is V2-09's
// job); ContextPolicyRef's own doc comment states this convention
// explicitly so a future compiler knows where to enforce it for real.
package agentprofile

import (
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// AgentProfileDefinitionID identifies an Agent Profile's mutable
// Definition row, kept as its own named type per this codebase's
// kind-safe-ID convention (each kind declares its own ID type rather
// than sharing a generic phantom type — see internal/domain/definition's
// own package doc for why).
type AgentProfileDefinitionID string

// AgentProfileVersionID identifies one immutable, published
// AgentProfileVersion.
type AgentProfileVersionID string

// supportedOS is the closed set of operating systems Alpha's core
// contract supports (docs/architecture/02-architecture-decisions.md
// ADR-002 decision list item 6: "Core Windows/Linux; beta worker
// Linux"). Compatibility.OS entries outside this set are rejected as an
// OS mismatch — there is no worker target this Alpha could ever run
// against for anything else.
var supportedOS = map[string]bool{
	"windows": true,
	"linux":   true,
}

// Compatibility is an Agent Profile's declared OS/toolchain requirement
// — the same shape/reasoning internal/domain/command's own Compatibility
// would use once V2-05 merges (that package does not exist in this
// worktree as of V2-07; a later task should consolidate the two
// identical shapes into one, see this task's own final report). OS is
// required and non-empty: a profile with no declared OS could never be
// checked against a real worker target. Toolchain is optional: many
// providers need no toolchain beyond the provider executable itself.
type Compatibility struct {
	OS        []string `json:"os" yaml:"os"`
	Toolchain []string `json:"toolchain,omitempty" yaml:"toolchain,omitempty"`
}

// Budget is this Profile's own declared token ceiling
// (docs/architecture/03-system-architecture.md §5.1's Conversation &
// Context component owns "token/resource budget"). It is deliberately
// narrow: max-attempts/backoff/timeout already belong to whatever
// ATTEMPT-category policy.PolicyDocument a workflow node separately
// pins (go-core-spec.md §6), and duplicating that concept here under a
// different name/authority would create two answers to the same
// question. MaxTokens is this Profile's own ceiling because a Profile
// is reusable across many nodes/workflows and therefore needs its own
// baseline cost cap independent of any one node's attempt policy.
type Budget struct {
	MaxTokens uint32 `json:"maxTokens" yaml:"maxTokens"`
}

// AgentProfileDocument is an Agent Profile's complete authored content —
// everything V2-07's own "Thực hiện" line names: "pin provider/model/
// context/tool refs" plus the compatibility/capability/budget envelope
// its own "Verify" line requires (missing capability, OS mismatch,
// invalid budget, policy dependency).
type AgentProfileDocument struct {
	// ProviderKey names the provider adapter this profile runs through
	// (go-core-spec.md §14's own "ProviderKey" field name on
	// AgentExecutionRequest) — an abstract key, never a concrete
	// AdapterBuildVersion pin (see this package's own doc comment).
	ProviderKey string `json:"providerKey" yaml:"providerKey"`
	// Model names the provider model this profile requests.
	Model string `json:"model" yaml:"model"`
	// ToolRefs is the set of tool identifiers a provider adapter may use
	// under this profile. These are flat string identifiers, not
	// definition.DependencyPin values: there is no standalone ToolKind
	// DefinitionKind to pin against in this design (tools are a provider
	// adapter's own allow-list concept, go-core-spec.md §19's "provider
	// executable/argv template/protocol version/capability allow-list"),
	// so a pin would have nothing concrete to name.
	ToolRefs []string `json:"toolRefs,omitempty" yaml:"toolRefs,omitempty"`
	// ContextPolicyRef pins the exact context-route PolicyVersion this
	// profile uses (V2-07's own "context route là exact PolicyVersion").
	// Its Kind must be definition.KindPolicy; this package cannot verify
	// the referenced Policy's own Category is CONTEXT (see package doc).
	ContextPolicyRef definition.DependencyPin `json:"contextPolicyRef" yaml:"contextPolicyRef"`
	// Compatibility is the OS/toolchain requirement this profile
	// declares.
	Compatibility Compatibility `json:"compatibility" yaml:"compatibility"`
	// RequiredCapabilities is the set of named capabilities a run must
	// grant before this profile may execute — same shape/convention as
	// internal/domain/block.BlockDocument.RequiredCapabilities.
	RequiredCapabilities []string `json:"requiredCapabilities,omitempty" yaml:"requiredCapabilities,omitempty"`
	// Budget is this profile's own declared token ceiling.
	Budget Budget `json:"budget" yaml:"budget"`
}
