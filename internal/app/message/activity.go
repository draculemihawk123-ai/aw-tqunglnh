package message

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/message"
)

// ActivityRequest describes one message the ENGINE writes into a work item's task chat (V11-11, ADR-042): a node started, the agent's
// last words, how a node ended, an approval waiting. Such a message is display-only: the context scheduler never puts it in a prompt, so
// writing them cannot change what any agent is shown.
type ActivityRequest struct {
	ProjectID  string
	WorkItemID string
	// NodeRunID and AttemptID say what the message is about; the attempt may be empty (an approval waiting has none).
	NodeRunID string
	AttemptID string
	// Key makes the write idempotent: the message id is derived from it, so the same key is written at most once however often the engine
	// reaches the same point (a redelivered job, a replayed finalization).
	Key string
	// Role is SYSTEM for the engine's own notices and ASSISTANT for the agent's words.
	Role message.Role
	// Actor names the author: the agent (its profile) or "aw".
	Actor   string
	Text    string
	Matcher redact.Matcher
}

// ActivityMessageID is the id AppendActivity gives the message of key.
func ActivityMessageID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "act-" + hex.EncodeToString(sum[:16])
}

// AppendActivity writes an activity message unless one with the same Key already exists. It reports whether it wrote one. The text is
// redacted before it is stored, like every message. The write is not part of any engine transition and an error from it must never fail
// one: callers log and carry on.
func AppendActivity(ctx context.Context, uow ports.UnitOfWork, store ports.ArtifactStore, ids idsource.Source, clk clock.Clock, req ActivityRequest) (bool, error) {
	if strings.TrimSpace(req.Key) == "" {
		return false, errors.New("message: an activity message needs a Key")
	}
	if req.Role != message.RoleSystem && req.Role != message.RoleAssistant {
		return false, errors.New("message: an activity message is written as SYSTEM or ASSISTANT")
	}
	id := ActivityMessageID(req.Key)
	exists := func(tx ports.Tx) (bool, error) {
		_, err := tx.Messages().GetMessage(ctx, id)
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			return false, nil
		}
		return err == nil, err
	}
	var already bool
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) (err error) { already, err = exists(tx); return }); err != nil {
		return false, err
	}
	if already {
		return false, nil
	}
	text := req.Text
	if redacted, _ := req.Matcher.Redact([]byte(text)); len(redacted) > 0 {
		text = string(redacted)
	}
	append_ := AppendMessageRequest{
		ProjectID: req.ProjectID, WorkItemID: req.WorkItemID, AttemptID: req.AttemptID, Role: req.Role, Content: []byte(text),
		ContentType: "text/plain", Sensitivity: redact.Public, Matcher: req.Matcher,
	}
	prepared, err := PrepareMessage(ctx, store, ids, clk, append_)
	if err != nil {
		return false, err
	}
	wrote := false
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if again, err := exists(tx); err != nil || again {
			return err
		}
		if _, err := tx.Artifacts().InsertArtifact(ctx, prepared); err != nil {
			return err
		}
		cmd := ports.Command{ID: id, Actor: req.Actor, CorrelationID: "activity", RequestedAt: clk.Now()}
		_, err := appendMessageRowTx(ctx, tx, ids, cmd, ports.AppendMessageRequest{
			ID: id, ProjectID: req.ProjectID, WorkItemID: req.WorkItemID, AttemptID: req.AttemptID, Actor: req.Actor, Role: req.Role,
			ContentArtifactID: string(prepared.ID), DisplayOnly: true, NodeRunID: req.NodeRunID,
		})
		wrote = err == nil
		return err
	})
	return wrote, err
}
