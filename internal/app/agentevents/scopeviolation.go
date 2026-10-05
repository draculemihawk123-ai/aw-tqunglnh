package agentevents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
)

// This file is V9-09's answer to "an agent that writes outside its
// pathScopes is reported with a code and nothing else": the attempt row keeps
// only the error CODE (SCOPE_VIOLATION), so WHICH paths breached the scope was
// lost the moment the check fired. The violation list is therefore written
// into the attempt's own agent_events stream — the existing, redacted,
// size-bounded, append-only per-attempt record, no migration — as one
// orchestrator-authored DIAGNOSTIC event, and read back by the run timeline
// (internal/app/runtime.GetRunTimeline, the query behind GET
// /runs/{id}/timeline and `aw run timeline`) as the attempt's failureDetail.
//
// Every scope-violation producer shares it: the mid-run checkpoint
// (Sink.captureCheckpointLocked), the end-of-run buildEvidence check, and any
// strict read-only check — they all end in the one classify branch that calls
// RecordScopeViolation, as long as the error they build wraps
// scopeguard.ErrScopeViolation (build it with scopeguard.NewViolationsError to
// get the structured path list; any other wrapping still reports its message,
// truncated).

const (
	// ScopeViolationDiagnosticCode is the AgentDiagnostic.Code of the event
	// RecordScopeViolation writes, and the code ScopeViolationDetail's reader
	// side (ScopeViolationDetailFromRecords) looks for.
	ScopeViolationDiagnosticCode = "SCOPE_VIOLATION"

	// MaxReportedViolations bounds how many violating paths the detail names;
	// the rest are summarized as "and N more".
	MaxReportedViolations = 20

	// maxReportedPathRunes bounds one reported path, so a pathological file
	// name cannot make the detail unbounded in the other dimension.
	maxReportedPathRunes = 200

	// maxViolationFallbackRunes bounds the text reported for an error that
	// wraps scopeguard.ErrScopeViolation without carrying a structured list.
	maxViolationFallbackRunes = 1000

	violationSourceMetadataKey  = "source"
	violationSourceOrchestrator = "orchestrator"
)

// ScopeViolationDetail renders err — a scope violation — as one bounded,
// operator-readable line naming the violating paths:
//
//	2 path(s) outside the granted scope: repo-a:leaked.txt; repo-a:x/y.go
//
// At most MaxReportedViolations paths are named, followed by "and N more".
// An error that wraps scopeguard.ErrScopeViolation without a
// *scopeguard.ViolationsError is reported by its own message, truncated.
func ScopeViolationDetail(err error) string {
	var violations *scopeguard.ViolationsError
	if !errors.As(err, &violations) || len(violations.Violations) == 0 {
		if err == nil {
			return ""
		}
		return truncateRunes(sanitize(err.Error()), maxViolationFallbackRunes)
	}
	total := len(violations.Violations)
	shown := total
	if shown > MaxReportedViolations {
		shown = MaxReportedViolations
	}
	paths := make([]string, 0, shown)
	for _, violation := range violations.Violations[:shown] {
		paths = append(paths, truncateRunes(sanitize(fmt.Sprintf("%s:%s", violation.RepositoryID, violation.Path)), maxReportedPathRunes))
	}
	detail := fmt.Sprintf("%d path(s) outside the granted scope: %s", total, strings.Join(paths, "; "))
	if total > shown {
		detail += fmt.Sprintf("; and %d more", total-shown)
	}
	return detail
}

// sanitize replaces control characters, so a hostile file name cannot forge
// extra lines or terminal escapes in an operator's output.
func sanitize(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, text)
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "…"
}

// RedactDetail replaces every occurrence of a known secret inside text — a
// substring scan, because a violating path embeds a secret as part of a longer
// string, not as a whole value (which is all redact.Matcher.String and
// Matcher.Value, used on structured fields elsewhere, can see).
func RedactDetail(matcher redact.Matcher, text string) string {
	redacted, _ := matcher.Redact([]byte(text))
	return string(redacted)
}

