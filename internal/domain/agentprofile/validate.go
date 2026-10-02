package agentprofile

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// ValidateDocument checks doc against every rule this package's own doc
// comments require, collecting every problem found rather than stopping
// at the first — mirrors internal/domain/block.ValidateDocument's own
// collect-everything convention.
func ValidateDocument(doc AgentProfileDocument) authoring.Diagnostics {
	var diags authoring.Diagnostics

	if strings.TrimSpace(doc.ProviderKey) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: "providerKey",
			What: "provider key is empty",
			Why:  "a profile with no provider key names no adapter that could ever execute it",
			Fix:  "set providerKey to the provider adapter this profile runs through",
		})
	}
	if strings.TrimSpace(doc.Model) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: "model",
			What: "model is empty",
			Why:  "a profile with no model names nothing concrete to request from the provider",
			Fix:  "set model to the provider model this profile requests",
		})
	}

	diags = append(diags, validateToolRefs(doc.ToolRefs)...)
	diags = append(diags, validateContextPolicyRef(doc.ContextPolicyRef)...)
	diags = append(diags, validateCompatibility(doc.Compatibility)...)
	diags = append(diags, validateRequiredCapabilities(doc.RequiredCapabilities)...)
	diags = append(diags, validateEnvAllowlist(doc.EnvAllowlist)...)

	if doc.Budget.MaxTokens == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "budget.maxTokens",
			What: "max tokens is zero",
			Why:  "a zero token budget can never let an attempt under this profile do any work",
			Fix:  "set budget.maxTokens to a positive number",
		})
	}

	return diags
}

func validateToolRefs(refs []string) authoring.Diagnostics {
	var diags authoring.Diagnostics
	seen := make(map[string]bool, len(refs))
	for i, ref := range refs {
		path := fmt.Sprintf("toolRefs[%d]", i)
		trimmed := strings.TrimSpace(ref)
		if trimmed == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "tool ref is empty",
				Why:  "an empty tool identifier can never be matched against a real provider tool",
				Fix:  "remove the empty entry or name the tool it should have been",
			})
			continue
		}
		if seen[trimmed] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("duplicate tool ref %q", trimmed),
				Why:  "listing the same tool twice can never mean anything more than listing it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seen[trimmed] = true
	}
	return diags
}

// validateContextPolicyRef is V2-07's own "policy dependency" test case:
// a profile whose context-route pin does not structurally name an exact
// published Policy is rejected before it can ever be published.
func validateContextPolicyRef(ref definition.DependencyPin) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if strings.TrimSpace(ref.DefinitionID) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: "contextPolicyRef.definitionId",
			What: "context policy ref definition id is empty",
			Why:  "V2-07 requires context route to be an exact PolicyVersion — a profile with no definitionId pins nothing at all",
			Fix:  "set contextPolicyRef.definitionId to the Policy definition to pin",
		})
	}
	if strings.TrimSpace(ref.VersionID) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: "contextPolicyRef.versionId",
			What: "context policy ref version id is empty",
			Why:  "a dependency pin must name an exact published version, never a range that could silently move later",
			Fix:  "set contextPolicyRef.versionId to the exact published Policy version to pin",
		})
	}
	if ref.Kind != definition.KindPolicy {
		diags = append(diags, authoring.Diagnostic{
			Path: "contextPolicyRef.kind",
			What: fmt.Sprintf("context policy ref kind %q is not POLICY", ref.Kind),
			Why:  "context route must be an exact PolicyVersion (this Profile's own convention: that Policy's own Category must be CONTEXT, verified when the pin is actually resolved) — anything else cannot carry context-route semantics",
			Fix:  "set contextPolicyRef.kind to POLICY, pinning a Policy definition whose Category is CONTEXT",
		})
	}
	return diags
}

