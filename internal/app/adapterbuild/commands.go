// Package adapterbuild is the application-command layer for V2-07A's
// immutable AdapterBuildVersion registry (docs/design/04-v2-definition-plane.md
// V2-07A, ADR-022): ProbeAdapterBuild, RegisterAdapterBuild, ListAdapterBuilds
// and GetAdapterBuild — the four application commands V2-07A's own scope
// line names. Neither command spawns a real provider CLI to verify its
// protocol/capability behavior (that is V5-06/07's job): Probe only
// hashes the configured executable's file content, the same fingerprint-
// only observation internal/app/doctor already makes, graduated here into
// a real, persisted, signed registration flow.
package adapterbuild

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// tokenTTL is how long a candidate token remains valid — ADR-022 asks
// for "expiry ngắn" (short expiry) without naming a duration; 5 minutes
// is long enough for an operator to read the printed candidate and
// confirm registration, short enough that a stale, unused token is never
// a standing risk.
const tokenTTL = 5 * time.Minute

// ErrExecutableDrift is returned by RegisterAdapterBuild when the
// executable at the token's bound path no longer hashes to the content
// the token pinned — this is V2-07A's own "build khác exact pin bị
// reject" bar, enforced by re-measuring outside the transaction and
// comparing against what the operator actually confirmed (ADR-022's
// TOCTOU-closing re-probe step).
var ErrExecutableDrift = errors.New("adapterbuild: executable no longer matches the content hash the candidate token pinned")

// ErrCapabilityManifestDrift is returned by RegisterAdapterBuild when the
// freshly-supplied capability manifest does not hash to what the token
// bound — the same re-measurement principle applied to the declared
// manifest, not just the executable file.
var ErrCapabilityManifestDrift = errors.New("adapterbuild: capability manifest no longer matches what the candidate token pinned")

// ProbeRequest is what a caller supplies to ProbeAdapterBuild.
type ProbeRequest struct {
	ProviderKey        string
	ExecutablePath     string
	ProtocolVersion    string
	CapabilityManifest adapterbuild.CapabilityManifest
	OS                 string
	Toolchain          string
	ConfigIdentity     string
}

// ProbeAdapterBuild measures the configured executable's content hash
// and the declared capability manifest's hash, binds the whole tuple
// into a server-signed CandidateToken, and returns it — without ever
// mutating the adapter build registry itself (go-core-spec.md's own
// command table: "không mutate registry"). The one write it may perform
// is lazily bootstrapping the per-installation signing key on first use,
// which is infrastructure the registry depends on, not a registry row.
func ProbeAdapterBuild(ctx context.Context, uow ports.UnitOfWork, req ProbeRequest) (adapterbuild.CandidateToken, error) {
	if strings.TrimSpace(req.ProviderKey) == "" {
		return adapterbuild.CandidateToken{}, errors.New("adapterbuild: providerKey is required")
	}
	if strings.TrimSpace(req.ExecutablePath) == "" {
		return adapterbuild.CandidateToken{}, errors.New("adapterbuild: executablePath is required")
	}
	if strings.TrimSpace(req.ProtocolVersion) == "" {
		return adapterbuild.CandidateToken{}, errors.New("adapterbuild: protocolVersion is required")
	}
	if err := adapterbuild.ValidateCapabilityManifest(req.CapabilityManifest); err != nil {
		return adapterbuild.CandidateToken{}, err
	}
	if strings.TrimSpace(req.OS) == "" || strings.TrimSpace(req.Toolchain) == "" || strings.TrimSpace(req.ConfigIdentity) == "" {
		return adapterbuild.CandidateToken{}, errors.New("adapterbuild: os, toolchain and configIdentity are all required")
	}

	contentHash, err := HashExecutableFile(req.ExecutablePath)
	if err != nil {
		return adapterbuild.CandidateToken{}, fmt.Errorf("adapterbuild: measure executable: %w", err)
	}
	_, manifestHash, err := adapterbuild.HashCapabilityManifest(req.CapabilityManifest)
	if err != nil {
		return adapterbuild.CandidateToken{}, err
	}

	tuple := adapterbuild.CandidateTuple{
		ProviderKey:            req.ProviderKey,
		ExecutablePath:         req.ExecutablePath,
		ExecutableContentHash:  contentHash,
		ProtocolVersion:        req.ProtocolVersion,
		CapabilityManifestHash: manifestHash,
		OS:                     req.OS,
		Toolchain:              req.Toolchain,
		ConfigIdentity:         req.ConfigIdentity,
	}

	var key []byte
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		loaded, err := tx.AdapterBuilds().LoadOrCreateSigningKey(ctx)
		key = loaded
		return err
	}); err != nil {
		return adapterbuild.CandidateToken{}, err
	}

	nonce, err := randomNonce()
	if err != nil {
		return adapterbuild.CandidateToken{}, err
	}
	expiresAt := time.Now().UTC().Add(tokenTTL)
	return adapterbuild.SignToken(tuple, nonce, expiresAt, key)
}

