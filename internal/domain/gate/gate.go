// Package gate is the Gate DefinitionKind's contract
// (docs/design/04-v2-definition-plane.md V2-05): a Gate never carries
// its own executable content — it references a published Command
// (docs/design/01-system-design.md's DefinitionKind table: Gate
// publishes "command/evaluator ref + verdict/evidence mapping") and adds
// the criteria mapping that turns that Command's raw result into an
// authoritative verdict with provenance
// (docs/design/07-v5-execution-evidence.md V5-10, "MACHINE_GATE tạo
// verdict authoritative với provenance").
//
// Like internal/domain/block and internal/domain/command, Gate plugs
// directly into the generic definition.VersionFields contract V2-02
// already built — there is no separate GateVersion wrapper type here.
package gate

import (
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// GateDefinitionID identifies a Gate's mutable Definition row.
type GateDefinitionID string

// GateVersionID identifies one immutable, published GateVersion.
type GateVersionID string

// Verdict is the closed set of outcomes a Gate's evaluation may resolve
// to (docs/design/07-v5-execution-evidence.md V5-10's own enum — the
// runtime, not this package, is what actually produces one of these for
// a real execution; this package only ever declares which Criterion maps
// to which evidence, never evaluates anything itself).
type Verdict string

const (
	VerdictPass          Verdict = "PASS"
	VerdictFail          Verdict = "FAIL"
	VerdictError         Verdict = "ERROR"
	VerdictNotRun        Verdict = "NOT_RUN"
	VerdictNotApplicable Verdict = "NOT_APPLICABLE"
)

// Criterion is one thing a Gate's evaluation checks, and which evidence
// key its verdict is recorded against. Evaluating a Criterion against a
// real Command execution result — and therefore actually producing a
// Verdict — is the runtime's job (V5-10), never this authoring-time
// schema's.
type Criterion struct {
	Name        string `json:"name" yaml:"name"`
	EvidenceKey string `json:"evidenceKey" yaml:"evidenceKey"`
}

// GateDocument is a Gate's complete authored content: which Command it
// evaluates, and the criteria mapping V2-05's own Thực hiện line
// requires ("Gate thêm verdict/evidence mapping").
type GateDocument struct {
	// CommandRef pins the exact Command this Gate evaluates. Its Kind
	// must be definition.KindCommand — a Gate never evaluates anything
	// else directly (see ValidateDocument).
	CommandRef definition.DependencyPin `json:"commandRef" yaml:"commandRef"`
	// Criteria is the complete set of checks this Gate's evaluation
	// performs. At least one is required — a Gate with no criteria has
	// no evidence mapping at all, exactly the "missing evidence mapping"
	// case V2-05's own Verify line requires rejecting.
	Criteria []Criterion `json:"criteria" yaml:"criteria"`
	// PolicyRefs pins the permission/completion policies this Gate's
	// evaluation is authorized under. Every pin's Kind must be
	// definition.KindPolicy.
	PolicyRefs []definition.DependencyPin `json:"policyRefs,omitempty" yaml:"policyRefs,omitempty"`
}
