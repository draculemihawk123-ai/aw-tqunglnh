// Package policy is the PolicyVersion contract
// (docs/design/04-v2-definition-plane.md V2-07, docs/design/00-roadmap.md
// §2, ADR-012). Per docs/design/01-system-design.md's DefinitionKind
// table (§4.1), Policy publishes "attempt, completion, permission,
// context hoặc cleanup" and its "Quyền thực thi" is "Chỉ semantics tương
// ứng" — a published Policy governs exactly one of those five semantic
// categories, never several at once, and it is never itself something
// that executes: every other DefinitionKind that needs one (Block via
// PolicyRefs, Agent Profile via ContextPolicyRef) references it by an
// exact definition.DependencyPin with Kind == definition.KindPolicy.
//
// A single PolicyDocument therefore always names one Category and
// carries only that category's own rule struct (Attempt/Completion/
// Permission/Context/Cleanup) — the other four are always nil. This
// "exactly one of five" shape is a deliberate, closed design: it mirrors
// how the rest of this codebase already expresses a closed choice (e.g.
// internal/domain/block.ScopeAccess), and it makes "Chỉ semantics tương
// ứng" a structural fact instead of a convention a caller has to trust.
//
// Each category's own rule shape is intentionally minimal: the source
// docs name the vocabulary (AttemptPolicy's "max attempts, retryable
// error codes, backoff, timeout" at docs/architecture/04-go-core-spec.md
// §6; the four CompletionDecision outcomes at ADR-021; the two isolation
// tiers at ADR-013; "context route là một loại PolicyVersion, pin
// selector/order/budget và resource identities" at ADR-012; cleanup's
// retention/seal preconditions at go-core-spec.md §15/§16) without fully
// elaborating a rule engine for any of them — this package captures
// exactly the fields those citations ground, not more, and documents
// every field's citation in its own doc comment.
package policy

import "github.com/taQuangLing/agent-workflow/internal/domain/errorcode"

// PolicyDefinitionID identifies a Policy's mutable Definition row, kept
// as its own named type per this codebase's kind-safe-ID convention
// (each kind declares its own ID type rather than sharing a generic
// phantom type — see internal/domain/definition's own package doc for
// why).
type PolicyDefinitionID string

// PolicyVersionID identifies one immutable, published PolicyVersion.
type PolicyVersionID string

// Category is the closed set of semantics a Policy may govern
// (docs/design/01-system-design.md §4.1's "attempt, completion,
// permission, context hoặc cleanup").
type Category string

const (
	CategoryAttempt    Category = "ATTEMPT"
	CategoryCompletion Category = "COMPLETION"
	CategoryPermission Category = "PERMISSION"
	CategoryContext    Category = "CONTEXT"
	CategoryCleanup    Category = "CLEANUP"
)

var validCategories = map[Category]bool{
	CategoryAttempt: true, CategoryCompletion: true, CategoryPermission: true,
	CategoryContext: true, CategoryCleanup: true,
}

// Valid reports whether c is one of the five defined Categories.
func (c Category) Valid() bool { return validCategories[c] }

// IsolationTier is the two explicit trust tiers ADR-013 defines for
// execution admission and enforcement.
type IsolationTier string

const (
	// IsolationTierEnforcedIsolated requires the worker to actually
	// enforce the filesystem/network/secret profile via an OS/container
	// mechanism; missing enforcement fails admission closed (ADR-013).
	IsolationTierEnforcedIsolated IsolationTier = "ENFORCED_ISOLATED"
	// IsolationTierOperatorTrustedLocal is only granted by an operator to
	// an already-trusted executable/provider; the UI/evidence must show
	// that isolation was not fully enforced (ADR-013).
	IsolationTierOperatorTrustedLocal IsolationTier = "OPERATOR_TRUSTED_LOCAL"
)

var validIsolationTiers = map[IsolationTier]bool{
	IsolationTierEnforcedIsolated: true, IsolationTierOperatorTrustedLocal: true,
}

