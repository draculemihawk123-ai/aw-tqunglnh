package engineeringpack

import (
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// ValidateDocument checks doc's own directly-authored dependency pins:
// shape, allowed Kind and duplicates. It cannot check the composition
// graph beyond doc's own direct pins (no self-reference against this
// pack's own DefinitionID — that needs the enclosing EngineeringPackDefinition,
// checked in Compile — and no cross-pack cycle, since that requires
// other packs' own already-published dependency data, which is exactly
// what ResolvePackGraph in graph.go exists for and what a real caller
// like a future graph/dependency compiler, V2-09, would supply).
func ValidateDocument(doc EngineeringPackDocument) authoring.Diagnostics {
	var diags authoring.Diagnostics

	if len(doc.Dependencies) == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "dependencies",
			What: "no dependencies declared",
			Why:  "an Engineering Pack that composes nothing can never contribute any resource once installed",
			Fix:  "add at least one Skill, Layer or Engineering Pack dependency pin",
		})
	}

	seenDefinitionIDs := make(map[string]bool, len(doc.Dependencies))
	for i, pin := range doc.Dependencies {
		path := fmt.Sprintf("dependencies[%d]", i)

		definitionID := strings.TrimSpace(pin.DefinitionID)
		versionID := strings.TrimSpace(pin.VersionID)
		if definitionID == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: path + ".definitionId",
				What: "dependency definition id is empty",
				Why:  "a dependency pin must name an exact published version to be resolvable at all",
				Fix:  "set definitionId to the Skill/Layer/Engineering Pack definition to depend on",
			})
		}
		if versionID == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: path + ".versionId",
				What: "dependency version id is empty",
				Why:  "a dependency pin must name an exact published version, never a range that could silently move later",
				Fix:  "set versionId to the exact published version to pin",
			})
		}

		if !packDependencyKinds[pin.Kind] {
			diags = append(diags, authoring.Diagnostic{
				Path: path + ".kind",
				What: fmt.Sprintf("dependency kind %q is not composable", pin.Kind),
				Why:  "an Engineering Pack only ever composes passive resources — Skill, Layer or another Engineering Pack — never anything with execution authority",
				Fix:  "set kind to SKILL, LAYER or ENGINEERING_PACK",
			})
		}

		if definitionID != "" {
			if seenDefinitionIDs[definitionID] {
				diags = append(diags, authoring.Diagnostic{
					Path: path,
					What: fmt.Sprintf("duplicate dependency on definition %q", definitionID),
					Why:  "pinning the same definition twice (even at different versions) leaves it ambiguous which version's resources this pack actually composes",
					Fix:  "remove the duplicate entry, keeping only the one version intended",
				})
			}
			seenDefinitionIDs[definitionID] = true
		}
	}

	return diags
}

// validateNoSelfDependency rejects a pack that pins its own
// DefinitionID as one of its Engineering Pack dependencies — the
// smallest possible pack composition cycle, and one this package can
// catch locally (unlike a longer A->B->A cycle across two different
// packs, which needs ResolvePackGraph and data from both packs).
func validateNoSelfDependency(ownDefinitionID string, doc EngineeringPackDocument) authoring.Diagnostics {
	var diags authoring.Diagnostics
	for i, pin := range doc.Dependencies {
		if pin.Kind == definition.KindEngineeringPack && pin.DefinitionID == ownDefinitionID {
			diags = append(diags, authoring.Diagnostic{
				Path: fmt.Sprintf("dependencies[%d]", i),
				What: "dependency pins this pack's own definition",
				Why:  "a pack cannot compose itself — that is a one-node cycle in the pack composition graph, which can never be resolved",
				Fix:  "remove the self-referencing dependency",
			})
		}
	}
	return diags
}
