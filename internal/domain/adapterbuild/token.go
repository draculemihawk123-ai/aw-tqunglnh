package adapterbuild

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
)

// CandidateToken is ADR-022's server-signed candidate token: the artifact
// ProbeAdapterBuild hands back to an operator to confirm, and the exact
// thing RegisterAdapterBuild verifies before ever touching the registry.
// It binds the whole measured CandidateTuple (never just a bare
// fingerprint — see CandidateTuple's own doc comment for why), plus a
// nonce and a short expiry, signed with a per-installation key so a token
// can never be forged or replayed after the key rotates.
type CandidateToken struct {
	Tuple     CandidateTuple `json:"tuple"`
	Nonce     string         `json:"nonce"`
	ExpiresAt time.Time      `json:"expiresAt"`
	Signature string         `json:"signature"`
}

// signingPayload is the exact value SignToken/VerifyToken sign — Tuple
// and Nonce and ExpiresAt, and nothing else (Signature is obviously
// excluded: it is the output, not part of the input).
type signingPayload struct {
	Tuple     CandidateTuple `json:"tuple"`
	Nonce     string         `json:"nonce"`
	ExpiresAt string         `json:"expiresAt"`
}

// ErrInvalidSignature is returned by VerifyToken when a token's signature
// does not match what the given key would have produced — a forged
// token, a token signed under a since-rotated key, or a token whose Tuple
// (or Nonce/ExpiresAt) was tampered with after signing all produce this
// same error, since HMAC gives no way to distinguish those cases (nor
// should it).
var ErrInvalidSignature = errors.New("adapterbuild: candidate token signature is invalid")

// ErrTokenExpired is returned by VerifyToken for an otherwise
// well-signed token whose ExpiresAt has already passed.
var ErrTokenExpired = errors.New("adapterbuild: candidate token has expired")

// SignToken produces a CandidateToken for tuple, signed with key. nonce
// must already be a caller-generated random value (this package performs
// no I/O, including randomness, keeping it pure computation like the rest
// of internal/domain) — internal/app/adapterbuild is where a real nonce
// is generated via crypto/rand.
func SignToken(tuple CandidateTuple, nonce string, expiresAt time.Time, key []byte) (CandidateToken, error) {
	if err := tuple.validate(); err != nil {
		return CandidateToken{}, err
	}
	if strings.TrimSpace(nonce) == "" {
		return CandidateToken{}, errors.New("adapterbuild: nonce is required")
	}
	if expiresAt.IsZero() {
		return CandidateToken{}, errors.New("adapterbuild: expiresAt is required")
	}
	if len(key) == 0 {
		return CandidateToken{}, errors.New("adapterbuild: signing key is required")
	}
	signature, err := sign(tuple, nonce, expiresAt, key)
	if err != nil {
		return CandidateToken{}, err
	}
	return CandidateToken{Tuple: tuple, Nonce: nonce, ExpiresAt: expiresAt, Signature: signature}, nil
}

// VerifyToken checks token's signature against key and that it has not
// expired as of now. A caller must check the error's identity (errors.Is
// against ErrInvalidSignature/ErrTokenExpired) rather than treat any
// non-nil error the same way, since an expired-but-genuinely-signed token
// and a forged token warrant different operator-facing messages.
func VerifyToken(token CandidateToken, key []byte, now time.Time) error {
	if len(key) == 0 {
		return errors.New("adapterbuild: signing key is required")
	}
	expected, err := sign(token.Tuple, token.Nonce, token.ExpiresAt, key)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(expected), []byte(token.Signature)) {
		return ErrInvalidSignature
	}
	if !now.Before(token.ExpiresAt) {
		return ErrTokenExpired
	}
	return nil
}

func sign(tuple CandidateTuple, nonce string, expiresAt time.Time, key []byte) (string, error) {
	canonical, _, err := authoring.Canonicalize(signingPayload{
		Tuple:     tuple,
		Nonce:     nonce,
		ExpiresAt: expiresAt.UTC().Format(time.RFC3339Nano),
	}, authoring.CanonicalizeOptions{})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(canonical)
	return hex.EncodeToString(mac.Sum(nil)), nil
}