// AttemptRules is Category ATTEMPT's rule shape:
// docs/architecture/04-go-core-spec.md §6 names "AttemptPolicy: max
// attempts, retryable error codes, backoff, timeout" as exactly what a
// node's technical-retry policy carries; this struct is that vocabulary
// made a versioned, hashable, standalone Policy document.
type AttemptRules struct {
	// MaxAttempts is the maximum number of ExecutionAttempts a technical
	// retry may create for one NodeRun occurrence.
	MaxAttempts uint32 `json:"maxAttempts" yaml:"maxAttempts"`
	// RetryableErrorCodes is the set of AppError codes
	// (internal/domain/errorcode, go-core-spec.md §18) whose technical
	// failure may be retried — errorcode.Code, not a bare []string
	// (correction found during V4-06 scoping review: this field used to be
	// []string, validated against this package's own now-removed private
	// knownErrorCodes/nonRetryableErrorCodes maps; using the canonical
	// domain type directly means there is exactly one place in this
	// codebase that ever defines what a valid/never-retryable code is).
	RetryableErrorCodes []errorcode.Code `json:"retryableErrorCodes,omitempty" yaml:"retryableErrorCodes,omitempty"`
	// BackoffSeconds is the delay before a retried attempt is created.
	// go-core-spec.md §6 names "backoff" as part of AttemptPolicy without
	// specifying a strategy (linear/exponential/jitter); this package
	// intentionally keeps it as a single fixed-delay field rather than
	// inventing an unfounded backoff-curve shape — a richer strategy can
	// be added later without breaking this field's meaning.
	BackoffSeconds uint32 `json:"backoffSeconds" yaml:"backoffSeconds"`
	// TimeoutSeconds is how long a single ExecutionAttempt may run before
	// it is considered TIMED_OUT.
	TimeoutSeconds uint32 `json:"timeoutSeconds" yaml:"timeoutSeconds"`
}

// CompletionRules is Category COMPLETION's rule shape. ADR-021 defines
// the four-outcome CompletionDecision state machine itself (PASS/REWORK/
// BLOCK/FAIL) as engine behavior, not authored content; what a
// CompletionPolicy document itself can meaningfully declare at this
// layer is which Evidence kinds are required before a PASS is even
// possible — GC-INV-12 ("completion policy và required evidence quyết
// định") and GC-INV-13 ("NOT_RUN, thiếu evidence hoặc verifier error
// không được quy thành PASS") both ground this field; the four-outcome
// transition machinery itself belongs to the runtime engine (V4/V5), not
// this authoring-time schema.
type CompletionRules struct {
	// RequiredEvidenceKinds is the set of Evidence.Kind values that must
	// be present (and PASS) before a completion candidate may resolve to
	// CompletionDecision PASS.
	RequiredEvidenceKinds []string `json:"requiredEvidenceKinds" yaml:"requiredEvidenceKinds"`
}

// PermissionRules is Category PERMISSION's rule shape: ADR-013's two
// isolation tiers plus the named capability grants
// (docs/architecture/02-architecture-decisions.md ADR-013's
// INTEGRATION_MULTI_REPOSITORY_WRITE is the one concrete example this
// repo's accepted architecture already names) a permission-granting
// Policy extends to whatever pins it.
type PermissionRules struct {
	// IsolationTier is the trust tier this permission grants.
	IsolationTier IsolationTier `json:"isolationTier" yaml:"isolationTier"`
	// GrantedCapabilities is the set of named capabilities this
	// permission grants (e.g. "INTEGRATION_MULTI_REPOSITORY_WRITE").
	GrantedCapabilities []string `json:"grantedCapabilities,omitempty" yaml:"grantedCapabilities,omitempty"`
}

