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
