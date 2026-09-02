// Package adapterbuild is the immutable AdapterBuildVersion registry's
// domain model (docs/design/04-v2-definition-plane.md V2-07A, ADR-022,
// ADR-012, AK-ARCH-020A, HE-02-M07): a provider adapter build's identity
// — content-hash-pinned, never a mutable config string — so a Run/Attempt
// can pin exactly which build it executed against and later admission can
// reject a build that has drifted from that pin.
//
// AdapterBuildVersion is deliberately NOT a DefinitionKind (ADR-022): it
// never uses the Definition/Version pair, never goes through
// POST /definitions/{kind}/publish, and never appears in authoring's
// catalog — internal/domain/definition.Kind's own closed enum and
// definition.DependencyPin's Kind field are structurally incapable of
// referencing one. This package is a wholly separate, standalone model;
// it does not import internal/domain/definition at all.
//
// A Build's identity is content-addressed: its ID is a hash of its own
// CandidateTuple (mirroring ADR-012's resource identity pattern of
// owner_version_id+resource_key+content_hash), so registering the exact
// same measured tuple twice is naturally idempotent, and no code path in
// this package or its callers ever updates or deletes an existing row —
// the whole "version đã publish không sửa được" guarantee falls out of
// the identity scheme itself, not an access-control check.
package adapterbuild

import (
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
)

// CandidateTuple is the exact set of facts ADR-022 requires a candidate
// token to bind: "provider key, canonical executable path và content
// hash, protocol version, hash của capability manifest, OS/toolchain/
// config identity, một nonce và expiry" (nonce/expiry live on
// CandidateToken, not the tuple itself, since they are per-token, not
// per-build). Binding the whole tuple — not just a bare fingerprint — is
// what lets RegisterAdapterBuild's re-measurement be compared against
// precisely what an operator confirmed, closing the probe/register TOCTOU
// gap.
type CandidateTuple struct {
	ProviderKey            string `json:"providerKey"`
	ExecutablePath         string `json:"executablePath"`
	ExecutableContentHash  string `json:"executableContentHash"`
	ProtocolVersion        string `json:"protocolVersion"`
	CapabilityManifestHash string `json:"capabilityManifestHash"`
	OS                     string `json:"os"`
	Toolchain              string `json:"toolchain"`
	ConfigIdentity         string `json:"configIdentity"`
}

// validate checks every CandidateTuple field is present — an empty field
// would let a token bind to "anything" for that axis, defeating the
// point of binding the whole tuple in the first place.
func (t CandidateTuple) validate() error {
	fields := map[string]string{
		"providerKey":            t.ProviderKey,
		"executablePath":         t.ExecutablePath,
		"executableContentHash":  t.ExecutableContentHash,
		"protocolVersion":        t.ProtocolVersion,
		"capabilityManifestHash": t.CapabilityManifestHash,
		"os":                     t.OS,
		"toolchain":              t.Toolchain,
		"configIdentity":         t.ConfigIdentity,
	}
	for name, value := range fields {
		if strings.TrimSpace(value) == "" {
			return errors.New("adapterbuild: candidate tuple field " + name + " is required")
		}
	}
	return nil
}

// ID returns the content-addressed identity of a Build carrying this
// tuple: a canonical hash of the tuple itself, so two builds are the same
// build if and only if every axis of the tuple measured identically.
func (t CandidateTuple) ID() (string, error) {
	if err := t.validate(); err != nil {
		return "", err
	}
	_, hash, err := authoring.Canonicalize(t, authoring.CanonicalizeOptions{})
	if err != nil {
		return "", err
	}
	return hash, nil
}

// CapabilityManifest is what a candidate build declares it supports —
// the same shape ports.AgentCapabilities already establishes at the
// runtime layer (internal/app/ports/agent.go), minus the fields
// (Provider, AdapterVersion, ProtocolVersion, TestedCLIVersion) this
// package already tracks as their own CandidateTuple fields, so nothing
// is represented twice.
type CapabilityManifest struct {
	SupportsStart       bool     `json:"supportsStart" yaml:"supportsStart"`
	SupportsResume      bool     `json:"supportsResume" yaml:"supportsResume"`
	SupportsCancel      bool     `json:"supportsCancel" yaml:"supportsCancel"`
	CanonicalEventKinds []string `json:"canonicalEventKinds,omitempty" yaml:"canonicalEventKinds,omitempty"`
}

