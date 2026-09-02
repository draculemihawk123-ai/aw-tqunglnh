package gate

import (
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// ValidateDocument checks doc against every rule V2-05 requires,
// collecting every problem found rather than stopping at the first.
func ValidateDocument(doc GateDocument) authoring.Diagnostics {
	var diags authoring.Diagnostics

	diags = append(diags, validateCommandRef(doc.CommandRef)...)
	diags = append(diags, validateCriteria(doc.Criteria)...)
	diags = append(diags, validatePolicyRefs(doc.PolicyRefs)...)

	return diags
}

func validateCommandRef(ref definition.DependencyPin) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if strings.TrimSpace(ref.DefinitionID) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: "commandRef.definitionId",
			What: "command ref definition id is empty",
			Why:  "a Gate with no command ref has nothing to evaluate",
			Fix:  "set commandRef.definitionId to the Command definition this Gate evaluates",
		})
	}
	if strings.TrimSpace(ref.VersionID) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: "commandRef.versionId",
			What: "command ref version id is empty",
			Why:  "a dependency pin must name an exact published version, never a range that could silently move later",
			Fix:  "set commandRef.versionId to the exact published version to pin",
		})
	}
	if ref.Kind != definition.KindCommand {
		diags = append(diags, authoring.Diagnostic{
			Path: "commandRef.kind",
			What: fmt.Sprintf("command ref kind %q is not COMMAND", ref.Kind),
			Why:  "a Gate evaluates a published Command's result — it never evaluates any other DefinitionKind directly",
			Fix:  "point commandRef at a Command definition instead",
		})
	}
	return diags
}

func validateCriteria(criteria []Criterion) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if len(criteria) == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "criteria",
			What: "no criteria declared",
			Why:  "a Gate with no criteria has no evidence mapping at all, so its evaluation can never produce an authoritative verdict",
			Fix:  "declare at least one criterion mapping a name to an evidence key",
		})
	}
	seenNames := make(map[string]bool, len(criteria))
	seenKeys := make(map[string]bool, len(criteria))
	for i, criterion := range criteria {
		pathPrefix := fmt.Sprintf("criteria[%d]", i)
		name := strings.TrimSpace(criterion.Name)
		if name == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: pathPrefix + ".name",
				What: "criterion name is empty",
				Why:  "an empty criterion name can never be reported against in evidence",
				Fix:  "name the criterion",
			})
		} else if seenNames[name] {
			diags = append(diags, authoring.Diagnostic{
				Path: pathPrefix + ".name",
				What: fmt.Sprintf("duplicate criterion name %q", name),
				Why:  "two criteria with the same name can never be told apart in evidence",
				Fix:  "give each criterion a distinct name",
			})
		}
		seenNames[name] = true

		key := strings.TrimSpace(criterion.EvidenceKey)
		if key == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: pathPrefix + ".evidenceKey",
				What: "evidence key is empty",
				Why:  "a criterion with no evidence key has nothing for the runtime to record its verdict against — this is exactly the missing evidence mapping V2-05 requires rejecting",
				Fix:  "set evidenceKey to the output/evidence field this criterion maps from",
			})
		} else if seenKeys[key] {
			diags = append(diags, authoring.Diagnostic{
				Path: pathPrefix + ".evidenceKey",
				What: fmt.Sprintf("duplicate evidence key %q", key),
				Why:  "two criteria mapped to the same evidence key can never be distinguished by whatever produced that evidence",
				Fix:  "map each criterion to a distinct evidence key",
			})
		}
		seenKeys[key] = true
	}
	return diags
}

func validatePolicyRefs(refs []definition.DependencyPin) authoring.Diagnostics {
	var diags authoring.Diagnostics
	seen := make(map[string]bool, len(refs))
	for i, ref := range refs {
		pathPrefix := fmt.Sprintf("policyRefs[%d]", i)
		if strings.TrimSpace(ref.DefinitionID) == "" || strings.TrimSpace(ref.VersionID) == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: pathPrefix,
				What: "policy ref is missing definitionId or versionId",
				Why:  "a dependency pin must name an exact published version to be resolvable at all",
				Fix:  "set both definitionId and versionId on the policy ref",
			})
			continue
		}
		if ref.Kind != definition.KindPolicy {
			diags = append(diags, authoring.Diagnostic{
				Path: pathPrefix + ".kind",
				What: fmt.Sprintf("policy ref kind %q is not POLICY", ref.Kind),
				Why:  "policyRefs exists specifically to pin Policy definitions",
				Fix:  "set kind to POLICY, or move this pin to the field it actually belongs in",
			})
		}
		if seen[ref.DefinitionID] {
			diags = append(diags, authoring.Diagnostic{
				Path: pathPrefix,
				What: fmt.Sprintf("duplicate policy ref %q", ref.DefinitionID),
				Why:  "pinning the same policy definition twice can never mean anything more than pinning it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seen[ref.DefinitionID] = true
	}
	return diags
}
