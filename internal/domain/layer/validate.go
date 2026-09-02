package layer

import (
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
)

// ValidateDocument checks doc against every rule HE-04-M02/M03/M04 and
// HE-03-M06 require, collecting every problem found rather than stopping
// at the first — an author fixing a Layer from scratch needs the whole
// picture in one pass. This mirrors internal/domain/skill.ValidateDocument
// exactly (see that package's doc comment for why the two stay separate
// packages despite the shared shape).
func ValidateDocument(doc LayerDocument) authoring.Diagnostics {
	var diags authoring.Diagnostics

	if len(doc.Resources) == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "resources",
			What: "no resources declared",
			Why:  "a Layer that publishes nothing can never be selected for anything",
			Fix:  "add at least one resource",
		})
	}

	seenKeys := make(map[string]bool, len(doc.Resources))
	for i, resource := range doc.Resources {
		path := fmt.Sprintf("resources[%d]", i)
		diags = append(diags, validateResource(path, resource)...)

		key := strings.TrimSpace(resource.Key)
		if key != "" {
			if seenKeys[key] {
				diags = append(diags, authoring.Diagnostic{
					Path: path + ".key",
					What: fmt.Sprintf("duplicate resource key %q", key),
					Why:  "a resource key must be unique within its document — ADR-012's owner_version_id + resource_key + content_hash identity is meaningless if resource_key isn't unique inside the version it belongs to",
					Fix:  "give this resource a distinct key",
				})
			}
			seenKeys[key] = true
		}
	}

	return diags
}

func validateResource(path string, resource Resource) authoring.Diagnostics {
	var diags authoring.Diagnostics

	if strings.TrimSpace(resource.Key) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: path + ".key",
			What: "resource key is empty",
			Why:  "a resource with no key can never be named by ADR-012's owner_version_id + resource_key + content_hash identity",
			Fix:  "set key to a stable, unique name for this resource",
		})
	}

	if strings.TrimSpace(resource.Convention) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: path + ".convention",
			What: "convention is empty",
			Why:  "a resource with no convention content has nothing for an attempt to load or follow",
			Fix:  "set convention to the text this resource should contribute",
		})
	}

	if !resource.Priority.Valid() {
		diags = append(diags, authoring.Diagnostic{
			Path: path + ".priority",
			What: fmt.Sprintf("unsupported priority %q", resource.Priority),
			Why:  "HE-04-M03 requires every resource to declare exactly one of HARD_CONSTRAINT, REQUIRED_PROCEDURE, GUIDANCE or REFERENCE so a resolver can tell an invariant from advice",
			Fix:  "set priority to HARD_CONSTRAINT, REQUIRED_PROCEDURE, GUIDANCE or REFERENCE",
		})
	}

	diags = append(diags, validateSelector(path+".selector", resource.Selector, resource.Global)...)
	diags = append(diags, validateProvenance(path+".provenance", resource.Provenance)...)

	return diags
}

func validateSelector(path string, selector Selector, global bool) authoring.Diagnostics {
	var diags authoring.Diagnostics

	dimensions := []struct {
		name   string
		values []string
	}{
		{"componentTags", selector.ComponentTags},
		{"pathTags", selector.PathTags},
		{"taskKinds", selector.TaskKinds},
		{"blockKinds", selector.BlockKinds},
		{"riskClasses", selector.RiskClasses},
	}

	populated := false
	for _, dimension := range dimensions {
		if len(dimension.values) > 0 {
			populated = true
		}
		seen := make(map[string]bool, len(dimension.values))
		for i, value := range dimension.values {
			valuePath := fmt.Sprintf("%s.%s[%d]", path, dimension.name, i)
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				diags = append(diags, authoring.Diagnostic{
					Path: valuePath,
					What: "selector value is empty",
					Why:  "an empty tag can never be matched against a real task/attempt context",
					Fix:  "remove the empty entry or name the tag it should have been",
				})
				continue
			}
			if seen[trimmed] {
				diags = append(diags, authoring.Diagnostic{
					Path: valuePath,
					What: fmt.Sprintf("duplicate selector value %q", trimmed),
					Why:  "listing the same tag twice can never mean anything more than listing it once",
					Fix:  "remove the duplicate entry",
				})
			}
			seen[trimmed] = true
		}
	}

	if !global && !populated {
		diags = append(diags, authoring.Diagnostic{
			Path: path,
			What: "non-global resource has no selector populated",
			Why:  "HE-04-M02 requires every non-global resource to declare when it applies via component/path tag, task kind, block kind or risk class",
			Fix:  "populate at least one selector dimension, or set global: true if this resource is genuinely meant to apply everywhere",
		})
	}

	return diags
}

func validateProvenance(path string, provenance Provenance) authoring.Diagnostics {
	var diags authoring.Diagnostics

	if strings.TrimSpace(provenance.Owner) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: path + ".owner",
			What: "provenance owner is empty",
			Why:  "HE-03-M06/HE-04-M04 require every resource to have an accountable owner — an un-owned rule is never revisited when it goes stale",
			Fix:  "set provenance.owner to who is accountable for this resource staying correct",
		})
	}

	if strings.TrimSpace(provenance.Source) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: path + ".source",
			What: "provenance source is empty",
			Why:  "HE-03-M06 requires a resource to have a traceable source, not just an owner's say-so",
			Fix:  "set provenance.source to where this resource's content actually comes from",
		})
	}

	if provenance.LastVerified == nil && strings.TrimSpace(provenance.Revision) == "" {
		diags = append(diags, authoring.Diagnostic{
			Path: path,
			What: "provenance has neither lastVerified nor revision set",
			Why:  "HE-03-M06/HE-04-M04 require a review/expiry condition — last_verified or an equivalent revision anchor — so a stale resource can eventually be detected",
			Fix:  "set provenance.lastVerified to when this was last confirmed accurate, or provenance.revision to the equivalent revision anchor it tracks",
		})
	}

	return diags
}
