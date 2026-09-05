package policy

import (
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
)

// ValidateDocument checks doc against every rule this package's own doc
// comments require, collecting every problem found rather than stopping
// at the first — mirrors internal/domain/block.ValidateDocument's own
// collect-everything convention.
func ValidateDocument(doc PolicyDocument) authoring.Diagnostics {
	var diags authoring.Diagnostics

	if !doc.Category.Valid() {
		diags = append(diags, authoring.Diagnostic{
			Path: "category",
			What: fmt.Sprintf("unsupported category %q", doc.Category),
			Why:  "a Policy must declare exactly which of the five semantics (ATTEMPT, COMPLETION, PERMISSION, CONTEXT, CLEANUP) it governs so consumers know which rules apply",
			Fix:  "set category to one of ATTEMPT, COMPLETION, PERMISSION, CONTEXT, CLEANUP",
		})
		return diags // no category to check rule-shape agreement against
	}

	present := map[Category]bool{
		CategoryAttempt:    doc.Attempt != nil,
		CategoryCompletion: doc.Completion != nil,
		CategoryPermission: doc.Permission != nil,
		CategoryContext:    doc.Context != nil,
		CategoryCleanup:    doc.Cleanup != nil,
	}
	for category, isPresent := range present {
		path := strings.ToLower(string(category))
		switch {
		case category == doc.Category && !isPresent:
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("category is %s but %s rules are missing", doc.Category, path),
				Why:  "a Policy's own semantics are only Chỉ semantics tương ứng (only its declared category's rules) — without them there is nothing for a consumer to apply",
				Fix:  fmt.Sprintf("set the %s field with this Policy's rules", path),
			})
		case category != doc.Category && isPresent:
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("category is %s but %s rules are also set", doc.Category, path),
				Why:  "a Policy governs exactly one category's semantics (Chỉ semantics tương ứng) — setting another category's rules alongside it is ambiguous about which one actually applies",
				Fix:  fmt.Sprintf("remove the %s field, or change category to %s if that was the intended semantics", path, category),
			})
		}
	}

	switch doc.Category {
	case CategoryAttempt:
		if doc.Attempt != nil {
			diags = append(diags, validateAttemptRules(*doc.Attempt)...)
		}
	case CategoryCompletion:
		if doc.Completion != nil {
			diags = append(diags, validateCompletionRules(*doc.Completion)...)
		}
	case CategoryPermission:
		if doc.Permission != nil {
			diags = append(diags, validatePermissionRules(*doc.Permission)...)
		}
	case CategoryContext:
		if doc.Context != nil {
			diags = append(diags, validateContextRules(*doc.Context)...)
		}
	case CategoryCleanup:
		if doc.Cleanup != nil {
			diags = append(diags, validateCleanupRules(*doc.Cleanup)...)
		}
	}

	return diags
}

func validateAttemptRules(rules AttemptRules) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if rules.MaxAttempts == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "attempt.maxAttempts",
			What: "max attempts is zero",
			Why:  "a technical retry budget of zero can never create even the first attempt",
			Fix:  "set attempt.maxAttempts to a positive number",
		})
	}
	if rules.TimeoutSeconds == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "attempt.timeoutSeconds",
			What: "timeout is zero",
			Why:  "an attempt with no timeout can run forever, starving whatever budget the run is under",
			Fix:  "set attempt.timeoutSeconds to a positive number of seconds",
		})
	}
	if rules.BackoffSeconds == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "attempt.backoffSeconds",
			What: "backoff is zero",
			Why:  "a zero backoff retries immediately with no delay, which can hammer a struggling dependency instead of giving it room to recover",
			Fix:  "set attempt.backoffSeconds to a positive number of seconds",
		})
	}

	seen := make(map[errorcode.Code]bool, len(rules.RetryableErrorCodes))
	for i, code := range rules.RetryableErrorCodes {
		path := fmt.Sprintf("attempt.retryableErrorCodes[%d]", i)
		trimmed := errorcode.Code(strings.TrimSpace(string(code)))
		if trimmed == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "retryable error code is empty",
				Why:  "an empty code can never match a real AppError.Code at retry time",
				Fix:  "remove the empty entry or name the error code it should have been",
			})
			continue
		}
		if !trimmed.Valid() {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("unknown error code %q", trimmed),
				Why:  "a retry decision keys off an exact AppError.Code from the closed set go-core-spec.md §18 defines; a code outside that set could never actually match a real failure",
				Fix:  "use one of the AppError codes go-core-spec.md §18 defines",
			})
			continue
		}
		if trimmed.NeverRetryable() {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("%q is never retryable", trimmed),
				Why:  "go-core-spec.md §14/§18 explicitly forbids treating this code as retryable technical failure — it is fail-closed admission behavior or a state that always needs recovery/escalation instead",
				Fix:  "remove this code from retryableErrorCodes",
			})
			continue
		}
		if seen[trimmed] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("duplicate retryable error code %q", trimmed),
				Why:  "listing the same code twice can never mean anything more than listing it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seen[trimmed] = true
	}
	return diags
}

