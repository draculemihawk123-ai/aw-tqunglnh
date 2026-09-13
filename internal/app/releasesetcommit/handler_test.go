package releasesetcommit

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

func TestHandler_ImplementsWorkerpoolHandler(t *testing.T) {
	var _ workerpool.Handler = (*Handler)(nil)
}

func TestHandler_Handle_CreatesRealCommit(t *testing.T) {
	fx := newExecuteFixture(t)
	writeTestFile(t, filepath.Join(fx.workspacePath, "service.txt"), "changed\n")
	result := fx.request(t, "req-1", "hash-1", "record repository result")
	job := fx.claim(t, "worker-1", 10*time.Minute)

	handler := NewHandler(fx.deps())
	if err := handler.Handle(fx.ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)
	if intent.State != "COMMITTED" {
		t.Fatalf("state = %s, want COMMITTED", intent.State)
	}
	if !strings.Contains(fx.headMessage(t), markerTrailer(intent.Marker)) {
		t.Fatal("HEAD commit message is missing this operation's own marker trailer")
	}
}
