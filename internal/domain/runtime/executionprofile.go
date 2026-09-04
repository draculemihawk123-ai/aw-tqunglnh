package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
)

// ExecutorKind is which of the three executable node types a
// ResolvedExecutionProfileV1 resolves for.
type ExecutorKind string

const (
	ExecutorKindAgent       ExecutorKind = "AGENT"
	ExecutorKindCommand     ExecutorKind = "COMMAND"
	ExecutorKindMachineGate ExecutorKind = "MACHINE_GATE"
)

var validExecutorKinds = map[ExecutorKind]bool{
	ExecutorKindAgent: true, ExecutorKindCommand: true, ExecutorKindMachineGate: true,
}

// ResolvedExecutorRef pins the exact AgentProfileVersion/CommandVersion/
// GateVersion a ResolvedExecutionProfileV1 executes under, plus that
// version's own compiled-content hash — never a bare DefinitionID/VersionID
// pair alone, so a republish under the same identity with different
// content still changes ExecutionProfileHash.
type ResolvedExecutorRef struct {
	Kind         ExecutorKind `json:"kind"`
	DefinitionID string       `json:"definitionId"`
	VersionID    string       `json:"versionId"`
	CompiledHash string       `json:"compiledHash"`
}

// ResolvedPolicyRef is one already-resolved Policy pin's identity: the
// exact published version, its own Category and its own compiled-content
// hash — never the raw PolicyDocument itself (a policy's rules are data
// this profile pins by identity, not content this hash needs to embed
// twice over).
type ResolvedPolicyRef struct {
	DefinitionID string          `json:"definitionId"`
	VersionID    string          `json:"versionId"`
	Category     policy.Category `json:"category"`
	CompiledHash string          `json:"compiledHash"`
}

// ResolvedAdapterBuildRef optionally pins the exact, content-addressed
// AdapterBuildVersion this node's provider must run (ADR-012's own "Run
// pin expected version" bar for the provider-adapter axis).
type ResolvedAdapterBuildRef struct {
	BuildID         string `json:"buildId"`
	ProtocolVersion string `json:"protocolVersion,omitempty"`
	CapabilityHash  string `json:"capabilityHash,omitempty"`
}

