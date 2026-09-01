package redact

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestStringExactMatch(t *testing.T) {
	m := NewMatcher("sk-live-abc123", "hunter2")
	cases := map[string]string{
		"sk-live-abc123": placeholder,
		"hunter2":         placeholder,
		"public value":    "public value",
	}
	for input, want := range cases {
		if got := m.String(input); got != want {
			t.Errorf("String(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestFalsePositiveAllowlist proves the matcher only ever fires on an
// exact match: a value that merely contains, prefixes, or suffixes a real
// secret must never be redacted, since that is exactly the false-positive
// failure mode a pattern/substring matcher would introduce.
func TestFalsePositiveAllowlist(t *testing.T) {
	m := NewMatcher("hunter2")
	allowlist := []string{
		"hunter2 is a common password",
		"prefix-hunter2",
		"hunter2-suffix",
		"Hunter2",
		"hunter",
		"",
	}
	for _, s := range allowlist {
		if m.IsSecret(s) {
			t.Errorf("IsSecret(%q) = true, want false (not an exact match)", s)
		}
		if got := m.String(s); got != s {
			t.Errorf("String(%q) = %q, want unchanged %q", s, got, s)
		}
	}
}

func TestEmptySecretNeverMatches(t *testing.T) {
	m := NewMatcher("", "real-secret")
	if m.IsSecret("") {
		t.Fatal("an empty secret fixture must never make \"\" itself redactable")
	}
	if got := m.String("anything"); got != "anything" {
		t.Fatalf("String(%q) = %q, want unchanged", "anything", got)
	}
}

func TestPreview(t *testing.T) {
	m := NewMatcher("sk-live-abcdef1234")
	if got := m.Preview("public"); got != "public" {
		t.Errorf("Preview of a non-secret should be unchanged, got %q", got)
	}
	got := m.Preview("sk-live-abcdef1234")
	if !strings.HasPrefix(got, placeholder) {
		t.Fatalf("Preview(%q) = %q, want it to start with %q", "sk-live-abcdef1234", got, placeholder)
	}
	if strings.Contains(got, "sk-live-abcdef") {
		t.Fatalf("Preview(%q) = %q leaked more than the safe tail", "sk-live-abcdef1234", got)
	}
	if !strings.HasSuffix(got, "1234") {
		t.Fatalf("Preview(%q) = %q, want it to end with the last 4 characters", "sk-live-abcdef1234", got)
	}
}

func TestPreviewShortSecretIsFullyMasked(t *testing.T) {
	m := NewMatcher("abc")
	got := m.Preview("abc")
	if got != placeholder {
		t.Fatalf("Preview of a secret no longer than the preview tail should be the bare placeholder, got %q", got)
	}
}

func TestTaggedSecretSensitivityAlwaysRedactsRegardlessOfContent(t *testing.T) {
	m := NewMatcher() // no known secret fixtures at all
	got := m.Tagged(Secret, "whatever-this-value-is")
	if got != placeholder {
		t.Fatalf("Tagged(Secret, ...) = %q, want %q even with no matching fixture", got, placeholder)
	}
}

func TestTaggedSensitiveMasksEvenWithoutFixtureMatch(t *testing.T) {
	m := NewMatcher() // this exact value is not a known secret fixture
	got := m.Tagged(Sensitive, "field-value-1234")
	if got == "field-value-1234" {
		t.Fatal("Tagged(Sensitive, ...) must not return the value unmasked just because it isn't in the fixture set")
	}
	if !strings.HasPrefix(got, placeholder) {
		t.Fatalf("Tagged(Sensitive, ...) = %q, want it to start with %q", got, placeholder)
	}
}

func TestTaggedPublicPassesThrough(t *testing.T) {
	m := NewMatcher()
	if got := m.Tagged(Public, "anything"); got != "anything" {
		t.Fatalf("Tagged(Public, ...) = %q, want unchanged", got)
	}
}

func TestTaggedExactMatchWinsOverPublicTag(t *testing.T) {
	m := NewMatcher("real-secret")
	got := m.Tagged(Public, "real-secret")
	if got != placeholder {
		t.Fatalf("Tagged(Public, ...) on a value that exactly matches a known secret must still redact, got %q", got)
	}
}

// --- nested value redaction ---

func TestValue_NestedMapsAndSlices(t *testing.T) {
	m := NewMatcher("s3cr3t")
	input := map[string]any{
		"safe": "hello",
		"nested": map[string]any{
			"password": "s3cr3t",
			"list":     []any{"a", "s3cr3t", "b"},
		},
	}
	got, err := m.Value(input)
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	encoded := fmt.Sprint(got)
	if strings.Contains(encoded, "s3cr3t") {
		t.Fatalf("redacted value still contains the secret: %v", got)
	}
	if !strings.Contains(encoded, "hello") {
		t.Fatalf("redacted value lost a non-secret string: %v", got)
	}
}

type configFixture struct {
	Name     string
	Password string
	unexported string //nolint:unused // exercising that unexported fields are skipped
}

func TestValue_StructFieldsAndUnexportedSkipped(t *testing.T) {
	m := NewMatcher("s3cr3t")
	input := configFixture{Name: "svc", Password: "s3cr3t", unexported: "s3cr3t"}
	got, err := m.Value(input)
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	resultMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("Value(struct) = %T, want map[string]any", got)
	}
	if resultMap["Name"] != "svc" {
		t.Errorf("Name = %v, want svc", resultMap["Name"])
	}
	if resultMap["Password"] != placeholder {
		t.Errorf("Password = %v, want %q", resultMap["Password"], placeholder)
	}
	if _, present := resultMap["unexported"]; present {
		t.Errorf("unexported field should not appear in the redacted output at all, got %v", resultMap)
	}
}

func TestValue_PointerAndNil(t *testing.T) {
	m := NewMatcher("s3cr3t")
	secret := "s3cr3t"
	got, err := m.Value(&secret)
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	if got != placeholder {
		t.Fatalf("Value(*string secret) = %v, want %q", got, placeholder)
	}

	var nilPtr *string
	got, err = m.Value(nilPtr)
	if err != nil {
		t.Fatalf("Value(nil pointer): %v", err)
	}
	if got != nil {
		t.Fatalf("Value(nil pointer) = %v, want nil", got)
	}
}

func TestValue_TopLevelNilIsNil(t *testing.T) {
	m := NewMatcher("s3cr3t")
	got, err := m.Value(nil)
	if err != nil {
		t.Fatalf("Value(nil): %v", err)
	}
	if got != nil {
		t.Fatalf("Value(nil) = %v, want nil", got)
	}
}

// --- argv / env / error-detail shaped inputs (the sinks V1-09/V1-02's
// apperror.Details are expected to route through this same contract) ---

func TestValue_Argv(t *testing.T) {
	m := NewMatcher("hunter2")
	argv := []string{"agentkit", "login", "--password", "hunter2"}
	got, err := m.Value(argv)
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	result, ok := got.([]any)
	if !ok || len(result) != 4 {
		t.Fatalf("Value(argv) = %v (%T), want a 4-element slice", got, got)
	}
	if result[3] != placeholder {
		t.Errorf("argv[3] = %v, want %q", result[3], placeholder)
	}
	if result[1] != "login" {
		t.Errorf("argv[1] = %v, want unchanged \"login\"", result[1])
	}
}

func TestValue_Env(t *testing.T) {
	m := NewMatcher("s3cr3t-token")
	env := map[string]string{
		"PATH":        "/usr/bin",
		"API_TOKEN":   "s3cr3t-token",
	}
	got, err := m.Value(env)
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	result, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("Value(env) = %T, want map[string]any", got)
	}
	if result["API_TOKEN"] != placeholder {
		t.Errorf("API_TOKEN = %v, want %q", result["API_TOKEN"], placeholder)
	}
	if result["PATH"] != "/usr/bin" {
		t.Errorf("PATH = %v, want unchanged", result["PATH"])
	}
}

func TestValue_ErrorDetailMap(t *testing.T) {
	m := NewMatcher("conn-string-with-password")
	details := map[string]string{
		"operation": "connect",
		"dsn":       "conn-string-with-password",
	}
	got, err := m.Value(details)
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	result := got.(map[string]any)
	if result["dsn"] != placeholder {
		t.Errorf("dsn = %v, want %q", result["dsn"], placeholder)
	}
	if result["operation"] != "connect" {
		t.Errorf("operation = %v, want unchanged", result["operation"])
	}
}

// --- bounded recursion ---

func TestValue_MaxDepthExceeded(t *testing.T) {
	// Build a linked chain of maps deeper than MaxDepth.
	var innermost any = "leaf"
	for i := 0; i < MaxDepth+5; i++ {
		innermost = map[string]any{"next": innermost}
	}
	m := NewMatcher()
	_, err := m.Value(innermost)
	if !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("Value on a structure deeper than MaxDepth = %v, want ErrDepthExceeded", err)
	}
}

func TestValue_WithinMaxDepthSucceeds(t *testing.T) {
	var innermost any = "leaf"
	for i := 0; i < MaxDepth-2; i++ {
		innermost = map[string]any{"next": innermost}
	}
	m := NewMatcher()
	_, err := m.Value(innermost)
	if err != nil {
		t.Fatalf("Value within MaxDepth should succeed, got error: %v", err)
	}
}

func TestValue_DoesNotMutateInput(t *testing.T) {
	m := NewMatcher("s3cr3t")
	original := map[string]string{"password": "s3cr3t"}
	snapshot := map[string]string{"password": "s3cr3t"}
	if _, err := m.Value(original); err != nil {
		t.Fatalf("Value: %v", err)
	}
	if !reflect.DeepEqual(original, snapshot) {
		t.Fatalf("Value mutated its input: got %v, want unchanged %v", original, snapshot)
	}
}
