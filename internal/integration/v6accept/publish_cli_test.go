// publish_cli_test.go is V6-15P's own publishOverride (journey_test.go's
// journey.publish, definitions_test.go's own doc comment on it): the CLI
// transport for the exact same create-validate-publish flow
// publishDefinition already proves over HTTP, so every reusable
// definition-graph builder (publishVerificationWorkflow,
// publishCompletionPolicy, publishReleaseWorkflow) can be replayed through
// `aw definition create/validate/publish` subprocess calls without
// duplicating a single document literal.
package v6accept

import (
	"encoding/json"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// publishDefinitionCLI is publishDefinition's CLI-driven twin: identical
// signature, identical return shape, identical three-step flow (create,
// validate, publish) — only the transport differs. scopePrefix here is
// never an HTTP path segment; only its PRESENCE matters (empty means an
// installation-scoped definition, "/projects/{id}" means project-scoped —
// exactly what publishDefinition's own scopePrefix already encodes), so
// this function pulls the project id back OUT of that same string rather
// than asking every caller to pass it twice.
func (j *journey) publishDefinitionCLI(t *testing.T, scopePrefix string, kind definition.Kind, definitionID, name string, document any) publishedDefinition {
	t.Helper()
	projectID := scopePrefixProjectID(t, scopePrefix, j.projectID)

	createArgs := []string{"definition", "create", "--kind", string(kind)}
	createArgs = append(createArgs, projectFlag(projectID)...)
	createBody, err := json.Marshal(map[string]string{"definitionId": definitionID, "name": name})
	if err != nil {
		t.Fatalf("encode create body: %v", err)
	}
	runCLI(t, j.s, createBody, createArgs...).requireOK(t)

	content, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode %s document: %v", kind, err)
	}

	// Flags before the positional <definitionId> everywhere in this file —
	// Go's flag.FlagSet.Parse (parseFlags, every leaf's own helpers.go)
	// stops consuming flags at the first non-flag argument, so a
	// positional placed BEFORE a flag silently turns that flag into more
	// positional args instead of being parsed.
	validateArgs := append([]string{"definition", "validate", "--kind", string(kind)}, projectFlag(projectID)...)
	validateArgs = append(validateArgs, definitionID)
	runCLI(t, j.s, content, validateArgs...).requireOK(t)

	// --yes: `definition publish` is HighImpact (UX Screen 4 confirm), and
	// this subprocess is never a TTY.
	publishArgs := append([]string{"definition", "publish", "--kind", string(kind), "--yes"}, projectFlag(projectID)...)
	publishArgs = append(publishArgs, definitionID)
	published := runCLI(t, j.s, content, publishArgs...).requireOK(t)
	var version struct {
		ID           string `json:"id"`
		CompiledHash string `json:"compiledHash"`
	}
	published.decodeEnvelope(t, &version)
	if version.ID == "" || version.CompiledHash == "" {
		t.Fatalf("aw definition publish %s %s returned no version id/compiled hash: %s", kind, definitionID, published.stdout)
	}
	return publishedDefinition{definitionID: definitionID, versionID: version.ID, compiledHash: version.CompiledHash}
}

// scopePrefixProjectID recovers the project id publishDefinition's own
// scopePrefix argument encodes ("" or "/projects/{id}"), so the CLI
// transport can pass the SAME scope as a --project-id flag instead of a
// path segment. fallback is used when scopePrefix is the project-scoped
// form but the id embedded in it does not match what the caller already
// knows as j.projectID — that should never happen in practice (every real
// call site builds scopePrefix FROM j.projectID), so a mismatch is a real
// bug in the caller, not a case to silently paper over.
func scopePrefixProjectID(t *testing.T, scopePrefix, fallback string) string {
	t.Helper()
	if scopePrefix == "" {
		return ""
	}
	const prefix = "/projects/"
	if len(scopePrefix) <= len(prefix) || scopePrefix[:len(prefix)] != prefix {
		t.Fatalf("scopePrefixProjectID: %q is not the installation scope (\"\") or a project scope (%q<id>)", scopePrefix, prefix)
	}
	id := scopePrefix[len(prefix):]
	if fallback != "" && id != fallback {
		t.Fatalf("scopePrefixProjectID: scopePrefix names project %q but the journey's own projectID is %q", id, fallback)
	}
	return id
}

// projectFlag returns ["--project-id", id] when id is non-empty, or nil —
// so an installation-scoped call (id == "") omits the flag entirely rather
// than passing --project-id "", which every leaf's own flag parsing (e.g.
// workitem/show.go's "--project-id is required" check) treats as a real,
// present-but-empty value rather than "omitted".
func projectFlag(id string) []string {
	if id == "" {
		return nil
	}
	return []string{"--project-id", id}
}
