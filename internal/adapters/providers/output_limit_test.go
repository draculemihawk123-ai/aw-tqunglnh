package providers_test

// V9-14a: a provider CLI's output past the cap used to be discarded in silence.
//
// The process supervisor bounds an attempt's stdout at 10 MiB by default. A
// provider adapter consumes that stream as it arrives (it is parsed line by line,
// never buffered), so the cap protected no memory — but a long attempt run with
// the CLI's verbose stream could cross it, and everything after was dropped:
// the terminal `result` event with it, and often the last line half-written. The
// attempt then failed as a broken protocol after all its money was spent, a
// diagnosis that points at the CLI instead of at the cap.
//
// The adapters now (1) raise the default cap to jsonl.DefaultOutputLimitBytes and
// make it configurable, and (2) fail an attempt whose output was truncated as
// "output_truncated" with ErrOutputTruncated, never as a protocol error.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	processadapter "github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/codex"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

type outputLimitCase struct {
	name      string
	newWith   func(limit int) (ports.AgentExecutor, error)
	truncated error
	protocol  error
}

func outputLimitCases() []outputLimitCase {
	return []outputLimitCase{
		{
			name: "claude",
			newWith: func(limit int) (ports.AgentExecutor, error) {
				return claude.New(processadapter.NewSupervisor(), claude.Config{
					Executable: os.Args[0], PrefixArgs: helperPrefix("claude"), PermissionMode: "dontAsk",
					VersionEnvironment: map[string]string{"AGENTKIT_PROVIDER_HELPER": "1"}, OutputLimitBytes: limit,
				})
			},
			truncated: claude.ErrOutputTruncated, protocol: claude.ErrProtocol,
		},
		{
			name: "codex",
			newWith: func(limit int) (ports.AgentExecutor, error) {
				return codex.New(processadapter.NewSupervisor(), codex.Config{
					Executable: os.Args[0], PrefixArgs: helperPrefix("codex"),
					VersionEnvironment: map[string]string{"AGENTKIT_PROVIDER_HELPER": "1"}, OutputLimitBytes: limit,
				})
			},
			truncated: codex.ErrOutputTruncated, protocol: codex.ErrProtocol,
		},
	}
}

func TestProviderAdapters_OutputPastTheCapIsATruncationNotAProtocolError(t *testing.T) {
	t.Parallel()
	for _, tc := range outputLimitCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// The fake CLI's whole stream is a few hundred bytes; 100 cuts it in the
			// first events, mid-line, like a real long attempt crossing a real cap.
			executor, err := tc.newWith(100)
			if err != nil {
				t.Fatal(err)
			}
			capture := filepath.Join(t.TempDir(), "capture.json")
			request := helperRequest("limit-"+tc.name, t.TempDir(), capture, "success")
			result, err := executor.Start(context.Background(), request, &eventCollector{})
			if !errors.Is(err, tc.truncated) {
				t.Fatalf("Start error = %v, want ErrOutputTruncated", err)
			}
			if errors.Is(err, tc.protocol) {
				t.Fatalf("a truncated stream was reported as a protocol error: %v", err)
			}
			if !strings.Contains(err.Error(), "100 bytes") {
				t.Fatalf("error %q does not name the configured limit", err)
			}
			if result.Status != ports.AgentExecutionFailed || result.TerminationReason != "output_truncated" {
				t.Fatalf("result = %s/%q, want FAILED/output_truncated", result.Status, result.TerminationReason)
			}
		})
	}
}

func TestProviderAdapters_OutputWithinTheCapSucceeds(t *testing.T) {
	t.Parallel()
	for _, tc := range outputLimitCases() {
		tc := tc
		// 0 is the default (256 MiB); the second is an explicit, generous limit.
		for _, limit := range []int{0, 1 << 20} {
			limit := limit
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				executor, err := tc.newWith(limit)
				if err != nil {
					t.Fatal(err)
				}
				capture := filepath.Join(t.TempDir(), "capture.json")
				request := helperRequest("within-"+tc.name, t.TempDir(), capture, "success")
				result, err := executor.Start(context.Background(), request, &eventCollector{})
				if err != nil || result.Status != ports.AgentExecutionSucceeded {
					t.Fatalf("limit %d: result = %s, err = %v, want a normal success", limit, result.Status, err)
				}
			})
		}
	}
}

func TestProviderAdapters_RefuseANegativeOutputLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range outputLimitCases() {
		if _, err := tc.newWith(-1); err == nil {
			t.Errorf("%s: a negative output limit was accepted", tc.name)
		}
	}
}
