package authoring_test

import (
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
)

type testNested struct {
	Value string `json:"value" yaml:"value"`
}

type testDocument struct {
	Name        string      `json:"name" yaml:"name"`
	Tags        []string    `json:"tags,omitempty" yaml:"tags,omitempty"`
	Nested      *testNested `json:"nested,omitempty" yaml:"nested,omitempty"`
	Enabled     bool        `json:"enabled,omitempty" yaml:"enabled,omitempty"`
	PublishedBy string      `json:"publishedBy,omitempty" yaml:"publishedBy,omitempty"`
}

func TestDecodeStrict_JSON_ValidInputSucceeds(t *testing.T) {
	var doc testDocument
	err := authoring.DecodeStrict([]byte(`{"name":"demo","tags":["a","b"]}`), authoring.FormatJSON, &doc)
	if err != nil {
		t.Fatalf("DecodeStrict: %v", err)
	}
	if doc.Name != "demo" || len(doc.Tags) != 2 {
		t.Fatalf("doc = %+v, want Name=demo Tags=[a b]", doc)
	}
}

func TestDecodeStrict_JSON_RejectsUnknownField(t *testing.T) {
	var doc testDocument
	err := authoring.DecodeStrict([]byte(`{"name":"demo","typo_field":"x"}`), authoring.FormatJSON, &doc)
	if err == nil {
		t.Fatal("DecodeStrict should reject an unknown field")
	}
}

func TestDecodeStrict_JSON_RejectsDuplicateKey(t *testing.T) {
	var doc testDocument
	err := authoring.DecodeStrict([]byte(`{"name":"first","name":"second"}`), authoring.FormatJSON, &doc)
	if err == nil {
		t.Fatal("DecodeStrict should reject a duplicate key")
	}
	if !strings.Contains(err.Error(), `duplicate key "name"`) {
		t.Fatalf("err = %v, want it to mention the duplicate key", err)
	}
}

func TestDecodeStrict_JSON_RejectsNestedDuplicateKey(t *testing.T) {
	var doc testDocument
	err := authoring.DecodeStrict([]byte(`{"name":"demo","nested":{"value":"a","value":"b"}}`), authoring.FormatJSON, &doc)
	if err == nil {
		t.Fatal("DecodeStrict should reject a nested duplicate key")
	}
	if !strings.Contains(err.Error(), "nested.value") {
		t.Fatalf("err = %v, want it to include the nested path nested.value", err)
	}
}

func TestDecodeStrict_YAML_ValidInputSucceeds(t *testing.T) {
	var doc testDocument
	err := authoring.DecodeStrict([]byte("name: demo\ntags: [a, b]\n"), authoring.FormatYAML, &doc)
	if err != nil {
		t.Fatalf("DecodeStrict: %v", err)
	}
	if doc.Name != "demo" || len(doc.Tags) != 2 {
		t.Fatalf("doc = %+v, want Name=demo Tags=[a b]", doc)
	}
}

func TestDecodeStrict_YAML_RejectsUnknownField(t *testing.T) {
	var doc testDocument
	err := authoring.DecodeStrict([]byte("name: demo\ntypo_field: x\n"), authoring.FormatYAML, &doc)
	if err == nil {
		t.Fatal("DecodeStrict should reject an unknown field")
	}
}

func TestDecodeStrict_YAML_RejectsDuplicateKey(t *testing.T) {
	var doc testDocument
	err := authoring.DecodeStrict([]byte("name: first\nname: second\n"), authoring.FormatYAML, &doc)
	if err == nil {
		t.Fatal("DecodeStrict should reject a duplicate key")
	}
	if !strings.Contains(err.Error(), `duplicate key "name"`) {
		t.Fatalf("err = %v, want it to mention the duplicate key", err)
	}
}

func TestDecodeStrict_YAML_RejectsAmbiguousBoolean(t *testing.T) {
	cases := []string{"yes", "no", "on", "off", "y", "n", "Yes", "OFF"}
	for _, token := range cases {
		t.Run(token, func(t *testing.T) {
			var doc testDocument
			err := authoring.DecodeStrict([]byte("name: demo\nenabled: "+token+"\n"), authoring.FormatYAML, &doc)
			if err == nil {
				t.Fatalf("DecodeStrict should reject ambiguous boolean shorthand %q", token)
			}
			if !strings.Contains(err.Error(), "ambiguous implicit boolean") {
				t.Fatalf("err = %v, want it to mention the ambiguous boolean", err)
			}
		})
	}
}

func TestDecodeStrict_YAML_AcceptsExplicitBoolean(t *testing.T) {
	var doc testDocument
	err := authoring.DecodeStrict([]byte("name: demo\nenabled: true\n"), authoring.FormatYAML, &doc)
	if err != nil {
		t.Fatalf("DecodeStrict should accept explicit true/false: %v", err)
	}
	if !doc.Enabled {
		t.Fatal("doc.Enabled = false, want true")
	}
}

func TestDecodeStrict_YAML_AcceptsQuotedAmbiguousToken(t *testing.T) {
	// A quoted "on" is unambiguously a string, not the shorthand for
	// true — DecodeStrict must not flag it just because the token
	// happens to match one of the shorthand spellings.
	type stringField struct {
		Mode string `json:"mode" yaml:"mode"`
	}
	var doc stringField
	err := authoring.DecodeStrict([]byte(`mode: "on"`+"\n"), authoring.FormatYAML, &doc)
	if err != nil {
		t.Fatalf("DecodeStrict should accept a quoted ambiguous token: %v", err)
	}
	if doc.Mode != "on" {
		t.Fatalf("doc.Mode = %q, want %q", doc.Mode, "on")
	}
}
