package logging_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
)

const knownSecret = "super-secret-api-key-xyz"

func newTestLogger(buf *bytes.Buffer, format logging.Format) *logging.Logger {
	matcher := redact.NewMatcher(knownSecret)
	return logging.New(buf, format, matcher).WithClock(clock.NewFixed(time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)))
}

// TestLogger_JSON_SecretField_ZeroOccurrences is V1-09's own "secret
// fixture search bằng 0" Verify requirement: a known secret passed as a
// field value must never appear anywhere in the serialized output.
func TestLogger_JSON_SecretField_ZeroOccurrences(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, logging.JSON)

	if err := logger.Info(logging.Identity{}, "did a thing", map[string]any{"token": knownSecret}); err != nil {
		t.Fatalf("Info: %v", err)
	}

	output := buf.String()
	if strings.Contains(output, knownSecret) {
		t.Fatalf("output contains the raw secret: %s", output)
	}
	if !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("output does not contain the redaction placeholder: %s", output)
	}
}

func TestLogger_Text_SecretField_ZeroOccurrences(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, logging.Text)

	if err := logger.Info(logging.Identity{}, "did a thing", map[string]any{"token": knownSecret}); err != nil {
		t.Fatalf("Info: %v", err)
	}

	output := buf.String()
	if strings.Contains(output, knownSecret) {
		t.Fatalf("output contains the raw secret: %s", output)
	}
	if !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("output does not contain the redaction placeholder: %s", output)
	}
}

// TestLogger_MessageThatIsExactlyASecret_Redacted covers the one message
// shape redact.Matcher's exact-match contract actually redacts: a
// message whose entire value equals a known secret. redact.Matcher is
// deliberately never a substring/pattern scanner (see its own package
// doc — "scanning free text for 'looks like a secret' is exactly what
// produces false positives and false negatives"), so a secret embedded
// inside a larger free-text message (e.g. "using token <secret> now")
// is NOT, and by design cannot be, caught this way. A caller must pass a
// secret as a structured field value, never smash it into prose, for
// V1-09's redaction guarantee to apply — the field-value tests above are
// what actually exercise the realistic "secret fixture search bằng 0"
// scenario.
func TestLogger_MessageThatIsExactlyASecret_Redacted(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, logging.JSON)

	if err := logger.Info(logging.Identity{}, knownSecret, nil); err != nil {
		t.Fatalf("Info: %v", err)
	}

	output := buf.String()
	if strings.Contains(output, knownSecret) {
		t.Fatalf("output contains the raw secret: %s", output)
	}
	if !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("output does not contain the redaction placeholder: %s", output)
	}
}

func TestLogger_SecretNestedInFieldValue_ZeroOccurrences(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, logging.JSON)

	nested := map[string]any{
		"request": map[string]any{
			"headers": []any{knownSecret, "content-type: application/json"},
		},
	}
	if err := logger.Warn(logging.Identity{}, "request failed", map[string]any{"detail": nested}); err != nil {
		t.Fatalf("Warn: %v", err)
	}

	output := buf.String()
	if strings.Contains(output, knownSecret) {
		t.Fatalf("output contains the raw secret nested inside a field value: %s", output)
	}
	if !strings.Contains(output, "content-type: application/json") {
		t.Fatalf("output lost a non-secret nested value: %s", output)
	}
}

// TestLogger_CorrelationFieldsPresent_JSON is V1-09's own "correlation
// fields present" Verify requirement.
func TestLogger_CorrelationFieldsPresent_JSON(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, logging.JSON)
	identity := logging.Identity{
		WorkItemID: "wi-1", WorkflowRunID: "run-1", NodeRunID: "node-1", AttemptID: "attempt-1",
		ActorType: "agent", ActorID: "claude", CorrelationID: "corr-1", CausationID: "cause-1",
	}

	if err := logger.Info(identity, "started", nil); err != nil {
		t.Fatalf("Info: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON entry: %v", err)
	}
	gotIdentity, ok := decoded["identity"].(map[string]any)
	if !ok {
		t.Fatalf("decoded entry has no identity object: %+v", decoded)
	}
	want := map[string]string{
		"WorkItemID": "wi-1", "WorkflowRunID": "run-1", "NodeRunID": "node-1", "AttemptID": "attempt-1",
		"ActorType": "agent", "ActorID": "claude", "CorrelationID": "corr-1", "CausationID": "cause-1",
	}
	for field, wantValue := range want {
		if got := gotIdentity[field]; got != wantValue {
			t.Fatalf("identity.%s = %v, want %q", field, got, wantValue)
		}
	}
}