// ScopeViolationRecord is what RecordScopeViolation needs: the attempt whose
// stream receives the event, the fencing proofs that attempt's writer holds
// (the same JobLease/WriteLeases the Sink was built with), and the
// collaborators every Sink write already uses.
type ScopeViolationRecord struct {
	AttemptID   string
	JobLease    ports.JobLease
	WriteLeases []ports.WriteLeaseGrant
	UOW         ports.UnitOfWork
	IDs         idsource.Source
	Clock       clock.Clock
	Matcher     redact.Matcher
}

// RecordScopeViolation appends one DIAGNOSTIC agent event (code
// ScopeViolationDiagnosticCode, message ScopeViolationDetail(cause)) to the
// attempt's stream, after whatever the provider already emitted. It must run
// once the provider call has returned (the Sink no longer assigns sequences),
// fenced against the same job and write leases as every Sink write. It is a
// best-effort annotation of a failure that is already decided: the caller
// classifies the attempt from cause regardless of what this returns.
func RecordScopeViolation(ctx context.Context, record ScopeViolationRecord, cause error) error {
	if record.AttemptID == "" || record.UOW == nil || record.IDs == nil || record.Clock == nil {
		return errors.New("agentevents: RecordScopeViolation requires AttemptID, UOW, IDs and Clock")
	}
	detail := RedactDetail(record.Matcher, ScopeViolationDetail(cause))
	if detail == "" {
		return nil
	}
	return record.UOW.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if err := validateFencing(ctx, tx, record.JobLease, record.WriteLeases, record.AttemptID); err != nil {
			return err
		}
		existing, err := tx.AgentEvents().ListByAttempt(ctx, record.AttemptID)
		if err != nil {
			return err
		}
		var last uint64
		for _, event := range existing {
			if event.Sequence > last {
				last = event.Sequence
			}
		}
		built, err := buildEventRecord(record.Matcher, record.IDs, record.AttemptID, ports.AgentEvent{
			AttemptID: ports.ExecutionAttemptID(record.AttemptID), Sequence: last + 1,
			Kind: ports.AgentEventDiagnostic, ObservedAt: record.Clock.Now(),
			Diagnostic:       &ports.AgentDiagnostic{Code: ScopeViolationDiagnosticCode, Message: detail},
			ProviderMetadata: map[string]string{violationSourceMetadataKey: violationSourceOrchestrator},
		})
		if err != nil {
			return err
		}
		return tx.AgentEvents().AppendBatch(ctx, []ports.AgentEventRecord{built})
	})
}

// ScopeViolationDetailFromRecords is the read side: the message of the LAST
// ScopeViolationDiagnosticCode DIAGNOSTIC event in records (an attempt's
// stream, oldest first), or "" when it has none.
func ScopeViolationDetailFromRecords(records []ports.AgentEventRecord) string {
	return lastDiagnosticMessage(records, ScopeViolationDiagnosticCode)
}

// ProviderFailureDetailFromRecords (V9-20) is the same read for the provider's
// own account of why it failed: the message of the LAST
// ports.ProviderFailureDiagnosticCode DIAGNOSTIC event, e.g. "Claude reported a
// failed result: You've hit your limit · resets 5pm". "" when the attempt has
// none — it did not fail at the provider, or it failed before V9-20 recorded
// the reason.
func ProviderFailureDetailFromRecords(records []ports.AgentEventRecord) string {
	return lastDiagnosticMessage(records, ports.ProviderFailureDiagnosticCode)
}

func lastDiagnosticMessage(records []ports.AgentEventRecord, code string) string {
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Kind != string(ports.AgentEventDiagnostic) {
			continue
		}
		var payload Payload
		if err := json.Unmarshal([]byte(records[i].PayloadJSON), &payload); err != nil || payload.Diagnostic == nil {
			continue
		}
		if payload.Diagnostic.Code == code {
			return payload.Diagnostic.Message
		}
	}
	return ""
}
