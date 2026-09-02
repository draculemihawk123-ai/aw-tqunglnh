package block

import (
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// executableExecutorKinds is the closed set of DefinitionKinds this
// repo's design actually grants execution authority to
// (docs/design/01-system-design.md's "Quyền thực thi" column: Block
// "qua executor ref", Command/Gate "có, khi policy grant", Agent Profile
// "qua provider policy"). Skill, Layer, Engineering Pack and Policy are
// deliberately absent — the first three are passive resources
// (docs/00-start-here.md requirement 6), and Policy is referenced
// through PolicyRefs, never as something that itself executes. Workflow
// is absent too: a Block's executor is what runs one node, not another
// whole graph (that composition is SUBFLOW, a structural node type this
// package's NodeType never lists).
var executableExecutorKinds = map[definition.Kind]bool{
	definition.KindBlock:        true,
	definition.KindCommand:      true,
	definition.KindGate:         true,
	definition.KindAgentProfile: true,
}

// ValidateDocument checks doc against every rule HE-14-M02 and this
// package's own doc comments require, collecting every problem found
// rather than stopping at the first — an author fixing a Block from
// scratch needs the whole picture in one pass.
func ValidateDocument(doc BlockDocument) authoring.Diagnostics {
	var diags authoring.Diagnostics

	diags = append(diags, validateCompatibleNodeTypes(doc.CompatibleNodeTypes)...)
	diags = append(diags, validateRequiredCapabilities(doc.RequiredCapabilities)...)

	if doc.TimeoutSeconds == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "timeoutSeconds",
			What: "timeout is zero",
			Why:  "a Block with no timeout can run forever, starving whatever budget the node's run is under",
			Fix:  "set timeoutSeconds to a positive number of seconds",
		})
	}

	diags = append(diags, validateScopeSelector(doc.ScopeSelector)...)

	if strings.TrimSpace(string(doc.DoneCondition)) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: "doneCondition",
			What: "done condition is empty",
			Why:  "without it nothing decides which declared outcome this Block's execution actually reached",
			Fix:  "set doneCondition to the expression the runtime should evaluate against the execution result",
		})
	}

	diags = append(diags, validateExecutorRef(doc.ExecutorRef)...)
	diags = append(diags, validatePolicyRefs(doc.PolicyRefs)...)
	diags = append(diags, validateOutcomes(doc.Outcomes)...)

	return diags
}

func validateCompatibleNodeTypes(nodeTypes []NodeType) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if len(nodeTypes) == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "compatibleNodeTypes",
			What: "no compatible node types declared",
			Why:  "a Block that isn't compatible with any node type can never be referenced by a graph",
			Fix:  "add at least one of AGENT, COMMAND, MACHINE_GATE or HUMAN_TASK",
		})
	}
	seen := make(map[NodeType]bool, len(nodeTypes))
	for i, nodeType := range nodeTypes {
		path := fmt.Sprintf("compatibleNodeTypes[%d]", i)
		if !validNodeTypes[nodeType] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("unsupported node type %q", nodeType),
				Why:  "only AGENT, COMMAND, MACHINE_GATE and HUMAN_TASK are node types a Block's executor ref can ever be invoked for",
				Fix:  "use one of AGENT, COMMAND, MACHINE_GATE, HUMAN_TASK",
			})
			continue
		}
		if seen[nodeType] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("duplicate compatible node type %q", nodeType),
				Why:  "listing the same node type twice can never mean anything more than listing it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seen[nodeType] = true
	}
	return diags
}

func validateRequiredCapabilities(capabilities []string) authoring.Diagnostics {
	var diags authoring.Diagnostics
	seen := make(map[string]bool, len(capabilities))
	for i, capability := range capabilities {
		path := fmt.Sprintf("requiredCapabilities[%d]", i)
		trimmed := strings.TrimSpace(capability)
		if trimmed == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "required capability is empty",
				Why:  "an empty capability name can never be granted or checked against",
				Fix:  "remove the empty entry or name the capability it should have been",
			})
			continue
		}
		if seen[trimmed] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("duplicate required capability %q", trimmed),
				Why:  "listing the same capability twice can never mean anything more than listing it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seen[trimmed] = true
	}
	return diags
}

func validateScopeSelector(selector ScopeSelector) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if selector.Access != ScopeAccessRead && selector.Access != ScopeAccessWrite {
		diags = append(diags, authoring.Diagnostic{
			Path: "scopeSelector.access",
			What: fmt.Sprintf("unsupported scope access %q", selector.Access),
			Why:  "a Block's declared repository access must be exactly READ or WRITE so a compiler can check it against real grants",
			Fix:  "set scopeSelector.access to READ or WRITE",
		})
	}
	seen := make(map[string]bool, len(selector.PathScopes))
	for i, pathScope := range selector.PathScopes {
		path := fmt.Sprintf("scopeSelector.pathScopes[%d]", i)
		trimmed := strings.TrimSpace(pathScope)
		if trimmed == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "path scope is empty",
				Why:  "an empty path pattern can never be matched against a real repository path",
				Fix:  "remove the empty entry or name the path pattern it should have been",
			})
			continue
		}
		if seen[trimmed] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("duplicate path scope %q", trimmed),
				Why:  "listing the same path pattern twice can never mean anything more than listing it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seen[trimmed] = true
	}
	return diags
}

func validateExecutorRef(ref definition.DependencyPin) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if strings.TrimSpace(ref.DefinitionID) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: "executorRef.definitionId",
			What: "executor ref definition id is empty",
			Why:  "a Block with no executor ref has nothing that can ever actually run it",
			Fix:  "set executorRef.definitionId to the Block/Command/Gate/AgentProfile definition to execute through",
		})
	}
	if strings.TrimSpace(ref.VersionID) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: "executorRef.versionId",
			What: "executor ref version id is empty",
			Why:  "a dependency pin must name an exact published version, never a range that could silently move later",
			Fix:  "set executorRef.versionId to the exact published version to pin",
		})
	}
	if !executableExecutorKinds[ref.Kind] {
		why := "only Block, Command, Gate and Agent Profile are ever granted execution authority in this design"
		if ref.Kind == definition.KindSkill || ref.Kind == definition.KindLayer {
			why = "Skill and Layer are passive resources — a script inside one stays inert data until a separate, policy-granted executable definition references its exact hash; a Block can never point at one directly"
		}
		diags = append(diags, authoring.Diagnostic{
			Path: "executorRef.kind",
			What: fmt.Sprintf("executor ref kind %q cannot execute", ref.Kind),
			Why:  why,
			Fix:  "point executorRef at a Block, Command, Gate or Agent Profile definition instead",
		})
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
				Why:  "policyRefs exists specifically to pin Policy definitions — anything else belongs in executorRef or a future dependency field instead",
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

func validateOutcomes(outcomes []string) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if len(outcomes) == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "outcomes",
			What: "no outcomes declared",
			Why:  "a done condition can never resolve to an outcome that was never declared, and a graph edge has nothing to route on",
			Fix:  "declare at least one outcome name",
		})
	}
	seen := make(map[string]bool, len(outcomes))
	for i, outcome := range outcomes {
		path := fmt.Sprintf("outcomes[%d]", i)
		trimmed := strings.TrimSpace(outcome)
		if trimmed == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "outcome name is empty",
				Why:  "an empty outcome name can never be routed on by a graph edge",
				Fix:  "remove the empty entry or name the outcome it should have been",
			})
			continue
		}
		if seen[trimmed] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("duplicate outcome %q", trimmed),
				Why:  "listing the same outcome twice can never mean anything more than listing it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seen[trimmed] = true
	}
	return diags
}
