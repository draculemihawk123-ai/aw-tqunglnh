package runtime_test

// V12-18 (ADR-053): a message aimed at one node reaches only that node's attempts; a next-attempt-only message reaches one attempt.

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	domainmessage "github.com/taQuangLing/agent-workflow/internal/domain/message"
)

func (f *rerunSQLite) appendAimed(text, key, node string, nextOnly bool) string {
	f.t.Helper()
	result, err := appmessage.AppendMessage(f.ctx, f.uow, f.redoDeps().ArtifactStore, f.ids, clock.System{}, redoCommand(key, "AppendMessage"), appmessage.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: f.workItemID, Role: domainmessage.RoleUser, Content: []byte(text), ContentType: "text/plain",
		Sensitivity: redact.Public, TargetNodeKey: node, NextAttemptOnly: nextOnly,
	})
	if err != nil {
		f.t.Fatalf("AppendMessage: %v", err)
	}
	return result.MessageID
}

func TestAimedMessages_ReachOnlyTheirNodeAndTheNextAttemptOnce(t *testing.T) {
	f := newRerunSQLite(t)
	everyone := f.appendAimed("keep the schema frozen", "idem-all", "", false)
	forImplement := f.appendAimed("use the existing helper", "idem-impl", "implement", false)
	forRepro := f.appendAimed("do not change product code", "idem-repro", "repro", false)
	once := f.appendAimed("this attempt only: skip the migration", "idem-once", "", true)

	attempt := f.runToRunningAttempt(f.mustStart("idem-start-1"))

	var refs []string
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		snapshot, err := tx.ContextSnapshots().GetSnapshotByAttemptID(f.ctx, attempt.attemptID)
		if err != nil {
			return err
		}
		for _, r := range snapshot.MessageRefs {
			refs = append(refs, r.MessageID)
		}
		return nil
	}); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	want := []string{everyone, forImplement, once}
	if len(refs) != len(want) {
		t.Fatalf("snapshot message refs = %v, want %v (not the message for repro %s)", refs, want, forRepro)
	}
	for i := range want {
		if refs[i] != want[i] {
			t.Fatalf("snapshot message refs = %v, want %v", refs, want)
		}
	}

	// The next attempt of any node no longer gets the next-attempt-only message, and the chat says it was delivered.
	messages, delivered, err := appmessage.ListMessagesWithDelivery(f.ctx, f.uow, f.workItemID)
	if err != nil {
		t.Fatalf("ListMessagesWithDelivery: %v", err)
	}
	if !delivered[once] {
		t.Fatalf("delivered = %v, want %s delivered", delivered, once)
	}
	for _, m := range appmessage.ForAttempt(messages, "implement", delivered) {
		if string(m.ID) == once {
			t.Fatal("a next-attempt-only message must reach one attempt only")
		}
	}
	if got := appmessage.ForAttempt(messages, "repro", delivered); len(got) != 2 || string(got[1].ID) != forRepro {
		t.Fatalf("messages for repro = %+v, want the message for every node and the one for repro", got)
	}
}
