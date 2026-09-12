// Package message is V5-02's application-command layer
// (docs/design/07-v5-execution-evidence.md), mirroring the domain package
// it orchestrates, internal/domain/message — the same "app package named
// after the domain package it orchestrates" convention internal/app/work
// follows for internal/domain/work.
//
// AppendMessage is the one public command this package exposes (plus the
// plain read ListMessages below): it composes internal/app/artifact's
// PrepareAttachment (Put+Verify, OUTSIDE any transaction) with a single
// ports.UnitOfWork transaction that inserts BOTH the resulting artifacts
// row and the messages row atomically, appends this new Message's own
// MessageAppended domain event, and records the command's idempotency
// receipt — the identical shape internal/app/work.CreateRootWorkItem
// already establishes for every other idempotent mutation in this
// codebase (receipt-check-first, then execute, then receipt-record-last,
// all inside one WithSerializedWrite call).
//
// Canonical Message content ALWAYS gets RetentionCanonicalContext (ADR-017:
// canonical Message content never gets a blanket TTL) — this is a policy
// decision baked into this command, not a caller-supplied choice.
package message

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	appartifact "github.com/taQuangLing/agent-workflow/internal/app/artifact"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/message"
)

// MaxContentSize bounds a single message's own content body — task chat is
// bounded, typed content, not a streaming sink (unlike V5-05's raw process
// output, which needs true streaming with its own separate bounded-sink
// discipline). 1 MiB matches config.ProcessOutputLimit's own default
// magnitude; nothing in the spec pins a tighter number.
const MaxContentSize = 1 << 20

// ErrContentTooLarge is returned by AppendMessage when Content exceeds
// MaxContentSize.
var ErrContentTooLarge = fmt.Errorf("message: content exceeds max size of %d bytes", MaxContentSize)

// AppendMessageRequest is what a caller supplies to AppendMessage.
type AppendMessageRequest struct {
	ProjectID  string
	WorkItemID string
	AttemptID  string // empty = no attempt linkage
	Role       message.Role
	Content    []byte
	// ContentType is a free-form media type — no allowlist yet (only a
	// non-empty check, enforced by artifact.NewArtifact). A stricter
	// media-type policy is deferred to whichever later task first needs
	// one against a real caller, the same "don't add a field/check no
	// contract test needs yet" discipline 00-roadmap.md §3 states.
	ContentType string
	Sensitivity redact.Sensitivity
	// Matcher optionally redacts any EXACT match against a known secret
	// value (redact.Matcher's own documented "exact equality, never a
	// pattern/regex" contract — it does not scan for a secret embedded
	// inside free-form prose, confirmed by this repo's own V1-09 checklist
	// note). The zero value is safe and performs no exact-value matching,
	// leaving only Sensitivity's own structural redaction in effect.
	Matcher redact.Matcher
}

// AppendMessageResult is what AppendMessage returns (and what a replayed
// command receipt reconstructs).
type AppendMessageResult struct {
	MessageID         string `json:"messageId"`
	ProjectID         string `json:"projectId"`
	WorkItemID        string `json:"workItemId"`
	Sequence          uint64 `json:"sequence"`
	ContentArtifactID string `json:"contentArtifactId"`
}