func validateCompatibility(c Compatibility) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if len(c.OS) == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "compatibility.os",
			What: "no OS declared",
			Why:  "a profile with no declared OS can never be checked against a real worker target",
			Fix:  "declare at least one of windows, linux",
		})
	}
	seenOS := make(map[string]bool, len(c.OS))
	for i, os := range c.OS {
		path := fmt.Sprintf("compatibility.os[%d]", i)
		trimmed := strings.TrimSpace(os)
		if !supportedOS[trimmed] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("unsupported OS %q", os),
				Why:  "ADR-002 scopes Alpha's core contract to windows and linux only — this profile could never run on a worker target outside that set",
				Fix:  "use one of: windows, linux",
			})
			continue
		}
		if seenOS[trimmed] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("duplicate OS %q", trimmed),
				Why:  "listing the same OS twice can never mean anything more than listing it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seenOS[trimmed] = true
	}

	seenToolchain := make(map[string]bool, len(c.Toolchain))
	for i, tc := range c.Toolchain {
		path := fmt.Sprintf("compatibility.toolchain[%d]", i)
		trimmed := strings.TrimSpace(tc)
		if trimmed == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "toolchain entry is empty",
				Why:  "an empty toolchain name can never be matched against a real worker capability",
				Fix:  "remove the empty entry or name the toolchain it should have been",
			})
			continue
		}
		if seenToolchain[trimmed] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("duplicate toolchain %q", trimmed),
				Why:  "listing the same toolchain twice can never mean anything more than listing it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seenToolchain[trimmed] = true
	}
	return diags
}

// validateRequiredCapabilities is V2-07's own "missing capability" test
// case: an empty capability entry can never be granted or checked
// against — same rule/message convention as
// internal/domain/block.validateRequiredCapabilities.
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

// validateEnvAllowlist is V9-05's rule for AgentProfileDocument.EnvAllowlist
// (gap G5): every entry must be a plausible environment variable NAME, and
// the list a set. A name is rejected when it is empty, contains a NUL byte,
// contains "=" or contains any whitespace — the first three are exactly what
// the process supervisor refuses to put in a child environment
// (internal/adapters/process buildEnvironment), the last one catches the
// padding and stray spaces that would make a name silently never match the
// operator's own `--env-allowlist` entry (matching is exact and
// case-sensitive, and the worker flag trims its entries, so a name that
// carries whitespace could never be equal to one). Names are NOT trimmed
// here: " PATH" is an error, not PATH, because the published document must
// say what it means. Duplicates (exact, case-sensitive) are rejected like
// every other set-like list of this document. This document holds names
// only: a NAME=value entry is refused precisely so a value can never be
// written into a definition.
//
// A local rule rather than a call into the worker's own list validation:
// internal/app/config.Validate only rejects blank entries, and a domain
// package may not import app anyway.
func validateEnvAllowlist(names []string) authoring.Diagnostics {
	var diags authoring.Diagnostics
	seen := make(map[string]bool, len(names))
	for i, name := range names {
		path := fmt.Sprintf("envAllowlist[%d]", i)
		switch {
		case name == "":
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "environment variable name is empty",
				Why:  "an empty name can never match a variable of the worker's environment",
				Fix:  "remove the empty entry or name the variable it should have been",
			})
			continue
		case strings.IndexByte(name, 0) >= 0:
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "environment variable name contains a NUL character",
				Why:  "an operating system cannot hold such a name, and the process supervisor refuses to spawn with one",
				Fix:  "remove the NUL character",
			})
			continue
		case strings.Contains(name, "="):
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "environment variable name contains \"=\"",
				Why:  "this list holds variable NAMES only, never NAME=value pairs; a value would be a secret that must never be written into a definition",
				Fix:  "list the bare name; the value is read from the worker's own environment when the process is spawned",
			})
			continue
		case strings.IndexFunc(name, unicode.IsSpace) >= 0:
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("environment variable name %q contains whitespace", name),
				Why:  "names are matched exactly against the operator's --env-allowlist, so a name with whitespace can never match",
				Fix:  "remove the whitespace so the entry is the exact variable name",
			})
			continue
		}
		if seen[name] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("duplicate environment variable name %q", name),
				Why:  "listing the same name twice can never mean anything more than listing it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seen[name] = true
	}
	return diags
}