// ResourceRef is one passive-resource identity a context route may pull
// in — ADR-012's own resource identity tuple ("owner_version_id +
// resource_key + content_hash"), reproduced here as this package's own
// struct rather than importing a Skill/Layer/Pack Go type: V2-06 (which
// would own that type) has not merged as of this task, and even once it
// exists, a Policy pinning a resource by this exact identity tuple
// (rather than importing another kind's package) matches the same
// cross-kind-reference-by-value convention this whole session already
// established (e.g. Block referencing Policy only via
// definition.DependencyPin, never by importing package policy).
type ResourceRef struct {
	OwnerVersionID string `json:"ownerVersionId" yaml:"ownerVersionId"`
	ResourceKey    string `json:"resourceKey" yaml:"resourceKey"`
	ContentHash    string `json:"contentHash" yaml:"contentHash"`
}

// ContextBudget is the token/resource ceiling ADR-012 says a context
// route pins ("pin selector/order/budget và resource identities").
type ContextBudget struct {
	// MaxTokens is the maximum token budget a ContextSnapshot assembled
	// under this route may spend (docs/architecture/03-system-architecture.md
	// §5.1's Conversation & Context component owns "token/resource
	// budget").
	MaxTokens uint32 `json:"maxTokens" yaml:"maxTokens"`
}

// ContextRules is Category CONTEXT's rule shape — ADR-012's "Context
// route là một loại PolicyVersion, pin selector/order/budget và resource
// identities" made concrete: Selector is which resources may be pulled
// in (a set — membership only), Order is the sequence they are actually
// assembled in (a list — order is the whole point of this field, so it
// is never treated as a set), Budget is the ceiling, and ResourceRefs is
// the exact pinned resource identity set.
type ContextRules struct {
	// Selector is the set of resource-key patterns eligible for this
	// context route. At least one is required.
	Selector []string `json:"selector" yaml:"selector"`
	// Order is the explicit assembly sequence (e.g. resource keys or
	// selector-pattern names) — order-significant by definition, unlike
	// every other array field in this package.
	Order []string `json:"order,omitempty" yaml:"order,omitempty"`
	// Budget is this route's token/resource ceiling.
	Budget ContextBudget `json:"budget" yaml:"budget"`
	// ResourceRefs is the exact set of pinned passive-resource identities
	// this route may assemble from.
	ResourceRefs []ResourceRef `json:"resourceRefs,omitempty" yaml:"resourceRefs,omitempty"`
}

// CleanupRules is Category CLEANUP's rule shape. go-core-spec.md §15
// ("Cleanup bị từ chối khi còn active JobLease/WriteLease, attempt không
// terminal, ReleaseSet chưa seal/abandon hoặc workspace quarantined chưa
// reconcile") and §19's typed retention class are both cited here;
// RetentionDays is intentionally the only authored field — the
// lease/terminal/ReleaseSet preconditions are runtime enforcement
// invariants (GC-INV-26), never something a Policy document declares or
// could override, so this package does not expose a field for them.
type CleanupRules struct {
	// RetentionDays overrides the platform's default retention window
	// (go-core-spec.md §19's "TTL 7 ngày mặc định") for whatever this
	// Policy is pinned to.
	RetentionDays uint32 `json:"retentionDays" yaml:"retentionDays"`
}

// PolicyDocument is a Policy's complete authored content: exactly one
// Category, and exactly that category's own rule struct populated — see
// this package's own doc comment for why "exactly one" is a deliberate,
// structural design choice.
type PolicyDocument struct {
	Category   Category         `json:"category" yaml:"category"`
	Attempt    *AttemptRules    `json:"attempt,omitempty" yaml:"attempt,omitempty"`
	Completion *CompletionRules `json:"completion,omitempty" yaml:"completion,omitempty"`
	Permission *PermissionRules `json:"permission,omitempty" yaml:"permission,omitempty"`
	Context    *ContextRules    `json:"context,omitempty" yaml:"context,omitempty"`
	Cleanup    *CleanupRules    `json:"cleanup,omitempty" yaml:"cleanup,omitempty"`
}
