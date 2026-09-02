package workflow

import (
	"testing"
	"time"
)

// FuzzCompileJSON_NeverPanics is V2-09's own "malformed corpus"/fuzz
// requirement (docs/design/04-v2-definition-plane.md V2-09's Verify
// line), mirroring internal/domain/authoring's own
// FuzzDecodeStrict_NeverPanics: CompileJSON is the first thing arbitrary,
// untrusted authored graph bytes reach, so no matter how malformed the
// input, it must always return a real error rather than panic — a crash
// here would mean a single malformed workflow document could take down
// whatever process compiles it.
func FuzzCompileJSON_NeverPanics(f *testing.F) {
	seeds := []string{
		`{"schemaVersion":"1","nodes":[],"edges":[]}`,
		`{`,
		`[]`,
		`null`,
		`{"schemaVersion":"1","nodes":[{"key":"a","type":"START"}],"edges":[]}`,
		`{"schemaVersion":"1","nodes":[{"key":"a","type":"FORK","outcomes":["x","y"]}],"edges":[]}`,
		`{"schemaVersion":"1","nodes":[{"key":"a","type":"AGENT","agent":{"profileRef":{"kind":"AGENT_PROFILE","definitionId":"d","versionId":"v"}}}],"edges":[]}`,
		"\x00\x01\x02",
		"",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	definition := WorkflowDefinition{ID: "fuzz-workflow", Name: "Fuzz", Status: DefinitionDraft, Version: 1}
	request := PublishRequest{
		VersionID: "fuzz-workflow-v1", VersionNumber: 1,
		PublishedBy: "fuzz", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	f.Fuzz(func(t *testing.T, rawDocument string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("CompileJSON panicked on input %q: %v", rawDocument, r)
			}
		}()
		_, _ = CompileJSON(definition, []byte(rawDocument), request)
	})
}
