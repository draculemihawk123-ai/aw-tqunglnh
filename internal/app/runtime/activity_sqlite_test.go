package runtime_test

// V11-11 (ADR-042): the engine writes its milestones into the task chat as display-only messages, once each, and never into a prompt.

import (
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	domainmessage "github.com/taQuangLing/agent-workflow/internal/domain/message"
)

func (f *rerunSQLite) thread() []domainmessage.Message {
	f.t.Helper()
	var messages []domainmessage.Message
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		var err error
		messages, err = tx.Messages().ListMessagesForWorkItem(f.ctx, f.workItemID)
		return err
	}); err != nil {
		f.t.Fatalf("ListMessagesForWorkItem: %v", err)
	}
	return messages
}

func TestAppendActivity_IsOnceDisplayOnlyAndRedacted(t *testing.T) {
	f := newRerunSQLite(t)
	deps := f.redoDeps()
	request := appmessage.ActivityRequest{
		ProjectID: "project-1", WorkItemID: f.workItemID, NodeRunID: "", AttemptID: "", Key: "start:attempt-1",
		Role: domainmessage.RoleSystem, Actor: "aw", Text: "build started with token hunter2-secret", Matcher: redact.NewMatcher("hunter2-secret"),
	}
	wrote, err := appmessage.AppendActivity(f.ctx, f.uow, deps.ArtifactStore, f.ids, clock.System{}, request)
	if err != nil || !wrote {
		t.Fatalf("AppendActivity = %v, %v; want it written", wrote, err)
	}
	again, err := appmessage.AppendActivity(f.ctx, f.uow, deps.ArtifactStore, f.ids, clock.System{}, request)
	if err != nil || again {
		t.Fatalf("second AppendActivity = %v, %v; want no second message", again, err)
	}
	thread := f.thread()
	if len(thread) != 1 || !thread[0].DisplayOnly || thread[0].Role != domainmessage.RoleSystem || string(thread[0].ID) != appmessage.ActivityMessageID("start:attempt-1") {
		t.Fatalf("thread = %+v, want exactly one display-only SYSTEM message with the key-derived id", thread)
	}
	ref, err := appmessage.ResolveMessageContent(f.ctx, f.uow, "project-1", f.workItemID, string(thread[0].ID))
	if err != nil {
		t.Fatalf("ResolveMessageContent: %v", err)
	}
	reader, err := deps.ArtifactStore.Open(f.ctx, ref)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer reader.Close()
	buf := make([]byte, 256)
	n, _ := reader.Read(buf)
	if strings.Contains(string(buf[:n]), "hunter2-secret") {
		t.Fatalf("the stored text still holds the secret: %q", buf[:n])
	}
	if _, err := appmessage.AppendActivity(f.ctx, f.uow, deps.ArtifactStore, f.ids, clock.System{}, appmessage.ActivityRequest{
		ProjectID: "project-1", WorkItemID: f.workItemID, Key: "k", Role: domainmessage.RoleUser, Text: "x",
	}); err == nil {
		t.Fatal("an activity message must not be written as USER")
	}
}

// The prompt of the next node is the same with and without activity messages: the scheduler leaves them out of the snapshot.
func TestActivityMessages_NeverReachTheContextSnapshot(t *testing.T) {
	f := newRerunSQLite(t)
	deps := f.redoDeps()
	user := f.appendMessage(domainmessage.RoleUser, "please keep the schema frozen", "idem-user")
	if _, err := appmessage.AppendActivity(f.ctx, f.uow, deps.ArtifactStore, f.ids, clock.System{}, appmessage.ActivityRequest{
		ProjectID: "project-1", WorkItemID: f.workItemID, Key: "start:x", Role: domainmessage.RoleSystem, Actor: "aw", Text: "implement started.",
	}); err != nil {
		t.Fatalf("AppendActivity: %v", err)
	}
	started := f.mustStart("idem-start-1")
	attempt := f.runToRunningAttempt(started)

	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		a, err := tx.Runtime().GetExecutionAttempt(f.ctx, attempt.attemptID)
		if err != nil || a.ContextSnapshotID == nil {
			t.Fatalf("attempt = %+v, %v; want a bound snapshot", a, err)
		}
		snapshot, err := tx.ContextSnapshots().GetSnapshot(f.ctx, string(*a.ContextSnapshotID))
		if err != nil {
			return err
		}
		if len(snapshot.MessageRefs) != 1 || snapshot.MessageRefs[0].MessageID != user.MessageID {
			t.Fatalf("snapshot message refs = %+v, want only the person's message %s", snapshot.MessageRefs, user.MessageID)
		}
		return nil
	}); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
}

func TestActivityPoster_AnnouncesStartAndEndOnceEach(t *testing.T) {
	n := failedAgentNodeFixture(t)
	deps := n.f.redoDeps()
	poster := runtime.NewActivityPoster(n.f.uow, deps.ArtifactStore, n.f.ids, clock.System{}, redact.NewMatcher())
	payload := runtime.ExecuteNodeJobPayload{RunID: n.runID, NodeRunID: n.nodeRunID, AttemptID: n.attemptID}

	poster.NodeStarted(n.f.ctx, payload)
	poster.NodeFinished(n.f.ctx, payload)
	poster.NodeStarted(n.f.ctx, payload)
	poster.NodeFinished(n.f.ctx, payload)
	poster.NodeReached(n.f.ctx, n.nodeRunID) // not an approval node: nothing

	thread := n.f.thread()
	if len(thread) != 2 {
		t.Fatalf("thread = %d messages, want a start and an end line (each posted once)", len(thread))
	}
	for _, m := range thread {
		if !m.DisplayOnly || m.NodeRunID != n.nodeRunID || m.AttemptID == nil || string(*m.AttemptID) != n.attemptID {
			t.Fatalf("message %+v must be display-only and name its node run and attempt", m)
		}
	}
	texts := map[string]string{}
	for _, m := range thread {
		ref, err := appmessage.ResolveMessageContent(n.f.ctx, n.f.uow, "project-1", n.f.workItemID, string(m.ID))
		if err != nil {
			t.Fatalf("ResolveMessageContent: %v", err)
		}
		reader, err := deps.ArtifactStore.Open(n.f.ctx, ref)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		buf := make([]byte, 512)
		count, _ := reader.Read(buf)
		reader.Close()
		texts[string(m.ID)] = string(buf[:count])
	}
	start := texts[appmessage.ActivityMessageID("start:"+n.attemptID)]
	end := texts[appmessage.ActivityMessageID("end:"+n.attemptID)]
	if !strings.HasPrefix(start, "implement started") || !strings.Contains(end, "implement failed") || !strings.Contains(end, "Run Again") {
		t.Fatalf("start %q / end %q; want the node named and, for the failure, the plain-words reason and next step", start, end)
	}
}
