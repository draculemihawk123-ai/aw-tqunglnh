package cli_test

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func TestConfirmTableDriven(t *testing.T) {
	tests := []struct {
		name          string
		assumeYes     bool
		interactive   bool
		json          bool
		stdinLine     string
		wantConfirmed bool
		wantErr       error
	}{
		{name: "--yes always wins, even noninteractive+json", assumeYes: true, interactive: false, json: true, wantConfirmed: true},
		{name: "noninteractive without --yes is refused", interactive: false, wantErr: cli.ErrConfirmationRequired},
		{name: "json output without --yes is refused even if interactive", interactive: true, json: true, wantErr: cli.ErrConfirmationRequired},
		{name: "interactive lowercase y confirms", interactive: true, stdinLine: "y\n", wantConfirmed: true},
		{name: "interactive YES confirms case-insensitively", interactive: true, stdinLine: "YES\n", wantConfirmed: true},
		{name: "interactive whitespace-padded yes confirms", interactive: true, stdinLine: "  yes  \n", wantConfirmed: true},
		{name: "interactive blank Enter declines", interactive: true, stdinLine: "\n", wantConfirmed: false},
		{name: "interactive explicit no declines", interactive: true, stdinLine: "n\n", wantConfirmed: false},
		{name: "interactive garbage declines", interactive: true, stdinLine: "sure why not\n", wantConfirmed: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var prompter bytes.Buffer
			stdin := strings.NewReader(tc.stdinLine)
			confirmed, err := cli.Confirm(cli.ConfirmOptions{
				Prompt: "cancel this run?", AssumeYes: tc.assumeYes, Interactive: tc.interactive, JSON: tc.json,
				Stdin: stdin, Prompter: &prompter,
			})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Confirm() error = %v, want %v", err, tc.wantErr)
				}
				if prompter.Len() != 0 {
					t.Fatalf("a prompt was written even though confirmation could never succeed: %q", prompter.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("Confirm() error = %v, want nil", err)
			}
			if confirmed != tc.wantConfirmed {
				t.Fatalf("Confirm() = %v, want %v", confirmed, tc.wantConfirmed)
			}
		})
	}
}

func TestConfirmAssumeYesNeverWritesPromptOrReadsStdin(t *testing.T) {
	var prompter bytes.Buffer
	confirmed, err := cli.Confirm(cli.ConfirmOptions{AssumeYes: true, Prompter: &prompter, Stdin: nil})
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !confirmed {
		t.Fatal("Confirm() = false, want true for --yes")
	}
	if prompter.Len() != 0 {
		t.Fatalf("--yes path wrote a prompt nobody needs to answer: %q", prompter.String())
	}
}

func TestIsTerminalFalseForNonCharDeviceFileAndNil(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-a-tty")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()
	if cli.IsTerminal(f) {
		t.Fatal("IsTerminal(regular file) = true, want false")
	}
	if cli.IsTerminal(nil) {
		t.Fatal("IsTerminal(nil) = true, want false")
	}
}
