package runtime_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
)

// V12-26 (ADR-059): the prompt read for the Prompt tab is exactly the prompt the attempt is given, with known secrets redacted.
func TestReadAttemptPrompt_IsTheAssembledPromptRedacted(t *testing.T) {
	uow, _, store, runID, nodeRunID, attemptID := assembleRequestFixture(t)
	ctx := context.Background()
	assembled, err := runtime.AssembleAgentExecutionRequest(ctx, uow, store, runtime.AssembleAgentExecutionRequestRequest{RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID})
	if err != nil {
		t.Fatalf("AssembleAgentExecutionRequest: %v", err)
	}

	read, err := runtime.ReadAttemptPrompt(ctx, uow, store, redact.NewMatcher(), "project-1", attemptID)
	if err != nil {
		t.Fatalf("ReadAttemptPrompt: %v", err)
	}
	if read.Prompt != assembled.Prompt || read.NodeRunID != nodeRunID || read.RunID != runID || read.Bytes != len(assembled.Prompt) || read.Truncated {
		t.Fatalf("read = %+v, want the assembled prompt of %s", read, nodeRunID)
	}

	redacted, err := runtime.ReadAttemptPrompt(ctx, uow, store, redact.NewMatcher("always follow this"), "project-1", attemptID)
	if err != nil || strings.Contains(redacted.Prompt, "always follow this") {
		t.Fatalf("redacted prompt still holds the secret (err %v)", err)
	}

	if _, err := runtime.ReadAttemptPrompt(ctx, uow, store, redact.NewMatcher(), "other-project", attemptID); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("another project's read: err = %v, want not found", err)
	}
}