// RegisterRequest is what a caller supplies to RegisterAdapterBuild. The
// caller supplies CapabilityManifest again (not just the token) so
// Register can re-derive its hash and compare against what the token
// bound, the same TOCTOU-closing re-measurement ADR-022 requires of the
// executable file.
type RegisterRequest struct {
	Token              adapterbuild.CandidateToken
	CapabilityManifest adapterbuild.CapabilityManifest
	RegisteredBy       string
}

// RegisterResult is what RegisterAdapterBuild returns.
type RegisterResult struct {
	Build          adapterbuild.Build
	AlreadyExisted bool
}

// RegisterAdapterBuild verifies req.Token (signature and expiry), then —
// entirely outside any database transaction, per ADR-022's own explicit
// ordering ("re-probe/re-hash executable ngoài database transaction,
// ngay trước commit") — re-measures the executable file and the supplied
// capability manifest, rejecting with ErrExecutableDrift/
// ErrCapabilityManifestDrift if either no longer matches what the token
// pinned. Only once both match does it open a transaction, and even then
// persists only the values it just measured server-side, never anything
// the token or the caller merely claimed.
func RegisterAdapterBuild(ctx context.Context, uow ports.UnitOfWork, req RegisterRequest) (RegisterResult, error) {
	if strings.TrimSpace(req.RegisteredBy) == "" {
		return RegisterResult{}, errors.New("adapterbuild: registeredBy is required")
	}

	var key []byte
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, err := tx.AdapterBuilds().LoadSigningKey(ctx)
		key = loaded
		return err
	}); err != nil {
		return RegisterResult{}, err
	}
	if err := adapterbuild.VerifyToken(req.Token, key, time.Now().UTC()); err != nil {
		return RegisterResult{}, err
	}

	freshExecutableHash, err := HashExecutableFile(req.Token.Tuple.ExecutablePath)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("adapterbuild: re-measure executable: %w", err)
	}
	if freshExecutableHash != req.Token.Tuple.ExecutableContentHash {
		return RegisterResult{}, ErrExecutableDrift
	}
	_, freshManifestHash, err := adapterbuild.HashCapabilityManifest(req.CapabilityManifest)
	if err != nil {
		return RegisterResult{}, err
	}
	if freshManifestHash != req.Token.Tuple.CapabilityManifestHash {
		return RegisterResult{}, ErrCapabilityManifestDrift
	}

	build, err := adapterbuild.NewBuild(adapterbuild.NewBuildRequest{
		Tuple:              req.Token.Tuple,
		CapabilityManifest: req.CapabilityManifest,
		RegisteredBy:       req.RegisteredBy,
		RegisteredAt:       time.Now().UTC(),
	})
	if err != nil {
		return RegisterResult{}, err
	}

	var result RegisterResult
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		persisted, alreadyExisted, err := tx.AdapterBuilds().InsertIfAbsent(ctx, build)
		result = RegisterResult{Build: persisted, AlreadyExisted: alreadyExisted}
		return err
	})
	return result, err
}

// ListAdapterBuilds returns every registered build.
func ListAdapterBuilds(ctx context.Context, uow ports.UnitOfWork) ([]adapterbuild.Build, error) {
	var builds []adapterbuild.Build
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, err := tx.AdapterBuilds().List(ctx)
		builds = loaded
		return err
	})
	return builds, err
}

// GetAdapterBuild returns the build with the given ID, or
// ports.ErrAdapterBuildNotFound.
func GetAdapterBuild(ctx context.Context, uow ports.UnitOfWork, id string) (adapterbuild.Build, error) {
	var build adapterbuild.Build
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		loaded, err := tx.AdapterBuilds().Get(ctx, id)
		build = loaded
		return err
	})
	return build, err
}

// HashExecutableFile returns the sha256 content hash of the file at path,
// prefixed "sha256:" — exported so VerifyNoDrift (drift.go) can reuse the
// exact same measurement ProbeAdapterBuild/RegisterAdapterBuild already use
// for registration, rather than a second, potentially-diverging
// implementation.
func HashExecutableFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func randomNonce() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("adapterbuild: generate nonce: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
