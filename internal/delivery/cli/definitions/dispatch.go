// This file is this package's own per-Kind compile dispatch — the THIRD
// independent copy of this shape (see doc.go's own top comment for why it
// is never shared), mirroring
// internal/delivery/httpapi/definitions/dispatch.go's own compileClosure/
// workflowRequestFrom (V6-05, the actual precedent this package follows —
// NOT cmd/aw/definition.go's own pre-framework compileClosureForKind) as
// closely as an http.ResponseWriter-free package can: every one of the
// eight shared (non-Workflow) kinds follows the exact same shape —
// <Kind>Definition{ID, Fields}, PublishRequest{VersionID, VersionNumber,
// SchemaVersion, Document, Dependencies, PublishedBy, PublishedAt},
// CompileFrom(def, rawDocument, format, req) — so, like both precedents,
// this is deliberately nine near-identical cases rather than a clever
// generic dispatch: each kind's own concrete ID/Version types are exactly
// what keep a document authored for one kind from ever being silently
// accepted as another's.
package definitions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
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
// httpapi's own compileInputs field for field.
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
// closure appdefinitions.ValidateDraftRequest.Compile/
// PublishDefinitionVersionRequest.Compile expect, for any of the eight
// shared (non-Workflow) DefinitionKinds. kind must already be one of those
// eight (callers route KindWorkflow through workflowRequestFrom below
// instead).
func compileClosure(kind definition.Kind, fields definition.Fields, in compileInputs) (func() (definition.VersionFields, error), error) {
	switch kind {
	case definition.KindBlock:
		def := block.BlockDefinition{ID: block.BlockDefinitionID(in.DefinitionID), Fields: fields}
		req := block.PublishRequest{
			VersionID: block.BlockVersionID(in.VersionID), VersionNumber: in.VersionNumber,
			SchemaVersion: in.SchemaVersion, Dependencies: in.Dependencies, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) {
			return block.CompileFrom(def, in.RawDocument, in.Format, req)
		}, nil
	case definition.KindSkill:
		def := skill.SkillDefinition{ID: skill.SkillDefinitionID(in.DefinitionID), Fields: fields}
		req := skill.PublishRequest{
			VersionID: skill.SkillVersionID(in.VersionID), VersionNumber: in.VersionNumber,
			SchemaVersion: in.SchemaVersion, Dependencies: in.Dependencies, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) {
			return skill.CompileFrom(def, in.RawDocument, in.Format, req)
		}, nil
	case definition.KindLayer:
		def := layer.LayerDefinition{ID: layer.LayerDefinitionID(in.DefinitionID), Fields: fields}
		req := layer.PublishRequest{
			VersionID: layer.LayerVersionID(in.VersionID), VersionNumber: in.VersionNumber,
			SchemaVersion: in.SchemaVersion, Dependencies: in.Dependencies, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) {
			return layer.CompileFrom(def, in.RawDocument, in.Format, req)
		}, nil
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
		return func() (definition.VersionFields, error) {
			return agentprofile.CompileFrom(def, in.RawDocument, in.Format, req)
		}, nil
	case definition.KindCommand:
		def := command.CommandDefinition{ID: command.CommandDefinitionID(in.DefinitionID), Fields: fields}
		req := command.PublishRequest{
			VersionID: command.CommandVersionID(in.VersionID), VersionNumber: in.VersionNumber,
			SchemaVersion: in.SchemaVersion, Dependencies: in.Dependencies, PublishedBy: in.PublishedBy, PublishedAt: in.PublishedAt,
		}
		return func() (definition.VersionFields, error) {
			return command.CompileFrom(def, in.RawDocument, in.Format, req)
		}, nil
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
		return func() (definition.VersionFields, error) {
			return policy.CompileFrom(def, in.RawDocument, in.Format, req)
		}, nil
	default:
		return nil, fmt.Errorf("cli/definitions: kind %q has no non-workflow compile path", kind)
	}
}

