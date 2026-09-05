package runtime

import (
	"strings"
	"testing"
)

func validRuntimeExecutionConfigSnapshot() RuntimeExecutionConfigSnapshotV1 {
	return RuntimeExecutionConfigSnapshotV1{
		SchemaVersion: 1, ProcessOutputLimitBytes: 1 << 20,
		EnvAllowlist: []string{"PATH", "HOME"}, NetworkAccess: NetworkAccessNone,
		SecretRefs: []string{"npm-token"},
	}
}

func TestNewRuntimeExecutionConfigSnapshotV1_AcceptsValidSnapshot(t *testing.T) {
	normalized, hash, err := NewRuntimeExecutionConfigSnapshotV1(validRuntimeExecutionConfigSnapshot())
	if err != nil {
		t.Fatalf("NewRuntimeExecutionConfigSnapshotV1: %v", err)
	}
	if hash == "" || !strings.HasPrefix(hash, "sha256:") {
		t.Fatalf("hash = %q, want a sha256:-prefixed digest", hash)
	}
	if len(normalized.EnvAllowlist) != 2 {
		t.Fatalf("normalized.EnvAllowlist = %v, want 2 entries", normalized.EnvAllowlist)
	}
}

func TestNewRuntimeExecutionConfigSnapshotV1_HashIsDeterministicAndOrderIndependent(t *testing.T) {
	base := validRuntimeExecutionConfigSnapshot()
	_, hashA, err := NewRuntimeExecutionConfigSnapshotV1(base)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	reordered := base
	reordered.EnvAllowlist = []string{"HOME", "PATH"}
	_, hashB, err := NewRuntimeExecutionConfigSnapshotV1(reordered)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if hashA != hashB {
		t.Fatalf("hashA = %s, hashB = %s, want identical", hashA, hashB)
	}
}

func TestNewRuntimeExecutionConfigSnapshotV1_DifferentNetworkAccessProducesDifferentHash(t *testing.T) {
	base := validRuntimeExecutionConfigSnapshot()
	_, hashA, err := NewRuntimeExecutionConfigSnapshotV1(base)
	if err != nil {
		t.Fatalf("base: %v", err)
	}
	changed := base
	changed.NetworkAccess = NetworkAccessAllowed
	_, hashB, err := NewRuntimeExecutionConfigSnapshotV1(changed)
	if err != nil {
		t.Fatalf("changed: %v", err)
	}
	if hashA == hashB {
		t.Fatal("changing NetworkAccess produced the same hash, want a different one")
	}
}

func TestNewRuntimeExecutionConfigSnapshotV1_RejectsInvalidSnapshots(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(RuntimeExecutionConfigSnapshotV1) RuntimeExecutionConfigSnapshotV1
		wantErr string
	}{
		{
			name: "wrong schema version",
			mutate: func(s RuntimeExecutionConfigSnapshotV1) RuntimeExecutionConfigSnapshotV1 {
				s.SchemaVersion = 2
				return s
			},
			wantErr: "schema version must be 1",
		},
		{
			name: "zero output limit",
			mutate: func(s RuntimeExecutionConfigSnapshotV1) RuntimeExecutionConfigSnapshotV1 {
				s.ProcessOutputLimitBytes = 0
				return s
			},
			wantErr: "process output limit must be greater than zero",
		},
		{
			name: "invalid network access",
			mutate: func(s RuntimeExecutionConfigSnapshotV1) RuntimeExecutionConfigSnapshotV1 {
				s.NetworkAccess = "SOMETIMES"
				return s
			},
			wantErr: "unsupported network access",
		},
		{
			name: "blank env allowlist entry",
			mutate: func(s RuntimeExecutionConfigSnapshotV1) RuntimeExecutionConfigSnapshotV1 {
				s.EnvAllowlist = append(s.EnvAllowlist, "  ")
				return s
			},
			wantErr: "is blank",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := NewRuntimeExecutionConfigSnapshotV1(test.mutate(validRuntimeExecutionConfigSnapshot()))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, test.wantErr)
			}
		})
	}
}