// ResolvedExecutionProfileV1 is the immutable, canonicalizable, hashable
// snapshot GC-INV-08 requires a NodeRun/ExecutionAttempt pin before its
// first Attempt: "Execution config hiệu lực được resolve thành immutable
// ExecutionProfile, canonicalize/hash và pin vào NodeRun/Attempt"
// (go-core-spec §19). ExecutionProfileHash is SHA-256 of this exact type's
// own canonical JSON (NewResolvedExecutionProfileV1's own return value) —
// never a hash of a node's PolicyRefs alone, a correction made during
// V4-04 scoping review after an earlier, narrower reading of this
// invariant.
//
// This type is deliberately a PURE, I/O-free domain value: it never
// itself resolves an AgentProfileVersion/CommandVersion/PolicyVersion/
// AdapterBuildVersion from a live Tx. A caller (V4-04's own NodeRun/
// Attempt scheduling transaction) is responsible for looking up every
// referenced, already-published definition and populating this struct's
// fields before calling NewResolvedExecutionProfileV1 — locking this
// type's shape and its canonicalization/hash contract now, ahead of that
// resolver, is deliberately this correction changeset's own scope; the
// resolver itself is V4-04's.
//
// Deliberately excluded from this hash (per the same review): secret
// values (SecretRefs names them, never resolves them — go-core-spec's own
// "secret chỉ resolve ở worker ngay trước spawn và không persist"),
// ContextSnapshot and RevisionSet (both already have their own separate
// pins on ExecutionAttempt — InputRevisionSet/ContextSnapshotID — so
// folding them in here would pin the same fact twice under two different
// names) and repository scope (NodeRun.EffectiveScope is its own separate
// pin, GC-INV-08's own "input hash, effective scope và execution profile"
// naming three siblings, not one merged concept).
type ResolvedExecutionProfileV1 struct {
	// SchemaVersion is this contract's own version — 1 for everything this
	// type currently defines. A future incompatible change to this shape
	// bumps it and mints ResolvedExecutionProfileV2 alongside, the same
	// "version, don't mutate" discipline every other compiled/immutable
	// snapshot in this codebase already follows.
	SchemaVersion uint32 `json:"schemaVersion"`
	// Executor pins the exact AgentProfileVersion/CommandVersion/
	// GateVersion this profile executes under.
	Executor ResolvedExecutorRef `json:"executor"`
	// Policies is every resolved Policy pin governing this execution —
	// ATTEMPT (timeout/retry), PERMISSION (isolation tier/capabilities),
	// CONTEXT, COMPLETION or CLEANUP, in any combination a node's own
	// PolicyRefs declares. Sorted by (DefinitionID, VersionID) for
	// canonicalization — declaration order carries no meaning here, the
	// same "set-like list được normalize/sort" rule go-core-spec §6
	// already applies to a compiled WorkflowDocument.
	Policies []ResolvedPolicyRef `json:"policies"`
	// ProviderKey/Model/ToolRefs/MaxTokens are populated only when
	// Executor.Kind == AGENT (resolved from the pinned AgentProfileVersion's
	// own AgentProfileDocument) — every one must be its zero value
	// otherwise; NewResolvedExecutionProfileV1 enforces this exclusivity
	// the same way workflow.Node's own typed-config fields are mutually
	// exclusive by Type.
	ProviderKey string   `json:"providerKey,omitempty"`
	Model       string   `json:"model,omitempty"`
	ToolRefs    []string `json:"toolRefs,omitempty"`
	MaxTokens   uint32   `json:"maxTokens,omitempty"`
	// EnvAllowlist/NetworkAccess/SecretRefs are populated only when
	// Executor.Kind == COMMAND (resolved from the pinned CommandVersion's
	// own CommandDocument) — every one must be its zero value otherwise.
	// AGENT/MACHINE_GATE never carry these: a provider adapter's own
	// environment/network posture is the adapter's responsibility
	// (go-core-spec §19's own provider "argv template/protocol version/
	// capability allow-list" line), not something a workflow node declares
	// per execution.
	EnvAllowlist  []string `json:"envAllowlist,omitempty"`
	NetworkAccess string   `json:"networkAccess,omitempty"`
	SecretRefs    []string `json:"secretRefs,omitempty"`
	// AdapterBuild optionally pins the exact provider build/protocol this
	// execution must run — nil when nothing pins one yet (a legitimate,
	// deliberately deferred Alpha state, the same optionality
	// workflow.AgentNodeConfig.AdapterBuildID already documents).
	AdapterBuild *ResolvedAdapterBuildRef `json:"adapterBuild,omitempty"`
	// TimeoutSeconds is this execution's own ceiling — required and
	// positive; an execution with no timeout could run forever.
	TimeoutSeconds uint32 `json:"timeoutSeconds"`
	// IsolationTier is ADR-013's own two explicit trust tiers — required.
	IsolationTier policy.IsolationTier `json:"isolationTier"`
	// AllowedCapabilities is the effective set of named capabilities this
	// execution may use, sorted for canonicalization.
	AllowedCapabilities []string `json:"allowedCapabilities,omitempty"`
}

