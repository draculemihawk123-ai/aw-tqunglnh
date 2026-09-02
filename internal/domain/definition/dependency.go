package definition

// DependencyPin is one exact reference a published Version pins: another
// DefinitionKind's exact DefinitionID+VersionID (ADR-012's "publish
// dedicate theo DefinitionID + CompiledSnapshotHash" applies at the
// referenced version too — a pin names the version by ID, never a range,
// so re-resolving it later can never silently pick up a different
// published version). It deliberately does not carry the referenced
// definition's ProjectID: trusting a value the publisher supplied would
// make cross-project validation spoofable — a real cross-project check
// resolves the pin's actual project through the repository at publish
// time instead (see ports.ErrCrossProjectDependency).
type DependencyPin struct {
	Kind         Kind   `json:"kind" yaml:"kind"`
	DefinitionID string `json:"definitionId" yaml:"definitionId"`
	VersionID    string `json:"versionId" yaml:"versionId"`
}

// DependencyManifest is the complete, ordered set of pins a Version
// publishes with. It is stored as canonical JSON in
// definition_versions.dependency_manifest, mirroring the same shape
// internal/domain/workflow.DependencyManifest already uses for
// WorkflowVersion — the two are independent types (WORKFLOW keeps its
// own dedicated tables, V2-02's scoping decision), but sharing the same
// field shape keeps a future consumer that reads both from a shared
// projection simple.
type DependencyManifest struct {
	Pins []DependencyPin `json:"pins,omitempty"`
}

// Clone returns a deep copy of m so a caller can never mutate a
// DependencyManifest another value still references.
func (m DependencyManifest) Clone() DependencyManifest {
	return DependencyManifest{Pins: append([]DependencyPin(nil), m.Pins...)}
}
