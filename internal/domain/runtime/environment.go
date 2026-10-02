package runtime

import (
	"strings"
	"unicode"
)

// IntersectEnvironmentNames returns the sorted, deduplicated set of names
// that appear in BOTH requested and ceiling — V9-05's one rule for forming an
// AGENT process's inherited environment (gap G5): what the pinned
// AgentProfileVersion asks for (requested, its envAllowlist), cut down to
// what the operator allows (ceiling, the `aw worker --env-allowlist` of the
// RuntimeExecutionConfigSnapshotV1 in force).
//
// The match is exact and case-sensitive on purpose — on Windows too, where
// the operating system itself would treat PATH and Path as one variable.
// An author and an operator who want a variable passed write its name the
// same way in both lists; a rule that was "equal on this OS" would make the
// pinned set, and therefore ExecutionProfileHash, depend on which OS
// evaluated it. The ceiling is a hard limit: a name that is not in it is
// never returned, however often or however the profile asks for it. An empty
// requested or an empty ceiling returns nil (nothing is inherited), which is
// also exactly what happened before V9-05. Neither input is modified.
//
// Only names are ever handled here; the values are read from the worker's
// environment by the process supervisor at spawn time and never pass through
// this function, the profile, the hash or any artifact.
func IntersectEnvironmentNames(requested, ceiling []string) []string {
	if len(requested) == 0 || len(ceiling) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(ceiling))
	for _, name := range ceiling {
		allowed[name] = struct{}{}
	}
	var both []string
	for _, name := range requested {
		if _, ok := allowed[name]; ok {
			both = append(both, name)
		}
	}
	return sortedUniqueStrings(both)
}

// validEnvironmentName reports whether name can be an inherited environment
// variable name at all: non-empty, no "=", no NUL, no whitespace. The same
// rule internal/domain/agentprofile applies when a profile is published
// (kept as a local copy: a domain package does not import its sibling for a
// six-line predicate) — NewResolvedExecutionProfileV1 re-checks it so a
// pinned profile can never carry a name the process supervisor would refuse
// to put in a child environment.
func validEnvironmentName(name string) bool {
	return name != "" &&
		strings.IndexByte(name, 0) < 0 &&
		!strings.Contains(name, "=") &&
		strings.IndexFunc(name, unicode.IsSpace) < 0
}
