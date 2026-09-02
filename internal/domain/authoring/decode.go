package authoring

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Format is the source encoding of one authored document.
type Format int

const (
	FormatYAML Format = iota
	FormatJSON
)

// ambiguousBooleanTokens are the YAML 1.1 shorthand boolean spellings
// (yes/no/on/off/y/n and case variants) a strict decoder rejects even
// though yaml.v3 itself resolves them to true/false by default: an
// author who typed "enabled: on" almost certainly meant something, but
// whether they meant the boolean true or the literal string "on" is
// exactly the kind of ambiguity this package exists to close instead of
// silently guessing.
var ambiguousBooleanTokens = map[string]bool{
	"yes": true, "Yes": true, "YES": true,
	"no": true, "No": true, "NO": true,
	"on": true, "On": true, "ON": true,
	"off": true, "Off": true, "OFF": true,
	"y": true, "Y": true, "n": true, "N": true,
}

// DecodeStrict decodes data (in format) into target, collecting every
// problem it finds — unknown fields, duplicate mapping/object keys, and
// (YAML only) implicit ambiguous boolean shorthand — rather than
// stopping at the first. target must be a pointer to a struct whose
// json/yaml tags name every field the schema allows; anything in data
// that doesn't match one is an unknown-field diagnostic, not a silently
// ignored value.
func DecodeStrict(data []byte, format Format, target any) error {
	var diags Diagnostics
	if format == FormatJSON {
		diags = append(diags, findJSONDuplicateKeys(data)...)
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target); err != nil {
			diags = append(diags, Diagnostic{
				What: "JSON does not match the expected shape",
				Why:  err.Error(),
				Fix:  "check field names and value types against the schema",
			})
		}
	}
	if format == FormatYAML {
		var root yaml.Node
		if err := yaml.Unmarshal(data, &root); err != nil {
			return Diagnostics{{What: "YAML does not parse", Why: err.Error(), Fix: "fix the YAML syntax error at the reported location"}}
		}
		diags = append(diags, findYAMLDuplicateKeys(&root, nil)...)
		diags = append(diags, findYAMLAmbiguousBooleans(&root, nil)...)

		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(target); err != nil {
			diags = append(diags, Diagnostic{
				What: "YAML does not match the expected shape",
				Why:  err.Error(),
				Fix:  "check field names and value types against the schema",
			})
		}
	}
	return diags.AsError()
}

func findJSONDuplicateKeys(data []byte) Diagnostics {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var diags Diagnostics
	var walk func(path []string) error
	walk = func(path []string) error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, isDelim := token.(json.Delim)
		if !isDelim {
			return nil // scalar value: nothing further to walk
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, _ := keyToken.(string)
				if seen[key] {
					diags = append(diags, Diagnostic{
						Path: strings.Join(append(append([]string(nil), path...), key), "."),
						What: fmt.Sprintf("duplicate key %q", key),
						Why:  "a repeated key means the source doesn't uniquely say which value was intended, and different decoders could pick different ones",
						Fix:  fmt.Sprintf("remove the duplicate %q entry, keeping only the one intended", key),
					})
				}
				seen[key] = true
				if err := walk(append(path, key)); err != nil {
					return err
				}
			}
			_, err := decoder.Token() // consume closing '}'
			return err
		case '[':
			index := 0
			for decoder.More() {
				if err := walk(append(path, fmt.Sprintf("[%d]", index))); err != nil {
					return err
				}
				index++
			}
			_, err := decoder.Token() // consume closing ']'
			return err
		}
		return nil
	}
	if err := walk(nil); err != nil {
		return nil // a genuine parse error is already surfaced by the strict Decode call this runs alongside
	}
	return diags
}

func findYAMLDuplicateKeys(node *yaml.Node, path []string) Diagnostics {
	var diags Diagnostics
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			diags = append(diags, findYAMLDuplicateKeys(child, path)...)
		}
	case yaml.MappingNode:
		seen := map[string]bool{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode, valueNode := node.Content[i], node.Content[i+1]
			key := keyNode.Value
			if seen[key] {
				diags = append(diags, Diagnostic{
					Line: keyNode.Line, Column: keyNode.Column,
					Path: strings.Join(append(append([]string(nil), path...), key), "."),
					What: fmt.Sprintf("duplicate key %q", key),
					Why:  "a repeated key means the source doesn't uniquely say which value was intended, and different decoders could pick different ones",
					Fix:  fmt.Sprintf("remove the duplicate %q entry, keeping only the one intended", key),
				})
			}
			seen[key] = true
			diags = append(diags, findYAMLDuplicateKeys(valueNode, append(path, key))...)
		}
	case yaml.SequenceNode:
		for i, child := range node.Content {
			diags = append(diags, findYAMLDuplicateKeys(child, append(path, fmt.Sprintf("[%d]", i)))...)
		}
	}
	return diags
}

func findYAMLAmbiguousBooleans(node *yaml.Node, path []string) Diagnostics {
	var diags Diagnostics
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			diags = append(diags, findYAMLAmbiguousBooleans(child, path)...)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			keyNode, valueNode := node.Content[i], node.Content[i+1]
			diags = append(diags, findYAMLAmbiguousBooleans(valueNode, append(path, keyNode.Value))...)
		}
	case yaml.SequenceNode:
		for i, child := range node.Content {
			diags = append(diags, findYAMLAmbiguousBooleans(child, append(path, fmt.Sprintf("[%d]", i)))...)
		}
	case yaml.ScalarNode:
		// yaml.v3 itself tags a plain "yes"/"no"/"on"/"off"/"y"/"n" as
		// !!str, not !!bool — it follows the YAML 1.2 core schema, which
		// dropped YAML 1.1's expanded boolean set. The ambiguity is real
		// anyway: verified directly against this library that decoding
		// one of these tokens into a Go bool field still succeeds
		// (yaml.v3 loosely coerces on unmarshal even though it resolved
		// the node's own tag as a string), while decoding it into a
		// string field also succeeds — so the same plain token silently
		// means two different things depending on which field it lands
		// in, which is exactly the ambiguity this check exists to
		// surface. Matching on Style (plain vs quoted) rather than Tag
		// is what makes this reliable regardless of that resolution
		// quirk: a quoted "on" is unambiguous and must never be flagged.
		if node.Style == 0 && ambiguousBooleanTokens[node.Value] {
			diags = append(diags, Diagnostic{
				Line: node.Line, Column: node.Column,
				Path: strings.Join(path, "."),
				What: fmt.Sprintf("ambiguous implicit boolean %q", node.Value),
				Why:  "unquoted yes/no/on/off/y/n silently means true/false when decoded into a boolean field but a literal string when decoded into a string field, hiding which one was intended",
				Fix:  `use the explicit "true"/"false" for a boolean, or quote the value (e.g. "on") if a literal string was intended`,
			})
		}
	}
	return diags
}
