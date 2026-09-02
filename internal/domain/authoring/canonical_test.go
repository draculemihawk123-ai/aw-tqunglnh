package authoring_test

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"gopkg.in/yaml.v3"
)

// decodeAny gets a plain map[string]any tree out of a JSON or YAML
// fixture for Canonicalize to work on directly — these tests are about
// Canonicalize's own behavior, not strict-decode's (that's
// decode_test.go's job), so this bypasses DecodeStrict's struct-tag
// matching entirely rather than needing a target type for every fixture
// shape used below.
func decodeAny(t *testing.T, data string, format authoring.Format) map[string]any {
	t.Helper()
	var doc map[string]any
	var err error
	if format == authoring.FormatJSON {
		err = json.Unmarshal([]byte(data), &doc)
	} else {
		err = yaml.Unmarshal([]byte(data), &doc)
	}
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return doc
}

func TestCanonicalize_KeyOrderIndependent(t *testing.T) {
	a := decodeAny(t, `{"b":1,"a":2}`, authoring.FormatJSON)
	b := decodeAny(t, `{"a":2,"b":1}`, authoring.FormatJSON)

	_, hashA, err := authoring.Canonicalize(a, authoring.CanonicalizeOptions{})
	if err != nil {
		t.Fatalf("Canonicalize(a): %v", err)
	}
	_, hashB, err := authoring.Canonicalize(b, authoring.CanonicalizeOptions{})
	if err != nil {
		t.Fatalf("Canonicalize(b): %v", err)
	}
	if hashA != hashB {
		t.Fatalf("hashA = %s, hashB = %s, want identical (key order must not affect the hash)", hashA, hashB)
	}
}

// TestCanonicalize_YAMLAndJSON_SameSemanticContent_SameHash is V2-03's
// own core property: "cùng semantic authoring input tạo cùng canonical
// JSON/SourceHash trên Windows/Linux" — proven here for the YAML-vs-JSON
// half of that claim (the CRLF/LF half is
// TestCanonicalize_LineEndingIndependent below).
func TestCanonicalize_YAMLAndJSON_SameSemanticContent_SameHash(t *testing.T) {
	jsonDoc := decodeAny(t, `{"name":"demo","tags":["a","b"],"count":3}`, authoring.FormatJSON)
	yamlDoc := decodeAny(t, "name: demo\ntags: [a, b]\ncount: 3\n", authoring.FormatYAML)

	_, jsonHash, err := authoring.Canonicalize(jsonDoc, authoring.CanonicalizeOptions{})
	if err != nil {
		t.Fatalf("Canonicalize(json): %v", err)
	}
	_, yamlHash, err := authoring.Canonicalize(yamlDoc, authoring.CanonicalizeOptions{})
	if err != nil {
		t.Fatalf("Canonicalize(yaml): %v", err)
	}
	if jsonHash != yamlHash {
		t.Fatalf("jsonHash = %s, yamlHash = %s, want identical for the same semantic content", jsonHash, yamlHash)
	}
}

func TestCanonicalize_LineEndingIndependent(t *testing.T) {
	crlf := map[string]any{"description": "line one\r\nline two"}
	lf := map[string]any{"description": "line one\nline two"}

	_, hashCRLF, err := authoring.Canonicalize(crlf, authoring.CanonicalizeOptions{})
	if err != nil {
		t.Fatalf("Canonicalize(crlf): %v", err)
	}
	_, hashLF, err := authoring.Canonicalize(lf, authoring.CanonicalizeOptions{})
	if err != nil {
		t.Fatalf("Canonicalize(lf): %v", err)
	}
	if hashCRLF != hashLF {
		t.Fatalf("hashCRLF = %s, hashLF = %s, want identical (a platform's line-ending style must not affect the hash)", hashCRLF, hashLF)
	}
}

func TestCanonicalize_SetPathSortsRegardlessOfOrder(t *testing.T) {
	first := map[string]any{"tags": []any{"beta", "alpha", "gamma"}}
	second := map[string]any{"tags": []any{"gamma", "alpha", "beta"}}
	opts := authoring.CanonicalizeOptions{SetPaths: map[string]bool{"tags": true}}

	_, hash1, err := authoring.Canonicalize(first, opts)
	if err != nil {
		t.Fatalf("Canonicalize(first): %v", err)
	}
	_, hash2, err := authoring.Canonicalize(second, opts)
	if err != nil {
		t.Fatalf("Canonicalize(second): %v", err)
	}
	if hash1 != hash2 {
		t.Fatalf("hash1 = %s, hash2 = %s, want identical for a set field regardless of authored order", hash1, hash2)
	}
}

func TestCanonicalize_ListPathPreservesOrder(t *testing.T) {
	first := map[string]any{"nodes": []any{"start", "middle", "end"}}
	second := map[string]any{"nodes": []any{"end", "middle", "start"}}
	// No SetPaths entry for "nodes": it is a list, and reordering a list
	// is a genuine semantic change that must produce a different hash.

	_, hash1, err := authoring.Canonicalize(first, authoring.CanonicalizeOptions{})
	if err != nil {
		t.Fatalf("Canonicalize(first): %v", err)
	}
	_, hash2, err := authoring.Canonicalize(second, authoring.CanonicalizeOptions{})
	if err != nil {
		t.Fatalf("Canonicalize(second): %v", err)
	}
	if hash1 == hash2 {
		t.Fatal("a reordered list field produced the same hash — list order must be preserved as semantically significant")
	}
}

