package adapterbuild

import (
	"time"

	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// capabilityManifestBody is the wire shape of a CapabilityManifest — reused
// by both probeAdapterBuildBody (a fresh declaration) and
// registerAdapterBuildBody (the caller's own re-declaration, re-hashed and
// compared against the token's bound hash exactly like
// RegisterAdapterBuild's own doc comment describes). Field-for-field
// identical to domainadapterbuild.CapabilityManifest; declared separately
// (rather than reusing that type on the wire directly) purely to match
// this package's own — and every sibling endpoint package's own — "every
// route defines its own wire DTO" convention (see
// internal/delivery/httpapi/workitem/dto.go's own top-of-file doc comment
// for why), even though today the two shapes carry identical json tags.
type capabilityManifestBody struct {
	SupportsStart       bool     `json:"supportsStart"`
	SupportsResume      bool     `json:"supportsResume"`
	SupportsCancel      bool     `json:"supportsCancel"`
	CanonicalEventKinds []string `json:"canonicalEventKinds,omitempty"`
}

func (b capabilityManifestBody) toManifest() domainadapterbuild.CapabilityManifest {
	return domainadapterbuild.CapabilityManifest{
		SupportsStart: b.SupportsStart, SupportsResume: b.SupportsResume, SupportsCancel: b.SupportsCancel,
		CanonicalEventKinds: b.CanonicalEventKinds,
	}
}

func capabilityManifestBodyFrom(m domainadapterbuild.CapabilityManifest) capabilityManifestBody {
	return capabilityManifestBody{
		SupportsStart: m.SupportsStart, SupportsResume: m.SupportsResume, SupportsCancel: m.SupportsCancel,
		CanonicalEventKinds: m.CanonicalEventKinds,
	}
}

// probeAdapterBuildBody is POST /adapter-builds/probe's own wire request
// shape — every field internal/app/adapterbuild.ProbeRequest itself
// declares (commands.go, line ~92), named identically, and nothing else:
// this package never adds a field ProbeRequest does not have, and never
// omits one it does (this task's own "Verify: token/fingerprint/protocol/
// config/scope schemas" line). No file path is ever read by THIS package —
// ExecutablePath here is forwarded verbatim to
// appadapterbuild.ProbeAdapterBuild, which is the one and only place a real
// filesystem hash of it ever happens.
type probeAdapterBuildBody struct {
	ProviderKey        string                 `json:"providerKey"`
	ExecutablePath     string                 `json:"executablePath"`
	ProtocolVersion    string                 `json:"protocolVersion"`
	CapabilityManifest capabilityManifestBody `json:"capabilityManifest"`
	OS                 string                 `json:"os"`
	Toolchain          string                 `json:"toolchain"`
	ConfigIdentity     string                 `json:"configIdentity"`
}

// registerAdapterBuildBody is POST /adapter-builds' own wire request shape
// — mirrors internal/app/adapterbuild.RegisterRequest exactly (Token,
// CapabilityManifest): no RegisteredBy field on the wire at all, the same
// V6-10I choice this package inherits verbatim (RegisterAdapterBuild
// derives RegisteredBy from cmd.Actor, which this package's own
// prepareCommand always sources from the bound LocalPrincipalSnapshot,
// never from a request body/header field). Token is
// domainadapterbuild.CandidateToken itself (not a further-wrapped DTO): a
// caller must echo back, byte-for-byte, the exact token
// POST /adapter-builds/probe handed it — CandidateToken's own json tags are
// already its complete, stable wire shape (see that type's own doc
// comment), so re-declaring an identical struct here would only invite the
// two to drift.
type registerAdapterBuildBody struct {
	Token              domainadapterbuild.CandidateToken `json:"token"`
	CapabilityManifest capabilityManifestBody            `json:"capabilityManifest"`
}

// adapterBuildView is this package's own stable, exported-field JSON view
// of a domainadapterbuild.Build — mirrors cmd/aw/adapter.go's own
// adapterBuildView exactly (same fields, same round-trip-through-accessors
// technique), since Build itself has no exported field at all (its own doc
// comment: immutability is structural, accessor-only) and
// encoding/json.Marshal on it directly would silently print "{}".
type adapterBuildView struct {
	ID                     string                 `json:"id"`
	ProviderKey            string                 `json:"providerKey"`
	ExecutablePath         string                 `json:"executablePath"`
	ExecutableContentHash  string                 `json:"executableContentHash"`
	ProtocolVersion        string                 `json:"protocolVersion"`
	CapabilityManifestHash string                 `json:"capabilityManifestHash"`
	OS                     string                 `json:"os"`
	Toolchain              string                 `json:"toolchain"`
	ConfigIdentity         string                 `json:"configIdentity"`
	CapabilityManifest     capabilityManifestBody `json:"capabilityManifest"`
	RegisteredBy           string                 `json:"registeredBy"`
	// RegisteredAt is a plain time.Time — encoding/json's own default
	// RFC3339Nano encoding — the exact same choice cmd/aw/adapter.go's own
	// adapterBuildView already makes for this identical field on the CLI's
	// JSON output, kept identical here rather than inventing a second wire
	// timestamp convention for the same underlying value.
	RegisteredAt time.Time `json:"registeredAt"`
}

func newAdapterBuildView(build domainadapterbuild.Build) adapterBuildView {
	tuple := build.Tuple()
	return adapterBuildView{
		ID: build.ID(), ProviderKey: tuple.ProviderKey, ExecutablePath: tuple.ExecutablePath,
		ExecutableContentHash: tuple.ExecutableContentHash, ProtocolVersion: tuple.ProtocolVersion,
		CapabilityManifestHash: tuple.CapabilityManifestHash, OS: tuple.OS, Toolchain: tuple.Toolchain,
		ConfigIdentity: tuple.ConfigIdentity, CapabilityManifest: capabilityManifestBodyFrom(build.CapabilityManifest()),
		RegisteredBy: build.RegisteredBy(), RegisteredAt: build.RegisteredAt(),
	}
}

// adapterBuildListResponse wraps a []adapterBuildView collection in an
// object — the same "future field could be added beside items without a
// breaking wire-shape change" reasoning workitem's own workItemListResponse
// doc comment documents at length. No cursor pagination: ListAdapterBuilds
// itself is a plain, unbounded query (internal/app/adapterbuild/commands.go),
// the same shape ListProjects/ListWorkItems already have on the wire.
type adapterBuildListResponse struct {
	Builds []adapterBuildView `json:"builds"`
}

// registerAdapterBuildResponse is POST /adapter-builds' own wire response
// shape — a camelCase-JSON projection of
// internal/app/adapterbuild.RegisterResult.
type registerAdapterBuildResponse struct {
	Build          adapterBuildView `json:"build"`
	AlreadyExisted bool             `json:"alreadyExisted"`
}
