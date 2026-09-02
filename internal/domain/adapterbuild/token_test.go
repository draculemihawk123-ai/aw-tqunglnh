package adapterbuild_test

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

func testKey() []byte { return []byte("test-signing-key-32-bytes-long!!") }

func TestSignToken_ThenVerifyToken_Succeeds(t *testing.T) {
	token, err := adapterbuild.SignToken(validTuple(), "nonce-1", time.Now().UTC().Add(5*time.Minute), testKey())
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	if err := adapterbuild.VerifyToken(token, testKey(), time.Now().UTC()); err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
}

func TestVerifyToken_RejectsWrongKey(t *testing.T) {
	token, err := adapterbuild.SignToken(validTuple(), "nonce-1", time.Now().UTC().Add(5*time.Minute), testKey())
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	otherKey := []byte("a-completely-different-key-value")
	if err := adapterbuild.VerifyToken(token, otherKey, time.Now().UTC()); err == nil {
		t.Fatal("VerifyToken should reject a token signed with a different key")
	}
}

// TestVerifyToken_RejectsTamperedTuple is the literal TOCTOU-closing
// property ADR-022 requires: a token whose Tuple was modified after
// signing (e.g. an attacker trying to claim a token issued for one
// executable actually covers a different one) must fail verification.
func TestVerifyToken_RejectsTamperedTuple(t *testing.T) {
	token, err := adapterbuild.SignToken(validTuple(), "nonce-1", time.Now().UTC().Add(5*time.Minute), testKey())
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	token.Tuple.ExecutableContentHash = "sha256:tampered"
	if err := adapterbuild.VerifyToken(token, testKey(), time.Now().UTC()); err == nil {
		t.Fatal("VerifyToken should reject a token whose tuple was tampered with after signing")
	}
}

// TestVerifyToken_RejectsTamperedTupleField is V2-07B's own addition to
// TestVerifyToken_RejectsTamperedTuple above: since a candidate token
// binds the WHOLE tuple (docs/design/04-v2-definition-plane.md V2-07B —
// "token bind toàn bộ tuple"), tampering with ANY single axis, not just
// ExecutableContentHash (already covered above), must independently fail
// verification. This enumerates every axis V2-07B's own Verify line calls
// out by name — protocol version, capability-manifest hash, and
// OS/toolchain/config identity — plus the two remaining tuple fields for
// completeness.
func TestVerifyToken_RejectsTamperedTupleField(t *testing.T) {
	mutations := map[string]func(*adapterbuild.CandidateTuple){
		"providerKey":            func(tuple *adapterbuild.CandidateTuple) { tuple.ProviderKey = "codex" },
		"executablePath":         func(tuple *adapterbuild.CandidateTuple) { tuple.ExecutablePath = "/usr/local/bin/other" },
		"protocolVersion":        func(tuple *adapterbuild.CandidateTuple) { tuple.ProtocolVersion = "claude-stream-json/v2" },
		"capabilityManifestHash": func(tuple *adapterbuild.CandidateTuple) { tuple.CapabilityManifestHash = "sha256:tampered" },
		"os":                     func(tuple *adapterbuild.CandidateTuple) { tuple.OS = "windows" },
		"toolchain":              func(tuple *adapterbuild.CandidateTuple) { tuple.Toolchain = "node-22" },
		"configIdentity":         func(tuple *adapterbuild.CandidateTuple) { tuple.ConfigIdentity = "alternate" },
	}
	for field, mutate := range mutations {
		t.Run(field, func(t *testing.T) {
			token, err := adapterbuild.SignToken(validTuple(), "nonce-1", time.Now().UTC().Add(5*time.Minute), testKey())
			if err != nil {
				t.Fatalf("SignToken: %v", err)
			}
			mutate(&token.Tuple)
			if err := adapterbuild.VerifyToken(token, testKey(), time.Now().UTC()); err == nil {
				t.Fatalf("VerifyToken should reject a token whose %s was tampered with after signing", field)
			}
		})
	}
}

func TestVerifyToken_RejectsExpiredToken(t *testing.T) {
	expiresAt := time.Now().UTC().Add(-1 * time.Second)
	token, err := adapterbuild.SignToken(validTuple(), "nonce-1", expiresAt, testKey())
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	err = adapterbuild.VerifyToken(token, testKey(), time.Now().UTC())
	if err == nil {
		t.Fatal("VerifyToken should reject an expired token")
	}
}

func TestSignToken_RejectsInvalidTuple(t *testing.T) {
	tuple := validTuple()
	tuple.ProviderKey = ""
	if _, err := adapterbuild.SignToken(tuple, "nonce-1", time.Now().UTC().Add(5*time.Minute), testKey()); err == nil {
		t.Fatal("SignToken should reject an invalid tuple")
	}
}

func TestSignToken_RejectsEmptyNonceOrExpiryOrKey(t *testing.T) {
	future := time.Now().UTC().Add(5 * time.Minute)
	if _, err := adapterbuild.SignToken(validTuple(), "", future, testKey()); err == nil {
		t.Fatal("SignToken should reject an empty nonce")
	}
	if _, err := adapterbuild.SignToken(validTuple(), "nonce-1", time.Time{}, testKey()); err == nil {
		t.Fatal("SignToken should reject a zero expiry")
	}
	if _, err := adapterbuild.SignToken(validTuple(), "nonce-1", future, nil); err == nil {
		t.Fatal("SignToken should reject an empty key")
	}
}

func TestSignToken_Deterministic(t *testing.T) {
	expiresAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	first, err := adapterbuild.SignToken(validTuple(), "nonce-1", expiresAt, testKey())
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	second, err := adapterbuild.SignToken(validTuple(), "nonce-1", expiresAt, testKey())
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	if first.Signature != second.Signature {
		t.Fatalf("signature not deterministic: %s vs %s", first.Signature, second.Signature)
	}
}
