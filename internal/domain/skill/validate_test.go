package skill_test

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
)

func verifiedAt(t time.Time) *time.Time { return &t }

func validDocument() skill.SkillDocument {
	return skill.SkillDocument{
		Resources: []skill.Resource{
			{
				Key:         "go-error-wrapping",
				Instruction: "wrap errors with fmt.Errorf(\"%w\", err)",
				Priority:    definition.PriorityHardConstraint,
				Selector: skill.Selector{
					ComponentTags: []string{"backend"},
					TaskKinds:     []string{"code-change"},
				},
				Provenance: skill.Provenance{
					Owner:        "platform-team",
					Source:       "docs/style/go-errors.md",
					LastVerified: verifiedAt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
				},
			},
		},
	}
}

func TestValidateDocument_ValidDocumentHasNoProblems(t *testing.T) {
	diags := skill.ValidateDocument(validDocument())
	if diags.HasProblems() {
		t.Fatalf("ValidateDocument(valid) = %v, want no problems", diags)
	}
}

func TestValidateDocument_RejectsNoResources(t *testing.T) {
	diags := skill.ValidateDocument(skill.SkillDocument{})
	requireProblemPath(t, diags, "resources")
}

func TestValidateDocument_RejectsEmptyKey(t *testing.T) {
	doc := validDocument()
	doc.Resources[0].Key = "  "
	diags := skill.ValidateDocument(doc)
	requireProblemPath(t, diags, "resources[0].key")
}

func TestValidateDocument_RejectsDuplicateKey(t *testing.T) {
	doc := validDocument()
	doc.Resources = append(doc.Resources, doc.Resources[0])
	diags := skill.ValidateDocument(doc)
	requireProblemPath(t, diags, "resources[1].key")
}

func TestValidateDocument_RejectsEmptyInstruction(t *testing.T) {
	doc := validDocument()
	doc.Resources[0].Instruction = ""
	diags := skill.ValidateDocument(doc)
	requireProblemPath(t, diags, "resources[0].instruction")
}

func TestValidateDocument_RejectsUnsupportedPriority(t *testing.T) {
	doc := validDocument()
	doc.Resources[0].Priority = "URGENT"
	diags := skill.ValidateDocument(doc)
	requireProblemPath(t, diags, "resources[0].priority")
}

func TestValidateDocument_AcceptsEveryPriorityClass(t *testing.T) {
	for _, priority := range []definition.PriorityClass{
		definition.PriorityHardConstraint, definition.PriorityRequiredProcedure,
		definition.PriorityGuidance, definition.PriorityReference,
	} {
		t.Run(string(priority), func(t *testing.T) {
			doc := validDocument()
			doc.Resources[0].Priority = priority
			diags := skill.ValidateDocument(doc)
			for _, d := range diags {
				if d.Path == "resources[0].priority" {
					t.Fatalf("ValidateDocument should accept priority=%s, got %v", priority, d)
				}
			}
		})
	}
}

func TestValidateDocument_RejectsNonGlobalResourceWithoutSelector(t *testing.T) {
	doc := validDocument()
	doc.Resources[0].Selector = skill.Selector{}
	diags := skill.ValidateDocument(doc)
	requireProblemPath(t, diags, "resources[0].selector")
}

func TestValidateDocument_AcceptsGlobalResourceWithoutSelector(t *testing.T) {
	doc := validDocument()
	doc.Resources[0].Global = true
	doc.Resources[0].Selector = skill.Selector{}
	diags := skill.ValidateDocument(doc)
	if diags.HasProblems() {
		t.Fatalf("ValidateDocument(global, no selector) = %v, want no problems", diags)
	}
}

func TestValidateDocument_RejectsEmptySelectorValue(t *testing.T) {
	doc := validDocument()
	doc.Resources[0].Selector.ComponentTags = []string{" "}
	diags := skill.ValidateDocument(doc)
	requireProblemPath(t, diags, "resources[0].selector.componentTags[0]")
}

func TestValidateDocument_RejectsDuplicateSelectorValue(t *testing.T) {
	doc := validDocument()
	doc.Resources[0].Selector.ComponentTags = []string{"backend", "backend"}
	diags := skill.ValidateDocument(doc)
	requireProblemPath(t, diags, "resources[0].selector.componentTags[1]")
}

func TestValidateDocument_RejectsMissingOwner(t *testing.T) {
	doc := validDocument()
	doc.Resources[0].Provenance.Owner = ""
	diags := skill.ValidateDocument(doc)
	requireProblemPath(t, diags, "resources[0].provenance.owner")
}

func TestValidateDocument_RejectsMissingSource(t *testing.T) {
	doc := validDocument()
	doc.Resources[0].Provenance.Source = ""
	diags := skill.ValidateDocument(doc)
	requireProblemPath(t, diags, "resources[0].provenance.source")
}

func TestValidateDocument_RejectsMissingLastVerifiedAndRevision(t *testing.T) {
	doc := validDocument()
	doc.Resources[0].Provenance.LastVerified = nil
	doc.Resources[0].Provenance.Revision = ""
	diags := skill.ValidateDocument(doc)
	requireProblemPath(t, diags, "resources[0].provenance")
}

func TestValidateDocument_AcceptsRevisionInsteadOfLastVerified(t *testing.T) {
	doc := validDocument()
	doc.Resources[0].Provenance.LastVerified = nil
	doc.Resources[0].Provenance.Revision = "commit-abc123"
	diags := skill.ValidateDocument(doc)
	if diags.HasProblems() {
		t.Fatalf("ValidateDocument(revision instead of lastVerified) = %v, want no problems", diags)
	}
}

func requireProblemPath(t *testing.T, diags authoring.Diagnostics, path string) {
	t.Helper()
	for _, d := range diags {
		if d.Path == path {
			return
		}
	}
	t.Fatalf("diags = %v, want a problem at path %q", diags, path)
}
