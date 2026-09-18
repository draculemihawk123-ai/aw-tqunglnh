package adapterbuild

import (
	"fmt"
	"io"
	"time"

	appadapterbuild "github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// capabilityManifestView mirrors domainadapterbuild.CapabilityManifest's own
// JSON shape exactly, and internal/delivery/httpapi/adapterbuild/dto.go's
// own capabilityManifestBody field-for-field — declared separately purely
// to match this package's own, and every sibling leaf's own, "every
// route/leaf defines its own wire DTO" convention (see that file's own
// top-of-file doc comment for why), even though today the two shapes carry
// identical json tags.
type capabilityManifestView struct {
	SupportsStart       bool     `json:"supportsStart"`
	SupportsResume      bool     `json:"supportsResume"`
	SupportsCancel      bool     `json:"supportsCancel"`
	CanonicalEventKinds []string `json:"canonicalEventKinds,omitempty"`
}

func capabilityManifestViewFrom(m domainadapterbuild.CapabilityManifest) capabilityManifestView {
	return capabilityManifestView{
		SupportsStart: m.SupportsStart, SupportsResume: m.SupportsResume, SupportsCancel: m.SupportsCancel,
		CanonicalEventKinds: m.CanonicalEventKinds,
	}
}

// buildView is this package's own stable, exported-field JSON view of a
// domainadapterbuild.Build — mirrors internal/delivery/httpapi/adapterbuild/
// dto.go's own adapterBuildView and cmd/aw/adapter.go's own adapterBuildView
// exactly (same fields, same round-trip-through-accessors technique), since
// Build itself has no exported field at all (its own doc comment:
// immutability is structural, accessor-only) and encoding/json.Marshal on
// it directly would silently print "{}".
type buildView struct {
	ID                     string                 `json:"id"`
	ProviderKey            string                 `json:"providerKey"`
	ExecutablePath         string                 `json:"executablePath"`
	ExecutableContentHash  string                 `json:"executableContentHash"`
	ProtocolVersion        string                 `json:"protocolVersion"`
	CapabilityManifestHash string                 `json:"capabilityManifestHash"`
	OS                     string                 `json:"os"`
	Toolchain              string                 `json:"toolchain"`
	ConfigIdentity         string                 `json:"configIdentity"`
	CapabilityManifest     capabilityManifestView `json:"capabilityManifest"`
	RegisteredBy           string                 `json:"registeredBy"`
	RegisteredAt           time.Time              `json:"registeredAt"`
}

func newBuildView(build domainadapterbuild.Build) buildView {
	tuple := build.Tuple()
	return buildView{
		ID: build.ID(), ProviderKey: tuple.ProviderKey, ExecutablePath: tuple.ExecutablePath,
		ExecutableContentHash: tuple.ExecutableContentHash, ProtocolVersion: tuple.ProtocolVersion,
		CapabilityManifestHash: tuple.CapabilityManifestHash, OS: tuple.OS, Toolchain: tuple.Toolchain,
		ConfigIdentity: tuple.ConfigIdentity, CapabilityManifest: capabilityManifestViewFrom(build.CapabilityManifest()),
		RegisteredBy: build.RegisteredBy(), RegisteredAt: build.RegisteredAt(),
	}
}

// buildListView wraps a []buildView collection in an object — the same
// "future field could be added beside items without a breaking wire-shape
// change" reasoning internal/delivery/httpapi/adapterbuild/dto.go's own
// adapterBuildListResponse doc comment documents.
type buildListView struct {
	Builds []buildView `json:"builds"`
}

// registerResultView is `aw adapter register`'s own wire response shape — a
// camelCase-JSON projection of appadapterbuild.RegisterResult, mirroring
// internal/delivery/httpapi/adapterbuild/dto.go's own
// registerAdapterBuildResponse exactly.
type registerResultView struct {
	Build          buildView `json:"build"`
	AlreadyExisted bool      `json:"alreadyExisted"`
}

func newRegisterResultView(result appadapterbuild.RegisterResult) registerResultView {
	return registerResultView{Build: newBuildView(result.Build), AlreadyExisted: result.AlreadyExisted}
}

// registerReplayPayload mirrors internal/app/adapterbuild/commands.go's own
// unexported registerReceiptPayload field-for-field: the exact wire shape
// RegisterAdapterBuild's own transaction writes into a command receipt's
// ResultJSON (that package's own doc comment: Build has no exported field,
// so its receipt round-trips through Tuple/CapabilityManifest/RegisteredBy/
// RegisteredAt + adapterbuild.NewBuild instead of Build directly). A REPLAYED
// cli.Dispatch result for RegisterAdapterBuild is exactly this shape (never
// appadapterbuild.RegisterResult itself), so RunRegister decodes into this
// type and reconstructs a Build via domainadapterbuild.NewBuild — the same
// technique that internal package's own toResult() method uses, restated
// here since that method is unexported and this package may not reach into
// it directly.
type registerReplayPayload struct {
	Tuple              domainadapterbuild.CandidateTuple     `json:"tuple"`
	CapabilityManifest domainadapterbuild.CapabilityManifest `json:"capabilityManifest"`
	RegisteredBy       string                                `json:"registeredBy"`
	RegisteredAt       time.Time                             `json:"registeredAt"`
	AlreadyExisted     bool                                  `json:"alreadyExisted"`
}

func (p registerReplayPayload) toResult() (appadapterbuild.RegisterResult, error) {
	build, err := domainadapterbuild.NewBuild(domainadapterbuild.NewBuildRequest{
		Tuple: p.Tuple, CapabilityManifest: p.CapabilityManifest,
		RegisteredBy: p.RegisteredBy, RegisteredAt: p.RegisteredAt,
	})
	if err != nil {
		return appadapterbuild.RegisterResult{}, err
	}
	return appadapterbuild.RegisterResult{Build: build, AlreadyExisted: p.AlreadyExisted}, nil
}

// probeRequestWire is `aw adapter probe`'s own canonical NormalizedPayload
// shape (cli.EnvelopeRequest.NormalizedPayload's own doc comment: "decodes
// it into a typed request struct first and re-marshals that struct here")
// — mirrors internal/delivery/httpapi/adapterbuild/dto.go's own
// probeAdapterBuildBody field-for-field: every field
// appadapterbuild.ProbeRequest itself declares, named identically, nothing
// else.
type probeRequestWire struct {
	ProviderKey        string                 `json:"providerKey"`
	ExecutablePath     string                 `json:"executablePath"`
	ProtocolVersion    string                 `json:"protocolVersion"`
	CapabilityManifest capabilityManifestView `json:"capabilityManifest"`
	OS                 string                 `json:"os"`
	Toolchain          string                 `json:"toolchain"`
	ConfigIdentity     string                 `json:"configIdentity"`
}

// registerRequestWire is `aw adapter register`'s own canonical
// NormalizedPayload shape — mirrors internal/delivery/httpapi/adapterbuild/
// dto.go's own registerAdapterBuildBody exactly: Token is
// domainadapterbuild.CandidateToken itself (not a further-wrapped DTO), the
// same "a caller must echo back, byte-for-byte, the exact token probe
// handed it" reasoning that file's own doc comment gives.
type registerRequestWire struct {
	Token              domainadapterbuild.CandidateToken `json:"token"`
	CapabilityManifest capabilityManifestView            `json:"capabilityManifest"`
}

// -- human-readable output (the "human" half of V6-15B's own JSON/human
// dual-output contract every leaf command follows; --json branches to
// cli.EncodeQueryResult/cli.EncodeCommandResult instead) --

func writeHumanBuild(stdout io.Writer, v buildView) {
	fmt.Fprintf(stdout, "id: %s\n", v.ID)
	fmt.Fprintf(stdout, "providerKey: %s\n", v.ProviderKey)
	fmt.Fprintf(stdout, "executablePath: %s\n", v.ExecutablePath)
	fmt.Fprintf(stdout, "executableContentHash: %s\n", v.ExecutableContentHash)
	fmt.Fprintf(stdout, "protocolVersion: %s\n", v.ProtocolVersion)
	fmt.Fprintf(stdout, "capabilityManifestHash: %s\n", v.CapabilityManifestHash)
	fmt.Fprintf(stdout, "os: %s\n", v.OS)
	fmt.Fprintf(stdout, "toolchain: %s\n", v.Toolchain)
	fmt.Fprintf(stdout, "configIdentity: %s\n", v.ConfigIdentity)
	fmt.Fprintf(stdout, "capabilityManifest: supportsStart=%t supportsResume=%t supportsCancel=%t canonicalEventKinds=%v\n",
		v.CapabilityManifest.SupportsStart, v.CapabilityManifest.SupportsResume, v.CapabilityManifest.SupportsCancel, v.CapabilityManifest.CanonicalEventKinds)
	fmt.Fprintf(stdout, "registeredBy: %s\n", v.RegisteredBy)
	fmt.Fprintf(stdout, "registeredAt: %s\n", v.RegisteredAt.Format(time.RFC3339))
}

func writeHumanBuildList(stdout io.Writer, list buildListView) {
	fmt.Fprintf(stdout, "builds: %d\n", len(list.Builds))
	for _, b := range list.Builds {
		fmt.Fprintf(stdout, "  - %s providerKey=%s registeredBy=%s registeredAt=%s\n", b.ID, b.ProviderKey, b.RegisteredBy, b.RegisteredAt.Format(time.RFC3339))
	}
}

func writeHumanToken(stdout io.Writer, token domainadapterbuild.CandidateToken, replayed bool, idempotencyKey string) {
	fmt.Fprintf(stdout, "idempotencyKey: %s\n", idempotencyKey)
	fmt.Fprintf(stdout, "replayed: %t\n", replayed)
	fmt.Fprintf(stdout, "providerKey: %s\n", token.Tuple.ProviderKey)
	fmt.Fprintf(stdout, "executablePath: %s\n", token.Tuple.ExecutablePath)
	fmt.Fprintf(stdout, "executableContentHash: %s\n", token.Tuple.ExecutableContentHash)
	fmt.Fprintf(stdout, "protocolVersion: %s\n", token.Tuple.ProtocolVersion)
	fmt.Fprintf(stdout, "capabilityManifestHash: %s\n", token.Tuple.CapabilityManifestHash)
	fmt.Fprintf(stdout, "os: %s\n", token.Tuple.OS)
	fmt.Fprintf(stdout, "toolchain: %s\n", token.Tuple.Toolchain)
	fmt.Fprintf(stdout, "configIdentity: %s\n", token.Tuple.ConfigIdentity)
	fmt.Fprintf(stdout, "nonce: %s\n", token.Nonce)
	fmt.Fprintf(stdout, "expiresAt: %s\n", token.ExpiresAt.Format(time.RFC3339))
	fmt.Fprintln(stdout, "-- save this candidate token as JSON (aw adapter probe --json, take .result) and pass it to 'aw adapter register --file <path>' before it expires --")
}

func writeHumanRegisterResult(stdout io.Writer, v registerResultView, replayed bool, idempotencyKey string) {
	fmt.Fprintf(stdout, "idempotencyKey: %s\n", idempotencyKey)
	fmt.Fprintf(stdout, "replayed: %t\n", replayed)
	fmt.Fprintf(stdout, "alreadyExisted: %t\n", v.AlreadyExisted)
	writeHumanBuild(stdout, v.Build)
}