func validateCompletionRules(rules CompletionRules) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if len(rules.RequiredEvidenceKinds) == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "completion.requiredEvidenceKinds",
			What: "no required evidence kinds declared",
			Why:  "GC-INV-12/13 require completion policy and evidence to decide PASS together — a completion policy that requires nothing can never distinguish NOT_RUN from a real pass",
			Fix:  "declare at least one required Evidence.Kind",
		})
	}
	seen := make(map[string]bool, len(rules.RequiredEvidenceKinds))
	for i, kind := range rules.RequiredEvidenceKinds {
		path := fmt.Sprintf("completion.requiredEvidenceKinds[%d]", i)
		trimmed := strings.TrimSpace(kind)
		if trimmed == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "required evidence kind is empty",
				Why:  "an empty kind can never be matched against a real Evidence.Kind",
				Fix:  "remove the empty entry or name the evidence kind it should have been",
			})
			continue
		}
		if seen[trimmed] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("duplicate required evidence kind %q", trimmed),
				Why:  "listing the same evidence kind twice can never mean anything more than listing it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seen[trimmed] = true
	}
	return diags
}

func validatePermissionRules(rules PermissionRules) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if !validIsolationTiers[rules.IsolationTier] {
		diags = append(diags, authoring.Diagnostic{
			Path: "permission.isolationTier",
			What: fmt.Sprintf("unsupported isolation tier %q", rules.IsolationTier),
			Why:  "ADR-013 defines exactly two trust tiers; anything else cannot be enforced or displayed correctly at execution time",
			Fix:  "set permission.isolationTier to ENFORCED_ISOLATED or OPERATOR_TRUSTED_LOCAL",
		})
	}
	seen := make(map[string]bool, len(rules.GrantedCapabilities))
	for i, capability := range rules.GrantedCapabilities {
		path := fmt.Sprintf("permission.grantedCapabilities[%d]", i)
		trimmed := strings.TrimSpace(capability)
		if trimmed == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "granted capability is empty",
				Why:  "an empty capability name can never be checked against a real requirement",
				Fix:  "remove the empty entry or name the capability it should have been",
			})
			continue
		}
		if seen[trimmed] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("duplicate granted capability %q", trimmed),
				Why:  "listing the same capability twice can never mean anything more than listing it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seen[trimmed] = true
	}
	return diags
}

func validateContextRules(rules ContextRules) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if len(rules.Selector) == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "context.selector",
			What: "no selector declared",
			Why:  "a context route with no selector can never resolve to any resource to assemble",
			Fix:  "declare at least one resource-key selector pattern",
		})
	}
	seen := make(map[string]bool, len(rules.Selector))
	for i, pattern := range rules.Selector {
		path := fmt.Sprintf("context.selector[%d]", i)
		trimmed := strings.TrimSpace(pattern)
		if trimmed == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "selector pattern is empty",
				Why:  "an empty pattern can never be matched against a real resource key",
				Fix:  "remove the empty entry or name the pattern it should have been",
			})
			continue
		}
		if seen[trimmed] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("duplicate selector pattern %q", trimmed),
				Why:  "listing the same pattern twice can never mean anything more than listing it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seen[trimmed] = true
	}

	seenOrder := make(map[string]bool, len(rules.Order))
	for i, key := range rules.Order {
		path := fmt.Sprintf("context.order[%d]", i)
		trimmed := strings.TrimSpace(key)
		if trimmed == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "order entry is empty",
				Why:  "an empty entry names nothing to sequence",
				Fix:  "remove the empty entry or name the resource key it should have been",
			})
			continue
		}
		if seenOrder[trimmed] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: fmt.Sprintf("duplicate order entry %q", trimmed),
				Why:  "a resource can only occupy one position in the assembly sequence — listing it twice makes the sequence ambiguous",
				Fix:  "remove the duplicate entry",
			})
		}
		seenOrder[trimmed] = true
	}

	if rules.Budget.MaxTokens == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "context.budget.maxTokens",
			What: "max tokens is zero",
			Why:  "a zero token budget can never assemble any context content, making the route useless",
			Fix:  "set context.budget.maxTokens to a positive number",
		})
	}

	seenRef := make(map[ResourceRef]bool, len(rules.ResourceRefs))
	for i, ref := range rules.ResourceRefs {
		path := fmt.Sprintf("context.resourceRefs[%d]", i)
		if strings.TrimSpace(ref.OwnerVersionID) == "" || strings.TrimSpace(ref.ResourceKey) == "" || strings.TrimSpace(ref.ContentHash) == "" {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "resource ref is missing ownerVersionId, resourceKey or contentHash",
				Why:  "ADR-012's resource identity is the full owner_version_id + resource_key + content_hash tuple — a partial tuple can never resolve to a real pinned resource",
				Fix:  "set ownerVersionId, resourceKey and contentHash on the resource ref",
			})
			continue
		}
		if seenRef[ref] {
			diags = append(diags, authoring.Diagnostic{
				Path: path,
				What: "duplicate resource ref",
				Why:  "pinning the same resource identity twice can never mean anything more than pinning it once",
				Fix:  "remove the duplicate entry",
			})
		}
		seenRef[ref] = true
	}

	return diags
}

func validateCleanupRules(rules CleanupRules) authoring.Diagnostics {
	var diags authoring.Diagnostics
	if rules.RetentionDays == 0 {
		diags = append(diags, authoring.Diagnostic{
			Path: "cleanup.retentionDays",
			What: "retention days is zero",
			Why:  "a zero retention window is not a meaningful override of the platform default (go-core-spec.md §19's 7-day default)",
			Fix:  "set cleanup.retentionDays to a positive number of days",
		})
	}
	return diags
}