// AppendMessage validates and redacts req.Content, durably attaches it via
// internal/app/artifact.PrepareAttachment (OUTSIDE any transaction — see
// that function's own doc comment for why), then atomically inserts the
// resulting artifact row, the Message row and its own domain event inside
// one ports.UnitOfWork transaction. It follows V1-06's idempotent-command
// shape exactly like internal/app/work.CreateRootWorkItem: a retry with
// the same IdempotencyKey and RequestHash replays the first call's result
// without creating any duplicate artifact/message row; the same key with a
// different RequestHash is rejected as ports.ErrReceiptConflict.
func AppendMessage(
	ctx context.Context,
	uow ports.UnitOfWork,
	store ports.ArtifactStore,
	ids idsource.Source,
	clk clock.Clock,
	cmd ports.Command,
	req AppendMessageRequest,
) (AppendMessageResult, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return AppendMessageResult{}, errors.New("message: ProjectID is required")
	}
	if strings.TrimSpace(req.WorkItemID) == "" {
		return AppendMessageResult{}, errors.New("message: WorkItemID is required")
	}
	if !req.Role.Valid() {
		return AppendMessageResult{}, fmt.Errorf("message: unknown Role %q", req.Role)
	}
	if len(req.Content) == 0 {
		return AppendMessageResult{}, errors.New("message: Content is required")
	}
	if len(req.Content) > MaxContentSize {
		return AppendMessageResult{}, ErrContentTooLarge
	}
	if strings.TrimSpace(req.ContentType) == "" {
		return AppendMessageResult{}, errors.New("message: ContentType is required")
	}
	// Validated BEFORE any redaction/Put: an out-of-range Sensitivity
	// would otherwise fall through redact.Matcher.Tagged's own default
	// (unredacted passthrough) and only be caught later by
	// artifact.NewArtifact INSIDE the transaction — by which point Put has
	// already written the unredacted bytes durably to disk, with no
	// Delete method on ArtifactStore to ever remove them again.
	if req.Sensitivity < redact.Public || req.Sensitivity > redact.Secret {
		return AppendMessageResult{}, fmt.Errorf("message: unknown Sensitivity %d", req.Sensitivity)
	}

	// Pre-check the idempotency receipt BEFORE any ArtifactStore I/O (audit
	// finding, 2026-09-08): a pure replay or a genuine RequestHash conflict
	// must be identified before Put ever runs, not just accepted as "wasted
	// I/O" — a caller retrying after a transient failure with DIFFERENT
	// content should never see that new content silently discarded only
	// after already being written to disk. This is a read-only pre-check;
	// loadOrValidateReceipt runs again unchanged inside the write
	// transaction below to close the TOCTOU race between the two (a
	// concurrent caller could record the receipt in between) — the
	// identical two-phase "read-only pre-check, then re-verify inside a
	// serialized-write transaction" pattern internal/app/runtime's own
	// admission probe already established.
	if preResult, replayed, err := loadOrValidateReceipt(ctx, uow, cmd); err != nil {
		return AppendMessageResult{}, err
	} else if replayed {
		return preResult, nil
	}

	// Redact BEFORE Put — the same "redact first, then bound" ordering
	// V1-09's own checklist establishes: content must be in its final,
	// redacted form before anything durable is ever written from it.
	redactedContent := req.Matcher.Tagged(req.Sensitivity, string(req.Content))

	prepared, err := appartifact.PrepareAttachment(ctx, store, ids, clk, appartifact.PrepareAttachmentRequest{
		ProjectID:      req.ProjectID,
		Body:           strings.NewReader(redactedContent),
		ContentType:    req.ContentType,
		Sensitivity:    req.Sensitivity,
		Redacted:       true,
		RetentionClass: artifact.RetentionCanonicalContext,
	})
	if err != nil {
		return AppendMessageResult{}, fmt.Errorf("message: prepare content attachment: %w", err)
	}

	var result AppendMessageResult
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if replayedResult, found, err := loadOrValidateReceiptTx(ctx, tx, cmd); err != nil {
			return err
		} else if found {
			result = replayedResult
			return nil
		}

		if _, err := tx.Artifacts().InsertArtifact(ctx, prepared); err != nil {
			return err
		}

		messageID := ids.NewID()
		m, err := tx.Messages().AppendMessage(ctx, ports.AppendMessageRequest{
			ID: messageID, ProjectID: req.ProjectID, WorkItemID: req.WorkItemID, AttemptID: req.AttemptID,
			Actor: cmd.Actor, Role: req.Role, ContentArtifactID: string(prepared.ID),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		})
		if err != nil {
			return err
		}

		eventPayload, err := json.Marshal(messageAppendedEventPayload{
			WorkItemID: req.WorkItemID, Role: string(m.Role),
			Sequence: m.Sequence, ContentArtifactID: string(m.ContentArtifactID),
		})
		if err != nil {
			return fmt.Errorf("marshal MessageAppended payload: %w", err)
		}
		// AggregateType/AggregateID is this brand-new Message's OWN
		// identity, not the WorkItem's — Sequence=1 is safe because this
		// is that Message's very first and only event, ever (messages are
		// immutable/append-only, so no future command ever appends a
		// second event for the same MessageID). Scoping to the WorkItem's
		// own aggregate stream instead would require a real MAX(sequence)
		// query against that stream (which already accumulates events
		// from unrelated commands) — unnecessary complexity this
		// dedicated per-message stream avoids entirely, the same
		// per-entity-stream discipline WorkItemBlocker/ScopeExpansionOrigin
		// already use for their own domain events.
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-appended", ProjectID: req.ProjectID,
			AggregateType: "Message", AggregateID: messageID, Sequence: 1,
			EventType: MessageAppendedEventType, SchemaVersion: MessageAppendedSchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = AppendMessageResult{
			MessageID: string(m.ID), ProjectID: req.ProjectID, WorkItemID: req.WorkItemID,
			Sequence: m.Sequence, ContentArtifactID: string(m.ContentArtifactID),
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal receipt result: %w", err)
		}
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
			CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON),
			CreatedAt: cmd.RequestedAt,
		})
	})
	return result, err
}

// loadOrValidateReceipt is AppendMessage's own read-only pre-check, run
// BEFORE any ArtifactStore I/O — see AppendMessage's own call site doc
// comment for why. found=true means cmd was already processed (result is
// the replayed AppendMessageResult); a receipt with a different
// RequestHash for the same key is ports.ErrReceiptConflict; no receipt at
// all is (zero value, false, nil).
func loadOrValidateReceipt(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command) (AppendMessageResult, bool, error) {
	var result AppendMessageResult
	var found bool
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		result, found, err = loadOrValidateReceiptTx(ctx, tx, cmd)
		return err
	})
	return result, found, err
}

// loadOrValidateReceiptTx is loadOrValidateReceipt's own Tx-scoped body —
// shared verbatim by both the read-only pre-check above and the
// serialized-write transaction's own re-check (AppendMessage), so the two
// can never drift out of sync with each other.
func loadOrValidateReceiptTx(ctx context.Context, tx ports.Tx, cmd ports.Command) (AppendMessageResult, bool, error) {
	existingReceipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
	if err != nil {
		return AppendMessageResult{}, false, err
	}
	if !found {
		return AppendMessageResult{}, false, nil
	}
	if existingReceipt.RequestHash != cmd.RequestHash {
		return AppendMessageResult{}, false, ports.ErrReceiptConflict
	}
	var result AppendMessageResult
	if err := json.Unmarshal([]byte(existingReceipt.ResultJSON), &result); err != nil {
		return AppendMessageResult{}, false, err
	}
	return result, true, nil
}

// ListMessages returns every Message for workItemID, ordered by Sequence —
// the full, canonical task chat (this task's own Done-when bar: a reload
// reads this, never a provider's own session transcript). A plain read
// needs no ports.Command envelope — it can never conflict or replay.
func ListMessages(ctx context.Context, uow ports.UnitOfWork, workItemID string) ([]message.Message, error) {
	var result []message.Message
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		result, err = tx.Messages().ListMessagesForWorkItem(ctx, workItemID)
		return err
	})
	return result, err
}
