// Package redact is Alpha's one shared redactor
// (docs/design/03-v1-alpha-foundation.md V1-02A): every later sink — config
// dump, domain event, command receipt, structured log, artifact metadata —
// must route a value through this contract before it can reach a
// persistence sink, instead of each sink inventing its own scrubbing. Two
// independent mechanisms are combined: an exact-match Matcher against a
// fixed set of known secret values (content-based — catches a real secret
// wherever it appears, even somewhere no one thought to mark sensitive),
// and an explicit Sensitivity a caller attaches to one value via Tagged
// (structural — a field's role is Secret/Sensitive regardless of its
// literal content). Matching is always exact equality, never a
// pattern/regex: scanning free text for "looks like a secret" is exactly
// what produces false positives and false negatives.
package redact

import (
	"errors"
	"fmt"
	"reflect"
)

// Sensitivity classifies how a value must be handled once it might reach a
// persistence sink.
type Sensitivity int

const (
	// Public values pass through redaction unchanged.
	Public Sensitivity = iota
	// Sensitive values are shown as a safe preview, never in full.
	Sensitive
	// Secret values are never shown, not even a preview.
	Secret
)

const (
	placeholder      = "[REDACTED]"
	previewSuffixLen = 4
)

// Matcher is a fixed set of known secret values.
type Matcher struct {
	secrets map[string]struct{}
}

// NewMatcher builds a Matcher from a fixed set of known secret values. An
// empty string is ignored — treating "" as a secret would redact
// everything.
func NewMatcher(secrets ...string) Matcher {
	set := make(map[string]struct{}, len(secrets))
	for _, s := range secrets {
		if s == "" {
			continue
		}
		set[s] = struct{}{}
	}
	return Matcher{secrets: set}
}

// IsSecret reports whether s exactly equals one of the known secret
// values.
func (m Matcher) IsSecret(s string) bool {
	_, ok := m.secrets[s]
	return ok
}

// String returns s unchanged unless it is an exact secret match, in which
// case it returns the placeholder.
func (m Matcher) String(s string) string {
	if m.IsSecret(s) {
		return placeholder
	}
	return s
}

// mask returns the placeholder, or the placeholder plus s's last few
// characters when s is long enough to leave a recognizable tail without
// reconstructing the value from it.
func mask(s string) string {
	if len(s) <= previewSuffixLen {
		return placeholder
	}
	return placeholder + s[len(s)-previewSuffixLen:]
}

// Preview returns a safe preview of s: s itself if it does not match a
// known secret, otherwise a masked tail.
func (m Matcher) Preview(s string) string {
	if !m.IsSecret(s) {
		return s
	}
	return mask(s)
}

// Tagged redacts s according to an explicitly declared Sensitivity,
// independent of whether s happens to match a known secret fixture — the
// structural counterpart to String/Value's content-based matching, for a
// caller that knows a field's role (e.g. "this argument is always a
// password") before any real secret value is known. An exact secret match
// always wins regardless of the declared Sensitivity.
func (m Matcher) Tagged(sensitivity Sensitivity, s string) string {
	switch {
	case sensitivity == Secret || m.IsSecret(s):
		return placeholder
	case sensitivity == Sensitive:
		return mask(s)
	default:
		return s
	}
}

// MaxDepth bounds recursion into nested maps/slices/structs/pointers so a
// pathologically deep input cannot exhaust the stack or hang redaction.
const MaxDepth = 32

// ErrDepthExceeded is returned by Value when a nested structure exceeds
// MaxDepth; the caller must treat the whole value as unsafe to emit rather
// than accept a silently partial redaction.
var ErrDepthExceeded = errors.New("redact: nested value exceeds max depth")

// Value walks v (maps, slices, arrays, structs, pointers, interfaces, and
// combinations of them) and returns a redacted copy: every string that
// exactly matches a known secret becomes the placeholder; everything else
// is copied unchanged. It never mutates v. Unexported struct fields are
// skipped — they are not part of a sink's public output surface.
func (m Matcher) Value(v any) (any, error) {
	return m.redactReflect(reflect.ValueOf(v), 0)
}

func (m Matcher) redactReflect(v reflect.Value, depth int) (any, error) {
	if !v.IsValid() {
		return nil, nil
	}
	if depth > MaxDepth {
		return nil, ErrDepthExceeded
	}
	switch v.Kind() {
	case reflect.String:
		return m.String(v.String()), nil
	case reflect.Ptr, reflect.Interface:
		if v.IsNil() {
			return nil, nil
		}
		return m.redactReflect(v.Elem(), depth+1)
	case reflect.Map:
		if v.IsNil() {
			return nil, nil
		}
		result := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			key := fmt.Sprint(iter.Key().Interface())
			redacted, err := m.redactReflect(iter.Value(), depth+1)
			if err != nil {
				return nil, err
			}
			result[key] = redacted
		}
		return result, nil
	case reflect.Slice, reflect.Array:
		length := v.Len()
		result := make([]any, length)
		for i := 0; i < length; i++ {
			redacted, err := m.redactReflect(v.Index(i), depth+1)
			if err != nil {
				return nil, err
			}
			result[i] = redacted
		}
		return result, nil
	case reflect.Struct:
		t := v.Type()
		result := make(map[string]any, v.NumField())
		for i := 0; i < v.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			redacted, err := m.redactReflect(v.Field(i), depth+1)
			if err != nil {
				return nil, err
			}
			result[field.Name] = redacted
		}
		return result, nil
	default:
		if v.CanInterface() {
			return v.Interface(), nil
		}
		return nil, nil
	}
}