func TestLogger_CorrelationFieldsPresent_Text(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, logging.Text)
	identity := logging.Identity{CorrelationID: "corr-1", WorkItemID: "wi-1"}

	if err := logger.Info(identity, "started", nil); err != nil {
		t.Fatalf("Info: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "correlation_id=corr-1") {
		t.Fatalf("output missing correlation_id: %s", output)
	}
	if !strings.Contains(output, "work_item_id=wi-1") {
		t.Fatalf("output missing work_item_id: %s", output)
	}
}

func TestLogger_IdentityOmittedWhenEmpty(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, logging.JSON)
	if err := logger.Info(logging.Identity{}, "no identity here", nil); err != nil {
		t.Fatalf("Info: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON entry: %v", err)
	}
	if _, present := decoded["identity"]; present {
		t.Fatalf("decoded entry has an identity key even though Identity was entirely empty: %+v", decoded)
	}
}

// TestLogger_Error_UsesTypedFailureCategory is V1-09's own "typed failure
// category" requirement, and proves Logger.Error's signature enforces
// "error safe/public tách raw private cause": there is no parameter here
// a caller could pass a raw error/cause through even by accident.
func TestLogger_Error_UsesTypedFailureCategory(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, logging.JSON)

	if err := logger.Error(logging.Identity{}, apperror.CodeUnavailable, "database busy", nil); err != nil {
		t.Fatalf("Error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON entry: %v", err)
	}
	if decoded["failure_category"] != string(apperror.CodeUnavailable) {
		t.Fatalf("failure_category = %v, want %q", decoded["failure_category"], apperror.CodeUnavailable)
	}
	if decoded["level"] != string(logging.LevelError) {
		t.Fatalf("level = %v, want %q", decoded["level"], logging.LevelError)
	}
}

func TestLogger_NonErrorLevels_OmitFailureCategory(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, logging.JSON)
	if err := logger.Info(logging.Identity{}, "fine", nil); err != nil {
		t.Fatalf("Info: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON entry: %v", err)
	}
	if _, present := decoded["failure_category"]; present {
		t.Fatalf("decoded entry has failure_category on a non-Error level entry: %+v", decoded)
	}
}

func TestLogger_FieldValue_BoundedWhenOversized(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, logging.JSON)
	oversized := strings.Repeat("a", logging.MaxFieldValueBytes+500)

	if err := logger.Info(logging.Identity{}, "big field", map[string]any{"blob": oversized}); err != nil {
		t.Fatalf("Info: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON entry: %v", err)
	}
	fields, ok := decoded["fields"].(map[string]any)
	if !ok {
		t.Fatalf("decoded entry has no fields object: %+v", decoded)
	}
	got, _ := fields["blob"].(string)
	if len(got) > logging.MaxFieldValueBytes {
		t.Fatalf("bounded field length = %d, want <= %d", len(got), logging.MaxFieldValueBytes)
	}
	if !strings.HasSuffix(got, "...(truncated)") {
		t.Fatalf("bounded field does not end with the truncation marker: %q", got[max(0, len(got)-30):])
	}
}

func TestLogger_MessageBoundedWhenOversized(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, logging.JSON)
	oversized := strings.Repeat("m", logging.MaxFieldValueBytes+500)

	if err := logger.Info(logging.Identity{}, oversized, nil); err != nil {
		t.Fatalf("Info: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON entry: %v", err)
	}
	message, _ := decoded["message"].(string)
	if len(message) > logging.MaxFieldValueBytes {
		t.Fatalf("bounded message length = %d, want <= %d", len(message), logging.MaxFieldValueBytes)
	}
}

func TestLogger_WithClock_StampsInjectedTime(t *testing.T) {
	var buf bytes.Buffer
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	matcher := redact.NewMatcher()
	logger := logging.New(&buf, logging.JSON, matcher).WithClock(clock.NewFixed(fixed))

	if err := logger.Info(logging.Identity{}, "stamped", nil); err != nil {
		t.Fatalf("Info: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON entry: %v", err)
	}
	gotTime, err := time.Parse(time.RFC3339Nano, decoded["time"].(string))
	if err != nil {
		t.Fatalf("parse entry time: %v", err)
	}
	if !gotTime.Equal(fixed) {
		t.Fatalf("entry time = %v, want %v", gotTime, fixed)
	}
}

func TestLogger_ConcurrentWrites_DoNotInterleaveOrRace(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf, logging.JSON)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = logger.Info(logging.Identity{}, "concurrent", map[string]any{"n": n})
		}(i)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 20 {
		t.Fatalf("got %d lines, want 20 (writes must not interleave into a corrupted line)", len(lines))
	}
	for _, line := range lines {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("line is not valid JSON (interleaved write): %q: %v", line, err)
		}
	}
}
