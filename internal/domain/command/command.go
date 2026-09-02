// Package command is the Command DefinitionKind's contract
// (docs/design/04-v2-definition-plane.md V2-04, AK-ARCH-017): "Resource
// chứa script không chạy được nếu thiếu published executable definition
// và policy grant." A Command is exactly that published executable
// definition — the one thing this repo's design actually grants
// execution authority to alongside Gate, Block and Agent Profile
// (docs/design/01-system-design.md's DefinitionKind table).
//
// A Command never accepts a free-form shell string
// (docs/design/01-system-design.md: "Command/Gate definition không nhận
// free-form shell. Template chỉ thay placeholder đã khai báo thành argv
// element riêng."): its Argv is a literal-or-declared-placeholder list,
// spawned argv-only, never re-parsed through a shell — the same
// argument-injection concern docs/design/07-v5-execution-evidence.md's
// ProcessSupervisor (V5-05) exists to close at the runtime layer. This
// package closes it at the authoring layer: an unknown placeholder name
// is a publish-time rejection, never a runtime surprise.
//
// Like internal/domain/block, Command plugs directly into the generic
// definition.VersionFields contract V2-02 already built — there is no
// separate CommandVersion wrapper type here.
package command

import (
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// CommandDefinitionID identifies a Command's mutable Definition row.
type CommandDefinitionID string

// CommandVersionID identifies one immutable, published CommandVersion.
type CommandVersionID string

// ArgvElementKind is whether one Argv slot is literal text or a
// reference to a declared placeholder name.
type ArgvElementKind string

const (
	ArgvLiteral     ArgvElementKind = "LITERAL"
	ArgvPlaceholder ArgvElementKind = "PLACEHOLDER"
)

// ArgvElement is exactly one argv slot: either literal text, or the name
// of a declared placeholder the runtime substitutes into this one slot
// and no other — never interpolated into a larger string
// ("placeholder đã khai báo thành argv element riêng", "declared
// placeholder becomes its own separate argv element").
type ArgvElement struct {
	Kind  ArgvElementKind `json:"kind" yaml:"kind"`
	Value string          `json:"value" yaml:"value"`
}

// ExecutableRef pins the exact script this Command spawns by its
// resource identity (ADR-012: a resource living inside a Skill/Layer
// version has identity owner_version_id + resource_key + content_hash;
// there is no top-level ResourceDefinition kind to pin via
// definition.DependencyPin instead). ContentHash is what makes this a
// "pinned" executable, per AK-ARCH-017/§10.2: a script stays inert data
// until something references its exact hash.
type ExecutableRef struct {
	OwnerVersionID string `json:"ownerVersionId" yaml:"ownerVersionId"`
	ResourceKey    string `json:"resourceKey" yaml:"resourceKey"`
	ContentHash    string `json:"contentHash" yaml:"contentHash"`
}

// Compatibility is the OS/toolchain support this Command declares,
// reusing the same compatibility-declaration vocabulary
// docs/architecture/04-go-core-spec.md already requires of Layer ("Layer
// phải khai OS/toolchain compatibility").
type Compatibility struct {
	OS        []string `json:"os" yaml:"os"`
	Toolchain []string `json:"toolchain,omitempty" yaml:"toolchain,omitempty"`
}

// NetworkAccess is whether a Command's execution may reach the network
// at all — the coarse declared permission docs/architecture/03-system-
// architecture.md §10.2 requires ("network... permission") as a field of
// every executable definition.
type NetworkAccess string

const (
	NetworkAccessNone    NetworkAccess = "NONE"
	NetworkAccessAllowed NetworkAccess = "ALLOWED"
)

// OutputContract is what a Command's execution captures.
type OutputContract struct {
	CaptureStdout  bool   `json:"captureStdout" yaml:"captureStdout"`
	CaptureStderr  bool   `json:"captureStderr" yaml:"captureStderr"`
	MaxOutputBytes uint64 `json:"maxOutputBytes" yaml:"maxOutputBytes"`
}

// CommandDocument is a Command's complete authored content — everything
// docs/architecture/03-system-architecture.md §10.2 requires of an
// Executable Definition, scoped to what V2-05's own Thực hiện line
// lists: "argv placeholder allowlist, cwd target, OS/toolchain,
// env/network/secret permission, timeout/output contract."
type CommandDocument struct {
	// Executable pins the exact script this Command spawns.
	Executable ExecutableRef `json:"executable" yaml:"executable"`
	// Argv is the complete, ordered argument vector. Order is
	// significant — unlike this package's set-like fields, two Commands
	// with the same argv elements in a different order are genuinely
	// different commands.
	Argv []ArgvElement `json:"argv" yaml:"argv"`
	// PlaceholderAllowlist is the closed set of placeholder names Argv
	// may reference. Any ArgvElement of kind PLACEHOLDER whose Value is
	// not in this set is rejected — this is what makes "unknown
	// placeholder" a publish-time error rather than a runtime surprise.
	PlaceholderAllowlist []string `json:"placeholderAllowlist,omitempty" yaml:"placeholderAllowlist,omitempty"`
	// CwdRepositoryTarget names which repository (within the run's
	// scope) this Command's working directory resolves against —
	// "Cwd phải resolve từ WorkspaceHandle + repository target": the
	// actual WorkspaceHandle resolution is the runtime's job, this only
	// declares which target it resolves against.
	CwdRepositoryTarget string        `json:"cwdRepositoryTarget" yaml:"cwdRepositoryTarget"`
	Compatibility       Compatibility `json:"compatibility" yaml:"compatibility"`
	// EnvAllowlist is the closed set of environment variable names this
	// Command's execution may see — "Environment là allowlist".
	EnvAllowlist []string `json:"envAllowlist,omitempty" yaml:"envAllowlist,omitempty"`
	// NetworkAccess is this Command's declared network permission.
	NetworkAccess NetworkAccess `json:"networkAccess" yaml:"networkAccess"`
	// SecretRefs names which secrets this Command's execution may
	// resolve — names only, never values: "secret chỉ resolve ở worker
	// ngay trước spawn và không persist".
	SecretRefs     []string       `json:"secretRefs,omitempty" yaml:"secretRefs,omitempty"`
	TimeoutSeconds uint32         `json:"timeoutSeconds" yaml:"timeoutSeconds"`
	Output         OutputContract `json:"output" yaml:"output"`
	// PolicyRefs pins the permission/attempt/completion policies this
	// Command's execution is authorized under. Every pin's Kind must be
	// definition.KindPolicy.
	PolicyRefs []definition.DependencyPin `json:"policyRefs,omitempty" yaml:"policyRefs,omitempty"`
}
