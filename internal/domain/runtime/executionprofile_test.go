package runtime

import (
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
)

func validAgentProfile() ResolvedExecutionProfileV1 {
	return ResolvedExecutionProfileV1{
		SchemaVersion: 1,
		Executor: ResolvedExecutorRef{
			Kind: ExecutorKindAgent, DefinitionID: "agent-default", VersionID: "v1", CompiledHash: "sha256:agent-v1",
		},
		Policies: []ResolvedPolicyRef{
			{DefinitionID: "attempt-policy", VersionID: "v1", Category: policy.CategoryAttempt, CompiledHash: "sha256:attempt-v1"},
			{DefinitionID: "permission-policy", VersionID: "v1", Category: policy.CategoryPermission, CompiledHash: "sha256:permission-v1"},
		},
		ProviderKey: "claude", Model: "claude-sonnet", ToolRefs: []string{"read_file", "write_file"}, MaxTokens: 100000,
		AdapterBuild:               &ResolvedAdapterBuildRef{BuildID: "sha256:build-1", ProtocolVersion: "1", CapabilityHash: "sha256:capability-1"},
		RuntimeExecutionConfigHash: "sha256:runtime-config-1",
		TimeoutSeconds:             3600, IsolationTier: policy.IsolationTierEnforcedIsolated,
		AllowedCapabilities: []string{"WRITE_REPOSITORY"},
	}
}

func validCommandProfile() ResolvedExecutionProfileV1 {
	return ResolvedExecutionProfileV1{
		SchemaVersion: 1,
		Executor: ResolvedExecutorRef{
			Kind: ExecutorKindCommand, DefinitionID: "command-test", VersionID: "v1", CompiledHash: "sha256:command-v1",
		},
		Policies:                   []ResolvedPolicyRef{{DefinitionID: "attempt-policy", VersionID: "v1", Category: policy.CategoryAttempt, CompiledHash: "sha256:attempt-v1"}},
		RuntimeExecutionConfigHash: "sha256:runtime-config-1",
		TimeoutSeconds:             300, IsolationTier: policy.IsolationTierEnforcedIsolated,
	}
}

func validMachineGateProfile() ResolvedExecutionProfileV1 {
	return ResolvedExecutionProfileV1{
		SchemaVersion: 1,
		Executor: ResolvedExecutorRef{
			Kind: ExecutorKindMachineGate, DefinitionID: "gate-verify", VersionID: "v1", CompiledHash: "sha256:gate-v1",
		},
		Policies:                   []ResolvedPolicyRef{{DefinitionID: "gate-policy", VersionID: "v1", Category: policy.CategoryCompletion, CompiledHash: "sha256:gate-policy-v1"}},
		RuntimeExecutionConfigHash: "sha256:runtime-config-1",
		TimeoutSeconds:             120, IsolationTier: policy.IsolationTierOperatorTrustedLocal,
	}
}

func TestNewResolvedExecutionProfileV1_AcceptsEachExecutorKind(t *testing.T) {
	for name, profile := range map[string]ResolvedExecutionProfileV1{
		"agent": validAgentProfile(), "command": validCommandProfile(), "machine_gate": validMachineGateProfile(),
	} {
		t.Run(name, func(t *testing.T) {
			normalized, hash, err := NewResolvedExecutionProfileV1(profile)
			if err != nil {
				t.Fatalf("NewResolvedExecutionProfileV1(%s): %v", name, err)
			}
			if hash == "" || !strings.HasPrefix(hash, "sha256:") {
				t.Fatalf("hash = %q, want a sha256:-prefixed digest", hash)
			}
			if normalized.SchemaVersion != 1 {
				t.Fatalf("normalized.SchemaVersion = %d, want 1", normalized.SchemaVersion)
			}
		})
	}
}

