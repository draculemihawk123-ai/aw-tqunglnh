// This file is this package's own per-Kind compile dispatch — mirroring
// cmd/aw/definition.go's own compileClosureForKind (V2-11) with one real
// improvement: `fields` here is the REAL, authoritatively reloaded
// internal/domain/definition.Fields for this DefinitionID (its current
// Status/Scope/Name via internal/app/definitions.GetDefinition — V6-05's
// own new query), never cmd/aw's own synthetic StatusDraft placeholder
// (see that file's syntheticDraftFields doc comment for exactly why it had
// no better option at the time). Every one of the eight shared
// (non-Workflow) kinds follows the exact same shape —
// <Kind>Definition{ID, Fields}, PublishRequest{VersionID, VersionNumber,
// SchemaVersion, Document, Dependencies, PublishedBy, PublishedAt},
// CompileFrom(def, rawDocument, format, req) — so, like cmd/aw's own copy,
// this is deliberately nine near-identical cases rather than a clever
// generic dispatch: each kind's own concrete ID/Version types
// (BlockDefinitionID vs CommandDefinitionID, ...) are exactly what keep a
// document authored for one kind from ever being silently accepted as
// another's.
package definitions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/block"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/engineeringpack"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/layer"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// compileInputs is the per-kind-agnostic input compileClosure needs to
// build one of the eight shared kinds' own Compile closure — mirrors
// cmd/aw/definition.go's own compileInputs, plus VersionID (this package
// always mints a fresh one itself via Dependencies.IDs, never lets a
// caller supply one — see routes.go's own doc comment) and Dependencies
// (this package's own request bodies accept author-declared pins; cmd/aw's
// CLI never did, V2-11 being explicitly out of scope for dependency
// declarations on the eight shared kinds).
type compileInputs struct {
	DefinitionID  string
	RawDocument   []byte
	Format        authoring.Format
	VersionID     string
	VersionNumber uint64
	SchemaVersion int
	Dependencies  definition.DependencyManifest
	PublishedBy   string
	PublishedAt   time.Time
}

// compileClosure builds the func() (definition.VersionFields, error)
// closure internal/app/definitions.ValidateDraftRequest.Compile/
// PublishDefinitionVersionRequest.Compile expect, for any of the eight
// shared (non-Workflow) DefinitionKinds. kind must already be one of those
// eight (callers route KindWorkflow through workflowRequestFrom below
// instead) — an unrecognized kind returns a plain error, never reached in
// practice since every route validates kind via definition.Kind.Valid()
// before ever calling this.
func compileClosure(kind definition.Kind, fields definition.Fields, in compileInputs) (func() (definition.VersionFields, error), error) {
	switch kind {
	case definition.KindBlock:
		def := block.BlockDefinition{ID: block.BlockDefinitionID(in.DefinitionID), Fields: fields}
		req := block.PublishRequest{
			VersionID: block.BlockVersionID(in.VersionID), VersionNumber: in.VersionNumber,
			SchemaVersion: in.SchemaVersion, Dependencies: in.Dependencies, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) { return block.CompileFrom(def, in.RawDocument, in.Format, req) }, nil
	case definition.KindSkill:
		def := skill.SkillDefinition{ID: skill.SkillDefinitionID(in.DefinitionID), Fields: fields}
		req := skill.PublishRequest{
			VersionID: skill.SkillVersionID(in.VersionID), VersionNumber: in.VersionNumber,
			SchemaVersion: in.SchemaVersion, Dependencies: in.Dependencies, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) { return skill.CompileFrom(def, in.RawDocument, in.Format, req) }, nil
	case definition.KindLayer:
		def := layer.LayerDefinition{ID: layer.LayerDefinitionID(in.DefinitionID), Fields: fields}
		req := layer.PublishRequest{
			VersionID: layer.LayerVersionID(in.VersionID), VersionNumber: in.VersionNumber,
			SchemaVersion: in.SchemaVersion, Dependencies: in.Dependencies, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) { return layer.CompileFrom(def, in.RawDocument, in.Format, req) }, nil
	case definition.KindEngineeringPack:
		def := engineeringpack.EngineeringPackDefinition{ID: engineeringpack.EngineeringPackDefinitionID(in.DefinitionID), Fields: fields}
		req := engineeringpack.PublishRequest{
			VersionID: engineeringpack.EngineeringPackVersionID(in.VersionID), VersionNumber: in.VersionNumber,
			SchemaVersion: in.SchemaVersion, Dependencies: in.Dependencies, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) {
			return engineeringpack.CompileFrom(def, in.RawDocument, in.Format, req)
		}, nil
	case definition.KindAgentProfile:
		def := agentprofile.AgentProfileDefinition{ID: agentprofile.AgentProfileDefinitionID(in.DefinitionID), Fields: fields}
		req := agentprofile.PublishRequest{
			VersionID: agentprofile.AgentProfileVersionID(in.VersionID), VersionNumber: in.VersionNumber,
			SchemaVersion: in.SchemaVersion, Dependencies: in.Dependencies, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) { return agentprofile.CompileFrom(def, in.RawDocument, in.Format, req) }, nil
	case definition.KindCommand:
		def := command.CommandDefinition{ID: command.CommandDefinitionID(in.DefinitionID), Fields: fields}
		req := command.PublishRequest{
			VersionID: command.CommandVersionID(in.VersionID), VersionNumber: in.VersionNumber,
			SchemaVersion: in.SchemaVersion, Dependencies: in.Dependencies, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) { return command.CompileFrom(def, in.RawDocument, in.Format, req) }, nil
	case definition.KindGate:
		def := gate.GateDefinition{ID: gate.GateDefinitionID(in.DefinitionID), Fields: fields}
		req := gate.PublishRequest{
			VersionID: gate.GateVersionID(in.VersionID), VersionNumber: in.VersionNumber,
			SchemaVersion: in.SchemaVersion, Dependencies: in.Dependencies, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) { return gate.CompileFrom(def, in.RawDocument, in.Format, req) }, nil
	case definition.KindPolicy:
		def := policy.PolicyDefinition{ID: policy.PolicyDefinitionID(in.DefinitionID), Fields: fields}
		req := policy.PublishRequest{
			VersionID: policy.PolicyVersionID(in.VersionID), VersionNumber: in.VersionNumber,
			SchemaVersion: in.SchemaVersion, Dependencies: in.Dependencies, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) { return policy.CompileFrom(def, in.RawDocument, in.Format, req) }, nil
	default:
		return nil, fmt.Errorf("definitions: kind %q has no non-workflow compile path", kind)
	}
}