// NewResolvedExecutionProfileV1 validates profile, normalizes every
// set-like field into sorted, deduplicated order (go-core-spec §6's own
// canonicalization discipline: "set-like list được normalize/sort"), and
// returns both the normalized value and its ExecutionProfileHash
// ("sha256:" + hex digest of the normalized value's own canonical JSON —
// struct field order is already fixed by this type's own declaration, so
// json.Marshal alone gives deterministic byte output once every slice
// field is itself sorted).
func NewResolvedExecutionProfileV1(profile ResolvedExecutionProfileV1) (ResolvedExecutionProfileV1, string, error) {
	if profile.SchemaVersion != 1 {
		return ResolvedExecutionProfileV1{}, "", fmt.Errorf("resolved execution profile schema version must be 1, got %d", profile.SchemaVersion)
	}
	if !validExecutorKinds[profile.Executor.Kind] {
		return ResolvedExecutionProfileV1{}, "", fmt.Errorf("resolved execution profile has unsupported executor kind %q", profile.Executor.Kind)
	}
	if strings.TrimSpace(profile.Executor.DefinitionID) == "" || strings.TrimSpace(profile.Executor.VersionID) == "" || strings.TrimSpace(profile.Executor.CompiledHash) == "" {
		return ResolvedExecutionProfileV1{}, "", errors.New("resolved execution profile executor definition id, version id and compiled hash are required")
	}
	if profile.TimeoutSeconds == 0 {
		return ResolvedExecutionProfileV1{}, "", errors.New("resolved execution profile timeout must be greater than zero")
	}
	if !validIsolationTierForProfile(profile.IsolationTier) {
		return ResolvedExecutionProfileV1{}, "", fmt.Errorf("resolved execution profile has unsupported isolation tier %q", profile.IsolationTier)
	}
	for i, p := range profile.Policies {
		if strings.TrimSpace(p.DefinitionID) == "" || strings.TrimSpace(p.VersionID) == "" || strings.TrimSpace(p.CompiledHash) == "" {
			return ResolvedExecutionProfileV1{}, "", fmt.Errorf("resolved execution profile policy[%d] definition id, version id and compiled hash are required", i)
		}
	}
	if profile.AdapterBuild != nil && strings.TrimSpace(profile.AdapterBuild.BuildID) == "" {
		return ResolvedExecutionProfileV1{}, "", errors.New("resolved execution profile adapter build id is required when AdapterBuild is set")
	}

	agentFieldsPopulated := profile.ProviderKey != "" || profile.Model != "" || len(profile.ToolRefs) > 0 || profile.MaxTokens != 0
	commandFieldsPopulated := len(profile.EnvAllowlist) > 0 || profile.NetworkAccess != "" || len(profile.SecretRefs) > 0
	switch profile.Executor.Kind {
	case ExecutorKindAgent:
		if commandFieldsPopulated {
			return ResolvedExecutionProfileV1{}, "", errors.New("resolved execution profile: AGENT executor must not populate EnvAllowlist/NetworkAccess/SecretRefs")
		}
		if strings.TrimSpace(profile.ProviderKey) == "" || strings.TrimSpace(profile.Model) == "" {
			return ResolvedExecutionProfileV1{}, "", errors.New("resolved execution profile: AGENT executor requires ProviderKey and Model")
		}
	case ExecutorKindCommand:
		if agentFieldsPopulated {
			return ResolvedExecutionProfileV1{}, "", errors.New("resolved execution profile: COMMAND executor must not populate ProviderKey/Model/ToolRefs/MaxTokens")
		}
		if strings.TrimSpace(profile.NetworkAccess) == "" {
			return ResolvedExecutionProfileV1{}, "", errors.New("resolved execution profile: COMMAND executor requires NetworkAccess")
		}
	case ExecutorKindMachineGate:
		if agentFieldsPopulated || commandFieldsPopulated {
			return ResolvedExecutionProfileV1{}, "", errors.New("resolved execution profile: MACHINE_GATE executor must not populate AGENT or COMMAND fields")
		}
	}

	normalized := profile
	normalized.Policies = append([]ResolvedPolicyRef(nil), profile.Policies...)
	sort.Slice(normalized.Policies, func(i, j int) bool {
		if normalized.Policies[i].DefinitionID != normalized.Policies[j].DefinitionID {
			return normalized.Policies[i].DefinitionID < normalized.Policies[j].DefinitionID
		}
		return normalized.Policies[i].VersionID < normalized.Policies[j].VersionID
	})
	normalized.ToolRefs = sortedUniqueStrings(profile.ToolRefs)
	normalized.EnvAllowlist = sortedUniqueStrings(profile.EnvAllowlist)
	normalized.SecretRefs = sortedUniqueStrings(profile.SecretRefs)
	normalized.AllowedCapabilities = sortedUniqueStrings(profile.AllowedCapabilities)
	if profile.AdapterBuild != nil {
		adapterBuild := *profile.AdapterBuild
		normalized.AdapterBuild = &adapterBuild
	}

	canonical, err := json.Marshal(normalized)
	if err != nil {
		return ResolvedExecutionProfileV1{}, "", fmt.Errorf("canonicalize resolved execution profile: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return normalized, "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validIsolationTierForProfile(tier policy.IsolationTier) bool {
	return tier == policy.IsolationTierEnforcedIsolated || tier == policy.IsolationTierOperatorTrustedLocal
}

func sortedUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		unique = append(unique, v)
	}
	sort.Strings(unique)
	return unique
}
