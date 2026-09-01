// Package logging is Alpha's structured diagnostic log pipeline
// (docs/design/03-v1-alpha-foundation.md V1-09, HE-11-M01/HE-11-M07).
// This is deliberately NOT a domain event or evidence stream: nothing
// ever reads a logged Entry back to decide application state or prove a
// gate passed — internal/app/ports' EventsRepository/outbox (V1-06/
// V1-07) is the harness's only durable, replayable record of what
// happened. A Logger exists purely so an operator/developer can diagnose
// a run, with the same correlation identity the domain events for that
// run carry, and with V1-02A's redactor mandatorily applied before
// anything reaches a sink.
package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
)

// Level is a log entry's severity.
type Level string

const (
	LevelDebug Level = "DEBUG"
	LevelInfo  Level = "INFO"
	LevelWarn  Level = "WARN"
	LevelError Level = "ERROR"
)

// Format selects the local sink's serialization.
type Format int

const (
	// JSON writes one JSON object per line (machine-parseable).
	JSON Format = iota
	// Text writes one human-readable line per entry, for local
	// development. HE-11-S02 (export to OpenTelemetry/a standard
	// observability system) is explicitly a beta concern; both of these
	// local sinks are what V1-09 itself scopes to ("JSON/text local
	// sinks").
	Text
)

// Identity is the trace identity/correlation fields HE-11-M02/HE-11-M03
// ask every event to carry when available. Every field is optional: a
// Logger call site fills in whatever it actually knows.
type Identity struct {
	WorkItemID    string
	WorkflowRunID string
	NodeRunID     string
	AttemptID     string
	ActorType     string
	ActorID       string
	CorrelationID string
	CausationID   string
}

// MaxFieldValueBytes bounds one field's serialized value (V1-09's own
// "bounded values"): a diagnostic log line exists to be read by a human
// or a log aggregator, never to carry an artifact-sized payload — that is
// exactly what V1-08's ArtifactStore is for.
const MaxFieldValueBytes = 4096

const truncatedSuffix = "...(truncated)"

// Entry is one structured diagnostic log line, after redaction and
// bounding — the shape a Sink actually receives. Callers never construct
// this directly; Logger's Debug/Info/Warn/Error methods build it.
type Entry struct {
	Time     time.Time `json:"time"`
	Level    Level     `json:"level"`
	Message  string    `json:"message"`
	Identity Identity  `json:"identity,omitzero"`
	// FailureCategory is V1-09's "typed failure category": the
	// apperror.Code of the error a Logger.Error call reported, empty for
	// every other level.
	FailureCategory apperror.Code  `json:"failure_category,omitempty"`
	Fields          map[string]any `json:"fields,omitempty"`
}

// Logger writes structured diagnostic entries to one local sink (JSON or
// text), always redacted through matcher first.
type Logger struct {
	mu      sync.Mutex
	w       io.Writer
	format  Format
	matcher redact.Matcher
	clock   clock.Clock
}

// New returns a Logger writing to w in format, redacting every entry
// through matcher (V1-02A) before it is ever serialized.
func New(w io.Writer, format Format, matcher redact.Matcher) *Logger {
	return &Logger{w: w, format: format, matcher: matcher, clock: clock.System{}}
}

// WithClock overrides the Logger's clock (tests inject clock.Fixed so
// entry timestamps are deterministic).
func (l *Logger) WithClock(c clock.Clock) *Logger {
	l.clock = c
	return l
}

// Debug/Info/Warn/Error's message parameter is redacted only as a whole
// value (redact.Matcher's contract is always exact equality, never a
// substring/pattern scan — see internal/app/redact's package doc). A
// message that literally equals a known secret is redacted; a secret
// concatenated into a larger free-text message is not, and by design
// cannot be. Pass a secret as a structured field value, never smash it
// into message text, if it might ever reach this Logger.
func (l *Logger) Debug(identity Identity, message string, fields map[string]any) error {
	return l.log(LevelDebug, identity, message, "", fields)
}

func (l *Logger) Info(identity Identity, message string, fields map[string]any) error {
	return l.log(LevelInfo, identity, message, "", fields)
}

func (l *Logger) Warn(identity Identity, message string, fields map[string]any) error {
	return l.log(LevelWarn, identity, message, "", fields)
}