func TestNewResolvedExecutionProfileV1_HashIsDeterministicAndOrderIndependent(t *testing.T) {
	profile := validAgentProfile()
	_, hashA, err := NewResolvedExecutionProfileV1(profile)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Reorder set-like fields and Policies — canonicalization must sort
	// them, so the hash stays identical.
	reordered := profile
	reordered.ToolRefs = []string{"write_file", "read_file"}
	reordered.Policies = []ResolvedPolicyRef{profile.Policies[1], profile.Policies[0]}
	_, hashB, err := NewResolvedExecutionProfileV1(reordered)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if hashA != hashB {
		t.Fatalf("hashA = %s, hashB = %s, want identical (reordering set-like fields must not change the hash)", hashA, hashB)
	}
}

func TestNewResolvedExecutionProfileV1_DifferentContentProducesDifferentHash(t *testing.T) {
	base := validAgentProfile()
	_, hashA, err := NewResolvedExecutionProfileV1(base)
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	changed := base
	changed.Model = "claude-opus"
	_, hashB, err := NewResolvedExecutionProfileV1(changed)
	if err != nil {
		t.Fatalf("changed: %v", err)
	}
	if hashA == hashB {
		t.Fatal("changing Model produced the same hash, want a different one")
	}
}

// TestNewResolvedExecutionProfileV1_RuntimeExecutionConfigDifference_ChangesHash
// is this task's own most important correctness proof (a real gap the
// previous version of this type had): two otherwise-identical AGENT
// profiles that differ ONLY in their effective runtime execution config
// (environment/network/secret/output-limit policy) must never hash the
// same.
func TestNewResolvedExecutionProfileV1_RuntimeExecutionConfigDifference_ChangesHash(t *testing.T) {
	base := validAgentProfile()
	_, hashA, err := NewResolvedExecutionProfileV1(base)
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	changed := base
	changed.RuntimeExecutionConfigHash = "sha256:runtime-config-2"
	_, hashB, err := NewResolvedExecutionProfileV1(changed)
	if err != nil {
		t.Fatalf("changed: %v", err)
	}
	if hashA == hashB {
		t.Fatal("changing RuntimeExecutionConfigHash produced the same hash, want a different one")
	}
}

func TestNewResolvedExecutionProfileV1_DeduplicatesSetLikeFields(t *testing.T) {
	profile := validAgentProfile()
	profile.ToolRefs = []string{"read_file", "read_file", "write_file"}
	normalized, _, err := NewResolvedExecutionProfileV1(profile)
	if err != nil {
		t.Fatalf("NewResolvedExecutionProfileV1: %v", err)
	}
	if len(normalized.ToolRefs) != 2 {
		t.Fatalf("normalized.ToolRefs = %v, want exactly 2 deduplicated entries", normalized.ToolRefs)
	}
}

// TestNewResolvedExecutionProfileV1_DeduplicatesRepeatedPolicy is a
// correction found during review: a repeated Policy pin used to hash
// differently from the same profile without the repeat.
func TestNewResolvedExecutionProfileV1_DeduplicatesRepeatedPolicy(t *testing.T) {
	withRepeat := validAgentProfile()
	withRepeat.Policies = append(withRepeat.Policies, withRepeat.Policies[0])
	normalized, hashWithRepeat, err := NewResolvedExecutionProfileV1(withRepeat)
	if err != nil {
		t.Fatalf("NewResolvedExecutionProfileV1(withRepeat): %v", err)
	}
	if len(normalized.Policies) != 2 {
		t.Fatalf("normalized.Policies = %+v, want exactly 2 deduplicated entries", normalized.Policies)
	}

	_, hashWithoutRepeat, err := NewResolvedExecutionProfileV1(validAgentProfile())
	if err != nil {
		t.Fatalf("NewResolvedExecutionProfileV1(withoutRepeat): %v", err)
	}
	if hashWithRepeat != hashWithoutRepeat {
		t.Fatalf("hashWithRepeat = %s, hashWithoutRepeat = %s, want identical (a repeated pin must not change the hash)", hashWithRepeat, hashWithoutRepeat)
	}
}

