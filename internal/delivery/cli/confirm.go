package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// IsTerminal reports whether f is a real interactive terminal — the
// standard, dependency-free Go heuristic (a character device, not a pipe/
// redirect/regular file/nil). There is no existing precedent for this
// check elsewhere in this codebase (this task's own design decision);
// every call site in this framework goes through this one function so a
// future swap to a different heuristic has exactly one place to change.
// It deliberately returns a plain bool rather than being wired directly
// into Confirm/ConfirmOptions: Confirm takes Interactive as a plain field
// instead, so a table-driven test can set it directly without needing a
// real *os.File that is or is not a TTY (there is no portable way to
// fabricate one in a unit test).
func IsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// ErrConfirmationRequired is returned by Confirm when a high-impact
// command needs a human's explicit yes and cannot get one: noninteractive
// stdin/stdout, or JSON output requested (V6-15B's own "High-impact
// prompts only TTY; noninteractive/JSON require --yes" line).
var ErrConfirmationRequired = errors.New("cli: high-impact command requires confirmation (re-run with --yes)")

// ConfirmOptions configures Confirm. Interactive is caller-resolved
// (IsTerminal(os.Stdin) && IsTerminal(os.Stdout) in production, or
// injected directly in a table-driven test — the seam that lets a test
// control TTY state without a real terminal). Prompter is where the
// prompt text itself is written — always the process' own stderr in real
// use, exactly like Diagnosticf's own writer, never the JSON stdout
// stream a --json caller is parsing.
type ConfirmOptions struct {
	Prompt      string
	AssumeYes   bool
	Interactive bool
	JSON        bool
	Stdin       io.Reader
	Prompter    io.Writer
}

// Confirm implements V6-15B's own "High-impact prompts only TTY;
// noninteractive/JSON require --yes" rule. AssumeYes (--yes) always wins
// and never reads Stdin or writes Prompter. Otherwise: JSON output or a
// non-interactive session can never complete a real prompt, so both
// return ErrConfirmationRequired immediately, without writing a prompt no
// one can answer. A real interactive session writes Prompt to Prompter
// and reads one line from Stdin, treating exactly "y" or "yes"
// (case-insensitive, surrounding whitespace trimmed) as confirmation —
// anything else, including a plain Enter, is a decline, returned as
// (false, nil): the caller decides for itself whether "declined" should
// exit non-zero, never this function.
func Confirm(opts ConfirmOptions) (bool, error) {
	if opts.AssumeYes {
		return true, nil
	}
	if opts.JSON || !opts.Interactive {
		return false, ErrConfirmationRequired
	}
	fmt.Fprintf(opts.Prompter, "%s [y/N]: ", opts.Prompt)
	reader := bufio.NewReader(opts.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("cli: read confirmation: %w", err)
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes", nil
}
