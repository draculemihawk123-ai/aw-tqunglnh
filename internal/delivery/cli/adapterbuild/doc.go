// Package adapterbuild is V6-15F's own CLI leaf over V2-07A's immutable
// AdapterBuildVersion registry (docs/design/08-v6-api-projections.md
// V6-15F: "operate the immutable AdapterBuild registry from the terminal
// via hardened commands"): `aw adapter list|show|probe|register` — the
// terminal-side mirror of GET/POST /adapter-builds and
// GET /adapter-builds/{id}/POST /adapter-builds/probe
// (internal/delivery/httpapi/adapterbuild, V6-10J, merged), built on the
// exact same already-hardened internal/app/adapterbuild application
// commands that package wraps (ProbeAdapterBuild/RegisterAdapterBuild/
// ListAdapterBuilds/GetAdapterBuild, V6-10I) — never a second,
// independently invented registry concept.
//
// "Không làm: no legacy no-envelope call" (this task's own scope line):
// this package never calls, imports, or in any way reuses
// cmd/aw/adapter.go's own runAdapterProbe/runAdapterRegister — that file's
// own commands hand-build a ports.Command via newDefinitionCommand/
// requestHash (a pre-CommandEnvelope-framework hashing scheme, V2-07B era)
// and, for the capability manifest, spawn a REAL provider executor
// (newAgentExecutor/AgentExecutor.Capabilities) to measure it, never
// accepting one as a caller-supplied field. This package instead builds
// every command exclusively through internal/delivery/cli's own
// cli.BuildEnvelope/cli.Dispatch (the exact same SemanticHash/
// LookupReceipt/ReconcileReceipt replay authority
// internal/delivery/httpapi/adapterbuild, V6-10J, already dispatches
// through — a receipt written by an HTTP call and one written by a CLI
// call for an equivalent request are checked against the one shared replay
// authority), and — mirroring V6-10J's own documented choice, not the
// legacy CLI's — accepts ProviderKey/ExecutablePath/ProtocolVersion/
// CapabilityManifest/OS/Toolchain/ConfigIdentity as caller-supplied flags:
// this package never spawns a real provider CLI process and never reads
// any file off the local filesystem other than a request body (--file/
// stdin), the identical restriction that package's own top-of-file doc
// comment documents for the identical reason.
//
// "Không làm: no ProjectID" (AdapterBuildVersion is installation-scoped,
// not project-scoped, ADR-022/ADR-025's own closed list): every command in
// this package registers with cli.ScopeInstallation and never calls
// cli.BindProjectFlag anywhere — the same "installation-scoped, no project
// flag" pattern internal/delivery/cli/health, internal/delivery/cli/
// settings and internal/delivery/cli/doctor already established.
//
// Redaction: neither domainadapterbuild.Build nor domainadapterbuild.
// CandidateToken carries a credential-shaped field at all (no API key, no
// password, no provider auth token) — ExecutablePath and ConfigIdentity are
// operator-assigned identifiers, not secrets, and Signature is an HMAC
// digest that reveals nothing about the per-installation signing key that
// produced it. The one genuinely sensitive value in this whole flow — the
// per-installation candidate-token signing key
// (ports.AdapterBuildRepository.LoadOrCreateSigningKey/LoadSigningKey) —
// is never returned by ProbeAdapterBuild, RegisterAdapterBuild,
// ListAdapterBuilds or GetAdapterBuild in the first place, so there is
// nothing here for this package to mask; redaction_test.go proves this
// mechanically rather than merely asserting it in prose.
//
// This package never wires itself into cmd/aw (V6-15O's own job — "chỉ
// V6-15O compose CLI/parity registry", docs/design/08-v6-api-projections.md
// §1 rule 8): it only registers its own cli.Descriptor(s) into cli.Default
// from its own init(), and exposes RunList/RunShow/RunProbe/RunRegister for
// a future composition root to call once that wiring exists.
package adapterbuild