func TestNewResolvedExecutionProfileV1_RejectsInvalidProfiles(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(ResolvedExecutionProfileV1) ResolvedExecutionProfileV1
		base    func() ResolvedExecutionProfileV1
		wantErr string
	}{
		{
			name: "wrong schema version",
			base: validAgentProfile,
			mutate: func(p ResolvedExecutionProfileV1) ResolvedExecutionProfileV1 {
				p.SchemaVersion = 2
				return p
			},
			wantErr: "schema version must be 1",
		},
		{
			name: "unsupported executor kind",
			base: validAgentProfile,
			mutate: func(p ResolvedExecutionProfileV1) ResolvedExecutionProfileV1 {
				p.Executor.Kind = "ROUTER"
				return p
			},
			wantErr: "unsupported executor kind",
		},
		{
			name: "missing executor compiled hash",
			base: validAgentProfile,
			mutate: func(p ResolvedExecutionProfileV1) ResolvedExecutionProfileV1 {
				p.Executor.CompiledHash = ""
				return p
			},
			wantErr: "executor definition id, version id and compiled hash are required",
		},
		{
			name: "zero timeout",
			base: validAgentProfile,
			mutate: func(p ResolvedExecutionProfileV1) ResolvedExecutionProfileV1 {
				p.TimeoutSeconds = 0
				return p
			},
			wantErr: "timeout must be greater than zero",
		},
		{
			name: "invalid isolation tier",
			base: validAgentProfile,
			mutate: func(p ResolvedExecutionProfileV1) ResolvedExecutionProfileV1 {
				p.IsolationTier = "SOMETIMES_ISOLATED"
				return p
			},
			wantErr: "unsupported isolation tier",
		},
		{
			name: "missing runtime execution config hash",
			base: validAgentProfile,
			mutate: func(p ResolvedExecutionProfileV1) ResolvedExecutionProfileV1 {
				p.RuntimeExecutionConfigHash = ""
				return p
			},
			wantErr: "runtime execution config hash is required",
		},
		{
			name: "policy missing compiled hash",
			base: validAgentProfile,
			mutate: func(p ResolvedExecutionProfileV1) ResolvedExecutionProfileV1 {
				p.Policies[0].CompiledHash = ""
				return p
			},
			wantErr: "compiled hash are required",
		},
		{
			name: "policy has invalid category",
			base: validAgentProfile,
			mutate: func(p ResolvedExecutionProfileV1) ResolvedExecutionProfileV1 {
				p.Policies[0].Category = "NOT_A_CATEGORY"
				return p
			},
			wantErr: "unsupported category",
		},
		{
			name: "adapter build with blank protocol/capability",
			base: validAgentProfile,
			mutate: func(p ResolvedExecutionProfileV1) ResolvedExecutionProfileV1 {
				p.AdapterBuild = &ResolvedAdapterBuildRef{BuildID: "sha256:build-1"}
				return p
			},
			wantErr: "adapter build id, protocol version and capability hash are all required",
		},
		{
			name: "adapter build on COMMAND executor",
			base: validCommandProfile,
			mutate: func(p ResolvedExecutionProfileV1) ResolvedExecutionProfileV1 {
				p.AdapterBuild = &ResolvedAdapterBuildRef{BuildID: "sha256:build-1", ProtocolVersion: "1", CapabilityHash: "sha256:capability-1"}
				return p
			},
			wantErr: "AdapterBuild is only meaningful for the AGENT executor",
		},
		{
			name: "AGENT executor missing ProviderKey",
			base: validAgentProfile,
			mutate: func(p ResolvedExecutionProfileV1) ResolvedExecutionProfileV1 {
				p.ProviderKey = ""
				return p
			},
			wantErr: "AGENT executor requires ProviderKey and Model",
		},
		{
			name: "COMMAND executor with AGENT fields populated",
			base: validCommandProfile,
			mutate: func(p ResolvedExecutionProfileV1) ResolvedExecutionProfileV1 {
				p.ProviderKey = "claude"
				return p
			},
			wantErr: "COMMAND executor must not populate",
		},
		{
			name: "MACHINE_GATE executor with AGENT fields populated",
			base: validMachineGateProfile,
			mutate: func(p ResolvedExecutionProfileV1) ResolvedExecutionProfileV1 {
				p.Model = "claude-sonnet"
				return p
			},
			wantErr: "MACHINE_GATE executor must not populate",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := NewResolvedExecutionProfileV1(test.mutate(test.base()))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, test.wantErr)
			}
		})
	}
}