// decodeWorkflowDocument strictly decodes raw JSON into a
// workflow.WorkflowDocument — mirrors cmd/aw/definition.go's own
// decodeWorkflowDocument exactly: Workflow has no YAML decode path
// anywhere in this codebase (internal/domain/workflow never exposes a
// DecodeStrict-based DecodeDocument the way the other eight kinds do), so
// every WORKFLOW route in this package only ever accepts "format": "json".
func decodeWorkflowDocument(raw []byte) (workflow.WorkflowDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document workflow.WorkflowDocument
	if err := decoder.Decode(&document); err != nil {
		return workflow.WorkflowDocument{}, fmt.Errorf("decode workflow document: %w", err)
	}
	return document, nil
}

// workflowRequestFrom builds the workflow.WorkflowDefinition/PublishRequest
// pair ValidateDraftRequest.WorkflowDefinition/WorkflowRequest and
// PublishDefinitionVersionRequest.WorkflowDefinition/WorkflowRequest need,
// from this route's own REAL reloaded definition.Fields (fields.Name/
// Status/Scope, never a caller-supplied or synthetic placeholder) plus the
// raw JSON document body.
func workflowRequestFrom(definitionID string, fields definition.Fields, raw []byte, versionID string, versionNumber uint64, publishedBy string, publishedAt time.Time) (workflow.WorkflowDefinition, workflow.PublishRequest, error) {
	document, err := decodeWorkflowDocument(raw)
	if err != nil {
		return workflow.WorkflowDefinition{}, workflow.PublishRequest{}, err
	}
	wfDefinition := workflow.WorkflowDefinition{
		ID: workflow.WorkflowDefinitionID(definitionID), Name: fields.Name,
		Status: fields.Status, Version: fields.Version,
	}
	if !fields.Scope.IsGlobal() {
		pid := *fields.Scope.ProjectID
		wfDefinition.ProjectID = &pid
	}
	request := workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID(versionID), VersionNumber: versionNumber,
		Document: document, PublishedBy: publishedBy, PublishedAt: publishedAt,
	}
	return wfDefinition, request, nil
}