var capabilityManifestSetPaths = map[string]bool{"canonicalEventKinds": true}

// ErrInvalidCapabilityManifest is returned by ValidateCapabilityManifest
// for a manifest that could never be admitted — mirrors
// internal/app/agentregistry's own validateCapabilities rules exactly
// (a build that cannot even start is useless; a duplicate or empty event
// kind can never be told apart from another).
var ErrInvalidCapabilityManifest = errors.New("adapterbuild: invalid capability manifest")

// ValidateCapabilityManifest rejects a manifest with SupportsStart false,
// or any empty/duplicate CanonicalEventKinds entry.
func ValidateCapabilityManifest(m CapabilityManifest) error {
	if !m.SupportsStart {
		return ErrInvalidCapabilityManifest
	}
	seen := make(map[string]bool, len(m.CanonicalEventKinds))
	for _, kind := range m.CanonicalEventKinds {
		if kind == "" || seen[kind] {
			return ErrInvalidCapabilityManifest
		}
		seen[kind] = true
	}
	return nil
}

// HashCapabilityManifest canonicalizes m and returns its canonical JSON
// alongside its hash — the value a CandidateTuple.CapabilityManifestHash
// binds, and the value RegisterAdapterBuild re-derives from a
// freshly-supplied manifest to compare against a token's bound hash.
func HashCapabilityManifest(m CapabilityManifest) (canonicalJSON string, hash string, err error) {
	raw, hash, err := authoring.Canonicalize(m, authoring.CanonicalizeOptions{SetPaths: capabilityManifestSetPaths})
	if err != nil {
		return "", "", err
	}
	return string(raw), hash, nil
}

// Build is one immutable, registered AdapterBuildVersion. It has no
// exported mutation method — once constructed, every field is fixed
// forever, the same immutability convention
// internal/domain/definition.VersionFields already established.
type Build struct {
	id                 string
	tuple              CandidateTuple
	capabilityManifest CapabilityManifest
	registeredBy       string
	registeredAt       time.Time
}

// NewBuildRequest is what a caller supplies to NewBuild.
type NewBuildRequest struct {
	Tuple              CandidateTuple
	CapabilityManifest CapabilityManifest
	RegisteredBy       string
	RegisteredAt       time.Time
}

// NewBuild validates and constructs an immutable Build. It is the one
// path both a fresh registration and a load-from-storage reconstruction
// go through (mirroring definition.NewVersionFields's own dual use),
// so a row loaded back out of the registry can never silently carry data
// that would have been rejected had it been submitted fresh.
func NewBuild(req NewBuildRequest) (Build, error) {
	id, err := req.Tuple.ID()
	if err != nil {
		return Build{}, err
	}
	if err := ValidateCapabilityManifest(req.CapabilityManifest); err != nil {
		return Build{}, err
	}
	if strings.TrimSpace(req.RegisteredBy) == "" {
		return Build{}, errors.New("adapterbuild: registeredBy is required")
	}
	if req.RegisteredAt.IsZero() {
		return Build{}, errors.New("adapterbuild: registeredAt is required")
	}
	return Build{
		id:                 id,
		tuple:              req.Tuple,
		capabilityManifest: req.CapabilityManifest,
		registeredBy:       req.RegisteredBy,
		registeredAt:       req.RegisteredAt,
	}, nil
}

func (b Build) ID() string                             { return b.id }
func (b Build) Tuple() CandidateTuple                  { return b.tuple }
func (b Build) CapabilityManifest() CapabilityManifest { return b.capabilityManifest }
func (b Build) RegisteredBy() string                   { return b.registeredBy }
func (b Build) RegisteredAt() time.Time                { return b.registeredAt }