func TestCanonicalize_ExcludePathStripped(t *testing.T) {
	first := map[string]any{"name": "demo", "publishedBy": "alice"}
	second := map[string]any{"name": "demo", "publishedBy": "bob"}
	opts := authoring.CanonicalizeOptions{ExcludePaths: map[string]bool{"publishedBy": true}}

	_, hash1, err := authoring.Canonicalize(first, opts)
	if err != nil {
		t.Fatalf("Canonicalize(first): %v", err)
	}
	_, hash2, err := authoring.Canonicalize(second, opts)
	if err != nil {
		t.Fatalf("Canonicalize(second): %v", err)
	}
	if hash1 != hash2 {
		t.Fatalf("hash1 = %s, hash2 = %s, want identical once publishedBy is excluded", hash1, hash2)
	}

	// Sanity: without the exclusion, the two really would differ.
	_, hash1NoExclude, err := authoring.Canonicalize(first, authoring.CanonicalizeOptions{})
	if err != nil {
		t.Fatalf("Canonicalize(first, no exclude): %v", err)
	}
	_, hash2NoExclude, err := authoring.Canonicalize(second, authoring.CanonicalizeOptions{})
	if err != nil {
		t.Fatalf("Canonicalize(second, no exclude): %v", err)
	}
	if hash1NoExclude == hash2NoExclude {
		t.Fatal("publishedBy differs between first/second but hashes matched without ExcludePaths — the test fixture itself is broken")
	}
}

// TestCanonicalize_Deterministic_RepeatedCalls is V2-03's own "property"
// test: canonicalizing the same tree many times, built from a map with
// enough keys that Go's randomized map iteration order would expose any
// place the implementation accidentally depends on it, must always
// produce byte-identical output.
func TestCanonicalize_Deterministic_RepeatedCalls(t *testing.T) {
	tree := map[string]any{}
	for i := 0; i < 50; i++ {
		tree[randKey(i)] = map[string]any{"value": i, "tags": []any{"z", "a", "m"}}
	}
	opts := authoring.CanonicalizeOptions{SetPaths: map[string]bool{}}

	var firstBytes []byte
	var firstHash string
	for i := 0; i < 20; i++ {
		encoded, hash, err := authoring.Canonicalize(tree, opts)
		if err != nil {
			t.Fatalf("Canonicalize (attempt %d): %v", i, err)
		}
		if i == 0 {
			firstBytes, firstHash = encoded, hash
			continue
		}
		if string(encoded) != string(firstBytes) || hash != firstHash {
			t.Fatalf("attempt %d produced different output than attempt 0 — canonicalization must be deterministic", i)
		}
	}
}

func randKey(i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	return string(letters[i%len(letters)]) + string(letters[(i*7)%len(letters)]) + string(rune('0'+i%10))
}

// TestCanonicalize_PropertyPermutations is V2-03's own "property" test
// requirement from a different angle than determinism-of-repeated-calls
// above: many different random permutations of the same object's key
// order must all canonicalize to the identical hash.
func TestCanonicalize_PropertyPermutations(t *testing.T) {
	baseKeys := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}
	// Each key's value is fixed by the key itself, never by its position
	// in a given permutation — otherwise every permutation would build a
	// genuinely different key->value mapping (a different document, not
	// just a differently-ordered build of the same one), and different
	// hashes would be the correct outcome rather than a bug. This is
	// exactly the mistake an earlier version of this test made.
	fixedValue := make(map[string]int, len(baseKeys))
	for i, key := range baseKeys {
		fixedValue[key] = i
	}
	rng := rand.New(rand.NewSource(42))

	var referenceHash string
	for attempt := 0; attempt < 30; attempt++ {
		shuffled := append([]string(nil), baseKeys...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		tree := map[string]any{}
		for _, key := range shuffled { // build order varies; each key's value never does
			tree[key] = fixedValue[key]
		}
		_, hash, err := authoring.Canonicalize(tree, authoring.CanonicalizeOptions{})
		if err != nil {
			t.Fatalf("Canonicalize (attempt %d): %v", attempt, err)
		}
		if attempt == 0 {
			referenceHash = hash
			continue
		}
		if hash != referenceHash {
			t.Fatalf("permutation %d produced hash %s, want %s (identical content, different key order)", attempt, hash, referenceHash)
		}
	}
}

// TestCanonicalize_Golden is V2-03's own "golden" test requirement: a
// representative authored document's canonical JSON is compared
// byte-for-byte against a checked-in fixture, so a change to
// canonicalization behavior itself (not just its determinism) is caught.
func TestCanonicalize_Golden(t *testing.T) {
	source := `
name: golden-example
tags: [zebra, alpha, mango]
nodes:
  - start
  - middle
  - end
publishedBy: alice
`
	doc := decodeAny(t, source, authoring.FormatYAML)
	got, _, err := authoring.Canonicalize(doc, authoring.CanonicalizeOptions{
		SetPaths:     map[string]bool{"tags": true},
		ExcludePaths: map[string]bool{"publishedBy": true},
	})
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	got = append(got, '\n')

	goldenPath := filepath.Join("testdata", "golden", "example.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("canonical output does not match golden fixture.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
