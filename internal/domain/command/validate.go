package command

import (
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// ValidateDocument checks doc against every rule V2-05 requires,
// collecting every problem found rather than stopping at the first.
func ValidateDocument(doc CommandDocument) authoring.Diagnostics {
	var diags authoring.Diagnostics

	diags = append(diags, validateExecutableRef(doc.Executable)...)
	diags = append(diags, validateArgv(doc.Argv, doc.PlaceholderAllowlist)...)
	diags = append(diags, validateStringSet("placeholderAllowlist", doc.PlaceholderAllowlist)...)

	if strings.TrimSpace(doc.CwdRepositoryTarget) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: "cwdRepositoryTarget",
			What: "cwd repository target is empty",
			Why:  "a Command's working directory must resolve against a named repository target, never an implicit default",
			Fix:  "set cwdRepositoryTarget to the repository this Command runs against",
		})
	}

	diags = append(diags, validateCompatibility(doc.Compatibility)...)
	diags = append(diags, validateStringSet("envAllowlist", doc.EnvAllowlist)...)

	if doc.NetworkAccess != NetworkAccessNone && doc.NetworkAccess != NetworkAccessAllowed {
		diags = append(diags, authoring.Diagnostic{
			Path: "networkAccess",
			What: fmt.Sprintf("unsupported network access %q", doc.NetworkAccess),
			Why:  "network permission must be declared exactly, never left ambiguous",
			Fix:  "set networkAccess to NONE or ALLOWED",
		})
	}

	diags = append(diags, validateStringSet("secretRefs", doc.SecretRefs)...)

	if doc.TimeoutSeconds == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "timeoutSeconds",
			What: "timeout is zero",
			Why:  "a Command with no timeout can run forever, starving whatever budget its run is under",
			Fix:  "set timeoutSeconds to a positive number of seconds",
		})
	}
	if doc.Output.MaxOutputBytes == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "output.maxOutputBytes",
			What: "max output bytes is zero",
			Why:  "an unbounded output capture can exhaust memory or disk on a runaway process",
			Fix:  "set output.maxOutputBytes to a positive byte limit",
		})
	}

	diags = append(diags, validatePolicyRefs(doc.PolicyRefs)...)

	return diags
}

func validateExecutableRef(ref ExecutableRef) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if strings.TrimSpace(ref.OwnerVersionID) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: "executable.ownerVersionId",
			What: "executable owner version id is empty",
			Why:  "a Command must pin the exact Skill/Layer version its script resource lives in",
			Fix:  "set executable.ownerVersionId to the resource's owning version",
		})
	}
	if strings.TrimSpace(ref.ResourceKey) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: "executable.resourceKey",
			What: "executable resource key is empty",
			Why:  "a resource identity is owner_version_id + resource_key + content_hash — all three are required",
			Fix:  "set executable.resourceKey to the resource's key within its owning version",
		})
	}
	if strings.TrimSpace(ref.ContentHash) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: "executable.contentHash",
			What: "executable content hash is empty",
			Why:  "a script stays inert data until something references its exact hash — an unpinned executable can never be trusted to still be the content that was reviewed",
			Fix:  "set executable.contentHash to the exact hash of the script content to pin",
		})
	}
	return diags
}

func validateArgv(argv []ArgvElement, allowlist []string) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if len(argv) == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "argv",
			What: "argv is empty",
			Why:  "a Command with nothing to spawn can never execute",
			Fix:  "declare at least one argv element",
		})
	}
	allowed := make(map[string]bool, len(allowlist))
	for _, name := range allowlist {
		allowed[strings.TrimSpace(name)] = true
	}
	for i, element := range argv {
		path := fmt.Sprintf("argv[%d]", i)
		switch element.Kind {
		case ArgvLiteral:
			// A literal is never checked against the placeholder
			// allowlist — literal text is exactly what it says.
		case ArgvPlaceholder:
			name := strings.TrimSpace(element.Value)
			if name == "" {
				diags = append(diags, authoring.Diagnostic{
					Path: path + ".value",
					What: "placeholder name is empty",
					Why:  "an empty placeholder name can never be resolved to a declared allowlist entry",
					Fix:  "name the placeholder, or change this element's kind to LITERAL",
				})
				continue
			}
			if !allowed[name] {
				diags = append(diags, authoring.Diagnostic{
					Path: path + ".value",
					What: fmt.Sprintf("unknown placeholder %q", name),
					Why:  "a placeholder not in placeholderAllowlist could substitute anything at runtime with nothing to check it against at publish time",
					Fix:  fmt.Sprintf("add %q to placeholderAllowlist, or fix the typo", name),
				})
			}
		default:
			diags = append(diags, authoring.Diagnostic{
				Path: path + ".kind",
				What: fmt.Sprintf("unsupported argv element kind %q", element.Kind),
				Why:  "argv is never a free-form shell string — every element must be exactly LITERAL or PLACEHOLDER, spawned argv-only",
				Fix:  "set kind to LITERAL or PLACEHOLDER",
			})
		}
	}
	return diags
}

func validateCompatibility(compat Compatibility) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if len(compat.OS) == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "compatibility.os",
			What: "no OS compatibility declared",
			Why:  "a Command must declare which OS(es) it runs on, the same compatibility contract Layer already requires",
			Fix:  "declare at least one OS this Command supports",
		})
	}
	diags = append(diags, validateStringSet("compatibility.os", compat.OS)...)
	diags = append(diags, validateStringSet("compatibility.toolchain", compat.Toolchain)...)
	return diags
}

// validateStringSet is the shared shape of "no empty entries, no
// duplicates" every declared name-set field in this package needs.
func validateStringSet(path string, values []string) authoring.Diagnostics {
	var diags authoring.Diagnostics
	seen := make(map[string]bool, len(values))
	for i, value := range values {
		entryPath := fmt.Sprintf("%s[%d]", path, i)
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: entryPath,
				What: "entry is empty",
				Why:  "an empty entry can never be matched against anything real",
				Fix:  "remove the empty entry or name what it should have been",
			})
			continue
		}
		if seen[trimmed] {
			diags = append(diags, authoring.Diagnostic{
				Path: entryPath,
				What: fmt.Sprintf("duplicate entry %q", trimmed),
				Why:  "listing the same entry twice can never mean anything more than listing it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seen[trimmed] = true
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