// Error logs a failure. It deliberately takes a typed apperror.Code and a
// caller-written message rather than an error value — this is V1-09's
// own "error safe/public tách raw private cause": there is no parameter
// this call could pass a raw error's private cause through even by
// accident, since apperror.Error's own cause is unexported and this
// signature has nowhere to put one. A caller holding a plain (non-typed)
// error decides for itself what safe message to write; apperror.CodeOf
// extracts a Code when the error already is (or wraps) an *apperror.Error,
// empty otherwise.
func (l *Logger) Error(identity Identity, code apperror.Code, message string, fields map[string]any) error {
	return l.log(LevelError, identity, message, code, fields)
}

func (l *Logger) log(level Level, identity Identity, message string, category apperror.Code, fields map[string]any) error {
	redactedFields, err := l.redactFields(fields)
	if err != nil {
		return fmt.Errorf("logging: redact fields: %w", err)
	}
	redactedMessage, err := l.matcher.Value(message)
	if err != nil {
		return fmt.Errorf("logging: redact message: %w", err)
	}
	entry := Entry{
		Time:            l.clock.Now(),
		Level:           level,
		Message:         boundString(fmt.Sprint(redactedMessage)),
		Identity:        identity,
		FailureCategory: category,
		Fields:          redactedFields,
	}
	return l.write(entry)
}

func (l *Logger) redactFields(fields map[string]any) (map[string]any, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	result := make(map[string]any, len(fields))
	for key, value := range fields {
		redacted, err := l.matcher.Value(value)
		if err != nil {
			return nil, err
		}
		result[key] = boundValue(redacted)
	}
	return result, nil
}

func (l *Logger) write(entry Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch l.format {
	case JSON:
		return l.writeJSON(entry)
	case Text:
		return l.writeText(entry)
	default:
		return fmt.Errorf("logging: unknown format %d", l.format)
	}
}

func (l *Logger) writeJSON(entry Entry) error {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("logging: encode entry: %w", err)
	}
	encoded = append(encoded, '\n')
	_, err = l.w.Write(encoded)
	return err
}

func (l *Logger) writeText(entry Entry) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s] %s", entry.Time.Format(time.RFC3339Nano), entry.Level, entry.Message)
	if entry.FailureCategory != "" {
		fmt.Fprintf(&b, " category=%s", entry.FailureCategory)
	}
	for _, key := range sortedIdentityKeys(entry.Identity) {
		fmt.Fprintf(&b, " %s=%s", key.name, key.value)
	}
	for _, key := range sortedFieldKeys(entry.Fields) {
		fmt.Fprintf(&b, " %s=%v", key, entry.Fields[key])
	}
	b.WriteByte('\n')
	_, err := l.w.Write([]byte(b.String()))
	return err
}

type identityField struct{ name, value string }

func sortedIdentityKeys(id Identity) []identityField {
	all := []identityField{
		{"work_item_id", id.WorkItemID},
		{"workflow_run_id", id.WorkflowRunID},
		{"node_run_id", id.NodeRunID},
		{"attempt_id", id.AttemptID},
		{"actor_type", id.ActorType},
		{"actor_id", id.ActorID},
		{"correlation_id", id.CorrelationID},
		{"causation_id", id.CausationID},
	}
	present := make([]identityField, 0, len(all))
	for _, f := range all {
		if f.value != "" {
			present = append(present, f)
		}
	}
	return present
}

func sortedFieldKeys(fields map[string]any) []string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// boundValue truncates a string value (or a string found inside a nested
// map/slice redact.Value produced) to MaxFieldValueBytes. Non-string
// scalars pass through unchanged — bounding exists for runaway text, not
// to reject a legitimately large number.
func boundValue(v any) any {
	switch value := v.(type) {
	case string:
		return boundString(value)
	case map[string]any:
		result := make(map[string]any, len(value))
		for k, nested := range value {
			result[k] = boundValue(nested)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for i, nested := range value {
			result[i] = boundValue(nested)
		}
		return result
	default:
		return v
	}
}

func boundString(s string) string {
	if len(s) <= MaxFieldValueBytes {
		return s
	}
	return s[:MaxFieldValueBytes-len(truncatedSuffix)] + truncatedSuffix
}
