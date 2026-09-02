package authoring_test

import (
	"encoding/json"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
)

// FuzzDecodeStrict_NeverPanics is V2-03's own "fuzz" test requirement:
// DecodeStrict is the first thing arbitrary, untrusted authored bytes
// reach, so it must never panic no matter how malformed its input is —
// a real error/Diagnostics value is always an acceptable outcome; a
// crash never is.
func FuzzDecodeStrict_NeverPanics(f *testing.F) {
	seeds := []string{
		`{"name":"demo"}`,
		`{"name":"demo","name":"duplicate"}`,
		`{`,
		`[]`,
		`null`,
		"name: demo\n",
		"name: demo\nname: duplicate\n",
		"enabled: yes\n",
		"- a\n- b\n",
		"",
		"\x00\x01\x02",
	}
	for _, seed := range seeds {
		f.Add(seed, 0) // 0 selects FormatJSON below; 1 selects FormatYAML
	}

	f.Fuzz(func(t *testing.T, data string, formatSelector int) {
		format := authoring.FormatJSON
		if formatSelector%2 != 0 {
			format = authoring.FormatYAML
		}
		var target map[string]any
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("DecodeStrict panicked on input %q (format=%v): %v", data, format, r)
			}
		}()
		_ = authoring.DecodeStrict([]byte(data), format, &target)
	})
}

// FuzzCanonicalize_DeterministicAndNoPanic fuzzes Canonicalize with
// arbitrary JSON-decodable trees (built from fuzzed bytes via a JSON
// round trip, since Canonicalize's contract only promises meaningful
// output for a tree — the fuzz target's real job is proving it never
// panics and, when it does succeed, always agrees with itself on a
// second call for the exact same tree.
func FuzzCanonicalize_DeterministicAndNoPanic(f *testing.F) {
	seeds := []string{
		`{"a":1,"b":[1,2,3]}`,
		`{}`,
		`[]`,
		`{"nested":{"a":{"b":{"c":[1,2,3]}}}}`,
		`{"unicode":"héllo wörld 日本語"}`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data string) {
		var tree any
		if err := json.Unmarshal([]byte(data), &tree); err != nil {
			return // not valid JSON; Canonicalize isn't meant to validate, DecodeStrict is
		}
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Canonicalize panicked on input %q: %v", data, r)
			}
		}()
		first, firstHash, err := authoring.Canonicalize(tree, authoring.CanonicalizeOptions{})
		if err != nil {
			return
		}
		second, secondHash, err := authoring.Canonicalize(tree, authoring.CanonicalizeOptions{})
		if err != nil {
			t.Fatalf("Canonicalize succeeded once then failed on the identical tree: %v", err)
		}
		if string(first) != string(second) || firstHash != secondHash {
			t.Fatalf("Canonicalize produced different output for two calls on the identical tree: %q vs %q", first, second)
		}
	})
}
