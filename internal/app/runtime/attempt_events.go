package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// Reading what an agent attempt did (V11-11, ADR-042). agent_events has always held the stream — what the agent said, each tool call, the
// usage, the diagnostics — written while the attempt runs, but nothing read it by attempt. ListAttemptEvents is that read: one page by
// Sequence, so a client can follow a running attempt by asking again from the last sequence it saw. It only reads; every text is redacted
// again with the process matcher (the sink redacted before storing; a second pass costs nothing and means a later change to the matcher
// still applies) and cut to a per-field bound, with Truncated saying so.

const (
	// DefaultAttemptEventsLimit and MaxAttemptEventsLimit bound one page of ListAttemptEvents.
	DefaultAttemptEventsLimit = 100
	MaxAttemptEventsLimit     = 500

	maxEventMessageBytes = 8 << 10
	maxToolInputBytes    = 2 << 10
	maxToolOutputBytes   = 4 << 10
)

// AttemptToolView is a tool call as the progress pane shows it. Input is the call's argument JSON compacted to one line.
type AttemptToolView struct {
	CallID   string `json:"callId,omitempty"`
	Name     string `json:"name"`
	Input    string `json:"input,omitempty"`
	Output   string `json:"output,omitempty"`
	IsError  bool   `json:"isError,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
}

// AttemptEventView is one agent_events row, redacted and bounded.
type AttemptEventView struct {
	Sequence   uint64                 `json:"sequence"`
	Kind       string                 `json:"kind"`
	At         time.Time              `json:"at"`
	Message    string                 `json:"message,omitempty"`
	Tool       *AttemptToolView       `json:"tool,omitempty"`
	Diagnostic *AttemptDiagnosticView `json:"diagnostic,omitempty"`
	Usage      *UsageView             `json:"usage,omitempty"`
	// Truncated reports that Message, a tool input or a tool output was cut to its bound.
	Truncated bool `json:"truncated,omitempty"`
}

// AttemptDiagnosticView is a DIAGNOSTIC event's code and message.
type AttemptDiagnosticView struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// AttemptEventsPage is ListAttemptEvents' result. NextSequence is the cursor to pass as afterSequence for the next page (the last
// returned Sequence, or the one given when nothing is new). Finished says the attempt is in a terminal state: once a page with
// HasMore=false arrives for a finished attempt, there is nothing more to wait for.
type AttemptEventsPage struct {
	AttemptID    string             `json:"attemptId"`
	NodeRunID    string             `json:"nodeRunId"`
	AttemptState string             `json:"attemptState"`
	Finished     bool               `json:"finished"`
	Events       []AttemptEventView `json:"events"`
	NextSequence uint64             `json:"nextSequence"`
	HasMore      bool               `json:"hasMore"`
}

// attemptTerminal reports whether the attempt can no longer produce events.
func attemptTerminal(state runtimedomain.ExecutionAttemptState) bool {
	switch state {
	case runtimedomain.ExecutionAttemptQueued, runtimedomain.ExecutionAttemptRunning:
		return false
	default:
		return true
	}
}

// ListAttemptEvents returns the events of attemptID after afterSequence, at most limit of them (DefaultAttemptEventsLimit when limit <= 0,
// never more than MaxAttemptEventsLimit). ports.ErrPersistenceNotFound for an unknown attempt.
func ListAttemptEvents(ctx context.Context, uow ports.UnitOfWork, matcher redact.Matcher, attemptID string, afterSequence uint64, limit int) (AttemptEventsPage, error) {
	if limit <= 0 {
		limit = DefaultAttemptEventsLimit
	}
	if limit > MaxAttemptEventsLimit {
		limit = MaxAttemptEventsLimit
	}
	var page AttemptEventsPage
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		// one extra row tells whether another page follows without a second query
		records, err := tx.AgentEvents().ListAfterSequence(ctx, attemptID, afterSequence, limit+1)
		if err != nil {
			return err
		}
		page = AttemptEventsPage{
			AttemptID: attemptID, NodeRunID: string(attempt.NodeRunID), AttemptState: string(attempt.State),
			Finished: attemptTerminal(attempt.State), Events: []AttemptEventView{}, NextSequence: afterSequence,
		}
		if len(records) > limit {
			records = records[:limit]
			page.HasMore = true
		}
		for _, record := range records {
			page.Events = append(page.Events, toAttemptEventView(record, matcher))
			page.NextSequence = record.Sequence
		}
		return nil
	})
	return page, err
}

func toAttemptEventView(record ports.AgentEventRecord, matcher redact.Matcher) AttemptEventView {
	view := AttemptEventView{Sequence: record.Sequence, Kind: record.Kind, At: record.CreatedAt}
	var payload agentevents.Payload
	if err := json.Unmarshal([]byte(record.PayloadJSON), &payload); err != nil {
		// an unreadable payload is shown as an event with no body rather than failing the whole page
		return view
	}
	cut := func(text string, bound int) string {
		text = redactText(matcher, text)
		if len(text) <= bound {
			return text
		}
		view.Truncated = true
		return truncateUTF8(text, bound)
	}
	view.Message = cut(payload.Message, maxEventMessageBytes)
	if payload.Tool != nil {
		view.Tool = &AttemptToolView{
			CallID: payload.Tool.CallID, Name: redactText(matcher, payload.Tool.Name), IsError: payload.Tool.IsError, ExitCode: payload.Tool.ExitCode,
			Input: cut(compactJSON(payload.Tool.Input), maxToolInputBytes), Output: cut(payload.Tool.Output, maxToolOutputBytes),
		}
	}
	if payload.Diagnostic != nil {
		view.Diagnostic = &AttemptDiagnosticView{Code: payload.Diagnostic.Code, Message: cut(payload.Diagnostic.Message, maxEventMessageBytes)}
	}
	if payload.Usage != nil {
		view.Usage = usageView(*payload.Usage)
	}
	return view
}

// compactJSON renders a tool call's arguments on one line; text that is not JSON is returned as it is.
func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buffer bytes.Buffer
	if err := json.Compact(&buffer, raw); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return buffer.String()
}

// truncateUTF8 cuts text to at most bound bytes without splitting a character.
func truncateUTF8(text string, bound int) string {
	if len(text) <= bound {
		return text
	}
	cut := text[:bound]
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	return cut
}

// redactText replaces every occurrence of a known secret inside free text (Matcher.String only matches a whole value).
func redactText(matcher redact.Matcher, text string) string {
	redacted, _ := matcher.Redact([]byte(text))
	return string(redacted)
}