// decodeWorkflowDocument strictly decodes raw JSON into a
// workflow.WorkflowDocument — mirrors httpapi's own decodeWorkflowDocument:
// Workflow has no YAML decode path anywhere in this codebase, so every
// WORKFLOW document this package ever compiles only ever accepts
// "format": "json".
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
// pair appdefinitions.ValidateDraftRequest.WorkflowDefinition/
// WorkflowRequest and PublishDefinitionVersionRequest.WorkflowDefinition/
// WorkflowRequest need, from this invocation's own REAL reloaded
// definition.Fields (fields.Name/Status/Scope, never a caller-supplied or
// synthetic placeholder) plus the raw JSON document body.
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

// documentInput is the flag/stdin-derived document payload every
// validate/publish invocation decodes before dispatching — this package's
// own equivalent of httpapi's authorDocumentBody, sourced from flags
// (--format, --schema-version) plus a bounded body (--file/stdin) instead
// of an HTTP JSON request body. Deliberately carries no author-declared
// Dependencies field (unlike httpapi's authorDocumentBody): this task's
// own "Command surface to build" names no --dependencies-style flag for
// validate/publish, so every candidate this package compiles gets an
// empty definition.DependencyManifest — exactly the same zero-value HTTP
// itself produces for a request body that omits "dependencies" too, never
// a behavior gap, just a CLI flag surface this task did not ask for.
type documentInput struct {
	Content       string
	Format        authoring.Format
	SchemaVersion int
}

// buildCandidate decodes in against kind's own real document shape and
// returns either a non-workflow Compile closure (compile != nil,
// wfDefinition/wfRequest zero) or a Workflow WorkflowDefinition/
// PublishRequest pair (compile == nil) — exactly the either/or
// appdefinitions.ValidateDraftRequest/PublishDefinitionVersionRequest
// themselves accept. Mirrors httpapi's own buildCandidate, minus its
// http.ResponseWriter-writing side effects: every failure here is
// returned as a plain error for the caller (validate.go/publish.go) to
// classify.
func buildCandidate(kind definition.Kind, definitionID string, fields definition.Fields, in documentInput, versionID string, versionNumber uint64, publishedBy string, publishedAt time.Time) (compile func() (definition.VersionFields, error), wfDefinition workflow.WorkflowDefinition, wfRequest workflow.PublishRequest, err error) {
	if strings.TrimSpace(in.Content) == "" {
		return nil, workflow.WorkflowDefinition{}, workflow.PublishRequest{}, usageErrorf("--content (or --file/stdin) is required")
	}

	if kind == definition.KindWorkflow {
		if in.Format != authoring.FormatJSON {
			return nil, workflow.WorkflowDefinition{}, workflow.PublishRequest{}, usageErrorf(`WORKFLOW documents must be --format "json" — no YAML decode path exists for WORKFLOW`)
		}
		def, req, werr := workflowRequestFrom(definitionID, fields, []byte(in.Content), versionID, versionNumber, publishedBy, publishedAt)
		if werr != nil {
			return nil, workflow.WorkflowDefinition{}, workflow.PublishRequest{}, werr
		}
		return nil, def, req, nil
	}

	schemaVersion := in.SchemaVersion
	if schemaVersion == 0 {
		schemaVersion = 1
	}
	compileFn, cerr := compileClosure(kind, fields, compileInputs{
		DefinitionID: definitionID, RawDocument: []byte(in.Content), Format: in.Format,
		VersionID: versionID, VersionNumber: versionNumber, SchemaVersion: schemaVersion,
		PublishedBy: publishedBy, PublishedAt: publishedAt,
	})
	if cerr != nil {
		return nil, workflow.WorkflowDefinition{}, workflow.PublishRequest{}, cerr
	}
	return compileFn, workflow.WorkflowDefinition{}, workflow.PublishRequest{}, nil
}
